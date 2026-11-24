"""Demo "shop" microservices: the customer workload Sentinel observes.

One codebase, four services selected by ``SERVICE_NAME``::

    frontend --> checkout --> payments
        \\            \\---> inventory
         \\---------------> inventory

Every service is instrumented with OpenTelemetry (traces, metrics, logs -> OTLP -> collector)
and exposes an authenticated admin API to inject faults. A fault plays the role of a bad
feature flag or config push: Sentinel's ``disable_fault_flag`` remediation clears it.
"""

from __future__ import annotations

import asyncio
import hmac
import logging
import os
import random
import time
from typing import Any

import httpx
from fastapi import FastAPI, Header, HTTPException, Request, Response
from fastapi.responses import JSONResponse
from opentelemetry import metrics, trace
from opentelemetry._logs import set_logger_provider
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.instrumentation.httpx import HTTPXClientInstrumentor
from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.sdk.metrics.view import ExplicitBucketHistogramAggregation, View
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from pydantic import BaseModel, Field

SERVICE = os.environ.get("SERVICE_NAME", "frontend")
ADMIN_TOKEN = os.environ.get("ADMIN_TOKEN", "")
PORT = int(os.environ.get("PORT", "8080"))
UPSTREAM = {name: f"http://{name}:{PORT}" for name in ("checkout", "payments", "inventory")}

# --- OpenTelemetry setup -------------------------------------------------------------------
resource = Resource.create({"service.name": SERVICE, "service.namespace": "shop",
                            "deployment.environment": os.environ.get("ENVIRONMENT", "demo")})
tracer_provider = TracerProvider(resource=resource)
tracer_provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(tracer_provider)

metric_reader = PeriodicExportingMetricReader(
    OTLPMetricExporter(), export_interval_millis=int(os.environ.get("METRIC_INTERVAL_MS", "10000"))
)
metrics.set_meter_provider(MeterProvider(
    resource=resource,
    metric_readers=[metric_reader],
    views=[View(instrument_name="shop.server.duration",
                aggregation=ExplicitBucketHistogramAggregation(
                    [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 10]))],
))
meter = metrics.get_meter("shop")
REQUESTS = meter.create_counter("shop.server.requests", unit="1", description="HTTP requests")
DURATION = meter.create_histogram("shop.server.duration", unit="s", description="Request latency")

logger_provider = LoggerProvider(resource=resource)
logger_provider.add_log_record_processor(BatchLogRecordProcessor(OTLPLogExporter()))
set_logger_provider(logger_provider)
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
logging.getLogger().addHandler(LoggingHandler(level=logging.INFO, logger_provider=logger_provider))
log = logging.getLogger(SERVICE)
for noisy in ("httpx", "uvicorn.access"):
    logging.getLogger(noisy).setLevel(logging.WARNING)

HTTPXClientInstrumentor().instrument()
client = httpx.AsyncClient(timeout=httpx.Timeout(3.0))


# --- Fault injection ("feature flag") ------------------------------------------------------
class Fault(BaseModel):
    error_rate: float = Field(0.0, ge=0, le=1)
    latency_ms: int = Field(0, ge=0, le=10_000)
    duration_s: int = Field(600, ge=1, le=3600)


_fault: dict[str, float] = {"error_rate": 0.0, "latency_ms": 0.0, "expires_at": 0.0}


def active_fault() -> dict[str, float]:
    if _fault["expires_at"] and time.time() > _fault["expires_at"]:
        _fault.update(error_rate=0.0, latency_ms=0.0, expires_at=0.0)
    return _fault


def check_admin(token: str | None) -> None:
    if not ADMIN_TOKEN or not token or not hmac.compare_digest(token, ADMIN_TOKEN):
        raise HTTPException(401, "invalid admin token")


app = FastAPI(title=f"shop-{SERVICE}")


@app.middleware("http")
async def telemetry_and_faults(request: Request, call_next: Any) -> Response:
    path = request.url.path
    business = not path.startswith(("/admin", "/health"))
    t0 = time.perf_counter()
    if business:
        f = active_fault()
        if f["latency_ms"]:
            await asyncio.sleep(f["latency_ms"] / 1000 * random.uniform(0.8, 1.2))  # noqa: S311
        if f["error_rate"] and random.random() < f["error_rate"]:  # noqa: S311
            log.error("injected fault: simulated %s failure on %s", SERVICE, path)
            response: Response = JSONResponse({"error": f"{SERVICE} unavailable"}, status_code=503)
            _record(request, response.status_code, t0)
            trace.get_current_span().set_status(trace.Status(trace.StatusCode.ERROR, "fault"))
            return response
    response = await call_next(request)
    if business:
        _record(request, response.status_code, t0)
    return response


