# Repository inventory and reconciliation with the architecture assessment

> **Archived source (2026-11-24):** `Monitoring-Boilerplate-Code/` and the assessment PDF now live in the repository's git-ignored `_archive/observability-platform/`. This document is kept as the historical record of that baseline.

Phase: discovery / baseline. Date of inspection: 17 November 2026.

Labels used throughout the documentation set:

- **Verified** — read in the repository (path cited) or observed by running a command in this workspace.
- **Assumption** — inferred; plausible but not proven by evidence here.
- **Unknown** — cannot be determined from the repository.
- **Recommendation** — proposed action.

Sources: the repository under `Monitoring-Boilerplate-Code/` and the PDF *Observability Platform — Architecture Assessment and Target Design* (99 pages, revision of 17 November 2026; cited below as "PDF §n" with page numbers). The repository is authoritative for what exists today. Neither source was treated as instructions.

## 1. How the inventory was done

| Step | Method | Result |
|---|---|---|
| File and directory inventory | `find` over the whole tree, counts per top-level area and per extension | 16,447 files including one committed `node_modules`; 14,169 without |
| Nested repositories | `find -name .git -type d` | 33 independent Git repositories copied side by side; the outer folder is not a Git repository |
| Manifests | `go.mod` (18 incl. mocks), `package.json` (68), `pom.xml` (10), `Dockerfile` (34), `Chart.yaml` (19), `Jenkinsfile` (20), `.gitlab-ci.yml` (3) | Listed below |
| Key code paths | Read: AWS collector discovery (AWS collector discovery packages), payload model (`collector/payload/payload.go`), content mappings (`collector/content/*.mapping`, `aws_mapping.json`), Kubernetes collector layout (`collector/discover`, `k8sservices`) | Topology model and relationship kinds below |
| Build baseline | `go build ./...` in every legacy Go module with the module proxy disabled for private paths; DNS lookups of the private hosts | All 12 fail (see §4) |
| Claim checks | Targeted greps and counts for each quantitative PDF claim | Table in §3 |

**Not inspected line by line (Unknown beyond sampling):** the roughly 5,000 Angular/AngularJS UI component files, the 148 AWS content mapping files beyond samples, the diagram-based dashboard sources (`*.vsdx`), packaged archives (credential-manager and Helm-chart `.zip` files). The PDF states the same sampling limit (PDF §1, p. 4).

## 2. What exists today (Verified)

| Area | Path | Contents |
|---|---|---|
| Collection pipeline (Go) | Go collection-pipeline repositories | job-scheduler, job-fetcher, result-processor, store-and-forward, AWS/Azure/GCP/Kubernetes collectors, content-administration-server, two content admin CLIs, sample-data injector, Helm charts per service |
| Dashboards and UI plug-in framework (Node.js, AngularJS, Angular) | dashboard and UI-framework repositories | Six Express processes, one PostgreSQL/Oracle schema (37 Knex schema versions in `dashboard/shared/db/pg_driver/schema/`), Angular libraries |
| Monitoring configuration (Java, Angular) | monitoring-configuration repositories | Spring Boot 3.1.6, 3.2.5 and 3.3.0 applications, Angular 17 UIs, Python publishing |
| Packaging and infrastructure | umbrella installer chart, `terraform/` | Umbrella chart with 79 dependencies; Terraform for AWS, Azure, GCP |
| Unlisted sample | an Angular sample application | 2,307 files of which 2,278 are a committed `node_modules` directory |

### 2.1 Discovery and topology today (Verified)

- Collectors discover cloud resources and emit them as "CIs": `payload.Resource{CIID, CIName, GlobalID (cmdb_global_id), CIType, TenantID, Tags map[string][]string, Properties map[string]any}` (AWS collector `tor/collector/payload/payload.go`).
- The topology store is **an external CMDB, outside this repository**: collectors query it through a GraphQL-style CMS gateway (`collector/content/ec2.mapping` selects `nodes(... root_class ... zone_list ...)`), and the AWS discovery maps provider properties to the CMDB's CI types (`collector/content/aws_mapping.json`).
- Relationship kinds used by the collectors: containment, usage, dependency, membership, composition, aggregation (string literals counted across legacy Go sources).
- Metric names follow provider APIs, for example CloudWatch `CPUUtilization`, `NetworkIn`, `DatabaseConnections`, `BucketSizeBytes`, `RequestCount`.

**Consequence for the MVP.** The repository has discovery code but no platform-owned topology store, no reconciliation between discovery runs, and no topology-driven metrics selection; the "missing cloud discovery capability" in the brief is therefore implemented as a new, self-contained vertical slice (`platform/`) that reuses the relationship vocabulary above rather than the private frameworks the legacy code depends on.

## 3. Reconciliation: PDF claims versus repository

