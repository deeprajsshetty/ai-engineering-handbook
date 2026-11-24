"""Alert ingestion and query endpoints."""

from __future__ import annotations

from typing import Any

from fastapi import APIRouter, Body, Depends, HTTPException, Query, Request, status
from pydantic import ValidationError
from sqlalchemy import func, select

from sentinel.api.schemas import AlertOut, GenericAlertIn, IngestOut, Page
from sentinel.api.security import Principal, require, require_webhook
from sentinel.application.alerts import parse_alertmanager
from sentinel.domain.models import AlertIn, AlertStatus, Role, Severity
from sentinel.infrastructure.db import utcnow
from sentinel.infrastructure.orm import AlertRow

router = APIRouter(prefix="/api/v1/alerts", tags=["Alerts"])


@router.post(
    "/alertmanager",
    response_model=IngestOut,
    summary="Alertmanager webhook receiver",
    operation_id="ingest_alertmanager",
)
def alertmanager_webhook(
    request: Request,
    payload: dict[str, Any] = Body(
        ..., examples=[{"version": "4", "status": "firing", "alerts": []}]
    ),
    actor: str = Depends(require_webhook),
) -> IngestOut:
    try:
        alerts = parse_alertmanager(payload)
    except (ValidationError, KeyError, ValueError) as exc:
        raise HTTPException(422, f"invalid payload: {exc}") from exc
    res = request.app.state.sentinel.alerts.ingest(alerts, source="alertmanager", actor=actor)
    return IngestOut(**res.__dict__)


@router.post(
    "",
    response_model=IngestOut,
    status_code=status.HTTP_202_ACCEPTED,
    summary="Ingest a generic alert",
    operation_id="ingest_alert",
)
def ingest_generic(
    request: Request, body: GenericAlertIn, principal: Principal = Depends(require(Role.OPERATOR))
) -> IngestOut:
    alert = AlertIn(
        alertname=body.alertname,
        service=body.service,
        severity=Severity.parse(body.severity),
        status=AlertStatus(body.status),
        summary=body.summary,
        description=body.description,
        labels=body.labels,
        starts_at=body.starts_at or utcnow(),
        fingerprint=body.fingerprint,
    )
    res = request.app.state.sentinel.alerts.ingest([alert], source="api", actor=principal.name)
    return IngestOut(**res.__dict__)


@router.get("", response_model=Page[AlertOut], summary="List alerts", operation_id="list_alerts")
def list_alerts(
    request: Request,
    status_: str | None = Query(None, alias="status", pattern="^(firing|resolved)$"),
    service: str | None = Query(None, max_length=64),
    limit: int = Query(50, ge=1, le=200),
    offset: int = Query(0, ge=0),
    _: Principal = Depends(require(Role.VIEWER)),
) -> Page[AlertOut]:
    q = select(AlertRow)
    if status_:
        q = q.where(AlertRow.status == status_)
    if service:
        q = q.where(AlertRow.service == service)
    with request.app.state.sentinel.c.db.session() as s:
        total = s.scalar(select(func.count()).select_from(q.subquery())) or 0
        rows = s.scalars(q.order_by(AlertRow.received_at.desc()).limit(limit).offset(offset)).all()
        items = [AlertOut.model_validate(r) for r in rows]
    return Page(items=items, total=total, limit=limit, offset=offset)
