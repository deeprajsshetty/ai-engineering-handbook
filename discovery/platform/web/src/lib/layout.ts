import type { Edge, RelationType } from "../api/types";

/** Hierarchical relationship types; they form a DAG and define layers. */
export const HIERARCHY: ReadonlySet<RelationType> = new Set<RelationType>(["contains", "composed_of"]);

export interface LayoutNode {
  id: string;
  layer: number;
  x: number;
  y: number;
}

export interface Layout {
  nodes: Map<string, LayoutNode>;
  width: number;
  height: number;
  layers: number;
}

/**
 * layeredLayout places nodes in rows by their depth in the containment
 * hierarchy (longest path from a root over contains/composed_of edges),
 * computed with Kahn's topological order in O(V + E). The server guarantees
 * the hierarchy is acyclic; any node left over (defensive) goes to the last
 * row. Within a row nodes are ordered by the given sort key, so the picture
 * is stable between refreshes.
 */
export function layeredLayout(
  ids: string[],
  edges: Edge[],
  sortKey: (id: string) => string,
  opts: { colWidth?: number; rowHeight?: number; margin?: number } = {},
): Layout {
  const colWidth = opts.colWidth ?? 150;
  const rowHeight = opts.rowHeight ?? 110;
  const margin = opts.margin ?? 70;
  const present = new Set(ids);
  const indeg = new Map<string, number>(ids.map((id) => [id, 0]));
  const out = new Map<string, string[]>();
  for (const e of edges) {
    if (!HIERARCHY.has(e.type) || !present.has(e.sourceId) || !present.has(e.targetId)) continue;
    indeg.set(e.targetId, (indeg.get(e.targetId) ?? 0) + 1);
    const l = out.get(e.sourceId) ?? [];
    l.push(e.targetId);
    out.set(e.sourceId, l);
  }
  const depth = new Map<string, number>();
  const queue = ids.filter((id) => (indeg.get(id) ?? 0) === 0);
  for (const id of queue) depth.set(id, 0);
  for (let i = 0; i < queue.length; i++) {
    const u = queue[i]!;
    for (const v of out.get(u) ?? []) {
      depth.set(v, Math.max(depth.get(v) ?? 0, (depth.get(u) ?? 0) + 1));
      const d = (indeg.get(v) ?? 0) - 1;
      indeg.set(v, d);
      if (d === 0) queue.push(v);
    }
  }
  let maxDepth = 0;
  for (const d of depth.values()) maxDepth = Math.max(maxDepth, d);
  const rows: string[][] = [];
  for (const id of ids) {
    const d = depth.has(id) ? depth.get(id)! : maxDepth + 1;
    (rows[d] ??= []).push(id);
  }
  const compact = rows.filter((r) => r && r.length > 0);
  const widest = Math.max(1, ...compact.map((r) => r.length));
  const width = margin * 2 + (widest - 1) * colWidth;
  const nodes = new Map<string, LayoutNode>();
  compact.forEach((row, layer) => {
    row.sort((a, b) => sortKey(a).localeCompare(sortKey(b)));
    const offset = ((widest - row.length) * colWidth) / 2;
    row.forEach((id, i) => {
      nodes.set(id, { id, layer, x: margin + offset + i * colWidth, y: margin + layer * rowHeight });
    });
  });
  return { nodes, width, height: margin * 2 + Math.max(0, compact.length - 1) * rowHeight, layers: compact.length };
}
