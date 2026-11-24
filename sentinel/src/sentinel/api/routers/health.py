"""Liveness, readiness and Prometheus metrics endpoints (unauthenticated, rate-limit exempt)."""

from __future__ import annotations

import logging
import time

from fastapi import APIRouter, Request, Response
from fastapi.responses import JSONResponse
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest
from sqlalchemy import func, select

from sentinel import __version__
from sentinel.api.schemas import HealthOut
from sentinel.domain.models import IncidentStatus
from sentinel.infrastructure.orm import IncidentRow
from sentinel.observability.metrics import INCIDENTS_ACTIVE

log = logging.getLogger(__name__)
router = APIRouter(tags=["Health"])
_gauge_refreshed = [0.0]


@router.get("/health/live", response_model=HealthOut, summary="Liveness probe")
def live() -> HealthOut:
    return HealthOut(status="ok", version=__version__)


@router.get("/health/ready", response_model=HealthOut, summary="Readiness probe")
def ready(request: Request) -> Response:
    app = request.app.state.sentinel
    checks: dict[str, str] = {}
    healthy = True
    try:
        app.c.db.ping()
        checks["database"] = "ok"
    except Exception as exc:  # readiness must report, not raise
        checks["database"] = f"error: {type(exc).__name__}"
        healthy = False
    for name, url in (
        ("prometheus", app.c.settings.prometheus_url),
        ("loki", app.c.settings.loki_url),
        ("tempo", app.c.settings.tempo_url),
    ):
        checks[name] = "configured" if url else "disabled"
    body = HealthOut(status="ok" if healthy else "unavailable", checks=checks, version=__version__)
    return JSONResponse(body.model_dump(), status_code=200 if healthy else 503)


@router.get("/metrics", include_in_schema=False)
def metrics(request: Request) -> Response:
    now = time.monotonic()
    if now - _gauge_refreshed[0] > 15:
        try:
            with request.app.state.sentinel.c.db.session() as s:
                n = s.scalar(
                    select(func.count())
                    .select_from(IncidentRow)
                    .where(IncidentRow.status != IncidentStatus.RESOLVED.value)
                )
            INCIDENTS_ACTIVE.set(n or 0)
            _gauge_refreshed[0] = now
        except Exception:  # metrics must render even if the DB is down
            log.debug("could not refresh incident gauge", exc_info=True)
    return Response(generate_latest(), media_type=CONTENT_TYPE_LATEST)
