# Security notes and threat model (MVP)

Scope: the `platform/` slice. No compliance or certification is claimed; the platform is **not production-ready** until the gaps in §4 are closed. Legacy findings S1–S6 from the PDF (committed keys and passwords, insecure TLS defaults, credentials in URLs) are in legacy repositories and are **not remediated here**; rotation is recommended to their owners.

## 1. Assets and trust boundaries

Assets: provider credentials (by reference), topology of customer estates (sensitive: reveals architecture), metric data, audit trail, database credentials. Boundaries are drawn in the [HLD trust-boundary diagram](../design/hld.md#5-security-zones-and-trust-boundaries).

## 2. Threats and controls (STRIDE-style)

| Threat | Control in code | Test |
|---|---|---|
| Spoofing: forged or missing token | Bearer required on every `/api/v1` route except the spec; SHA-256 + constant-time compare; 401 with `WWW-Authenticate` | `TestDevAuthenticator`, E2E |
| Spoofing: dev tokens used in production | `OBS_AUTH_MODE=dev` refused when `OBS_ENV=production` | `TestConfigValidation` |
| Tampering / IDOR across tenants | Tenant from credential; every SQL statement filters `tenant_id`; IDs include tenant in hash; cross-tenant → 404 | E2E tenant isolation; store tests |
| Tampering: cross-tenant metric injection | Sample insert joins `resources` on `(id, tenant_id)` | `TestMetricSamplesIdempotentAndBucketed` |
| Elevation: viewer runs discovery | Route-table permissions, checked against OpenAPI | E2E 403s; `TestOpenAPIMatchesRouteTable` |
| Information disclosure: secrets in DB/API/logs | Secret-like config keys rejected; credentials by reference; `secrets.Value` redacts; DB URL redacted; errors name references only | `TestSecretReferences`, E2E "hunter2" check |
| Information disclosure: arbitrary env read via `env:` ref | Only `OBS_SECRET_*` names; `file:` confined to `OBS_SECRET_DIR`, no `..` | `TestSecretReferences` |
| SSRF to metadata or internal services | https only, egress allow-list, metadata/link-local refused at validation and dial time, no redirects, no embedded credentials | `TestEgressPolicy` (both) |
| DoS: large bodies, floods, huge provider responses, unbounded queries | 1 MiB bodies, per-principal token bucket, 32 MiB page cap, snapshot cap, limit ≤ 500, series ≤ 1,500 points / 7 days, graph ≤ 1,000 nodes, statement timeouts | `TestRateLimiter`, E2E limit checks |
| DoS: unbounded logs and data | Job events capped; retention for samples, jobs, changes, audit | store tests |
| Repudiation | Audit events with actor, tenant, request ID | E2E audit check |
| Injection (SQL, LIKE) | Parameterized SQL only; LIKE wildcards in user input escaped | E2E `q=%` check |
| XSS / clickjacking | React escaping; CSP `default-src 'self'`; `X-Frame-Options: DENY`; JSON encoder escapes HTML | static UI test |
| Write access to targets | Kubernetes client issues GET only; RBAC example is get/list/watch | contract test fails on non-GET |
| Write access to monitored databases | Engines issue catalog/statistics `SELECT`s only, row-capped; PostgreSQL sessions forced `default_transaction_read_only=on`, `statement_timeout=5s`; documented least-privilege grants (`pg_monitor`; `SELECT_CATALOG_ROLE`) | `TestPostgreSQLEndToEnd` (runs as configured role), Oracle `enginetest.Fake` fails on unexpected SQL |
| SSRF / lateral movement via database connections | DB host must be in `OBS_EGRESS_ALLOWLIST`; resolved addresses re-checked at dial time by `netguard.Policy.Dialer` (metadata, link-local refused; loopback only when allow-listed literally); plaintext only with `OBS_ALLOW_INSECURE_ENDPOINTS` | `TestDialTimeGuard`, `TestValidateConfig` |
| Information disclosure: platform-store password (Oracle) | `OBS_DATABASE_URL` from a Secret; `oracle.Open` redacts the password from driver errors; config logs the URL redacted; verified no password in the demo log | `make demo-oracle` log check (verification.md) |
| Information disclosure: DB connection strings in errors | Passwords set on driver config, never in a DSN string; errors containing `://` are replaced | `discovery/database.sanitize` |
| Supply chain | Exact pins, lockfiles, `go mod verify`, govulncheck/npm audit, Trivy image and secret scan, SBOM in CI | CI workflow (not run locally — see verification) |
| Container escape impact | distroless, non-root UID 65532, read-only root FS, all capabilities dropped, no SA token mounted | chart render |

## 3. Production guidance (Recommendation)

TLS at the gateway (cert-manager), and `sslmode=verify-full` to PostgreSQL; NetworkPolicies enabled (chart default) with explicit provider CIDRs; secrets via External Secrets Operator from the customer's store (OpenBao, cloud secret managers); restrict `/metrics` to the monitoring namespace; image signing (cosign) and admission policy; branch protection with required CI and review.

## 4. Known gaps before production

1. OIDC/BFF authentication not implemented (ADR 0006).
2. No per-resource authorization beyond tenant + role.
3. Audit retention and contents not validated against compliance needs (PDF Q7 open).
4. The egress check validates IPs at dial time; outbound proxies are not supported yet.
5. `/metrics` is unauthenticated (by design for scrapers) — must be network-restricted.
6. No data-residency controls (PDF Q8 open).
