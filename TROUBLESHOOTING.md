# Troubleshooting

Start with the quick checks below, then find your symptom in the tables. For the platform's
operational runbooks (worker stuck, database down, restoring from backup), see
[docs/runbooks](docs/runbooks/README.md).

## Quick checks

```bash
docker compose -f deploy/compose/docker-compose.yml ps
```

`ps` shows container status. Everything should be `running`, and `sentinel-migrate` should have
exited with code 0.

```bash
curl -s localhost:8000/health/ready | python3 -m json.tool
```

`/health/ready` reports the database and which telemetry backends are configured.

```bash
docker compose -f deploy/compose/docker-compose.yml logs --tail 100 sentinel-api sentinel-worker
```

Look for platform errors in these logs. They are JSON; filter on `"level": "ERROR"`.

Check the Prometheus targets page at http://localhost:9090/targets. Every job should be UP.

## Startup and deployment

| Symptom | Cause | Fix |
|---|---|---|
| `sentinel-migrate` exits non-zero | Database not reachable or wrong credentials | `docker compose logs sentinel-migrate`. Check that `POSTGRES_*` in `.env` matches. If you changed the password after the first start, the volume keeps the old one: run `make clean` and start again |
| `bind: address already in use` | Port taken (3000, 8000, 9090 and others) | Stop the other process, or change the host-side port in `docker-compose.yml` |
| API exits with `ValidationError ... SENTINEL_API_KEYS must be set` | `SENTINEL_ENV=prod` without the required secrets | Set the keys and token, or use `SENTINEL_ENV=dev` locally |
| `SENTINEL_API_KEYS entries must look like name:role:sha256hex` | Malformed key list | Regenerate with `sentinel gen-key NAME ROLE`. Separate entries with commas, no spaces |
| Console says "auth failed" | Using the hash instead of the raw key | Use the raw key from `deploy/compose/.env.keys`, not the value in `.env` |
| Loki logs `empty ring` at startup | Normal while Loki initialises | Ignore it unless it persists past 1 minute |
| Grafana dashboards are empty right after startup | No data yet | Wait about 60 s (10 s metric export plus 15 s scrape plus 1 m rate windows) |

## No incident appears after injecting a fault

Work through this list in order:

1. **Is traffic flowing?** In Prometheus, query `shop:request_rate:1m`. It should be about 8 rps at
   the frontend. If not, check `docker compose logs loadgen`.
2. **Did the alert fire?** Open http://localhost:9090/alerts. `HighErrorRate` goes pending, then
   firing after 30 s.
3. **Did Alertmanager deliver it?** Open http://localhost:9093. Then check
   `docker compose logs alertmanager | grep -i notify`. A 401 here means the webhook token does not
   match. Recreate both containers so they read the same `.env`:

   ```bash
   docker compose up -d alertmanager sentinel-api
   ```

4. **Did Sentinel receive it?**

   ```bash
   curl localhost:8000/api/v1/alerts -H "X-API-Key: $KEY"
   ```

5. **Was the investigation run?** Check `latest_investigation.status` on the incident. If it stays
   `queued`, the worker is not running: `docker compose logs sentinel-worker`.

## Wrong or low-confidence root cause

| Symptom | Cause | Fix |
|---|---|---|
| Anomalies show `method: static_threshold`, "insufficient baseline" | Fewer than 6 baseline points (stack just started, or a new service) | Expected. Wait 5 to 10 minutes after startup before demos. Static thresholds still work, with less precision |
| `data_gaps` lists `traces unavailable` or `logs unavailable` | Backend down or URL unset | Check the `SENTINEL_*_URL` variables and `docker compose ps tempo loki` |
| `signals_with_data: 0` | Metric names don't match `signals.yaml` | Compare against `curl localhost:9090/api/v1/label/__name__/values`. See [PROJECT_KNOWLEDGE.md §5](PROJECT_KNOWLEDGE.md#5-implementation-facts-that-are-easy-to-get-wrong) |
| Root cause is an upstream service | Missing dependency edge, so the topology component misleads | Check `GET /api/v1/services/dependency-map`. Add the edge with `PUT /api/v1/services/{name}` |
| Two unrelated failures merged into one incident | Window or hop count too wide | Lower `SENTINEL_CORRELATION_WINDOW_MINUTES` or `_MAX_HOPS` |

## Remediation

| Symptom | Cause | Fix |
|---|---|---|
| Approve returns 403 | Key has the viewer role | Use an operator or admin key |
| Approve returns 409 | Already decided, or expired because the incident resolved | Expected. Start a new investigation if the problem continues |
| Remediation `failed` with `"service ... not in catalog"` | Target service not in the catalog | Add it with `PUT /api/v1/services/{name}` (admin) |
| `failed` with `status_code: 401` | `SENTINEL_ACTION_TOKEN` differs from the target's admin token | In Compose both come from `DEMO_ADMIN_TOKEN`. Recreate the containers |
| Stays `approved` | Manual executor (waiting for `/complete`), or the worker is down | Check `executor`, then the worker logs |

## Rate limiting and performance

| Symptom | Cause | Fix |
|---|---|---|
| Many `429` responses with `Retry-After` | All clients share one key (600/min per credential per replica) | Use one key per client, or raise `SENTINEL_RATE_LIMIT_PER_MINUTE`. For load tests: `SENTINEL_RATE_LIMIT_PER_MINUTE=1000000 docker compose up -d sentinel-api` |
| Slow investigations | Telemetry backend slow or timing out | `sentinel_telemetry_queries_total{outcome="error"}`. Tune `SENTINEL_TELEMETRY_TIMEOUT_S` and `_RETRIES` |
| LLM narrative is template-like with a remote provider | Fallback was used (bad key, quota, timeout) | `llm_usage[].fallback` in the result, and `sentinel_llm_requests_total{outcome="fallback"}`; check the worker logs |

## Development

| Symptom | Cause | Fix |
|---|---|---|
| `TypeError: Server.__init__() takes 2 positional arguments` | `mcp` 2.x installed with fastapi-mcp 0.4 | `uv sync` (the lock pins `mcp<2`) |
| `alembic check` reports changes | ORM changed without a migration | `uv run alembic revision --autogenerate -m "..."` and review it |
| Tests pass locally but fail in CI on PostgreSQL | SQLite-only behaviour (JSON, tz) | Run with `SENTINEL_TEST_DATABASE_URL`, as in [DEVELOPMENT_GUIDELINES.md](DEVELOPMENT_GUIDELINES.md#testing-standards) |
| `make dashboards` changes JSON in CI | Dashboard JSON was edited by hand | Edit `build_dashboards.py` instead and regenerate |

## Handbook modules

| Symptom | Cause | Fix |
|---|---|---|
| `handbook/001-fastapi-mcp-todo/.venv/bin/uvicorn: bad interpreter` | The virtualenv was created before the folder moved under `handbook/` | `rm -rf .venv && python3 -m venv .venv && .venv/bin/pip install -r requirements.txt` |
| `openai.AuthenticationError` in a notebook | Keys were redacted from the notebooks | Put the key in `handbook/.env` (see `handbook/.env.example`) and restart the kernel |
| Render deploy can't find the app | Old blueprint path | `render.yaml` now uses `rootDir: handbook/001-fastapi-mcp-todo` |
