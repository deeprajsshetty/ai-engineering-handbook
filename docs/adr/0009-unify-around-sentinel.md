# ADR-0009: Unify the observability products around Sentinel

**Status:** Accepted · **Date:** 2026-11-24

## Context
The repository held two unrelated observability products:

* `sentinel/` (Python): alerts → correlation → a deterministic agent pipeline → root cause →
  human-approved remediation.
* `observability-platform/` (Go + React): resource discovery, topology reconciliation and
  topology-driven metrics. It also held `Monitoring-Boilerplate-Code/`, a snapshot of 33 legacy
  repositories that cannot be built, and an assessment PDF.

They shared no code, contracts or deployment. Sentinel's dependency graph knew only application
services, while the Go platform knew the infrastructure under them (pods, nodes, databases). The
Go platform's CI lived in a nested `.github/` that GitHub never runs. The legacy snapshot
contained embedded `.git` directories, which would have turned into empty gitlinks on commit.

## Decision
* **Sentinel is the product core.** The Go platform becomes its **discovery service**, renamed
  from `observability-platform/` to `discovery/`. The Go module path stays the same, because Go
  imports do not depend on the folder name, so no Go code changes.
* **Integration is read-only and one-directional.** Sentinel's `HttpDiscoverySource` calls only
  `GET /api/v1/resources` and `GET /api/v1/resources/{id}/neighborhood`. A new pipeline step,
  `infrastructure_context`, sits after `telemetry_analysis`. It maps services to resources by
  exact name and configured types, and records infrastructure issues (missing or stale).
* **Context, not control.** Infrastructure findings appear in the report and the RCA narrative.
  They do **not** change the ranking or the recommendations. A scoring component is added only
  after the evaluation set shows that it helps (consistent with ADR-0007).
* **Optional at runtime.** The step is skipped when `SENTINEL_DISCOVERY_URL` is unset, and records
  a data gap when discovery is down. In Compose, discovery is a `discovery` profile
  (`make up-discovery`), so the default stack and its e2e test are unchanged.
* **Separate databases.** Each product keeps its own PostgreSQL database and migrations.
* **CI at the root.** `.github/workflows/discovery-ci.yml` is path-filtered to `discovery/**`.
* **Archive, don't delete.** The legacy snapshot, the PDF and the nested workflow move to the
  git-ignored `_archive/`, with a manifest, for manual deletion.

## Alternatives
* **Rewrite discovery in Python inside Sentinel.** Rejected: it would discard about 13,800 lines of
  tested Go (store contracts for PostgreSQL and Oracle, a reconciliation engine, the UI) for no
  functional gain.
* **Build an agent layer inside the Go platform.** Rejected: it would duplicate Sentinel's
  pipeline, approval workflow and audit in a second language.
* **Merge the two databases.** Rejected: the schemas, retention and migration tooling differ.
  Separate stores keep failure and upgrade domains apart.
* **Let discovery findings re-rank root causes now.** Deferred: there is no labelled data yet to
  set a weight.

## Consequences
* One entry point (`README.md`, [unified platform guide](../guide/unified-platform-guide.md)) and
  one local stack.
* Production use of the integration is **blocked on discovery's production auth**. Its dev
  tokens are refused when `OBS_ENV=production`, and OIDC or service tokens are not implemented.
  Until then, `SENTINEL_DISCOVERY_URL` stays unset in production.
* With its simulated estate, discovery has no resources named after the demo shop services, so
  the Compose demo proves the wiring but not useful context. Real context needs a Kubernetes
  connection.
* When the discovery API contract changes, `sentinel/src/sentinel/infrastructure/discovery.py`
  and its tests must change too.
