from __future__ import annotations

import os
from collections.abc import Iterator
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

import httpx
import pytest
from fastapi.testclient import TestClient
from sqlalchemy import text

from sentinel.api.app import create_app
from sentinel.api.security import hash_key
from sentinel.application.container import Container
from sentinel.application.facade import SentinelApp
from sentinel.config import Settings
from sentinel.infrastructure.actions import ActionExecutor
from sentinel.infrastructure.db import Base, Database
from sentinel.llm.providers import DeterministicProvider, FallbackProvider
from tests.fakes import FakeDiscovery, FakeTelemetry, Scenario, payments_failure

KEYS = {"admin": "admin-key-123", "operator": "operator-key-123", "viewer": "viewer-key-123"}
WEBHOOK_TOKEN = "webhook-token-abcdefghijklmnopqrstuvwxyz"


def headers(role: str) -> dict[str, str]:
    return {"X-API-Key": KEYS[role]}


@pytest.fixture(
    params=["sqlite"] + (["postgres"] if os.getenv("SENTINEL_TEST_DATABASE_URL") else [])
)
def database_url(request: pytest.FixtureRequest, tmp_path: Path) -> str:
    if request.param == "postgres":
        return os.environ["SENTINEL_TEST_DATABASE_URL"]
    return f"sqlite:///{tmp_path / 'test.db'}"


@pytest.fixture
def settings(database_url: str) -> Settings:
    return Settings(
        env="test",
        database_url=database_url,
        api_keys=",".join(f"{r}-user:{r}:{hash_key(k)}" for r, k in KEYS.items()),
        webhook_token=WEBHOOK_TOKEN,
        action_token="action-token",
        log_json=False,
        log_level="WARNING",
        rate_limit_per_minute=100_000,
        rate_limit_burst=10_000,
        mcp_enabled=True,
    )


class ExecutorTransport:
    """Records remediation HTTP calls and answers like the demo services' admin API."""

    def __init__(self) -> None:
        self.requests: list[httpx.Request] = []
        self.status = 200

    def __call__(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        return httpx.Response(self.status, json={"cleared": True})


@pytest.fixture
def scenario() -> Scenario:
    return payments_failure()


@pytest.fixture
def executor_transport() -> ExecutorTransport:
    return ExecutorTransport()


@pytest.fixture
def container(
    settings: Settings, scenario: Scenario, executor_transport: ExecutorTransport
) -> Iterator[Container]:
    db = Database(settings)
    if db.is_postgres:
        with db.engine.begin() as conn:
            conn.execute(text("DROP SCHEMA public CASCADE; CREATE SCHEMA public;"))
    Base.metadata.create_all(db.engine)
    c = Container.build(
        settings,
        db=db,
        telemetry=FakeTelemetry(scenario),
        llm=FallbackProvider(DeterministicProvider()),
        discovery=FakeDiscovery(enabled=False),
        executor=ActionExecutor(
            settings, client=httpx.Client(transport=httpx.MockTransport(executor_transport))
        ),
    )
    yield c
    db.engine.dispose()


@pytest.fixture
def sentinel(container: Container) -> SentinelApp:
    app = SentinelApp(container)
    app.bootstrap()
    return app


@pytest.fixture
def client(settings: Settings, container: Container) -> Iterator[TestClient]:
    app = create_app(settings, container)
    with TestClient(app) as c:
        yield c


def am_alert(
    service: str,
    alertname: str = "HighErrorRate",
    status: str = "firing",
    severity: str = "critical",
    minutes_ago: int = 2,
) -> dict[str, Any]:
    starts = datetime.now(UTC) - timedelta(minutes=minutes_ago)
    return {
        "status": status,
        "labels": {"alertname": alertname, "service": service, "severity": severity},
        "annotations": {"summary": f"{alertname} on {service}"},
        "startsAt": starts.isoformat().replace("+00:00", "Z"),
        "endsAt": "0001-01-01T00:00:00Z"
        if status == "firing"
        else datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        "fingerprint": f"fp-{alertname}-{service}",
        "generatorURL": "http://prometheus:9090/graph",
    }


def am_payload(*alerts: dict[str, Any]) -> dict[str, Any]:
    return {
        "version": "4",
        "status": "firing",
        "receiver": "sentinel",
        "alerts": list(alerts),
        "groupLabels": {},
        "commonLabels": {},
        "commonAnnotations": {},
        "externalURL": "",
    }
