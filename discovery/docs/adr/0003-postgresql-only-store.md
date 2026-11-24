# ADR 0003 — PostgreSQL as the only required store for the MVP

**Status:** Accepted for the MVP; **amended by [ADR 0011](0011-platform-store-portability.md)** — Oracle is now a supported alternative platform store.

**Context.** Evidence: the legacy platform already runs PostgreSQL (or Oracle) for the dashboard schema and store-and-forward (Verified); the PDF keeps PostgreSQL bundled (D2) and proposes ClickHouse for analytics (D11), Valkey for cache/queues (D8). The MVP needs relational integrity (FKs, unique constraints for idempotency and the one-active-job rule), transactions for snapshot application, and modest time-series volume for a demo.

**Decision.** PostgreSQL (tested on 18.3) is the only required datastore: topology, jobs, audit, and MVP time series. No Redis/Valkey, ClickHouse or object storage in the MVP.

**Alternatives rejected.**
- *SQLite for local + PostgreSQL for production* — two SQL dialects to test; local PostgreSQL is available via a script or container.
- *ClickHouse now* — a second stateful system to operate before any volume exists; the `store.Metrics` interface keeps it a drop-in later.
- *Redis Streams for jobs* — see ADR 0005.
- *Oracle for the new tables now* — D2 requires it eventually; doubling the test matrix for an MVP is not justified. **Gap documented**; SQL is plain and isolated in `store/postgres` so an `store/oracle` package can follow.

**Consequences.** Time-series retention defaults to 7 days with batched deletes; the LLD gives the volume at which to move metrics (≈ thousands of resources). Operational cost: one PostgreSQL with backups (managed or CloudNativePG).
