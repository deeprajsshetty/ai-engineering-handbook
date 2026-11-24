# Architecture

This document describes **Sentinel**, the AI-agentic observability platform in this repository,
and how the rest of the repository relates to it. Diagrams live in
[docs/diagrams](docs/diagrams/README.md); decisions and their trade-offs live in
[docs/adr](docs/adr/README.md).

## 1. Context

An enterprise runs many services across environments. On-call engineers are paged by several
alerts for one failure, spend most of an incident jumping between dashboards, logs and traces to
find *where* it started, and then apply a fix by hand. Sentinel shortens that loop:

1. **Collect** metrics, logs and traces through one OpenTelemetry pipeline.
2. **Detect** with Prometheus alert rules on golden signals (rate, errors, latency).
3. **Correlate** alerts into a single incident using time and the service dependency graph.
4. **Investigate** with a pipeline of specialised agents that gather evidence, detect anomalies,
   rank root causes and retrieve runbooks.
5. **Remediate** only through allow-listed actions that a human approves.
6. **Report** with an auditable incident timeline and a generated report.

## 2. Architectural principles

| Principle | How it shows up |
|---|---|
| Deterministic core, LLM at the edge | Detection, correlation, ranking and action selection are plain, tested code. The LLM writes explanations only, and every LLM call has a template fallback. |
| Human in the loop for change | Agents *propose*; operators *approve*. Approval is never exposed over MCP. |
| Open standards | OpenTelemetry (OTLP, W3C trace context), PromQL/LogQL/TraceQL, Alertmanager webhooks, MCP. |
| Boring infrastructure | PostgreSQL is the database *and* the work queue. No broker until load requires one (ADR-0002). |
| Graceful degradation | Any telemetry backend or the LLM can be down: the investigation completes and lists what was missing. |
| Observable by default | Sentinel exports its own metrics, traces and logs, and has its own alert rules. |
| Clean boundaries | `domain` has no I/O; `infrastructure` holds adapters; `application` orchestrates use cases; `api` is a thin interface. |
| Secure by default | Hashed API keys, RBAC, constant-time comparisons, rate limiting, body limits, CSP, non-root read-only containers. |

## 3. Repository map

```
ai-engineering-handbook/
├── sentinel/                 Sentinel platform (Python 3.12, FastAPI, LangGraph)
│   ├── src/sentinel/
│   │   ├── domain/           pure logic: anomaly, correlation, rca, topology, traces, models
│   │   ├── application/      use cases: alerts, investigations, remediation, topology, audit
│   │   ├── agents/           LangGraph graph + 7 agents
│   │   ├── llm/              provider abstraction + versioned prompts
│   │   ├── knowledge/        runbook knowledge base + BM25 retriever (RAG)
│   │   ├── infrastructure/   db/orm, telemetry + discovery clients, action executor, notifier, catalogs
│   │   ├── api/              routers, security, middleware, MCP mount, static console
│   │   ├── observability/    logging, metrics, tracing for Sentinel itself
│   │   ├── migrations/       Alembic migrations (shipped inside the package)
│   │   ├── defaults/         services.yaml, signals.yaml, actions.yaml
│   │   └── worker.py, cli.py
│   └── tests/                unit, integration, e2e, load
├── discovery/                discovery + topology service (Go + React), read by Sentinel (ADR-0009)
├── demo-services/shop/       instrumented demo workload + load generator
├── deploy/
│   ├── compose/              full local stack (14 containers; +4 with the `discovery` profile)
│   └── k8s/                  Kustomize base + dev/prod overlays
├── docs/                     diagrams, ADRs, agent specs, use cases, FDE playbook, runbooks, testing
├── handbook/                 learning track 000-012 (notebooks + small apps), one README per module
├── scripts/                  bootstrap-env.sh, demo.sh
└── .github/                  CI, release, dependabot
```

The **handbook** is the learning half of the repository (LLM APIs, prompt engineering,
LangChain, RAG, Gemini, LangGraph, MCP). Sentinel reuses those ideas in production form:
FastAPI + MCP from modules 001/008, RAG from 005, and LangGraph workflows from 007/011/012.

## 4. Components

### 4.1 Telemetry plane

| Component | Role | Why this choice |
|---|---|---|
| OpenTelemetry SDK | Instrument services (traces, metrics, logs) | Vendor-neutral standard; one SDK for all three signals |
| OpenTelemetry Collector (contrib) | Single ingestion point; batching, memory limits; derives the **service graph** and span RED metrics from traces; scrapes host and PostgreSQL | Decouples apps from backends; new backends need no app changes |
| Prometheus | Metrics store, recording rules, alert rules | De-facto standard, PromQL, native Alertmanager integration |
| Alertmanager | Grouping, inhibition, webhook delivery | Mature, de-duplicates before Sentinel |
| Loki | Log store with native OTLP ingestion | Indexes labels only (cheap), shares label model with Prometheus |
| Tempo | Trace store, TraceQL search | Object-storage backed, cheap at scale, integrates with Grafana |
| Grafana | Dashboards with metrics-to-traces-to-logs links | One UI over all three stores |

### 4.2 Sentinel

| Component | Responsibility |
|---|---|
| **API** (`sentinel-api`) | Alert ingestion (Alertmanager + generic), incident/investigation/remediation/service APIs, operator console, MCP server, health and metrics. Stateless; scales horizontally. |
| **Worker** (`sentinel-worker`) | Claims investigations and approved remediations from PostgreSQL, runs the agent graph, executes actions. Scales horizontally (SKIP LOCKED claims + leases). |
| **PostgreSQL** | Services catalog, alerts, incidents, investigations (also the queue), remediations, audit log. |
| **Agents** | Seven specialised LangGraph nodes - see [agent specifications](docs/agents/agent-specifications.md). |
| **Discovery client** | Read-only adapter to `discovery/` (`GET` resources and neighbourhoods) that adds infrastructure context; optional, see [unified platform guide](docs/guide/unified-platform-guide.md). |
| **Knowledge base** | Markdown runbooks with front-matter (`signals`, `actions`), retrieved with BM25. |
| **Catalogs** | `services.yaml` (static topology, seeded into the DB), `signals.yaml` (query templates), `actions.yaml` (allow-listed remediations). |

