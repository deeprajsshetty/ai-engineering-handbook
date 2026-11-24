-- 0002_metrics: time-series samples and collection runs.
-- Samples are keyed by (resource, metric, timestamp) so repeated collection
-- of the same aligned timestamp is an idempotent no-op (ON CONFLICT DO NOTHING).

CREATE TABLE metric_samples (
    resource_id  text             NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    metric       text             NOT NULL,
    ts           timestamptz      NOT NULL,
    tenant_id    text             NOT NULL,
    value        double precision NOT NULL,
    source       text             NOT NULL CHECK (source IN ('simulated', 'measured')),
    collector    text             NOT NULL,
    PRIMARY KEY (resource_id, metric, ts)
);
-- Range scans per series use the primary key; retention deletes use ts.
CREATE INDEX metric_samples_ts_idx ON metric_samples (ts);

CREATE TABLE metric_collections (
    id             text        PRIMARY KEY,
    tenant_id      text        NOT NULL,
    connection_id  text        NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    trigger        text        NOT NULL CHECK (trigger IN ('manual', 'schedule', 'discovery')),
    status         text        NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    requested_by   text        NOT NULL,
    collector      text        NOT NULL,
    resources      integer     NOT NULL DEFAULT 0,
    samples        integer     NOT NULL DEFAULT 0,
    error          text        NOT NULL DEFAULT '',
    started_at     timestamptz NOT NULL,
    finished_at    timestamptz
);
CREATE INDEX metric_collections_tenant_idx ON metric_collections (tenant_id, started_at DESC, id DESC);
CREATE INDEX metric_collections_started_idx ON metric_collections (started_at);
