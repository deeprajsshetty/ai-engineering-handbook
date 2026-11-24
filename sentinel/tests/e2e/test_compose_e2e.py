"""End-to-end test against the running docker compose stack (deploy/compose).

    scripts/bootstrap-env.sh && (cd deploy/compose && docker compose up -d --build)
    pytest -m e2e tests/e2e -s

Flow: inject a fault into payments -> Prometheus alert -> Alertmanager webhook -> Sentinel
incident -> agents identify payments -> operator approves `disable_fault_flag` -> Sentinel calls
payments' admin API -> error rate recovers -> alerts resolve -> incident auto-resolves.
"""

from __future__ import annotations

import os
import time
from collections.abc import Callable
from pathlib import Path
from typing import Any

import httpx
import pytest

pytestmark = pytest.mark.e2e

COMPOSE_DIR = Path(__file__).resolve().parents[3] / "deploy" / "compose"
API = os.getenv("SENTINEL_URL", "http://localhost:8000")
PAYMENTS_ADMIN = os.getenv("PAYMENTS_URL", "http://localhost:8082")


def _env(path: Path) -> dict[str, str]:
    out = {}
    for line in path.read_text().splitlines():
        if "=" in line and not line.lstrip().startswith("#"):
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip()
    return out


@pytest.fixture(scope="module")
def creds() -> dict[str, str]:
    if not (COMPOSE_DIR / ".env.keys").exists():
        pytest.skip("compose stack not bootstrapped (run scripts/bootstrap-env.sh)")
    return {**_env(COMPOSE_DIR / ".env"), **_env(COMPOSE_DIR / ".env.keys")}


def wait_for(fn: Callable[[], Any], timeout: float, interval: float = 5, what: str = "") -> Any:
    deadline = time.time() + timeout
    last: Any = None
    while time.time() < deadline:
        last = fn()
        if last:
            return last
        time.sleep(interval)
    raise AssertionError(f"timed out after {timeout}s waiting for {what}; last={last!r}")


def test_fault_to_remediation(creds: dict[str, str]) -> None:
    op = {"X-API-Key": creds["SENTINEL_OPERATOR_KEY"]}
    admin = {"X-Admin-Token": creds["DEMO_ADMIN_TOKEN"]}
    with httpx.Client(base_url=API, timeout=10) as api:
        assert api.get("/health/ready").json()["checks"]["database"] == "ok"
        before = {i["id"] for i in api.get("/api/v1/incidents", headers=op).json()["items"]}

        # 1. Inject the fault (the "bad feature flag").
        httpx.post(
            f"{PAYMENTS_ADMIN}/admin/faults",
            headers=admin,
            json={"error_rate": 0.7, "duration_s": 900},
        ).raise_for_status()
        t0 = time.time()
        print("\nfault injected into payments")

        # 2. An incident with an identified root cause appears.
        def identified() -> dict[str, Any] | None:
            items = api.get("/api/v1/incidents?status=active", headers=op).json()["items"]
            for i in items:
                if i["id"] not in before and i["root_cause_service"]:
                    return i
            return None

        inc = wait_for(identified, 300, what="incident with root cause")
        print(
            f"incident #{inc['id']} identified after {time.time() - t0:.0f}s: "
            f"root cause={inc['root_cause_service']} services={inc['services']}"
        )
        assert inc["root_cause_service"] == "payments"
        detail = api.get(f"/api/v1/incidents/{inc['id']}", headers=op).json()
        result = detail["latest_investigation"]["result"]
        assert result["evidence_ok"] and result["signals_with_data"] > 0
        assert result["traces"]["traces_analyzed"] > 0, result["data_gaps"]
        assert result["hypotheses"][0]["service"] == "payments"
        print("hypotheses:", [(h["service"], h["confidence"]) for h in result["hypotheses"][:3]])
        print("data gaps:", result["data_gaps"])

        # 3. Human approves the recommended low-risk remediation.
        flag = next(
            r
            for r in detail["remediations"]
            if r["action"] == "disable_fault_flag" and r["service"] == "payments"
        )
        assert flag["status"] == "pending_approval"
        r = api.post(
            f"/api/v1/remediations/{flag['id']}/approve",
            json={"comment": "e2e: approve flag rollback"},
            headers=op,
        )
        assert r.status_code == 200, r.text

        def executed() -> dict[str, Any] | None:
            rems = api.get(f"/api/v1/incidents/{inc['id']}/remediations", headers=op).json()
            row = next(x for x in rems if x["id"] == flag["id"])
            return row if row["status"] in {"succeeded", "failed"} else None

        row = wait_for(executed, 60, 2, what="remediation execution")
        assert row["status"] == "succeeded", row
        faults = httpx.get(f"{PAYMENTS_ADMIN}/admin/faults", headers=admin).json()
        assert faults["error_rate"] == 0
        print(f"remediation succeeded after {time.time() - t0:.0f}s")

        # 4. Alerts resolve and Sentinel auto-resolves the incident.
        def resolved() -> bool:
            i = api.get(f"/api/v1/incidents/{inc['id']}", headers=op).json()
            return i["status"] == "resolved"

        wait_for(resolved, 420, 10, what="incident auto-resolution")
        print(f"incident resolved after {time.time() - t0:.0f}s")
        report = api.get(f"/api/v1/incidents/{inc['id']}/report", headers=op).text
        assert "payments" in report
