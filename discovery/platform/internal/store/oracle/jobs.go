package oracle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

// ---------------------------------------------------------------- connections

const connCols = `id, tenant_id, name, provider, config, credential_ref, discovery_interval_minutes, created_by, created_at, updated_at`

func scanConn(r scanner) (store.Connection, error) {
	var c store.Connection
	err := r.Scan(&c.ID, &c.TenantID, &c.Name, &c.Provider, jsonScan{&c.Config}, nullStr{&c.CredentialRef}, &c.DiscoveryIntervalMinutes,
		&c.CreatedBy, utc{&c.CreatedAt}, utc{&c.UpdatedAt})
	if c.Config == nil {
		c.Config = map[string]any{}
	}
	return c, err
}

// CreateConnection inserts c; a duplicate name in the tenant is ErrConflict.
func (s *Store) CreateConnection(ctx context.Context, c store.Connection) (store.Connection, error) {
	q := &qb{}
	q.w(`INSERT INTO connections (` + connCols + `) VALUES (` + q.b(c.ID) + `,` + q.b(c.TenantID) + `,` + q.b(c.Name) + `,` + q.b(c.Provider) + `,` +
		q.b(clob(c.Config)) + `,` + q.b(str(c.CredentialRef)) + `,` + q.b(c.DiscoveryIntervalMinutes) + `,` + q.b(c.CreatedBy) + `,` +
		q.b(ts(c.CreatedAt)) + `,` + q.b(ts(c.CreatedAt)) + `)`)
	if _, err := s.exec(ctx, q); err != nil {
		if isUnique(err, "CONNECTIONS_TENANT_NAME_UQ") {
			return store.Connection{}, fmt.Errorf("%w: a connection named %q already exists", store.ErrConflict, c.Name)
		}
		return store.Connection{}, err
	}
	return s.GetConnection(ctx, c.TenantID, c.ID)
}

// GetConnection returns one connection of the tenant.
func (s *Store) GetConnection(ctx context.Context, tenantID, id string) (store.Connection, error) {
	q := &qb{}
	q.w(`SELECT ` + connCols + ` FROM connections WHERE tenant_id=` + q.b(tenantID) + ` AND id=` + q.b(id))
	c, err := scanConn(s.db.QueryRowContext(ctx, q.String(), q.args...))
	return c, notFound(err)
}

// ListConnections pages by ID.
func (s *Store) ListConnections(ctx context.Context, tenantID string, p store.Page) ([]store.Connection, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 1)
	if err != nil {
		return nil, "", err
	}
	q := &qb{}
	q.w(`SELECT ` + connCols + ` FROM connections WHERE tenant_id=` + q.b(tenantID))
	if keys != nil {
		q.w(` AND id > ` + q.b(keys[0]))
	}
	q.w(` ORDER BY id`)
	pageLimit(q, p.Limit+1)
	rows, err := s.query(ctx, q)
	out, err := collect(rows, err, scanConn)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		next = store.EncodeCursor(out[len(out)-1].ID)
	}
	return out, next, nil
}

// AllConnections lists every connection (workers only).
func (s *Store) AllConnections(ctx context.Context) ([]store.Connection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+connCols+` FROM connections ORDER BY id FETCH FIRST 10000 ROWS ONLY`)
	return collect(rows, err, scanConn)
}

// ----------------------------------------------------------------------- jobs

const jobCols = `id, tenant_id, connection_id, status, trigger_kind, requested_by, request_id, idempotency_key, attempt,
	cancel_requested, phase, pages, resources_seen, relationships_seen, warnings, counts, complete, error, worker_id,
	created_at, started_at, finished_at, heartbeat_at`

func scanJob(r scanner) (store.Job, error) {
	var j store.Job
	err := r.Scan(&j.ID, &j.TenantID, &j.ConnectionID, &j.Status, &j.Trigger, &j.RequestedBy, nullStr{&j.RequestID}, nullStr{&j.IdempotencyKey}, &j.Attempt,
		boolScan{&j.CancelRequested}, &j.Progress.Phase, &j.Progress.Pages, &j.Progress.Resources, &j.Progress.Relationships, &j.Progress.Warnings,
		jsonScan{&j.Counts}, boolScan{&j.Complete}, nullStr{&j.Error}, nullStr{&j.WorkerID},
		utc{&j.CreatedAt}, utcPtr{&j.StartedAt}, utcPtr{&j.FinishedAt}, utcPtr{&j.HeartbeatAt})
	return j, err
}

