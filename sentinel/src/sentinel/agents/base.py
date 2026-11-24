"""Shared agent plumbing: dependencies, graph state, and node instrumentation."""

from __future__ import annotations

import logging
import operator
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Annotated, Any, TypedDict

from opentelemetry import trace

from sentinel.config import Settings
from sentinel.domain.models import (
    AgentStep,
    Anomaly,
    Hypothesis,
    InfraEvidence,
    LogEvidence,
    Recommendation,
    Signal,
    TraceEvidence,
)
from sentinel.domain.topology import DependencyGraph
from sentinel.infrastructure.catalog import ActionCatalog, SignalQueries
from sentinel.infrastructure.discovery import DiscoverySource
from sentinel.infrastructure.telemetry import TelemetryBackend
from sentinel.knowledge.retriever import RunbookRetriever
from sentinel.llm.providers import LLMProvider
from sentinel.observability.metrics import AGENT_ERRORS, AGENT_STEP_DURATION

log = logging.getLogger(__name__)
tracer = trace.get_tracer("sentinel.agents")


@dataclass
class AgentDeps:
    settings: Settings
    telemetry: TelemetryBackend
    llm: LLMProvider
    retriever: RunbookRetriever
    actions: ActionCatalog
    queries: SignalQueries
    graph: DependencyGraph
    discovery: DiscoverySource


class InvestigationState(TypedDict, total=False):
    # Inputs
    incident: dict[str, Any]
    window: dict[str, float]  # start, split, end (unix seconds)
    scope: list[str]
    # Produced by agents
    signals: list[Signal]
    logs: list[LogEvidence]
    traces: TraceEvidence
    evidence_ok: bool
    infrastructure: list[InfraEvidence]
    anomalies: list[Anomaly]
    correlation: dict[str, Any]
    hypotheses: list[Hypothesis]
    narrative: str
    recommendations: list[Recommendation]
    summary: str
    report: str
    # Append-only (reducers merge partial updates from every node)
    data_gaps: Annotated[list[str], operator.add]
    steps: Annotated[list[AgentStep], operator.add]
    llm_usage: Annotated[list[dict[str, Any]], operator.add]


NodeFn = Callable[[InvestigationState, AgentDeps], dict[str, Any]]


def instrumented(name: str, fn: NodeFn, deps: AgentDeps) -> Callable[[InvestigationState], dict]:
    """Wrap an agent node with tracing, metrics, timing and failure isolation.

    A failing agent never aborts the investigation: it records a data gap and the pipeline
    continues with whatever evidence the other agents produced (graceful degradation).
    """

    def node(state: InvestigationState) -> dict[str, Any]:
        started = time.time()
        t0 = time.perf_counter()
        with tracer.start_as_current_span(f"agent.{name}") as span:
            span.set_attribute("sentinel.incident_id", state.get("incident", {}).get("id", 0))
            try:
                update = fn(state, deps)
                ok = True
                summary = str(update.pop("_summary", "ok"))
            except Exception as exc:
                log.exception("agent %s failed", name)
                AGENT_ERRORS.labels(name).inc()
                span.record_exception(exc)
                update = {"data_gaps": [f"{name} agent failed: {type(exc).__name__}: {exc}"]}
                ok = False
                summary = f"failed: {type(exc).__name__}"
            elapsed = time.perf_counter() - t0
            AGENT_STEP_DURATION.labels(name).observe(elapsed)
            span.set_attribute("sentinel.agent.ok", ok)
        step = AgentStep(
            agent=name,
            started_at=started,
            duration_ms=round(elapsed * 1000, 1),
            summary=summary,
            ok=ok,
        )
        update["steps"] = [*update.get("steps", []), step]
        return update

    return node
