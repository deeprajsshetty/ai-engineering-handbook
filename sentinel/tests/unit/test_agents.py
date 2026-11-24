"""The LangGraph investigation pipeline, end to end, against fake telemetry."""

from __future__ import annotations

import time

from sentinel.agents.base import AgentDeps
from sentinel.agents.graph import build_graph, run_investigation
from sentinel.config import Settings
from sentinel.domain.topology import DependencyGraph
from sentinel.infrastructure.catalog import ActionCatalog, SignalQueries
from sentinel.infrastructure.discovery import DiscoveryUnavailable
from sentinel.knowledge.retriever import RunbookRetriever
from sentinel.llm.providers import DeterministicProvider, FallbackProvider, LLMError, LLMRequest
from tests.fakes import (
    FakeDiscovery,
    FakeTelemetry,
    Scenario,
    inventory_latency,
    payments_db_missing,
    payments_failure,
)

GRAPH = DependencyGraph(
    [
        ("frontend", "checkout"),
        ("frontend", "inventory"),
        ("checkout", "payments"),
        ("checkout", "inventory"),
    ]
)
INCIDENT = {
    "id": 1,
    "title": "HighErrorRate on frontend",
    "status": "investigating",
    "severity": "critical",
    "services": ["frontend", "checkout"],
    "started_at": "now",
    "alerts": [
        {
            "alertname": "HighErrorRate",
            "service": "frontend",
            "severity": "critical",
            "status": "firing",
            "starts_at": "now",
            "summary": "",
        },
        {
            "alertname": "HighErrorRate",
            "service": "checkout",
            "severity": "critical",
            "status": "firing",
            "starts_at": "now",
            "summary": "",
        },
    ],
}
SCOPE = ["frontend", "checkout", "inventory", "payments"]


def deps(scenario: Scenario, llm=None, discovery=None, **settings_kw) -> AgentDeps:  # type: ignore[no-untyped-def]
    s = Settings(env="test", **settings_kw)
    return AgentDeps(
        settings=s,
        telemetry=FakeTelemetry(scenario),
        llm=llm or FallbackProvider(DeterministicProvider()),
        retriever=RunbookRetriever.from_directory(),
        actions=ActionCatalog.load(s.actions_file),
        queries=SignalQueries.load(s.signals_file),
        graph=GRAPH,
        discovery=discovery or FakeDiscovery(enabled=False),
    )


def window() -> dict[str, float]:
    end = time.time()
    return {"start": end - 1800, "split": end - 300, "end": end}


def test_payments_failure_identifies_payments_and_recommends_flag_disable() -> None:
    result = run_investigation(deps(payments_failure()), INCIDENT, window(), SCOPE)
    assert result["evidence_ok"]
    top = result["hypotheses"][0]
    assert top["service"] == "payments" and top["cause"] == "error_rate"
    assert top["confidence"] >= 0.5
    # payments never alerted: the correlation agent should flag the silent root cause.
    assert "payments" in result["correlation"]["silent_anomalies"]
    assert result["correlation"]["propagation"][0] == "payments"
    recs = result["recommendations"]
    assert recs[0]["action"] == "disable_fault_flag" and recs[0]["service"] == "payments"
    assert all(r["requires_approval"] for r in recs)
    assert [s["agent"] for s in result["steps"]] == [
        "telemetry_analysis",
        "infrastructure_context",
        "anomaly_detection",
        "incident_correlation",
        "root_cause_analysis",
        "remediation_recommendation",
        "reporting",
    ]
    assert all(s["ok"] for s in result["steps"])
    for section in (
        "## Root cause analysis",
        "## Anomalies",
        "## Recommended remediation",
        "payments",
    ):
        assert section in result["report"]
    assert {u["task"] for u in result["llm_usage"]} == {"rca_narrative", "incident_summary"}


def test_latency_incident_ranks_slow_dependency() -> None:
    result = run_investigation(deps(inventory_latency()), INCIDENT, window(), SCOPE)
    top = result["hypotheses"][0]
    assert top["service"] == "inventory" and top["cause"] == "latency_p95"
    assert result["recommendations"][0]["action"] == "disable_fault_flag"


