"""Authentication (API keys) and role-based authorisation.

* Keys are configured as ``name:role:sha256hex`` - only hashes are stored, never raw keys.
* Clients send ``X-API-Key: <key>`` or ``Authorization: Bearer <key>``.
* Comparison is constant-time. Roles are hierarchical: viewer < operator < admin.
* The Alertmanager webhook uses a separate bearer token with no other privileges.
"""

from __future__ import annotations

import hashlib
import hmac
from collections.abc import Callable
from dataclasses import dataclass

from fastapi import Depends, HTTPException, Request, status

from sentinel.config import Settings
from sentinel.domain.models import Role


@dataclass(frozen=True)
class Principal:
    name: str
    role: Role

    def allows(self, required: Role) -> bool:
        return self.role.rank >= required.rank


def hash_key(raw: str) -> str:
    return hashlib.sha256(raw.encode()).hexdigest()


@dataclass(frozen=True)
class KeyEntry:
    name: str
    role: Role
    digest: str


def parse_api_keys(spec: str) -> list[KeyEntry]:
    entries = []
    for item in filter(None, (p.strip() for p in spec.split(","))):
        try:
            name, role, digest = item.split(":")
            entries.append(KeyEntry(name, Role(role), digest.lower()))
        except ValueError as exc:
            raise ValueError(
                "SENTINEL_API_KEYS entries must look like name:role:sha256hex"
            ) from exc
    return entries


class Authenticator:
    def __init__(self, settings: Settings) -> None:
        self.entries = parse_api_keys(settings.api_keys)
        self.webhook_token = settings.webhook_token.get_secret_value()

    @staticmethod
    def _extract(request: Request) -> str | None:
        if key := request.headers.get("x-api-key"):
            return key.strip()
        auth = request.headers.get("authorization", "")
        if auth.lower().startswith("bearer "):
            return auth[7:].strip()
        return None

    def authenticate(self, request: Request) -> Principal | None:
        raw = self._extract(request)
        if not raw:
            return None
        digest = hash_key(raw)
        match = None
        for e in self.entries:  # iterate all entries: no early exit timing signal
            if hmac.compare_digest(e.digest, digest):
                match = e
        return Principal(match.name, match.role) if match else None

    def check_webhook(self, request: Request) -> bool:
        raw = self._extract(request)
        return bool(self.webhook_token and raw and hmac.compare_digest(raw, self.webhook_token))


def get_principal(request: Request) -> Principal:
    principal: Principal | None = getattr(request.state, "principal", None)
    if principal is None:
        principal = request.app.state.auth.authenticate(request)
    if principal is None:
        raise HTTPException(
            status.HTTP_401_UNAUTHORIZED,
            "missing or invalid API key",
            headers={"WWW-Authenticate": "Bearer"},
        )
    request.state.principal = principal
    return principal


def require(role: Role) -> Callable[..., Principal]:
    def dep(principal: Principal = Depends(get_principal)) -> Principal:
        if not principal.allows(role):
            raise HTTPException(status.HTTP_403_FORBIDDEN, f"requires role {role.value}")
        return principal

    return dep


def require_webhook(request: Request) -> str:
    auth: Authenticator = request.app.state.auth
    if auth.check_webhook(request):
        return "alertmanager"
    principal = auth.authenticate(request)
    if principal and principal.allows(Role.OPERATOR):
        return principal.name
    raise HTTPException(status.HTTP_401_UNAUTHORIZED, "invalid webhook credentials")
