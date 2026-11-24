"""Alert-to-incident correlation rules (pure functions).

An incoming alert joins an existing active incident when both hold:

1. **Temporal**: the incident had activity within the correlation window.
2. **Topological**: the alert's service is already in the incident, or is within
   ``max_hops`` of one of the incident's services in the dependency graph.

Otherwise a new incident is opened. These rules intentionally favour *grouping*: one noisy
failure that cascades through three services should page once, not three times.
"""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass
from datetime import datetime, timedelta

from sentinel.domain.models import Anomaly
from sentinel.domain.topology import DependencyGraph


@dataclass(frozen=True)
class IncidentCandidate:
    incident_id: int
    services: frozenset[str]
    last_activity_at: datetime


def find_matching_incident(
    service: str,
    at: datetime,
    candidates: Sequence[IncidentCandidate],
    graph: DependencyGraph,
    window: timedelta,
    max_hops: int,
) -> int | None:
    """Return the id of the best incident for an alert, or ``None`` to open a new one."""
    best: tuple[int, float, int] | None = None  # (distance, -recency, id)
    for cand in candidates:
        if at - cand.last_activity_at > window:
            continue
        if service in cand.services:
            distance = 0
        else:
            near = graph.neighborhood(service, max_hops)
            if not (near & cand.services):
                continue
            distance = min(_hop_distance(graph, service, s, max_hops) for s in near & cand.services)
        key = (distance, -cand.last_activity_at.timestamp(), cand.incident_id)
        if best is None or key < best:
            best = key
    return best[2] if best else None


def _hop_distance(graph: DependencyGraph, a: str, b: str, max_hops: int) -> int:
    for hops in range(1, max_hops + 1):
        if b in graph.neighborhood(a, hops):
            return hops
    return max_hops + 1


@dataclass(frozen=True)
class PropagationChain:
    """Anomalous services ordered from the suspected origin outwards."""

    ordered_services: list[str]
    edges: list[tuple[str, str]]  # anomalous caller -> anomalous callee
    blast_radius: list[str]  # healthy-or-anomalous upstream services affected


def propagation_chain(anomalies: Sequence[Anomaly], graph: DependencyGraph) -> PropagationChain:
    anomalous = {a.service for a in anomalies}
    onset: dict[str, float] = {}
    for a in anomalies:
        if a.onset_ts is not None:
            onset[a.service] = min(onset.get(a.service, a.onset_ts), a.onset_ts)
    edges = [(c, d) for c, d in graph.edges if c in anomalous and d in anomalous]

    # Origin first: services with no anomalous dependency, then by onset time.
    def sort_key(s: str) -> tuple[int, float, str]:
        has_bad_dep = any(d in anomalous for d in graph.dependencies(s))
        return (1 if has_bad_dep else 0, onset.get(s, float("inf")), s)

    ordered = sorted(anomalous, key=sort_key)
    blast: set[str] = set()
    for s in anomalous:
        blast |= graph.upstream(s)
    return PropagationChain(ordered, edges, sorted(blast | anomalous))
