# Deployment guide

| Environment | Tooling | Validated in this repo |
|---|---|---|
| Local development (API only) | `uv`, SQLite | Yes |
| Full stack (demo, staging-like) | Docker Compose | Yes: 14 containers, e2e and load tests pass |
| Kubernetes (dev / prod) | Kustomize overlays | Manifests render with `kubectl kustomize`; not applied to a cluster here |
| CI/CD | GitHub Actions | Workflows written; each step mirrors a command that passes locally |

## 1. Local development (no Docker)

```bash
cd sentinel
uv sync --extra dev
```

```bash
uv run sentinel migrate
```

`sentinel migrate` creates `./sentinel.db` (SQLite).

```bash
uv run sentinel gen-key dev admin
```

`gen-key` prints a key and a `SENTINEL_API_KEYS` entry. Export the entry, then start the API:

```bash
export SENTINEL_API_KEYS='dev:admin:<sha256 from gen-key>'
uv run uvicorn --factory sentinel.api.app:app_factory --reload
```

In a second terminal, start the worker:

```bash
uv run sentinel worker
```

Without `SENTINEL_PROMETHEUS_URL`, `SENTINEL_LOKI_URL` and `SENTINEL_TEMPO_URL` set,
investigations run but report data gaps. Point those variables at the Compose stack's ports
(9090, 3100, 3200) to use real telemetry.

## 2. Full stack with Docker Compose

Requirements: Docker 24+ with Compose v2, about 4 GB of free RAM, and these ports free: 3000,
3100, 3200, 4317, 4318, 8000, 8080-8083, 9090 and 9093.

```bash
make up
```

`make up` runs `scripts/bootstrap-env.sh` followed by `docker compose up -d --build`.

`bootstrap-env.sh` copies `deploy/compose/.env.example` to `.env` and replaces every `CHANGE_ME`
with a random secret. It also generates admin, operator and viewer API keys. The raw keys go to
`deploy/compose/.env.keys` (mode 600, git-ignored); only their SHA-256 hashes go into `.env`.
Re-running the script does nothing; pass `--force` to regenerate everything.

Startup order is enforced with health conditions: `postgres` (healthy), then `sentinel-migrate`
(completes), then `sentinel-api` and `sentinel-worker`.

| Container | Purpose | Port |
|---|---|---|
| postgres | Sentinel database | internal |
| sentinel-migrate | `alembic upgrade head`, then exits | n/a |
| sentinel-api | API, console, MCP | 8000 |
| sentinel-worker | Agents and executor | metrics on 9100 (internal) |
| otel-collector | OTLP ingestion | 4317, 4318 |
| prometheus, alertmanager | Metrics, rules, alerting | 9090, 9093 |
| loki, tempo | Logs, traces | 3100, 3200 |
| grafana | Dashboards | 3000 |
| frontend, checkout, payments, inventory | Demo workload | 8080-8083 |
| loadgen | Background traffic (8 rps) | n/a |

**Hardening already applied in Compose:**

- read-only root filesystems with `tmpfs` for `/tmp`;
- `no-new-privileges`, and all Linux capabilities dropped;
- non-root users in the images;
- the webhook token is delivered to Alertmanager as a Compose secret.

**Common operations**

```bash
docker compose -f deploy/compose/docker-compose.yml ps
make logs
make demo-errors
make status
make down
make clean
```

`make logs` tails the API and worker logs, `make demo-errors` injects a fault, `make status` lists
active incidents, `make down` stops the stack and keeps its data, and `make clean` stops it and
deletes the volumes.

**Switching on an LLM.** Set `SENTINEL_LLM_PROVIDER=openai` (or `anthropic`) and the matching
`SENTINEL_*_API_KEY` in `deploy/compose/.env`, then recreate the platform containers:

```bash
docker compose -f deploy/compose/docker-compose.yml up -d sentinel-api sentinel-worker
```

## 3. Environment configuration

