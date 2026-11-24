# Project knowledge base

This is the single place to understand the project without any prior conversation. If you are a
developer or an AI agent picking this up cold, read this file first, then
[ARCHITECTURE.md](ARCHITECTURE.md).

Last verified against the code: **2026-11-10** (Sentinel v1.0.0).

---

## 1. What this repository is

| Part | Purpose | State |
|---|---|---|
| `handbook/` | Learning modules: OpenAI APIs, prompt engineering, LangChain, RAG (Pinecone/Chroma), Gemini, LangGraph, a FastAPI + MCP todo API, and a research-agent capstone. | Preserved as-is from the original repo. Notebooks read API keys from the environment or a `.env` file; copy `handbook/.env.example`. |
| `sentinel/` | **Sentinel**: AI-agentic observability platform. The production reference implementation. | v1.0.0, tested and deployable with Docker Compose. Kubernetes manifests render and validate but have not been applied to a cluster in this repo. |
| `demo-services/shop/` | Instrumented microservices that stand in for the customer's workload. | Used by the Compose stack and the e2e test. |
| `deploy/` | Compose stack and Kubernetes manifests. | Compose validated end to end. |
| `docs/` | Diagrams, ADRs, agent specs, use cases, FDE playbook, runbooks, test strategy. | Kept in sync with code (see §9). |

## 2. Business context

**Customer (archetype):** a mid-to-large enterprise with dozens of services across environments,
an on-call rotation, and Prometheus-style alerting that pages too often.

**Pain points found in discovery** ([docs/fde/customer-discovery.md](docs/fde/customer-discovery.md)):

1. Alert storms: one failure pages 3-10 times across dependent services.
2. Slow triage: 20-40 minutes per incident spent finding *where* a failure started.
3. Tribal knowledge: runbooks exist but are not found during incidents.
4. Risky manual fixes under pressure, with poor audit trails.
5. Tool sprawl: metrics, logs and traces in different tools with no links between them.

**Objectives and success metrics**

| Objective | Metric | Target | Measured by |
|---|---|---|---|
| Fewer pages | Alerts per incident delivered to humans | >= 3:1 grouping on cascading failures | `sentinel_alerts_received_total` / `sentinel_incidents_created_total` |
| Faster diagnosis | Time from first alert to identified root cause (MTTI) | < 2 min | Investigation `finished_at - incident.started_at`; e2e measured 75-80 s |
| Correct diagnosis | Top-1 RCA accuracy on validated incidents | >= 80% | Post-incident review labels (see AIDLC §6) |
| Faster recovery | MTTR for flag/config-caused incidents | -50% | Incident `resolved_at - started_at` |
| Safe automation | Unapproved production changes | 0 | Audit log: every `remediation.executed` has a preceding `remediation.approved` |

## 3. Glossary

| Term | Meaning |
|---|---|
| Golden signals | Request rate, error rate, latency (p95) per service. |
| Incident | A group of correlated alerts that represents one problem. |
| Investigation | One run of the agent graph for an incident. Stored with its full result. |
| Hypothesis | A ranked candidate root cause with evidence and component scores. |
| Remediation | A proposed action (from the allow-list) with a HITL state machine. |
| Data gap | Evidence the agents could not collect (backend down, query failed). |
| Silent anomaly | A service that is anomalous but did not alert. It is often the true root cause. |
| HITL | Human in the loop: a person approves any change. |
| Fault flag | The demo services' injectable fault. It stands in for a bad feature flag or config push. |

## 4. Key technical decisions (summary)

Full rationale in [docs/adr](docs/adr/README.md).

1. **Layered package**: `domain` (pure) <- `application` <- `api`/`worker`; adapters in `infrastructure`, `llm`, `knowledge`.
2. **PostgreSQL is the queue**: `SKIP LOCKED` plus leases. No broker.
3. **HITL is persisted state** outside the agent graph. Approvals are never exposed via MCP.
4. **LLM is optional and only narrates**. The deterministic provider is the default, and every remote call falls back to it.
5. **BM25 runbook retrieval** instead of a vector DB.
6. **OpenTelemetry + Prometheus/Loki/Tempo/Grafana**. The dependency graph is derived from traces.
7. **Deterministic weighted RCA** (anomaly 0.30, topology 0.25, traces 0.20, earliness 0.15, logs 0.10).
8. **Repo restructure**: `handbook/` for learning, top-level folders for the platform.

## 5. Implementation facts that are easy to get wrong

* **Metric names.** The demo services emit OTel counters/histograms named `shop.server.requests`
  and `shop.server.duration` (unit `s`). The Collector's Prometheus exporter turns them into
  `shop_server_requests_total` and `shop_server_duration_seconds_bucket`, with labels `service`,
  `route`, `method`, `status_class`. Sentinel's queries live in
  `sentinel/src/sentinel/defaults/signals.yaml`. Change both together.
