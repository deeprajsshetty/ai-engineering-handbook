"""Alert ingestion: normalise, de-duplicate, correlate into incidents, trigger investigations."""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from typing import Any

from sqlalchemy import select
from sqlalchemy.orm import Session

from sentinel.application import audit
from sentinel.application.container import Container
from sentinel.application.investigations import InvestigationService
from sentinel.application.remediation import expire_pending
from sentinel.application.topology import TopologyService
from sentinel.domain.correlation import IncidentCandidate, find_matching_incident
from sentinel.domain.models import AlertIn, AlertStatus, IncidentStatus, Severity
from sentinel.infrastructure.db import utcnow
from sentinel.infrastructure.orm import AlertRow, IncidentRow
from sentinel.observability.metrics import ALERTS_RECEIVED, INCIDENTS_CREATED

log = logging.getLogger(__name__)
_EPOCH = datetime(1970, 1, 1).year


@dataclass
class IngestResult:
    received: int = 0
    created_alerts: int = 0
    updated_alerts: int = 0
    incidents_opened: list[int] = field(default_factory=list)
    incidents_updated: list[int] = field(default_factory=list)
    incidents_resolved: list[int] = field(default_factory=list)
    investigations_queued: list[int] = field(default_factory=list)


def parse_alertmanager(payload: dict[str, Any]) -> list[AlertIn]:
    """Convert an Alertmanager v4 webhook payload into normalised alerts."""
    out: list[AlertIn] = []
    common = payload.get("commonLabels") or {}
    for a in payload.get("alerts", []):
        labels = {str(k): str(v) for k, v in (a.get("labels") or {}).items()}
        ann = {str(k): str(v) for k, v in (a.get("annotations") or {}).items()}
        service = (
            labels.get("service")
            or labels.get("service_name")
            or labels.get("job")
            or common.get("service")
            or "unknown"
        )
        ends_raw = a.get("endsAt")
        ends_at = datetime.fromisoformat(ends_raw.replace("Z", "+00:00")) if ends_raw else None
        if ends_at and ends_at.year <= _EPOCH + 1:  # Alertmanager sends 0001-01-01 for "none"
            ends_at = None
        out.append(
            AlertIn(
                alertname=labels.get("alertname", "unknown"),
                service=service,
                severity=Severity.parse(labels.get("severity")),
                status=AlertStatus(a.get("status", "firing")),
                summary=ann.get("summary", ""),
                description=ann.get("description", ""),
                labels=labels,
                annotations=ann,
                starts_at=datetime.fromisoformat(a["startsAt"].replace("Z", "+00:00")),
                ends_at=ends_at,
                fingerprint=a.get("fingerprint"),
                generator_url=a.get("generatorURL"),
            )
        )
    return out


