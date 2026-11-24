import { useState, type FormEvent } from "react";
import { api, ApiError, describeError } from "../api/client";
import type { Connection, Me, Provider } from "../api/types";
import { useAsync, href } from "../lib/hooks";
import { newIdempotencyKey, relativeTime } from "../lib/format";
import { Badge, Card, Empty, ErrorBanner, JobStatusBadge, Loading, SimulatedBadge } from "../components/ui";

export function Connections({ me, params }: { me: Me; params?: URLSearchParams }) {
  const preset = params?.get("provider") ?? undefined;
  const conns = useAsync(() => api.connections(), [], 3000);
  const providers = useAsync(() => api.providers(), []);
  const [notice, setNotice] = useState<{ tone: string; text: string }>();
  const [showForm, setShowForm] = useState(() => Boolean(preset));
  const canWrite = me.permissions.includes("connections:write");
  const canRun = me.permissions.includes("discovery:run");
  const canCollect = me.permissions.includes("metrics:collect");

  async function act(label: string, fn: () => Promise<string>) {
    setNotice(undefined);
    try {
      setNotice({ tone: "ok", text: await fn() });
      conns.reload();
    } catch (e) {
      setNotice({ tone: "bad", text: `${label}: ${describeError(e)}` });
    }
  }

  const run = (c: Connection) =>
    act("Start discovery", async () => {
      try {
        const j = await api.startDiscovery(c.id, newIdempotencyKey("ui"));
        return `Discovery job ${j.id} queued for ${c.name}.`;
      } catch (e) {
        if (e instanceof ApiError && e.problem?.code === "job_active") return `${c.name} already has an active job.`;
        throw e;
      }
    });

  return (
    <div className="stack">
      <div className="page-head">
        <h1>Connections</h1>
        {canWrite && (
          <button type="button" className="btn" onClick={() => setShowForm((s) => !s)} aria-expanded={showForm}>
            {showForm ? "Close" : "New connection"}
          </button>
        )}
      </div>
      <p className="muted">A connection is one account, project, subscription or cluster that discovery reads (read-only).</p>
      {notice && (
        <p role="status" className={`banner banner-${notice.tone}`}>
          {notice.text}
        </p>
      )}
      {showForm && providers.data && (
        <NewConnection
          providers={providers.data.items}
          preset={preset}
          onCreated={(c) => {
            setShowForm(false);
            setNotice({ tone: "ok", text: `Created ${c.name}.` });
            conns.reload();
          }}
        />
      )}
      <ErrorBanner error={conns.error} onRetry={conns.reload} />
      {!conns.data && conns.loading && <Loading />}
      {conns.data && conns.data.items.length === 0 && (
        <Empty>
          No connections. Run <code>make seed</code> or create one.
        </Empty>
      )}
      <div className="cards">
        {conns.data?.items.map((c) => (
          <Card
            key={c.id}
            title={
              <>
                {c.name} {c.simulated ? <SimulatedBadge /> : <Badge tone="neutral">{c.provider}</Badge>}
              </>
            }
          >
            <dl className="kv">
              <div className="kv-row">
                <dt>Provider</dt>
                <dd>{c.provider}</dd>
              </div>
              <div className="kv-row">
                <dt>Credential</dt>
                <dd>{c.credential.configured ? <code>{c.credential.ref}</code> : "none"}</dd>
              </div>
              <div className="kv-row">
                <dt>Schedule</dt>
                <dd>{c.discoveryIntervalMinutes ? `every ${c.discoveryIntervalMinutes} min` : "manual"}</dd>
              </div>
              <div className="kv-row">
                <dt>Metrics</dt>
                <dd>{c.metricsSource}</dd>
              </div>
              <div className="kv-row">
                <dt>Last job</dt>
                <dd>
                  {c.lastJob ? (
                    <>
                      <a href={href("jobs", c.lastJob.id)}>
                        <JobStatusBadge status={c.lastJob.status} />
                      </a>{" "}
                      <span className="muted">{relativeTime(c.lastJob.createdAt)}</span>
                    </>
                  ) : (
                    "never run"
                  )}
                </dd>
              </div>
            </dl>
            <div className="actions">
              {canRun && (
                <button type="button" className="btn btn-primary" onClick={() => run(c)}>
                  Run discovery
                </button>
              )}
              {canWrite && (
                <button
                  type="button"
                  className="btn"
                  onClick={() =>
                    act("Validate", async () => {
                      const r = await api.validateConnection(c.id);
                      return r.valid
                        ? `${c.name}: configuration valid; ${r.check.message ?? "check passed"}.`
                        : `${c.name}: ${[...r.errors.map((e) => `${e.field}: ${e.message}`), r.check.message].filter(Boolean).join("; ")}`;
                    })
                  }
                >
                  Validate
                </button>
              )}
              {canCollect && (
                <button
                  type="button"
                  className="btn"
                  onClick={() =>
                    act("Collect metrics", async () => {
                      const r = await api.collectMetrics(c.id);
                      return `Metric collection ${r.id} started (${r.collector}).`;
                    })
                  }
                >
                  Collect metrics
                </button>
              )}
              <a className="btn btn-ghost" href={href("topology", undefined, { connectionId: c.id })}>
                Topology
              </a>
            </div>
          </Card>
        ))}
      </div>
    </div>
  );
}

