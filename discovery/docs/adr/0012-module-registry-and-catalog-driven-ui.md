# ADR 0012 — Module registry and catalog-driven UI for extensibility

**Status:** Accepted.

**Context.** The review asked that the MVP be "easily extensible and easy to integrate any additional domains". Before this change, adding an integration touched `app.go` wiring, the UI (hard-coded type categories, a JSON text box for configuration), and nothing checked that a provider's resource types had metrics or display metadata.

**Decision.**
1. **Modules** (`internal/modules`). A module bundles the discovery providers and metrics collectors of one integration. `modules.Install` builds the set at start-up and refuses mis-wiring (duplicate provider names; a collector for a provider the module does not own). `app.New` only calls `Install(Default(), deps)`.
2. **Resource-type catalog** (`internal/domain/catalog`). Domains (compute, Kubernetes, database, network, storage, serverless, other) and types with display name, icon key, key metric and key properties. Unknown types are still accepted and shown under "other".
3. **Metadata-driven UI.** `GET /api/v1/catalog` returns domains, types, metric definitions, relationship types and installed modules; the UI uses it for icons, names, facets and domain tiles. Provider configuration forms are generated from `configFields` (now with `options` and `placeholder`), so a new provider gets a usable form without front-end code.
4. **Consistency tests** (`modules_test.go`): every type a provider declares and every type a metric applies to must be in the catalog; every catalog key metric must apply to its type; every provider advertising metrics must have a collector.

**Alternatives rejected.**
- *Go `plugin` package / dynamically loaded `.so`*: fragile (exact toolchain match), Linux-only, unsafe to load untrusted code; compiled-in modules keep the static, signed image.
- *Out-of-process plugins over gRPC (HashiCorp go-plugin style)*: right for third-party extensions, but adds a protocol, process supervision and a security review; recorded as roadmap item "partner SDK".
- *UI per integration*: does not scale and was the main source of change per integration.

**Consequences.** Adding a domain is: provider (+ optional collector) package, catalog entries, metric definitions, one module entry — see [../guides/extending.md](../guides/extending.md). Catalog and metric registries are package-level and populated at init; registering at runtime after start-up is not supported (by design).
