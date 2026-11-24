# Deployment

Artifacts (identical in every environment): the OCI image built from `deploy/docker/Dockerfile` and the Helm chart `deploy/helm/obs-platform`. Diagrams: [HLD §4](../design/hld.md#4-deployment).

## Local

| Option | Commands | Needs |
|---|---|---|
| Binaries + local PostgreSQL | `make demo` (or `make db-up migrate seed run`) | Go 1.27, Node 24, PostgreSQL 16+ binaries |
| Containers | `docker compose -f deploy/compose/docker-compose.yml up --build` | Docker with registry access |
| Binaries + Oracle as the platform store | `make demo-oracle` (Oracle Database Free 23ai in Docker on 127.0.0.1:15210) | Docker; ~6 GB image |

## Kubernetes (public or private)

1. **Image.** Build once in CI, scan, push to your registry, record the digest. Promote the same digest; never rebuild per environment.
2. **Database.** Either **PostgreSQL** or **Oracle** ([ADR 0011](../adr/0011-platform-store-portability.md)).
   *Oracle:* Oracle Database 19c or later (tested with Oracle Database Free 23ai only). Create a schema owner with `CREATE SESSION`, `CREATE TABLE`, `CREATE SEQUENCE` and a tablespace quota; add `-f deploy/helm/obs-platform/values-oracle.yaml` (sets `database.driver: oracle` and the NetworkPolicy port 1521); put `oracle://user:password@host:1521/service` in the Secret. Use Oracle Native Network Encryption or TCPS between the platform and the database.
   *PostgreSQL:* 16+ (tested with 18.3): managed service (public cloud examples: RDS/Aurora, Azure Flexible Server, Cloud SQL) or CloudNativePG / a DBA-run cluster (private). Create a database and a role owning it. Use TLS (`sslmode=verify-full`).
3. **Secrets.** Create `obsplatform-db` with key `DATABASE_URL` (via External Secrets Operator from your secret store, or `kubectl create secret generic obsplatform-db --from-literal=DATABASE_URL=...` for evaluation). Optional provider credentials: a Secret mounted at `OBS_SECRET_DIR`, referenced by connections as `file:<key>`.
4. **Install.**
   ```bash
   helm upgrade --install obs deploy/helm/obs-platform -n obs --create-namespace \
     -f deploy/helm/obs-platform/values-public-cloud.yaml \
     --set image.repository=<registry>/obsplatform --set image.digest=sha256:<digest>
   ```
   The pre-upgrade hook Job runs `obsplatform migrate up` before pods roll.
5. **Edge.** Gateway API `HTTPRoute` (preferred) or Ingress (fallback); TLS at the gateway.
6. **Verify.** `kubectl -n obs rollout status deploy/obs-obs-platform-api`; port-forward and `make smoke URL=http://localhost:8080` (in an evaluation cluster with dev auth).

**Important:** the chart defaults to `OBS_ENV=production` and `OBS_AUTH_MODE=oidc`. OIDC is not implemented in the MVP, so API pods fail fast with an explicit message. For a throwaway evaluation cluster only, add `-f values-dev.yaml`.

## What differs between public and private cloud

Only values and cluster add-ons: registry, edge controller class, database endpoint and TLS CA, secret back end, egress allow-list and NetworkPolicy CIDRs, monitoring stack. See the table in [HLD §4](../design/hld.md#4-deployment). Not portable by nature: backup/failover of the database, certificate issuance, DNS, load balancer provisioning — these are environment operations.

## Rollout, rollback, and failure handling

- Rolling update with `maxUnavailable: 0`; readiness gates traffic on database reachability and no pending migrations.
- Migrations are forward-only and must be backward compatible with the previous release (expand → deploy → contract one release later).
- Rollback triggers: readiness failing after rollout, 5xx rate on `obs_http_requests_total{code=~"5.."}` above baseline, discovery jobs failing (`obs_discovery_jobs_total{status="failed"}`) for previously healthy connections.
- Roll back with `helm rollback`; the schema stays (forward-only), which is safe because the previous release must tolerate it.
- Canary: deploy a second release with one API replica behind a weighted `HTTPRoute` (Gateway API `backendRefs` weights) — **Recommendation**, not templated in the MVP chart.
