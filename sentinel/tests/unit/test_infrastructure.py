"""Telemetry clients, catalogs, LLM providers, retriever, security primitives."""

from __future__ import annotations

import base64
import json
from pathlib import Path

import httpx
import pytest

from sentinel.api.middleware import TokenBucket
from sentinel.api.security import hash_key, parse_api_keys
from sentinel.config import Settings
from sentinel.domain.models import Role, SignalKind
from sentinel.infrastructure.actions import ActionExecutor
from sentinel.infrastructure.catalog import ActionCatalog, SignalQueries, load_services
from sentinel.infrastructure.telemetry import (
    BackendUnavailable,
    HttpTelemetryBackend,
    TelemetryError,
    parse_otlp_trace,
)
from sentinel.knowledge.retriever import RunbookRetriever
from sentinel.llm.providers import (
    AnthropicProvider,
    DeterministicProvider,
    FallbackProvider,
    LLMError,
    LLMRequest,
    OpenAIProvider,
    build_provider,
)


def backend(handler, **kw) -> HttpTelemetryBackend:  # type: ignore[no-untyped-def]
    s = Settings(
        env="test",
        prometheus_url="http://prom",
        loki_url="http://loki",
        tempo_url="http://tempo",
        telemetry_retries=2,
        **kw,
    )
    return HttpTelemetryBackend(s, client=httpx.Client(transport=httpx.MockTransport(handler)))


class TestTelemetryClients:
    def test_prometheus_range_parsing_skips_nan(self) -> None:
        def h(req: httpx.Request) -> httpx.Response:
            assert req.url.path == "/api/v1/query_range"
            return httpx.Response(
                200,
                json={
                    "status": "success",
                    "data": {
                        "result": [
                            {
                                "metric": {"service": "a"},
                                "values": [[1, "0.5"], [2, "NaN"], [3, "1"]],
                            }
                        ]
                    },
                },
            )

        res = backend(h).query_range("up", 0, 10, 5)
        assert [p.value for p in res[0].points] == [0.5, 1.0]

    def test_retries_on_5xx_then_succeeds(self) -> None:
        calls = {"n": 0}

        def h(req: httpx.Request) -> httpx.Response:
            calls["n"] += 1
            if calls["n"] < 3:
                return httpx.Response(503)
            return httpx.Response(200, json={"data": {"result": []}})

        assert backend(h).query_instant("up") == []
        assert calls["n"] == 3

    def test_4xx_is_not_retried(self) -> None:
        calls = {"n": 0}

        def h(req: httpx.Request) -> httpx.Response:
            calls["n"] += 1
            return httpx.Response(400, json={"error": "bad query"})

        with pytest.raises(TelemetryError):
            backend(h).query_instant("bad(")
        assert calls["n"] == 1

    def test_disabled_backend(self) -> None:
        b = HttpTelemetryBackend(Settings(env="test"))
        with pytest.raises(BackendUnavailable):
            b.query_logs("{}", 0, 1, 10)

    def test_loki_and_tempo(self) -> None:
        def h(req: httpx.Request) -> httpx.Response:
            if req.url.path.startswith("/loki"):
                return httpx.Response(
                    200,
                    json={
                        "data": {
                            "result": [
                                {"stream": {"service_name": "a"}, "values": [["123", "boom"]]}
                            ]
                        }
                    },
                )
            if req.url.path == "/api/search":
                return httpx.Response(200, json={"traces": [{"traceID": "abc"}, {}]})
            return httpx.Response(200, json={"batches": []})

        b = backend(h)
        assert b.query_logs("{}", 0, 1, 10)[0].line == "boom"
        assert b.search_traces("{}", 0, 1, 5) == ["abc"]
        assert b.get_trace("abc") == []

    def test_parse_otlp_trace_normalises_base64_ids(self) -> None:
        sid = base64.b64encode(bytes.fromhex("0102030405060708")).decode()
        pid = base64.b64encode(bytes.fromhex("1112131415161718")).decode()
        data = {
            "batches": [
                {
                    "resource": {
                        "attributes": [
                            {"key": "service.name", "value": {"stringValue": "payments"}}
                        ]
                    },
                    "scopeSpans": [
                        {
                            "spans": [
                                {
                                    "spanId": sid,
                                    "parentSpanId": pid,
                                    "name": "charge",
                                    "startTimeUnixNano": "1",
                                    "endTimeUnixNano": "5",
                                    "status": {"code": "STATUS_CODE_ERROR"},
                                }
                            ]
                        }
                    ],
                }
            ]
        }
        (span,) = parse_otlp_trace("t", data)
        assert span.span_id == "0102030405060708"
        assert span.parent_id == "1112131415161718"
        assert span.is_error and span.service == "payments" and span.duration_ns == 4


