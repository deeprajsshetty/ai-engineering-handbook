# UC-06: Incident reporting, notification and AI-agent access (MCP)

| Field | |
|---|---|
| **ID / Name** | UC-06 · Reports, timeline, notifications, read-only MCP tools |
| **Priority** | 6 |

## Business view

**Problem.**

- Post-incident write-ups take hours.
- Stakeholders learn about incidents late.
- Other internal AI assistants (IDE copilots, chat bots) cannot see incident context.

**Objectives.**

- Generate the incident report and timeline automatically.
- Notify chat channels when an incident opens and when its root cause is found.
- Let approved AI agents *read* incident data, but never change it.

**Stakeholders and actors.**

| Actor | Role in this use case |
|---|---|
| Incident commander | Uses the report |
| Business stakeholders | Read the executive summary |
| Chat channel | Receives notifications |
| External MCP clients | Read incident data |

**Expected outcomes.**

- Faster, more consistent post-incident reviews.
- Earlier stakeholder awareness.
- AI assistants that can answer "what is broken right now?"

## Functional requirements

1. `GET /api/v1/incidents/{id}/report` returns the latest completed investigation's report as
   markdown.
2. `GET /api/v1/incidents/{id}` includes a timeline built from audit events. Each event has a
   timestamp, actor, action and details.
3. When `SENTINEL_NOTIFICATION_WEBHOOK_URL` is set, Sentinel posts Slack-compatible messages when
   an incident opens and when its root cause is identified.
4. `/mcp` (Streamable HTTP) exposes these read-only tools:
   - `list_incidents`
   - `get_incident`
   - `get_incident_report`
   - `list_remediations`
   - `get_dependency_map`
   - `get_service_health`
   - `list_services`
   - `list_alerts`
5. MCP requires a valid API key with viewer role or higher. The caller's key is forwarded to each
   tool call, so normal role-based access control applies.

## Non-functional requirements

- Notification failures never block incident handling (3 retries, then the failure is logged).
- The report is reproducible from the stored investigation result.

## Prerequisites and dependencies

- A completed investigation (UC-03).
- For MCP: an MCP-capable client and an API key.

## Inputs and outputs

| | |
|---|---|
| **Inputs** | The investigation result, audit events, and MCP JSON-RPC requests |
| **Outputs** | A markdown report, the timeline JSON, chat messages, and MCP tool results |

## End-to-end workflow

1. The Reporting agent assembles the report: summary, alerts, anomalies, propagation, RCA table
   with evidence, narrative, recommended actions, data gaps and agent execution.
2. The investigation service stores the report and notifies the chat channel.
3. An MCP client connects to `/mcp` with `Authorization: Bearer <key>` and calls, for example,
   `get_incident`. `fastapi-mcp` turns that call into an internal API request, forwarding the
   caller's key.

## AI agent responsibilities

The Incident Summary and Reporting agent writes the report and summary. External agents can only
read data through MCP.

## Exception and failure handling

| Case | Response |
|---|---|
| No completed investigation | 404 "no completed investigation yet" |
| MCP request without a key | 401 |
| Notification webhook returns 5xx | Retried up to 3 times, then counted as `outcome=error` |

## Security considerations

- Approve, reject, resolve, ingest and upsert endpoints are **excluded** from the MCP tool list.
- Reports contain no secrets. Log excerpts are normalised: identifiers and numbers are collapsed.

## Observability requirements

- `sentinel_notifications_total{outcome}`.
- HTTP metrics for the `/mcp` route.

## Acceptance criteria

- [x] The report endpoint returns markdown that starts with `# Incident`. The live example is in
  [example-incident-report.md](example-incident-report.md).
- [x] The timeline includes `incident.opened` through `incident.auto_resolved`.
- [x] The notifier posts `{"text": ...}` and retries after a 500.
- [x] MCP `initialize` without a key returns 401. With a viewer key it returns 200.

## Testing scenarios

- `tests/unit/test_ops.py` (notifier).
- `tests/integration/test_api_platform.py::test_mcp_endpoint_requires_auth`.
- Lifecycle test timeline assertions.
