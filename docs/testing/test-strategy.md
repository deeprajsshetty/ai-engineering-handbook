# Test strategy and validation report

## Strategy

| Level | Purpose | Tooling | Location | When |
|---|---|---|---|---|
| Unit | Algorithms and adapters in isolation | pytest, `httpx.MockTransport` | `sentinel/tests/unit` | Every commit |
| Scenario evals | AI quality: known failures must produce the right RCA and actions | pytest + `tests/fakes.py` scenarios | `tests/unit/test_agents.py` | Every commit |
| Integration | API + application + DB, including HITL, auth and audit | FastAPI `TestClient`; SQLite, plus PostgreSQL in CI | `sentinel/tests/integration` | Every commit |
| Migration | Schema = models; upgrade/downgrade | Alembic `compare_metadata` | `test_migrations.py` + CI `alembic check` | Every commit |
| Contract | OpenAPI operation IDs, LLM request shapes | pytest | `test_openapi_contract`, `TestLLMProviders` | Every commit |
| End to end | Real telemetry → alert → RCA → approved fix → recovery | pytest marker `e2e` + Compose | `tests/e2e/test_compose_e2e.py` | `master`, labelled PRs, releases |
| Performance | Latency and error budget under load | Locust | `tests/load/locustfile.py` | Before releases |
| Security | Static analysis, dependencies, secrets, images, authz | bandit, pip-audit, gitleaks, Trivy, authz tests | CI `security` / `images` | Every commit |
| Resilience | Behaviour with dependencies down | Scenario tests + live fault tests | `test_agents.py`, this report | Every commit, plus before releases |
| Config | Rules, Alertmanager, manifests, dashboards | promtool, amtool, kubeconform, generator diff | CI `deploy-config` | Every commit |

Targets: coverage ≥ 85%; p95 below 300 ms for reads and below 500 ms for alert ingestion at 50
users with 0% errors; MTTI below 2 min.

### Fake telemetry

`tests/fakes.py::FakeTelemetry` implements the `TelemetryBackend` protocol. Scenarios declare a
baseline and a recent value per service and signal, error log counts, synthetic call-chain traces
(error origin or slow service), and which backends are "down". To add a regression case, write a
`Scenario` that reproduces the incident and assert the top hypothesis.

## Validation report

Run on 2026-11-10 on macOS with Docker 29.4.1 (Compose v5.1.3), Python 3.12.

### 1. Repository and build

