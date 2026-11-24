-- 0001_core: connection profiles, discovery jobs, topology and audit.
-- Forward-only. All timestamps are timestamptz (UTC on the wire).
-- Every table carries tenant_id and every query filters by it.

CREATE TABLE connections (
    id                          text        PRIMARY KEY,
    tenant_id                   text        NOT NULL,
    name                        text        NOT NULL,
    provider                    text        NOT NULL,
    config                      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    credential_ref              text        NOT NULL DEFAULT '',  -- reference only, never a secret value
    discovery_interval_minutes  integer     NOT NULL DEFAULT 0
                                            CHECK (discovery_interval_minutes BETWEEN 0 AND 10080),
    created_by                  text        NOT NULL,
    created_at                  timestamptz NOT NULL,
    updated_at                  timestamptz NOT NULL,
    CONSTRAINT connections_tenant_name_uq UNIQUE (tenant_id, name)
);
CREATE INDEX connections_tenant_id_idx ON connections (tenant_id, id);

CREATE TABLE discovery_jobs (
    id                  text        PRIMARY KEY,
    tenant_id           text        NOT NULL,
    connection_id       text        NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    status              text        NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    trigger             text        NOT NULL CHECK (trigger IN ('manual', 'schedule')),
    requested_by        text        NOT NULL,
    request_id          text        NOT NULL DEFAULT '',
    idempotency_key     text,
    attempt             integer     NOT NULL DEFAULT 0,
    cancel_requested    boolean     NOT NULL DEFAULT false,
    phase               text        NOT NULL DEFAULT 'pending',
    pages               integer     NOT NULL DEFAULT 0,
    resources_seen      integer     NOT NULL DEFAULT 0,
    relationships_seen  integer     NOT NULL DEFAULT 0,
    warnings            integer     NOT NULL DEFAULT 0,
    counts              jsonb       NOT NULL DEFAULT '{}'::jsonb,
    complete            boolean     NOT NULL DEFAULT false,
    error               text        NOT NULL DEFAULT '',
    worker_id           text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL,
    started_at          timestamptz,
    finished_at         timestamptz,
    heartbeat_at        timestamptz
);
-- At most one queued or running job per connection (concurrency invariant).
CREATE UNIQUE INDEX discovery_jobs_one_active_uq ON discovery_jobs (connection_id)
    WHERE status IN ('queued', 'running');
-- Idempotency-Key replay returns the original job.
CREATE UNIQUE INDEX discovery_jobs_idempotency_uq ON discovery_jobs (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX discovery_jobs_tenant_created_idx ON discovery_jobs (tenant_id, created_at DESC, id DESC);
CREATE INDEX discovery_jobs_queue_idx ON discovery_jobs (created_at) WHERE status = 'queued';
CREATE INDEX discovery_jobs_lease_idx ON discovery_jobs (heartbeat_at) WHERE status = 'running';
CREATE INDEX discovery_jobs_finished_idx ON discovery_jobs (finished_at) WHERE finished_at IS NOT NULL;

CREATE TABLE job_events (
    job_id     text        NOT NULL REFERENCES discovery_jobs (id) ON DELETE CASCADE,
    seq        integer     NOT NULL,
    tenant_id  text        NOT NULL,
    at         timestamptz NOT NULL,
    level      text        NOT NULL CHECK (level IN ('info', 'warn', 'error')),
    message    text        NOT NULL,
    details    jsonb,
    PRIMARY KEY (job_id, seq)
);

CREATE TABLE resources (
    id             text        PRIMARY KEY,              -- deterministic hash of (tenant, provider, scope, external_id)
    tenant_id      text        NOT NULL,
    connection_id  text        NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    provider       text        NOT NULL,
    scope          text        NOT NULL,
    external_id    text        NOT NULL,
    type           text        NOT NULL,
    native_type    text        NOT NULL DEFAULT '',
    name           text        NOT NULL,
    region         text        NOT NULL,
    labels         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    properties     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    content_hash   text        NOT NULL,
    status         text        NOT NULL CHECK (status IN ('active', 'missing')),
    first_seen_at  timestamptz NOT NULL,
    last_seen_at   timestamptz NOT NULL,
    missing_since  timestamptz,
    last_job_id    text        NOT NULL,
    CONSTRAINT resources_natural_key_uq UNIQUE (tenant_id, provider, scope, external_id),
    CONSTRAINT resources_missing_since_ck CHECK ((status = 'missing') = (missing_since IS NOT NULL))
);
CREATE INDEX resources_tenant_name_idx ON resources (tenant_id, name, id);
CREATE INDEX resources_tenant_conn_status_idx ON resources (tenant_id, connection_id, status);
CREATE INDEX resources_tenant_type_idx ON resources (tenant_id, type);
CREATE INDEX resources_labels_gin ON resources USING gin (labels jsonb_path_ops);
CREATE INDEX resources_missing_idx ON resources (missing_since) WHERE status = 'missing';

CREATE TABLE relationships (
    id             text        PRIMARY KEY,              -- deterministic hash of (tenant, source, type, target)
    tenant_id      text        NOT NULL,
    connection_id  text        NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    source_id      text        NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    target_id      text        NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
    type           text        NOT NULL CHECK (type IN ('contains', 'composed_of', 'member_of', 'uses', 'depends_on', 'routes_to', 'connects_to')),
    status         text        NOT NULL CHECK (status IN ('active', 'missing')),
    first_seen_at  timestamptz NOT NULL,
    last_seen_at   timestamptz NOT NULL,
    missing_since  timestamptz,
    last_job_id    text        NOT NULL,
    CONSTRAINT relationships_natural_key_uq UNIQUE (tenant_id, source_id, type, target_id),
    CONSTRAINT relationships_no_self_ck CHECK (source_id <> target_id),
    CONSTRAINT relationships_missing_since_ck CHECK ((status = 'missing') = (missing_since IS NOT NULL))
);
CREATE INDEX relationships_source_idx ON relationships (source_id);
CREATE INDEX relationships_target_idx ON relationships (target_id);
CREATE INDEX relationships_tenant_conn_status_idx ON relationships (tenant_id, connection_id, status);
CREATE INDEX relationships_tenant_type_idx ON relationships (tenant_id, type, id);

CREATE TABLE topology_changes (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    text        NOT NULL,
    job_id       text        NOT NULL,
    resource_id  text        NOT NULL,
    kind         text        NOT NULL CHECK (kind IN ('added', 'changed', 'reactivated', 'missing')),
    at           timestamptz NOT NULL
);
CREATE INDEX topology_changes_job_idx ON topology_changes (tenant_id, job_id, id);
CREATE INDEX topology_changes_resource_idx ON topology_changes (tenant_id, resource_id, id DESC);
CREATE INDEX topology_changes_at_idx ON topology_changes (at);

CREATE TABLE audit_events (
    id           text        PRIMARY KEY,
    tenant_id    text        NOT NULL,
    actor        text        NOT NULL,
    action       text        NOT NULL,
    target_type  text        NOT NULL,
    target_id    text        NOT NULL,
    request_id   text        NOT NULL DEFAULT '',
    at           timestamptz NOT NULL,
    details      jsonb
);
CREATE INDEX audit_events_tenant_at_idx ON audit_events (tenant_id, at DESC, id DESC);
CREATE INDEX audit_events_at_idx ON audit_events (at);
