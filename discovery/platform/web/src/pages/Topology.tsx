import { useMemo, useState } from "react";
import { api } from "../api/client";
import { useAsync, href } from "../lib/hooks";
import { GraphView } from "../components/GraphView";
import { Badge, Card, Empty, ErrorBanner, Loading, StatusBadge } from "../components/ui";
import { ResourcePanel } from "./ResourceDetail";

export function Topology({ params }: { params: URLSearchParams }) {
  const [connectionId, setConnectionId] = useState(params.get("connectionId") ?? "");
  const [type, setType] = useState(params.get("type") ?? "");
  const [q, setQ] = useState("");
  const [includeMissing, setIncludeMissing] = useState(false);
  const [view, setView] = useState<"graph" | "table">("graph");
  const [selected, setSelected] = useState<string>();
  const conns = useAsync(() => api.connections(), []);
  const graph = useAsync(
    () => api.graph({ connectionId, type, q, includeMissing, maxNodes: 300 }),
    [connectionId, type, q, includeMissing],
    15000,
  );
  const types = useMemo(() => [...new Set(graph.data?.nodes.map((n) => n.type) ?? [])].sort(), [graph.data]);
  const byId = useMemo(() => new Map(graph.data?.nodes.map((n) => [n.id, n]) ?? []), [graph.data]);
  const g = graph.data;

  return (
    <div className="stack">
      <h1>Topology</h1>
      <form className="filters" role="search" onSubmit={(e) => e.preventDefault()}>
        <label className="field">
          <span>Connection</span>
          <select value={connectionId} onChange={(e) => setConnectionId(e.target.value)}>
            <option value="">All connections</option>
            {conns.data?.items.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Type</span>
          <select value={type} onChange={(e) => setType(e.target.value)}>
            <option value="">All types</option>
            {(type && !types.includes(type) ? [type, ...types] : types).map((t) => (
              <option key={t}>{t}</option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Search</span>
          <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="name or external ID" />
        </label>
        <label className="check">
          <input type="checkbox" checked={includeMissing} onChange={(e) => setIncludeMissing(e.target.checked)} /> include missing
        </label>
        <div className="segmented" role="radiogroup" aria-label="View">
          {(["graph", "table"] as const).map((v) => (
            <button key={v} type="button" role="radio" aria-checked={view === v} className={view === v ? "on" : ""} onClick={() => setView(v)}>
              {v === "graph" ? "Graph" : "Table"}
            </button>
          ))}
        </div>
      </form>
      <ErrorBanner error={graph.error} onRetry={graph.reload} />
      {!g && graph.loading && <Loading />}
      {g && (
        <p className="muted">
          {g.nodes.length} resources, {g.edges.length} relationships
          {g.truncated && <Badge tone="warn">truncated at 300 nodes — narrow the filter</Badge>}{" "}
          {g.cycleBackEdges.acyclicTypes === 0 ? "· containment is acyclic" : <Badge tone="bad">containment cycle detected</Badge>}
          {g.cycleBackEdges.dependencyTypes > 0 && ` · ${g.cycleBackEdges.dependencyTypes} dependency cycle edge(s) (permitted)`}
        </p>
      )}
      {g && g.nodes.length === 0 && (
        <Empty>
          Nothing matches. Run discovery from <a href={href("connections")}>Connections</a>.
        </Empty>
      )}
      {g && g.nodes.length > 0 && (
        <div className={selected ? "split" : ""}>
          <Card>
            {view === "graph" ? (
              <>
                <GraphView nodes={g.nodes} edges={g.edges} selectedId={selected} onSelect={setSelected} ariaLabel="Topology graph; nodes are buttons" />
                <p className="legend muted">
                  <span className="legend-hier">━ contains / composed_of</span> <span className="legend-dep">┄ uses / depends_on / routes_to</span> ·
                  missing resources are hatched
                </p>
              </>
            ) : (
              <div className="table-wrap">
                <table>
                  <caption className="sr-only">Relationships</caption>
                  <thead>
                    <tr>
                      <th scope="col">Source</th>
                      <th scope="col">Relationship</th>
                      <th scope="col">Target</th>
                    </tr>
                  </thead>
                  <tbody>
                    {g.edges.map((e) => (
                      <tr key={e.id}>
                        <td>
                          <button type="button" className="btn-link" onClick={() => setSelected(e.sourceId)}>
                            {byId.get(e.sourceId)?.name}
                          </button>{" "}
                          <span className="muted">{byId.get(e.sourceId)?.type}</span>
                        </td>
                        <td>
                          <code>{e.type}</code>
                        </td>
                        <td>
                          <button type="button" className="btn-link" onClick={() => setSelected(e.targetId)}>
                            {byId.get(e.targetId)?.name}
                          </button>{" "}
                          <span className="muted">{byId.get(e.targetId)?.type}</span>{" "}
                          {byId.get(e.targetId)?.status === "missing" && <StatusBadge status="missing" />}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Card>
          {selected && <ResourcePanel id={selected} onClose={() => setSelected(undefined)} />}
        </div>
      )}
    </div>
  );
}
