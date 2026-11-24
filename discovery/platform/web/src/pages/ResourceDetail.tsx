import { api } from "../api/client";
import type { ResourceMetric } from "../api/types";
import { useAsync, href } from "../lib/hooks";
import { dateTime, formatValue, relativeTime } from "../lib/format";
import { LineChart } from "../components/LineChart";
import { GraphView } from "../components/GraphView";
import { TIME_RANGES, useApp } from "../lib/context";
import { TypeIcon } from "../components/Icon";
import { Card, Empty, ErrorBanner, FreshnessBadge, KeyValues, Loading, SimulatedBadge, StatusBadge } from "../components/ui";


/** Full-page resource view. */
export function ResourceDetail({ id }: { id: string }) {
  const nb = useAsync(() => api.neighborhood(id, 1), [id], 30000);
  return (
    <div className="stack">
      <p>
        <a href={href("resources")}>← Explorer</a>
      </p>
      <ResourcePanel id={id} full />
      <Card title="Neighbourhood (1 hop)">
        <ErrorBanner error={nb.error} />
        {nb.data &&
          (nb.data.nodes.length > 1 ? (
            <GraphView
              nodes={nb.data.nodes}
              edges={nb.data.edges}
              selectedId={id}
              onSelect={(n) => (window.location.hash = href("resources", n))}
              ariaLabel="Neighbourhood graph"
            />
          ) : (
            <Empty>No relationships.</Empty>
          ))}
      </Card>
    </div>
  );
}

/** ResourcePanel shows attributes, freshness, relationships and metrics. */
export function ResourcePanel({ id, onClose, full }: { id: string; onClose?: () => void; full?: boolean }) {
  const res = useAsync(() => api.resource(id), [id], 30000);
  const rels = useAsync(() => api.relationships({ resourceId: id, limit: 200 }), [id]);
  const r = res.data;
  const { typeInfo } = useApp();
  const keyProps = r ? (typeInfo(r.type).keyProperties ?? []).filter((k) => r.properties[k] !== undefined) : [];
  return (
    <div className="stack panel" aria-live="polite">
      <ErrorBanner error={res.error} onRetry={res.reload} />
      {!r && <Loading />}
      {r && (
        <>
          <Card
            title={
              full ? (
                <span className="h1">{r.name}</span>
              ) : (
                <a href={href("resources", r.id)} title="Open resource page">
                  {r.name}
                </a>
              )
            }
            actions={
              onClose && (
                <button type="button" className="btn btn-small" onClick={onClose} aria-label="Close details">
                  ✕
                </button>
              )
            }
          >
            <p className="cell-name">
              <TypeIcon type={r.type} /> <strong>{typeInfo(r.type).displayName}</strong> <code className="muted">{r.type}</code> <StatusBadge status={r.status} /> <FreshnessBadge freshness={r.freshness} />{" "}
              {r.labels.simulated === "true" && <SimulatedBadge />}
            </p>
            <KeyValues
              entries={[
                ...keyProps.map((k) => [k, String(r.properties[k])] as [string, string]),
                ["Provider", r.provider],
                ["Scope", <code key="s">{r.scope}</code>],
                ["External ID", <code key="x">{r.externalId}</code>],
                ["Native type", r.nativeType || "—"],
                ["Region", r.region],
                ["First seen", dateTime(r.firstSeenAt)],
                ["Last seen", `${dateTime(r.lastSeenAt)} (${relativeTime(r.lastSeenAt)})`],
                ...(r.missingSince ? ([["Missing since", dateTime(r.missingSince)]] as [string, string][]) : []),
                ["Last job", <a key="j" href={href("jobs", r.lastJobId)}>{r.lastJobId.slice(0, 16)}…</a>],
              ]}
            />
            {Object.keys(r.labels).length > 0 && (
              <div className="labels" aria-label="Labels">
                {Object.entries(r.labels).map(([k, v]) => (
                  <span key={k} className="label">
                    {k}={v}
                  </span>
                ))}
              </div>
            )}
            {Object.keys(r.properties).length > 0 && (
              <details>
                <summary>Provider properties</summary>
                <pre className="props">{JSON.stringify(r.properties, null, 2)}</pre>
              </details>
            )}
          </Card>
          <Card title="Relationships">
            {rels.data && rels.data.items.length > 0 ? (
              <ul className="list compact">
                {rels.data.items.map((e) =>
                  e.sourceId === id ? (
                    <li key={e.id}>
                      → <code>{e.type}</code> <a href={href("resources", e.targetId)}>{e.targetName}</a> <span className="muted">{e.targetType}</span>
                      {e.status === "missing" && <StatusBadge status="missing" />}
                    </li>
                  ) : (
                    <li key={e.id}>
                      ← <a href={href("resources", e.sourceId)}>{e.sourceName}</a> <code>{e.type}</code> this{" "}
                      <span className="muted">({e.sourceType})</span>
                      {e.status === "missing" && <StatusBadge status="missing" />}
                    </li>
                  ),
                )}
              </ul>
            ) : (
              <Empty>No relationships.</Empty>
            )}
          </Card>
          <Metrics id={id} capabilities={r.metricCapabilities} />
        </>
      )}
    </div>
  );
}

function Metrics({ id, capabilities }: { id: string; capabilities: string[] }) {
  const metrics = useAsync(() => api.resourceMetrics(id), [id], 30000);
  const { range, setRange } = useApp();
  if (capabilities.length === 0) {
    return (
      <Card title="Metrics">
        <Empty>No metrics are defined for this resource type.</Empty>
      </Card>
    );
  }
  return (
    <Card
      title="Metrics"
      actions={
        <div className="segmented" role="radiogroup" aria-label="Time range">
          {TIME_RANGES.map((r) => (
            <button key={r.key} type="button" role="radio" aria-checked={range.key === r.key} className={range.key === r.key ? "on" : ""} onClick={() => setRange(r.key)}>
              {r.short}
            </button>
          ))}
        </div>
      }
    >
      <ErrorBanner error={metrics.error} onRetry={metrics.reload} />
      {!metrics.data && <Loading />}
      <div className="metrics">
        {metrics.data?.items.map((m) => <MetricChart key={m.name} id={id} m={m} rangeMs={range.ms} />)}
      </div>
    </Card>
  );
}

function MetricChart({ id, m, rangeMs }: { id: string; m: ResourceMetric; rangeMs: number }) {
  const series = useAsync(() => api.series(id, m.name, new Date(Date.now() - rangeMs).toISOString()), [id, m.name, rangeMs], 60000);
  return (
    <div className="metric">
      <div className="metric-head">
        <strong>{m.name}</strong> <span className="muted">({m.unit})</span>{" "}
        {m.latest ? <SimulatedBadge simulated={m.latest.source === "simulated"} /> : <span className="muted">no samples yet</span>}
        <div className="metric-value">{m.latest ? formatValue(m.latest.value, m.unit) : "—"}</div>
        <div className="muted small">
          {m.description}
          {m.latest && ` · ${relativeTime(m.latest.ts)} · ${m.latest.collector}`}
        </div>
      </div>
      {series.error ? <ErrorBanner error={series.error} /> : series.data && <LineChart points={series.data.points} unit={m.unit} label={m.name} />}
    </div>
  );
}
