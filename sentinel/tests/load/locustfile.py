"""Load test for the Sentinel API.

    make load        # 50 users, 60s, against http://localhost:8000

Mix: dashboards/console polling (reads) dominate; alert webhooks arrive in bursts. Alerts use
a dedicated `loadtest-*` service namespace so they never correlate with real incidents.
Targets (documented in docs/testing/test-strategy.md): p95 < 300ms reads, < 500ms webhook,
0% errors at 50 concurrent users on a laptop-sized deployment.
"""

from __future__ import annotations

import os
import random
from datetime import UTC, datetime

from locust import HttpUser, between, task

KEY = os.environ.get("SENTINEL_OPERATOR_KEY", "")


class ConsoleUser(HttpUser):
    wait_time = between(0.5, 2)

    def on_start(self) -> None:
        self.client.headers.update({"X-API-Key": KEY})

    @task(6)
    def list_incidents(self) -> None:
        self.client.get("/api/v1/incidents?status=active", name="/api/v1/incidents")

    @task(3)
    def stats(self) -> None:
        self.client.get("/api/v1/stats")

    @task(2)
    def dependency_map(self) -> None:
        self.client.get("/api/v1/services/dependency-map")

    @task(1)
    def alert(self) -> None:
        svc = f"loadtest-{random.randint(1, 20)}"
        now = datetime.now(UTC).isoformat()
        self.client.post(
            "/api/v1/alerts",
            name="/api/v1/alerts",
            json={
                "alertname": "LoadTestAlert",
                "service": svc,
                "severity": "info",
                "status": random.choice(["firing", "resolved"]),
                "starts_at": now,
                "fingerprint": f"load-{svc}",
            },
        )

    @task(1)
    def health(self) -> None:
        self.client.get("/health/ready")
