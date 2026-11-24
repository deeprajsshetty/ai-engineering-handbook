-- 0002_metrics (Oracle 19c+): time-series samples and collection runs.
-- Samples are keyed by (resource, metric, timestamp); MERGE ... WHEN NOT
-- MATCHED makes repeated collection of the same timestamp a no-op.

CREATE TABLE metric_samples (
    resource_id  VARCHAR2(64)    NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    metric       VARCHAR2(128)   NOT NULL,
    ts           TIMESTAMP(6)    NOT NULL,
    tenant_id    VARCHAR2(128)   NOT NULL,
    value        BINARY_DOUBLE   NOT NULL,
    source       VARCHAR2(16)    NOT NULL CHECK (source IN ('simulated', 'measured')),
    collector    VARCHAR2(128)   NOT NULL,
    CONSTRAINT metric_samples_pk PRIMARY KEY (resource_id, metric, ts)
)
;
CREATE INDEX metric_samples_ts_idx ON metric_samples (ts)
;
CREATE TABLE metric_collections (
    id             VARCHAR2(64)    PRIMARY KEY,
    tenant_id      VARCHAR2(128)   NOT NULL,
    connection_id  VARCHAR2(64)    NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    trigger_kind   VARCHAR2(16)    NOT NULL CHECK (trigger_kind IN ('manual', 'schedule', 'discovery')),
    status         VARCHAR2(16)    NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    requested_by   VARCHAR2(256)   NOT NULL,
    collector      VARCHAR2(128)   NOT NULL,
    resources      NUMBER(10)      DEFAULT 0 NOT NULL,
    samples        NUMBER(10)      DEFAULT 0 NOT NULL,
    error          VARCHAR2(2000),
    started_at     TIMESTAMP(6)    NOT NULL,
    finished_at    TIMESTAMP(6)
)
;
CREATE INDEX metric_collections_tenant_idx ON metric_collections (tenant_id, id)
;
CREATE INDEX metric_collections_started_idx ON metric_collections (started_at)
;
