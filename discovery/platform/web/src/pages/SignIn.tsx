import { useState, type FormEvent } from "react";
import { api, describeError, setToken } from "../api/client";
import type { Me } from "../api/types";

const DEMO_TOKENS = [
  { token: "dev-admin", label: "demo / admin — all permissions" },
  { token: "dev-operator", label: "demo / operator — run discovery and collection" },
  { token: "dev-viewer", label: "demo / viewer — read only" },
  { token: "dev-acme-admin", label: "acme / admin — a second tenant (isolation demo)" },
];

/**
 * Development sign-in. The server accepts static tokens only when
 * OBS_AUTH_MODE=dev, which is refused in production. Production sign-in is an
 * OIDC flow through a backend-for-frontend (not implemented in the MVP).
 */
export function SignIn({ onSignedIn }: { onSignedIn: (me: Me) => void }) {
  const [token, setTok] = useState("dev-admin");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    setToken(token.trim());
    try {
      onSignedIn(await api.me());
    } catch (err) {
      setToken(null);
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="signin">
      <form className="card signin-card" onSubmit={submit} aria-describedby="dev-note">
        <h1>Observability Platform</h1>
        <p id="dev-note" className="banner banner-warn">
          Development sign-in. Static demo tokens work only when the server runs with <code>OBS_AUTH_MODE=dev</code>; this is not a
          production authentication method.
        </p>
        <fieldset>
          <legend>Demo identity</legend>
          {DEMO_TOKENS.map((t) => (
            <label key={t.token} className="radio">
              <input type="radio" name="token" value={t.token} checked={token === t.token} onChange={() => setTok(t.token)} />
              <span>
                <code>{t.token}</code> {t.label}
              </span>
            </label>
          ))}
        </fieldset>
        <label className="field">
          <span>Or enter a token</span>
          <input type="password" autoComplete="off" value={token} onChange={(e) => setTok(e.target.value)} required minLength={8} />
        </label>
        {error && (
          <p role="alert" className="banner banner-bad">
            {error}
          </p>
        )}
        <button className="btn btn-primary" disabled={busy} type="submit">
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </main>
  );
}
