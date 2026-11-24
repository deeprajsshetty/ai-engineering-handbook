"""Migrations apply cleanly, match the ORM models, and downgrade."""

from __future__ import annotations

from pathlib import Path

from alembic import command
from alembic.autogenerate import compare_metadata
from alembic.config import Config
from alembic.runtime.migration import MigrationContext
from sqlalchemy import create_engine, inspect

import sentinel
from sentinel.infrastructure import orm  # noqa: F401
from sentinel.infrastructure.db import Base


def cfg(url: str) -> Config:
    c = Config()
    c.set_main_option("script_location", str(Path(sentinel.__file__).parent / "migrations"))
    c.set_main_option("sqlalchemy.url", url)
    return c


def test_upgrade_matches_models_and_downgrades(tmp_path: Path) -> None:
    url = f"sqlite:///{tmp_path / 'm.db'}"
    command.upgrade(cfg(url), "head")
    engine = create_engine(url)
    with engine.connect() as conn:
        diff = compare_metadata(
            MigrationContext.configure(conn, opts={"compare_type": True}), Base.metadata
        )
    assert diff == [], diff
    assert {
        "incidents",
        "alerts",
        "investigations",
        "remediations",
        "audit_events",
        "services",
    } <= set(inspect(engine).get_table_names())
    command.downgrade(cfg(url), "base")
    assert set(inspect(engine).get_table_names()) <= {"alembic_version"}
