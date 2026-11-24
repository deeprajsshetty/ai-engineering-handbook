import { useEffect, useMemo, useRef, useState } from "react";
import type { Edge, Resource } from "../api/types";
import { HIERARCHY, layeredLayout } from "../lib/layout";

const CATEGORY_CLASS: Record<string, string> = {
  network: "cat-network",
  compute: "cat-compute",
  database: "cat-data",
  cache: "cat-data",
  storage: "cat-storage",
  serverless: "cat-compute",
  k8s: "cat-k8s",
};

export function category(type: string): string {
  return CATEGORY_CLASS[type.split(".")[0] ?? ""] ?? "cat-other";
}

interface Props {
  nodes: Resource[];
  edges: Edge[];
  selectedId?: string;
  onSelect: (id: string) => void;
  ariaLabel: string;
}

/**
 * GraphView renders a layered topology diagram. Containment edges are solid
 * and define rows; dependency edges are dashed curves. Nodes are focusable
 * buttons (Enter/Space selects), and the parent page also offers a table view.
 */
export function GraphView({ nodes, edges, selectedId, onSelect, ariaLabel }: Props) {
  const [hoverId, setHoverId] = useState<string | null>(null);
  const byId = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);
  const layout = useMemo(
    () =>
      layeredLayout(
        nodes.map((n) => n.id),
        edges,
        (id) => {
          const n = byId.get(id);
          return n ? `${n.type}|${n.name}` : id;
        },
      ),
    [nodes, edges, byId],
  );
  const scrollRef = useRef<HTMLDivElement>(null);
  // Wide graphs open centred rather than scrolled to the left edge.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollLeft = Math.max(0, (el.scrollWidth - el.clientWidth) / 2);
  }, [layout.width]);
  const focus = hoverId ?? selectedId ?? null;
  const touching = useMemo(() => {
    const s = new Set<string>();
    if (!focus) return s;
    for (const e of edges) if (e.sourceId === focus || e.targetId === focus) s.add(e.id);
    return s;
  }, [edges, focus]);

  return (
    <div className="graph-scroll" ref={scrollRef}>
      <svg
        className="graph"
        width={layout.width}
        height={layout.height}
        viewBox={`0 0 ${layout.width} ${layout.height}`}
        role="group"
        aria-label={ariaLabel}
      >
        <defs>
          <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M0,0 L10,5 L0,10 z" className="graph-arrow" />
          </marker>
        </defs>
        <g>
          {edges.map((e) => {
            const a = layout.nodes.get(e.sourceId);
            const b = layout.nodes.get(e.targetId);
            if (!a || !b) return null;
            const hier = HIERARCHY.has(e.type);
            const dim = focus && !touching.has(e.id);
            const mx = (a.x + b.x) / 2 + (hier ? 0 : (b.y === a.y ? 0 : 30));
            const my = (a.y + b.y) / 2 - (b.y === a.y ? 40 : 0);
            const d = hier ? `M${a.x},${a.y + 18} L${b.x},${b.y - 22}` : `M${a.x},${a.y} Q${mx},${my} ${b.x},${b.y}`;
            return (
              <path
                key={e.id}
                d={d}
                className={`graph-edge ${hier ? "edge-hier" : "edge-dep"} ${dim ? "dim" : ""} ${touching.has(e.id) ? "hot" : ""}`}
                markerEnd="url(#arrow)"
              >
                <title>{`${byId.get(e.sourceId)?.name ?? e.sourceId} ${e.type} ${byId.get(e.targetId)?.name ?? e.targetId}`}</title>
              </path>
            );
          })}
        </g>
        <g>
          {nodes.map((n) => {
            const p = layout.nodes.get(n.id);
            if (!p) return null;
            const selected = n.id === selectedId;
            return (
              <g
                key={n.id}
                transform={`translate(${p.x},${p.y})`}
                className={`graph-node ${category(n.type)} ${selected ? "selected" : ""} ${n.status === "missing" ? "missing" : ""}`}
                tabIndex={0}
                role="button"
                aria-pressed={selected}
                aria-label={`${n.name}, ${n.type}, ${n.status}`}
                onClick={() => onSelect(n.id)}
                onKeyDown={(ev) => {
                  if (ev.key === "Enter" || ev.key === " ") {
                    ev.preventDefault();
                    onSelect(n.id);
                  }
                }}
                onMouseEnter={() => setHoverId(n.id)}
                onMouseLeave={() => setHoverId(null)}
                onFocus={() => setHoverId(n.id)}
                onBlur={() => setHoverId(null)}
              >
                <circle r={16} />
                <text y={32} textAnchor="middle" className="graph-label">
                  {n.name.length > 20 ? `${n.name.slice(0, 19)}…` : n.name}
                </text>
                <text y={44} textAnchor="middle" className="graph-sublabel">
                  {n.type}
                </text>
              </g>
            );
          })}
        </g>
      </svg>
    </div>
  );
}
