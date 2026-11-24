"""Human-in-the-loop remediation workflow.

State machine::

    pending_approval --approve--> approved --(worker, http executor)--> executing --> succeeded
           |                          |                                        \\--> failed
           +--reject--> rejected      +--(manual executor)--complete--> succeeded | failed

    executing --(lease expired: worker died mid-call)--> failed (unknown_outcome)

Only operators/admins may approve; every transition is audited with the actor's identity.

An ``executing`` row holds a lease. If the worker dies before recording the result, the lease
expires and the row is marked ``failed`` with ``unknown_outcome``. It is *not* retried: the
action may already have taken effect, so a human decides whether to re-run it.
"""

from __future__ import annotations

import logging
from datetime import timedelta

from sqlalchemy import select
from sqlalchemy.orm import Session

from sentinel.application import audit
from sentinel.application.container import Container
from sentinel.application.topology import TopologyService
from sentinel.domain.models import IncidentStatus, RemediationStatus
from sentinel.infrastructure.db import utcnow
from sentinel.infrastructure.orm import IncidentRow, RemediationRow
from sentinel.observability.metrics import (
    QUEUE_DEPTH,
    REMEDIATION_UNKNOWN_OUTCOME,
    REMEDIATIONS,
)

log = logging.getLogger(__name__)
MAX_EXECUTION_ATTEMPTS = 3


def expire_pending(s: Session, incident_id: int, actor: str) -> int:
    """Close proposals still awaiting approval when their incident resolves.

    Prevents a stale approval from executing a change against an incident that is already over.
    """
    rows = s.scalars(
        select(RemediationRow).where(
            RemediationRow.incident_id == incident_id,
            RemediationRow.status == RemediationStatus.PENDING_APPROVAL.value,
        )
    ).all()
    for row in rows:
        row.status = RemediationStatus.REJECTED.value
        row.decided_by, row.decided_at = actor, utcnow()
        row.decision_comment = "expired: incident resolved before approval"
        REMEDIATIONS.labels(row.action, row.status).inc()
        audit.record(
            s,
            actor=actor,
            action="remediation.expired",
            entity_type="remediation",
            entity_id=row.id,
            incident_id=incident_id,
        )
    return len(rows)


class InvalidTransition(Exception):
    pass


class NotFound(Exception):
    pass


