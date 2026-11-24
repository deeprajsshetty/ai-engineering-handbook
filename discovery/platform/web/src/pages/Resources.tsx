import { useEffect, useState } from "react";
import { api, describeError } from "../api/client";
import type { Resource } from "../api/types";
import { href, useAsync } from "../lib/hooks";
import { relativeTime } from "../lib/format";
import { useApp } from "../lib/context";
import { Empty, FreshnessBadge, Loading, StatusBadge } from "../components/ui";
import { Icon, TypeIcon } from "../components/Icon";

// Facets shown in the sidebar, in order. engine/env are label facets.
const FACETS: { key: string; label: string }[] = [
  { key: "domain", label: "Domain" },
  { key: "type", label: "Type" },
  { key: "engine", label: "Engine" },
  { key: "provider", label: "Provider" },
  { key: "region", label: "Region" },
  { key: "env", label: "Environment" },
  { key: "status", label: "Status" },
];

type Filters = Record<string, string>;

const LABEL_FACETS = new Set(["engine", "env"]);

/** toQuery maps UI filters to API parameters (label facets use label=k=v). */
function toQuery(f: Filters): Record<string, string | undefined> {
  const q: Record<string, string | undefined> = {};
  for (const [k, v] of Object.entries(f)) {
    if (!v) continue;
    if (LABEL_FACETS.has(k)) q.label = `${k}=${v}`;
    else if (k !== "label") q[k] = v;
  }
  if (f.label && !q.label) q.label = f.label;
  return q;
}

function navigate(next: Filters) {
  const clean = Object.fromEntries(Object.entries(next).filter(([, v]) => v));
  window.location.assign(href("resources", undefined, clean));
}

/**
 * Resource explorer: faceted search over every discovered resource, in the
 * style of entity explorers in commercial observability tools. Filters live
 * in the URL, so a view can be bookmarked and shared.
 */
export function Resources({ params }: { params: URLSearchParams }) {
  const { typeInfo, domainName } = useApp();
  const filters: Filters = {};
  for (const k of ["q", "label", ...FACETS.map((f) => f.key)]) {
    const v = params.get(k);
    if (v) filters[k] = v;
  }
  const filterKey = JSON.stringify(filters);
  const [q, setQ] = useState(filters.q ?? "");
  const query = toQuery(filters);
  // First page and facets follow the URL; "Load more" pages are appended in
  // state tagged with the filter key, so a filter change drops them.
  const first = useAsync(() => Promise.all([api.resources({ ...query, limit: 50 }), api.facets(query)]), [filterKey]);
  const [more, setMore] = useState<{ key: string; rows: Resource[]; cursor?: string }>();
  const [moreError, setMoreError] = useState<string>();
  const [loadingMore, setLoadingMore] = useState(false);
  const extra = more?.key === filterKey ? more : undefined;
  const rows = [...(first.data?.[0].items ?? []), ...(extra?.rows ?? [])];
  const cursor = extra ? extra.cursor : first.data?.[0].nextCursor;
  const facets = first.data?.[1].facets ?? {};
  const loading = first.loading || loadingMore;
  const error = first.error ? describeError(first.error) : moreError;

  function toggle(key: string, value: string) {
    const next = { ...filters };
    if (next[key] === value) delete next[key];
    else next[key] = value;
    if (key === "domain") delete next.type; // a type belongs to one domain
    if (LABEL_FACETS.has(key)) for (const other of LABEL_FACETS) if (other !== key) delete next[other]; // API takes one label filter
    navigate(next);
  }

  // Debounce free-text search into the URL.
  useEffect(() => {
    if (q === (filters.q ?? "")) return;
    const t = setTimeout(() => navigate({ ...filters, q }), 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q]);

  async function loadMore() {
    if (!cursor) return;
    setLoadingMore(true);
    setMoreError(undefined);
    try {
      const page = await api.resources({ ...query, limit: 50, cursor });
      setMore({ key: filterKey, rows: [...(extra?.rows ?? []), ...page.items], cursor: page.nextCursor });
    } catch (e) {
      setMoreError(describeError(e));
    } finally {
      setLoadingMore(false);
    }
  }

  const facetLabel = (key: string, value: string) => {
    if (key === "domain") return domainName(value);
    if (key === "type") return typeInfo(value).displayName;
    return value;
  };
  const active = Object.entries(filters).filter(([k]) => k !== "q");

  return (
    <div className="stack">
      <div className="page-head">
        <h1>Explorer</h1>
        <span className="muted">Every discovered resource, across providers and domains</span>
      </div>
      <div className="explorer">
        <aside className="facets" aria-label="Filters">
          {FACETS.map((f) => {
            const values = facets[f.key] ?? [];
            if (values.length === 0 && !filters[f.key]) return null;
            return (
              <fieldset key={f.key} className="facet">
                <legend>{f.label}</legend>
                {values.slice(0, 12).map((v) => (
                  <label key={v.value} className="facet-row">
                    <input type="checkbox" checked={filters[f.key] === v.value} onChange={() => toggle(f.key, v.value)} />
                    {f.key === "type" && <TypeIcon type={v.value} size={14} />}
                    <span className="facet-value">{facetLabel(f.key, v.value)}</span>
                    <span className="facet-count">{v.count}</span>
                  </label>
                ))}
              </fieldset>
            );
          })}
        </aside>
        <section className="stack explorer-main">
          <form className="searchbar" role="search" onSubmit={(e) => e.preventDefault()}>
            <Icon name="search" />
            <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search by name or external ID" aria-label="Search resources" />
          </form>
          {active.length > 0 && (
            <div className="chips" aria-label="Active filters">
              {active.map(([k, v]) => (
                <button key={k} type="button" className="chip" onClick={() => toggle(k, v)} aria-label={`Remove filter ${k} ${v}`}>
                  <span className="muted">{k}:</span> {facetLabel(k, v)} <Icon name="close" size={12} />
                </button>
              ))}
              <button type="button" className="btn-link" onClick={() => navigate({})}>
                Clear all
              </button>
            </div>
          )}
          {error && (
            <p role="alert" className="banner banner-bad">
              {error}
            </p>
          )}
          {rows.length === 0 && loading && <Loading />}
          {rows.length === 0 && !loading && !error && <Empty>No resources match these filters.</Empty>}
          {rows.length > 0 && (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Name</th>
                    <th scope="col">Type</th>
                    <th scope="col">Region</th>
                    <th scope="col">Provider</th>
                    <th scope="col">Status</th>
                    <th scope="col">Last seen</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <tr key={r.id}>
                      <td>
                        <span className="cell-name">
                          <TypeIcon type={r.type} />
                          <a href={href("resources", r.id)}>{r.name}</a>
                          {r.labels.engine && <span className="label">{r.labels.engine}</span>}
                        </span>
                      </td>
                      <td title={r.type}>{typeInfo(r.type).displayName}</td>
                      <td>{r.region}</td>
                      <td>{r.provider}</td>
                      <td>
                        <StatusBadge status={r.status} /> <FreshnessBadge freshness={r.freshness} />
                      </td>
                      <td>{relativeTime(r.lastSeenAt)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {cursor && (
            <button type="button" className="btn" onClick={() => void loadMore()} disabled={loading}>
              {loading ? "Loading…" : "Load more"}
            </button>
          )}
        </section>
      </div>
    </div>
  );
}
