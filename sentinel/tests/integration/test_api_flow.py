"""Full API flow: webhook -> correlation -> agents -> HITL approval -> execution -> resolution."""

from __future__ import annotations

from datetime import timedelta

from fastapi.testclient import TestClient
from sqlalchemy import select

from sentinel.application.facade import SentinelApp
from sentinel.domain.topology import DependencyGraph
from sentinel.infrastructure.db import utcnow
from sentinel.infrastructure.orm import AuditEventRow, InvestigationRow, RemediationRow
from tests.conftest import WEBHOOK_TOKEN, ExecutorTransport, am_alert, am_payload, headers

WEBHOOK = "/api/v1/alerts/alertmanager"
AUTH = {"Authorization": f"Bearer {WEBHOOK_TOKEN}"}


def drain(client: TestClient) -> int:
    app: SentinelApp = client.app.state.sentinel  # type: ignore[attr-defined]
    return app.drain()


def test_end_to_end_incident_lifecycle(
    client: TestClient, executor_transport: ExecutorTransport
) -> None:
    # 1. Three cascading alerts arrive in two webhook calls -> one incident.
    r = client.post(
        WEBHOOK, json=am_payload(am_alert("frontend"), am_alert("checkout")), headers=AUTH
    )
    assert r.status_code == 200, r.text
    body = r.json()
    assert len(body["incidents_opened"]) == 1 and body["investigations_queued"]
    incident_id = body["incidents_opened"][0]
    r = client.post(WEBHOOK, json=am_payload(am_alert("payments", minutes_ago=3)), headers=AUTH)
    assert r.json()["incidents_opened"] == [] and r.json()["incidents_updated"] == [incident_id]

    # 2. Worker runs the agents.
    assert drain(client) >= 1
    inc = client.get(f"/api/v1/incidents/{incident_id}", headers=headers("viewer")).json()
    assert inc["status"] == "identified"
    assert inc["root_cause_service"] == "payments"
    assert set(inc["services"]) == {"frontend", "checkout", "payments"}
    assert inc["latest_investigation"]["status"] == "completed"
    assert inc["summary"]
    pending = [m for m in inc["remediations"] if m["status"] == "pending_approval"]
    flag = next(m for m in pending if m["action"] == "disable_fault_flag")

    report = client.get(f"/api/v1/incidents/{incident_id}/report", headers=headers("viewer"))
    assert report.status_code == 200 and "# Incident" in report.text

    # 3. Human in the loop: viewers cannot approve, operators can.
    r = client.post(
        f"/api/v1/remediations/{flag['id']}/approve", json={}, headers=headers("viewer")
    )
    assert r.status_code == 403
    assert executor_transport.requests == []
    r = client.post(
        f"/api/v1/remediations/{flag['id']}/approve",
        json={"comment": "flag flipped 5m ago"},
        headers=headers("operator"),
    )
    assert r.status_code == 200 and r.json()["status"] == "approved"
    assert r.json()["decided_by"] == "operator-user"
    # Double approval is rejected.
    assert (
        client.post(
            f"/api/v1/remediations/{flag['id']}/approve", json={}, headers=headers("operator")
        ).status_code
        == 409
    )

    # 4. Worker executes the approved action against the allow-listed URL.
    drain(client)
    assert [str(rq.url) for rq in executor_transport.requests] == [
        "http://payments:8080/admin/faults/clear"
    ]
    rem = client.get(
        f"/api/v1/incidents/{incident_id}/remediations", headers=headers("viewer")
    ).json()
    assert next(m for m in rem if m["id"] == flag["id"])["status"] == "succeeded"

    # 5. Alerts resolve -> incident auto-resolves.
    resolved = [am_alert(s, status="resolved") for s in ("frontend", "checkout", "payments")]
    r = client.post(WEBHOOK, json=am_payload(*resolved), headers=AUTH)
    assert r.json()["incidents_resolved"] == [incident_id]
    inc = client.get(f"/api/v1/incidents/{incident_id}", headers=headers("viewer")).json()
    assert inc["status"] == "resolved"
    # Proposals nobody approved are expired so they can never run against a resolved incident.
    leftover = [m for m in inc["remediations"] if m["id"] != flag["id"]]
    assert leftover
    assert all(m["status"] == "rejected" for m in leftover)
    assert all("expired" in m["decision_comment"] for m in leftover)

    # 6. Audit trail captures every step with actors.
    actions = [e["action"] for e in inc["timeline"]]
    for expected in (
        "incident.opened",
        "investigation.queued",
        "investigation.completed",
        "remediation.proposed",
        "remediation.approved",
        "remediation.executed",
        "incident.auto_resolved",
    ):
        assert expected in actions, expected
    audit = client.get("/api/v1/audit", headers=headers("admin"))
    assert audit.status_code == 200 and len(audit.json()) >= len(actions)
    assert client.get("/api/v1/audit", headers=headers("operator")).status_code == 403


