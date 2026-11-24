# ADR 0002 — Modular monolith: one binary, `api` and `worker` roles

**Status:** Accepted.

**Context.** Discovery, reconciliation and metric collection all read and write the topology; the API reads it. Scaling profiles differ (HTTP vs. long-running provider calls), but data ownership is one boundary. No traffic numbers exist (PDF Q9).

**Decision.** One Go binary `obsplatform`. `OBS_ROLES=api,worker` selects what a process runs. Locally both run in one process; Helm deploys two Deployments from the same image.

**Alternatives rejected.**
- *Separate discovery, topology and metrics services* — splits one data-ownership boundary, requiring cross-service transactions or eventual consistency for reconciliation, three schemas and three release trains for no measured need.
- *Single Deployment with everything* — a slow provider or a large snapshot would compete with API latency and scale together.

**Consequences.** Package boundaries (`discovery`, `metrics`, `store`, `api`, `domain`) are the future service seams; `store.Metrics` is the first candidate to move to its own store/service (ADR 0003).
