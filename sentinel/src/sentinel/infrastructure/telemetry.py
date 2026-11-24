"""Clients for the telemetry backends: Prometheus (metrics), Loki (logs), Tempo (traces).

Every call has a timeout and bounded retries with exponential backoff. Backends are optional:
a missing URL disables that backend and the agents degrade gracefully, recording the gap.
"""

from __future__ import annotations

import base64
import logging
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any, Protocol, TypeVar

import httpx
from tenacity import (
    Retrying,
    retry_if_exception,
    stop_after_attempt,
    wait_exponential,
)

from sentinel.config import Settings
from sentinel.domain.models import Point, SpanData
from sentinel.observability.metrics import TELEMETRY_QUERIES

log = logging.getLogger(__name__)
T = TypeVar("T")


class TelemetryError(RuntimeError):
    pass


class BackendUnavailable(TelemetryError):
    pass


@dataclass
class SeriesResult:
    labels: dict[str, str]
    points: list[Point]


@dataclass
class LogLine:
    ts_ns: int
    labels: dict[str, str]
    line: str


class TelemetryBackend(Protocol):
    """What the agents need from the telemetry layer (easy to fake in tests)."""

    def query_range(
        self, promql: str, start: float, end: float, step: int
    ) -> list[SeriesResult]: ...

    def query_instant(self, promql: str) -> list[SeriesResult]: ...

    def query_logs(self, logql: str, start: float, end: float, limit: int) -> list[LogLine]: ...

    def search_traces(self, traceql: str, start: float, end: float, limit: int) -> list[str]: ...

    def get_trace(self, trace_id: str) -> list[SpanData]: ...


def _retryable(exc: BaseException) -> bool:
    if isinstance(exc, httpx.TransportError):
        return True
    return isinstance(exc, httpx.HTTPStatusError) and exc.response.status_code >= 500


