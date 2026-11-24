"""HTTP middleware: request ids, security headers, body limits, rate limiting, metrics."""

from __future__ import annotations

import logging
import threading
import time
import uuid
from collections.abc import Awaitable, Callable

from fastapi import Request, Response
from fastapi.responses import JSONResponse
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.routing import Match

from sentinel.observability.logging import request_id_var
from sentinel.observability.metrics import HTTP_LATENCY, HTTP_REQUESTS

log = logging.getLogger("sentinel.http")
EXEMPT_PATHS = ("/health", "/metrics")


class TokenBucket:
    """Per-client token bucket. In-process: limits are per replica (documented trade-off)."""

    def __init__(self, rate_per_minute: int, burst: int) -> None:
        self.rate = rate_per_minute / 60.0
        self.burst = float(burst)
        self._buckets: dict[str, tuple[float, float]] = {}
        self._lock = threading.Lock()

    def take(self, key: str) -> float:
        """Consume one token. Returns 0 if allowed, else seconds until a token is available."""
        now = time.monotonic()
        with self._lock:
            tokens, last = self._buckets.get(key, (self.burst, now))
            tokens = min(self.burst, tokens + (now - last) * self.rate)
            if tokens >= 1:
                self._buckets[key] = (tokens - 1, now)
                if len(self._buckets) > 10_000:  # bound memory under key churn
                    self._buckets.pop(next(iter(self._buckets)))
                return 0.0
            self._buckets[key] = (tokens, now)
            return (1 - tokens) / self.rate if self.rate > 0 else 60.0


def _route_template(request: Request) -> str:
    for route in request.app.router.routes:
        match, _ = route.matches(request.scope)
        if match is Match.FULL:
            return getattr(route, "path", "unmatched")
    return "unmatched"


def _error(status: int, code: str, message: str, rid: str, headers: dict | None = None) -> Response:
    return JSONResponse(
        {"error": {"code": code, "message": message, "request_id": rid}},
        status_code=status,
        headers=headers,
    )


class EdgeMiddleware(BaseHTTPMiddleware):
    def __init__(self, app: object, limiter: TokenBucket, max_bytes: int) -> None:
        super().__init__(app)  # type: ignore[arg-type]
        self.limiter = limiter
        self.max_bytes = max_bytes

    async def dispatch(
        self, request: Request, call_next: Callable[[Request], Awaitable[Response]]
    ) -> Response:
        rid = request.headers.get("x-request-id") or uuid.uuid4().hex
        token = request_id_var.set(rid[:64])
        t0 = time.perf_counter()
        route = _route_template(request)
        try:
            length = request.headers.get("content-length")
            if length and length.isdigit() and int(length) > self.max_bytes:
                response = _error(413, "payload_too_large", "request body too large", rid)
            elif not request.url.path.startswith(EXEMPT_PATHS) and (
                wait := self.limiter.take(self._client_key(request))
            ):
                response = _error(
                    429,
                    "rate_limited",
                    "too many requests",
                    rid,
                    {"Retry-After": str(max(1, int(wait + 0.999)))},
                )
            else:
                response = await call_next(request)
        except Exception:
            log.exception("unhandled error", extra={"route": route})
            response = _error(500, "internal_error", "internal server error", rid)
        finally:
            request_id_var.reset(token)
        elapsed = time.perf_counter() - t0
        HTTP_REQUESTS.labels(request.method, route, str(response.status_code)).inc()
        HTTP_LATENCY.labels(request.method, route).observe(elapsed)
        response.headers["X-Request-ID"] = rid
        response.headers["X-Content-Type-Options"] = "nosniff"
        response.headers["X-Frame-Options"] = "DENY"
        response.headers["Referrer-Policy"] = "no-referrer"
        response.headers.setdefault(
            "Content-Security-Policy",
            "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'",
        )
        if not request.url.path.startswith(EXEMPT_PATHS):
            log.info(
                "request",
                extra={
                    "method": request.method,
                    "route": route,
                    "status": response.status_code,
                    "duration_ms": round(elapsed * 1000, 1),
                    "request_id": rid,
                },
            )
        return response

    @staticmethod
    def _client_key(request: Request) -> str:
        # Key by credential when present (hash prefix, never the raw key), else by client IP.
        cred = request.headers.get("x-api-key") or request.headers.get("authorization")
        if cred:
            import hashlib

            return "k:" + hashlib.sha256(cred.encode()).hexdigest()[:16]
        return "ip:" + (request.client.host if request.client else "unknown")
