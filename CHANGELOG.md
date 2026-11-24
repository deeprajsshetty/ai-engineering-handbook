# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[SemVer](https://semver.org/).

## [Unreleased]

### Security

- Remediation actions can only target services in the operator-controlled catalog. Before this
  change, a service name seen only in trace data (the service graph) was accepted as a target, so
  telemetry could steer where Sentinel sent `X-Admin-Token` after a human approval.

### Fixed

- A remediation left in `executing` by a crashed worker stayed stuck forever and blocked
  re-proposal. Executions now hold a lease (`SENTINEL_REMEDIATION_LEASE_S`, default 120 s). An
  expired row becomes `failed` with `unknown_outcome`, is audited, and is not retried
  automatically.

### Added

- Migration `0002`: `remediations.lease_expires_at`.
- **Unified platform (ADR-0009).** `observability-platform/` is now `discovery/`, Sentinel's
  read-only discovery and topology service. A new pipeline step, `infrastructure_context`
  (`HttpDiscoverySource`), maps services to discovered resources and reports missing or stale
  infrastructure in the report and RCA narrative, without changing the ranking. New settings:
  `SENTINEL_DISCOVERY_*`.
- Compose profile `discovery` and `make up-discovery`; `make discovery-lint` and
  `make discovery-test`; root workflow `.github/workflows/discovery-ci.yml`.
- `docs/guide/unified-platform-guide.md` (step by step) and
  `docs/guide/agentic-engineering-guide.md`.

### Changed

- Handbook restructured into one numbered track, 000–012: `010-ai-agents/` became modules
  008–011, and the old 008 capstone is now 012. Every module has a README with objectives, files,
  run steps and a self-check. Folder names have no spaces.

### Removed (moved to the git-ignored `_archive/`, listed in `_archive/MANIFEST.md`)

- `Monitoring-Boilerplate-Code/` (33 legacy repos that could not be built) and the assessment PDF.
- Duplicate datasets, media and notebooks; `code_for_old_library_version/` folders; personal
  working copies; checkpoints, caches, local databases and a module virtualenv; generated load
  reports.
- Interview-prep inputs and outputs that profiled real, named individuals (privacy).
- Metric `sentinel_remediation_unknown_outcome_total{action}` and alert
  `SentinelRemediationUnknownOutcome`.

## [1.0.0] - 2026-11-10

### Added

**Sentinel AIOps platform** (`sentinel/`)

- Alert ingestion: an Alertmanager v4 webhook and a generic alert API. Fingerprints make ingestion
  idempotent.
- Incident correlation by time window and dependency-graph distance, under a PostgreSQL advisory
  lock. Incidents auto-resolve, and new episodes are detected when a resolved alert fires again.
- A six-agent LangGraph investigation pipeline:
  - Telemetry Analysis (PromQL, LogQL, TraceQL);
  - Anomaly Detection (robust z-score with MAD, with a static fallback);
  - Incident Correlation (propagation order, blast radius, silent anomalies);
  - Root Cause Analysis (weighted ranking, LLM narrative);
  - Remediation Recommendation (BM25 runbook RAG plus the action allow-list);
  - Reporting (markdown report and executive summary).
- Human-in-the-loop remediation:
  - a state machine with `http` and `manual` executors;
  - an SSRF guard;
  - retries;
  - expiry of pending proposals when an incident resolves;
  - an optional auto-approval for low-risk actions.
- LLM providers: deterministic (the default), OpenAI and Anthropic, each with automatic fallback
  and token metering.
- API v1 with:
  - API-key auth and roles (viewer, operator, admin);
  - rate limiting, body limits and security headers;
  - a single error envelope;
  - request IDs;
  - an audit log and an incident timeline.
- Read-only MCP tools at `/mcp`.
- An operator console with incidents, RCA, approvals and the timeline.
- Self-observability: Prometheus metrics, OTel traces and logs, and platform alert rules.
- A PostgreSQL-backed work queue (`SKIP LOCKED` plus leases), and a worker with graceful shutdown.
- Alembic migrations shipped inside the package, and a `sentinel` CLI (`gen-key`, `hash-key`,
  `migrate`, `seed`, `worker`, `drain`).
- Tests:
  - 79 unit and integration tests (93% coverage);
  - an e2e test against the live stack;
  - a Locust load test.

**Telemetry stack and demo** (`deploy/compose`, `demo-services/shop`)

- OpenTelemetry Collector with traces, metrics and logs pipelines, plus the `servicegraph` and
  `spanmetrics` connectors and the hostmetrics and postgresql receivers.
- Prometheus recording and alert rules, Alertmanager routed to Sentinel, Loki (OTLP), Tempo and
  Grafana.
- Three provisioned dashboards, generated from code.
- Instrumented demo services (frontend, checkout, payments, inventory) with token-protected fault
  injection, and a load generator.
- A 14-container Compose stack with hardening (read-only rootfs, no capabilities, no new
  privileges), a secrets bootstrap script and a demo script.

**Deployment and delivery**

- Kubernetes Kustomize base and dev/prod overlays:
  - PSA restricted, default-deny NetworkPolicies;
  - HPA, PDBs and probes;
  - a migration Job;
  - an ExternalSecret and an Ingress.
- GitHub Actions CI:
  - quality checks;
  - tests on SQLite and PostgreSQL;
  - security scans (bandit, pip-audit, gitleaks);
  - an image build with a Trivy scan;
  - deployment-config validation;
  - the e2e test.
- A release workflow: multi-arch images, SBOM, provenance and cosign signing. Dependabot is
  configured.

**Documentation**

- README, ARCHITECTURE, AIDLC, PROJECT_KNOWLEDGE, DEVELOPMENT_GUIDELINES, API_DOCUMENTATION,
  DEPLOYMENT_GUIDE, SECURITY and TROUBLESHOOTING.
- 11 Mermaid diagrams, 8 ADRs and the agent specifications.
- Six detailed use cases plus the handbook use cases.
- The FDE playbook and discovery record, runbooks, the test strategy with a validation report, and
  the initial assessment.

### Changed

- The repository is restructured: the learning modules moved to `handbook/` with their content
  unchanged, and the loose root data files moved to `handbook/000-python-data-foundations/`.
- `render.yaml` now points at `handbook/001-fastapi-mcp-todo`. The old path `fastapi-mcp-todo`
  never existed.
- `.gitignore` was rewritten: virtualenvs (including the 357 MB `genai/`), local databases, caches,
  notebook checkpoints and `.env` files are excluded.

### Security

- Hard-coded OpenAI and LangSmith API keys were redacted from 10 notebook files (including
  checkpoints), and code cells now read keys from the environment. **The keys must be rotated by
  the owner**; see [SECURITY.md §6](SECURITY.md#6-security-history-of-this-repository).
- Added gitleaks, nbstripout and detect-private-key pre-commit hooks.
