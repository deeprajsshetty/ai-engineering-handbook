"""Read-only client for the discovery service (``discovery/``, the Go platform).

Sentinel uses it to add *infrastructure context* to an investigation: which pods, nodes and
databases sit under an application service, and whether any of them is missing or stale.

Boundaries:

* GET only. Sentinel never creates connections, starts discovery jobs or writes anything.
* Service names are validated with ``SERVICE_NAME_RE`` before they reach a query string.
* Resource IDs returned by the API are validated before they are put in a URL path.
* Everything the API returns is untrusted data (resource names come from the discovered estate).
* A missing URL disables the source, and failures surface as ``DiscoveryError`` so the agent
  records a data gap instead of failing the investigation.
"""

from __future__ import annotations

import logging
import re
from typing import Any, Protocol

import httpx
from tenacity import Retrying, retry_if_exception, stop_after_attempt, wait_exponential

from sentinel.config import Settings
from sentinel.domain.models import InfraEvidence, InfraResource
from sentinel.infrastructure.catalog import SERVICE_NAME_RE
from sentinel.observability.metrics import TELEMETRY_QUERIES

log = logging.getLogger(__name__)
_RESOURCE_ID_RE = re.compile(r"^[A-Za-z0-9_\-]{1,128}$")
_BACKEND = "discovery"


class DiscoveryError(RuntimeError):
    pass


class DiscoveryUnavailable(DiscoveryError):
    pass


class DiscoverySource(Protocol):
    """What the infrastructure agent needs (easy to fake in tests)."""

    @property
    def enabled(self) -> bool: ...

    def infra_for(self, service: str) -> InfraEvidence: ...


def _retryable(exc: BaseException) -> bool:
    if isinstance(exc, httpx.TransportError):
        return True
    return isinstance(exc, httpx.HTTPStatusError) and exc.response.status_code >= 500


def _resource(raw: dict[str, Any]) -> InfraResource | None:
    rid = str(raw.get("id", ""))
    if not _RESOURCE_ID_RE.match(rid):
        return None
    return InfraResource(
        id=rid,
        name=str(raw.get("name", ""))[:200],
        type=str(raw.get("type", "unknown"))[:64],
        status=str(raw.get("status", "active"))[:16],
        freshness=(str(raw["freshness"])[:16] if raw.get("freshness") else None),
    )


class HttpDiscoverySource:
    def __init__(self, settings: Settings, client: httpx.Client | None = None) -> None:
        self.settings = settings
        self.base = (settings.discovery_url or "").rstrip("/")
        self.client = client or httpx.Client(timeout=settings.discovery_timeout_s)
        self.types = [t.strip() for t in settings.discovery_service_types.split(",") if t.strip()]

    @property
    def enabled(self) -> bool:
        return bool(self.base)

    def _get(self, path: str, params: dict[str, Any]) -> Any:
        if not self.base:
            TELEMETRY_QUERIES.labels(_BACKEND, "disabled").inc()
            raise DiscoveryUnavailable("discovery is not configured")
        headers = {"Accept": "application/json"}
        token = self.settings.discovery_token.get_secret_value()
        if token:
            headers["Authorization"] = f"Bearer {token}"

        def go() -> Any:
            r = self.client.get(f"{self.base}{path}", params=params, headers=headers)
            r.raise_for_status()
            return r.json()

        try:
            result = Retrying(
                stop=stop_after_attempt(2),
                wait=wait_exponential(multiplier=0.2, max=1),
                retry=retry_if_exception(_retryable),
                reraise=True,
            )(go)
        except httpx.TransportError as exc:
            TELEMETRY_QUERIES.labels(_BACKEND, "error").inc()
            raise DiscoveryUnavailable(f"discovery unreachable: {exc}") from exc
        except (httpx.HTTPError, ValueError) as exc:
            TELEMETRY_QUERIES.labels(_BACKEND, "error").inc()
            raise DiscoveryError(f"discovery query failed: {exc}") from exc
        TELEMETRY_QUERIES.labels(_BACKEND, "ok").inc()
        return result

    def infra_for(self, service: str) -> InfraEvidence:
        if not SERVICE_NAME_RE.match(service):
            raise DiscoveryError(f"refusing to query unsafe service name {service!r}")
        evidence = InfraEvidence(service=service)
        seen: set[str] = set()
        for rtype in self.types:
            data = self._get("/api/v1/resources", {"q": service, "type": rtype, "limit": 20})
            for raw in data.get("items") or []:
                # `q` is a substring match: keep exact names only.
                res = _resource(raw)
                if res and res.name == service and res.id not in seen:
                    seen.add(res.id)
                    evidence.matched.append(res)
        for res in list(evidence.matched):
            sub = self._get(
                f"/api/v1/resources/{res.id}/neighborhood",
                {
                    "depth": self.settings.discovery_depth,
                    "direction": "both",
                    "maxNodes": self.settings.discovery_max_nodes,
                    "includeMissing": "true",
                },
            )
            evidence.truncated = evidence.truncated or bool(sub.get("truncated"))
            for raw in sub.get("nodes") or []:
                node = _resource(raw)
                if node and node.id not in seen:
                    seen.add(node.id)
                    evidence.neighbours.append(node)
        return evidence
