# Verification record

Environment: macOS (Darwin 25.6, arm64), Go 1.27.4 (toolchain pinned in `platform/go.mod`), Node 24.21.0, PostgreSQL 18.3 (Homebrew binaries), Docker 29.4.1, Helm 3.22.1. Date: 17 November 2026. Every result below was observed in this workspace; nothing is reported as passed that did not run.

## Results

| # | Command | Outcome |
|---|---|---|
| 1 | Legacy baseline: `go build ./...` in each of 12 Go collection-pipeline modules under `Monitoring-Boilerplate-Code/` | **Fail (pre-existing)**: private module `go-service-framework` (internal Git host) unavailable; host does not resolve. npm/Maven legacy builds not run (internal registry unreachable, `mvn` absent). |
| 2 | `make lint` (gofmt check, `go vet` on `./cmd/... ./contracts/... ./internal/...`, ESLint) | **Pass** |
| 3 | `make typecheck` (`tsc -b`, strict) | **Pass** |
| 4 | `make test-go` (`go test -race -count=1 -coverpkg=./internal/...`, `OBS_TEST_REQUIRE_DB=1`, throwaway PostgreSQL 18.3 per test binary) | **Pass**: 7 test packages, 47 top-level test functions, combined statement coverage of `internal/...` **74.2 %** |
| 5 | `make test-web` (Vitest + Testing Library, jsdom) | **Pass**: 7 tests |
| 6 | `npm run build` (Vite) | **Pass**: JS 261 KB (80 KB gzip), CSS 9 KB |
| 7 | `govulncheck@v1.1.4` on Go 1.27.3 | **Found GO-2026-6859** (net/http MaxBytesReader header race, server crash), reachable from the API. **Fixed** by pinning `toolchain go1.27.4` and `golang:1.27.4-alpine`; re-scan: **No vulnerabilities found**; tests re-run on 1.27.4: pass |
| 8 | `npm audit --omit=dev` | **0 vulnerabilities** (production bundle: react, react-dom, scheduler) |
| 9 | `npm audit` (all) | 8 moderate, all one advisory (minimatch GHSA-g3hv-75wg-m249) in the ESLint/typescript-eslint dev toolchain. Fixed versions (eslint 10.15.0, typescript-eslint 8.73.0) were published 0–1 days earlier; **accepted temporarily** (dev-only, lints our own code); bump after 2026-12-01 |
| 10 | `make demo` (local PostgreSQL, build, migrate, seed, serve, smoke) | **Pass** — first run found two real bugs, both fixed and covered by tests: smoke raced server start-up; with `OBS_WEB_DIR` set, route patterns `GET /` and `/api/` conflicted and the server panicked (`TestStaticUIAndFallbacks` added) |
| 11 | Clean-checkout run: copy without `.local`, `node_modules`, `dist`, `bin`, then `make demo` | **Pass** (`npm ci` from lockfile, fresh database, `SMOKE PASS`) |
| 12 | Smoke output (both runs) | discovery `added=32`; topology 32 active resources, 66 relationships; graph 32 nodes, 66 edges, no containment cycles; 60 simulated `cpu.utilization` points; web UI served |
| 13 | Graceful shutdown (`make demo-stop` → SIGTERM) | Log shows `shutting down`, no errors |
| 14 | Browser check (desktop app browser pane) | Sign-in, overview with simulated banner, run discovery from UI, drift visible (32 active after second run: web-03 missing, batch-01 added), layered topology graph, resource detail with metrics charts, units, simulated badges and neighbourhood graph; no console errors; at 375 px width no horizontal page scroll. Fixes applied: graph opens centred; nav labels no longer wrap |
| 15 | Mermaid: parse + render all 14 diagrams in `docs/` with mermaid 12.2.0 in the browser | **Pass** after fixing one sequence diagram (`;` is a statement separator) |
| 16 | Markdown relative links and anchors in README and docs | **Pass** after adding this file |
| 17 | `helm lint deploy/helm/obs-platform` | **Pass** (info: icon recommended) |
| 18 | `helm template --kube-version 1.31.0` with `values-public-cloud.yaml`, `values-private-cloud.yaml`, `values-dev.yaml` | **Pass**: renders 2 Deployments (api, worker), Service, ServiceAccount(s), migrate Job hook, 2 NetworkPolicies, plus HTTPRoute + PDB + ServiceMonitor (public), Ingress + PDB (private) |
| 19 | `kubectl apply --dry-run=client` of rendered manifests | **Not run**: no reachable cluster (kubeconfig points to a stopped minikube) |
| 20 | `make docker-build` / `docker compose up --build` | **Blocked (environment)**: the Docker Desktop daemon routes registry traffic through its internal proxy `http.docker.internal:3128`, and pulls of `golang`, `postgres` and the `docker/dockerfile` frontend made no progress for > 10 minutes, although the host itself reaches Docker Hub. The image has therefore **not** been built here; the CI `package` job builds, scans and smoke-tests it |
| 21 | `scripts/license-inventory.sh` and `scripts/go-sbom.sh` | **Pass**: inventory generated; server binary links 6 Go modules (MIT, BSD-3-Clause) |
| 22 | GitHub Actions workflow | **Not executed** (no runner here); its steps map to Makefile targets that were run above |

