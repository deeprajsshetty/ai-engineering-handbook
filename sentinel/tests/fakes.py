"""Test doubles: a scenario-driven telemetry backend that speaks the TelemetryBackend protocol."""

from __future__ import annotations

import re
import time
from dataclasses import dataclass, field

from sentinel.domain.models import InfraEvidence, InfraResource, Point, SpanData
from sentinel.infrastructure.telemetry import BackendUnavailable, LogLine, SeriesResult

_SERVICE_RE = re.compile(r'service(?:_name)?="([a-z0-9\-]+)"')


@dataclass
class Scenario:
    """Baseline/recent values per service and signal, plus logs and traces."""

    # {service: {"error_rate": (baseline, recent), "latency_p95": (...), "request_rate": (...)}}
    signals: dict[str, dict[str, tuple[float, float]]] = field(default_factory=dict)
    error_logs: dict[str, int] = field(default_factory=dict)
    traces: list[list[SpanData]] = field(default_factory=list)
    edges: list[tuple[str, str]] = field(default_factory=list)
    down: set[str] = field(default_factory=set)  # backends to simulate as unavailable


def chain_trace(
    trace_id: str,
    services: list[str],
    error_at: str | None,
    slow_at: str | None,
    slow_ms: float = 900,
) -> list[SpanData]:
    """A synchronous call chain services[0] -> services[1] -> ...; the error/slowness starts at
    ``error_at``/``slow_at`` and propagates to every caller."""
    spans: list[SpanData] = []
    start = 1_000_000_000
    n = len(services)
    durations = [20.0] * n
    if slow_at:
        idx = services.index(slow_at)
        durations[idx] += slow_ms
    # Each span's duration includes its children's.
    total = [sum(durations[i:]) for i in range(n)]
    erroring = set(services[: services.index(error_at) + 1]) if error_at else set()
    parent = None
    for i, svc in enumerate(services):
        sid = f"{i + 1:016x}"
        spans.append(
            SpanData(
                trace_id=trace_id,
                span_id=sid,
                parent_id=parent,
                service=svc,
                name=f"{svc} op",
                start_ns=start + i * 1_000_000,
                end_ns=start + i * 1_000_000 + int(total[i] * 1e6),
                is_error=svc in erroring,
            )
        )
        parent = sid
    return spans


class FakeTelemetry:
    def __init__(self, scenario: Scenario | None = None) -> None:
        self.scenario = scenario or Scenario()
        self.calls: list[tuple[str, str]] = []

    def _check(self, backend: str) -> None:
        if backend in self.scenario.down:
            raise BackendUnavailable(f"{backend} is not configured")

    @staticmethod
    def _kind(promql: str) -> str:
        if "histogram_quantile" in promql:
            return "latency_p95"
        if "5xx" in promql:
            return "error_rate"
        return "request_rate"

    def query_range(self, promql: str, start: float, end: float, step: int) -> list[SeriesResult]:
        self._check("prometheus")
        self.calls.append(("query_range", promql))
        m = _SERVICE_RE.search(promql)
        if not m or m.group(1) not in self.scenario.signals:
            return []
        values = self.scenario.signals[m.group(1)].get(self._kind(promql))
        if values is None:
            return []
        baseline, recent = values
        split = start + (end - start) * 0.8
        pts, t, i = [], start, 0
        while t <= end:
            jitter = (i % 3 - 1) * baseline * 0.05
            pts.append(Point(ts=t, value=(recent if t >= split else baseline + jitter)))
            t += step
            i += 1
        return [SeriesResult(labels={"service": m.group(1)}, points=pts)]

    def query_instant(self, promql: str) -> list[SeriesResult]:
        self._check("prometheus")
        self.calls.append(("query_instant", promql))
        if "traces_service_graph" in promql:
            return [
                SeriesResult({"client": a, "server": b}, [Point(ts=time.time(), value=5.0)])
                for a, b in self.scenario.edges
            ]
        m = _SERVICE_RE.search(promql)
        if not m or m.group(1) not in self.scenario.signals:
            return []
        values = self.scenario.signals[m.group(1)].get(self._kind(promql))
        return [SeriesResult({}, [Point(ts=time.time(), value=values[1])])] if values else []

    def query_logs(self, logql: str, start: float, end: float, limit: int) -> list[LogLine]:
        self._check("loki")
        self.calls.append(("query_logs", logql))
        m = _SERVICE_RE.search(logql)
        svc = m.group(1) if m else ""
        n = min(self.scenario.error_logs.get(svc, 0), limit)
        return [
            LogLine(
                ts_ns=int(end * 1e9) - i,
                labels={"service_name": svc},
                line=f"ERROR request {1000 + i} failed: upstream returned 503",
            )
            for i in range(n)
        ]

    def search_traces(self, traceql: str, start: float, end: float, limit: int) -> list[str]:
        self._check("tempo")
        self.calls.append(("search_traces", traceql))
        return [t[0].trace_id for t in self.scenario.traces][:limit]

    def get_trace(self, trace_id: str) -> list[SpanData]:
        self._check("tempo")
        for t in self.scenario.traces:
            if t[0].trace_id == trace_id:
                return t
        return []


