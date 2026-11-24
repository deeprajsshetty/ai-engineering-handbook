# Configuration reference

All configuration is environment variables, validated at start-up (`internal/config`). Invalid values stop the process with a list of every problem. Durations use Go syntax (`30s`, `2h`).

| Variable | Default | Meaning |
|---|---|---|
| `OBS_STORE_DRIVER` | `postgres` | platform store: `postgres` or `oracle` ([ADR 0011](../adr/0011-platform-store-portability.md)) |
| `OBS_DATABASE_URL` | **required** | `postgres://user:pass@host:5432/db?sslmode=…`, or with `OBS_STORE_DRIVER=oracle` `oracle://user:pass@host:1521/service` (go-ora options such as `?SSL=true` may be appended). Password never logged. |
| `OBS_ENV` | `development` | `development` or `production` (production forbids dev auth and insecure endpoints) |
| `OBS_ROLES` | `api,worker` | which loops this process runs |
| `OBS_HTTP_ADDR` | `:8080` | listen address |
| `OBS_WEB_DIR` | empty | directory of the built UI to serve (image sets `/web`) |
| `OBS_AUTH_MODE` | `dev` | `dev` or `oidc` (OIDC not implemented: fails fast) |
| `OBS_DEV_TOKENS` | built-in demo tokens | `token=tenant:subject:role;…`, tokens ≥ 8 chars, roles viewer/operator/admin |
| `OBS_OIDC_ISSUER`, `OBS_OIDC_AUDIENCE` | empty | required when `OBS_AUTH_MODE=oidc` |
| `OBS_LOG_LEVEL` / `OBS_LOG_FORMAT` | `info` / `json` | `debug|info|warn|error` / `json|text` |
| `OBS_DB_MAX_CONNS` | `10` | pool size per process |
| `OBS_RATE_LIMIT_RPS` / `OBS_RATE_LIMIT_BURST` | `20` / `40` | per tenant+subject |
| `OBS_MAX_BODY_BYTES` | `1048576` | request body cap |
| `OBS_SHUTDOWN_TIMEOUT` | `20s` | graceful drain |
| `OBS_DISCOVERY_WORKERS` | `2` | concurrent jobs per worker process |
| `OBS_DISCOVERY_JOB_TIMEOUT` | `10m` | per job |
| `OBS_DISCOVERY_PAGE_TIMEOUT` | `30s` | per provider request |
| `OBS_DISCOVERY_MAX_RESOURCES` | `50000` | snapshot cap |
| `OBS_DISCOVERY_MAX_ATTEMPTS` | `3` | lease-expiry retries |
| `OBS_DISCOVERY_POLL_INTERVAL` | `1s` | queue polling |
| `OBS_JOB_LEASE_TIMEOUT` | `30s` | heartbeat lease |
| `OBS_MAX_JOB_EVENTS` | `200` | per job |
| `OBS_METRICS_INTERVAL` | `60s` | collection period and simulated sample spacing (≥ 5 s) |
| `OBS_METRICS_BACKFILL` | `1h` | simulated history generated for new resources |
| `OBS_METRICS_CONCURRENCY` | `4` | concurrent collection runs |
| `OBS_METRIC_RETENTION` | `168h` | sample retention |
| `OBS_TOPOLOGY_PURGE_AFTER` | `168h` | purge missing resources after |
| `OBS_JOB_RETENTION` | `720h` | jobs, change log, collection runs |
| `OBS_AUDIT_RETENTION` | `2160h` | audit events |
| `OBS_RETENTION_INTERVAL` | `1h` | retention pass period |
| `OBS_FRESHNESS_WINDOW` / `OBS_STALE_AFTER` | `1h` / `24h` | freshness classification |
| `OBS_EGRESS_ALLOWLIST` | empty (deny all) | provider hosts: `host`, `*.suffix`, or literal IP |
| `OBS_ALLOW_INSECURE_ENDPOINTS` | `false` | allow `http://` and loopback targets (development only) |
| `OBS_SECRET_DIR` | `/var/run/secrets/obs` | root for `file:` credential references |
| `OBS_SEED_DATABASE_TARGET` | unset | `seed` only: `host:port/database` of a PostgreSQL server to add as the measured "Local PostgreSQL (measured)" connection (set by `scripts/demo.sh`) |
| `OBS_SEED_DATABASE_USER` | `obs_monitor` | `seed` only: monitoring user for that connection |
| `OBS_SEED_DATABASE_CREDENTIAL_REF` | `file:demo-db-password` | `seed` only: credential reference for that connection |
| `OBS_SECRET_<NAME>` | — | values for `env:OBS_SECRET_<NAME>` references |

Test-only: `OBS_TEST_DATABASE_URL` (use an existing PostgreSQL), `OBS_TEST_REQUIRE_DB=1` (fail instead of skip when no database). Scripts: `OBS_DEV_PG_PORT` (default 54329), `OBS_API_URL` (Vite proxy target).

## Secrets for local development

```bash
export OBS_SECRET_MY_CLUSTER_TOKEN="$(cat ~/path/to/token)"   # never commit it
# connection: "credentialRef": "env:OBS_SECRET_MY_CLUSTER_TOKEN"
```
or put a file in a git-ignored directory and set `OBS_SECRET_DIR` to it, then use `file:<name>`.

## Database connections

A `database` connection's host must be in `OBS_EGRESS_ALLOWLIST` (host name, `*.suffix`, or literal IP; private IPs are allowed only when listed literally). The password is a credential reference. Minimum grants:

| Engine | Grant |
|---|---|
| PostgreSQL | `CREATE ROLE obs_monitor LOGIN PASSWORD '…' IN ROLE pg_monitor;` |
| Oracle (CDB) | common user, e.g. `CREATE USER c##obs_monitor IDENTIFIED BY "…" CONTAINER=ALL; GRANT CREATE SESSION, SELECT_CATALOG_ROLE TO c##obs_monitor CONTAINER=ALL; ALTER USER c##obs_monitor SET CONTAINER_DATA=ALL CONTAINER=CURRENT;` and connect to the root service (without `CONTAINER_DATA=ALL` only the root container is visible) |
| Oracle (non-CDB) | `GRANT CREATE SESSION, SELECT_CATALOG_ROLE TO obs_monitor;` |

`tls`: `verify-full` (default) needs the server certificate to chain to the system trust store; `require` encrypts without verification; `disable` needs `OBS_ALLOW_INSECURE_ENDPOINTS=true`.

## Oracle as the platform store

Schema owner grants (run as a DBA; the platform creates its own tables on `migrate up`):

```sql
CREATE USER obs IDENTIFIED BY "<password>" QUOTA UNLIMITED ON users;
GRANT CREATE SESSION, CREATE TABLE, CREATE SEQUENCE TO obs;
```

Every platform session sets `TIME_ZONE = 'UTC'`; timestamps are stored as UTC `TIMESTAMP(6)`. Local development: `make oracle-up` starts Oracle Database Free in Docker and `scripts/dev-oracle.sh url` prints the connection URL (passwords are generated into `.local/oracle.env`).