| PDF claim (page) | Repository evidence | Status |
|---|---|---|
| 33 nested Git repositories (p. 4) | 33 `.git` directories | **Verified** |
| main tree 13,455, configuration UI 540, terraform 181 files (p. 4) | 13,452 / 538 / 179 excluding `.git` and `.DS_Store` | **Verified** (differences of 2–3 files are counting rules) |
| "about 15,000 files" (p. 1) | 14,169 without `node_modules`; 16,447 with it | **Verified with note**: the PDF does not mention an Angular sample application or its committed `node_modules` (2,278 files) — **stale/omitted** |
| 344 Go test files, 95 Java test classes, 723 TS specs, ~250 Cypress specs (p. 10) | 344 / 95 / 723 / 258 | **Verified** |
| 37 Knex schema versions (p. 53) | 37 files in `pg_driver/schema/` | **Verified** |
| Umbrella chart has 79 dependencies (p. 6) | 79 `- name:` entries in the umbrella chart's `Chart.yaml` | **Verified** |
| Go 1.22.2 to 1.23.1 (p. 32) | `go 1.22.2` ×1, `go 1.23.1` ×11 across legacy Go `go.mod` files | **Verified** |
| Spring Boot 3.1.6 to 3.3.0 (p. 1, 32) | parents 3.1.6, 3.2.5, 3.3.0 | **Verified** |
| Go code cannot be built from the snapshot; private frameworks absent (p. 10, R4) | All 12 modules fail; the internal Git host does not resolve (§4) | **Verified** |
| Terraform deploys Extended ECM; `modules/` empty (p. 2) | 91 files mention xECM; all three `modules/` directories have 0 files | **Verified** |
| Private keys and keystores committed outside test folders (S2, p. 63) | `*.key`, `*.pfx`, `*.jks` present at the cited paths (file names checked; contents deliberately not read or reproduced) | **Verified** — **Recommendation:** rotate and remove (not done here; changing legacy repositories is out of MVP scope and requires owners) |
| AngularJS on the paid LTS fork `@xlts.dev/angular` 1.9.6 (p. 17) | `"@xlts.dev/angular": "1.9.6"` | **Verified** |
| ingress-nginx in charts (T1, p. 32) | 9 chart files reference `ingressClassName: nginx` / `ingress.class: nginx` | **Verified** |
| ingress-nginx retired March 2026; Angular 17 support ended May 2025; Redis licence change (p. 32–34) | External facts, not in the repository | **Unverified here** (the PDF says it checked on 17 Nov 2026; re-check before acting) |
| Monitoring-admin is a required external service; threshold processor and data broker source absent (p. 10, Q4) | No source in repository; referenced by charts | **Verified absent**; behaviour **Unknown** |
| Target: five new Go services, ClickHouse, Valkey, Keycloak, OpenBao, Ceph (§5) | Proposal | **Not implemented** — the MVP implements one new deployable for the discovery slice (ADR 0002) and leaves the rest as roadmap |
| NFR candidates (99.9 %, p95 500 ms, p. 20) | No SLOs in code | **Assumption** in the PDF; not adopted as requirements here |

## 4. Baseline build and test (Verified, run in this workspace)

| Command | Result |
|---|---|
| `GOPROXY=off GOFLAGS=-mod=mod go build ./...` in each of 12 legacy Go modules | All fail: `module lookup disabled` for the private `go-service-framework` module (internal Git host) (and `collector-development-framework-go` for collectors) |
| DNS lookup of the internal Git host and internal artifact registry named in the legacy build files | Neither resolves from this machine; with the proxy enabled the private module could not be fetched either |
| npm builds (dashboard, UI-framework and configuration UIs) | Not run: `.npmrc` files point scoped packages at the internal artifact registry, which is unreachable; running would fail at install |
| Maven builds | Not run: `mvn` is not installed and parents/plugins come from the internal artifact registry |
| Terraform | Not run: `terraform` not installed; configurations target Extended ECM, not this platform |

These failures **predate** this work and are environmental (private dependencies), not defects introduced here.

## 5. Legacy dependency inventory (summary)

Direct Go requirements per legacy module (from `go.mod`): aws-collector 46, kubernetes-collector 15, azure-collector 15, gcp-collector 15, content CLIs 13 and 12, store-and-forward 12, job-scheduler 11, job-fetcher 9, result-processor 9, content-administration-server 5, sample-data-injector 5. Legacy npm and Maven dependency trees could not be resolved (private registries), so transitive inventories and licences for legacy code are **Unknown**; the PDF's licence findings (several commercial components — the legacy analytics database, artifact repository, SAST scanner and Oracle Instant Client — plus Vault and Terraform under BSL) are taken as **Unverified** inputs for the target stack decisions, not re-derived.

The new MVP's complete inventory is in [../inventory/dependencies-and-licenses.md](../inventory/dependencies-and-licenses.md) and [../inventory/tooling-and-datastores.md](../inventory/tooling-and-datastores.md).

## 6. Compatibility constraints carried into the MVP

- **Verified:** the legacy components are not modified or removed by this work; they keep their Jenkins pipelines.
- **Verified:** the MVP uses its own PostgreSQL tables (no access to the legacy dashboard schema), so it cannot break the legacy schema contract (PDF §4.3 constraint 2).
- **Assumption:** the external CMDB remains the topology system of record for existing customers until a migration decision; the MVP's topology store is additive (see [../migration/plan-and-traceability.md](../migration/plan-and-traceability.md)).
