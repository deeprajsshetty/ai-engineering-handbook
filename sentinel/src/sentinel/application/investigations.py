"""Investigation queue and execution.

The queue is a PostgreSQL table claimed with ``FOR UPDATE SKIP LOCKED`` plus a lease, which
gives at-least-once processing across any number of workers without adding a message broker
(ADR-0002). A worker that crashes mid-investigation loses its lease and the job is retried
up to ``investigation_max_attempts`` times.
"""

from __future__ import annotations

import logging
import time
from datetime import timedelta
from typing import Any

from opentelemetry import trace
from sqlalchemy import func, or_, select
from sqlalchemy.orm import Session

from sentinel.agents.base import AgentDeps
from sentinel.agents.graph import run_investigation
from sentinel.application import audit
from sentinel.application.container import Container
from sentinel.application.topology import TopologyService
from sentinel.domain.models import (
    IncidentStatus,
    InvestigationStatus,
    RemediationStatus,
)
from sentinel.infrastructure.db import utcnow
from sentinel.infrastructure.orm import IncidentRow, InvestigationRow, RemediationRow
from sentinel.observability.metrics import (
    INVESTIGATION_DURATION,
    INVESTIGATIONS,
    QUEUE_DEPTH,
    RCA_CONFIDENCE,
    REMEDIATIONS,
)

log = logging.getLogger(__name__)
tracer = trace.get_tracer("sentinel.investigations")
MAX_SCOPE = 15