class RemediationService:
    def __init__(self, c: Container, topology: TopologyService) -> None:
        self.c = c
        self.topology = topology

    def _get(self, s: Session, rid: int) -> RemediationRow:
        row = s.get(RemediationRow, rid)
        if row is None:
            raise NotFound(f"remediation {rid} not found")
        return row

    def decide(self, rid: int, approve: bool, actor: str, comment: str | None) -> RemediationRow:
        with self.c.db.session() as s:
            row = self._get(s, rid)
            if row.status != RemediationStatus.PENDING_APPROVAL.value:
                raise InvalidTransition(f"remediation {rid} is {row.status}, not pending_approval")
            row.status = (
                RemediationStatus.APPROVED if approve else RemediationStatus.REJECTED
            ).value
            row.decided_by, row.decided_at, row.decision_comment = actor, utcnow(), comment
            if approve:
                incident = s.get(IncidentRow, row.incident_id)
                if incident and IncidentStatus(incident.status).is_active:
                    incident.status = IncidentStatus.MITIGATING.value
            REMEDIATIONS.labels(row.action, row.status).inc()
            audit.record(
                s,
                actor=actor,
                action=f"remediation.{'approved' if approve else 'rejected'}",
                entity_type="remediation",
                entity_id=rid,
                incident_id=row.incident_id,
                details={"action": row.action, "service": row.service, "comment": comment},
            )
            return row

    def complete_manual(
        self, rid: int, success: bool, actor: str, notes: str | None
    ) -> RemediationRow:
        with self.c.db.session() as s:
            row = self._get(s, rid)
            if row.executor != "manual" or row.status != RemediationStatus.APPROVED.value:
                raise InvalidTransition("only approved manual remediations can be completed")
            row.status = (
                RemediationStatus.SUCCEEDED if success else RemediationStatus.FAILED
            ).value
            row.executed_at = utcnow()
            row.result = {"reported_by": actor, "notes": notes}
            REMEDIATIONS.labels(row.action, row.status).inc()
            audit.record(
                s,
                actor=actor,
                action="remediation.completed_manually",
                entity_type="remediation",
                entity_id=rid,
                incident_id=row.incident_id,
                details={"success": success, "notes": notes},
            )
            return row

    def reap_expired_executions(self) -> list[int]:
        """Fail ``executing`` rows whose lease expired (the worker died mid-execution)."""
        now = utcnow()
        with self.c.db.session() as s:
            stmt = select(RemediationRow).where(
                RemediationRow.status == RemediationStatus.EXECUTING.value,
                RemediationRow.lease_expires_at < now,
            )
            if self.c.db.is_postgres:
                stmt = stmt.with_for_update(skip_locked=True)
            reaped: list[int] = []
            for row in s.scalars(stmt):
                row.status = RemediationStatus.FAILED.value
                row.lease_expires_at = None
                row.executed_at = now
                row.result = {
                    "error": "unknown_outcome",
                    "detail": "worker stopped before recording the result; the action may "
                    "or may not have taken effect. Verify the target before re-running.",
                    "attempts": row.attempts,
                }
                REMEDIATIONS.labels(row.action, row.status).inc()
                REMEDIATION_UNKNOWN_OUTCOME.labels(row.action).inc()
                log.warning(
                    "remediation lease expired, outcome unknown",
                    extra={"remediation_id": row.id, "action": row.action},
                )
                audit.record(
                    s,
                    actor="sentinel-worker",
                    action="remediation.unknown_outcome",
                    entity_type="remediation",
                    entity_id=row.id,
                    incident_id=row.incident_id,
                    details={"action": row.action, "service": row.service},
                )
                reaped.append(row.id)
            return reaped

    def claim_next_approved(self) -> int | None:
        self.reap_expired_executions()
        with self.c.db.session() as s:
            stmt = (
                select(RemediationRow)
                .where(
                    RemediationRow.status == RemediationStatus.APPROVED.value,
                    RemediationRow.executor == "http",
                )
                .order_by(RemediationRow.decided_at)
                .limit(1)
            )
            if self.c.db.is_postgres:
                stmt = stmt.with_for_update(skip_locked=True)
            row = s.scalar(stmt)
            if row is None:
                QUEUE_DEPTH.labels("remediations").set(0)
                return None
            row.status = RemediationStatus.EXECUTING.value
            row.attempts += 1
            row.lease_expires_at = utcnow() + timedelta(seconds=self.c.settings.remediation_lease_s)
            return row.id

    def execute(self, rid: int) -> RemediationRow:
        with self.c.db.session() as s:
            row = self._get(s, rid)
            spec = self.c.actions.get(row.action)
            known = self.topology.catalog_services(s)
            action, service, incident_id = row.action, row.service, row.incident_id
        if spec is None:
            result_ok, detail = False, {"error": f"action {action} no longer in catalog"}
        else:
            res = self.c.executor.execute(spec, service, known)
            result_ok, detail = res.ok, res.detail
        with self.c.db.session() as s:
            row = self._get(s, rid)
            row.lease_expires_at = None
            retry = (
                not result_ok
                and row.attempts < MAX_EXECUTION_ATTEMPTS
                and "status_code" not in detail
            )
            if retry:
                row.status = RemediationStatus.APPROVED.value  # transient failure: retry later
            else:
                row.status = (
                    RemediationStatus.SUCCEEDED if result_ok else RemediationStatus.FAILED
                ).value
                row.executed_at = utcnow()
            row.result = detail
            REMEDIATIONS.labels(row.action, row.status).inc()
            audit.record(
                s,
                actor="sentinel-executor",
                action="remediation.executed",
                entity_type="remediation",
                entity_id=rid,
                incident_id=incident_id,
                details={"ok": result_ok, "retry": retry, **detail},
            )
            return row
