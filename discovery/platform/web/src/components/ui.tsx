import type { ReactNode } from "react";
import { describeError } from "../api/client";
import type { Freshness, JobStatus, Status } from "../api/types";

export function Badge({ tone, children, title }: { tone: string; children: ReactNode; title?: string }) {
  return (
    <span className={`badge badge-${tone}`} title={title}>
      {children}
    </span>
  );
}

const jobTone: Record<JobStatus, string> = {
  queued: "neutral",
  running: "info",
  succeeded: "ok",
  failed: "bad",
  cancelled: "warn",
};

export function JobStatusBadge({ status }: { status: JobStatus }) {
  return <Badge tone={jobTone[status]}>{status}</Badge>;
}

export function StatusBadge({ status }: { status: Status }) {
  return (
    <Badge tone={status === "active" ? "ok" : "warn"} title={status === "missing" ? "Not seen in the latest complete discovery run" : undefined}>
      {status}
    </Badge>
  );
}

const freshTone: Record<Freshness, string> = { fresh: "ok", aging: "warn", stale: "bad" };

export function FreshnessBadge({ freshness }: { freshness: Freshness }) {
  return <Badge tone={freshTone[freshness]}>{freshness}</Badge>;
}

export function SimulatedBadge({ simulated = true }: { simulated?: boolean }) {
  return simulated ? (
    <Badge tone="sim" title="Generated locally by the deterministic simulator; not a real measurement">
      simulated
    </Badge>
  ) : (
    <Badge tone="ok" title="Read from a provider metrics API">
      measured
    </Badge>
  );
}

export function ErrorBanner({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  if (!error) return null;
  return (
    <div role="alert" className="banner banner-bad">
      <span>{describeError(error)}</span>
      {onRetry && (
        <button type="button" className="btn btn-small" onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  );
}

export function Loading({ label = "Loading" }: { label?: string }) {
  return (
    <p className="muted" role="status" aria-live="polite">
      {label}…
    </p>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="empty">{children}</p>;
}

export function Card({ title, actions, children }: { title?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      {(title || actions) && (
        <header className="card-head">
          {title && <h2>{title}</h2>}
          {actions && <div className="card-actions">{actions}</div>}
        </header>
      )}
      {children}
    </section>
  );
}

export function Stat({ label, value, hint }: { label: string; value: ReactNode; hint?: ReactNode }) {
  return (
    <div className="stat">
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value}</div>
      {hint && <div className="stat-hint">{hint}</div>}
    </div>
  );
}

export function KeyValues({ entries }: { entries: [string, ReactNode][] }) {
  return (
    <dl className="kv">
      {entries.map(([k, v]) => (
        <div key={k} className="kv-row">
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}
