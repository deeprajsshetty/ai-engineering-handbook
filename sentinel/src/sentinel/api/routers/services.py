"""Service catalog, dependency map, live service health and audit endpoints."""

from __future__ import annotations

from datetime import datetime

from fastapi import APIRouter, Depends, HTTPException, Path, Query, Request
from sqlalchemy import func, select

from sentinel.api.schemas import (
    AuditOut,
    DependencyMapOut,
    ServiceHealthOut,
    ServiceIn,
    ServiceOut,
    SignalValue,
)
from sentinel.api.security import Principal, require
from sentinel.application import audit
from sentinel.domain.anomaly import POLICIES
from sentinel.domain.models import Role, SignalKind
from sentinel.infrastructure.catalog import SERVICE_NAME_RE, ServiceSpec, SignalQueries
from sentinel.infrastructure.orm import AlertRow, AuditEventRow, ServiceRow
from sentinel.infrastructure.telemetry import TelemetryError

router = APIRouter(prefix="/api/v1", tags=["Services"])
ServiceName = Path(..., pattern=SERVICE_NAME_RE.pattern)


@router.get("/services", response_model=list[ServiceOut], operation_id="list_services")
def list_services(
    request: Request, _: Principal = Depends(require(Role.VIEWER))
) -> list[ServiceOut]:
    with request.app.state.sentinel.c.db.session() as s:
        return [
            ServiceOut.model_validate(r)
            for r in s.scalars(select(ServiceRow).order_by(ServiceRow.name))
        ]


@router.put("/services/{name}", response_model=ServiceOut, operation_id="upsert_service")
def upsert_service(
    request: Request,
    body: ServiceIn,
    name: str = ServiceName,
    principal: Principal = Depends(require(Role.ADMIN)),
) -> ServiceOut:
    bad = [d for d in body.dependencies if not SERVICE_NAME_RE.match(d)]
    if bad:
        raise HTTPException(422, f"invalid dependency names: {bad}")
    app = request.app.state.sentinel
    with app.c.db.session() as s:
        row = app.topology.upsert(s, ServiceSpec(name=name, **body.model_dump()))
        audit.record(
            s,
            actor=principal.name,
            action="service.upserted",
            entity_type="service",
            entity_id=name,
            details=body.model_dump(),
        )
        return ServiceOut.model_validate(row)


@router.get(
    "/services/dependency-map",
    response_model=DependencyMapOut,
    operation_id="get_dependency_map",
    summary="Dependency graph merged from the catalog and live traces",
)
def dependency_map(
    request: Request, _: Principal = Depends(require(Role.VIEWER))
) -> DependencyMapOut:
    app = request.app.state.sentinel
    with app.c.db.session() as s:
        return DependencyMapOut(**app.topology.dependency_map(s))


@router.get(
    "/services/{name}/health",
    response_model=ServiceHealthOut,
    operation_id="get_service_health",
    summary="Current golden signals for a service",
)
def service_health(
    request: Request, name: str = ServiceName, _: Principal = Depends(require(Role.VIEWER))
) -> ServiceHealthOut:
    app = request.app.state.sentinel
    signals: list[SignalValue] = []
    degraded = False
    reachable = False
    for kind, tmpl in app.c.queries.metrics.items():
        try:
            res = app.c.telemetry.query_instant(SignalQueries.render(tmpl, name))
            reachable = True
        except TelemetryError:
            signals.append(SignalValue(kind=kind.value, value=None))
            continue
        value = res[0].points[-1].value if res and res[0].points else None
        signals.append(SignalValue(kind=kind.value, value=value))
        if value is not None and kind is not SignalKind.REQUEST_RATE:
            degraded |= value >= POLICIES[kind].static_threshold
    with app.c.db.session() as s:
        firing = (
            s.scalar(
                select(func.count())
                .select_from(AlertRow)
                .where(AlertRow.service == name, AlertRow.status == "firing")
            )
            or 0
        )
    status = "unknown" if not reachable else ("degraded" if degraded or firing else "healthy")
    return ServiceHealthOut(service=name, status=status, signals=signals, active_alerts=firing)


@router.get(
    "/audit", response_model=list[AuditOut], operation_id="list_audit_events", tags=["Audit"]
)
def list_audit(
    request: Request,
    incident_id: int | None = None,
    since: datetime | None = None,
    limit: int = Query(200, ge=1, le=1000),
    _: Principal = Depends(require(Role.ADMIN)),
) -> list[AuditOut]:
    q = select(AuditEventRow).order_by(AuditEventRow.id.desc()).limit(limit)
    if incident_id is not None:
        q = q.where(AuditEventRow.incident_id == incident_id)
    if since is not None:
        q = q.where(AuditEventRow.ts >= since)
    with request.app.state.sentinel.c.db.session() as s:
        return [AuditOut.model_validate(r) for r in s.scalars(q)]
