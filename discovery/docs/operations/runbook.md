# Runbook

## Health and diagnostics

| Check | How |
|---|---|
| Liveness | `GET /healthz` → `{"status":"ok","version":…}` |
| Readiness | `GET /readyz` → 503 with a reason when the database is unreachable, migrations are pending, or the process is shutting down |
| Metrics | `GET /metrics` (Prometheus text) |
| Logs | stdout, JSON by default; each request logs `requestId`, tenant, subject, route, status, duration. Clients may send `X-Request-ID` (echoed back). Problem responses include `requestId`. |
| Job diagnostics | `GET /api/v1/discovery-jobs/{id}` (phase, pages, counts, error, attempt) and `/events` (bounded log) |
| Collection diagnostics | `GET /api/v1/metric-collections?connectionId=…` |
| Migration state | `obsplatform migrate status` |

## Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| Start-up prints `invalid configuration` | missing/invalid env | fix each listed variable |
| `/readyz` says migrations pending | new release without migration | run `obsplatform migrate up` (Helm hook does this) |
| `pg_ctl: could not start server`, log says "postmaster became multithreaded" (macOS) | no locale in env | scripts set `LC_ALL=C`; export it if starting manually |
| Job `failed`: `resolving credential` | reference missing | set `OBS_SECRET_<NAME>` or the file under `OBS_SECRET_DIR` |
| Job `failed`: `endpoint denied by egress policy` | host not allow-listed | add it to `OBS_EGRESS_ALLOWLIST` |
| Job `failed`: `403 … read-only ClusterRole` | RBAC in target cluster | apply the discovery ClusterRole (`discoveryRBAC.create=true` or copy the template) |
| Job `failed`: `continue token expired (410)` | list took too long | rerun; reduce `pageSize` or narrow `namespaces` |
| Job stuck `running` | worker died | lease expires after `OBS_JOB_LEASE_TIMEOUT`; job is requeued automatically |
| `409 job_active` | a job already runs | wait or cancel it (`extra.activeJobId`) |
| No metrics | collector missing for provider, or resource type has no metric capabilities | check `metricCapabilities` on the resource and `/metric-collections` |
| Kubernetes metrics fail with "metrics.k8s.io API is not available" | metrics-server not installed | install it or accept topology without measured metrics |
| UI shows "simulated" | fixture provider | expected: data is generated locally |

## Migrations and rollback

Forward-only. On PostgreSQL each migration runs in a transaction under an advisory lock; checksums detect edited history. On **Oracle**, DDL commits implicitly, so a migration is applied statement by statement and recorded in `schema_migrations` only when all statements succeed; replicas serialize on an exclusive lock of `schema_migration_lock`. If an Oracle migration fails part-way, `obsplatform migrate status` shows it pending and `migrate up` will fail on the first already-created object (`ORA-00955`): drop the objects that migration created (its file lists them), fix the cause, and run `migrate up` again. Destructive down-migrations are not provided because dropping topology or sample tables loses data. Recovery options, in order: (1) roll back the application (previous release must tolerate the newer schema); (2) forward-fix with a new migration; (3) restore from backup (below). Write migrations as expand/contract: add columns nullable or with defaults, backfill, switch code, drop in a later release.

## Backup and restore (Oracle store)

Use the organisation's standard Oracle tooling: RMAN backups with point-in-time recovery, or Data Pump for a logical copy of the schema (`expdp`/`impdp` with `SCHEMAS=<owner>`). The same priorities apply as for PostgreSQL below.

## Backup and restore (PostgreSQL)

- Managed services: enable automated backups and point-in-time recovery; RPO/RTO are a stakeholder decision (PDF Q6, **Unknown**).
- Self-managed: CloudNativePG scheduled backups to object storage, or `pg_dump`:
  ```bash
  pg_dump --format=custom --no-owner "$OBS_DATABASE_URL" > obs-$(date -u +%Y%m%dT%H%M%SZ).dump
  pg_restore --clean --if-exists --no-owner --dbname "$OBS_DATABASE_URL" obs-<timestamp>.dump
  ```
- What matters most: `connections` (configuration) and `audit_events`. Topology can be rebuilt by re-running discovery; simulated samples are regenerated; measured samples are lost beyond the backup point.

## Cleanup

| Goal | Command |
|---|---|
| Stop the demo | `make demo-stop` |
| Remove build outputs | `make clean` |
| Delete local database data | `make db-destroy` (removes `.local/pg`) |
| Remove compose volumes | `docker compose -f deploy/compose/docker-compose.yml down -v` |
