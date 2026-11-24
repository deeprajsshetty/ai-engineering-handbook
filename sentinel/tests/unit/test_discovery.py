"""Read-only client for the discovery service (discovery/, the Go platform)."""

from __future__ import annotations

import httpx
import pytest

from sentinel.config import Settings
from sentinel.infrastructure.discovery import (
    DiscoveryError,
    DiscoveryUnavailable,
    HttpDiscoverySource,
)

RESOURCES = {
    "items": [
        {"id": "res_pay", "name": "payments", "type": "k8s.deployment", "status": "active"},
        # `q` is a substring match on the server: this one must be ignored.
        {"id": "res_pay2", "name": "payments-legacy", "type": "k8s.deployment"},
        # Unsafe id must never reach a URL path.
        {"id": "../../admin", "name": "payments", "type": "k8s.deployment"},
    ]
}
NEIGHBORHOOD = {
    "rootId": "res_pay",
    "nodes": [
        {"id": "res_pay", "name": "payments", "type": "k8s.deployment", "status": "active"},
        {"id": "res_pod", "name": "payments-abc", "type": "k8s.pod", "freshness": "stale"},
        {"id": "res_db", "name": "payments-db", "type": "database.instance", "status": "missing"},
    ],
    "edges": [],
    "truncated": True,
}


def source(handler, **kw) -> tuple[HttpDiscoverySource, list[httpx.Request]]:  # type: ignore[no-untyped-def]
    seen: list[httpx.Request] = []

    def h(req: httpx.Request) -> httpx.Response:
        seen.append(req)
        return handler(req)

    settings = Settings(
        env="test",
        discovery_url="http://discovery:8080/",
        discovery_token="dev-viewer",
        discovery_service_types="k8s.deployment",
        **kw,
    )
    return HttpDiscoverySource(
        settings, client=httpx.Client(transport=httpx.MockTransport(h))
    ), seen


def happy(req: httpx.Request) -> httpx.Response:
    if req.url.path == "/api/v1/resources":
        return httpx.Response(200, json=RESOURCES)
    if req.url.path == "/api/v1/resources/res_pay/neighborhood":
        return httpx.Response(200, json=NEIGHBORHOOD)
    return httpx.Response(404, json={"code": "not_found"})


def test_maps_service_to_resources_and_flags_problems() -> None:
    src, seen = source(happy)
    ev = src.infra_for("payments")
    assert [r.id for r in ev.matched] == ["res_pay"]  # exact name only, unsafe id dropped
    assert {r.id for r in ev.neighbours} == {"res_pod", "res_db"}  # root not duplicated
    assert ev.truncated
    assert ev.issues == [
        "k8s.pod payments-abc is stale (not re-observed recently)",
        "database.instance payments-db is missing (no longer observed by discovery)",
    ]
    assert all(r.method == "GET" for r in seen)  # read-only
    assert seen[0].headers["authorization"] == "Bearer dev-viewer"
    assert seen[0].url.params["q"] == "payments" and seen[0].url.params["type"] == "k8s.deployment"
    nb = seen[1].url.params
    assert (nb["depth"], nb["direction"], nb["includeMissing"]) == ("2", "both", "true")


def test_no_match_returns_empty_evidence() -> None:
    src, seen = source(lambda r: httpx.Response(200, json={"items": []}))
    ev = src.infra_for("inventory")
    assert ev.matched == [] and ev.neighbours == [] and ev.issues == []
    assert len(seen) == 1  # no neighbourhood call without a match


def test_refuses_unsafe_service_name() -> None:
    src, seen = source(happy)
    with pytest.raises(DiscoveryError, match="unsafe"):
        src.infra_for("payments&type=x")
    assert seen == []


def test_disabled_without_url() -> None:
    src = HttpDiscoverySource(Settings(env="test"))
    assert not src.enabled
    with pytest.raises(DiscoveryUnavailable):
        src.infra_for("payments")


def test_server_error_is_retried_then_reported() -> None:
    src, seen = source(lambda r: httpx.Response(503))
    with pytest.raises(DiscoveryError) as exc:
        src.infra_for("payments")
    assert not isinstance(exc.value, DiscoveryUnavailable)
    assert len(seen) == 2  # one retry, bounded


def test_unreachable_is_unavailable() -> None:
    def boom(req: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused")

    src, _ = source(boom)
    with pytest.raises(DiscoveryUnavailable):
        src.infra_for("payments")


def test_auth_failure_is_an_error_not_retried() -> None:
    src, seen = source(lambda r: httpx.Response(401, json={"code": "unauthenticated"}))
    with pytest.raises(DiscoveryError):
        src.infra_for("payments")
    assert len(seen) == 1