* **Service graph.** `traces_service_graph_request_total{client,server}` comes from the
  Collector's `servicegraph` connector. The virtual nodes `user` and `unknown` are ignored.
* **Loki labels.** With native OTLP ingestion, `service_name` is a stream label. `severity_text`
  and `trace_id` are *structured metadata*, so filter them with `| severity_text=~"..."`, not
  in the stream selector.
* **Tempo trace IDs** may come back without leading zeros, and span IDs are base64 in OTLP-JSON.
  `parse_otlp_trace` normalises span IDs to hex.
* **Investigation window**: `split = first_alert_start - 120s` and baseline = lookback minus recent
  (default 25 min). With fewer than 6 baseline points the detector uses static thresholds and says
  so (`method=static_threshold`, "insufficient baseline").
* **Alertmanager auth**: the webhook token reaches Alertmanager as a Compose *secret* from the
  `SENTINEL_WEBHOOK_TOKEN` env var (`credentials_file: /run/secrets/webhook_token`). No token file
  lives in the repo.
* **`fastapi-mcp` 0.4 needs `mcp<2`.** The `mcp` 2.x SDK changed `Server()` and breaks it. The
  pin is in `pyproject.toml`.
* **Migrations ship inside the package** (`sentinel/migrations`), so the container image can run
  `sentinel migrate` without the source tree.
* **Action targets come from the catalog only.** `RemediationService.execute` checks the
  service against `TopologyService.catalog_services` (seed file plus admin API), *not* the
  trace-discovered graph. Service-graph labels are telemetry, so anything instrumented can write
  them, and they must never decide where the action token is sent.
* **Abandoned executions are failed, not retried.** An `executing` remediation holds a lease
  (`lease_expires_at`). If it expires, the worker marks the row `failed` with
  `result.error = "unknown_outcome"`, audits `remediation.unknown_outcome`, and alerts
  (`SentinelRemediationUnknownOutcome`). The action may already have run, so a human decides.
* **Resolution expires proposals.** When an incident resolves, remediations still
  `pending_approval` are set to `rejected` ("expired"), so a stale approval can never run.

## 6. Configuration reference

All settings are `SENTINEL_*` environment variables (see `sentinel/src/sentinel/config.py`).

