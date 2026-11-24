# UC-02: Alert ingestion, de-duplication and incident correlation

| Field | |
|---|---|
| **ID / Name** | UC-02 · Alert ingestion and incident correlation |
| **Priority** | 2 |

## Business view

**Problem.** One failure pages several teams, because every service that depends on the failing
one alerts too. Repeated webhook deliveries create duplicates.

**Objectives.** One incident per underlying failure, with every related alert attached and no
duplicates.

**Stakeholders and actors.**

| Actor | Role in this use case |
|---|---|
| Alertmanager | Sends alerts |
| On-call engineer | Receives one incident instead of several pages |
| Service owners | Keep the dependency catalog accurate |

**Expected outcomes.** At least 3:1 alert-to-incident grouping on cascading failures, less noise,
and the right team engaged first.

## Functional requirements

1. Accept Alertmanager v4 webhooks at `POST /api/v1/alerts/alertmanager`, authenticated with a
   bearer token.
2. Accept generic alerts at `POST /api/v1/alerts` (role operator or higher).
3. De-duplicate by fingerprint: a repeat delivery updates the existing alert and does not create a
   new one.
4. A firing alert joins an active incident when two conditions hold:
   - the incident had activity within the window (15 min);
   - the alert's service is in the incident, or within 2 hops of it in the dependency graph.
   Otherwise the alert opens a new incident.
5. When an incident gains a new service, update its severity and scope and queue a new investigation.
6. When every alert of an incident has resolved, auto-resolve the incident and expire any
   remediation proposals that were never approved.
7. A resolved alert that fires again starts a new episode (a new incident).

## Non-functional requirements

| Requirement | Target |
|---|---|
| Ingestion latency | p95 under 500 ms (measured: 39 ms) |
| Idempotency | Safe under repeated delivery |
| Concurrency | Safe with any number of API replicas (PostgreSQL advisory lock around correlation) |
| Payload size | 1 MB limit |

## Prerequisites and dependencies

- An Alertmanager receiver configured to send to Sentinel (`deploy/compose/alertmanager/alertmanager.yml`).
- A service catalog, seeded from `services.yaml` and merged with edges discovered from traces.

## Inputs and outputs

| | |
|---|---|
| **Inputs** | Alertmanager payload: `alerts[].{status, labels{alertname, service, severity}, annotations, startsAt, endsAt, fingerprint, generatorURL}` |
| **Outputs** | `IngestOut{received, created_alerts, updated_alerts, incidents_opened[], incidents_updated[], incidents_resolved[], investigations_queued[]}`, rows in `alerts` and `incidents`, and audit events |

## End-to-end workflow

1. Authenticate the request: the webhook token is compared in constant time, with an API key of
   role operator or higher as the alternative.
2. Parse the payload into normalised `AlertIn` objects. The service comes from the `service`,
   `service_name` or `job` label, in that order.
3. Take the transaction-scoped advisory lock and load the dependency graph.
4. For each alert, upsert it by fingerprint, then do one of the following:
   - if it is firing and not attached, run `find_matching_incident`, then join the incident or open
     a new one;
   - if it is resolved, update the incident and check whether the incident should auto-resolve.
5. Queue an investigation for each opened or changed active incident, unless one is already
   queued or running.
6. Commit, then send notifications for newly opened incidents.

## Component interactions

`api/routers/alerts.py` → `application/alerts.py` (`AlertIngestionService`) →
`domain/correlation.py` and `application/topology.py` → `infrastructure/orm.py`.
See [sequence 7a](../diagrams/README.md#7a-alert-to-identified-root-cause-uc-02-uc-03).

## AI agent responsibilities

Grouping is deterministic and runs before any agent. The Incident Correlation Agent (UC-03) then
explains how the grouped anomalies relate.

## Exception and failure handling

| Case | Behaviour |
|---|---|
| Invalid payload (for example, missing `startsAt`) | 422 with details. Nothing is persisted (one transaction). |
| Unknown service | Accepted. Opens its own incident unless the graph links it to another. |
| Database unavailable | 500. Alertmanager retries the delivery, which is safe because ingestion is idempotent. |
| Notification failure | Logged and counted. Ingestion is unaffected. |

## Security considerations

- The webhook token is separate from API keys and grants nothing else.
- Body size limit, rate limiting, and service-name validation (`^[A-Za-z0-9_.-]+$`).

## Observability requirements

`sentinel_alerts_received_total{source,status}`, `sentinel_incidents_created_total{severity}`,
`sentinel_incidents_active`, and the HTTP latency histograms.

## Acceptance criteria (verified in tests)

- [x] Three cascading alerts delivered in two webhooks produce exactly one incident (`test_end_to_end_incident_lifecycle`).
- [x] The same payload delivered twice creates one alert (`test_duplicate_webhook_is_idempotent`).
- [x] An unrelated service opens a separate incident (`test_unrelated_service_opens_separate_incident`).
- [x] Re-firing after resolution opens a new incident (`test_refiring_after_resolution_opens_new_incident`).
- [x] A bad token returns 401 and a bad payload returns 422 (`test_webhook_rejects_bad_token_and_bad_payload`).

## Testing scenarios

The tests above, plus the correlation unit tests: nearest incident wins, outside the window opens a
new incident, neighbour joins. Under load, 201 alert posts at p95 39 ms with 0 errors.
