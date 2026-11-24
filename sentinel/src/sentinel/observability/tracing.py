"""OpenTelemetry tracing and log export for Sentinel itself.

Standard OTEL_* environment variables are honoured (e.g. ``OTEL_EXPORTER_OTLP_ENDPOINT``).
Disabled unless ``SENTINEL_OTEL_ENABLED=true`` so tests and local runs stay quiet.
"""

from __future__ import annotations

import logging

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

from sentinel import __version__
from sentinel.config import Settings

log = logging.getLogger(__name__)
_configured = False


def configure_tracing(settings: Settings, service: str | None = None) -> bool:
    global _configured
    if not settings.otel_enabled or _configured:
        return False
    resource = Resource.create(
        {
            "service.name": service or settings.service_name,
            "service.version": __version__,
            "deployment.environment": settings.env,
        }
    )
    provider = TracerProvider(resource=resource)
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(provider)

    # Ship logs over OTLP as well (to Loki via the collector), keeping stdout JSON logs.
    try:
        from opentelemetry._logs import set_logger_provider
        from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
        from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
        from opentelemetry.sdk._logs.export import BatchLogRecordProcessor

        lp = LoggerProvider(resource=resource)
        lp.add_log_record_processor(BatchLogRecordProcessor(OTLPLogExporter()))
        set_logger_provider(lp)
        logging.getLogger().addHandler(LoggingHandler(level=logging.INFO, logger_provider=lp))
    except ImportError:  # pragma: no cover - optional
        log.warning("OTLP log exporter unavailable; logs go to stdout only")

    from opentelemetry.instrumentation.httpx import HTTPXClientInstrumentor

    HTTPXClientInstrumentor().instrument()
    _configured = True
    return True