## Second pass (generic databases, modules, UX) — same environment, 17 November 2026

| # | Command | Outcome |
|---|---|---|
| 23 | `go get github.com/sijms/go-ora/v2@v2.9.0`, `go mod tidy`, `go mod verify` | **Pass** (release dated 2025-06-09; MIT) |
| 24 | `make lint`, `make typecheck` | **Pass** (first run found a gofmt issue in `domain/catalog`; fixed) |
| 25 | `make test-go` (`-race`, `OBS_TEST_REQUIRE_DB=1`, real PostgreSQL 18.3) | **Pass**: 10 test packages, 55 top-level test functions (excluding `TestMain`), combined coverage of `internal/...` **76.8 %** |
| 26 | PostgreSQL engine end to end (`TestPostgreSQLEndToEnd`): validate, check, discover, collect twice | **Pass** against the real test server: instance, databases, tablespaces; measured connections, sessions, cache hit, storage; transactions/s from the second snapshot |
| 27 | Oracle engine (`TestInventoryCDB`, `TestInventoryNonCDB`, `TestSample`, `TestErrorsPropagate`, `TestOpenDoesNotConnect`) | **Pass on canned results only.** Not executed against a live Oracle server (none available) — open item G10 |
| 28 | Egress guard for database dials (`TestDialTimeGuard`) | **Pass**: allow-listed name resolving to loopback refused at dial time |
| 29 | Module/catalog consistency (`TestDefaultInstallsAndIsConsistent`, `TestInstallRejectsMiswiring`) | **Pass** |
| 30 | API E2E additions: catalog, facets (`engine` counts under `domain=database`: oracle 6, postgresql 4), fleet latest values, 400s for unknown domain / missing type | **Pass**; `TestOpenAPIMatchesRouteTable` first failed (3 routes not in spec), spec updated, **Pass** |
| 31 | `make test-web` | **Pass**: 11 tests (new: Explorer, CommandPalette, Connections form, Databases) |
| 32 | `make vuln` | govulncheck: **No vulnerabilities found** (includes go-ora); `npm audit --omit=dev`: **0** |
| 33 | `make licenses` | **Pass**: inventory regenerated; go-ora listed as MIT |
| 34 | Clean-checkout `make demo` (copy without `.local`, `node_modules`, `dist`, `bin`) | **Pass**: `added=41`; 41 resources, 77 relationships, no containment cycles; 60 simulated CPU points; measured database `db.connections=3`; UI served; `SMOKE PASS` |
| 35 | Browser check (desktop app browser pane) | Overview domain tiles, Explorer facets and chips, command palette search (Ctrl+K) and keyboard selection, Databases fleet with Oracle (simulated) and PostgreSQL (measured, transactions/s shown), dark theme, 375 px width without horizontal page scroll. Fixes applied: palette shows "Searching…" while pending and closes on navigation; type column de-cluttered; simulated connection counts rounded |
| 36 | Mermaid 12.2.0 render of the 4 new or changed diagrams (README, HLD T2, X1, R1) | **Pass** (the other 14 are unchanged since #15) |
| 37 | Container image, CI workflow, Kubernetes dry-run | **Not run** — unchanged from #19, #20, #22 |

## Third pass: Oracle as the platform store (ADR 0011), same environment, 17 November 2026

| # | Command | Outcome |
|---|---|---|
| 38 | `docker pull gvenzl/oracle-free:23-slim-faststart` | **Pass** this time (the earlier registry stall, #20, did not recur); Oracle Database Free 23ai started on 127.0.0.1:15210 via `scripts/dev-oracle.sh` |
| 39 | Store contract (`storetest`, 9 cases) + `TestMigrations` on Oracle, `-race` | First runs failed and exposed 5 defects (missing `CREATE SEQUENCE` grant; `TimeStamp` bind rejected; session time zone +05:30 shifting comparisons; 5-minute bucket rounding; CROSS APPLY over `TABLE()` returning no rows). All fixed (ADR 0011). Final: **Pass** |
| 40 | Same contract on PostgreSQL | **Pass** (the fleet-lookup case was added to the contract for both backends) |
| 41 | Full API end-to-end suite with `OBS_TEST_STORE=oracle`, `-race` | **Pass** (4 tests: OpenAPI parity, static UI, discovery→topology→metrics incl. facets/fleet/catalog, cancel/failure) |
| 42 | `make test-oracle` | **Pass** |
| 43 | `make test-go` with Oracle enabled | **Pass**; 50 top-level test functions (the 9-case store contract runs as subtests on each backend); combined coverage of `internal/...` **74.9 %**. Without Oracle (`make test`, Oracle tests skip) coverage is 61.4 % |
| 44 | `make lint`, `make typecheck`, `make test` (PostgreSQL), `make vuln` | **Pass**; govulncheck: no vulnerabilities; `npm audit --omit=dev`: 0 |
| 45 | `make demo-oracle` (Oracle platform store; local PostgreSQL still monitored) | **Pass**: migrations `0001_core`, `0002_metrics` applied on Oracle; `added=41`, 41 resources, 77 relationships, no containment cycles, 60 simulated CPU points, measured PostgreSQL metrics, UI served, `SMOKE PASS`. Server log: 0 errors, neither Oracle password present |
| 46 | Browser check on the Oracle-backed server | Explorer with domain/engine facets and chips renders identical data |
| 47 | `helm lint` + `helm template` with `values-private-cloud.yaml` + `values-oracle.yaml`; other overlays | **Pass**: `OBS_STORE_DRIVER=oracle`, NetworkPolicy port 1521; other overlays render `postgres` |
| 48 | Mermaid 12.2.0 render of changed diagram parts (README, HLD T2 store nodes, R1) | **Pass** |
| 49 | Markdown links and anchors | **Pass** |
| 50 | CI `oracle-store` job | **Not executed** (no runner here); it runs the same commands as #39–#41 |
| 51 | Oracle **monitoring** engine against the live CDB (`TestLiveCDB`) as a common user with `CREATE SESSION` + `SELECT_CATALOG_ROLE` | First run **failed**: no PDBs visible. Cause: Oracle restricts common users to root-container data until `ALTER USER … SET CONTAINER_DATA=ALL CONTAINER=CURRENT`. Grant added to the test and documentation; rerun **Pass**: version 23.0.0.0.0, PDB `FREEPDB1`, 10 tablespaces, all 6 instance metrics measured. This supersedes #27 for Oracle 23ai |
| 52 | Oracle 19c, non-CDB, Autonomous Database, RAC | **Not run** |

## Fourth pass: proprietary references and HTTP router (18 November 2026)

| # | Command | Outcome |
|---|---|---|
| 53 | Case-insensitive scan of every text file outside `Monitoring-Boilerplate-Code/`, `node_modules`, `.local`, `dist` for vendor, legacy-product, internal-host and internal-path names | Before: matches only in `README.md`/`docs/` (8 files). Code, Helm chart, scripts, CI and UI had none. After rewriting to generic terms: **no matches** |
| 54 | `tools/routerbench` (our 32-route table) | ServeMux 179 ns/0 allocs; chi 203 ns/2 allocs; gin 45 ns/0 allocs (ADR 0013) |
| 55 | `BenchmarkHandlerStack` (full middleware stack, no DB) | Before: 6,839 ns, 28.7 KB, 76 allocs per `/healthz`. Profile: 91 % of bytes from building a `strings.Replacer` per metric update. After fix: 1,890 ns, 1.5 KB, 39 allocs |
| 56 | `make lint typecheck test`, `make test-oracle`, `make vuln`, `make demo-oracle` | **Pass** (11 web tests; Go suites on PostgreSQL and Oracle; no vulnerabilities; `SMOKE PASS`) |

## Reproduce

```bash
make lint typecheck test
make vuln
make demo
```

Oracle as the platform store (needs Docker):

```bash
make test-oracle
make demo-oracle
```

Then, when finished:

```bash
make demo-stop
```