type FieldValue = string | boolean;

function initialValues(p?: Provider): Record<string, FieldValue> {
  const out: Record<string, FieldValue> = {};
  for (const f of p?.configFields ?? []) {
    if (f.type === "boolean") out[f.name] = f.default === true;
    else out[f.name] = f.default === undefined ? (f.options?.[0]?.value ?? "") : String(f.default);
  }
  return out;
}

/** toConfig converts form strings to the typed JSON the API validates. */
function toConfig(p: Provider, values: Record<string, FieldValue>): Record<string, unknown> {
  const cfg: Record<string, unknown> = {};
  for (const f of p.configFields) {
    const v = values[f.name];
    if (v === undefined || v === "") continue;
    if (f.type === "integer") cfg[f.name] = Number(v);
    else if (f.type === "boolean") cfg[f.name] = v === true;
    else if (f.type === "stringList") cfg[f.name] = String(v).split(",").map((x) => x.trim()).filter(Boolean);
    else cfg[f.name] = v;
  }
  return cfg;
}

/**
 * NewConnection renders each provider's declared configuration fields, so a
 * provider added by a backend module gets a working form with no UI change.
 */
function NewConnection({ providers, preset, onCreated }: { providers: Provider[]; preset?: string; onCreated: (c: Connection) => void }) {
  const [provider, setProvider] = useState(() => (providers.some((x) => x.name === preset) ? preset! : (providers[0]?.name ?? "fixture")));
  const p = providers.find((x) => x.name === provider);
  const [name, setName] = useState("");
  const [values, setValues] = useState<Record<string, FieldValue>>(() => initialValues(p));
  const [credentialRef, setCredentialRef] = useState("");
  const [interval, setInterval] = useState(0);
  const [error, setError] = useState<string>();

  function pick(next: string) {
    setProvider(next);
    setValues(initialValues(providers.find((x) => x.name === next)));
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(undefined);
    if (!p) return;
    try {
      onCreated(
        await api.createConnection({
          name,
          provider,
          config: toConfig(p, values),
          credentialRef: credentialRef || undefined,
          discoveryIntervalMinutes: interval,
        }),
      );
    } catch (err) {
      setError(describeError(err));
    }
  }

  return (
    <Card title="New connection">
      <form className="form" onSubmit={submit}>
        <fieldset className="provider-picker">
          <legend>What do you want to monitor?</legend>
          <div className="provider-grid">
            {providers.map((x) => (
              <label key={x.name} className={`provider-card ${x.name === provider ? "on" : ""}`}>
                <input type="radio" name="provider" value={x.name} checked={x.name === provider} onChange={() => pick(x.name)} />
                <strong>{x.displayName}</strong>
                <span className="muted small">{x.description}</span>
                <span>{x.simulated ? <SimulatedBadge /> : <Badge tone="ok">{x.metricsSource} metrics</Badge>}</span>
              </label>
            ))}
          </div>
        </fieldset>
        <label className="field">
          <span>Name</span>
          <input value={name} onChange={(e) => setName(e.target.value)} required maxLength={64} placeholder="e.g. ERP Oracle (prod)" />
        </label>
        {p?.configFields.map((f) => {
          const v = values[f.name] ?? "";
          const id = `cfg-${f.name}`;
          const opt = f.options?.find((o) => o.value === v);
          return (
            <div key={f.name} className="field">
              <label htmlFor={id}>
                {f.name}
                {f.required && <span aria-hidden="true"> *</span>}
              </label>
              {f.options ? (
                <select id={id} value={String(v)} required={f.required} onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}>
                  {!f.required && <option value="">(default)</option>}
                  {f.options.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              ) : f.type === "boolean" ? (
                <input id={id} type="checkbox" checked={v === true} onChange={(e) => setValues({ ...values, [f.name]: e.target.checked })} />
              ) : (
                <input
                  id={id}
                  type={f.type === "integer" ? "number" : "text"}
                  value={String(v)}
                  required={f.required}
                  placeholder={f.placeholder ?? (f.type === "stringList" ? "comma,separated" : undefined)}
                  onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
                />
              )}
              <small className="muted">
                {f.description}
                {opt?.description && (
                  <>
                    <br />
                    <strong>Required privileges:</strong> {opt.description}
                  </>
                )}
              </small>
            </div>
          );
        })}
        <label className="field">
          <span>Credential reference {p?.requiresCredential ? "(required)" : "(not used)"}</span>
          <input
            value={credentialRef}
            onChange={(e) => setCredentialRef(e.target.value)}
            placeholder="env:OBS_SECRET_MY_PASSWORD or file:my-secret"
            disabled={!p?.requiresCredential}
            required={p?.requiresCredential}
          />
          <small className="muted">A reference to a secret held by the server; the secret itself is never sent from the browser.</small>
        </label>
        <label className="field">
          <span>Discovery interval in minutes (0 = manual)</span>
          <input type="number" min={0} max={10080} value={interval} onChange={(e) => setInterval(Number(e.target.value))} />
        </label>
        {error && (
          <p role="alert" className="banner banner-bad">
            {error}
          </p>
        )}
        <button type="submit" className="btn btn-primary">
          Create connection
        </button>
      </form>
    </Card>
  );
}