| Check | Result |
|---|---|
| Repository structure | Matches [ARCHITECTURE.md §3](../../ARCHITECTURE.md#3-repository-map) |
| `uv sync --frozen` + `uv lock` | ✅ 152 packages resolved, lock committed |
| `ruff check`, `ruff format --check`, `mypy` (59 files) | ✅ clean |
| `docker build` sentinel, shop | ✅ both build; images run as UID 10001 |
| `docker compose config -q` | ✅ |
| `kubectl kustomize overlays/dev` and `overlays/prod` | ✅ render (prod: 16 objects) |
| `promtool check rules` | ✅ 6 + 5 rules |
| `amtool check-config` | ✅ |

### 2. Automated tests

| Suite | Result |
|---|---|
| Unit (62 tests) + integration (17) | ✅ **79 passed**, 0 failed |
| Coverage | ✅ **93%** (gate 85%) |
| Migrations: upgrade → compare with models → downgrade | ✅ no drift (`alembic check`: "No new upgrade operations detected") |
| Bandit | ✅ no issues |
| pip-audit (runtime deps) | ✅ no known vulnerabilities |

### 3. Live stack (Compose, 14 containers)

| Check | Result |
|---|---|
| All containers healthy; migrations applied | ✅ |
| `/health/live`, `/health/ready` | ✅ `database: ok`, all backends configured |
| Metrics ingestion | ✅ `shop_server_requests_total{service,route,method,status_class}`, `shop_server_duration_seconds_*`, `system_*`, `postgresql_*`, `traces_span_metrics_*` |
| Service graph discovery | ✅ edges `frontend→checkout`, `frontend→inventory`, `checkout→payments`, `checkout→inventory` (plus virtual `user`, which is filtered out) |
| Logs in Loki | ✅ `service_name` label; `severity_text` and `trace_id` structured metadata |
| Traces in Tempo | ✅ TraceQL search returns traces |
| Grafana | ✅ 3 dashboards, 4 data sources provisioned |
| Prometheus rules | ✅ 11/11 healthy |

### 4. End-to-end use case

| Scenario | Result |
|---|---|
| **Errors in payments** (`error_rate=0.7`), e2e test | ✅ One incident for 3 alerts (frontend, checkout, payments). Root cause **payments**, confidence **0.68** (runner-up checkout 0.27) after **75 s**. No data gaps. `disable_fault_flag` approved by operator; succeeded at **77 s**; incident auto-resolved at **168 s**. Test passed in 167.7 s. |
| **Latency in inventory** (`latency_ms=900`) | ✅ Root cause **inventory** (`latency_p95`), confidence **0.76**, after **80 s**. Anomalies used the robust z-score (baseline available). Recommendations: `disable_fault_flag`, `scale_out`, `restart_service`. |

The generated report is in [example-incident-report.md](../use-cases/example-incident-report.md).
In the first scenario the stack had been running for only about 80 s before the fault, so the
anomaly detector correctly fell back to static thresholds ("insufficient baseline"). The ranking
was still correct because topology, traces and logs carry 55% of the score.

### 5. Resilience

| Fault | Result |
|---|---|
| Tempo stopped during an investigation (live) | ✅ Investigation completed in 1.3 s; data gaps recorded "trace search failed"; root cause still **inventory** (0.59); 0 worker errors |
| All backends down (scenario) | ✅ Routed straight to reporting; 3 data gaps; no hypotheses |
| LLM provider failing (scenario + contract) | ✅ Fallback to templates after 3 retries; `fallback: true` recorded |
| Agent crash (scenario) | ✅ Step isolated (`ok: false`), pipeline continued, correct RCA |
| Worker crash mid-job (integration) | ✅ Lease expiry → reclaimed (attempt 2) → failed after max attempts |
| Remediation target returns 500 (integration) | ✅ `failed` with status code recorded |
| Duplicate webhook delivery (integration) | ✅ Idempotent |

### 6. Performance (Locust, 50 users, 60 s, single API replica)

| Run | Requests | Failures | p50 | p95 | p99 |
|---|---|---|---|---|---|
| Rate limit at default (600/min per key; all users share one key) | 2,271 | 1,421 (all `429`, as designed) | 4 ms | 19 ms | 78 ms |
| Capacity (limit raised) | 2,292 | **0** | 8 ms | **21 ms** | 55 ms |
| ↳ `POST /api/v1/alerts` | 201 | 0 | 13 ms | **39 ms** | 91 ms |
| ↳ `GET /api/v1/incidents` | 1,033 | 0 | 8 ms | 19 ms | 55 ms |

Both targets are met by a wide margin. The first run also confirms that rate limiting works
under load, and that `/health/*` stays exempt (0 failures).

### 7. Preserved functionality

| Check | Result |
|---|---|
| `handbook/001-fastapi-mcp-todo` after the move (fresh venv, own `requirements.txt`) | ✅ CRUD round trip; MCP `initialize` returns 200 |
| All notebooks are still valid JSON after redaction | ✅ |

### Not validated here (environment-specific)

- Applying the Kubernetes manifests to a live cluster (NetworkPolicy enforcement, HPA behaviour,
  ExternalSecret sync).
- The GitHub Actions workflows themselves. Every step mirrors a command that passed locally, but
  kubeconform, Trivy and gitleaks were not run locally.
- Remote LLM providers against real APIs (request and response shapes are contract-tested with
  mocks).
- A managed PostgreSQL PITR restore drill.
