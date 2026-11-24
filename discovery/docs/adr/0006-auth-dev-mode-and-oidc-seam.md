# ADR 0006 — Development auth now, OIDC seam for production

**Status:** Accepted (development mode); Proposed (OIDC).

**Context.** Legacy identity is an external identity/SSO service integrated through Node.js auth middleware and Spring filters (Verified). The PDF decides OIDC with Keycloak bundled (D3), browser sessions through a backend-for-frontend (T8). No identity provider is available in this workspace, and the brief forbids presenting dev auth as production-ready.

**Decision.** `auth.Authenticator` is the seam. The MVP ships `DevAuthenticator`: static bearer tokens mapped to tenant, subject and role, compared as SHA-256 digests in constant time; configured by `OBS_DEV_TOKENS` or documented demo defaults; **rejected at start-up when `OBS_ENV=production`**. `OBS_AUTH_MODE=oidc` validates its settings and then fails fast with a clear "not implemented" error. Roles: viewer, operator, admin → permissions enforced per route; tenant always from the credential.

**OIDC design (Proposed).** Validate JWT access tokens (issuer, audience, expiry, signature via JWKS with caching) for API clients; for the browser, authorization code + PKCE handled server-side with an HttpOnly, Secure, SameSite=Lax session cookie and CSRF protection on state-changing requests; map a tenant claim and role claims to `Principal`.

**Consequences.** The UI's sign-in page is visibly marked development-only. The Helm chart defaults to production + OIDC, which fails fast until OIDC is implemented — deliberately; `values-dev.yaml` is for throwaway clusters only.