class AlertIngestionService:
    def __init__(
        self, c: Container, topology: TopologyService, investigations: InvestigationService
    ) -> None:
        self.c = c
        self.topology = topology
        self.investigations = investigations

    def ingest(self, alerts: list[AlertIn], source: str, actor: str) -> IngestResult:
        res = IngestResult(received=len(alerts))
        with self.c.db.session() as s:
            # One correlation decision at a time across all replicas.
            self.c.db.advisory_lock(s, "sentinel:alert-correlation")
            graph = self.topology.graph(s)
            for alert in alerts:
                ALERTS_RECEIVED.labels(source, alert.status.value).inc()
                self._ingest_one(s, alert, source, actor, graph, res)
            for inc_id in sorted(set(res.incidents_opened + res.incidents_updated)):
                incident = s.get(IncidentRow, inc_id)
                if incident is None or not IncidentStatus(incident.status).is_active:
                    continue
                inv_id = (
                    self.investigations.enqueue_if_idle(
                        s, incident, trigger="alert", requested_by=actor
                    )
                    if self.c.settings.auto_investigate
                    else None
                )
                if inv_id:
                    res.investigations_queued.append(inv_id)
        for inc_id in res.incidents_opened:
            self.c.notifier.send(f":rotating_light: Sentinel opened incident #{inc_id}")
        return res

    def _ingest_one(
        self, s: Session, alert: AlertIn, source: str, actor: str, graph: Any, res: IngestResult
    ) -> None:
        now = utcnow()
        fp = alert.effective_fingerprint()
        row = s.scalar(select(AlertRow).where(AlertRow.fingerprint == fp))
        # Re-firing after resolution starts a new episode: detach from the old incident.
        if (
            row is not None
            and row.status == AlertStatus.RESOLVED
            and alert.status is AlertStatus.FIRING
        ):
            old = s.get(IncidentRow, row.incident_id) if row.incident_id else None
            if old is None or not IncidentStatus(old.status).is_active:
                row.incident_id = None
        if row is None:
            row = AlertRow(fingerprint=fp, source=source, received_at=now)
            s.add(row)
            res.created_alerts += 1
        else:
            res.updated_alerts += 1
        row.alertname, row.service = alert.alertname, alert.service
        row.severity, row.status = alert.severity.value, alert.status.value
        row.summary, row.description = alert.summary, alert.description
        row.labels, row.annotations = alert.labels, alert.annotations
        row.starts_at, row.ends_at = alert.starts_at, alert.ends_at
        row.generator_url = alert.generator_url
        s.flush()

        if alert.status is AlertStatus.FIRING and row.incident_id is None:
            self._attach(s, row, alert, actor, graph, res)
        elif row.incident_id is not None:
            incident = s.get(IncidentRow, row.incident_id)
            if incident is not None:
                incident.last_activity_at = now
                if alert.status is AlertStatus.RESOLVED:
                    self._maybe_auto_resolve(s, incident, actor, res)

    def _attach(
        self, s: Session, row: AlertRow, alert: AlertIn, actor: str, graph: Any, res: IngestResult
    ) -> None:
        now = utcnow()
        active = list(
            s.scalars(
                select(IncidentRow).where(IncidentRow.status != IncidentStatus.RESOLVED.value)
            )
        )
        candidates = [
            IncidentCandidate(i.id, frozenset(i.services or []), i.last_activity_at) for i in active
        ]
        match_id = find_matching_incident(
            alert.service,
            now,
            candidates,
            graph,
            timedelta(minutes=self.c.settings.correlation_window_minutes),
            self.c.settings.correlation_max_hops,
        )
        if match_id is None:
            incident = IncidentRow(
                title=f"{alert.alertname} on {alert.service}",
                status=IncidentStatus.OPEN.value,
                severity=alert.severity.value,
                services=[alert.service],
                started_at=min(alert.starts_at, now),
                last_activity_at=now,
            )
            s.add(incident)
            s.flush()
            INCIDENTS_CREATED.labels(alert.severity.value).inc()
            res.incidents_opened.append(incident.id)
            audit.record(
                s,
                actor=actor,
                action="incident.opened",
                entity_type="incident",
                entity_id=incident.id,
                incident_id=incident.id,
                details={"alert": alert.alertname, "service": alert.service},
            )
        else:
            incident = s.get_one(IncidentRow, match_id)
            changed = alert.service not in (incident.services or [])
            if changed:
                incident.services = sorted({*incident.services, alert.service})
            incident.severity = Severity.max(Severity(incident.severity), alert.severity).value
            incident.started_at = min(incident.started_at, alert.starts_at)
            incident.last_activity_at = now
            if changed and match_id not in res.incidents_opened:
                res.incidents_updated.append(match_id)
            audit.record(
                s,
                actor=actor,
                action="incident.alert_correlated",
                entity_type="incident",
                entity_id=incident.id,
                incident_id=incident.id,
                details={
                    "alert": alert.alertname,
                    "service": alert.service,
                    "new_service": changed,
                },
            )
        row.incident_id = incident.id

    def _maybe_auto_resolve(
        self, s: Session, incident: IncidentRow, actor: str, res: IngestResult
    ) -> None:
        if not IncidentStatus(incident.status).is_active:
            return
        statuses = set(
            s.scalars(select(AlertRow.status).where(AlertRow.incident_id == incident.id))
        )
        if statuses == {AlertStatus.RESOLVED.value}:
            incident.status = IncidentStatus.RESOLVED.value
            incident.resolved_at = utcnow()
            incident.resolution_note = "All correlated alerts resolved."
            res.incidents_resolved.append(incident.id)
            expire_pending(s, incident.id, actor="sentinel")
            audit.record(
                s,
                actor=actor,
                action="incident.auto_resolved",
                entity_type="incident",
                entity_id=incident.id,
                incident_id=incident.id,
            )
