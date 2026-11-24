# Sentinel platform runbook

## SentinelApiDown

**Impact:** alerts are not ingested, so incidents are not opened. Alertmanager retries the
webhook, so alerts will be ingested late once the API is back. The pager path that bypasses
Sentinel still works.

1. Kubernetes: `kubectl -n sentinel get pods -l app.kubernetes.io/name=sentinel-api`, then
   `kubectl describe` and `kubectl logs --previous`. Compose: `docker compose ps sentinel-api`
   and `docker compose logs sentinel-api`.
2. If readiness is failing with `database: error`, follow
   [Database outage](#database-outage-and-restore).
3. If the process is crash-looping on startup, check for a configuration validation error, which
   is printed as the first log line (for example, prod mode without secrets).
4. Roll back to the previous image tag if the failure started with a deploy.
5. Verify: `curl /health/ready` returns 200, and the Alertmanager queue drains.

## Investigations failing or backlog

1. Check `sentinel_queue_depth{queue="investigations"}` and the worker pod status.
2. Find failed investigations:

   ```bash
   curl /api/v1/incidents?status=active -H "X-API-Key: $KEY"
   ```

   Then check `latest_investigation.error` on each.
3. Workers are not running or are stuck: restart them. Jobs whose lease expired are reclaimed
   automatically; this is safe.
4. Backlog caused by load: scale the worker deployment.

   ```bash
   kubectl -n sentinel scale deploy/sentinel-worker --replicas=4
   ```

5. Telemetry timeouts make every job slow: check `sentinel_telemetry_queries_total{outcome="error"}`.
   As a temporary measure, lower `SENTINEL_TELEMETRY_TIMEOUT_S`.
6. To pause automation while you investigate, set `SENTINEL_AUTO_INVESTIGATE=false` and restart
   the API. Alerts are still ingested and correlated.

## TelemetryPipelineDown

1. Check the Collector's health endpoint (`:13133/`) and its logs, looking for exporter errors or
   memory-limiter refusals.
2. If the Collector is OOM-killed, raise its memory limit or lower its batch sizes.
3. While the pipeline is down, Sentinel investigations will report data gaps. Do not trust a
   "no anomalies" result during this window.

## Database outage and restore

1. A managed database failover usually takes less than 2 minutes. The API readiness probe fails
   and pods leave the load balancer; the connection pool reconnects on its own
   (`pool_pre_ping`).
2. Restore from PITR:
   1. Restore to a new instance at timestamp T.
   2. Update `SENTINEL_DATABASE_URL` in the secret manager.
   3. Restart the API and workers.
   4. Run `sentinel migrate`. It is a no-op if the schema already matches.
3. Compose restore from a logical dump:

   ```bash
   docker compose exec -T postgres psql -U sentinel sentinel < backup.sql
   ```

4. After any restore, investigations that were `running` at T are reclaimed when their lease
   expires. Remediations that were `executing` at T need a manual check: verify the target state,
   then mark the outcome with `/complete` or re-approve.

## Secret rotation

| Secret | Steps |
|---|---|
| API key | `sentinel gen-key NAME ROLE`. Append the new entry to `SENTINEL_API_KEYS`, deploy, switch clients over, then remove the old entry and deploy again. |
| Webhook token | Update the secret, then restart Alertmanager and the Sentinel API in the same change window. Alertmanager retries cover the gap between the two restarts. |
| Action token | Update the target services' admin token and `SENTINEL_ACTION_TOKEN` together. |
| Database password | Rotate it in the secret manager and restart the pods. |
| LLM API key | Update `SENTINEL_*_API_KEY` and restart the workers. On failure, Sentinel falls back to templates, so the rotation itself causes no outage. |

## Bad remediation executed

1. Find what ran, and who approved it:

   ```bash
   curl /api/v1/audit?incident_id=N -H "X-API-Key: $ADMIN_KEY"
   ```

2. Revert the change on the target. For `disable_fault_flag`, re-enable the flag through the
   service's normal flag system.
3. Stop further automated execution: remove the action from `actions.yaml` (or set it to
   `executor: manual`) and redeploy. Pending approvals for that action can then only be completed
   by hand.
4. Post-incident review: was the recommendation wrong (add a scenario test), or the approval
   (process issue)?