func (s *Store) jobWhere(ctx context.Context, build func(q *qb)) (store.Job, error) {
	q := &qb{}
	q.w(`SELECT ` + jobCols + ` FROM discovery_jobs WHERE `)
	build(q)
	return scanJob(s.db.QueryRowContext(ctx, q.String(), q.args...))
}

// CreateJob inserts a queued job honouring idempotency and the one-active rule.
func (s *Store) CreateJob(ctx context.Context, j store.Job) (store.Job, bool, error) {
	byKey := func() (store.Job, error) {
		return s.jobWhere(ctx, func(q *qb) {
			q.w(`tenant_id=` + q.b(j.TenantID) + ` AND idempotency_key=` + q.b(j.IdempotencyKey))
		})
	}
	if j.IdempotencyKey != "" {
		existing, err := byKey()
		if err == nil {
			return existing, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return store.Job{}, false, err
		}
	}
	q := &qb{}
	q.w(`INSERT INTO discovery_jobs (id, tenant_id, connection_id, status, trigger_kind, requested_by, request_id, idempotency_key, phase, counts, created_at)
		VALUES (` + q.b(j.ID) + `,` + q.b(j.TenantID) + `,` + q.b(j.ConnectionID) + `,'queued',` + q.b(string(j.Trigger)) + `,` + q.b(j.RequestedBy) + `,` +
		q.b(str(j.RequestID)) + `,` + q.b(str(j.IdempotencyKey)) + `,'pending',` + q.b(clob(topology.Counts{})) + `,` + q.b(ts(j.CreatedAt)) + `)`)
	_, err := s.exec(ctx, q)
	switch {
	case err == nil:
		out, gerr := s.GetJob(ctx, j.TenantID, j.ID)
		return out, gerr == nil, gerr
	case isUnique(err, "DISCOVERY_JOBS_ONE_ACTIVE_UQ"):
		active, gerr := s.jobWhere(ctx, func(q *qb) {
			q.w(`tenant_id=` + q.b(j.TenantID) + ` AND connection_id=` + q.b(j.ConnectionID) + ` AND status IN ('queued','running')`)
		})
		if gerr != nil {
			return store.Job{}, false, store.ErrActiveJob
		}
		return active, false, store.ErrActiveJob
	case isUnique(err, "DISCOVERY_JOBS_IDEMPOTENCY_UQ"):
		existing, gerr := byKey()
		return existing, false, gerr
	default:
		return store.Job{}, false, err
	}
}

// GetJob returns one job of the tenant.
func (s *Store) GetJob(ctx context.Context, tenantID, id string) (store.Job, error) {
	j, err := s.jobWhere(ctx, func(q *qb) { q.w(`tenant_id=` + q.b(tenantID) + ` AND id=` + q.b(id)) })
	return j, notFound(err)
}

// ListJobs pages newest first on (created_at, id).
func (s *Store) ListJobs(ctx context.Context, tenantID string, f store.JobFilter, p store.Page) ([]store.Job, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 2)
	if err != nil {
		return nil, "", err
	}
	q := &qb{}
	q.w(`SELECT ` + jobCols + ` FROM discovery_jobs WHERE tenant_id=` + q.b(tenantID))
	if f.ConnectionID != "" {
		q.w(` AND connection_id=` + q.b(f.ConnectionID))
	}
	if f.Status != "" {
		q.w(` AND status=` + q.b(f.Status))
	}
	if keys != nil {
		t, err := time.Parse(time.RFC3339Nano, keys[0])
		if err != nil {
			return nil, "", store.ErrInvalidCursor
		}
		a, b := q.b(ts(t)), q.b(keys[1])
		q.w(` AND (created_at < ` + a + ` OR (created_at = ` + a + ` AND id < ` + b + `))`)
	}
	q.w(` ORDER BY created_at DESC, id DESC`)
	pageLimit(q, p.Limit+1)
	rows, err := s.query(ctx, q)
	out, err := collect(rows, err, scanJob)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = store.EncodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
	}
	return out, next, nil
}