def test_duplicate_webhook_is_idempotent(client: TestClient) -> None:
    payload = am_payload(am_alert("payments"))
    first = client.post(WEBHOOK, json=payload, headers=AUTH).json()
    second = client.post(WEBHOOK, json=payload, headers=AUTH).json()
    assert first["created_alerts"] == 1 and second["created_alerts"] == 0
    assert second["updated_alerts"] == 1 and second["incidents_opened"] == []
    alerts = client.get("/api/v1/alerts", headers=headers("viewer")).json()
    assert alerts["total"] == 1


def test_unrelated_service_opens_separate_incident(client: TestClient) -> None:
    a = client.post(WEBHOOK, json=am_payload(am_alert("payments")), headers=AUTH).json()
    b = client.post(WEBHOOK, json=am_payload(am_alert("billing-batch")), headers=AUTH).json()
    assert a["incidents_opened"] and b["incidents_opened"]
    assert a["incidents_opened"] != b["incidents_opened"]
    listing = client.get("/api/v1/incidents?status=active", headers=headers("viewer")).json()
    assert listing["total"] == 2
    only = client.get("/api/v1/incidents?service=billing-batch", headers=headers("viewer")).json()
    assert [i["services"] for i in only["items"]] == [["billing-batch"]]


def test_refiring_after_resolution_opens_new_incident(client: TestClient) -> None:
    first = client.post(WEBHOOK, json=am_payload(am_alert("payments")), headers=AUTH).json()
    client.post(WEBHOOK, json=am_payload(am_alert("payments", status="resolved")), headers=AUTH)
    again = client.post(WEBHOOK, json=am_payload(am_alert("payments")), headers=AUTH).json()
    assert again["incidents_opened"] and again["incidents_opened"] != first["incidents_opened"]


def test_generic_alert_and_manual_flows(client: TestClient) -> None:
    r = client.post(
        "/api/v1/alerts",
        json={"alertname": "SyntheticCheckFailed", "service": "checkout", "severity": "warning"},
        headers=headers("operator"),
    )
    assert r.status_code == 202
    incident_id = r.json()["incidents_opened"][0]
    # A queued investigation exists -> manual re-run conflicts.
    assert (
        client.post(
            f"/api/v1/incidents/{incident_id}/investigations", headers=headers("operator")
        ).status_code
        == 409
    )
    drain(client)
    r = client.post(f"/api/v1/incidents/{incident_id}/investigations", headers=headers("operator"))
    assert r.status_code == 202 and r.json()["trigger"] == "manual"
    drain(client)

    rems = client.get(
        "/api/v1/remediations?status=pending_approval", headers=headers("viewer")
    ).json()
    manual = next(m for m in rems if m["executor"] == "manual")
    client.post(
        f"/api/v1/remediations/{manual['id']}/reject",
        json={"comment": "not needed"},
        headers=headers("operator"),
    )
    assert (
        client.post(
            f"/api/v1/remediations/{manual['id']}/complete",
            json={"success": True},
            headers=headers("operator"),
        ).status_code
        == 409
    )

    other = next(m for m in rems if m["executor"] == "manual" and m["id"] != manual["id"])
    client.post(f"/api/v1/remediations/{other['id']}/approve", json={}, headers=headers("operator"))
    r = client.post(
        f"/api/v1/remediations/{other['id']}/complete",
        json={"success": True, "notes": "rolled back v1.4.2"},
        headers=headers("operator"),
    )
    assert r.status_code == 200 and r.json()["status"] == "succeeded"

    r = client.post(
        f"/api/v1/incidents/{incident_id}/resolve",
        json={"note": "fixed"},
        headers=headers("operator"),
    )
    assert r.status_code == 200 and r.json()["status"] == "resolved"
    assert (
        client.post(
            f"/api/v1/incidents/{incident_id}/resolve",
            json={"note": "x"},
            headers=headers("operator"),
        ).status_code
        == 409
    )


def test_failed_execution_is_recorded(
    client: TestClient, executor_transport: ExecutorTransport
) -> None:
    executor_transport.status = 500
    r = client.post(WEBHOOK, json=am_payload(am_alert("payments")), headers=AUTH).json()
    drain(client)
    inc = client.get(
        f"/api/v1/incidents/{r['incidents_opened'][0]}", headers=headers("viewer")
    ).json()
    flag = next(m for m in inc["remediations"] if m["action"] == "disable_fault_flag")
    client.post(f"/api/v1/remediations/{flag['id']}/approve", json={}, headers=headers("admin"))
    drain(client)
    rem = client.get("/api/v1/remediations", headers=headers("viewer")).json()
    row = next(m for m in rem if m["id"] == flag["id"])
    assert row["status"] == "failed" and row["result"]["status_code"] == 500