### 4.3 Discovery service

`discovery/` (`obsplatform`, Go + React, PostgreSQL or Oracle) discovers Kubernetes resources,
databases and a simulated estate, reconciles a typed topology and collects metrics. Sentinel reads
it and never writes to it. It has its own database, Helm chart and CI
(`.github/workflows/discovery-ci.yml`). Its design docs live in `discovery/docs/`.

### 4.4 Demo workload

`frontend -> checkout -> {payments, inventory}`, plus `frontend -> inventory`. Each service emits
`shop_server_requests_total{service,route,method,status_class}` and
`shop_server_duration_seconds` histograms, structured logs and traces, and exposes
`/admin/faults` (token-protected) to inject errors or latency. A fault stands in for a bad feature
flag or configuration push; `disable_fault_flag` is the matching remediation.

## 5. Key flows

* **Alert -> incident**: Alertmanager posts to `/api/v1/alerts/alertmanager`. Under a PostgreSQL
  advisory lock, alerts are upserted by fingerprint (idempotent), then attached to an active
  incident if it had activity inside `correlation_window_minutes` *and* the alert's service is
  within `correlation_max_hops` of the incident's services in the dependency graph. Otherwise a
  new incident opens. A changed scope queues a new investigation.
* **Investigation**: the worker claims a job (`FOR UPDATE SKIP LOCKED`, lease = `worker_lease_s`),
  builds the time window (baseline vs recent, split two minutes before the first alert) and scope
  (incident services plus downstream dependencies up to depth two), and runs the graph. Results,
  the root cause and remediation proposals are written in one transaction.
* **Remediation**: approving moves a proposal to `approved`; the worker executes `http` actions
  against the allow-listed URL template, only for catalog services. `manual` actions are completed
  by the operator. Everything is audited.
* **Resolution**: when every alert of an incident resolves, the incident auto-resolves. Operators
  can also resolve with a note.

Sequence diagrams: [docs/diagrams §7](docs/diagrams/README.md#7-sequence-diagrams-for-critical-use-cases).

## 6. Data model

See [docs/architecture/data-model.md](docs/architecture/data-model.md) for the ER diagram and
column reference. Six tables: `services`, `alerts`, `incidents`, `investigations`, `remediations`,
`audit_events`. JSON columns (JSONB on PostgreSQL) hold label sets and investigation results so
the agent output schema can evolve without migrations; anything queried by the API is a real
column.

## 7. Technology selection

| Need | Chosen | Considered, not chosen (why) |
|---|---|---|
| Language/API | Python 3.12 + FastAPI | Go (faster, but the AI/agent ecosystem and the team's handbook are Python) |
| Agent orchestration | LangGraph | Hand-rolled loop (fine, but loses typed state/reducers/conditional edges); CrewAI/AutoGen (autonomous multi-agent chat is harder to make deterministic and auditable) |
| Queue | PostgreSQL SKIP LOCKED | Kafka/RabbitMQ/SQS (more infrastructure for a workload of tens of jobs/minute) - ADR-0002 |
| Database | PostgreSQL | MongoDB (relational integrity + JSONB covers the need) |
| Retrieval | BM25 over runbooks | Vector DB (Pinecone/Chroma as in the handbook): unnecessary for a small curated corpus - ADR-0005 |
| LLM | Provider-neutral HTTP adapters, deterministic default | Single-vendor SDK lock-in - ADR-0004 |
| Deployment | Docker Compose (local/demo), Kubernetes + Kustomize (prod) | Helm chart for Sentinel (Kustomize is enough for two overlays) |

## 8. Quality attributes

| Attribute | Mechanism | Verified by |
|---|---|---|
| Correctness of RCA | Weighted multi-evidence ranking; scenario tests | `tests/unit/test_agents.py`, e2e |
| Availability | Stateless API, N workers, PDBs, probes, retries/leases | `test_worker_lease_recovery`, k8s manifests |
| Resilience | Timeouts, bounded retries with backoff, per-agent failure isolation, LLM fallback | `test_all_backends_down_degrades_gracefully`, `test_llm_outage_falls_back_without_failing`, `test_failing_agent_is_isolated` |
| Idempotency | Fingerprint upsert; one queued investigation per incident; dedup of open proposals | `test_duplicate_webhook_is_idempotent` |
| Security | See [SECURITY.md](SECURITY.md) | `test_api_platform.py`, bandit, pip-audit, gitleaks, Trivy |
| Performance | p95 targets in [test strategy](docs/testing/test-strategy.md) | Locust load test |
| Maintainability | Layered packages, typed (mypy), linted (ruff), 90% coverage | CI `quality` + `test` jobs |

## 9. Known limitations

* Rate limiting is per replica (in memory). Put a shared limit at the ingress for strict quotas.
* The `http` executor only calls allow-listed URLs. Kubernetes-native actions (scale, rollout
  restart) are modelled as `manual` until a Kubernetes executor with scoped RBAC is added.
* Anomaly detection compares one recent window to one baseline window. It does not model
  seasonality (time of day); see the roadmap in [PROJECT_KNOWLEDGE.md](PROJECT_KNOWLEDGE.md).
* The operator console is deliberately minimal; Grafana is the primary visual surface.
