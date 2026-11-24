"""initial schema

Revision ID: 0001
Revises: (none)
Create Date: 2026-11-10 00:35:23.162041
"""

from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0001"
down_revision = None
branch_labels = None
depends_on = None

# Self-contained column types: migrations must not import application code.
TS = sa.DateTime(timezone=True)
JSON_T = sa.JSON().with_variant(postgresql.JSONB(), "postgresql")


def upgrade() -> None:
    op.create_table(
        "audit_events",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("ts", TS, nullable=False),
        sa.Column("actor", sa.String(length=64), nullable=False),
        sa.Column("action", sa.String(length=64), nullable=False),
        sa.Column("entity_type", sa.String(length=32), nullable=False),
        sa.Column("entity_id", sa.String(length=64), nullable=False),
        sa.Column("incident_id", sa.Integer(), nullable=True),
        sa.Column("details", JSON_T, nullable=False),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index(
        op.f("ix_audit_events_incident_id"), "audit_events", ["incident_id"], unique=False
    )
    op.create_index(op.f("ix_audit_events_ts"), "audit_events", ["ts"], unique=False)
    op.create_table(
        "incidents",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("title", sa.String(length=300), nullable=False),
        sa.Column("status", sa.String(length=20), nullable=False),
        sa.Column("severity", sa.String(length=16), nullable=False),
        sa.Column("services", JSON_T, nullable=False),
        sa.Column("started_at", TS, nullable=False),
        sa.Column("last_activity_at", TS, nullable=False),
        sa.Column("resolved_at", TS, nullable=True),
        sa.Column("resolution_note", sa.Text(), nullable=True),
        sa.Column("root_cause_service", sa.String(length=64), nullable=True),
        sa.Column("root_cause_confidence", sa.Double(), nullable=True),
        sa.Column("summary", sa.Text(), nullable=True),
        sa.Column("created_at", TS, nullable=False),
        sa.Column("updated_at", TS, nullable=False),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index(op.f("ix_incidents_status"), "incidents", ["status"], unique=False)
    op.create_table(
        "services",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("name", sa.String(length=64), nullable=False),
        sa.Column("team", sa.String(length=100), nullable=True),
        sa.Column("tier", sa.String(length=20), nullable=True),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("dependencies", JSON_T, nullable=False),
        sa.Column("runbook_url", sa.String(length=500), nullable=True),
        sa.Column("created_at", TS, nullable=False),
        sa.Column("updated_at", TS, nullable=False),
        sa.PrimaryKeyConstraint("id"),
        sa.UniqueConstraint("name"),
    )
    op.create_table(
        "alerts",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("fingerprint", sa.String(length=128), nullable=False),
        sa.Column("alertname", sa.String(length=200), nullable=False),
        sa.Column("service", sa.String(length=64), nullable=False),
        sa.Column("severity", sa.String(length=16), nullable=False),
        sa.Column("status", sa.String(length=16), nullable=False),
        sa.Column("summary", sa.Text(), nullable=False),
        sa.Column("description", sa.Text(), nullable=False),
        sa.Column("labels", JSON_T, nullable=False),
        sa.Column("annotations", JSON_T, nullable=False),
        sa.Column("source", sa.String(length=32), nullable=False),
        sa.Column("generator_url", sa.Text(), nullable=True),
        sa.Column("starts_at", TS, nullable=False),
        sa.Column("ends_at", TS, nullable=True),
        sa.Column("incident_id", sa.Integer(), nullable=True),
        sa.Column("received_at", TS, nullable=False),
        sa.Column("updated_at", TS, nullable=False),
        sa.ForeignKeyConstraint(["incident_id"], ["incidents.id"], ondelete="SET NULL"),
        sa.PrimaryKeyConstraint("id"),
        sa.UniqueConstraint("fingerprint"),
    )
    op.create_index(op.f("ix_alerts_incident_id"), "alerts", ["incident_id"], unique=False)
    op.create_index(op.f("ix_alerts_service"), "alerts", ["service"], unique=False)
    op.create_index(op.f("ix_alerts_status"), "alerts", ["status"], unique=False)
    op.create_table(
        "investigations",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("incident_id", sa.Integer(), nullable=False),
        sa.Column("status", sa.String(length=16), nullable=False),
        sa.Column("trigger", sa.String(length=32), nullable=False),
        sa.Column("requested_by", sa.String(length=64), nullable=False),
        sa.Column("attempts", sa.Integer(), nullable=False),
        sa.Column("lease_expires_at", TS, nullable=True),
        sa.Column("queued_at", TS, nullable=False),
        sa.Column("started_at", TS, nullable=True),
        sa.Column("finished_at", TS, nullable=True),
        sa.Column("duration_ms", sa.Integer(), nullable=True),
        sa.Column("llm_provider", sa.String(length=32), nullable=True),
        sa.Column("error", sa.Text(), nullable=True),
        sa.Column("result", JSON_T, nullable=True),
        sa.ForeignKeyConstraint(["incident_id"], ["incidents.id"], ondelete="CASCADE"),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index(
        op.f("ix_investigations_incident_id"), "investigations", ["incident_id"], unique=False
    )
    op.create_index(
        "ix_investigations_status_queued", "investigations", ["status", "queued_at"], unique=False
    )
    op.create_table(
        "remediations",
        sa.Column("id", sa.Integer(), nullable=False),
        sa.Column("incident_id", sa.Integer(), nullable=False),
        sa.Column("investigation_id", sa.Integer(), nullable=True),
        sa.Column("action", sa.String(length=64), nullable=False),
        sa.Column("service", sa.String(length=64), nullable=False),
        sa.Column("params", JSON_T, nullable=False),
        sa.Column("risk", sa.String(length=16), nullable=False),
        sa.Column("executor", sa.String(length=16), nullable=False),
        sa.Column("rationale", sa.Text(), nullable=False),
        sa.Column("runbook", sa.String(length=200), nullable=True),
        sa.Column("status", sa.String(length=32), nullable=False),
        sa.Column("decided_by", sa.String(length=64), nullable=True),
        sa.Column("decided_at", TS, nullable=True),
        sa.Column("decision_comment", sa.Text(), nullable=True),
        sa.Column("attempts", sa.Integer(), nullable=False),
        sa.Column("executed_at", TS, nullable=True),
        sa.Column("result", JSON_T, nullable=True),
        sa.Column("created_at", TS, nullable=False),
        sa.Column("updated_at", TS, nullable=False),
        sa.ForeignKeyConstraint(["incident_id"], ["incidents.id"], ondelete="CASCADE"),
        sa.ForeignKeyConstraint(["investigation_id"], ["investigations.id"], ondelete="SET NULL"),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index(
        op.f("ix_remediations_incident_id"), "remediations", ["incident_id"], unique=False
    )
    op.create_index(op.f("ix_remediations_status"), "remediations", ["status"], unique=False)


def downgrade() -> None:
    op.drop_index(op.f("ix_remediations_status"), table_name="remediations")
    op.drop_index(op.f("ix_remediations_incident_id"), table_name="remediations")
    op.drop_table("remediations")
    op.drop_index("ix_investigations_status_queued", table_name="investigations")
    op.drop_index(op.f("ix_investigations_incident_id"), table_name="investigations")
    op.drop_table("investigations")
    op.drop_index(op.f("ix_alerts_status"), table_name="alerts")
    op.drop_index(op.f("ix_alerts_service"), table_name="alerts")
    op.drop_index(op.f("ix_alerts_incident_id"), table_name="alerts")
    op.drop_table("alerts")
    op.drop_table("services")
    op.drop_index(op.f("ix_incidents_status"), table_name="incidents")
    op.drop_table("incidents")
    op.drop_index(op.f("ix_audit_events_ts"), table_name="audit_events")
    op.drop_index(op.f("ix_audit_events_incident_id"), table_name="audit_events")
    op.drop_table("audit_events")
