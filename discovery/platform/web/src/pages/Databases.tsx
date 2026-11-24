import { useState } from "react";
import { api } from "../api/client";
import type { Fleet, FleetRow, MetricDefinition } from "../api/types";
import { href, useAsync } from "../lib/hooks";
import { formatValue, relativeTime } from "../lib/format";
import { Card, Empty, ErrorBanner, Loading, SimulatedBadge, Stat, StatusBadge } from "../components/ui";
import { TypeIcon } from "../components/Icon";

const INSTANCE_METRICS = ["db.connections", "db.sessions.active", "db.transactions.rate", "db.cache.hit_ratio", "storage.used", "cpu.utilization"];
const TABLESPACE_METRICS = ["db.storage.utilization", "storage.used"];

const ENGINE_NAMES: Record<string, string> = { oracle: "Oracle Database", postgresql: "PostgreSQL" };

/**
 * Databases is the engine-agnostic fleet view: every database server from
 * any engine and any connection, with the same health columns. Engine-only
 * metrics (for example host CPU from Oracle) show "—" where an engine does
 * not report them, rather than a guessed value.
 */
export function Databases() {
  const [engine, setEngine] = useState("");
  const label = engine ? `engine=${engine}` : undefined;
  const facets = useAsync(() => api.facets({ domain: "database", status: "active" }), [], 30000);
  const instances = useAsync(
    () => api.fleet({ type: "database.instance", status: "active", label, metrics: INSTANCE_METRICS.join(","), limit: 200 }),
    [label],
    30000,
  );
  const tablespaces = useAsync(
    () => api.fleet({ type: "database.tablespace", status: "active", label, metrics: TABLESPACE_METRICS.join(","), limit: 200 }),
    [label],
    30000,
  );
  const engines = facets.data?.facets.engine ?? [];
  const types = new Map((facets.data?.facets.type ?? []).map((t) => [t.value, t.count]));

  return (
    <div className="stack">
      <div className="page-head">
        <h1>Databases</h1>
        <span className="muted">One view for every engine. Connect another server from Connections → New connection → Database server.</span>
        <span className="spacer" />
        <a className="btn btn-primary" href={href("connections", undefined, { provider: "database" })}>
          Add database
        </a>
      </div>
      <ErrorBanner error={facets.error ?? instances.error} onRetry={instances.reload} />
      <div className="stats">
        <Stat label="Database servers" value={types.get("database.instance") ?? 0} />
        <Stat label="Databases / PDBs" value={types.get("database.database") ?? 0} />
        <Stat label="Tablespaces" value={types.get("database.tablespace") ?? 0} />
        <Stat label="Engines" value={engines.length} hint={engines.map((e) => ENGINE_NAMES[e.value] ?? e.value).join(", ") || "—"} />
      </div>
      <div className="segmented" role="radiogroup" aria-label="Engine">
        <button type="button" role="radio" aria-checked={engine === ""} className={engine === "" ? "on" : ""} onClick={() => setEngine("")}>
          All engines
        </button>
        {engines.map((e) => (
          <button
            key={e.value}
            type="button"
            role="radio"
            aria-checked={engine === e.value}
            className={engine === e.value ? "on" : ""}
            onClick={() => setEngine(e.value)}
          >
            {ENGINE_NAMES[e.value] ?? e.value} <span className="muted">{e.count}</span>
          </button>
        ))}
      </div>
      <Card title="Database servers">
        {!instances.data && instances.loading && <Loading />}
        {instances.data && <FleetTable fleet={instances.data} empty="No database servers discovered yet." />}
      </Card>
      <Card title="Tablespaces by utilization">
        {tablespaces.data && <TablespaceTable fleet={tablespaces.data} />}
      </Card>
    </div>
  );
}

function sourceOf(row: FleetRow): "simulated" | "measured" | undefined {
  const s = Object.values(row.latest)[0];
  return s?.source;
}

function FleetTable({ fleet, empty }: { fleet: Fleet; empty: string }) {
  if (fleet.items.length === 0) return <Empty>{empty}</Empty>;
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th scope="col">Server</th>
            <th scope="col">Engine</th>
            <th scope="col">Location</th>
            {fleet.metrics.map((m) => (
              <th key={m.name} scope="col" className="num" title={m.description}>
                {shortName(m)}
              </th>
            ))}
            <th scope="col">Data</th>
          </tr>
        </thead>
        <tbody>
          {fleet.items.map((r) => {
            const src = sourceOf(r);
            return (
              <tr key={r.id}>
                <td>
                  <span className="cell-name">
                    <TypeIcon type={r.type} />
                    <a href={href("resources", r.id)}>{r.name}</a>
                    <StatusBadge status={r.status} />
                  </span>
                </td>
                <td>
                  {ENGINE_NAMES[r.labels.engine ?? ""] ?? r.labels.engine ?? "—"}{" "}
                  <span className="muted small">{String(r.properties.engineVersion ?? "")}</span>
                </td>
                <td>{r.region}</td>
                {fleet.metrics.map((m) => {
                  const s = r.latest[m.name];
                  return (
                    <td key={m.name} className="num" title={s ? `${relativeTime(s.ts)} · ${s.collector}` : "not reported by this engine or not collected yet"}>
                      {s ? formatValue(s.value, m.unit) : <span className="muted">—</span>}
                    </td>
                  );
                })}
                <td>{src ? <SimulatedBadge simulated={src === "simulated"} /> : <span className="muted">no samples</span>}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function TablespaceTable({ fleet }: { fleet: Fleet }) {
  const rows = [...fleet.items].sort((a, b) => (b.latest["db.storage.utilization"]?.value ?? -1) - (a.latest["db.storage.utilization"]?.value ?? -1));
  if (rows.length === 0) return <Empty>No tablespaces discovered.</Empty>;
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th scope="col">Tablespace</th>
            <th scope="col">Engine</th>
            <th scope="col">Utilization</th>
            <th scope="col" className="num">
              Used
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const u = r.latest["db.storage.utilization"]?.value;
            const used = r.latest["storage.used"]?.value;
            return (
              <tr key={r.id}>
                <td>
                  <span className="cell-name">
                    <TypeIcon type={r.type} />
                    <a href={href("resources", r.id)}>{r.name}</a>
                  </span>
                </td>
                <td>{ENGINE_NAMES[r.labels.engine ?? ""] ?? r.labels.engine}</td>
                <td>
                  {u === undefined ? (
                    <span className="muted" title="This engine does not report a maximum size">
                      —
                    </span>
                  ) : (
                    <span className="meter" role="meter" aria-valuenow={u} aria-valuemin={0} aria-valuemax={100} aria-label={`${r.name} utilization`}>
                      <span className="meter-bar" style={{ width: `${Math.min(100, u)}%` }} />
                      <span className="meter-text">{u.toFixed(1)} %</span>
                    </span>
                  )}
                </td>
                <td className="num">{used === undefined ? "—" : formatValue(used, "By")}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function shortName(m: MetricDefinition): string {
  const map: Record<string, string> = {
    "db.connections": "Connections",
    "db.sessions.active": "Active sessions",
    "db.transactions.rate": "Transactions/s",
    "db.cache.hit_ratio": "Cache hit",
    "storage.used": "Storage",
    "cpu.utilization": "CPU",
  };
  return map[m.name] ?? m.name;
}
