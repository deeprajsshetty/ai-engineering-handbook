"""Cross-cutting API behaviour: auth, validation, limits, health, metrics, MCP, topology."""

from __future__ import annotations

from fastapi.testclient import TestClient

from sentinel.api.app import create_app
from sentinel.application.container import Container
from sentinel.config import Settings
from tests.conftest import am_alert, am_payload, headers


def test_auth_required(client: TestClient) -> None:
    r = client.get("/api/v1/incidents")
    assert r.status_code == 401
    assert r.json()["error"]["code"] == "http_401"
    assert r.headers["X-Request-ID"]
    assert client.get("/api/v1/incidents", headers={"X-API-Key": "wrong"}).status_code == 401
    assert client.get("/api/v1/whoami", headers=headers("operator")).json() == {
        "name": "operator-user",
        "role": "operator",
    }
    bearer = {"Authorization": "Bearer viewer-key-123"}
    assert client.get("/api/v1/incidents", headers=bearer).status_code == 200


def test_webhook_rejects_bad_token_and_bad_payload(client: TestClient) -> None:
    url = "/api/v1/alerts/alertmanager"
    assert (
        client.post(url, json=am_payload(), headers={"Authorization": "Bearer nope"}).status_code
        == 401
    )
    # Operators may post to the webhook too (manual replays).
    assert client.post(url, json=am_payload(), headers=headers("operator")).status_code == 200
    bad = {"alerts": [{"labels": {"service": "x"}}]}  # missing startsAt
    r = client.post(url, json=bad, headers=headers("operator"))
    assert r.status_code == 422


def test_validation_error_shape(client: TestClient) -> None:
    r = client.post(
        "/api/v1/alerts",
        json={"alertname": "", "service": "bad name!"},
        headers=headers("operator"),
    )
    assert r.status_code == 422
    assert r.json()["error"]["code"] == "validation_error"
    assert (
        client.get("/api/v1/incidents?status=bogus", headers=headers("viewer")).status_code == 422
    )


def test_rate_limit_and_body_limit(settings: Settings, container: Container) -> None:
    tight = settings.model_copy(
        update={"rate_limit_per_minute": 60, "rate_limit_burst": 3, "max_request_bytes": 200}
    )
    with TestClient(create_app(tight, container)) as c:
        codes = [
            c.get("/api/v1/incidents", headers=headers("viewer")).status_code for _ in range(5)
        ]
        assert codes[:3] == [200, 200, 200] and codes[-1] == 429
        r = c.get("/api/v1/incidents", headers=headers("viewer"))
        assert int(r.headers["Retry-After"]) >= 1
        # Health checks are exempt from rate limiting.
        assert all(c.get("/health/live").status_code == 200 for _ in range(5))
        big = c.post(
            "/api/v1/alerts",
            content=b"x" * 500,
            headers={**headers("admin"), "content-type": "application/json"},
        )
        assert big.status_code == 413


def test_health_and_metrics(client: TestClient) -> None:
    assert client.get("/health/live").json()["status"] == "ok"
    ready = client.get("/health/ready").json()
    assert ready["checks"]["database"] == "ok"
    client.post(
        "/api/v1/alerts/alertmanager",
        json=am_payload(am_alert("payments")),
        headers=headers("operator"),
    )
    text = client.get("/metrics").text
    for metric in (
        "sentinel_http_requests_total",
        "sentinel_alerts_received_total",
        "sentinel_incidents_created_total",
        "sentinel_incidents_active",
    ):
        assert metric in text


def test_security_headers_and_console(client: TestClient) -> None:
    r = client.get("/")
    assert r.status_code == 200 and "Sentinel" in r.text
    assert r.headers["X-Frame-Options"] == "DENY"
    assert "default-src 'self'" in r.headers["Content-Security-Policy"]
    assert client.get("/static/console.js").status_code == 200


def test_services_topology_and_health(client: TestClient) -> None:
    names = [s["name"] for s in client.get("/api/v1/services", headers=headers("viewer")).json()]
    assert names == ["checkout", "frontend", "inventory", "payments"]
    dm = client.get("/api/v1/services/dependency-map", headers=headers("viewer")).json()
    assert {"caller": "checkout", "callee": "payments", "source": "catalog"} in dm["edges"]

    r = client.put(
        "/api/v1/services/search",
        json={"team": "discovery", "dependencies": ["inventory"]},
        headers=headers("operator"),
    )
    assert r.status_code == 403
    r = client.put(
        "/api/v1/services/search",
        json={"team": "discovery", "dependencies": ["inventory"]},
        headers=headers("admin"),
    )
    assert r.status_code == 200 and r.json()["dependencies"] == ["inventory"]
    assert (
        client.put("/api/v1/services/Bad_Name", json={}, headers=headers("admin")).status_code
        == 422
    )

    h = client.get("/api/v1/services/payments/health", headers=headers("viewer")).json()
    assert h["status"] == "degraded"  # fake telemetry reports a 45% error rate
    assert {s["kind"] for s in h["signals"]} == {"request_rate", "error_rate", "latency_p95"}


def test_openapi_contract(client: TestClient) -> None:
    spec = client.get("/openapi.json").json()
    ops = {op["operationId"] for path in spec["paths"].values() for op in path.values()}
    for required in (
        "ingest_alertmanager",
        "list_incidents",
        "get_incident",
        "approve_remediation",
        "reject_remediation",
        "get_dependency_map",
        "start_investigation",
    ):
        assert required in ops
    assert all(p.startswith(("/api/v1", "/health")) for p in spec["paths"] if p != "/mcp")


def test_mcp_endpoint_requires_auth(client: TestClient) -> None:
    init = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "test", "version": "1"},
        },
    }
    accept = {"Accept": "application/json, text/event-stream"}
    assert client.post("/mcp", json=init, headers=accept).status_code == 401
    r = client.post("/mcp", json=init, headers={**accept, **headers("viewer")})
    assert r.status_code == 200, r.text
