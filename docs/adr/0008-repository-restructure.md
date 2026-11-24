# ADR-0008: Repository restructure

**Status:** Accepted · **Date:** 2026-11-10

## Context
The repository root mixed eight numbered learning modules, loose data files from a pandas
tutorial, a Render blueprint pointing at a non-existent folder, and a 357 MB virtualenv. Several
notebooks contained hard-coded API keys.

## Decision
* Move learning modules under `handbook/` unchanged (relative paths inside each module still work);
  loose root data files move with the notebook that produces them into `handbook/000-python-data-foundations/`.
* Add `sentinel/`, `demo-services/`, `deploy/`, `docs/`, `scripts/` at the top level.
* Fix `render.yaml` to point at `handbook/001-fastapi-mcp-todo`.
* Redact keys from notebooks, ignore virtualenvs, databases, caches and `.env` files.

## Consequences
The existing `.venv` inside `handbook/001-fastapi-mcp-todo/` has absolute paths baked in and must be
recreated after the move (see [TROUBLESHOOTING.md](../../TROUBLESHOOTING.md)).