// LatestJobs returns the newest job per connection of the tenant.
func (s *Store) LatestJobs(ctx context.Context, tenantID string) (map[string]store.Job, error) {
	q := &qb{}
	q.w(`SELECT ` + jobCols + ` FROM (SELECT j.*, ROW_NUMBER() OVER (PARTITION BY connection_id ORDER BY created_at DESC, id DESC) rn
		FROM discovery_jobs j WHERE tenant_id=` + q.b(tenantID) + `) WHERE rn = 1`)
	rows, err := s.query(ctx, q)
	js, err := collect(rows, err, scanJob)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.Job, len(js))
	for _, j := range js {
		out[j.ConnectionID] = j
	}
	return out, nil
}

// CountSucceededJobs counts successful runs of a connection.
func (s *Store) CountSucceededJobs(ctx context.Context, tenantID, connectionID string) (int, error) {
	q := &qb{}
	q.w(`SELECT count(*) FROM discovery_jobs WHERE tenant_id=` + q.b(tenantID) + ` AND connection_id=` + q.b(connectionID) + ` AND status='succeeded'`)
	var n int
	err := s.db.QueryRowContext(ctx, q.String(), q.args...).Scan(&n)
	return n, err
}

// ClaimJob locks the oldest queued job with FOR UPDATE SKIP LOCKED, so
// concurrent workers never claim the same job. Oracle does not allow
// FETCH FIRST together with FOR UPDATE, so the cursor is read for one row and
// closed; only fetched rows are locked.
func (s *Store) ClaimJob(ctx context.Context, workerID string, now time.Time) (store.Job, bool, error) {
	var id string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM discovery_jobs WHERE status='queued' ORDER BY created_at, id FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		if rows.Next() {
			err = rows.Scan(&id)
		}
		rows.Close()
		if err != nil || id == "" {
			return err
		}
		q := &qb{}
		n := q.b(ts(now))
		q.w(`UPDATE discovery_jobs SET status='running', worker_id=` + q.b(workerID) + `, attempt=attempt+1,
			started_at=NVL(started_at, ` + n + `), heartbeat_at=` + n + `, phase='starting' WHERE id=` + q.b(id))
		_, err = txExec(ctx, tx, q)
		return err
	})
	if err != nil || id == "" {
		return store.Job{}, false, err
	}
	j, err := s.jobWhere(ctx, func(q *qb) { q.w(`id=` + q.b(id)) })
	return j, err == nil, err
}

// Heartbeat stores progress if workerID still owns the lease.
func (s *Store) Heartbeat(ctx context.Context, jobID, workerID string, pr store.JobProgress, now time.Time) (bool, error) {
	q := &qb{}
	q.w(`UPDATE discovery_jobs SET heartbeat_at=` + q.b(ts(now)) + `, phase=` + q.b(pr.Phase) + `, pages=` + q.b(pr.Pages) +
		`, resources_seen=` + q.b(pr.Resources) + `, relationships_seen=` + q.b(pr.Relationships) + `, warnings=` + q.b(pr.Warnings) +
		` WHERE id=` + q.b(jobID) + ` AND worker_id=` + q.b(workerID) + ` AND status='running'`)
	n, err := s.exec(ctx, q)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, store.ErrNotOwner
	}
	var cancel bool
	q = &qb{}
	q.w(`SELECT cancel_requested FROM discovery_jobs WHERE id=` + q.b(jobID))
	err = s.db.QueryRowContext(ctx, q.String(), q.args...).Scan(boolScan{&cancel})
	return cancel, err
}

// FinishJob moves an owned running job to a terminal status.
func (s *Store) FinishJob(ctx context.Context, jobID, workerID string, status store.JobStatus, counts topology.Counts, pr store.JobProgress, complete bool, errMsg string, now time.Time) error {
	q := &qb{}
	n := q.b(ts(now))
	q.w(`UPDATE discovery_jobs SET status=` + q.b(string(status)) + `, counts=` + q.b(clob(counts)) + `, phase=` + q.b(pr.Phase) +
		`, pages=` + q.b(pr.Pages) + `, resources_seen=` + q.b(pr.Resources) + `, relationships_seen=` + q.b(pr.Relationships) +
		`, warnings=` + q.b(pr.Warnings) + `, complete=` + q.b(boolNum(complete)) + `, error=` + q.b(str(truncate(errMsg, 2000))) +
		`, finished_at=` + n + `, heartbeat_at=` + n +
		` WHERE id=` + q.b(jobID) + ` AND worker_id=` + q.b(workerID) + ` AND status='running'`)
	affected, err := s.exec(ctx, q)
	if err != nil {
		return err
	}
	if affected == 0 {
		return store.ErrNotOwner
	}
	return nil
}