class HttpTelemetryBackend:
    def __init__(self, settings: Settings, client: httpx.Client | None = None) -> None:
        self.settings = settings
        self.client = client or httpx.Client(timeout=settings.telemetry_timeout_s)
        self.urls = {
            "prometheus": settings.prometheus_url,
            "loki": settings.loki_url,
            "tempo": settings.tempo_url,
        }

    # -- plumbing ------------------------------------------------------------------------------
    def _call(self, backend: str, fn: Callable[[str], T]) -> T:
        base = self.urls.get(backend)
        if not base:
            TELEMETRY_QUERIES.labels(backend, "disabled").inc()
            raise BackendUnavailable(f"{backend} is not configured")
        retryer = Retrying(
            stop=stop_after_attempt(self.settings.telemetry_retries + 1),
            wait=wait_exponential(multiplier=0.2, max=2),
            retry=retry_if_exception(_retryable),
            reraise=True,
        )
        try:
            result = retryer(fn, base.rstrip("/"))
        except httpx.HTTPError as exc:
            TELEMETRY_QUERIES.labels(backend, "error").inc()
            raise TelemetryError(f"{backend} query failed: {exc}") from exc
        TELEMETRY_QUERIES.labels(backend, "ok").inc()
        return result

    def _get_json(self, url: str, params: dict[str, Any]) -> Any:
        resp = self.client.get(url, params=params)
        resp.raise_for_status()
        return resp.json()

    # -- Prometheus ----------------------------------------------------------------------------
    @staticmethod
    def _parse_matrix(data: Any) -> list[SeriesResult]:
        out: list[SeriesResult] = []
        result = data.get("data", {}).get("result", [])
        for item in result:
            values = item.get("values") or ([item["value"]] if "value" in item else [])
            pts = []
            for ts, raw in values:
                try:
                    v = float(raw)
                except (TypeError, ValueError):
                    continue
                if v == v and v not in (float("inf"), float("-inf")):
                    pts.append(Point(ts=float(ts), value=v))
            out.append(SeriesResult(labels=item.get("metric", {}), points=pts))
        return out

    def query_range(self, promql: str, start: float, end: float, step: int) -> list[SeriesResult]:
        def go(base: str) -> list[SeriesResult]:
            data = self._get_json(
                f"{base}/api/v1/query_range",
                {"query": promql, "start": start, "end": end, "step": step},
            )
            return self._parse_matrix(data)

        return self._call("prometheus", go)

    def query_instant(self, promql: str) -> list[SeriesResult]:
        def go(base: str) -> list[SeriesResult]:
            return self._parse_matrix(self._get_json(f"{base}/api/v1/query", {"query": promql}))

        return self._call("prometheus", go)

    # -- Loki ----------------------------------------------------------------------------------
    def query_logs(self, logql: str, start: float, end: float, limit: int) -> list[LogLine]:
        def go(base: str) -> list[LogLine]:
            data = self._get_json(
                f"{base}/loki/api/v1/query_range",
                {
                    "query": logql,
                    "start": int(start * 1e9),
                    "end": int(end * 1e9),
                    "limit": limit,
                    "direction": "backward",
                },
            )
            lines: list[LogLine] = []
            for stream in data.get("data", {}).get("result", []):
                labels = stream.get("stream", {})
                for entry in stream.get("values", []):
                    lines.append(LogLine(ts_ns=int(entry[0]), labels=labels, line=str(entry[1])))
            return lines

        return self._call("loki", go)

    # -- Tempo ---------------------------------------------------------------------------------
    def search_traces(self, traceql: str, start: float, end: float, limit: int) -> list[str]:
        def go(base: str) -> list[str]:
            data = self._get_json(
                f"{base}/api/search",
                {"q": traceql, "start": int(start), "end": int(end), "limit": limit},
            )
            return [t["traceID"] for t in data.get("traces", []) if t.get("traceID")]

        return self._call("tempo", go)

    def get_trace(self, trace_id: str) -> list[SpanData]:
        def go(base: str) -> list[SpanData]:
            data = self._get_json(f"{base}/api/traces/{trace_id}", {})
            return parse_otlp_trace(trace_id, data)

        return self._call("tempo", go)


def _norm_id(raw: str | None) -> str | None:
    """Tempo returns base64 ids in OTLP-JSON; normalise to hex so ids compare consistently."""
    if not raw:
        return None
    if all(c in "0123456789abcdefABCDEF" for c in raw) and len(raw) in (16, 32):
        return raw.lower()
    try:
        return base64.b64decode(raw).hex()
    except (ValueError, TypeError):
        return raw


def parse_otlp_trace(trace_id: str, data: dict[str, Any]) -> list[SpanData]:
    """Flatten an OTLP-JSON trace (``batches`` or ``resourceSpans``) into :class:`SpanData`."""
    if "trace" in data:  # Tempo /api/v2 shape
        data = data["trace"]
    batches = data.get("batches") or data.get("resourceSpans") or []
    spans: list[SpanData] = []
    for batch in batches:
        attrs = batch.get("resource", {}).get("attributes", [])
        service = next(
            (
                a.get("value", {}).get("stringValue")
                for a in attrs
                if a.get("key") == "service.name"
            ),
            "unknown",
        )
        scopes = batch.get("scopeSpans") or batch.get("instrumentationLibrarySpans") or []
        for scope in scopes:
            for sp in scope.get("spans", []):
                code = sp.get("status", {}).get("code")
                spans.append(
                    SpanData(
                        trace_id=trace_id,
                        span_id=_norm_id(sp.get("spanId")) or "",
                        parent_id=_norm_id(sp.get("parentSpanId")),
                        service=service or "unknown",
                        name=sp.get("name", ""),
                        start_ns=int(sp.get("startTimeUnixNano", 0)),
                        end_ns=int(sp.get("endTimeUnixNano", 0)),
                        is_error=code in (2, "STATUS_CODE_ERROR"),
                    )
                )
    return spans
