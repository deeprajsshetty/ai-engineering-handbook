# Architecture Decision Records

Format: [MADR-lite](https://adr.github.io/madr/). One decision per file, never edited after
acceptance except to mark it *Superseded by ADR-NNNN*.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-layered-architecture.md) | Layered (hexagonal-style) package structure | Accepted |
| [0002](0002-postgres-as-queue.md) | PostgreSQL as the work queue; no message broker | Accepted |
| [0003](0003-hitl-outside-the-graph.md) | Human approval as persisted state, outside the agent graph | Accepted |
| [0004](0004-llm-provider-abstraction.md) | Provider-neutral LLM adapters with deterministic fallback | Accepted |
| [0005](0005-bm25-runbook-retrieval.md) | BM25 retrieval over runbooks instead of a vector database | Accepted |
| [0006](0006-opentelemetry-grafana-stack.md) | OpenTelemetry + Prometheus/Loki/Tempo/Grafana | Accepted |
| [0007](0007-deterministic-rca.md) | Deterministic, weighted root-cause ranking | Accepted |
| [0008](0008-repository-restructure.md) | Restructure: `handbook/` + `sentinel/` + `deploy/` + `docs/` | Accepted |
| [0009](0009-unify-around-sentinel.md) | Unify around Sentinel: Go platform becomes the read-only `discovery/` service | Accepted |

Template for new ADRs: copy any file, keep the headings *Context, Decision, Alternatives,
Consequences*.