def test_worker_lease_recovery(client: TestClient) -> None:
    app: SentinelApp = client.app.state.sentinel  # type: ignore[attr-defined]
    client.post(WEBHOOK, json=am_payload(am_alert("payments")), headers=AUTH)
    inv_id = app.investigations.claim_next()
    assert inv_id is not None
    assert app.investigations.claim_next() is None  # leased: nobody else can take it
    # Simulate a crashed worker: lease expires.
    with app.c.db.session() as s:
        s.get_one(InvestigationRow, inv_id).lease_expires_at = utcnow() - timedelta(seconds=1)
    assert app.investigations.claim_next() == inv_id
    with app.c.db.session() as s:
        assert s.get_one(InvestigationRow, inv_id).attempts == 2
        s.get_one(InvestigationRow, inv_id).lease_expires_at = utcnow() - timedelta(seconds=1)
        s.get_one(InvestigationRow, inv_id).attempts = 3
    assert app.investigations.claim_next() is None  # exceeded attempts -> failed
    with app.c.db.session() as s:
        assert s.get_one(InvestigationRow, inv_id).status == "failed"


def _approved_flag(client: TestClient) -> int:
    """Open a payments incident, run the agents, approve disable_fault_flag. Returns its id."""
    r = client.post(WEBHOOK, json=am_payload(am_alert("payments")), headers=AUTH).json()
    drain(client)
    inc = client.get(
        f"/api/v1/incidents/{r['incidents_opened'][0]}", headers=headers("viewer")
    ).json()
    flag = next(m for m in inc["remediations"] if m["action"] == "disable_fault_flag")
    r2 = client.post(
        f"/api/v1/remediations/{flag['id']}/approve", json={}, headers=headers("operator")
    )
    assert r2.status_code == 200, r2.text
    return int(flag["id"])


def test_execution_lease_expiry_marks_unknown_outcome(
    client: TestClient, executor_transport: ExecutorTransport
) -> None:
    app: SentinelApp = client.app.state.sentinel  # type: ignore[attr-defined]
    rid = _approved_flag(client)
    assert app.remediations.claim_next_approved() == rid  # worker claims, then "crashes"
    with app.c.db.session() as s:
        row = s.get_one(RemediationRow, rid)
        assert row.status == "executing" and row.lease_expires_at is not None
    assert app.remediations.claim_next_approved() is None  # lease still valid: not reaped
    with app.c.db.session() as s:
        s.get_one(RemediationRow, rid).lease_expires_at = utcnow() - timedelta(seconds=1)
    assert app.remediations.claim_next_approved() is None  # reaped, never re-executed
    with app.c.db.session() as s:
        row = s.get_one(RemediationRow, rid)
        assert row.status == "failed" and row.result is not None
        assert row.result["error"] == "unknown_outcome" and row.lease_expires_at is None
        actions = set(
            s.scalars(select(AuditEventRow.action).where(AuditEventRow.entity_id == str(rid)))
        )
    assert "remediation.unknown_outcome" in actions
    assert executor_transport.requests == []  # the abandoned call is not replayed


def test_successful_execution_clears_lease(
    client: TestClient, executor_transport: ExecutorTransport
) -> None:
    app: SentinelApp = client.app.state.sentinel  # type: ignore[attr-defined]
    rid = _approved_flag(client)
    drain(client)
    with app.c.db.session() as s:
        row = s.get_one(RemediationRow, rid)
        assert row.status == "succeeded" and row.lease_expires_at is None
    assert len(executor_transport.requests) == 1


def test_execution_refuses_trace_discovered_service(
    client: TestClient, executor_transport: ExecutorTransport
) -> None:
    """A service seen only in telemetry (service graph) must never become an action target."""
    app: SentinelApp = client.app.state.sentinel  # type: ignore[attr-defined]
    rogue = DependencyGraph()
    rogue.add_edge("payments", "rogue-svc")
    app.topology._discovered = (float("inf"), rogue)  # pin the discovery cache
    assert "rogue-svc" in app.topology.discovered().nodes
    rid = _approved_flag(client)
    with app.c.db.session() as s:  # simulate an RCA that blamed the telemetry-only service
        s.get_one(RemediationRow, rid).service = "rogue-svc"
    drain(client)
    with app.c.db.session() as s:
        row = s.get_one(RemediationRow, rid)
        assert row.status == "failed" and row.result is not None
        assert "not in catalog" in row.result["error"]
    assert executor_transport.requests == []  # no request, so the admin token never leaves
