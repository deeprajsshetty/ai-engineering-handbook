"""Incident, investigation and report endpoints."""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Query, Request, status
from fastapi.responses import PlainTextResponse
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from sentinel.api.schemas import (
    IncidentDetailOut,
    IncidentSummaryOut,
    InvestigationOut,
    Page,
    RemediationOut,
    ResolveIn,
    TimelineEvent,
)
from sentinel.api.security import Principal, require
from sentinel.application import audit
from sentinel.application.remediation import expire_pending
from sentinel.domain.models import IncidentStatus, Role
from sentinel.infrastructure.db import utcnow
from sentinel.infrastructure.orm import (
    AlertRow,
    AuditEventRow,
    IncidentRow,
    InvestigationRow,
    RemediationRow,
)

router = APIRouter(prefix="/api/v1", tags=["Incidents"])


def _incident_or_404(s: Session, incident_id: int) -> IncidentRow:
    row = s.get(IncidentRow, incident_id)
    if row is None:
        raise HTTPException(status.HTTP_404_NOT_FOUND, f"incident {incident_id} not found")
    return row


@router.get(
    "/incidents",
    response_model=Page[IncidentSummaryOut],
    operation_id="list_incidents",
    summary="List incidents (newest first)",
)
def list_incidents(
    request: Request,
    status_: str | None = Query(
        None,
        alias="status",
        description="Filter: open, investigating, identified, mitigating, resolved, or 'active'",
    ),
    service: str | None = Query(None, max_length=64),
    limit: int = Query(50, ge=1, le=200),
    offset: int = Query(0, ge=0),
    _: Principal = Depends(require(Role.VIEWER)),
) -> Page[IncidentSummaryOut]:
    q = select(IncidentRow)
    if status_ == "active":
        q = q.where(IncidentRow.status != IncidentStatus.RESOLVED.value)
    elif status_:
        try:
            q = q.where(IncidentRow.status == IncidentStatus(status_).value)
        except ValueError as exc:
            raise HTTPException(422, f"unknown status {status_}") from exc
    q = q.order_by(IncidentRow.id.desc())
    with request.app.state.sentinel.c.db.session() as s:
        if service:
            # JSON-array membership is filtered in Python to stay portable across SQLite and
            # PostgreSQL. Scan is capped; use the status filter to narrow large histories.
            rows = [r for r in s.scalars(q.limit(5000)) if service in (r.services or [])]
            total = len(rows)
            page = rows[offset : offset + limit]
        else:
            total = s.scalar(select(func.count()).select_from(q.subquery())) or 0
            page = list(s.scalars(q.limit(limit).offset(offset)))
        items = [IncidentSummaryOut.model_validate(r) for r in page]
    return Page(items=items, total=total, limit=limit, offset=offset)


@router.get(
    "/incidents/{incident_id}",
    response_model=IncidentDetailOut,
    operation_id="get_incident",
    summary="Incident with alerts, RCA, remediations, timeline",
)
def get_incident(
    request: Request, incident_id: int, _: Principal = Depends(require(Role.VIEWER))
) -> IncidentDetailOut:
    with request.app.state.sentinel.c.db.session() as s:
        inc = _incident_or_404(s, incident_id)
        inv = s.scalar(
            select(InvestigationRow)
            .where(InvestigationRow.incident_id == incident_id)
            .order_by(InvestigationRow.id.desc())
            .limit(1)
        )
        rems = s.scalars(
            select(RemediationRow)
            .where(RemediationRow.incident_id == incident_id)
            .order_by(RemediationRow.id)
        ).all()
        events = s.scalars(
            select(AuditEventRow)
            .where(AuditEventRow.incident_id == incident_id)
            .order_by(AuditEventRow.ts, AuditEventRow.id)
        ).all()
        base = IncidentSummaryOut.model_validate(inc).model_dump()
        return IncidentDetailOut(
            **base,
            resolution_note=inc.resolution_note,
            alerts=inc.alerts,  # type: ignore[arg-type]
            latest_investigation=InvestigationOut.model_validate(inv) if inv else None,
            remediations=[RemediationOut.model_validate(r) for r in rems],
            timeline=[TimelineEvent.model_validate(e) for e in events],
        )