class InvestigationService:
    def __init__(self, c: Container, topology: TopologyService) -> None:
        self.c = c
        self.topology = topology

    # -- queue ---------------------------------------------------------------------------------
    def enqueue_if_idle(
        self, s: Session, incident: IncidentRow, trigger: str, requested_by: str
    ) -> int | None:
        pending = s.scalar(
            select(InvestigationRow.id).where(
                InvestigationRow.incident_id == incident.id,
                InvestigationRow.status.in_(
                    [InvestigationStatus.QUEUED.value, InvestigationStatus.RUNNING.value]
                ),
            )
        )
        if pending:
            return None
        return self.enqueue(s, incident, trigger, requested_by)

    def enqueue(self, s: Session, incident: IncidentRow, trigger: str, requested_by: str) -> int:
        inv = InvestigationRow(
            incident_id=incident.id,
            status=InvestigationStatus.QUEUED.value,
            trigger=trigger,
            requested_by=requested_by,
            llm_provider=self.c.llm.name,
        )
        s.add(inv)
        s.flush()
        if incident.status == IncidentStatus.OPEN.value:
            incident.status = IncidentStatus.INVESTIGATING.value
        audit.record(
            s,
            actor=requested_by,
            action="investigation.queued",
            entity_type="investigation",
            entity_id=inv.id,
            incident_id=incident.id,
            details={"trigger": trigger},
        )
        return inv.id

    def claim_next(self) -> int | None:
        """Atomically claim one queued (or lease-expired) investigation."""
        now = utcnow()
        lease = timedelta(seconds=self.c.settings.worker_lease_s)
        with self.c.db.session() as s:
            stmt = (
                select(InvestigationRow)
                .where(
                    or_(
                        InvestigationRow.status == InvestigationStatus.QUEUED.value,
                        (InvestigationRow.status == InvestigationStatus.RUNNING.value)
                        & (InvestigationRow.lease_expires_at < now),
                    )
                )
                .order_by(InvestigationRow.queued_at)
                .limit(1)
            )
            if self.c.db.is_postgres:
                stmt = stmt.with_for_update(skip_locked=True)
            inv = s.scalar(stmt)
            if inv is None:
                return None
            if inv.attempts >= self.c.settings.investigation_max_attempts:
                inv.status = InvestigationStatus.FAILED.value
                inv.error = (inv.error or "") + " | exceeded max attempts"
                inv.finished_at = now
                INVESTIGATIONS.labels("failed").inc()
                return None
            inv.status = InvestigationStatus.RUNNING.value
            inv.attempts += 1
            inv.started_at = now
            inv.lease_expires_at = now + lease
            return inv.id

    def queue_depth(self) -> int:
        with self.c.db.session() as s:
            n = (
                s.scalar(
                    select(func.count())
                    .select_from(InvestigationRow)
                    .where(InvestigationRow.status == InvestigationStatus.QUEUED.value)
                )
                or 0
            )
        QUEUE_DEPTH.labels("investigations").set(n)
        return int(n)

    # -- execution -----------------------------------------------------------------------------
    def build_context(
        self, s: Session, incident: IncidentRow
    ) -> tuple[dict[str, Any], dict[str, float], list[str], Any]:
        st = self.c.settings
        graph = self.topology.graph(s)
        end = (incident.resolved_at or utcnow()).timestamp()
        split = min(incident.started_at.timestamp() - 120, end - 60)
        baseline_s = (
            max(st.investigation_lookback_minutes - st.investigation_recent_minutes, 5) * 60
        )
        window = {"start": split - baseline_s, "split": split, "end": end}

        scope: list[str] = list(incident.services or [])
        for svc in list(scope):
            for dep in sorted(graph.downstream(svc, max_depth=2)):
                if dep not in scope:
                    scope.append(dep)
        scope = scope[:MAX_SCOPE]
        ctx = {
            "id": incident.id,
            "title": incident.title,
            "status": incident.status,
            "severity": incident.severity,
            "services": list(incident.services or []),
            "started_at": incident.started_at.isoformat(),
            "alerts": [
                {
                    "alertname": a.alertname,
                    "service": a.service,
                    "severity": a.severity,
                    "status": a.status,
                    "starts_at": a.starts_at.isoformat(),
                    "summary": a.summary,
                }
                for a in incident.alerts
            ],
        }
        return ctx, window, scope, graph

    def run(self, inv_id: int) -> dict[str, Any] | None:
        t0 = time.perf_counter()
        with self.c.db.session() as s:
            inv = s.get_one(InvestigationRow, inv_id)
            incident = s.get_one(IncidentRow, inv.incident_id)
            ctx, window, scope, graph = self.build_context(s, incident)

        deps = AgentDeps(
            settings=self.c.settings,
            telemetry=self.c.telemetry,
            llm=self.c.llm,
            retriever=self.c.retriever,
            actions=self.c.actions,
            queries=self.c.queries,
            graph=graph,
            discovery=self.c.discovery,
        )
        with tracer.start_as_current_span("investigation.run") as span:
            span.set_attribute("sentinel.incident_id", ctx["id"])
            span.set_attribute("sentinel.investigation_id", inv_id)
            try:
                result = run_investigation(deps, ctx, window, scope)
            except Exception as exc:  # the graph isolates agent errors; this is a last resort
                log.exception("investigation %s crashed", inv_id)
                self._fail(inv_id, f"{type(exc).__name__}: {exc}")
                return None

        elapsed = time.perf_counter() - t0
        self._complete(inv_id, result, elapsed)
        return result

    def _fail(self, inv_id: int, error: str) -> None:
        with self.c.db.session() as s:
            inv = s.get_one(InvestigationRow, inv_id)
            inv.error = error
            inv.lease_expires_at = None
            if inv.attempts >= self.c.settings.investigation_max_attempts:
                inv.status = InvestigationStatus.FAILED.value
                inv.finished_at = utcnow()
                INVESTIGATIONS.labels("failed").inc()
            else:
                inv.status = InvestigationStatus.QUEUED.value  # retry
            audit.record(
                s,
                actor="sentinel-worker",
                action="investigation.failed",
                entity_type="investigation",
                entity_id=inv_id,
                incident_id=inv.incident_id,
                details={"error": error[:500]},
            )

    def _complete(self, inv_id: int, result: dict[str, Any], elapsed: float) -> None:
        with self.c.db.session() as s:
            inv = s.get_one(InvestigationRow, inv_id)
            incident = s.get_one(IncidentRow, inv.incident_id)
            inv.status = InvestigationStatus.COMPLETED.value
            inv.result = result
            inv.finished_at = utcnow()
            inv.duration_ms = int(elapsed * 1000)
            inv.lease_expires_at = None
            inv.error = None
            INVESTIGATIONS.labels("completed").inc()
            INVESTIGATION_DURATION.observe(elapsed)

            hyps = result.get("hypotheses") or []
            if hyps:
                top = hyps[0]
                incident.root_cause_service = top["service"]
                incident.root_cause_confidence = top["confidence"]
                RCA_CONFIDENCE.observe(top["confidence"])
                if incident.status in (
                    IncidentStatus.OPEN.value,
                    IncidentStatus.INVESTIGATING.value,
                ):
                    incident.status = IncidentStatus.IDENTIFIED.value
            elif incident.status == IncidentStatus.INVESTIGATING.value:
                incident.status = IncidentStatus.OPEN.value
            incident.summary = result.get("summary") or incident.summary

            created = self._propose_remediations(s, incident, inv, result)
            audit.record(
                s,
                actor="sentinel-agents",
                action="investigation.completed",
                entity_type="investigation",
                entity_id=inv_id,
                incident_id=incident.id,
                details={
                    "root_cause": hyps[0]["service"] if hyps else None,
                    "confidence": hyps[0]["confidence"] if hyps else None,
                    "remediations_proposed": created,
                    "data_gaps": len(result.get("data_gaps", [])),
                },
            )
            incident_id, root = incident.id, incident.root_cause_service
        if root:
            self.c.notifier.send(
                f":mag: Incident #{incident_id}: likely root cause *{root}*. "
                f"{len(created)} remediation(s) awaiting approval."
            )

    def _propose_remediations(
        self, s: Session, incident: IncidentRow, inv: InvestigationRow, result: dict[str, Any]
    ) -> list[int]:
        """Persist recommendations, skipping ones already open for this incident."""
        open_keys = {
            (r.action, r.service)
            for r in s.scalars(
                select(RemediationRow).where(
                    RemediationRow.incident_id == incident.id,
                    RemediationRow.status.in_(
                        [
                            RemediationStatus.PENDING_APPROVAL.value,
                            RemediationStatus.APPROVED.value,
                            RemediationStatus.EXECUTING.value,
                        ]
                    ),
                )
            )
        }
        created: list[int] = []
        for rec in result.get("recommendations") or []:
            if (rec["action"], rec["service"]) in open_keys:
                continue
            status = (
                RemediationStatus.PENDING_APPROVAL
                if rec["requires_approval"]
                else RemediationStatus.APPROVED
            )
            row = RemediationRow(
                incident_id=incident.id,
                investigation_id=inv.id,
                action=rec["action"],
                service=rec["service"],
                params=rec.get("params") or {},
                risk=rec["risk"],
                executor=rec["executor"],
                rationale=rec["rationale"],
                runbook=rec.get("runbook"),
                status=status.value,
                decided_by=None if rec["requires_approval"] else "policy:auto_approve_low_risk",
                decided_at=None if rec["requires_approval"] else utcnow(),
            )
            s.add(row)
            s.flush()
            created.append(row.id)
            REMEDIATIONS.labels(row.action, row.status).inc()
            audit.record(
                s,
                actor="sentinel-agents",
                action="remediation.proposed",
                entity_type="remediation",
                entity_id=row.id,
                incident_id=incident.id,
                details={
                    "action": row.action,
                    "service": row.service,
                    "risk": row.risk,
                    "status": row.status,
                },
            )
        return created
