"""Operational CLI: ``sentinel <command>``.

Commands
  gen-key NAME ROLE   Generate a random API key and print the SENTINEL_API_KEYS entry for it.
  hash-key KEY        Print sha256(KEY) for building SENTINEL_API_KEYS by hand.
  migrate             Apply database migrations (alembic upgrade head).
  seed                Load the service catalog into the database.
  worker              Run the background worker.
  drain               Process queued work once, then exit (ops / debugging).
"""

from __future__ import annotations

import argparse
import secrets
import sys
from pathlib import Path
from typing import Any

from sentinel.api.security import hash_key
from sentinel.domain.models import Role


def _alembic_config() -> Any:
    from alembic.config import Config

    from sentinel.config import get_settings

    cfg = Config()
    cfg.set_main_option("script_location", str(Path(__file__).parent / "migrations"))
    cfg.set_main_option("sqlalchemy.url", get_settings().database_url.replace("%", "%%"))
    return cfg


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="sentinel", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    sub = parser.add_subparsers(dest="cmd", required=True)
    g = sub.add_parser("gen-key")
    g.add_argument("name")
    g.add_argument("role", choices=[r.value for r in Role])
    h = sub.add_parser("hash-key")
    h.add_argument("key")
    sub.add_parser("migrate")
    sub.add_parser("seed")
    sub.add_parser("worker")
    sub.add_parser("drain")
    args = parser.parse_args(argv)

    if args.cmd == "gen-key":
        key = "snt_" + secrets.token_urlsafe(32)
        print(f"API key (store it securely, shown once): {key}")
        print(f"SENTINEL_API_KEYS entry: {args.name}:{args.role}:{hash_key(key)}")
    elif args.cmd == "hash-key":
        print(hash_key(args.key))
    elif args.cmd == "migrate":
        from alembic import command

        command.upgrade(_alembic_config(), "head")
        print("migrations applied")
    elif args.cmd in {"seed", "drain"}:
        from sentinel.application.container import Container
        from sentinel.application.facade import SentinelApp
        from sentinel.config import get_settings

        app = SentinelApp(Container.build(get_settings()))
        app.bootstrap()
        if args.cmd == "drain":
            print(f"processed {app.drain()} work item(s)")
        else:
            print("service catalog seeded")
    elif args.cmd == "worker":
        from sentinel.worker import main as worker_main

        worker_main()
    return 0


if __name__ == "__main__":
    sys.exit(main())