def test_all_backends_down_degrades_gracefully() -> None:
    sc = Scenario(down={"prometheus", "loki", "tempo"})
    result = run_investigation(deps(sc), INCIDENT, window(), SCOPE)
    assert not result["evidence_ok"]
    assert result["hypotheses"] == []
    # Conditional edge skipped straight to reporting.
    assert [s["agent"] for s in result["steps"]] == [
        "telemetry_analysis",
        "infrastructure_context",
        "reporting",
    ]
    assert len(result["data_gaps"]) == 3
    assert "## Data gaps" in result["report"]


def test_partial_backends_still_produce_rca() -> None:
    sc = payments_failure()
    sc.down = {"tempo", "loki"}
    result = run_investigation(deps(sc), INCIDENT, window(), SCOPE)
    assert result["hypotheses"][0]["service"] == "payments"
    assert any("traces unavailable" in g for g in result["data_gaps"])


def test_llm_outage_falls_back_without_failing() -> None:
    class Broken:
        name = "broken"

        def generate(self, req: LLMRequest):  # type: ignore[no-untyped-def]
            raise LLMError("down")

    result = run_investigation(
        deps(payments_failure(), llm=FallbackProvider(Broken())), INCIDENT, window(), SCOPE
    )
    assert all(u["fallback"] for u in result["llm_usage"])
    assert result["narrative"]


def test_failing_agent_is_isolated(monkeypatch) -> None:  # type: ignore[no-untyped-def]
    from sentinel.agents import graph as graph_mod

    def boom(state, deps):  # type: ignore[no-untyped-def]
        raise RuntimeError("kaboom")

    monkeypatch.setitem(graph_mod.AGENTS, "incident_correlation", boom)
    result = graph_mod.serialize(
        build_graph(deps(payments_failure())).invoke(
            {
                "incident": INCIDENT,
                "window": window(),
                "scope": SCOPE,
                "data_gaps": [],
                "steps": [],
                "llm_usage": [],
            }
        )
    )
    failed = [s for s in result["steps"] if not s["ok"]]
    assert [s["agent"] for s in failed] == ["incident_correlation"]
    assert result["hypotheses"][0]["service"] == "payments"  # pipeline continued


def test_auto_approve_low_risk_opt_in() -> None:
    result = run_investigation(
        deps(payments_failure(), auto_approve_low_risk=True), INCIDENT, window(), SCOPE
    )
    flag = next(r for r in result["recommendations"] if r["action"] == "disable_fault_flag")
    assert flag["requires_approval"] is False


def test_infrastructure_context_is_reported_without_changing_ranking() -> None:
    baseline = run_investigation(deps(payments_failure()), INCIDENT, window(), SCOPE)
    disco = FakeDiscovery(payments_db_missing())
    result = run_investigation(deps(payments_failure(), discovery=disco), INCIDENT, window(), SCOPE)
    assert disco.calls == SCOPE  # every service in scope was looked up
    assert [h["service"] for h in result["hypotheses"]] == [
        h["service"] for h in baseline["hypotheses"]
    ]  # context only: ranking unchanged
    pay = next(e for e in result["infrastructure"] if e["service"] == "payments")
    assert {n["type"] for n in pay["neighbours"]} == {"k8s.pod", "database.instance"}
    assert "## Infrastructure context" in result["report"]
    assert "database.instance payments-db is missing" in result["report"]
    assert "Infrastructure context: database.instance payments-db" in result["narrative"]
    step = next(s for s in result["steps"] if s["agent"] == "infrastructure_context")
    assert step["ok"] and "1 issue" in step["summary"]


def test_infrastructure_disabled_is_skipped_silently() -> None:
    result = run_investigation(deps(payments_failure()), INCIDENT, window(), SCOPE)
    step = next(s for s in result["steps"] if s["agent"] == "infrastructure_context")
    assert "not configured" in step["summary"]
    assert result["infrastructure"] == []
    assert not any("infrastructure" in g for g in result["data_gaps"])
    assert "## Infrastructure context" not in result["report"]


def test_discovery_outage_is_a_data_gap_not_a_failure() -> None:
    disco = FakeDiscovery(fail=DiscoveryUnavailable("connection refused"))
    result = run_investigation(deps(payments_failure(), discovery=disco), INCIDENT, window(), SCOPE)
    assert disco.calls == ["frontend"]  # stops after the first outage, no hammering
    assert any("infrastructure context unavailable" in g for g in result["data_gaps"])
    assert result["hypotheses"][0]["service"] == "payments"