The configuration reference is in [PROJECT_KNOWLEDGE.md §6](PROJECT_KNOWLEDGE.md#6-configuration-reference).

**Production rules.** These are enforced when `SENTINEL_ENV=prod`:

- API keys must be configured;
- the webhook token must be at least 24 characters;
- PostgreSQL is required (SQLite is rejected).

## 4. Kubernetes

Layout:

```
deploy/k8s/
  base/            namespace (PSA restricted), SA (no token), ConfigMap, migrate Job, API + worker
                   Deployments/Service, HPA, PDBs, default-deny NetworkPolicies
  overlays/dev/    1 replica, DEBUG, Secret generated from git-ignored secrets.env
  overlays/prod/   GHCR image, ExternalSecret (External Secrets Operator), Ingress + TLS
```

### 4.1 Prerequisites (customer-provided)

| Dependency | Notes |
|---|---|
| Kubernetes 1.27+ | With a CNI that enforces NetworkPolicy (Calico, Cilium) |
| PostgreSQL 14+ | Managed (RDS, Cloud SQL, Azure Flexible Server); TLS; PITR enabled |
| Observability backends in namespace `monitoring` | `kube-prometheus-stack`, `grafana/loki`, `grafana/tempo`, and the `opentelemetry-collector` Helm charts. Reuse `deploy/compose/*` configs as the basis for chart values. |
| External Secrets Operator + `ClusterSecretStore` named `cloud-secret-manager` | Supplies `sentinel-secrets` |
| ingress-nginx + cert-manager | For the prod Ingress |
| Container registry | `ghcr.io/<owner>/sentinel` (published by the release workflow) |

### 4.2 Deploy

Dev overlay (kind or minikube):

```bash
cp deploy/k8s/overlays/dev/secrets.env.example deploy/k8s/overlays/dev/secrets.env
```

Edit `secrets.env`: the database URL, the `sentinel gen-key` output and the tokens. Then apply:

```bash
kubectl apply -k deploy/k8s/overlays/dev
```

Prod overlay: put the secret keys into the cloud secret manager at `sentinel/prod`. The keys are
`SENTINEL_DATABASE_URL`, `SENTINEL_API_KEYS`, `SENTINEL_WEBHOOK_TOKEN` and `SENTINEL_ACTION_TOKEN`
(plus the LLM keys if you use an LLM). Then apply and wait for the rollout:

```bash
kubectl apply -k deploy/k8s/overlays/prod
```

```bash
kubectl -n sentinel rollout status deploy/sentinel-api
```

The `sentinel-migrate` Job is annotated as an Argo CD `PreSync` hook. Without Argo CD, run it
before each release:

```bash
kubectl -n sentinel create job --from=job/sentinel-migrate migrate-$(date +%s)
```

### 4.3 Point Alertmanager at Sentinel

In `kube-prometheus-stack` values (Alertmanager config):

```yaml
receivers:
  - name: sentinel
    webhook_configs:
      - url: http://sentinel-api.sentinel.svc/api/v1/alerts/alertmanager
        send_resolved: true
        http_config:
          authorization: {type: Bearer, credentials_file: /etc/alertmanager/secrets/sentinel-webhook/token}
```

Mount the token with `alertmanager.alertmanagerSpec.secrets: [sentinel-webhook]`.

### 4.4 Scaling and availability

| Component | Strategy |
|---|---|
| API | Stateless. HPA from 2 to 6 replicas at 70% CPU, spread across nodes, PDB `minAvailable: 1`, rolling update with `maxUnavailable: 0` |
| Worker | Scale by replicas; claims use `SKIP LOCKED`. 120 s grace period so in-flight investigations finish |
| Database | Managed, Multi-AZ. Connection pool: 5 + 10 overflow per process |

## 5. CI/CD

`.github/workflows/ci.yml` runs on every push and pull request:

| Job | Gates |
|---|---|
| `quality` | ruff lint, ruff format check, mypy, `uv lock --check` |
| `test` | pytest on SQLite **and** PostgreSQL 17 (service container), coverage ≥ 85%, `sentinel migrate` + `alembic check` on PostgreSQL |
| `security` | bandit, pip-audit (strict), gitleaks |
| `images` | Build the sentinel and shop images (with cache), Trivy scan that fails on fixable HIGH/CRITICAL |
| `deploy-config` | `docker compose config`, Kustomize render + kubeconform, `promtool check rules`, `amtool check-config`, checks that dashboards are regenerated from source |
| `e2e` | On `master`, or on PRs labelled `run-e2e`: full stack, fault injection, approval, recovery |

`.github/workflows/release.yml` runs on a `v*.*.*` tag. It builds multi-arch images (amd64 and
arm64) with an SBOM and max-level provenance, pushes them to GHCR, and signs them with cosign
(keyless). CD is GitOps: bump the image tag (or digest) in `overlays/prod`, and Argo CD runs the
PreSync migration, then the rollout.

**Rollback.** Revert the image tag. Migrations are written to be backward compatible for one
release (expand, then contract). If a migration must be undone, run `alembic downgrade -1` from the
previous image.

## 6. Monitoring the deployment

Monitoring is already wired up:

- Prometheus scrapes `sentinel-api:8000/metrics` and `sentinel-worker:9100/metrics`;
- the `platform` rule group alerts on `SentinelApiDown`, `SentinelInvestigationsFailing`,
  `SentinelQueueBacklog`, `TelemetryPipelineDown` and `HostMemoryHigh`;
- the *Sentinel - AIOps Platform* dashboard shows the platform's health.

Route platform alerts to a human receiver as well as to Sentinel. If Sentinel is down, it cannot
page anyone about itself.

## 7. Health checks

| Probe | Endpoint | Meaning |
|---|---|---|
| Startup / liveness | `GET /health/live` | Process up |
| Readiness | `GET /health/ready` | Database reachable (503 otherwise). Telemetry backends are reported but do not fail readiness, because Sentinel degrades without them. |
| Worker liveness | `GET :9100/metrics` | Worker loop's metrics server is up |

## 8. Backup and disaster recovery

| Item | Target / practice |
|---|---|
| RPO / RTO | RPO ≤ 5 min (PITR), RTO ≤ 30 min |
| PostgreSQL | Managed PITR (7-35 days) plus a daily `pg_dump` to a separate account. Compose: `docker compose exec postgres pg_dump -U sentinel sentinel > backup.sql` |
| Restore drill | Quarterly: restore to a new instance, point a staging Sentinel at it, run `sentinel migrate` (no-op) and the integration suite |
| Telemetry stores | Rebuildable and not business-critical. Loki and Tempo blocks in object storage with lifecycle rules; Prometheus is short-term (Thanos or Mimir for long retention) |
| Configuration | In git: catalogs, rules, dashboards, manifests |
| Secrets | In the cloud secret manager, with versioning enabled |
| Region loss | Redeploy the manifests in the secondary region against the cross-region read replica, promoted to primary |

## 9. Environment-specific dependencies

Not provided by this repository; each environment must supply:

1. A Kubernetes cluster with a NetworkPolicy-enforcing CNI, ingress-nginx and cert-manager.
2. Managed PostgreSQL with TLS. Use `?sslmode=require` in `SENTINEL_DATABASE_URL`.
3. A secret manager plus External Secrets Operator (or replace `external-secret.yaml` with your
   mechanism).
4. Observability backends in `monitoring`, installed with Helm. Adjust the URLs in
   `base/configmap.yaml` if your service names differ.
5. Registry access to GHCR, or a mirror.
6. Optional: an approved LLM provider account and egress on port 443 (already allowed by the
   egress NetworkPolicy).
7. Optional: a Slack or Mattermost incoming webhook.
8. The `ipBlock` CIDR in `networkpolicy.yaml` (PostgreSQL egress) set to your database subnet.
