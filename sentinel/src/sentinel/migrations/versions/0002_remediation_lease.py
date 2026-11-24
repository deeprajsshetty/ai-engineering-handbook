"""remediation execution lease

Revision ID: 0002
Revises: 0001
Create Date: 2026-11-24 00:00:00

Adds ``remediations.lease_expires_at`` so an ``executing`` row abandoned by a crashed worker
can be detected and marked ``failed`` (unknown outcome) instead of staying stuck forever.
Additive and nullable: safe to deploy before the code that uses it.
"""

from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0002"
down_revision = "0001"
branch_labels = None
depends_on = None


def upgrade() -> None:
    with op.batch_alter_table("remediations") as batch:
        batch.add_column(sa.Column("lease_expires_at", sa.DateTime(timezone=True), nullable=True))


def downgrade() -> None:
    with op.batch_alter_table("remediations") as batch:
        batch.drop_column("lease_expires_at")
