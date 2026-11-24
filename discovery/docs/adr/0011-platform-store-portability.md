# ADR 0011 — Platform datastore portability: Oracle as the platform's own store

**Status:** Accepted and implemented. Verified against Oracle Database Free 23ai (container `gvenzl/oracle-free:23-slim-faststart`); not yet run against Oracle 19c, Autonomous Database or RAC.

**Context.** The architecture assessment (PDF decision D2, Verified in the legacy code) says the product must run its own schema on PostgreSQL *or* Oracle, because some private-cloud customers mandate Oracle. ADR 0003 chose PostgreSQL only for the MVP and recorded this as a gap; the review asked for it to be built. ADR 0010 covers *monitoring* Oracle; this ADR covers *storing platform data* in Oracle.

All persistence goes through the interfaces in `internal/store/store.go`; only the store packages know SQL. That is the seam.

**Decision.**
1. `store.Backend` = `store.Store` + `Driver()`, `MigrateUp()`, `MigrationStatus()`. The app, CLI and readiness probe depend only on it. `internal/store/backend.Open` selects the implementation from `OBS_STORE_DRIVER=postgres|oracle` (default `postgres`); `OBS_DATABASE_URL` is `postgres://…` or `oracle://user:password@host:port/service`.
2. `internal/store/oracle` implements the full interface on `database/sql` + go-ora (the same pure-Go driver as ADR 0010, so no new dependency and no Oracle client libraries in the image). Its own embedded, checksummed, forward-only migrations (`store/oracle/migrations`).
3. One driver-independent contract suite, `internal/store/storetest`, run by both store packages. The whole API end-to-end suite can also run on Oracle (`OBS_TEST_STORE=oracle`); `make test-oracle` does both, and CI has an `oracle-store` job.
4. Time series stay in the same store for now (as for PostgreSQL); the `store.Metrics` interface keeps a columnar store possible later (roadmap Phase 3).

**PostgreSQL feature → what the Oracle store does**

| PostgreSQL | Oracle store |
|---|---|
| `jsonb` + `@>` label containment | JSON in `CLOB … IS JSON`, plus a `resource_labels(resource_id, k, v)` table so label filters and the engine/env facets are indexed joins (no dynamic JSON paths) |
| `ON CONFLICT DO UPDATE / DO NOTHING` | `MERGE` (resources, edges, samples) |
| `FOR UPDATE SKIP LOCKED LIMIT 1` | `FOR UPDATE SKIP LOCKED` cursor, one row fetched (Oracle forbids `FETCH FIRST` with `FOR UPDATE`) |
| `unnest($1::text[])`, `= ANY($1)` | `IN` lists chunked at 1000; `TABLE(sys.odcivarchar2list(…))` for fleet pairs |
| Partial unique indexes (one active job; idempotency key) | Function-based unique indexes (`CASE … END` keys; NULL keys are not indexed) |
| `date_bin` | Epoch-second arithmetic with rounding (see below) |
| `pg_advisory_lock` for migrations | Exclusive lock on `schema_migration_lock` held by one session while DDL runs on another |
| `DISTINCT ON`, `LATERAL` | `ROW_NUMBER() OVER`; correlated scalar `MAX(ts)` per (resource, metric) + key lookup |
| `timestamptz` | `TIMESTAMP(6)` holding UTC; every session pinned to `TIME_ZONE='UTC'` |
| `''` is a value | `''` is `NULL` in Oracle: optional text columns are nullable, mapped to `""` in Go, and filters are assembled in Go so an empty filter adds no predicate |
| Reserved-word-free names | `trigger` → `trigger_kind`, `level` → `lvl` |

**Defects found by running against a live Oracle (all fixed, all covered by tests).**
- go-ora sets the session time zone from the client host (+05:30 here); comparing `TIMESTAMP` columns with bound times then shifted them and retention/series queries returned wrong rows. Fix: a connector that runs `ALTER SESSION SET TIME_ZONE='UTC'` on every new session.
- `DATE` subtraction yields fractional days, so 5 minutes became 299.999… seconds and a boundary sample fell into the previous bucket. Fix: round whole seconds before adding sub-seconds.
- A `CROSS APPLY` correlated to a `TABLE()` collection returned no rows on 23ai, even with literals. Fix: correlated scalar `MAX(ts)` (index min/max probe) joined back on the primary key; the case is now in the shared contract.
- go-ora's `TimeStamp` bind type is rejected by `database/sql` for named binds; plain `time.Time` in UTC round-trips exactly (verified with a non-UTC host zone).
- Identity columns need `CREATE SEQUENCE`; documented in the required grants.

**Alternatives rejected.** An ORM to hide dialects (the hot paths — snapshot apply, job claim, bucketed series — depend on database-specific behaviour an ORM would hide). One lowest-common-denominator SQL file (loses `SKIP LOCKED`-style guarantees). `godror` (cgo + Oracle Instant Client; breaks the static image).

**Consequences.**
- Snapshot apply and sample inserts on Oracle execute one `MERGE` per row inside one transaction (prepared once). Correct and idempotent, but more round trips than PostgreSQL's single `unnest` statement; array binding is a performance follow-up once volumes are known (roadmap Q2).
- Oracle DDL commits implicitly, so a failed migration cannot roll back as a unit; the migration is only recorded after all its statements succeed, and recovery is fix-forward (runbook).
- Required privileges for the schema owner: `CREATE SESSION`, `CREATE TABLE`, `CREATE SEQUENCE`, and a quota on its tablespace.
- Still unverified: Oracle 19c, Autonomous Database (wallet/mTLS connection), RAC failover, and performance at scale.

**Evidence.** `internal/store/oracle/**`, `internal/store/storetest`, `internal/store/backend`, `internal/testdb/oratest`; `make test-oracle`; `make demo-oracle` smoke run; [verification.md](../verification.md) §3.