// RequestCancel cancels a queued job immediately or flags a running one.
func (s *Store) RequestCancel(ctx context.Context, tenantID, id string, now time.Time) (store.Job, error) {
	q := &qb{}
	q.w(`UPDATE discovery_jobs SET
			finished_at = CASE WHEN status='queued' THEN ` + q.b(ts(now)) + ` ELSE finished_at END,
			phase = CASE WHEN status='queued' THEN 'cancelled' ELSE phase END,
			status = CASE WHEN status='queued' THEN 'cancelled' ELSE status END,
			cancel_requested = 1
		WHERE tenant_id=` + q.b(tenantID) + ` AND id=` + q.b(id) + ` AND status IN ('queued','running')`)
	n, err := s.exec(ctx, q)
	if err != nil {
		return store.Job{}, err
	}
	j, gerr := s.GetJob(ctx, tenantID, id)
	if gerr != nil {
		return store.Job{}, gerr
	}
	if n == 0 {
		return j, fmt.Errorf("%w: job is already %s", store.ErrConflict, j.Status)
	}
	return j, nil
}

// RecoverExpiredJobs handles workers that died holding a lease.
func (s *Store) RecoverExpiredJobs(ctx context.Context, before time.Time, maxAttempts int, now time.Time) (int, int, error) {
	q := &qb{}
	q.w(`UPDATE discovery_jobs SET status='failed', finished_at=` + q.b(ts(now)) + `, phase='failed',
			error='worker lease expired and attempts are exhausted'
		WHERE status='running' AND heartbeat_at < ` + q.b(ts(before)) + ` AND attempt >= ` + q.b(maxAttempts))
	failed, err := s.exec(ctx, q)
	if err != nil {
		return 0, 0, err
	}
	q = &qb{}
	q.w(`UPDATE discovery_jobs SET status=CASE WHEN cancel_requested=1 THEN 'cancelled' ELSE 'queued' END,
			finished_at=CASE WHEN cancel_requested=1 THEN ` + q.b(ts(now)) + ` ELSE NULL END, worker_id=NULL, phase='requeued'
		WHERE status='running' AND heartbeat_at < ` + q.b(ts(before)))
	requeued, err := s.exec(ctx, q)
	if err != nil {
		return 0, int(failed), err
	}
	return int(requeued), int(failed), nil
}

// AppendJobEvent appends an event unless the job already has maxEvents.
func (s *Store) AppendJobEvent(ctx context.Context, tenantID, jobID string, e store.JobEvent, maxEvents int) error {
	var details any = clob(nil)
	if len(e.Details) > 0 {
		details = clob(e.Details)
	}
	q := &qb{}
	job := q.b(jobID)
	q.w(`INSERT INTO job_events (job_id, seq, tenant_id, at, lvl, message, details)
		SELECT ` + job + `, NVL(MAX(seq), 0) + 1, ` + q.b(tenantID) + `, ` + q.b(ts(e.At)) + `, ` + q.b(e.Level) + `, ` +
		q.b(truncate(e.Message, 1000)) + `, ` + q.b(details) + ` FROM job_events WHERE job_id=` + job +
		` HAVING NVL(MAX(seq), 0) < ` + q.b(maxEvents))
	_, err := s.exec(ctx, q)
	if isUnique(err, "") {
		return nil // concurrent append for the same seq; dropping one diagnostic event is acceptable
	}
	return err
}

// ListJobEvents returns events after seq.
func (s *Store) ListJobEvents(ctx context.Context, tenantID, jobID string, afterSeq, limit int) ([]store.JobEvent, error) {
	q := &qb{}
	q.w(`SELECT seq, at, lvl, message, details FROM job_events WHERE tenant_id=` + q.b(tenantID) + ` AND job_id=` + q.b(jobID) +
		` AND seq > ` + q.b(afterSeq) + ` ORDER BY seq`)
	pageLimit(q, limit)
	rows, err := s.query(ctx, q)
	return collect(rows, err, func(r scanner) (store.JobEvent, error) {
		var e store.JobEvent
		err := r.Scan(&e.Seq, utc{&e.At}, &e.Level, &e.Message, jsonScan{&e.Details})
		return e, err
	})
}
