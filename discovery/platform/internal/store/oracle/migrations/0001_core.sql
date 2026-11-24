-- 0001_core (Oracle 19c+): connection profiles, discovery jobs, topology, audit.
-- Same logical schema as store/postgres/migrations/0001_core.sql, with these
-- deliberate differences:
--   * Oracle stores '' as NULL, so optional text columns are nullable and the
--     Go code maps NULL <-> "".
--   * JSON documents are CLOB columns constrained IS JSON (portable to 19c).
--   * Labels are also kept in resource_labels so label filters and facets
--     are plain indexed joins (no dynamic JSON paths).
--   * Partial indexes become function-based indexes (NULL keys are not indexed).
--   * Timestamps are TIMESTAMP(6) holding UTC by convention.
-- Statements are separated by a line containing only ';'.

CREATE TABLE connections (
    id                          VARCHAR2(64)   PRIMARY KEY,
    tenant_id                   VARCHAR2(128)  NOT NULL,
    name                        VARCHAR2(128)  NOT NULL,
    provider                    VARCHAR2(64)   NOT NULL,
    config                      CLOB           CHECK (config IS JSON),
    credential_ref              VARCHAR2(512),
    discovery_interval_minutes  NUMBER(10)     DEFAULT 0 NOT NULL
                                               CHECK (discovery_interval_minutes BETWEEN 0 AND 10080),
    created_by                  VARCHAR2(256)  NOT NULL,
    created_at                  TIMESTAMP(6)   NOT NULL,
    updated_at                  TIMESTAMP(6)   NOT NULL,
    CONSTRAINT connections_tenant_name_uq UNIQUE (tenant_id, name)
)
;
CREATE INDEX connections_tenant_id_idx ON connections (tenant_id, id)
;
CREATE TABLE discovery_jobs (
    id                  VARCHAR2(64)    PRIMARY KEY,
    tenant_id           VARCHAR2(128)   NOT NULL,
    connection_id       VARCHAR2(64)    NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    status              VARCHAR2(16)    NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    trigger_kind        VARCHAR2(16)    NOT NULL CHECK (trigger_kind IN ('manual', 'schedule')),
    requested_by        VARCHAR2(256)   NOT NULL,
    request_id          VARCHAR2(128),
    idempotency_key     VARCHAR2(256),
    attempt             NUMBER(10)      DEFAULT 0 NOT NULL,
    cancel_requested    NUMBER(1)       DEFAULT 0 NOT NULL CHECK (cancel_requested IN (0, 1)),
    phase               VARCHAR2(32)    DEFAULT 'pending' NOT NULL,
    pages               NUMBER(10)      DEFAULT 0 NOT NULL,
    resources_seen      NUMBER(10)      DEFAULT 0 NOT NULL,
    relationships_seen  NUMBER(10)      DEFAULT 0 NOT NULL,
    warnings            NUMBER(10)      DEFAULT 0 NOT NULL,
    counts              CLOB            CHECK (counts IS JSON),
    complete            NUMBER(1)       DEFAULT 0 NOT NULL CHECK (complete IN (0, 1)),
    error               VARCHAR2(2000),
    worker_id           VARCHAR2(128),
    created_at          TIMESTAMP(6)    NOT NULL,
    started_at          TIMESTAMP(6),
    finished_at         TIMESTAMP(6),
    heartbeat_at        TIMESTAMP(6)
)
;
-- At most one queued or running job per connection (concurrency invariant).
CREATE UNIQUE INDEX discovery_jobs_one_active_uq ON discovery_jobs (CASE WHEN status IN ('queued', 'running') THEN connection_id END)
;
-- Idempotency-Key replay returns the original job.
CREATE UNIQUE INDEX discovery_jobs_idempotency_uq ON discovery_jobs (CASE WHEN idempotency_key IS NOT NULL THEN tenant_id END, idempotency_key)
;
CREATE INDEX discovery_jobs_tenant_created_idx ON discovery_jobs (tenant_id, created_at DESC, id DESC)
;
CREATE INDEX discovery_jobs_queue_idx ON discovery_jobs (status, created_at, id)
;
CREATE INDEX discovery_jobs_conn_idx ON discovery_jobs (connection_id, created_at, id)
;
CREATE TABLE job_events (
    job_id     VARCHAR2(64)    NOT NULL REFERENCES discovery_jobs (id) ON DELETE CASCADE,
    seq        NUMBER(10)      NOT NULL,
    tenant_id  VARCHAR2(128)   NOT NULL,
    at         TIMESTAMP(6)    NOT NULL,
    lvl        VARCHAR2(8)     NOT NULL CHECK (lvl IN ('info', 'warn', 'error')),
    message    VARCHAR2(1000)  NOT NULL,
    details    CLOB            CHECK (details IS JSON),
    CONSTRAINT job_events_pk PRIMARY KEY (job_id, seq)
)
;
CREATE TABLE resources (
    id             VARCHAR2(64)    PRIMARY KEY,
    tenant_id      VARCHAR2(128)   NOT NULL,
    connection_id  VARCHAR2(64)    NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    provider       VARCHAR2(64)    NOT NULL,
    scope          VARCHAR2(512)   NOT NULL,
    external_id    VARCHAR2(1024)  NOT NULL,
    type           VARCHAR2(128)   NOT NULL,
    native_type    VARCHAR2(256),
    name           VARCHAR2(512)   NOT NULL,
    region         VARCHAR2(64)    NOT NULL,
    labels         CLOB            CHECK (labels IS JSON),
    properties     CLOB            CHECK (properties IS JSON),
    content_hash   VARCHAR2(128)   NOT NULL,
    status         VARCHAR2(16)    NOT NULL CHECK (status IN ('active', 'missing')),
    first_seen_at  TIMESTAMP(6)    NOT NULL,
    last_seen_at   TIMESTAMP(6)    NOT NULL,
    missing_since  TIMESTAMP(6),
    last_job_id    VARCHAR2(64)    NOT NULL,
    CONSTRAINT resources_natural_key_uq UNIQUE (tenant_id, provider, scope, external_id),
    CONSTRAINT resources_missing_since_ck CHECK ((status = 'missing' AND missing_since IS NOT NULL) OR (status = 'active' AND missing_since IS NULL))
)
;
CREATE INDEX resources_tenant_name_idx ON resources (tenant_id, name, id)
;
CREATE INDEX resources_tenant_conn_status_idx ON resources (tenant_id, connection_id, status)
;
CREATE INDEX resources_tenant_type_idx ON resources (tenant_id, type)
;
CREATE INDEX resources_missing_idx ON resources (CASE WHEN status = 'missing' THEN missing_since END)
;
CREATE TABLE resource_labels (
    resource_id  VARCHAR2(64)   NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    tenant_id    VARCHAR2(128)  NOT NULL,
    k            VARCHAR2(128)  NOT NULL,
    v            VARCHAR2(256),
    CONSTRAINT resource_labels_pk PRIMARY KEY (resource_id, k)
)
;
CREATE INDEX resource_labels_kv_idx ON resource_labels (tenant_id, k, v, resource_id)
;
CREATE TABLE relationships (
    id             VARCHAR2(64)  PRIMARY KEY,
    tenant_id      VARCHAR2(128) NOT NULL,
    connection_id  VARCHAR2(64)  NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    source_id      VARCHAR2(64)  NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    target_id      VARCHAR2(64)  NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    type           VARCHAR2(32)  NOT NULL CHECK (type IN ('contains', 'composed_of', 'member_of', 'uses', 'depends_on', 'routes_to', 'connects_to')),
    status         VARCHAR2(16)  NOT NULL CHECK (status IN ('active', 'missing')),
    first_seen_at  TIMESTAMP(6)  NOT NULL,
    last_seen_at   TIMESTAMP(6)  NOT NULL,
    missing_since  TIMESTAMP(6),
    last_job_id    VARCHAR2(64)  NOT NULL,
    CONSTRAINT relationships_natural_key_uq UNIQUE (tenant_id, source_id, type, target_id),
    CONSTRAINT relationships_no_self_ck CHECK (source_id <> target_id),
    CONSTRAINT relationships_missing_since_ck CHECK ((status = 'missing' AND missing_since IS NOT NULL) OR (status = 'active' AND missing_since IS NULL))
)
;
CREATE INDEX relationships_source_idx ON relationships (source_id)
;
CREATE INDEX relationships_target_idx ON relationships (target_id)
;
CREATE INDEX relationships_tenant_conn_status_idx ON relationships (tenant_id, connection_id, status)
;
CREATE INDEX relationships_tenant_type_idx ON relationships (tenant_id, type, id)
;
CREATE TABLE topology_changes (
    id           NUMBER(19)     GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    VARCHAR2(128)  NOT NULL,
    job_id       VARCHAR2(64)   NOT NULL,
    resource_id  VARCHAR2(64)   NOT NULL,
    kind         VARCHAR2(16)   NOT NULL CHECK (kind IN ('added', 'changed', 'reactivated', 'missing')),
    at           TIMESTAMP(6)   NOT NULL
)
;
CREATE INDEX topology_changes_job_idx ON topology_changes (tenant_id, job_id, id)
;
CREATE INDEX topology_changes_resource_idx ON topology_changes (tenant_id, resource_id, id)
;
CREATE INDEX topology_changes_at_idx ON topology_changes (at)
;
CREATE TABLE audit_events (
    id           VARCHAR2(64)   PRIMARY KEY,
    tenant_id    VARCHAR2(128)  NOT NULL,
    actor        VARCHAR2(256)  NOT NULL,
    action       VARCHAR2(128)  NOT NULL,
    target_type  VARCHAR2(64)   NOT NULL,
    target_id    VARCHAR2(128),
    request_id   VARCHAR2(128),
    at           TIMESTAMP(6)   NOT NULL,
    details      CLOB           CHECK (details IS JSON)
)
;
CREATE INDEX audit_events_tenant_at_idx ON audit_events (tenant_id, at DESC, id DESC)
;
CREATE INDEX audit_events_at_idx ON audit_events (at)
;