@router.get(
    "/incidents/{incident_id}/report",
    response_class=PlainTextResponse,
    operation_id="get_incident_report",
    summary="Latest incident report (markdown)",
)
def get_report(
    request: Request, incident_id: int, _: Principal = Depends(require(Role.VIEWER))
) -> PlainTextResponse:
    with request.app.state.sentinel.c.db.session() as s:
        _incident_or_404(s, incident_id)
        inv = s.scalar(
            select(InvestigationRow)
            .where(
                InvestigationRow.incident_id == incident_id,
                InvestigationRow.status == "completed",
            )
            .order_by(InvestigationRow.id.desc())
            .limit(1)
        )
    if inv is None or not inv.result:
        raise HTTPException(404, "no completed investigation yet")
    return PlainTextResponse(inv.result.get("report", ""), media_type="text/markdown")


@router.post(
    "/incidents/{incident_id}/investigations",
    response_model=InvestigationOut,
    status_code=202,
    operation_id="start_investigation",
    summary="Queue a new AI investigation for an incident",
)
def start_investigation(
    request: Request, incident_id: int, principal: Principal = Depends(require(Role.OPERATOR))
) -> InvestigationOut:
    app = request.app.state.sentinel
    with app.c.db.session() as s:
        inc = _incident_or_404(s, incident_id)
        inv_id = app.investigations.enqueue_if_idle(
            s, inc, trigger="manual", requested_by=principal.name
        )
        if inv_id is None:
            raise HTTPException(409, "an investigation is already queued or running")
        s.flush()
        return InvestigationOut.model_validate(s.get_one(InvestigationRow, inv_id))


@router.post(
    "/incidents/{incident_id}/resolve",
    response_model=IncidentSummaryOut,
    operation_id="resolve_incident",
    summary="Resolve an incident",
)
def resolve_incident(
    request: Request,
    incident_id: int,
    body: ResolveIn,
    principal: Principal = Depends(require(Role.OPERATOR)),
) -> IncidentSummaryOut:
    with request.app.state.sentinel.c.db.session() as s:
        inc = _incident_or_404(s, incident_id)
        if inc.status == IncidentStatus.RESOLVED.value:
            raise HTTPException(409, "incident already resolved")
        inc.status = IncidentStatus.RESOLVED.value
        inc.resolved_at = utcnow()
        inc.resolution_note = body.note
        expire_pending(s, inc.id, actor=principal.name)
        audit.record(
            s,
            actor=principal.name,
            action="incident.resolved",
            entity_type="incident",
            entity_id=inc.id,
            incident_id=inc.id,
            details={"note": body.note},
        )
        s.flush()
        return IncidentSummaryOut.model_validate(inc)


@router.get(
    "/investigations/{investigation_id}",
    response_model=InvestigationOut,
    operation_id="get_investigation",
    tags=["Investigations"],
)
def get_investigation(
    request: Request, investigation_id: int, _: Principal = Depends(require(Role.VIEWER))
) -> InvestigationOut:
    with request.app.state.sentinel.c.db.session() as s:
        inv = s.get(InvestigationRow, investigation_id)
        if inv is None:
            raise HTTPException(404, f"investigation {investigation_id} not found")
        return InvestigationOut.model_validate(inv)


@router.get(
    "/incidents/{incident_id}/remediations",
    response_model=list[RemediationOut],
    operation_id="list_incident_remediations",
    tags=["Remediations"],
)
def incident_remediations(
    request: Request, incident_id: int, _: Principal = Depends(require(Role.VIEWER))
) -> list[RemediationOut]:
    with request.app.state.sentinel.c.db.session() as s:
        _incident_or_404(s, incident_id)
        rows = s.scalars(select(RemediationRow).where(RemediationRow.incident_id == incident_id))
        return [RemediationOut.model_validate(r) for r in rows]


@router.get("/stats", operation_id="get_stats", tags=["Incidents"], summary="Headline numbers")
def stats(request: Request, _: Principal = Depends(require(Role.VIEWER))) -> dict[str, int]:
    with request.app.state.sentinel.c.db.session() as s:

        def count(model: type, *where: object) -> int:
            return int(s.scalar(select(func.count()).select_from(model).where(*where)) or 0)  # type: ignore[arg-type]

        return {
            "active_incidents": count(IncidentRow, IncidentRow.status != "resolved"),
            "resolved_incidents": count(IncidentRow, IncidentRow.status == "resolved"),
            "firing_alerts": count(AlertRow, AlertRow.status == "firing"),
            "pending_approvals": count(RemediationRow, RemediationRow.status == "pending_approval"),
        }
