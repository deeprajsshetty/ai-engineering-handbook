"""Executes approved remediation actions.

Only actions from the allow-listed catalog can run, only against services in the service
catalog (prevents SSRF via crafted service names), and only after the remediation row is in
the ``approved`` state. Every execution is idempotent from Sentinel's side: a remediation row
is executed at most ``max_attempts`` times and each attempt is audited.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any

import httpx

from sentinel.config import Settings
from sentinel.infrastructure.catalog import SERVICE_NAME_RE, ActionSpec

log = logging.getLogger(__name__)


@dataclass
class ExecutionResult:
    ok: bool
    detail: dict[str, Any]


class ActionExecutor:
    def __init__(self, settings: Settings, client: httpx.Client | None = None) -> None:
        self.settings = settings
        self.client = client or httpx.Client(timeout=10.0)

    def execute(self, spec: ActionSpec, service: str, known_services: set[str]) -> ExecutionResult:
        if spec.executor != "http":
            raise ValueError(f"action {spec.name} is manual and cannot be executed automatically")
        if not SERVICE_NAME_RE.match(service) or service not in known_services:
            return ExecutionResult(False, {"error": f"service {service!r} not in catalog"})
        if spec.url is None:
            return ExecutionResult(False, {"error": f"action {spec.name} has no url"})
        url = spec.url.format(service=service)
        headers = {"User-Agent": "sentinel-remediation/1.0"}
        token = self.settings.action_token.get_secret_value()
        if token:
            headers["X-Admin-Token"] = token
        try:
            resp = self.client.request(spec.method, url, headers=headers)
        except httpx.HTTPError as exc:
            log.warning("remediation call failed", extra={"action": spec.name, "url": url})
            return ExecutionResult(False, {"error": f"{type(exc).__name__}: {exc}", "url": url})
        body: Any
        try:
            body = resp.json()
        except ValueError:
            body = resp.text[:500]
        return ExecutionResult(
            200 <= resp.status_code < 300,
            {"status_code": resp.status_code, "url": url, "response": body},
        )
