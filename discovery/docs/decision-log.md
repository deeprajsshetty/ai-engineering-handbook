# Decision log

| Date (UTC) | Decision | Why | Record |
|---|---|---|---|
| 2026-11-17 | Treat the repository as authoritative; the PDF as evidence to verify | Brief | [inventory](assessment/inventory-and-reconciliation.md) |
| 2026-11-17 | Build a new `platform/` slice; do not modify legacy repositories | Legacy cannot be built (private frameworks) | ADR 0001 |
| 2026-11-17 | One binary with `api`/`worker` roles | One data-ownership boundary | ADR 0002 |
| 2026-11-17 | PostgreSQL only; ClickHouse/Valkey deferred | Evidence-backed store; minimise stateful deps | ADR 0003 |
| 2026-11-17 | Deterministic IDs; missing only on complete runs; purge window | Idempotency and no data loss on partial outages | ADR 0004 |
| 2026-11-17 | DB-backed job queue with leases | Multi-replica safe; transactional with job state | ADR 0005 |
| 2026-11-17 | Dev auth now, OIDC seam; chart defaults to production+OIDC (fails fast) | No IdP available; never present dev auth as production | ADR 0006 |
| 2026-11-17 | One image, one chart, overlays per environment | Same artifact everywhere | ADR 0007 |
| 2026-11-17 | Minimal deps; TypeScript 6.0.3 (not 7.x) for typescript-eslint compatibility | Smaller supply chain | ADR 0008 |
| 2026-11-17 | Kubernetes adapter on net/http | Small, reviewable, SSRF-safe | ADR 0009 |
| 2026-11-17 | Fixture drift limited to the east database label (was both regions) | Matches the documented scenario; found by E2E test | `discovery/fixture/fixture.go` |
| 2026-11-17 | Single catch-all route for SPA + API 404 | `GET /` and `/api/` patterns conflicted (panic found by `make demo`) | `api/api.go`, `TestStaticUIAndFallbacks` |
| 2026-11-17 | Removed `# syntax=` directive from Dockerfile | Unneeded external frontend pull | `deploy/docker/Dockerfile` |
| 2026-11-17 | Pin Go toolchain 1.27.4 (was 1.27.3) | govulncheck found GO-2026-6859 (net/http MaxBytesReader race) reachable from the API | `platform/go.mod`, Dockerfile |
| 2026-11-17 | Keep eslint 10.14.0 / typescript-eslint 8.72.1 despite a moderate minimatch advisory; revisit after 2026-12-01 | Fixes are < 2 weeks old; dev-only tool linting our own code | verification.md #9 |
| 2026-11-17 | Scope Go commands to `./cmd/... ./contracts/... ./internal/...` | `./...` descended into `web/node_modules` (a dependency ships Go files) | `Makefile` |
| 2026-11-17 | One generic `database` provider with pluggable engines; PostgreSQL via pgx, Oracle via go-ora v2.9.0 (MIT, pure Go) | Review request: Oracle + any database; static image preserved | ADR 0010 |
| 2026-11-17 | Oracle as the platform's own store: design only (superseded the same day) | Needed an Oracle instance | ADR 0011 |
| 2026-11-17 | Compiled-in modules + resource-type catalog served to the UI | New domains without API/UI changes; start-up consistency checks | ADR 0012 |
| 2026-11-17 | Facets ignore their own filter; fleet uses one LATERAL probe per resource×metric | Standard faceted UX; O(R·M·log n) latest values | LLD §9.6a |
| 2026-11-17 | Demo monitors its own PostgreSQL through a `pg_monitor`-only role | Measured database data offline, least privilege | `scripts/dev-postgres.sh monitor-role` |
| 2026-11-17 | No health colouring of metric values | Thresholds/SLOs are Unknown; values are shown, never judged | [UX benchmark](product/ux-benchmark.md) U11 |
| 2026-11-17 | Build the Oracle platform store (`OBS_STORE_DRIVER=oracle`) behind `store.Backend`; shared store contract suite | Stakeholder request; Oracle Database Free container made live testing possible | ADR 0011 |
| 2026-11-17 | Pin Oracle sessions to UTC; round epoch seconds; replace CROSS APPLY over TABLE() with scalar MAX | Three defects found only against a live Oracle 23ai | ADR 0011 "Defects found" |
| 2026-11-17 | Oracle snapshot apply uses per-row MERGE in one transaction | Correctness first; array binding deferred until volumes are known | ADR 0011 consequences |
| 2026-11-18 | Removed vendor and legacy product names, internal hostnames and internal repository paths from MVP docs; replaced with generic descriptions | Review request; MVP must not carry proprietary references | `README.md`, `docs/**` |
| 2026-11-18 | Keep `net/http` ServeMux; gin/chi measured and rejected | Router cost is ~0.003 % of a typical request; gin adds ~45 modules | ADR 0013 |
| 2026-11-18 | Build the Prometheus label escaper once | It caused ~90 % of per-request allocations; full stack 3.6× faster | `internal/telemetry`, ADR 0013 |
