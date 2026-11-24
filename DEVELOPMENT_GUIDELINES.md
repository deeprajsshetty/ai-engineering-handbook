# Development guidelines

## Setup

```bash
make setup
```

`make setup` runs `uv sync --extra dev` in `sentinel/` and installs the pre-commit hooks. Then run
the checks:

```bash
make lint
make test
```

Python 3.12 (CI). The code also runs on 3.13.

## Code structure rules

| Layer | May import | Must not |
|---|---|---|
| `domain/` | stdlib, pydantic | Anything with I/O (db, http, llm), other Sentinel layers |
| `infrastructure/`, `llm/`, `knowledge/` | `domain`, `config`, `observability` | `api`, `application` |
| `agents/` | `domain`, adapters via `AgentDeps` | Direct DB access |
| `application/` | All of the above | FastAPI types |
| `api/` | `application`, `domain` | Business logic (keep handlers thin) |

* **Pure first.** If logic can be a pure function, put it in `domain/` and test it directly.
* **Dependency injection.** Construct adapters in `Container.build()`. Tests pass fakes; never
  monkeypatch globals unless you are testing that the code handles a failure.
* **Agents** are `fn(state, deps) -> dict`. For expected failures, append to `data_gaps` instead
  of raising.
* **Anything that changes production** goes through `actions.yaml` and the remediation state
  machine. There is no other path.

## Coding standards

* `ruff` (lint and format, line length 100) and `mypy` must pass. CI enforces both.
* Use type hints on all public functions. Prefer `pydantic` models or dataclasses to bare dicts at
  layer boundaries.
* Use structured logging: `log.info("message", extra={"incident_id": 7})`. Never log secrets,
  tokens or full payloads. Use `SecretStr` for secret settings.
* Every network call needs a timeout and a bounded retry (`tenacity`), and must fail into a defined
  state.
* Write comments that explain *why*, not *what*. Keep docstrings short.
* Use the `SENTINEL_` prefix for new settings. Add each new setting to
  [PROJECT_KNOWLEDGE.md §6](PROJECT_KNOWLEDGE.md#6-configuration-reference) and `.env.example`.

## Database changes

1. Change `infrastructure/orm.py`.
2. Generate a migration:

   ```bash
   uv run alembic revision --autogenerate -m "add change_events"
   ```

3. Review the generated file. Use the self-contained `TS` and `JSON_T` types, and never import app
   code in migrations.
4. Prefer expand/contract changes: add a nullable column, backfill it, then constrain it in a
   later release.
5. `tests/integration/test_migrations.py` and CI `alembic check` must pass.

## API changes

* Additive changes (new optional fields, new endpoints) are fine in v1.
* Removing or renaming a field, or changing its meaning, requires a `/api/v2` route. Keep v1 for
  at least one release.
* Set an `operation_id` on every route. It becomes the MCP tool name, and the contract test checks
  it.
* Update [API_DOCUMENTATION.md](API_DOCUMENTATION.md).

## Testing standards

| Kind | Location | Rule |
|---|---|---|
| Unit | `tests/unit/` | Fast, no network, no Docker |
| Scenario evals | `tests/unit/test_agents.py` + `tests/fakes.py` | Every RCA bug fix adds a scenario |
| Integration | `tests/integration/` | Run on SQLite locally; on PostgreSQL too when `SENTINEL_TEST_DATABASE_URL` is set (CI) |
| E2E | `tests/e2e/` (marker `e2e`) | Run against the Compose stack; deselected by default |
| Load | `tests/load/locustfile.py` | Before releases and after performance-sensitive changes |

* Coverage must stay at or above 85% (CI gate). It is currently 93%.
* Test behaviour through public interfaces. Assert on status codes, states and audit events, not
  on internals.

To run the integration tests against PostgreSQL locally:

```bash
docker run -d --rm -p 5433:5432 -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=t postgres:17.6-alpine
```

```bash
SENTINEL_TEST_DATABASE_URL=postgresql+psycopg://postgres:pw@localhost:5433/t uv run pytest tests/integration
```

## Branching and commits

* Trunk-based: `master` is always deployable. Use short-lived branches: `feat/…`, `fix/…`,
  `docs/…`, `chore/…`.
* Use [Conventional Commits](https://www.conventionalcommits.org/), for example
  `feat(agents): add saturation signal`.
* Squash-merge after review and green CI. Label a PR `run-e2e` when it touches alerting, agents or
  deployment.
* Releases use SemVer tags (`v1.1.0`). The release workflow publishes signed images. Update
  [CHANGELOG.md](CHANGELOG.md).

## Pull request checklist

The PR template has this checklist:

- tests;
- docs updated;
- config documented;
- no secrets;
- migrations reviewed.

Reviewers check layering, failure handling, and that any new action path is behind approval.

## Notebooks (handbook)

* Keep keys out of cells: use `load_dotenv()` with `handbook/.env`.
* The pre-commit `nbstripout` hook removes outputs before commit, because outputs are where keys
  leaked before.
* New files over 50 MB are blocked (`check-added-large-files`). Several existing handbook PDFs and notebooks are 10-43 MB; move them to Git LFS when convenient.

## Documentation

Docs live next to the code they describe and are updated in the same PR. See the mapping in
[PROJECT_KNOWLEDGE.md §9](PROJECT_KNOWLEDGE.md#9-keeping-this-knowledge-current). Diagrams are
Mermaid in `docs/diagrams/README.md`. Dashboards are generated by
`deploy/compose/grafana/build_dashboards.py`; do not hand-edit the JSON.
