# MVP gap review (second pass)

**Scope.** The whole repository was reviewed again after the first MVP delivery, against the original brief, the architecture assessment and the review requests (Oracle and generic database support, UX on par with established tools, extensibility, production roadmap). Every row is labelled: **Closed** (fixed in this pass, with evidence), **Open – MVP** (should be fixed before calling the MVP complete), **Open – production** (needed before production; planned in [../roadmap.md](../roadmap.md)), or **Accepted** (deliberate, with reason).

## 1. Closed in this pass

| # | Gap | Evidence of closure |
|---|---|---|
| G1 | No database monitoring; no Oracle option | `database` provider + engines `postgresql`, `oracle` (ADR 0010); `TestPostgreSQLEndToEnd` on real PostgreSQL; Oracle unit tests; smoke "db.connections=3 (measured)" |
| G2 | Integrations wired by hand in `app.go`; no consistency checks | `internal/modules` + `modules_test.go` (ADR 0012) |
| G3 | UI hard-coded type categories and needed a JSON text box to configure providers | Catalog API; generated forms with options; Vitest "Connections form" test |
| G4 | No faceted exploration; free-text type filter | `GET /resources/facets`, Explorer page; API E2E + Vitest |
| G5 | Time range per chart only; no keyboard navigation; dark mode only via OS | Global time range, command palette, theme toggle; Vitest "CommandPalette" |
| G6 | No fleet view for a domain | `GET /fleet` with batched latest-sample query; Databases page; Vitest "Databases" |
| G7 | No measured data in the offline demo (all simulated) | Demo monitors its own PostgreSQL through a `pg_monitor`-only role |
| G8 | Fixture estate had no database depth | Oracle CDB with PDBs and tablespaces, PostgreSQL databases (simulated, labelled) |
| G9 | Simulated connection/session counts were fractional | Count units rounded in the simulator |
| G20 | Platform store on Oracle (PDF D2) | `store/oracle` + shared contract; `make test-oracle` and `make demo-oracle` pass on Oracle Database Free 23ai (ADR 0011) |
| G10 | Oracle never run live | Oracle Database Free 23ai container: platform-store contract and API suite pass on it, and the monitoring engine passes `TestLiveCDB` as a least-privilege common user (found and documented the `CONTAINER_DATA=ALL` grant). 19c and non-CDB remain (G20b) |

## 2. Open – MVP

| # | Gap | Why it matters | Proposed fix |
|---|---|---|---|
| G10 | Oracle engine not executed against a live Oracle server | Query or privilege mistakes would only surface at a customer | CI job with Oracle Database Free container (CDB + PDB), run `database` provider tests; record in verification.md |
| G11 | Container image not built in this environment (registry proxy blocked) | Image, Trivy scan and SBOM job unproven | Run the CI `package` job on a runner with registry access |
| G12 | GitHub Actions workflow never executed | Pipeline syntax and runner assumptions unproven | Push a branch and run it; fix findings |
| G13 | No browser end-to-end tests (only component tests + manual browser check) | Regressions across pages and the real API | Playwright suite against `make demo` (sign-in, explorer facets, palette, database add flow) |

## 3. Open – production (scheduled in the roadmap)

| # | Area | Gap |
|---|---|---|
| G14 | Identity | OIDC not implemented (ADR 0006); dev tokens only; no RBAC admin UI; no SCIM |
| G15 | Secrets | Only `env:`/`file:` references; no Vault/KMS/cloud secret-manager providers; no rotation workflow |
| G16 | Providers | AWS, Azure, GCP discovery and metrics not ported (legacy collectors depend on an unavailable private framework) |
| G17 | Databases | MySQL, SQL Server, MongoDB engines; query-level analytics (statements, plans, wait events) with PII-safe query normalization |
| G18 | Alerting | No alert rules, notification channels, maintenance windows or incident integration; thresholds/SLOs are Unknown |
| G19 | Data tier | Metrics stay in PostgreSQL with 7-day retention; no downsampling/rollups; columnar store (ClickHouse) is deferred (ADR 0003) |
| G20b | Oracle store breadth | Oracle 19c, Autonomous Database (wallet/mTLS), RAC failover, and bulk-write performance not yet verified |
| G21 | Scale and HA | No load test or capacity model; single-region; no documented RPO/RTO (Unknown) |
| G22 | Self-observability | Logs and Prometheus metrics exist; no OpenTelemetry tracing, no shipped dashboards/alerts for the platform itself |
| G23 | Supply chain | Image signing (cosign) and provenance (SLSA) not configured; SBOM produced but not attested |
| G24 | Compliance | No audit export, data-residency controls, or evidence for any certification — none is claimed |
| G25 | UX | Saved views/dashboards, notifications, i18n (legacy has l10n), accessibility audit with assistive-technology users |
| G26 | Legacy parity | F1–F24 remain on legacy runtimes ([../migration/plan-and-traceability.md](../migration/plan-and-traceability.md)) |

## 4. Accepted

| # | Item | Reason |
|---|---|---|
| A1 | Compiled-in modules rather than dynamic plug-ins | Static signed image; third-party SDK is a roadmap item (ADR 0012) |
| A2 | Counter-derived rates are per process | Correct values or none; a shared counter cache is only needed with multiple collector replicas per connection, which the in-flight guard already prevents per process |
| A3 | Dev-only minimatch advisory in the ESLint toolchain | Unchanged from the first pass; revisit after 2026-12-01 (verification.md) |
