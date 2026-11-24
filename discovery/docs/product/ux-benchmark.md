# UX benchmark: patterns from established observability platforms

**Purpose.** Record which interaction patterns from established platforms were adopted in the MVP, which were deferred, and why — so UX choices are traceable rather than taste.

**Method and evidence.** Public product documentation only (no accounts, no trials, no screenshots). Facts below are labelled **Documented** when a public page states them, or **Assumption** when they come from general product knowledge that was not re-checked for this record. Sources:
- Datadog Database Monitoring overview and data collected: <https://docs.datadoghq.com/database_monitoring/>, <https://docs.datadoghq.com/database_monitoring/data_collected/>
- New Relic service maps: <https://docs.newrelic.com/docs/new-relic-solutions/new-relic-one/ui-data/service-maps/service-maps/>
- Dynatrace Smartscape concepts and "view topology": <https://docs.dynatrace.com/docs/analyze-explore-automate/smartscape/smartscape-concepts>, <https://docs.dynatrace.com/docs/analyze-explore-automate/smartscape/smartscape-modals/smartscape-view-topology>

No feature parity with these products is claimed.

## Patterns and decisions

| # | Pattern | Seen in | Label | MVP decision | Where |
|---|---|---|---|---|---|
| U1 | Common entity model with per-type views; databases as first-class entities with an engine-agnostic list plus engine depth | Datadog DBM collects per-engine metrics described per integration (Postgres, Oracle, …) | Documented | **Adopted**: canonical `database.*` types for every engine; one Databases fleet view; engine-specific properties on detail pages | `pages/Databases.tsx`, ADR 0010 |
| U2 | Service/topology map with filters by entity type and relationship depth | New Relic service maps filter by health, entity types and depth (default depth 1 shows direct neighbours) | Documented | **Adopted** (already in MVP): topology map with type filter, neighbourhood depth 1 on detail | `pages/Topology.tsx`, `ResourceDetail.tsx` |
| U3 | See an entity's topology without leaving the current page; node selection opens a detail panel with properties and key metrics | Dynatrace Smartscape "view topology" modal and details panel | Documented | **Adopted** (already in MVP): side panel on the topology page, neighbourhood graph on detail | `ResourcePanel` |
| U4 | One global time-frame selector applied across views | Dynatrace global timeframe selector | Documented | **Adopted**: top-bar time range (15 m – 7 d), persisted per viewer, used by all metric charts | `lib/context.tsx`, `App.tsx` |
| U5 | Faceted entity search with counts, filter chips, shareable URLs | Entity explorers in commercial tools | Assumption | **Adopted**: Explorer with domain/type/engine/provider/region/env/status facets; each facet ignores its own filter; state in the URL | `pages/Resources.tsx`, `GET /resources/facets` |
| U6 | Keyboard-first navigation / quick search | Command palettes in modern developer tools | Assumption | **Adopted**: Ctrl/Cmd+K or "/" palette for pages, domains, resources, time range and theme; ARIA combobox | `components/CommandPalette.tsx` |
| U7 | Light and dark themes | Common across these products | Assumption | **Adopted**: system/light/dark toggle; tokens redefined for dark mode | `styles.css` |
| U8 | Guided "add integration" flow with per-integration fields and required privileges | Integration set-up pages | Assumption | **Adopted**: provider cards, generated form, engine select shows the minimum grants | `pages/Connections.tsx` |
| U9 | Provenance of data is visible | — (MVP-specific requirement) | Brief | **Kept**: simulated vs measured badges everywhere a value is shown | `SimulatedBadge` |
| U10 | Query-level database analytics: query samples, explain plans, wait events, blocking | Datadog DBM (query metrics, samples, explain plans, wait events, blocking queries) | Documented | **Deferred**: needs query-text handling (PII risk), agent or extensions such as `pg_stat_statements`; roadmap Phase 2 | [../roadmap.md](../roadmap.md) |
| U11 | Health states, anomaly detection, alerts | All three | Assumption | **Deferred**: needs agreed thresholds/SLOs, which are Unknown; the MVP shows values only and never colours them against invented thresholds | roadmap Phase 1–2 |
| U12 | Saved views and dashboards | All three | Assumption | **Partly**: URL-encoded explorer views can be bookmarked; saved dashboards are roadmap Phase 2 | — |

## Accessibility and responsiveness checks applied

Skip link; landmark navigation; every icon has a text label or is hidden from assistive tech; palette is a labelled combobox with `aria-activedescendant`; facets are fieldsets with legends; meters expose `role="meter"` and values; layout collapses to one column at ≤ 760 px with no horizontal page scroll (tables scroll inside their container).
