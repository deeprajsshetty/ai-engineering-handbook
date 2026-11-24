import { api, describeError } from "../api/client";
import type { Me } from "../api/types";
import { useAsync, href } from "../lib/hooks";
import { dateTime, relativeTime } from "../lib/format";
import { Card, Empty, ErrorBanner, JobStatusBadge, KeyValues, Loading } from "../components/ui";
import { useState } from "react";

export function Jobs({ id, me }: { id?: string; me: Me }) {
  return id ? <JobDetail id={id} me={me} /> : <JobList />;
}

function JobList() {
  const jobs = useAsync(() => api.jobs({ limit: 50 }), [], 3000);
  const conns = useAsync(() => api.connections(), []);
  const name = (cid: string) => conns.data?.items.find((c) => c.id === cid)?.name ?? cid;
  return (
    <div className="stack">
      <h1>Discovery jobs</h1>
      <ErrorBanner error={jobs.error} onRetry={jobs.reload} />
      {!jobs.data && <Loading />}
      {jobs.data && jobs.data.items.length === 0 && <Empty>No jobs yet.</Empty>}
      {jobs.data && jobs.data.items.length > 0 && (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th scope="col">Job</th>
                <th scope="col">Connection</th>
                <th scope="col">Status</th>
                <th scope="col">Phase</th>
                <th scope="col" className="num">
                  Resources
                </th>
                <th scope="col">Added / changed / missing / back</th>
                <th scope="col">Created</th>
              </tr>
            </thead>
            <tbody>
              {jobs.data.items.map((j) => (
                <tr key={j.id}>
                  <td>
                    <a href={href("jobs", j.id)}>
                      <code>{j.id.slice(0, 16)}…</code>
                    </a>
                  </td>
                  <td>{name(j.connectionId)}</td>
                  <td>
                    <JobStatusBadge status={j.status} />
                  </td>
                  <td>{j.progress.phase}</td>
                  <td className="num">{j.progress.resources}</td>
                  <td>
                    +{j.counts.added} / ~{j.counts.changed} / −{j.counts.missing} / ↺{j.counts.reactivated}
                  </td>
                  <td title={dateTime(j.createdAt)}>{relativeTime(j.createdAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function JobDetail({ id, me }: { id: string; me: Me }) {
  const job = useAsync(() => api.job(id), [id], 1500);
  const running = job.data && (job.data.status === "queued" || job.data.status === "running");
  const events = useAsync(() => api.jobEvents(id), [id], running ? 1500 : undefined);
  const changes = useAsync(() => api.changes({ jobId: id, limit: 200 }), [id, job.data?.status]);
  const [cancelErr, setCancelErr] = useState<string>();
  const j = job.data;

  return (
    <div className="stack">
      <p>
        <a href={href("jobs")}>← Jobs</a>
      </p>
      <h1>
        Discovery job {j && <JobStatusBadge status={j.status} />}
      </h1>
      <ErrorBanner error={job.error} onRetry={job.reload} />
      {!j && <Loading />}
      {j && (
        <div className="grid-2">
          <Card
            title="Progress"
            actions={
              running && me.permissions.includes("discovery:run") ? (
                <button
                  type="button"
                  className="btn"
                  onClick={() =>
                    api
                      .cancelJob(id)
                      .then(job.reload)
                      .catch((e: unknown) => setCancelErr(describeError(e)))
                  }
                >
                  {j.cancelRequested ? "Cancelling…" : "Cancel"}
                </button>
              ) : undefined
            }
          >
            {cancelErr && <p className="banner banner-bad">{cancelErr}</p>}
            {running && (
              <progress aria-label="Discovery in progress" className="progress">
                {j.progress.pages} pages
              </progress>
            )}
            <KeyValues
              entries={[
                ["ID", <code key="id">{j.id}</code>],
                ["Connection", <a key="c" href={href("topology", undefined, { connectionId: j.connectionId })}>{j.connectionId}</a>],
                ["Phase", j.progress.phase],
                ["Pages", j.progress.pages],
                ["Resources seen", j.progress.resources],
                ["Relationships seen", j.progress.relationships],
                ["Warnings", j.progress.warnings],
                ["Attempt", j.attempt],
                ["Trigger", `${j.trigger} by ${j.requestedBy}`],
                ["Complete snapshot", j.complete ? "yes" : "no (nothing is marked missing)"],
                ["Created", dateTime(j.createdAt)],
                ["Finished", dateTime(j.finishedAt)],
                ...(j.error ? ([["Error", <span key="e" className="text-bad">{j.error}</span>]] as [string, React.ReactNode][]) : []),
              ]}
            />
          </Card>
          <Card title="Reconciliation">
            <KeyValues
              entries={[
                ["Added", j.counts.added],
                ["Changed", j.counts.changed],
                ["Unchanged", j.counts.unchanged],
                ["Reactivated", j.counts.reactivated],
                ["Marked missing", j.counts.missing],
                ["Edges added", j.counts.edgesAdded],
                ["Edges marked missing", j.counts.edgesMissing],
              ]}
            />
            {changes.data && changes.data.items.length > 0 && (
              <ul className="list compact">
                {changes.data.items.map((c) => (
                  <li key={c.id}>
                    <span className={`change change-${c.kind}`}>{c.kind}</span>{" "}
                    <a href={href("resources", c.resourceId)}>{c.name || c.resourceId}</a> <span className="muted">{c.type}</span>
                  </li>
                ))}
              </ul>
            )}
          </Card>
          <Card title="Events">
            {events.data && events.data.items.length > 0 ? (
              <ol className="events">
                {events.data.items.map((e) => (
                  <li key={e.seq} className={`event-${e.level}`}>
                    <time dateTime={e.at}>{new Date(e.at).toLocaleTimeString()}</time> {e.message}
                    {e.details && <code className="event-details">{JSON.stringify(e.details)}</code>}
                  </li>
                ))}
              </ol>
            ) : (
              <Empty>No events yet.</Empty>
            )}
          </Card>
        </div>
      )}
    </div>
  );
}
