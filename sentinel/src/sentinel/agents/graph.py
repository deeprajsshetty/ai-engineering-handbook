"""LangGraph orchestration of the investigation agents.

    START -> telemetry_analysis -> infrastructure_context
               --(evidence?)--> anomaly_detection -> incident_correlation
                                -> root_cause_analysis -> remediation -> reporting -> END
               --(no evidence)-------------------------------------------> reporting -> END

The graph is a supervisor-less pipeline with one conditional edge: deterministic routing keeps
the flow predictable and auditable. Human-in-the-loop happens *after* the graph, on persisted
remediation proposals, so approvals survive restarts and can take hours (ADR-0003).
"""

from __future__ import annotations

from typing import Any

from langgraph.graph import END, START, StateGraph

from sentinel.agents import infrastructure_agent, telemetry_agent
from sentinel.agents.analysis_agents import anomaly_agent, correlation_agent, rca_agent
from sentinel.agents.base import AgentDeps, InvestigationState, instrumented
from sentinel.agents.response_agents import remediation_agent, reporting_agent

AGENTS = {
    "telemetry_analysis": telemetry_agent.run,
    "infrastructure_context": infrastructure_agent.run,
    "anomaly_detection": anomaly_agent,
    "incident_correlation": correlation_agent,
    "root_cause_analysis": rca_agent,
    "remediation_recommendation": remediation_agent,
    "reporting": reporting_agent,
}


def _route_after_telemetry(state: InvestigationState) -> str:
    return "anomaly_detection" if state.get("evidence_ok") else "reporting"


def build_graph(deps: AgentDeps) -> Any:
    g = StateGraph(InvestigationState)
    for name, fn in AGENTS.items():
        g.add_node(name, instrumented(name, fn, deps))  # type: ignore[call-overload]
    g.add_edge(START, "telemetry_analysis")
    g.add_edge("telemetry_analysis", "infrastructure_context")
    g.add_conditional_edges(
        "infrastructure_context",
        _route_after_telemetry,
        {"anomaly_detection": "anomaly_detection", "reporting": "reporting"},
    )
    g.add_edge("anomaly_detection", "incident_correlation")
    g.add_edge("incident_correlation", "root_cause_analysis")
    g.add_edge("root_cause_analysis", "remediation_recommendation")
    g.add_edge("remediation_recommendation", "reporting")
    g.add_edge("reporting", END)
    return g.compile()


def run_investigation(
    deps: AgentDeps, incident: dict[str, Any], window: dict[str, float], scope: list[str]
) -> dict[str, Any]:
    """Run the graph and return a JSON-serialisable result."""
    graph = build_graph(deps)
    final: InvestigationState = graph.invoke(
        {
            "incident": incident,
            "window": window,
            "scope": scope,
            "data_gaps": [],
            "steps": [],
            "llm_usage": [],
        }
    )
    return serialize(final)


def serialize(state: InvestigationState) -> dict[str, Any]:
    def dump(items: Any) -> Any:
        return [i.model_dump(mode="json") for i in items or []]

    traces = state.get("traces")
    return {
        "window": state.get("window"),
        "scope": state.get("scope"),
        "evidence_ok": state.get("evidence_ok", False),
        "anomalies": dump(state.get("anomalies")),
        "correlation": state.get("correlation", {}),
        "hypotheses": dump(state.get("hypotheses")),
        "narrative": state.get("narrative", ""),
        "recommendations": dump(state.get("recommendations")),
        "summary": state.get("summary", ""),
        "report": state.get("report", ""),
        "logs": dump(state.get("logs")),
        "traces": traces.model_dump(mode="json") if traces else None,
        "infrastructure": dump(state.get("infrastructure")),
        "signals_with_data": sum(1 for s in state.get("signals", []) if s.points),
        "data_gaps": state.get("data_gaps", []),
        "steps": dump(state.get("steps")),
        "llm_usage": state.get("llm_usage", []),
    }
