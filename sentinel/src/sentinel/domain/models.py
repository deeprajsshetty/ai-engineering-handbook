"""Domain vocabulary: enums and value objects shared by every layer.

This module has no I/O and no framework dependencies beyond pydantic.
"""

from __future__ import annotations

import hashlib
import json
from datetime import datetime
from enum import StrEnum
from typing import Any

from pydantic import BaseModel, Field


class Severity(StrEnum):
    INFO = "info"
    WARNING = "warning"
    CRITICAL = "critical"

    @property
    def rank(self) -> int:
        return {"info": 0, "warning": 1, "critical": 2}[self.value]

    @classmethod
    def parse(cls, value: str | None) -> Severity:
        v = (value or "").lower()
        if v in {"critical", "page", "sev1", "p1", "high"}:
            return cls.CRITICAL
        if v in {"warning", "warn", "sev2", "p2", "medium"}:
            return cls.WARNING
        return cls.INFO

    @classmethod
    def max(cls, *values: Severity) -> Severity:
        return max(values, key=lambda s: s.rank) if values else cls.INFO


class AlertStatus(StrEnum):
    FIRING = "firing"
    RESOLVED = "resolved"


class IncidentStatus(StrEnum):
    OPEN = "open"
    INVESTIGATING = "investigating"
    IDENTIFIED = "identified"
    MITIGATING = "mitigating"
    RESOLVED = "resolved"

    @property
    def is_active(self) -> bool:
        return self is not IncidentStatus.RESOLVED


class InvestigationStatus(StrEnum):
    QUEUED = "queued"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"


class RemediationStatus(StrEnum):
    PENDING_APPROVAL = "pending_approval"
    APPROVED = "approved"
    REJECTED = "rejected"
    EXECUTING = "executing"
    SUCCEEDED = "succeeded"
    FAILED = "failed"


class Risk(StrEnum):
    LOW = "low"
    MEDIUM = "medium"
    HIGH = "high"


class SignalKind(StrEnum):
    REQUEST_RATE = "request_rate"
    ERROR_RATE = "error_rate"
    LATENCY_P95 = "latency_p95"


class Role(StrEnum):
    VIEWER = "viewer"
    OPERATOR = "operator"
    ADMIN = "admin"

    @property
    def rank(self) -> int:
        return {"viewer": 0, "operator": 1, "admin": 2}[self.value]


# ---------------------------------------------------------------------------------------------
# Value objects
# ---------------------------------------------------------------------------------------------


class AlertIn(BaseModel):
    """A normalised alert, independent of the source system."""

    alertname: str = Field(min_length=1, max_length=200)
    service: str = Field(min_length=1, max_length=64, pattern=r"^[A-Za-z0-9_.\-]+$")
    severity: Severity = Severity.WARNING
    status: AlertStatus = AlertStatus.FIRING
    summary: str = Field(default="", max_length=2000)
    description: str = Field(default="", max_length=8000)
    labels: dict[str, str] = Field(default_factory=dict)
    annotations: dict[str, str] = Field(default_factory=dict)
    starts_at: datetime
    ends_at: datetime | None = None
    fingerprint: str | None = Field(default=None, max_length=128)
    generator_url: str | None = Field(default=None, max_length=2000)

    def effective_fingerprint(self) -> str:
        if self.fingerprint:
            return self.fingerprint
        identity = {"alertname": self.alertname, "service": self.service, **self.labels}
        raw = json.dumps(identity, sort_keys=True).encode()
        return hashlib.sha256(raw).hexdigest()[:32]


class Point(BaseModel):
    ts: float
    value: float


class Signal(BaseModel):
    """A time series for one golden signal of one service."""

    service: str
    kind: SignalKind
    points: list[Point] = Field(default_factory=list)


class Anomaly(BaseModel):
    service: str
    kind: SignalKind
    score: float  # robust z-score (or equivalent for threshold fallback)
    severity: float  # normalised 0..1
    baseline: float
    observed: float
    onset_ts: float | None
    method: str
    description: str


class LogEvidence(BaseModel):
    service: str
    error_count: int
    top_messages: list[tuple[str, int]] = Field(default_factory=list)


class SpanData(BaseModel):
    trace_id: str
    span_id: str
    parent_id: str | None
    service: str
    name: str
    start_ns: int
    end_ns: int
    is_error: bool

    @property
    def duration_ns(self) -> int:
        return max(0, self.end_ns - self.start_ns)


class TraceEvidence(BaseModel):
    """Per-service attribution derived from sampled traces."""

    error_origin_counts: dict[str, int] = Field(default_factory=dict)
    self_time_ms: dict[str, float] = Field(default_factory=dict)
    traces_analyzed: int = 0
    example_trace_ids: list[str] = Field(default_factory=list)


class InfraResource(BaseModel):
    """A resource the discovery service found around an application service."""

    id: str
    name: str
    type: str  # e.g. "k8s.pod", "k8s.node", "database.instance"
    status: str = "active"  # "active" | "missing"
    freshness: str | None = None  # "fresh" | "aging" | "stale"

    @property
    def problem(self) -> str | None:
        if self.status == "missing":
            return "missing (no longer observed by discovery)"
        if self.freshness == "stale":
            return "stale (not re-observed recently)"
        return None


class InfraEvidence(BaseModel):
    """Infrastructure context for one application service, from the discovery service."""

    service: str
    matched: list[InfraResource] = Field(default_factory=list)  # the service's own resources
    neighbours: list[InfraResource] = Field(default_factory=list)  # pods, nodes, databases, ...
    truncated: bool = False

    @property
    def issues(self) -> list[str]:
        return [
            f"{r.type} {r.name} is {r.problem}"
            for r in [*self.matched, *self.neighbours]
            if r.problem
        ]


class Hypothesis(BaseModel):
    service: str
    cause: str  # e.g. "error_rate", "latency_p95"
    score: float
    confidence: float
    evidence: list[str]
    components: dict[str, float]


class Recommendation(BaseModel):
    action: str
    service: str
    risk: Risk
    executor: str
    rationale: str
    runbook: str | None = None
    requires_approval: bool = True
    params: dict[str, Any] = Field(default_factory=dict)


class AgentStep(BaseModel):
    agent: str
    started_at: float
    duration_ms: float
    summary: str
    ok: bool = True