| Variable | Default | Notes |
|---|---|---|
| `SENTINEL_ENV` | `dev` | `prod` enforces API keys, a 24+ char webhook token, and PostgreSQL |
| `SENTINEL_DATABASE_URL` | `sqlite:///./sentinel.db` | `postgresql+psycopg://...` in Compose/K8s |
| `SENTINEL_API_KEYS` | empty | `name:role:sha256,...`. Roles: viewer, operator, admin |
| `SENTINEL_WEBHOOK_TOKEN` | empty | Alertmanager bearer token |
| `SENTINEL_ACTION_TOKEN` | empty | Sent as `X-Admin-Token` to remediation targets |
| `SENTINEL_PROMETHEUS_URL` / `_LOKI_URL` / `_TEMPO_URL` | unset | Unset disables that backend (recorded as a data gap) |
| `SENTINEL_LLM_PROVIDER` | `deterministic` | `openai` or `anthropic` (+ `SENTINEL_OPENAI_API_KEY` / `SENTINEL_ANTHROPIC_API_KEY`, optional `SENTINEL_LLM_MODEL`) |
| `SENTINEL_CORRELATION_WINDOW_MINUTES` | 15 | Alert-to-incident grouping window |
| `SENTINEL_CORRELATION_MAX_HOPS` | 2 | Topological distance for grouping |
| `SENTINEL_INVESTIGATION_LOOKBACK_MINUTES` | 30 | Total window |
| `SENTINEL_ANOMALY_Z_THRESHOLD` | 3.5 | Robust z-score threshold |
| `SENTINEL_AUTO_INVESTIGATE` | true | Queue an investigation on new incidents/scope changes |
| `SENTINEL_AUTO_APPROVE_LOW_RISK` | false | Explicit opt-in; low-risk actions skip approval |
| `SENTINEL_WORKER_LEASE_S` | 180 | Crash-recovery lease for investigations |
| `SENTINEL_REMEDIATION_LEASE_S` | 120 (min 30) | An `executing` remediation without a result after this is marked `failed` / `unknown_outcome` and never retried |
| `SENTINEL_RATE_LIMIT_PER_MINUTE` / `_BURST` | 600 / 120 | Per credential (or IP), per replica |
| `SENTINEL_NOTIFICATION_WEBHOOK_URL` | unset | Slack-compatible `{"text": ...}` |
| `SENTINEL_OTEL_ENABLED` | false | Exports Sentinel's own traces/logs via OTLP |
| `SENTINEL_MCP_ENABLED` | true | Mounts read-only MCP tools at `/mcp` |
| `SENTINEL_DISCOVERY_URL` / `_TOKEN` | unset / empty | Discovery service for infrastructure context; unset skips the step ([guide](docs/guide/unified-platform-guide.md#5-configuration-reference-sentinel-side)) |
| `SENTINEL_DISCOVERY_SERVICE_TYPES` | `k8s.deployment,k8s.service` | Resource types matched by exact service name |
| `SENTINEL_DISCOVERY_DEPTH` / `_MAX_NODES` | 2 / 50 | Neighbourhood bounds |

## 7. Development guidelines (short)

Full version: [DEVELOPMENT_GUIDELINES.md](DEVELOPMENT_GUIDELINES.md).

* Put new logic in `domain/` when it can be pure. Test it directly.
* Agents return partial state dicts. Never raise for expected failures; append to `data_gaps`.
* Anything that changes production goes through `actions.yaml` + the remediation state machine.
* Telemetry and log text is untrusted input. Never interpolate it into queries or URLs.
* A schema change means a new Alembic revision. The migration test must stay green.
* Keep docs in sync. The PR template has a checkbox for it.

## 8. Current status

| Area | Status |
|---|---|
| Telemetry pipeline (metrics, logs, traces, service graph, host and Postgres metrics) | Done, validated on Compose |
| Dashboards (shop golden signals, Sentinel platform, infrastructure) | Done, provisioned from `build_dashboards.py` |
| Alert rules (golden signals + platform self-monitoring) | Done, all 11 rules healthy |
| Alert ingestion, dedup, correlation, auto-resolve | Done, tested |
| 7-agent investigation graph, RCA, RAG remediation, reporting | Done; e2e: errors -> payments (68%), latency -> inventory (76%), measured before the infrastructure step was added |
| Discovery integration (read-only infrastructure context) | Done; unit-tested, and the client was checked against a live discovery server. Compose profile not run (no Docker daemon). Production blocked on discovery auth (ADR-0009) |
| HITL remediation with `http` and `manual` executors | Done, tested |
| Auth (API keys + roles), rate limiting, audit | Done, tested |
| MCP read-only tools | Done, auth tested |
| CI (quality, tests on SQLite + Postgres, security, images, config validation, e2e) | Written; jobs mirror commands that pass locally, but the workflow has not run on GitHub yet |
| Kubernetes | Manifests render (dev/prod); not applied to a cluster |

## 9. Keeping this knowledge current

When you change behaviour, update these together:

| Change | Update |
|---|---|
| New/changed endpoint | `API_DOCUMENTATION.md`, OpenAPI contract test |
| New config variable | `config.py`, §6 above, `.env.example`, `deploy/k8s/base/configmap.yaml` |
| Agent logic | `docs/agents/agent-specifications.md`, scenario tests |
| Architecture or technology | `ARCHITECTURE.md`, a new ADR, `docs/diagrams/README.md` |
| Release | `CHANGELOG.md` |

## 10. Roadmap

| Priority | Item | Why |
|---|---|---|
| P1 | Kubernetes executor (scale, rollout restart) with namespace-scoped RBAC | Turn `manual` actions into approved automated ones |
| P1 | RCA evaluation harness from labelled post-incident reviews | Measure top-1 accuracy continuously (AIDLC §6) |
| P2 | Seasonality-aware baselines (same hour last week) | Fewer false anomalies on daily traffic cycles |
| P2 | Deploy/change events as evidence (CI webhook -> `change_events` table) | "What changed?" is the strongest RCA signal |
| P2 | SSO (OIDC) for the console; keep API keys for machines | Enterprise identity |
| P3 | Shared rate limiting (Redis) and multi-tenant isolation | Multi-team SaaS deployment |
| P3 | Hybrid BM25 + embeddings over post-mortems | Larger, free-text knowledge base |

## 11. Handbook module index

| Module | Topic | Runnable app |
|---|---|---|
| 000 | Python/pandas data foundations | notebook |
| 001 | FastAPI CRUD API exposed as MCP tools (`fastapi-mcp`) | `uvicorn main:app` (Render blueprint at `render.yaml`) |
| 002 | OpenAI chat completions, DALL-E, Whisper/TTS, vision | notebooks |
| 003 | Prompt engineering | notebook |
| 004 | Daily meal plan generator (text + image + audio) | notebook |
| 005 | LangChain: chains, agents, Pinecone, RAG, Streamlit apps | `streamlit run ...` |
| 006 | Google Gemini API, image chat, AI image renaming | `streamlit run ...` |
| 007 | LangGraph: agents from scratch, reflection, essay writer | notebooks |
| 008 | MCP with OpenAI: remote tools, approvals, auth | notebook (Colab) |
| 009 | OpenAI Agents SDK: file-grounded agent | notebook (Colab) |
| 010 | CrewAI multi-agent: research crews, document automation, capstone | notebooks (Colab) |
| 011 | LangGraph projects: routers, interview coach | notebooks (Colab) |
| 012 | Capstone: research agent with LangGraph + ArXiv + SerpAPI + Pinecone RAG | notebook |

Full index and conventions: [handbook/README.md](handbook/README.md).
