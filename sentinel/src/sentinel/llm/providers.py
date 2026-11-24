"""LLM providers behind one small interface.

Design choices (see docs/adr/0004-llm-provider-abstraction.md):

* Plain HTTP via ``httpx`` instead of vendor SDKs: fewer dependencies, uniform timeout/retry
  handling, and no SDK version drift.
* The LLM only writes *narrative* (explanations, summaries). Ranking, correlation and the
  choice of executable actions are deterministic code, so a hallucination can never trigger
  an action.
* ``deterministic`` is a real provider, not a stub: it renders the same structured evidence
  into prose with templates, so the platform is fully functional offline and in CI.
* ``FallbackProvider`` wraps a remote provider and falls back to deterministic on any error.
"""

from __future__ import annotations

import json
import logging
import time
from dataclasses import dataclass
from typing import Any, Protocol

import httpx
from tenacity import Retrying, retry_if_exception, stop_after_attempt, wait_exponential

from sentinel.config import Settings
from sentinel.observability.metrics import LLM_REQUESTS, LLM_TOKENS

log = logging.getLogger(__name__)


class LLMError(RuntimeError):
    pass


@dataclass
class LLMRequest:
    task: str  # "rca_narrative" | "incident_summary"
    system: str
    prompt: str
    context: dict[str, Any]  # structured evidence (used by the deterministic provider)
    max_tokens: int = 700


@dataclass
class LLMResponse:
    text: str
    provider: str
    model: str
    input_tokens: int = 0
    output_tokens: int = 0
    latency_ms: float = 0.0
    fallback: bool = False


class LLMProvider(Protocol):
    name: str

    def generate(self, req: LLMRequest) -> LLMResponse: ...


# ---------------------------------------------------------------------------------------------
# Deterministic (template) provider
# ---------------------------------------------------------------------------------------------


class DeterministicProvider:
    name = "deterministic"

    def generate(self, req: LLMRequest) -> LLMResponse:
        ctx = req.context
        if req.task == "rca_narrative":
            text = self._rca(ctx)
        elif req.task == "incident_summary":
            text = self._summary(ctx)
        else:
            text = json.dumps(ctx, default=str)[:2000]
        return LLMResponse(text=text, provider=self.name, model="templates-v1")

    @staticmethod
    def _rca(ctx: dict[str, Any]) -> str:
        top = ctx.get("top_hypothesis")
        if not top:
            return (
                "No root cause could be established: no anomalous signals or error traces were "
                "found in the investigation window. Check whether telemetry is flowing."
            )
        chain = ctx.get("propagation", [])
        parts = [
            f"The most likely root cause is **{top['service']}** ({top['cause']}), "
            f"confidence {top['confidence']:.0%}."
        ]
        if top.get("evidence"):
            parts.append("Evidence: " + "; ".join(top["evidence"]) + ".")
        if len(chain) > 1:
            parts.append(
                "The failure appears to propagate " + " -> ".join(reversed(chain)) + " (caller "
                "to origin), which explains why upstream services alerted as well."
            )
        infra = ctx.get("infrastructure_issues") or []
        if infra:
            parts.append("Infrastructure context: " + "; ".join(infra[:3]) + ".")
        runner = ctx.get("runner_up")
        if runner:
            parts.append(
                f"Alternative hypothesis: {runner['service']} ({runner['cause']}, "
                f"confidence {runner['confidence']:.0%})."
            )
        return " ".join(parts)

    @staticmethod
    def _summary(ctx: dict[str, Any]) -> str:
        services = ", ".join(ctx.get("services", [])) or "unknown services"
        rc = ctx.get("root_cause")
        actions = ctx.get("recommended_actions", [])
        line = (
            f"{ctx.get('severity', 'unknown').upper()} incident affecting {services}, "
            f"triggered by {ctx.get('alert_count', 0)} alert(s)."
        )
        if rc:
            line += (
                f" Root cause: {rc['service']} ({rc['cause']}, {rc['confidence']:.0%} confidence)."
            )
        else:
            line += " Root cause not yet determined."
        if actions:
            line += " Recommended next step: " + actions[0] + " (awaiting human approval)."
        return line


# ---------------------------------------------------------------------------------------------
# Remote providers
# ---------------------------------------------------------------------------------------------


def _retryable(exc: BaseException) -> bool:
    if isinstance(exc, httpx.TransportError):
        return True
    return isinstance(exc, httpx.HTTPStatusError) and exc.response.status_code in (
        429,
        500,
        502,
        503,
        504,
        529,
    )


