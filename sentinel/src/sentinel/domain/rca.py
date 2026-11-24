"""Root-cause scoring.

Each candidate service gets a weighted score built from independent evidence types, so a single
noisy signal cannot dominate. The weights are explicit and documented (see
docs/agents/agent-specifications.md) so operators can reason about *why* a service was ranked.

=========================  ======  ===========================================================
Component                  Weight  Meaning
=========================  ======  ===========================================================
anomaly                    0.30    Strongest anomaly severity on this service (0..1)
topology                   0.25    1.0 if none of its own dependencies are anomalous
traces                     0.20    Share of error-originating spans / self-time in traces
earliness                  0.15    Earlier anomaly onset ranks higher
logs                       0.10    Share of error logs emitted by this service
=========================  ======  ===========================================================
"""

from __future__ import annotations

from collections.abc import Sequence

from sentinel.domain.models import (
    Anomaly,
    Hypothesis,
    LogEvidence,
    SignalKind,
    TraceEvidence,
)
from sentinel.domain.topology import DependencyGraph

WEIGHTS = {"anomaly": 0.30, "topology": 0.25, "traces": 0.20, "earliness": 0.15, "logs": 0.10}


def _share(values: dict[str, float], key: str) -> float:
    total = sum(values.values())
    return values.get(key, 0.0) / total if total > 0 else 0.0


def rank_root_causes(
    anomalies: Sequence[Anomaly],
    graph: DependencyGraph,
    logs: Sequence[LogEvidence] = (),
    traces: TraceEvidence | None = None,
    candidates: Sequence[str] = (),
) -> list[Hypothesis]:
    traces = traces or TraceEvidence()
    by_service: dict[str, list[Anomaly]] = {}
    for a in anomalies:
        by_service.setdefault(a.service, []).append(a)
    anomalous = set(by_service)

    trace_errors = {k: float(v) for k, v in traces.error_origin_counts.items()}
    log_errors = {e.service: float(e.error_count) for e in logs}
    pool = set(candidates) | anomalous | {s for s, v in trace_errors.items() if v > 0}
    if not pool:
        return []

    onsets = sorted(
        {
            min(x.onset_ts for x in lst if x.onset_ts is not None)
            for lst in by_service.values()
            if any(x.onset_ts is not None for x in lst)
        }
    )

    hypotheses: list[Hypothesis] = []
    for svc in sorted(pool):
        svc_anoms = by_service.get(svc, [])
        anomaly_c = max((a.severity for a in svc_anoms), default=0.0)

        deps = graph.dependencies(svc)
        bad_deps = sorted(deps & anomalous)
        if svc not in anomalous:
            topology_c = 0.0
        elif not bad_deps:
            topology_c = 1.0
        else:
            topology_c = 0.2

        # Traces: error origins dominate; fall back to self-time for latency incidents.
        traces_c = _share(trace_errors, svc)
        if traces_c == 0.0 and any(a.kind is SignalKind.LATENCY_P95 for a in svc_anoms):
            traces_c = _share(traces.self_time_ms, svc)

        svc_onsets = [a.onset_ts for a in svc_anoms if a.onset_ts is not None]
        if svc_onsets and onsets:
            idx = onsets.index(min(svc_onsets))
            earliness_c = 1.0 - (idx / max(len(onsets), 1))
        else:
            earliness_c = 0.0

        logs_c = _share(log_errors, svc)

        components = {
            "anomaly": round(anomaly_c, 3),
            "topology": round(topology_c, 3),
            "traces": round(traces_c, 3),
            "earliness": round(earliness_c, 3),
            "logs": round(logs_c, 3),
        }
        score = sum(WEIGHTS[k] * v for k, v in components.items())

        dominant = max(svc_anoms, key=lambda a: a.severity) if svc_anoms else None
        cause = dominant.kind.value if dominant else "unknown"
        evidence = [a.description for a in svc_anoms]
        if svc in anomalous and not bad_deps:
            evidence.append(
                f"its dependencies ({', '.join(sorted(deps))}) look healthy"
                if deps
                else "it has no downstream dependencies, so the failure starts here"
            )
        if bad_deps:
            evidence.append(f"depends on anomalous services: {', '.join(bad_deps)}")
        if trace_errors.get(svc):
            evidence.append(
                f"{int(trace_errors[svc])} error span(s) originate here across "
                f"{traces.traces_analyzed} sampled traces"
            )
        if traces.self_time_ms.get(svc) and traces_c > 0 and not trace_errors.get(svc):
            evidence.append(f"{traces_c:.0%} of traced self-time is spent here")
        if log_errors.get(svc):
            evidence.append(f"{int(log_errors[svc])} error log line(s) in the window")
        hypotheses.append(
            Hypothesis(
                service=svc,
                cause=cause,
                score=round(score, 4),
                confidence=0.0,
                evidence=evidence,
                components=components,
            )
        )

    hypotheses.sort(key=lambda h: (-h.score, h.service))
    _assign_confidence(hypotheses)
    return [h for h in hypotheses if h.score > 0]


def _assign_confidence(hypotheses: list[Hypothesis]) -> None:
    """Confidence blends absolute score with the margin over the runner-up."""
    if not hypotheses:
        return
    top = hypotheses[0].score
    second = hypotheses[1].score if len(hypotheses) > 1 else 0.0
    for i, h in enumerate(hypotheses):
        if i == 0:
            margin = (top - second) / top if top > 0 else 0.0
            h.confidence = round(min(1.0, 0.6 * h.score + 0.4 * margin + 0.1), 3)
        else:
            h.confidence = round(min(1.0, 0.6 * h.score * (h.score / top if top else 0)), 3)
