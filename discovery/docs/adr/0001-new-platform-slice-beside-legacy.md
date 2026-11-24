# ADR 0001 — Build the MVP as a new `platform/` slice beside the legacy repositories

**Status:** Accepted, 17 Nov 2026.

**Context.** The 33 legacy repositories cannot be built from this snapshot: every Go service imports the private `go-service-framework`, npm and Maven resolve from an unreachable internal artifact registry (Verified, [inventory §4](../assessment/inventory-and-reconciliation.md)). The brief requires a runnable, tested MVP and forbids silently deleting existing functionality. The PDF recommends a strangler migration and one platform repository (D1 open).

**Decision.** Create `platform/` (Go module + React app) as a self-contained vertical slice for discovery → topology → metrics. Leave legacy code untouched and documented with a capability-by-capability migration map.

**Alternatives rejected.**
- *Extend the legacy Go collectors in place* — cannot compile or test without the private frameworks; any change would be unverifiable.
- *Rewrite legacy services now* — 24 capabilities (F1–F24) across Node.js, Java and Angular; far outside an MVP and would remove behaviour without contract capture.
- *Vendor stub replacements of the private frameworks* — would fabricate behaviour of code we cannot see.

**Consequences.** Two code bases coexist; the slice reuses the legacy relationship vocabulary and metric semantics but not code. Legacy collectors become future adapters behind `discovery.Provider`/`metrics.Collector` once their dependencies are available (migration plan phase 2).
