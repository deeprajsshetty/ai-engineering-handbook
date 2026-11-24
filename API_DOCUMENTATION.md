# API documentation

Sentinel API **v1**. The interactive OpenAPI spec is at `http://localhost:8000/docs` (Swagger) and
`/redoc`. The machine-readable spec is at `/openapi.json`. A contract test
(`test_openapi_contract`) guards the operation IDs listed here.

* **Base URL**: `http://localhost:8000` (Compose) or your ingress host.
* **Versioning**: all business endpoints live under `/api/v1`. Breaking changes go to `/api/v2`,
  and v1 stays supported for at least one release (see
  [DEVELOPMENT_GUIDELINES.md](DEVELOPMENT_GUIDELINES.md#api-changes)).
* **Content type**: `application/json`, except the report endpoint, which returns `text/markdown`.

## Authentication and authorisation

| Credential | Header | Used by |
|---|---|---|
| API key | `X-API-Key: <key>` or `Authorization: Bearer <key>` | People, scripts, MCP clients |
| Webhook token | `Authorization: Bearer <SENTINEL_WEBHOOK_TOKEN>` | Alertmanager only |

Roles are hierarchical: **viewer** < **operator** < **admin**.

| Capability | viewer | operator | admin |
|---|:-:|:-:|:-:|
| Read incidents, alerts, remediations, services, reports | ✓ | ✓ | ✓ |
| Ingest alerts, start investigations, resolve incidents | | ✓ | ✓ |
| Approve, reject or complete remediations | | ✓ | ✓ |
| Upsert services, read the audit log | | | ✓ |

To create a key, run `sentinel gen-key <name> <role>`. It prints the raw key (shown once) and the
`SENTINEL_API_KEYS` entry (`name:role:sha256`).

## Errors

Every error uses the same envelope:

```json
{"error": {"code": "http_404", "message": "incident 99 not found", "request_id": "3f2c..."}}
```

| Status | When |
|---|---|
| 401 | Missing or invalid credentials (`WWW-Authenticate: Bearer`) |
| 403 | Authenticated, but the role is too low |
| 404 | Unknown resource |
| 409 | Invalid state transition (already decided, already resolved, investigation already running) |
| 413 | Body larger than `SENTINEL_MAX_REQUEST_BYTES` (1 MB) |
| 422 | Validation error (`code: validation_error`, with `details`) |
| 429 | Rate limited; includes a `Retry-After` header (seconds) |
| 503 | `/health/ready` only: the database is unreachable |

Every response carries an `X-Request-ID` header. You can send your own value to correlate requests
with logs.

## Endpoints

### Health and metrics (no authentication; not rate limited)

| Method | Path | Description |
|---|---|---|
| GET | `/health/live` | Process is up. `{"status":"ok","version":"1.0.0"}` |
| GET | `/health/ready` | Database reachable, plus whether each backend is configured. Returns 503 if the database is down. |
| GET | `/metrics` | Prometheus exposition format |

### Alerts

#### `POST /api/v1/alerts/alertmanager` · `ingest_alertmanager`

Alertmanager webhook (payload v4). Authenticate with the webhook token, or with an operator key.

```bash
curl -X POST localhost:8000/api/v1/alerts/alertmanager \
  -H "Authorization: Bearer $SENTINEL_WEBHOOK_TOKEN" -H 'Content-Type: application/json' \
  -d '{"version":"4","status":"firing","alerts":[{"status":"firing",
       "labels":{"alertname":"HighErrorRate","service":"payments","severity":"critical"},
       "annotations":{"summary":"payments error rate 42%"},
       "startsAt":"2026-11-10T10:00:00Z","endsAt":"0001-01-01T00:00:00Z","fingerprint":"a1b2"}]}'
```

```json
{"received":1,"created_alerts":1,"updated_alerts":0,"incidents_opened":[12],
 "incidents_updated":[],"incidents_resolved":[],"investigations_queued":[31]}
```

#### `POST /api/v1/alerts` · `ingest_alert` (operator)

Ingest a generic alert from CI, synthetic checks or another tool. Returns `202` with the same body
as the webhook.

```json
{"alertname":"SyntheticCheckFailed","service":"checkout","severity":"warning",
 "status":"firing","summary":"login journey failing","labels":{"region":"eu-west-1"}}
```

`fingerprint` is optional. If you omit it, it is derived from `alertname`, `service` and the labels.

#### `GET /api/v1/alerts` · `list_alerts` (viewer)

Query parameters: `status` (`firing` or `resolved`), `service`, `limit` (1 to 200), `offset`.
Returns a page: `{"items":[Alert],"total":N,"limit":50,"offset":0}`.

### Incidents and investigations

| Method | Path | Operation | Role |
|---|---|---|---|
| GET | `/api/v1/incidents?status=active\|open\|investigating\|identified\|mitigating\|resolved&service=&limit=&offset=` | `list_incidents` | viewer |
| GET | `/api/v1/incidents/{id}` | `get_incident` | viewer |
| GET | `/api/v1/incidents/{id}/report` | `get_incident_report` (markdown) | viewer |
| POST | `/api/v1/incidents/{id}/investigations` | `start_investigation` (202; 409 if one is queued or running) | operator |
| POST | `/api/v1/incidents/{id}/resolve` `{"note": "..."}` | `resolve_incident` (expires pending proposals) | operator |
| GET | `/api/v1/incidents/{id}/remediations` | `list_incident_remediations` | viewer |
| GET | `/api/v1/investigations/{id}` | `get_investigation` | viewer |
| GET | `/api/v1/stats` | `get_stats`: active and resolved incidents, firing alerts, pending approvals | viewer |

Example response for `GET /api/v1/incidents/1` (abridged):

```json
{
  "id": 1, "title": "HighErrorRate on frontend", "status": "identified", "severity": "critical",
  "services": ["checkout", "frontend", "payments"],
  "root_cause_service": "payments", "root_cause_confidence": 0.68,
  "summary": "CRITICAL incident affecting checkout, frontend, payments ... Root cause: payments ...",
  "alerts": [{"alertname": "HighErrorRate", "service": "payments", "status": "firing", "...": "..."}],
  "latest_investigation": {"id": 1, "status": "completed", "duration_ms": 412,
    "result": {"hypotheses": [{"service": "payments", "cause": "error_rate", "confidence": 0.68,
               "components": {"anomaly": 0.74, "topology": 1.0, "traces": 1.0, "earliness": 1.0, "logs": 0.33},
               "evidence": ["12 error span(s) originate here across 12 sampled traces", "..."]}],
               "data_gaps": [], "steps": ["..."], "report": "# Incident #1 ..."}},
  "remediations": [{"id": 1, "action": "disable_fault_flag", "service": "payments", "risk": "low",
                    "executor": "http", "status": "pending_approval"}],
  "timeline": [{"ts": "...", "actor": "alertmanager", "action": "incident.opened", "details": {}}]
}
```

The full `result` schema is documented in [docs/architecture/data-model.md](docs/architecture/data-model.md).

### Remediations (human in the loop)

| Method | Path | Operation | Role | Transition |
|---|---|---|---|---|
| GET | `/api/v1/remediations?status=pending_approval&limit=` | `list_remediations` | viewer | n/a |
| POST | `/api/v1/remediations/{id}/approve` `{"comment": "..."}` | `approve_remediation` | operator | `pending_approval` → `approved` |
| POST | `/api/v1/remediations/{id}/reject` `{"comment": "..."}` | `reject_remediation` | operator | `pending_approval` → `rejected` |
| POST | `/api/v1/remediations/{id}/complete` `{"success": true, "notes": "..."}` | `complete_remediation` | operator | `approved` (manual) → `succeeded`/`failed` |

Approved `http` remediations are executed by the worker, usually within 5 s. Poll
`GET /api/v1/incidents/{id}/remediations` for `succeeded` or `failed`; the `result` field holds the
target's HTTP status and response.

### Services and topology

| Method | Path | Operation | Role |
|---|---|---|---|
| GET | `/api/v1/services` | `list_services` | viewer |
| PUT | `/api/v1/services/{name}` | `upsert_service` | admin |
| GET | `/api/v1/services/dependency-map` | `get_dependency_map` | viewer |
| GET | `/api/v1/services/{name}/health` | `get_service_health` | viewer |
| GET | `/api/v1/audit?incident_id=&since=&limit=` | `list_audit_events` | admin |
| GET | `/api/v1/whoami` | `whoami` | viewer |

`PUT /api/v1/services/search`:

```json
{"team": "discovery", "tier": "2", "description": "Product search",
 "dependencies": ["inventory"], "runbook_url": "https://wiki/search"}
```

`GET /api/v1/services/dependency-map`:

```json
{"nodes": ["checkout", "frontend", "inventory", "payments"],
 "edges": [{"caller": "checkout", "callee": "payments", "source": "both"}]}
```

`GET /api/v1/services/payments/health`:

```json
{"service": "payments", "status": "degraded", "active_alerts": 1,
 "signals": [{"kind": "request_rate", "value": 4.1}, {"kind": "error_rate", "value": 0.42},
             {"kind": "latency_p95", "value": 0.03}]}
```

## MCP (Model Context Protocol)

* **Endpoint**: `POST /mcp` (Streamable HTTP transport).
* **Auth**: the same API keys; viewer role or higher. Send `Authorization: Bearer <key>` or
  `X-API-Key`. The header is forwarded to each tool call, so normal role-based access control
  applies.
* **Tools** (read-only; each operation ID is the tool name):
  - `list_incidents`
  - `get_incident`
  - `get_incident_report`
  - `list_remediations`
  - `get_dependency_map`
  - `get_service_health`
  - `list_services`
  - `list_alerts`
* **Not exposed, by design**: ingestion, approve, reject, complete, resolve, upsert and audit.
  External AI agents cannot change production or incident state.

Example client configuration (Claude Desktop, Cursor or any MCP host that supports HTTP servers):

```json
{"mcpServers": {"sentinel": {"url": "http://localhost:8000/mcp",
  "headers": {"Authorization": "Bearer <viewer-key>"}}}}
```

## Integration notes

* **Alertmanager**: see `deploy/compose/alertmanager/alertmanager.yml`. Use `send_resolved: true`,
  so that incidents auto-resolve.
* **Labels Sentinel reads**: `alertname`, `service` (falling back to `service_name`, then `job`),
  and `severity` (`critical`, `warning` or `info`; `page`/`p1` map to critical).
* **Notifications** (outbound): `POST <SENTINEL_NOTIFICATION_WEBHOOK_URL>` with
  `{"text": "..."}`, which is compatible with Slack and Mattermost incoming webhooks.
* **Remediation targets** (outbound): `POST` to the URL template in `actions.yaml`, with
  `X-Admin-Token: <SENTINEL_ACTION_TOKEN>`. Any 2xx response counts as success.
