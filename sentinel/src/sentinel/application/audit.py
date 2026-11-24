"""Append-only audit trail. Every state change by a human or an agent is recorded here."""

from __future__ import annotations

from typing import Any

from sqlalchemy.orm import Session

from sentinel.infrastructure.orm import AuditEventRow


def record(
    session: Session,
    *,
    actor: str,
    action: str,
    entity_type: str,
    entity_id: int | str,
    incident_id: int | None = None,
    details: dict[str, Any] | None = None,
) -> None:
    session.add(
        AuditEventRow(
            actor=actor,
            action=action,
            entity_type=entity_type,
            entity_id=str(entity_id),
            incident_id=incident_id,
            details=details or {},
        )
    )
