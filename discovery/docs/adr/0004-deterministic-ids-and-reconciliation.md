# ADR 0004 — Deterministic identity, content hashes and complete-run-only missing marking

**Status:** Accepted.

**Context.** Providers may return the same resource twice (pagination overlap, retries), jobs may be re-run after a crash, and two connections may observe the same account. Deleting resources on one bad run would lose history and confuse users.

**Decision.** Resource ID = hash(tenant, provider, scope, external ID); edge ID = hash(tenant, source, type, target). Upserts on these IDs. A content hash over attributes classifies changed/unchanged. Only *complete* runs mark absent resources `missing` (with `missing_since`); they are purged after a retention window and reactivated if seen again.

**Alternatives rejected.** Random IDs with lookup-before-insert (race-prone, extra round trips); hard delete on absence (data loss on partial outages); tombstones without retention (unbounded growth).

**Consequences.** Idempotent re-runs; a resource is attributed to the connection that last saw it; changes are auditable in `topology_changes`.
