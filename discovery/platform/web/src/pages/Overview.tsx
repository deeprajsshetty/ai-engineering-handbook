import { api } from "../api/client";
import { useAsync, href } from "../lib/hooks";
import { relativeTime } from "../lib/format";
import { useApp } from "../lib/context";
import { Card, Empty, ErrorBanner, JobStatusBadge, Loading, SimulatedBadge, Stat } from "../components/ui";
import { Icon, TypeIcon } from "../components/Icon";

export function Overview() {
  const { domainName, catalog, typeInfo } = useApp();
  const facets = useAsync(() => api.facets({ status: "active" }), [], 15000);
  const summary = useAsync(() => api.summary(), [], 10000);
  const conns = useAsync(() => api.connections(), [], 10000);
  const jobs = useAsync(() => api.jobs({ limit: 5 }), [], 5000);
  const s = summary.data;
  const anySimulated = conns.data?.items.some((c) => c.simulated);

  return (
    <div className="stack">
      <h1>Overview</h1>
      {anySimulated && (
        <p className="banner banner-sim">
          <SimulatedBadge /> Some connections use the fixture provider. Their resources and metrics are generated locally and are not
          real measurements.
        </p>
      )}
      <ErrorBanner error={summary.error ?? conns.error} onRetry={summary.reload} />
      {!s && summary.loading && <Loading />}
      {s && (
        <div className="stats">
          <Stat label="Resources" value={s.activeResources} hint={`${s.missingResources} missing`} />
          <Stat label="Relationships" value={s.relationships} />
          <Stat label="Resource types" value={s.byType.length} />
          <Stat label="Last observation" value={relativeTime(s.lastSeenAt)} />
        </div>
      )}
      {facets.data && facets.data.facets.domain && facets.data.facets.domain.length > 0 && (
        <nav className="domain-tiles" aria-label="Domains">
          {facets.data.facets.domain.map((d) => (
            <a key={d.value} className={`domain-tile dom-${d.value}`} href={href("resources", undefined, { domain: d.value })}>
              <span className="type-icon">
                <Icon name={d.value} size={22} />
              </span>
              <span className="domain-count">{d.count}</span>
              <span className="domain-name">{domainName(d.value)}</span>
              <span className="muted small">{catalog?.domains.find((x) => x.name === d.value)?.description}</span>
            </a>
          ))}
        </nav>
      )}
      {s && s.resources === 0 && (
        <Empty>
          No topology yet. Go to <a href={href("connections")}>Connections</a> and run discovery.
        </Empty>
      )}
      <div className="grid-2">
        <Card title="Resources by type">
          {s && s.byType.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Type</th>
                  <th scope="col" className="num">
                    Active
                  </th>
                </tr>
              </thead>
              <tbody>
                {s.byType.map((t) => (
                  <tr key={t.key}>
                    <td>
                      <span className="cell-name">
                        <TypeIcon type={t.key} size={16} />
                        <a href={href("resources", undefined, { type: t.key })}>{typeInfo(t.key).displayName}</a>
                        <code className="muted small">{t.key}</code>
                      </span>
                    </td>
                    <td className="num">{t.count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <Empty>No resources.</Empty>
          )}
        </Card>
        <Card title="Recent discovery jobs" actions={<a href={href("jobs")}>All jobs</a>}>
          {jobs.data && jobs.data.items.length > 0 ? (
            <ul className="list">
              {jobs.data.items.map((j) => {
                const c = conns.data?.items.find((x) => x.id === j.connectionId);
                return (
                  <li key={j.id}>
                    <a href={href("jobs", j.id)}>{c?.name ?? j.connectionId}</a> <JobStatusBadge status={j.status} />{" "}
                    <span className="muted">
                      {relativeTime(j.createdAt)} · +{j.counts.added} ~{j.counts.changed} −{j.counts.missing}
                    </span>
                  </li>
                );
              })}
            </ul>
          ) : (
            <Empty>No jobs yet.</Empty>
          )}
        </Card>
        <Card title="Relationships by type">
          {s && s.relationshipTypes.length > 0 ? (
            <table>
              <tbody>
                {s.relationshipTypes.map((t) => (
                  <tr key={t.key}>
                    <td>
                      <code>{t.key}</code>
                    </td>
                    <td className="num">{t.count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <Empty>No relationships.</Empty>
          )}
        </Card>
        <Card title="Regions and providers">
          {s && s.byRegion.length > 0 ? (
            <table>
              <tbody>
                {s.byProvider.map((t) => (
                  <tr key={`p-${t.key}`}>
                    <td>provider {t.key}</td>
                    <td className="num">{t.count}</td>
                  </tr>
                ))}
                {s.byRegion.map((t) => (
                  <tr key={`r-${t.key}`}>
                    <td>region {t.key}</td>
                    <td className="num">{t.count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <Empty>None.</Empty>
          )}
        </Card>
      </div>
    </div>
  );
}