CHAIN = ["frontend", "checkout", "payments"]


def payments_failure() -> Scenario:
    """payments throws 5xx; checkout and frontend fail as a consequence."""
    return Scenario(
        signals={
            "frontend": {
                "error_rate": (0.0, 0.30),
                "latency_p95": (0.08, 0.09),
                "request_rate": (8.0, 8.0),
            },
            "checkout": {
                "error_rate": (0.0, 0.35),
                "latency_p95": (0.06, 0.07),
                "request_rate": (4.0, 4.0),
            },
            "payments": {
                "error_rate": (0.0, 0.45),
                "latency_p95": (0.03, 0.03),
                "request_rate": (4.0, 4.0),
            },
            "inventory": {
                "error_rate": (0.0, 0.0),
                "latency_p95": (0.02, 0.02),
                "request_rate": (6.0, 6.0),
            },
        },
        error_logs={"payments": 40, "checkout": 12, "frontend": 10},
        traces=[
            chain_trace(f"{i:032x}", CHAIN, error_at="payments", slow_at=None) for i in range(1, 7)
        ],
    )


def inventory_latency() -> Scenario:
    """inventory is slow; latency accumulates in checkout and frontend."""
    chain = ["frontend", "checkout", "inventory"]
    return Scenario(
        signals={
            "frontend": {
                "error_rate": (0.0, 0.0),
                "latency_p95": (0.08, 1.3),
                "request_rate": (8.0, 8.0),
            },
            "checkout": {
                "error_rate": (0.0, 0.0),
                "latency_p95": (0.06, 1.2),
                "request_rate": (4.0, 4.0),
            },
            "payments": {
                "error_rate": (0.0, 0.0),
                "latency_p95": (0.03, 0.03),
                "request_rate": (4.0, 4.0),
            },
            "inventory": {
                "error_rate": (0.0, 0.0),
                "latency_p95": (0.02, 1.1),
                "request_rate": (6.0, 6.0),
            },
        },
        traces=[
            chain_trace(f"{i:032x}", chain, error_at=None, slow_at="inventory") for i in range(1, 7)
        ],
    )


class FakeDiscovery:
    """In-memory discovery service: service name -> InfraEvidence (or an exception to raise)."""

    def __init__(
        self,
        estate: dict[str, InfraEvidence] | None = None,
        *,
        enabled: bool = True,
        fail: Exception | None = None,
    ) -> None:
        self.estate = estate or {}
        self._enabled = enabled
        self.fail = fail
        self.calls: list[str] = []

    @property
    def enabled(self) -> bool:
        return self._enabled

    def infra_for(self, service: str) -> InfraEvidence:
        self.calls.append(service)
        if self.fail is not None:
            raise self.fail
        return self.estate.get(service, InfraEvidence(service=service))


def payments_db_missing() -> dict[str, InfraEvidence]:
    """payments runs as a deployment whose database instance has disappeared from discovery."""
    return {
        "payments": InfraEvidence(
            service="payments",
            matched=[InfraResource(id="res_pay", name="payments", type="k8s.deployment")],
            neighbours=[
                InfraResource(id="res_pod1", name="payments-7d9f-abc", type="k8s.pod"),
                InfraResource(
                    id="res_db", name="payments-db", type="database.instance", status="missing"
                ),
            ],
        ),
        "checkout": InfraEvidence(
            service="checkout",
            matched=[InfraResource(id="res_chk", name="checkout", type="k8s.deployment")],
        ),
    }
