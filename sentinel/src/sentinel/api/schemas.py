"""API request/response contracts (v1). Changing a field is a breaking change: add, don't mutate."""

from __future__ import annotations

from datetime import datetime
from typing import Any, Generic, TypeVar

from pydantic import BaseModel, ConfigDict, Field

T = TypeVar("T")


class Page(BaseModel, Generic[T]):
    items: list[T]
    total: int
    limit: int
    offset: int


class ORM(BaseModel):
    model_config = ConfigDict(from_attributes=True)


class AlertOut(ORM):
    id: int
    fingerprint: str
    alertname: str
    service: str
    severity: str
    status: str
    summary: str
    description: str
    labels: dict[str, Any]
    source: str
    starts_at: datetime
    ends_at: datetime | None
    incident_id: int | None
    received_at: datetime


class RemediationOut(ORM):
    id: int
    incident_id: int
    investigation_id: int | None
    action: str
    service: str
    risk: str
    executor: str
    rationale: str
    runbook: str | None
    status: str
    decided_by: str | None
    decided_at: datetime | None
    decision_comment: str | None
    executed_at: datetime | None
    result: dict[str, Any] | None
    created_at: datetime


class InvestigationOut(ORM):
    id: int
    incident_id: int
    status: str
    trigger: str
    requested_by: str
    attempts: int
    queued_at: datetime
    started_at: datetime | None
    finished_at: datetime | None
    duration_ms: int | None
    llm_provider: str | None
    error: str | None
    result: dict[str, Any] | None = None


class IncidentSummaryOut(ORM):
    id: int
    title: str
    status: str
    severity: str
    services: list[str]
    started_at: datetime
    last_activity_at: datetime
    resolved_at: datetime | None
    root_cause_service: str | None
    root_cause_confidence: float | None
    summary: str | None


class TimelineEvent(ORM):
    ts: datetime
    actor: str
    action: str
    entity_type: str
    entity_id: str
    details: dict[str, Any]


class IncidentDetailOut(IncidentSummaryOut):
    resolution_note: str | None
    alerts: list[AlertOut]
    latest_investigation: InvestigationOut | None
    remediations: list[RemediationOut]
    timeline: list[TimelineEvent]


class GenericAlertIn(BaseModel):
    """Alert from any source other than Alertmanager (CI, synthetic checks, other tools)."""

    alertname: str = Field(min_length=1, max_length=200, examples=["HighErrorRate"])
    service: str = Field(min_length=1, max_length=64, pattern=r"^[A-Za-z0-9_.\-]+$")
    severity: str = Field(default="warning", examples=["critical"])
    status: str = Field(default="firing", pattern="^(firing|resolved)$")
    summary: str = Field(default="", max_length=2000)
    description: str = Field(default="", max_length=8000)
    labels: dict[str, str] = Field(default_factory=dict)
    starts_at: datetime | None = None
    fingerprint: str | None = Field(default=None, max_length=128)


class IngestOut(BaseModel):
    received: int
    created_alerts: int
    updated_alerts: int
    incidents_opened: list[int]
    incidents_updated: list[int]
    incidents_resolved: list[int]
    investigations_queued: list[int]


class DecisionIn(BaseModel):
    comment: str | None = Field(default=None, max_length=2000)


class CompletionIn(BaseModel):
    success: bool
    notes: str | None = Field(default=None, max_length=4000)


class ResolveIn(BaseModel):
    note: str = Field(min_length=1, max_length=4000)


class ServiceIn(BaseModel):
    team: str | None = Field(default=None, max_length=100)
    tier: str | None = Field(default=None, max_length=20)
    description: str | None = Field(default=None, max_length=2000)
    dependencies: list[str] = Field(default_factory=list, max_length=100)
    runbook_url: str | None = Field(default=None, max_length=500)


class ServiceOut(ORM):
    name: str
    team: str | None
    tier: str | None
    description: str | None
    dependencies: list[str]
    runbook_url: str | None


class DependencyEdge(BaseModel):
    caller: str
    callee: str
    source: str


class DependencyMapOut(BaseModel):
    nodes: list[str]
    edges: list[DependencyEdge]


class SignalValue(BaseModel):
    kind: str
    value: float | None


class ServiceHealthOut(BaseModel):
    service: str
    status: str  # healthy | degraded | unknown
    signals: list[SignalValue]
    active_alerts: int


class AuditOut(ORM):
    id: int
    ts: datetime
    actor: str
    action: str
    entity_type: str
    entity_id: str
    incident_id: int | None
    details: dict[str, Any]


class HealthOut(BaseModel):
    status: str
    checks: dict[str, str] = Field(default_factory=dict)
    version: str
