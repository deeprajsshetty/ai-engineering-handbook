# UC-05: Service dependency mapping and service health

| Field | |
|---|---|
| **ID / Name** | UC-05 · Dependency map (catalog + trace discovery) and live service health |
| **Priority** | 5 |

## Business view

**Problem.** Dependency diagrams in wikis are always out of date, and correlation and RCA are only
as good as the topology they use.

**Objectives.**

- Build an accurate, automatically maintained dependency graph.
- Give a quick health answer for any service.

**Stakeholders and actors.**

| Actor | Role in this use case |
|---|---|
| Service owners | Curate the catalog |
| SRE | Uses the map and health checks |
| Sentinel | Uses the graph for correlation and RCA |

**Expected outcomes.** Topology without manual upkeep, and faster orientation for new engineers.

## Functional requirements

1. Seed a static catalog from `services.yaml` at startup. Seeding is idempotent and never
   overwrites edits made through the API.
2. Admins can upsert a service, including its team, tier, description, dependencies and
   `runbook_url`, with `PUT /api/v1/services/{name}`.
3. Discover edges from traces using the `traces_service_graph_request_total{client,server}`
   metric. Results are cached for 60 s. The virtual `user` and `unknown` nodes are ignored.
4. `GET /api/v1/services/dependency-map` merges both sources. Each edge is tagged `catalog`,
   `traces` or `both`, which shows drift between documentation and reality.
5. `GET /api/v1/services/{name}/health` returns the current golden signals, the active alert count,
   and a status of `healthy`, `degraded` or `unknown`.

## Non-functional requirements

- Discovery must not fail requests: if Prometheus is down, the catalog alone is used.
- Names must match `^[a-z0-9][a-z0-9-]{0,62}$`.

## Prerequisites and dependencies

The OTel Collector `servicegraph` connector (UC-01) and context propagation between services.

## Inputs and outputs

| | |
|---|---|
| **Inputs** | `services.yaml`, API upserts, the Prometheus service-graph metric |
| **Outputs** | `DependencyMapOut{nodes, edges[{caller, callee, source}]}` and `ServiceHealthOut` |

## End-to-end workflow

1. The Collector pairs client and server spans and increments the edge counters.
2. The topology service runs an instant query
   (`sum by (client, server) (increase(traces_service_graph_request_total[15m]))`), keeps edges
   with a value above 0, and merges them with the catalog edges.
3. The merged graph is used by correlation (UC-02), by investigation scope and ranking (UC-03), and
   by the API.

## AI agent responsibilities

The Correlation and RCA agents consume the graph. The topology signal (an anomalous service whose
dependencies are healthy) carries 25% of the RCA score.

## Exception and failure handling

| Case | Behaviour |
|---|---|
| Prometheus unavailable | Discovery returns an empty graph, logged at INFO. The catalog edges still apply. |
| Invalid names | Rejected with 422 on upsert. Ignored during discovery. |

## Security considerations

- Upserting a service requires the admin role, and the change is audited (`service.upserted`).
- The catalog doubles as the allow-list for remediation targets, so editing it is a privileged
  action.

## Observability requirements

- Telemetry query metrics are labelled `backend=prometheus`.
- The Grafana node graph panel shows the Tempo service map.

## Acceptance criteria

- [x] On the live stack, discovered edges match the real call graph:
  `checkout→inventory`, `checkout→payments`, `frontend→checkout` and `frontend→inventory`.
- [x] Operators get 403 on upsert, admins get 200, and invalid names get 422.
- [x] Health reports `degraded` for a service with an elevated error rate.

## Testing scenarios

See `tests/integration/test_api_platform.py::test_services_topology_and_health` and the topology
unit tests in `tests/unit/test_domain.py`.
