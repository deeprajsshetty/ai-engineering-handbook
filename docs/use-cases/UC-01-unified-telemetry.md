# UC-01: Unified telemetry collection and visualisation

| Field | |
|---|---|
| **ID / Name** | UC-01 · Unified telemetry (metrics, logs, traces, APM, infrastructure, dashboards) |
| **Priority** | 1 (foundation for every other use case) |

## Business view

**Problem.** Metrics, logs and traces live in different tools without links between them, and
some services are not traced at all. During an incident, engineers switch tools and copy IDs by
hand.

**Objectives.**

- Use one open-standard pipeline for all signals.
- Use consistent service naming.
- Link the signals both ways: from metrics to traces to logs, and back.
- Watch the infrastructure and the database with the same stack.

**Stakeholders and actors.**

| Actor | Role in this use case |
|---|---|
| Service owners | Instrument their services |
| Platform team | Operates the Collector and the backends |
| SRE / on-call | Use the dashboards |
| Sentinel | Consumes the telemetry through queries |

**Expected outcomes.**

- One place to look during an incident.
- Lower tooling cost (open source and object storage).
- No vendor lock-in.

## Functional requirements

1. Services export traces, metrics and logs over OTLP to a Collector.
2. The Collector routes metrics to Prometheus, logs to Loki and traces to Tempo. It also derives a
   service graph and span RED metrics from traces.
3. The Collector scrapes host metrics (CPU, memory, load, network, filesystem) and PostgreSQL metrics.
4. Grafana provides three dashboards:
   - service golden signals (with dependency map, error logs and error traces);
   - the Sentinel platform;
   - infrastructure and database.
5. Logs carry `trace_id` and `span_id`, so Grafana can link a log line to its trace and a trace to
   its logs.

## Non-functional requirements

| Requirement | Target |
|---|---|
| Metric delay from export to dashboard | 30 s or less (10 s export, 15 s scrape) |
| Collector protection | `memory_limiter` (80% limit) and batching |
| Retention | Prometheus 7 days, Loki 7 days, Tempo 72 hours (configurable) |
| Instrumentation overhead | Batch span processor, async exporters |

## Prerequisites and dependencies

- OpenTelemetry SDK in each service.
- Network path from the services to the Collector on port 4317 or 4318.
- Backends running: `deploy/compose` locally, Helm charts in Kubernetes.

## Inputs and outputs

| | |
|---|---|
| **Inputs** | OTLP spans, metrics and logs. Host `/proc` (via hostmetrics). PostgreSQL statistics. |
| **Outputs** | Prometheus series `shop_server_requests_total`, `shop_server_duration_seconds_*`, `traces_service_graph_request_total`, `traces_span_metrics_*`, `system_*` and `postgresql_*`. Loki streams labelled `service_name`. Tempo traces. Grafana dashboards. |

## End-to-end workflow

1. A request reaches `frontend`. The OTel FastAPI instrumentation starts a server span, and the
   httpx instrumentation propagates W3C `traceparent` to `checkout`.
2. Middleware in each service records `shop.server.requests` and `shop.server.duration`. Errors are
   logged at ERROR with the active trace context.
3. The SDK exports in batches to the Collector (traces and logs as they come, metrics every 10 s).
4. Collector pipelines run:
   - `traces` goes to Tempo, the `servicegraph` connector and the `spanmetrics` connector;
   - `metrics` goes to the Prometheus exporter (`:8889`);
   - `logs` goes to Loki over OTLP.
5. Prometheus scrapes the exporter every 15 s and evaluates the recording rules
   (`shop:error_ratio:1m` and others).
6. Grafana queries all three stores. Data-source correlations enable trace and log jump links.

## Component interactions

See [diagram 4](../diagrams/README.md#4-end-to-end-data-flow) and
[diagram 10](../diagrams/README.md#10-observability-and-monitoring-architecture).
Configuration: `deploy/compose/otel-collector/config.yaml`, `prometheus/`, `loki/`, `tempo/`, `grafana/`.

## AI agent responsibilities

None directly. This use case produces the evidence the Telemetry Analysis Agent queries (UC-03).

## Exception and failure handling

| Failure | Behaviour |
|---|---|
| Collector down | SDK exporters retry and then drop (bounded queues). `TelemetryPipelineDown` alert fires. |
| A backend down | The other pipelines keep flowing. Sentinel records a data gap (UC-03). |
| Memory pressure | `memory_limiter` refuses data rather than letting the Collector crash. |

## Security considerations

- OTLP ports are internal only in Kubernetes (NetworkPolicy).
- Never log secrets or PII. Log attributes are structured, so redaction processors can be added in
  the Collector.
- Grafana has anonymous access off and its admin password comes from `.env` or a secret.

## Observability requirements

- The Collector exposes its own metrics on `:8888`, scraped as `otel-collector-self`.
- The Infrastructure dashboard shows Collector throughput and export failures.

## Acceptance criteria

- [x] All four demo services appear in `shop_server_requests_total`, with `service`, `route` and
  `status_class` labels.
- [x] The service graph shows `frontend→checkout`, `checkout→payments`, `checkout→inventory` and
  `frontend→inventory`.
- [x] Loki returns logs for `{service_name="checkout"}`, with `severity_text` and `trace_id`.
- [x] Tempo search returns traces for `{ resource.service.name = "frontend" }`.
- [x] Grafana provisions three dashboards and four data sources. All 11 alert rules are healthy.

## Testing scenarios

| Scenario | How |
|---|---|
| Pipeline smoke test | Validation steps in [test-strategy.md](../testing/test-strategy.md#validation-report) |
| Config validity | CI `deploy-config` job: `promtool check rules`, `amtool check-config`, and checks that the dashboards are regenerated from source |
| Backend outage | Stop `tempo` and confirm metrics and logs still flow and Sentinel reports the gap |