class _RemoteProvider:
    name = "remote"
    default_model = ""

    def __init__(self, settings: Settings, client: httpx.Client | None = None) -> None:
        self.settings = settings
        self.model = settings.llm_model or self.default_model
        self.client = client or httpx.Client(timeout=settings.llm_timeout_s)

    def _post(self, url: str, headers: dict[str, str], body: dict[str, Any]) -> dict[str, Any]:
        def go() -> dict[str, Any]:
            r = self.client.post(url, headers=headers, json=body)
            r.raise_for_status()
            return dict(r.json())

        try:
            for attempt in Retrying(
                stop=stop_after_attempt(3),
                wait=wait_exponential(multiplier=0.5, max=4),
                retry=retry_if_exception(_retryable),
                reraise=True,
            ):
                with attempt:
                    return go()
        except (httpx.HTTPError, ValueError) as exc:
            raise LLMError(f"{self.name} request failed: {exc}") from exc
        raise LLMError("unreachable")  # pragma: no cover


class OpenAIProvider(_RemoteProvider):
    name = "openai"
    default_model = "gpt-4o-mini"

    def generate(self, req: LLMRequest) -> LLMResponse:
        key = self.settings.openai_api_key.get_secret_value()
        if not key:
            raise LLMError("SENTINEL_OPENAI_API_KEY is not set")
        t0 = time.perf_counter()
        data = self._post(
            f"{self.settings.openai_base_url.rstrip('/')}/chat/completions",
            {"Authorization": f"Bearer {key}"},
            {
                "model": self.model,
                "max_tokens": req.max_tokens,
                "temperature": 0.2,
                "messages": [
                    {"role": "system", "content": req.system},
                    {"role": "user", "content": req.prompt},
                ],
            },
        )
        try:
            text = data["choices"][0]["message"]["content"] or ""
        except (KeyError, IndexError, TypeError) as exc:
            raise LLMError(f"unexpected OpenAI response shape: {exc}") from exc
        usage = data.get("usage", {})
        return LLMResponse(
            text=text.strip(),
            provider=self.name,
            model=self.model,
            input_tokens=int(usage.get("prompt_tokens", 0)),
            output_tokens=int(usage.get("completion_tokens", 0)),
            latency_ms=(time.perf_counter() - t0) * 1000,
        )


class AnthropicProvider(_RemoteProvider):
    name = "anthropic"
    default_model = "claude-sonnet-5-5"

    def generate(self, req: LLMRequest) -> LLMResponse:
        key = self.settings.anthropic_api_key.get_secret_value()
        if not key:
            raise LLMError("SENTINEL_ANTHROPIC_API_KEY is not set")
        t0 = time.perf_counter()
        data = self._post(
            f"{self.settings.anthropic_base_url.rstrip('/')}/v1/messages",
            {"x-api-key": key, "anthropic-version": "2023-06-01"},
            {
                "model": self.model,
                "max_tokens": req.max_tokens,
                "system": req.system,
                "messages": [{"role": "user", "content": req.prompt}],
            },
        )
        try:
            text = "".join(b.get("text", "") for b in data["content"] if b.get("type") == "text")
        except (KeyError, TypeError) as exc:
            raise LLMError(f"unexpected Anthropic response shape: {exc}") from exc
        usage = data.get("usage", {})
        return LLMResponse(
            text=text.strip(),
            provider=self.name,
            model=self.model,
            input_tokens=int(usage.get("input_tokens", 0)),
            output_tokens=int(usage.get("output_tokens", 0)),
            latency_ms=(time.perf_counter() - t0) * 1000,
        )


class FallbackProvider:
    """Try the primary provider; on failure use the deterministic provider. Records metrics."""

    def __init__(self, primary: LLMProvider, fallback: LLMProvider | None = None) -> None:
        self.primary = primary
        self.fallback = fallback or DeterministicProvider()
        self.name = primary.name

    def generate(self, req: LLMRequest) -> LLMResponse:
        try:
            resp = self.primary.generate(req)
            if not resp.text:
                raise LLMError("empty completion")
            LLM_REQUESTS.labels(self.primary.name, "ok").inc()
            LLM_TOKENS.labels(self.primary.name, "input").inc(resp.input_tokens)
            LLM_TOKENS.labels(self.primary.name, "output").inc(resp.output_tokens)
            return resp
        except LLMError as exc:
            LLM_REQUESTS.labels(self.primary.name, "fallback").inc()
            log.warning("LLM provider failed, using deterministic fallback: %s", exc)
            resp = self.fallback.generate(req)
            resp.fallback = True
            return resp


def build_provider(settings: Settings, client: httpx.Client | None = None) -> LLMProvider:
    if settings.llm_provider == "openai":
        return FallbackProvider(OpenAIProvider(settings, client))
    if settings.llm_provider == "anthropic":
        return FallbackProvider(AnthropicProvider(settings, client))
    return FallbackProvider(DeterministicProvider())
