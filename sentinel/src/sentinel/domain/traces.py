"""Trace analytics: find where errors originate and where time is spent."""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Iterable, Sequence

from sentinel.domain.models import SpanData, TraceEvidence


def error_origins(spans: Sequence[SpanData]) -> list[SpanData]:
    """Error spans with no erroring descendant: the deepest point where the failure started.

    A failing payment call makes the payments span, the checkout client span, the checkout
    server span and the frontend span all error. Only the payments span is the *origin*.
    """
    children: dict[str, list[SpanData]] = defaultdict(list)
    for s in spans:
        if s.parent_id:
            children[s.parent_id].append(s)

    def has_error_descendant(span: SpanData) -> bool:
        stack = list(children.get(span.span_id, []))
        while stack:
            node = stack.pop()
            if node.is_error:
                return True
            stack.extend(children.get(node.span_id, []))
        return False

    return [s for s in spans if s.is_error and not has_error_descendant(s)]


def self_time_by_service(spans: Sequence[SpanData]) -> dict[str, float]:
    """Milliseconds of *exclusive* time per service (span duration minus child durations)."""
    child_time: dict[str, int] = defaultdict(int)
    for s in spans:
        if s.parent_id:
            child_time[s.parent_id] += s.duration_ns
    out: dict[str, float] = defaultdict(float)
    for s in spans:
        out[s.service] += max(0, s.duration_ns - child_time.get(s.span_id, 0)) / 1e6
    return dict(out)


def summarize(traces: Iterable[Sequence[SpanData]]) -> TraceEvidence:
    ev = TraceEvidence()
    origin_counts: dict[str, int] = defaultdict(int)
    self_time: dict[str, float] = defaultdict(float)
    for spans in traces:
        if not spans:
            continue
        ev.traces_analyzed += 1
        if len(ev.example_trace_ids) < 5:
            ev.example_trace_ids.append(spans[0].trace_id)
        # Count each service at most once per trace so one chatty trace cannot dominate.
        for svc in {s.service for s in error_origins(spans)}:
            origin_counts[svc] += 1
        for svc, ms in self_time_by_service(spans).items():
            self_time[svc] += ms
    ev.error_origin_counts = dict(origin_counts)
    ev.self_time_ms = {k: round(v, 2) for k, v in self_time.items()}
    return ev
