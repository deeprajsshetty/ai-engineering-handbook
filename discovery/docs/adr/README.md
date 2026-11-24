# Architecture decision records

Format: context, decision, alternatives rejected (with reasons), consequences. Status values: Accepted (implemented), Proposed (not yet implemented).

| # | Title | Status |
|---|---|---|
| [0001](0001-new-platform-slice-beside-legacy.md) | New `platform/` slice beside the legacy repositories | Accepted |
| [0002](0002-modular-monolith-with-roles.md) | Modular monolith: one binary, `api` and `worker` roles | Accepted |
| [0003](0003-postgresql-only-store.md) | PostgreSQL as the only required store for the MVP | Accepted |
| [0004](0004-deterministic-ids-and-reconciliation.md) | Deterministic identity, content hashes, complete-run-only missing marking | Accepted |
| [0005](0005-database-backed-job-queue.md) | Database-backed job queue with leases | Accepted |
| [0006](0006-auth-dev-mode-and-oidc-seam.md) | Development auth now, OIDC seam for production | Accepted (dev) / Proposed (OIDC) |
| [0007](0007-one-image-one-chart.md) | One OCI image and one Helm chart for public and private cloud | Accepted |
| [0008](0008-minimal-dependencies.md) | Standard library first; minimal permissive dependencies | Accepted |
| [0009](0009-kubernetes-adapter-without-client-go.md) | Kubernetes adapter on net/http instead of client-go | Accepted |
| [0010](0010-generic-database-integration.md) | Generic database integration with pluggable engines (PostgreSQL, Oracle) | Accepted |
| [0011](0011-platform-store-portability.md) | Platform datastore portability (Oracle as the platform's own store) | Accepted |
| [0012](0012-module-registry-and-catalog-driven-ui.md) | Module registry and catalog-driven UI for extensibility | Accepted |
| [0013](0013-http-router-stdlib-servemux.md) | HTTP router: keep the standard-library `net/http` ServeMux (benchmarked against chi and gin) | Accepted |
