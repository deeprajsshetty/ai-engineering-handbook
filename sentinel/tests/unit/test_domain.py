"""Pure domain logic: anomaly detection, topology, correlation, traces and RCA ranking."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest

from sentinel.domain import anomaly, traces
from sentinel.domain.correlation import IncidentCandidate, find_matching_incident, propagation_chain
from sentinel.domain.models import Anomaly, LogEvidence, Point, Severity, Signal, SignalKind
from sentinel.domain.rca import rank_root_causes
from sentinel.domain.topology import DependencyGraph
from tests.fakes import chain_trace

GRAPH = DependencyGraph(
    [
        ("frontend", "checkout"),
        ("frontend", "inventory"),
        ("checkout", "payments"),
        ("checkout", "inventory"),
    ]
)


def series(
    kind: SignalKind,
    baseline: float,
    recent: float,
    n_base: int = 40,
    n_recent: int = 10,
    service: str = "svc",
) -> Signal:
    pts = [Point(ts=i, value=baseline + (i % 3 - 1) * baseline * 0.05) for i in range(n_base)]
    pts += [Point(ts=n_base + i, value=recent) for i in range(n_recent)]
    return Signal(service=service, kind=kind, points=pts)


class TestAnomalyDetection:
    def test_error_spike_is_detected_with_onset(self) -> None:
        a = anomaly.detect(series(SignalKind.ERROR_RATE, 0.0, 0.4), split_ts=40)
        assert a is not None
        assert a.method == "robust_z_mad"
        assert a.onset_ts == 40
        assert 0.9 < a.severity <= 1.0
        assert "40.0%" in a.description

    def test_flat_series_is_not_anomalous(self) -> None:
        assert anomaly.detect(series(SignalKind.LATENCY_P95, 0.1, 0.1), split_ts=40) is None

    def test_small_error_change_below_noise_floor_is_ignored(self) -> None:
        assert anomaly.detect(series(SignalKind.ERROR_RATE, 0.0, 0.01), split_ts=40) is None

    def test_latency_must_also_grow_by_ratio(self) -> None:
        # +30% is statistically visible on a tight baseline but below the 1.5x ratio guard.
        assert anomaly.detect(series(SignalKind.LATENCY_P95, 0.5, 0.65), split_ts=40) is None
        assert anomaly.detect(series(SignalKind.LATENCY_P95, 0.1, 0.9), split_ts=40) is not None

    def test_request_rate_drop_is_detected(self) -> None:
        a = anomaly.detect(series(SignalKind.REQUEST_RATE, 10.0, 0.5), split_ts=40)
        assert a is not None and a.score < 0 and "dropped" in a.description

    def test_cold_start_uses_static_threshold(self) -> None:
        s = series(SignalKind.ERROR_RATE, 0.0, 0.2, n_base=2)
        a = anomaly.detect(s, split_ts=2)
        assert a is not None and a.method == "static_threshold"

    def test_no_recent_points(self) -> None:
        assert anomaly.detect(series(SignalKind.ERROR_RATE, 0, 0.5), split_ts=1000) is None

    def test_detect_all_sorted_by_severity(self) -> None:
        found = anomaly.detect_all(
            [
                series(SignalKind.LATENCY_P95, 0.1, 0.3, service="a"),
                series(SignalKind.ERROR_RATE, 0.0, 0.5, service="b"),
            ],
            split_ts=40,
        )
        assert next(f.service for f in found) == "b"


class TestTopology:
    def test_relations(self) -> None:
        assert GRAPH.dependencies("checkout") == {"payments", "inventory"}
        assert GRAPH.dependents("inventory") == {"frontend", "checkout"}
        assert GRAPH.downstream("frontend") == {"checkout", "inventory", "payments"}
        assert GRAPH.upstream("payments") == {"checkout", "frontend"}
        assert GRAPH.neighborhood("payments", 1) == {"checkout"}
        assert GRAPH.neighborhood("payments", 2) == {"checkout", "frontend", "inventory"}

    def test_merge_and_self_edges(self) -> None:
        g = DependencyGraph([("a", "a"), ("a", "b")]).merge(DependencyGraph([("b", "c")]))
        assert g.edges == [("a", "b"), ("b", "c")]
        assert g.to_dict()["c"] == []


class TestCorrelation:
    now = datetime(2026, 1, 1, 12, 0, tzinfo=UTC)
    window = timedelta(minutes=15)

    def cand(self, iid: int, services: set[str], minutes_ago: int) -> IncidentCandidate:
        return IncidentCandidate(
            iid, frozenset(services), self.now - timedelta(minutes=minutes_ago)
        )

    def test_same_service_joins(self) -> None:
        c = [self.cand(1, {"payments"}, 1)]
        assert find_matching_incident("payments", self.now, c, GRAPH, self.window, 2) == 1

    def test_neighbour_joins_and_nearest_wins(self) -> None:
        c = [self.cand(1, {"frontend"}, 1), self.cand(2, {"checkout"}, 1)]
        assert find_matching_incident("payments", self.now, c, GRAPH, self.window, 2) == 2

    def test_outside_window_opens_new(self) -> None:
        c = [self.cand(1, {"payments"}, 30)]
        assert find_matching_incident("payments", self.now, c, GRAPH, self.window, 2) is None

    def test_unrelated_service_opens_new(self) -> None:
        c = [self.cand(1, {"payments"}, 1)]
        assert find_matching_incident("billing", self.now, c, GRAPH, self.window, 2) is None

    def test_propagation_chain_puts_origin_first(self) -> None:
        anomalies = [
            mk_anomaly("frontend", 30),
            mk_anomaly("checkout", 20),
            mk_anomaly("payments", 10),
        ]
        chain = propagation_chain(anomalies, GRAPH)
        assert chain.ordered_services[0] == "payments"
        assert ("checkout", "payments") in chain.edges
        assert set(chain.blast_radius) == {"frontend", "checkout", "payments"}


def mk_anomaly(service: str, onset: float, kind: SignalKind = SignalKind.ERROR_RATE) -> Anomaly:
    return Anomaly(
        service=service,
        kind=kind,
        score=20,
        severity=1.0,
        baseline=0,
        observed=0.4,
        onset_ts=onset,
        method="robust_z_mad",
        description=f"{kind.value} up",
    )


class TestTraces:
    def test_error_origin_is_deepest_error_span(self) -> None:
        spans = chain_trace("t1", ["frontend", "checkout", "payments"], "payments", None)
        assert [s.service for s in traces.error_origins(spans)] == ["payments"]

    def test_self_time_finds_slow_service(self) -> None:
        spans = chain_trace("t1", ["frontend", "checkout", "inventory"], None, "inventory")
        st = traces.self_time_by_service(spans)
        assert max(st, key=st.get) == "inventory"  # type: ignore[arg-type]

    def test_summarize_counts_each_service_once_per_trace(self) -> None:
        ev = traces.summarize(
            [chain_trace(f"t{i}", ["a", "b"], "b", None) for i in range(3)] + [[]]
        )
        assert ev.traces_analyzed == 3
        assert ev.error_origin_counts == {"b": 3}


class TestRootCauseRanking:
    def test_errors_rank_origin_service_first(self) -> None:
        anomalies = [
            mk_anomaly("frontend", 30),
            mk_anomaly("checkout", 20),
            mk_anomaly("payments", 10),
        ]
        ev = traces.summarize(
            [
                chain_trace(f"t{i}", ["frontend", "checkout", "payments"], "payments", None)
                for i in range(5)
            ]
        )
        logs = [
            LogEvidence(service="payments", error_count=40),
            LogEvidence(service="checkout", error_count=10),
        ]
        hyps = rank_root_causes(anomalies, GRAPH, logs, ev, candidates=["frontend"])
        assert hyps[0].service == "payments"
        assert hyps[0].components["topology"] == 1.0
        assert hyps[0].confidence > hyps[1].confidence
        assert any("dependencies" in e for e in hyps[0].evidence)
        assert any("depends on anomalous" in e for e in hyps[1].evidence)

    def test_latency_uses_self_time(self) -> None:
        anomalies = [
            mk_anomaly(s, 10, SignalKind.LATENCY_P95) for s in ("frontend", "checkout", "inventory")
        ]
        ev = traces.summarize(
            [chain_trace("t", ["frontend", "checkout", "inventory"], None, "inventory")]
        )
        hyps = rank_root_causes(anomalies, GRAPH, [], ev)
        assert hyps[0].service == "inventory"
        assert hyps[0].cause == "latency_p95"

    def test_no_evidence_no_hypotheses(self) -> None:
        assert rank_root_causes([], GRAPH) == []


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ("critical", Severity.CRITICAL),
        ("P1", Severity.CRITICAL),
        ("warning", Severity.WARNING),
        (None, Severity.INFO),
        ("whatever", Severity.INFO),
    ],
)
def test_severity_parse(raw: str | None, expected: Severity) -> None:
    assert Severity.parse(raw) is expected
    assert Severity.max(Severity.INFO, expected) is expected
