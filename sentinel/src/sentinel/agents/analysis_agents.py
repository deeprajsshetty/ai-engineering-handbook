"""Anomaly Detection, Incident Correlation and Root Cause Analysis agents."""

from __future__ import annotations

from typing import Any

from sentinel.agents.base import AgentDeps, InvestigationState
from sentinel.domain import anomaly as anomaly_detection
from sentinel.domain.correlation import propagation_chain
from sentinel.domain.models import TraceEvidence
from sentinel.domain.rca import rank_root_causes
from sentinel.llm import prompts
from sentinel.llm.providers import LLMRequest


def anomaly_agent(state: InvestigationState, deps: AgentDeps) -> dict[str, Any]:
    """Anomaly Detection Agent: robust z-score (MAD) per signal, static fallback on cold start."""
    found = anomaly_detection.detect_all(
        state.get("signals", []), state["window"]["split"], deps.settings.anomaly_z_threshold
    )
    services = sorted({a.service for a in found})
    return {
        "anomalies": found,
        "_summary": f"{len(found)} anomalies on {len(services)} services: {', '.join(services)}"
        if found
        else "no anomalies detected",
    }


def correlation_agent(state: InvestigationState, deps: AgentDeps) -> dict[str, Any]:
    """Incident Correlation Agent: map anomalies onto the dependency graph and the alerts."""
    anomalies = state.get("anomalies", [])
    chain = propagation_chain(anomalies, deps.graph)
    alerted = {a["service"] for a in state["incident"].get("alerts", [])}
    anomalous = {a.service for a in anomalies}
    correlation: dict[str, Any] = {
        "propagation": chain.ordered_services,
        "anomalous_edges": chain.edges,
        "blast_radius": chain.blast_radius,
        "alerted_services": sorted(alerted),
        "silent_anomalies": sorted(anomalous - alerted),  # anomalous but never alerted
        "alerts_without_anomaly": sorted(alerted - anomalous),
    }
    msg = (
        f"propagation {' <- '.join(chain.ordered_services)}"
        if chain.ordered_services
        else "no propagation path"
    )
    if correlation["silent_anomalies"]:
        msg += f"; silent anomalies on {', '.join(correlation['silent_anomalies'])}"
    return {"correlation": correlation, "_summary": msg}


def rca_agent(state: InvestigationState, deps: AgentDeps) -> dict[str, Any]:
    """Root Cause Analysis Agent: deterministic ranking + LLM-written explanation."""
    hypotheses = rank_root_causes(
        state.get("anomalies", []),
        deps.graph,
        state.get("logs", []),
        state.get("traces") or TraceEvidence(),
        candidates=state["incident"].get("services", []),
    )
    context = {
        "incident": {k: state["incident"].get(k) for k in ("title", "severity", "services")},
        "top_hypothesis": hypotheses[0].model_dump() if hypotheses else None,
        "runner_up": hypotheses[1].model_dump() if len(hypotheses) > 1 else None,
        "propagation": state.get("correlation", {}).get("propagation", []),
        "anomalies": [a.model_dump() for a in state.get("anomalies", [])][:10],
        "top_error_logs": [e.model_dump() for e in state.get("logs", [])][:5],
        "infrastructure_issues": [i for ev in state.get("infrastructure", []) for i in ev.issues][
            :10
        ],
    }
    resp = deps.llm.generate(
        LLMRequest(
            task="rca_narrative",
            system=prompts.RCA_SYSTEM,
            prompt=prompts.render("rca_narrative", context),
            context=context,
            max_tokens=deps.settings.llm_max_tokens,
        )
    )
    top = hypotheses[0] if hypotheses else None
    return {
        "hypotheses": hypotheses,
        "narrative": resp.text,
        "llm_usage": [_usage("rca_narrative", resp)],
        "_summary": f"root cause {top.service} ({top.cause}, {top.confidence:.0%})"
        if top
        else "no root cause identified",
    }


def _usage(task: str, resp: Any) -> dict[str, Any]:
    return {
        "task": task,
        "provider": resp.provider,
        "model": resp.model,
        "input_tokens": resp.input_tokens,
        "output_tokens": resp.output_tokens,
        "latency_ms": round(resp.latency_ms, 1),
        "fallback": resp.fallback,
        "prompt_version": prompts.PROMPT_VERSION,
    }
