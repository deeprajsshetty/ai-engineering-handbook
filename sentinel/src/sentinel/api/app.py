"""FastAPI application factory."""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import Depends, FastAPI, HTTPException, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import FileResponse, JSONResponse
from fastapi.staticfiles import StaticFiles

from sentinel import __version__
from sentinel.api.middleware import EdgeMiddleware, TokenBucket
from sentinel.api.routers import alerts, health, incidents, remediations, services
from sentinel.api.security import Authenticator, Principal, require
from sentinel.application.container import Container
from sentinel.application.facade import SentinelApp
from sentinel.config import Settings, get_settings
from sentinel.domain.models import Role
from sentinel.observability.logging import configure_logging, request_id_var
from sentinel.observability.tracing import configure_tracing

log = logging.getLogger(__name__)
STATIC = Path(__file__).parent / "static"

# Read-only operations exposed to external AI agents over MCP. Approvals are deliberately NOT
# exposed: remediation decisions stay with authenticated humans (HITL).
MCP_OPERATIONS = [
    "list_incidents",
    "get_incident",
    "get_incident_report",
    "list_remediations",
    "get_dependency_map",
    "get_service_health",
    "list_services",
    "list_alerts",
]


def create_app(settings: Settings | None = None, container: Container | None = None) -> FastAPI:
    settings = settings or get_settings()
    configure_logging(settings)
    tracing_on = configure_tracing(settings)
    sentinel = SentinelApp(container or Container.build(settings))

    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        sentinel.bootstrap()
        log.info("sentinel api started", extra={"version": __version__, "env": settings.env})
        yield

    app = FastAPI(
        title="Sentinel AIOps API",
        version=__version__,
        description=(
            "AI-agentic observability: alert correlation, root-cause analysis and "
            "human-approved remediation. Authenticate with `X-API-Key`."
        ),
        lifespan=lifespan,
        docs_url="/docs",
        redoc_url="/redoc",
    )
    app.state.sentinel = sentinel
    app.state.auth = Authenticator(settings)
    app.add_middleware(
        EdgeMiddleware,
        limiter=TokenBucket(settings.rate_limit_per_minute, settings.rate_limit_burst),
        max_bytes=settings.max_request_bytes,
    )

    @app.exception_handler(HTTPException)
    async def http_error(_: Request, exc: HTTPException) -> JSONResponse:
        return JSONResponse(
            {
                "error": {
                    "code": f"http_{exc.status_code}",
                    "message": exc.detail,
                    "request_id": request_id_var.get(),
                }
            },
            status_code=exc.status_code,
            headers=getattr(exc, "headers", None),
        )

    @app.exception_handler(RequestValidationError)
    async def validation_error(_: Request, exc: RequestValidationError) -> JSONResponse:
        return JSONResponse(
            {
                "error": {
                    "code": "validation_error",
                    "message": "request validation failed",
                    "details": jsonable(exc.errors()),
                    "request_id": request_id_var.get(),
                }
            },
            status_code=422,
        )

    for r in (health.router, alerts.router, incidents.router, remediations.router, services.router):
        app.include_router(r)

    app.mount("/static", StaticFiles(directory=STATIC), name="static")

    @app.get("/", include_in_schema=False)
    def console() -> FileResponse:
        return FileResponse(STATIC / "index.html", media_type="text/html")

    @app.get("/api/v1/whoami", tags=["Auth"], operation_id="whoami")
    def whoami(principal: Principal = Depends(require(Role.VIEWER))) -> dict[str, str]:
        return {"name": principal.name, "role": principal.role.value}

    if tracing_on:
        from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor

        FastAPIInstrumentor.instrument_app(app, excluded_urls="health,metrics")

    if settings.mcp_enabled:
        _mount_mcp(app)
    return app


def jsonable(errors: object) -> object:
    from fastapi.encoders import jsonable_encoder

    return jsonable_encoder(errors)


def _mount_mcp(app: FastAPI) -> None:
    """Expose read-only operations as MCP tools at /mcp (Streamable HTTP)."""
    try:
        from fastapi_mcp import AuthConfig, FastApiMCP
    except ImportError:  # pragma: no cover
        log.warning("fastapi-mcp not installed; MCP endpoint disabled")
        return
    mcp = FastApiMCP(
        app,
        name="Sentinel MCP",
        description="Read-only incident, RCA and topology tools for AI agents.",
        include_operations=MCP_OPERATIONS,
        auth_config=AuthConfig(dependencies=[Depends(require(Role.VIEWER))]),
        headers=["authorization", "x-api-key"],
    )
    mcp.mount_http()


def app_factory() -> FastAPI:  # uvicorn --factory entry point
    return create_app()