class TestCatalogs:
    def test_default_catalogs_load(self) -> None:
        s = Settings(env="test")
        actions = ActionCatalog.load(s.actions_file)
        assert actions.get("disable_fault_flag").executor == "http"  # type: ignore[union-attr]
        assert {a.name for a in actions.for_signal(SignalKind.ERROR_RATE)} >= {"disable_fault_flag"}
        assert {sv.name for sv in load_services(s.services_file)} == {
            "frontend",
            "checkout",
            "payments",
            "inventory",
        }
        q = SignalQueries.load(s.signals_file)
        assert 'service="payments"' in SignalQueries.render(
            q.metrics[SignalKind.ERROR_RATE], "payments"
        )

    def test_render_rejects_injection(self) -> None:
        with pytest.raises(ValueError, match="unsafe"):
            SignalQueries.render('{service="$service"}', 'x"} or vector(1) #')

    def test_invalid_action_catalog(self, tmp_path: Path) -> None:
        p = tmp_path / "a.yaml"
        p.write_text("actions:\n  bad:\n    executor: http\n")
        with pytest.raises(ValueError, match="requires url"):
            ActionCatalog.load(p)
        p.write_text("actions:\n  bad:\n    executor: ssh\n")
        with pytest.raises(ValueError, match="unsupported executor"):
            ActionCatalog.load(p)


class TestActionExecutor:
    def spec(self):  # type: ignore[no-untyped-def]
        return ActionCatalog.load(Settings(env="test").actions_file).get("disable_fault_flag")

    def test_calls_allow_listed_url_with_token(self) -> None:
        seen = []

        def h(req: httpx.Request) -> httpx.Response:
            seen.append(req)
            return httpx.Response(200, json={"ok": True})

        ex = ActionExecutor(
            Settings(env="test", action_token="tok"),
            client=httpx.Client(transport=httpx.MockTransport(h)),
        )
        res = ex.execute(self.spec(), "payments", {"payments"})
        assert res.ok and seen[0].url == "http://payments:8080/admin/faults/clear"
        assert seen[0].headers["X-Admin-Token"] == "tok"

    def test_refuses_unknown_service(self) -> None:
        ex = ActionExecutor(Settings(env="test"))
        res = ex.execute(self.spec(), "evil.example.com", {"payments"})
        assert not res.ok and "not in catalog" in res.detail["error"]

    def test_network_error_is_reported(self) -> None:
        def h(req: httpx.Request) -> httpx.Response:
            raise httpx.ConnectError("refused")

        ex = ActionExecutor(
            Settings(env="test"), client=httpx.Client(transport=httpx.MockTransport(h))
        )
        res = ex.execute(self.spec(), "payments", {"payments"})
        assert not res.ok and "ConnectError" in res.detail["error"]


REQ = LLMRequest(
    task="incident_summary",
    system="s",
    prompt="p",
    context={
        "severity": "critical",
        "services": ["a"],
        "alert_count": 2,
        "root_cause": {"service": "a", "cause": "error_rate", "confidence": 0.8},
        "recommended_actions": ["disable_fault_flag on a"],
    },
)


