"""Telemetry Analysis Agent.

Responsibility: gather the evidence. For every service in scope it pulls the golden signals
(request rate, error rate, p95 latency) from Prometheus, error logs from Loki, and samples
error/slow traces from Tempo. Tool failures are recorded as data gaps, never raised.
"""

from __future__ import annotations

import re
from collections import Counter
from typing import Any

from sentinel.agents.base import AgentDeps, InvestigationState
from sentinel.domain import traces as trace_analysis
from sentinel.domain.models import LogEvidence, Signal, SpanData
from sentinel.infrastructure.catalog import SignalQueries
from sentinel.infrastructure.telemetry import BackendUnavailable, TelemetryError

_NOISE = re.compile(r"\b[0-9a-f]{8,}\b|\d+(\.\d+)?")
MAX_TRACES = 12


def normalize_log(line: str) -> str:
    """Collapse ids and numbers so identical errors group together."""
    return _NOISE.sub("#", line.strip())[:160]


def run(state: InvestigationState, deps: AgentDeps) -> dict[str, Any]:
    win = state["window"]
    scope = state["scope"]
    gaps: list[str] = []
    down: set[str] = set()

    signals: list[Signal] = []
    for svc in scope:
        for kind, tmpl in deps.queries.metrics.items():
            if "prometheus" in down:
                break
            try:
                series = deps.telemetry.query_range(
                    SignalQueries.render(tmpl, svc),
                    win["start"],
                    win["end"],
                    deps.settings.investigation_step_seconds,
                )
            except BackendUnavailable as exc:
                down.add("prometheus")
                gaps.append(f"metrics unavailable: {exc}")
                break
            except TelemetryError as exc:
                gaps.append(f"metrics query failed for {svc}/{kind.value}: {exc}")
                continue
            points = series[0].points if series else []
            signals.append(Signal(service=svc, kind=kind, points=points))

    logs: list[LogEvidence] = []
    for svc in scope:
        if "loki" in down:
            break
        try:
            lines = deps.telemetry.query_logs(
                SignalQueries.render(deps.queries.log_errors, svc),
                win["split"] - 60,
                win["end"],
                500,
            )
        except BackendUnavailable as exc:
            down.add("loki")
            gaps.append(f"logs unavailable: {exc}")
            break
        except TelemetryError as exc:
            gaps.append(f"log query failed for {svc}: {exc}")
            continue
        if lines:
            top = Counter(normalize_log(entry.line) for entry in lines).most_common(3)
            logs.append(LogEvidence(service=svc, error_count=len(lines), top_messages=top))

    trace_ids: list[str] = []
    for q in (deps.queries.trace_errors, deps.queries.trace_slow):
        if "tempo" in down:
            break
        try:
            for tid in deps.telemetry.search_traces(q, win["split"] - 60, win["end"], MAX_TRACES):
                if tid not in trace_ids:
                    trace_ids.append(tid)
        except BackendUnavailable as exc:
            down.add("tempo")
            gaps.append(f"traces unavailable: {exc}")
        except TelemetryError as exc:
            gaps.append(f"trace search failed: {exc}")
    sampled: list[list[SpanData]] = []
    for tid in trace_ids[:MAX_TRACES]:
        try:
            sampled.append(deps.telemetry.get_trace(tid))
        except TelemetryError as exc:
            gaps.append(f"trace fetch failed for {tid}: {exc}")
    trace_ev = trace_analysis.summarize(sampled)

    with_data = sum(1 for s in signals if s.points)
    evidence_ok = with_data > 0 or bool(logs) or trace_ev.traces_analyzed > 0
    return {
        "signals": signals,
        "logs": logs,
        "traces": trace_ev,
        "evidence_ok": evidence_ok,
        "data_gaps": gaps,
        "_summary": (
            f"{with_data}/{len(signals)} series with data across {len(scope)} services, "
            f"{sum(e.error_count for e in logs)} error logs, {trace_ev.traces_analyzed} traces"
        ),
    }