def _record(request: Request, status: int, t0: float) -> None:
    route = request.scope.get("route")
    attrs = {"service": SERVICE, "route": getattr(route, "path", request.url.path),
             "method": request.method, "status_class": f"{status // 100}xx"}
    REQUESTS.add(1, attrs)
    DURATION.record(time.perf_counter() - t0, attrs)


async def call(service: str, method: str, path: str, **kw: Any) -> dict[str, Any]:
    """Call a dependency; translate its failure into a 502 for our caller."""
    try:
        r = await client.request(method, UPSTREAM[service] + path, **kw)
        r.raise_for_status()
        return dict(r.json())
    except httpx.HTTPError as exc:
        log.error("dependency %s failed on %s %s: %s", service, method, path, type(exc).__name__)
        trace.get_current_span().set_status(trace.Status(trace.StatusCode.ERROR, str(exc)))
        raise HTTPException(502, f"dependency {service} failed") from exc


async def work(lo_ms: int = 5, hi_ms: int = 25) -> None:
    await asyncio.sleep(random.uniform(lo_ms, hi_ms) / 1000)  # noqa: S311


# --- Business endpoints ----------------------------------------------------------------------
if SERVICE == "frontend":
    @app.get("/shop/browse")
    async def browse() -> dict[str, Any]:
        await work()
        return {"items": (await call("inventory", "GET", "/inventory/items"))["items"]}

    @app.post("/shop/checkout")
    async def shop_checkout() -> dict[str, Any]:
        await work()
        return await call("checkout", "POST", "/checkout",
                          json={"sku": random.choice(["A1", "B2", "C3"]), "qty": 1})  # noqa: S311

elif SERVICE == "checkout":
    @app.post("/checkout")
    async def checkout(order: dict[str, Any]) -> dict[str, Any]:
        await work()
        await call("inventory", "POST", "/inventory/reserve", json=order)
        charge = await call("payments", "POST", "/payments/charge", json={"amount": 42.0})
        log.info("order placed", extra={"sku": order.get("sku")})
        return {"status": "placed", "payment_id": charge["payment_id"]}

elif SERVICE == "payments":
    @app.post("/payments/charge")
    async def charge(body: dict[str, Any]) -> dict[str, Any]:
        await work(10, 40)
        return {"payment_id": f"pay_{random.randint(10**5, 10**6)}", "amount": body.get("amount")}  # noqa: S311

elif SERVICE == "inventory":
    @app.get("/inventory/items")
    async def items() -> dict[str, Any]:
        await work(3, 15)
        return {"items": [{"sku": s, "stock": random.randint(0, 50)} for s in ("A1", "B2", "C3")]}  # noqa: S311

    @app.post("/inventory/reserve")
    async def reserve(order: dict[str, Any]) -> dict[str, Any]:
        await work(3, 15)
        return {"reserved": order.get("sku"), "qty": order.get("qty", 1)}


# --- Admin & health --------------------------------------------------------------------------
@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok", "service": SERVICE}


@app.get("/admin/faults")
async def get_faults(x_admin_token: str | None = Header(None)) -> dict[str, float]:
    check_admin(x_admin_token)
    return active_fault()


@app.post("/admin/faults")
async def set_fault(fault: Fault, x_admin_token: str | None = Header(None)) -> dict[str, float]:
    check_admin(x_admin_token)
    _fault.update(error_rate=fault.error_rate, latency_ms=float(fault.latency_ms),
                  expires_at=time.time() + fault.duration_s)
    log.warning("fault flag enabled: %s", fault.model_dump())
    return _fault


@app.post("/admin/faults/clear")
async def clear_fault(x_admin_token: str | None = Header(None)) -> dict[str, Any]:
    check_admin(x_admin_token)
    _fault.update(error_rate=0.0, latency_ms=0.0, expires_at=0.0)
    log.warning("fault flag cleared by remediation")
    return {"cleared": True, "service": SERVICE}


FastAPIInstrumentor.instrument_app(app, excluded_urls="health,admin")