class TestLLMProviders:
    def test_deterministic_summary_and_rca(self) -> None:
        text = DeterministicProvider().generate(REQ).text
        assert "CRITICAL" in text and "80%" in text and "approval" in text
        rca = (
            DeterministicProvider()
            .generate(
                LLMRequest(
                    task="rca_narrative", system="", prompt="", context={"top_hypothesis": None}
                )
            )
            .text
        )
        assert "No root cause" in rca

    def test_openai_request_shape(self) -> None:
        def h(req: httpx.Request) -> httpx.Response:
            body = json.loads(req.content)
            assert req.headers["Authorization"] == "Bearer sk-test"
            assert body["messages"][0]["role"] == "system"
            return httpx.Response(
                200,
                json={
                    "choices": [{"message": {"content": "ok"}}],
                    "usage": {"prompt_tokens": 10, "completion_tokens": 2},
                },
            )

        p = OpenAIProvider(
            Settings(env="test", openai_api_key="sk-test"),
            client=httpx.Client(transport=httpx.MockTransport(h)),
        )
        r = p.generate(REQ)
        assert (r.text, r.input_tokens, r.output_tokens) == ("ok", 10, 2)

    def test_anthropic_request_shape(self) -> None:
        def h(req: httpx.Request) -> httpx.Response:
            assert req.url.path == "/v1/messages"
            assert req.headers["x-api-key"] == "k" and "anthropic-version" in req.headers
            return httpx.Response(
                200,
                json={
                    "content": [{"type": "text", "text": "hi"}],
                    "usage": {"input_tokens": 3, "output_tokens": 1},
                },
            )

        p = AnthropicProvider(
            Settings(env="test", anthropic_api_key="k"),
            client=httpx.Client(transport=httpx.MockTransport(h)),
        )
        assert p.generate(REQ).text == "hi"

    def test_missing_key_falls_back(self) -> None:
        p = build_provider(Settings(env="test", llm_provider="openai"))
        r = p.generate(REQ)
        assert r.fallback and r.provider == "deterministic"

    def test_server_error_falls_back_after_retries(self) -> None:
        calls = {"n": 0}

        def h(req: httpx.Request) -> httpx.Response:
            calls["n"] += 1
            return httpx.Response(503)

        primary = OpenAIProvider(
            Settings(env="test", openai_api_key="k"),
            client=httpx.Client(transport=httpx.MockTransport(h)),
        )
        r = FallbackProvider(primary).generate(REQ)
        assert r.fallback and calls["n"] == 3

    def test_bad_shape_raises(self) -> None:
        p = OpenAIProvider(
            Settings(env="test", openai_api_key="k"),
            client=httpx.Client(
                transport=httpx.MockTransport(lambda r: httpx.Response(200, json={"x": 1}))
            ),
        )
        with pytest.raises(LLMError):
            p.generate(REQ)


class TestRetriever:
    def test_finds_relevant_runbook(self) -> None:
        r = RunbookRetriever.from_directory()
        assert r.search("error_rate 5xx payments")[0].runbook.id == "high-error-rate"
        assert r.search("latency_p95 slow")[0].runbook.id == "high-latency"
        assert r.search("zzz-unknown-term") == []


class TestSecurity:
    def test_parse_keys(self) -> None:
        (e,) = parse_api_keys(f"ops:operator:{hash_key('k')}")
        assert e.role is Role.OPERATOR and e.digest == hash_key("k")
        with pytest.raises(ValueError, match="name:role"):
            parse_api_keys("broken")

    def test_role_hierarchy(self) -> None:
        assert Role.ADMIN.rank > Role.OPERATOR.rank > Role.VIEWER.rank

    def test_token_bucket(self) -> None:
        tb = TokenBucket(rate_per_minute=60, burst=2)
        assert tb.take("a") == 0 and tb.take("a") == 0
        assert tb.take("a") > 0
        assert tb.take("b") == 0

    def test_prod_settings_validation(self) -> None:
        with pytest.raises(ValueError, match="SENTINEL_API_KEYS"):
            Settings(env="prod")
        ok = Settings(
            env="prod",
            api_keys=f"a:admin:{hash_key('x')}",
            webhook_token="w" * 32,
            database_url="postgresql+psycopg://u:p@h/db",
        )
        assert ok.env == "prod"
