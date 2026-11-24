# Sentinel

The AI-agentic observability platform: alert correlation, multi-agent root-cause analysis, and
human-approved remediation. Project overview: [../README.md](../README.md). Architecture:
[../ARCHITECTURE.md](../ARCHITECTURE.md).

```bash
uv sync --extra dev
```

Install dependencies (locked in `uv.lock`) with the command above, then run the checks and tests:

```bash
uv run ruff check src tests && uv run mypy
uv run pytest
uv run pytest -m e2e tests/e2e -s
```

`pytest` runs the unit and integration tests. The `e2e` run needs the stack up (`make up` from the
repository root).

CLI commands:

```bash
uv run sentinel migrate
uv run sentinel gen-key alice operator
uv run sentinel worker
```

`migrate` applies database migrations. `gen-key` creates an API key and its `SENTINEL_API_KEYS`
entry. `worker` runs the background agents and executor.

Start the API:

```bash
uv run uvicorn --factory sentinel.api.app:app_factory --reload
```

| Path | Contents |
|---|---|
| `src/sentinel/domain/` | Pure logic: anomaly detection, correlation, RCA ranking, topology, trace analytics |
| `src/sentinel/agents/` | LangGraph pipeline and the six agents |
| `src/sentinel/application/` | Use cases: ingestion, investigations (queue), remediation (HITL), topology, audit |
| `src/sentinel/api/` | FastAPI routers, auth, middleware, MCP mount, console |
| `src/sentinel/infrastructure/` | Database, telemetry clients, action executor, notifier, YAML catalogs |
| `src/sentinel/llm/`, `knowledge/` | LLM providers and prompts; runbooks and BM25 retriever |
| `src/sentinel/defaults/` | `services.yaml`, `signals.yaml`, `actions.yaml` |
| `src/sentinel/migrations/` | Alembic migrations |
| `tests/` | `unit/`, `integration/`, `e2e/`, `load/`, `fakes.py` |
