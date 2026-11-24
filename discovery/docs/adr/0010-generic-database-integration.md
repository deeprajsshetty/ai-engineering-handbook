# ADR 0010 — Generic database integration with pluggable engines (PostgreSQL, Oracle)

**Status:** Accepted. PostgreSQL verified against a real server; Oracle verified against a live Oracle Database Free 23ai container database (`TestLiveCDB`) as a least-privilege common user. Not yet run against Oracle 19c or a non-CDB.

**Context.** The MVP review asked for Oracle support and "a generic design so any database can be integrated". The legacy product already ships PostgreSQL and Oracle drivers for its own schema (`pg_driver`/`ora_driver`, Verified in the assessment). Commercial tools treat databases as first-class entities with a common health view across engines plus engine-specific depth (see [../product/ux-benchmark.md](../product/ux-benchmark.md)). Constraints from the brief: read-only by default, credentials only by reference, egress restricted, no unverified claims.

Two readings of "Oracle support" exist: (a) *monitor* Oracle databases; (b) run the platform's *own* store on Oracle. This ADR covers (a). (b) is ADR 0011 (Proposed).

**Decision.**

1. One discovery provider, `database`, and one measured collector, `database-statistics`, both engine-agnostic. The engine is a connection setting (`engine: postgresql | oracle`), offered to the UI as an enumerated option.
2. Engine-specific code lives behind `dbengine.Engine` (`Open`, `Inventory`, `Sample`, `MinimumPrivileges`). Engines depend on a tiny `Querier` interface, not `*sql.DB`, so they are unit-testable with canned results (`dbengine/enginetest`).
3. A provider-neutral database model shared by all engines:

   | Canonical type | PostgreSQL | Oracle |
   |---|---|---|
   | `database.instance` | cluster (identity: `system_identifier`, fallback host:port) | instance (identity: DBID + instance name) |
   | `database.database` | database (`pg_database`) | PDB (`V$PDBS`), or the database itself for a non-CDB |
   | `database.tablespace` | tablespace (instance-level) | tablespace (per PDB; root tablespaces under the instance) |

   Edges: `instance contains database`, `instance|database contains tablespace` (acyclic `contains`, ADR 0004 rules apply).
4. Engine-neutral metrics added to the catalogue: `db.connections`, `db.sessions.active`, `db.transactions.rate`, `db.cache.hit_ratio`, `db.storage.utilization`, plus `storage.used` for databases and tablespaces. Engines report only what they can measure; missing values are shown as "—", never estimated.
5. Drivers: PostgreSQL through the existing `pgx` (no new dependency); Oracle through `github.com/sijms/go-ora/v2` v2.9.0 (MIT, pure Go wire protocol, no Oracle Instant Client, so the static distroless image is unchanged). Both accept a custom dialer, which is set to `netguard.Policy.Dialer` so every resolved address is re-checked (SSRF and DNS-rebinding defence, same as HTTP providers).
6. Read-only enforcement in depth: least-privilege roles documented per engine (`pg_monitor`; `SELECT_CATALOG_ROLE` or explicit `V$`/`DBA_` grants); PostgreSQL sessions additionally set `default_transaction_read_only=on` and `statement_timeout=5s`; engines issue only catalog/statistics `SELECT`s with row caps.
7. TLS: `verify-full` by default, `require` for private networks with internal CAs, `disable` only when `OBS_ALLOW_INSECURE_ENDPOINTS=true` (development).

**Alternatives rejected.**
- *One provider per engine* (`postgresql`, `oracle`, …): duplicates validation, egress, credentials and topology shaping per engine; the UI would need per-engine pages.
- *`godror` (Oracle's official cgo driver)*: requires Oracle Instant Client and cgo, breaking the static image and the multi-arch build; licence terms of the client libraries add review cost.
- *Agent-based collection (sidecar on each DB host)*: better for query-level analytics and host metrics, but a second deployable and an install burden; recorded as a roadmap item for query analytics.
- *Generic "run this SQL" connector*: maximally flexible but turns a monitoring credential into an arbitrary query channel and makes metric semantics unknowable.

**Consequences.**
- Adding MySQL, SQL Server, Db2 or MongoDB is one package implementing `Engine` and one line in `modules.DatabaseEngines()` — no change to API, store, scheduler or UI ([../guides/extending.md](../guides/extending.md)).
- Counter-derived rates (PostgreSQL transactions/s) need two snapshots per process; the first collection after start-up and any statistics reset produce no rate sample (documented in LLD §9.6b).
- PostgreSQL cache-hit ratio is cumulative since the last statistics reset; Oracle's is the server's 60-second value. The metric description says "share of block reads served from the buffer cache" for both; the per-engine difference is documented here.
- The live run found a missing grant: a common user with `SELECT_CATALOG_ROLE` sees only the root container in `V$PDBS` and `CDB_*` views until `ALTER USER … SET CONTAINER_DATA=ALL CONTAINER=CURRENT` is run. It is now part of the documented grants and the test. Still to do: 19c and a non-CDB.
- The demo monitors its own local PostgreSQL through a `pg_monitor`-only role, so measured database metrics are visible offline.

**Evidence.** `internal/dbengine/**`, `internal/discovery/database`, `internal/metrics/dbmetrics`; tests `TestPostgreSQLEndToEnd` (real PostgreSQL 18.3), `TestDialTimeGuard`, `TestValidateConfig`, Oracle `TestInventoryCDB`/`TestInventoryNonCDB`/`TestSample`; smoke output "database … db.connections=3 (measured)".
