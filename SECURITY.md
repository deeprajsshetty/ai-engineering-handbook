# Security

## 1. Reporting a vulnerability

Do not open a public issue. Use GitHub's private vulnerability reporting ("Security" → "Report a
vulnerability") or email the maintainer. Expect an acknowledgement within 3 business days.

## 2. Threat model (Sentinel)

| Asset | Threat | Control |
|---|---|---|
| Production services | Unauthorised change via remediation | Allow-listed actions only; human approval (operator or admin role); approval not exposed over MCP; service name must be in the catalog (blocks SSRF via crafted names); every transition audited |
| Production services | Stale approval executes after the incident is over | Pending proposals expire on resolution |
| API | Credential theft or guessing | Keys are 256-bit random (`sentinel gen-key`); only SHA-256 hashes are stored; constant-time comparison; rate limiting per credential or IP |
| Webhook | Forged alerts that trigger investigations | Separate bearer token (24+ chars in prod), constant-time check; operator key as the only alternative |
| Telemetry query layer | PromQL or LogQL injection through a service name | Service names validated against `^[a-z0-9][a-z0-9-]{0,62}$` before templating |
| LLM | Prompt injection through attacker-controlled log lines or alert text | Evidence passed as delimited JSON with an instruction to treat it as data; the LLM output is narrative only and cannot rank or select actions; deterministic fallback |
| LLM provider | Data exfiltration to an unapproved vendor | LLM disabled by default (`deterministic`); provider is explicit configuration; egress restricted to port 443 by NetworkPolicy |
| Console | XSS from incident data | DOM built with `textContent` only; CSP `default-src 'self'`; `X-Frame-Options: DENY`; `nosniff` |
| API | Resource exhaustion | 1 MB body limit; token-bucket rate limiting; bounded pagination (`limit` ≤ 200/500/1000); bounded LLM `max_tokens`; bounded trace sampling |
| Database | SQL injection | SQLAlchemy parameterised queries only |
| Containers | Escape or privilege escalation | Non-root UID 10001, read-only rootfs, all capabilities dropped, `no-new-privileges`, seccomp `RuntimeDefault`, PSA `restricted` namespace, no service-account token mounted |
| Supply chain | Vulnerable or malicious dependencies | `uv.lock` with hashes; pip-audit and Trivy in CI; Dependabot; signed images with SBOM and provenance |
| Secrets | Leakage into git or logs | `.gitignore`, gitleaks (CI and pre-commit), `SecretStr` settings (never logged), nbstripout for notebooks |

## 3. Authentication and authorisation

* **API keys**: `SENTINEL_API_KEYS="name:role:sha256(key),..."`. Roles are viewer < operator <
  admin. The capability matrix is in [API_DOCUMENTATION.md](API_DOCUMENTATION.md#authentication-and-authorisation).
* **Webhook**: `SENTINEL_WEBHOOK_TOKEN`, used only by Alertmanager.
* **Outbound action token**: `SENTINEL_ACTION_TOKEN`, sent as `X-Admin-Token` to remediation
  targets and nowhere else.
* **MCP**: the same API keys; read-only tools only.
* **Roadmap**: OIDC SSO for the console; keep API keys for machine clients.

## 4. Secrets management

| Environment | Mechanism |
|---|---|
| Local / Compose | `scripts/bootstrap-env.sh` generates random secrets into `deploy/compose/.env` (git-ignored). Raw API keys go to `.env.keys` (mode 600, git-ignored). The webhook token reaches Alertmanager as a Compose secret, not a file in the repo. |
| Kubernetes dev | `secretGenerator` from `overlays/dev/secrets.env` (git-ignored) |
| Kubernetes prod | External Secrets Operator syncs from the cloud secret manager into the `sentinel-secrets` Secret |

**Rotation.**

- *API key*: add the new hashed key alongside the old one, deploy, migrate clients, then remove
  the old one.
- *Webhook token*: update the secret, then restart Alertmanager and Sentinel together.
- *Database password*: rotate in the secret manager and restart the pods, since the connection
  pool reconnects.

## 5. Compliance considerations

* **Change control** (SOC 2 CC8, ISO 27001 A.8.32): every production change through Sentinel is
  approved by a named principal and audited (`audit_events`). Grant the application role only
  INSERT and SELECT on `audit_events`.
* **Data minimisation**: Sentinel stores alert metadata, aggregated statistics and normalised log
  excerpts (identifiers and numbers collapsed), not raw log streams.
* **Data residency**: with the deterministic provider, no data leaves your network.
* **Retention**: incident and audit data are kept indefinitely by default. Define a policy per
  customer and enforce it with a scheduled job.

## 6. Security history of this repository

**Finding (2026-11-10): API keys hard-coded in notebooks.** Assessment of the original repository
found the following in files that had **not been committed** (the git history contains only the
initial `.gitignore`, `LICENSE` and `README.md`):

| File (now under `handbook/`) | Secret type |
|---|---|
| `002-.../chat_completions_api_parameters_v2.ipynb` | OpenAI project key (`sk-proj-…`), in code |
| `002-.../making_gpt_requests_using_the_openai_api_v2.ipynb` | OpenAI project key, in code and in a cell output |
| `003-.../prompt_engineering_for_genai.ipynb` | OpenAI project key, in a cell output |
| `004-.../prompt_engineering_for_genai.ipynb` and checkpoints | OpenAI project key |
| `005-.../01_B_LangChain_Deep_Dive_Tools_Agents_ReAct.ipynb` and checkpoint | LangSmith key (`lsv2_pt_…`) |

**Remediation performed:**

- keys redacted in place;
- code cells changed to read `os.getenv(...)`;
- checkpoints, virtualenvs, local databases and `.env` files ignored;
- `handbook/.env.example` added;
- gitleaks and nbstripout pre-commit hooks added.

**Action required by the owner.** The keys existed in plain text on disk and may have been shared,
synced or pushed elsewhere:

1. **Rotate** both OpenAI project keys in the OpenAI dashboard (API keys → revoke) and the
   LangSmith key, even if you believe they were never shared.
2. Check the usage dashboards for unexpected activity.
3. If any copy of these notebooks was ever pushed to another remote, treat it as public and purge
   it with `git filter-repo`, in addition to rotating.

## 7. Security testing

| Test | Where |
|---|---|
| AuthN/AuthZ, 401/403 matrix, webhook token, rate limit, body limit, security headers | `tests/integration/test_api_platform.py` |
| SSRF guard (service not in catalog) | `tests/unit/test_infrastructure.py::TestActionExecutor` |
| Query-injection guard | `test_render_rejects_injection` |
| Static analysis | `bandit -r src` (clean) |
| Dependency vulnerabilities | `pip-audit` (clean as of 2026-11-10) |
| Image scan | Trivy in CI |
| Secret scan | gitleaks in CI and pre-commit |
