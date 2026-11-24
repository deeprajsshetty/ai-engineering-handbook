// Package postgres implements store.Store on PostgreSQL through pgx.
//
// SQL is plain and parameterized (no string interpolation of input). Bulk
// writes use unnest() over typed arrays so one round trip upserts a batch.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

// Store is the PostgreSQL implementation.
type Store struct {
	pool *pgxpool.Pool
}

var _ store.Store = (*Store)(nil)

// Open connects with bounded pool size and statement timeout.
func Open(ctx context.Context, url string, maxConns int32, searchPath string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("invalid database URL") // never echo the URL: it may hold a password
	}
	cfg.MaxConns = maxConns
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "30000"
	cfg.ConnConfig.RuntimeParams["application_name"] = "obsplatform"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	if searchPath != "" {
		cfg.ConnConfig.RuntimeParams["search_path"] = searchPath
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(cctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	if err := pool.Ping(cctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	return pool, nil
}

// New wraps a pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the pool for migrations and readiness checks.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping checks connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Close closes the pool.
func (s *Store) Close() { s.pool.Close() }

func isUnique(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && (constraint == "" || pe.ConstraintName == constraint)
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

func jsonb(v any) []byte {
	if v == nil {
		return []byte("{}")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// ---------------------------------------------------------------- connections

const connCols = `id, tenant_id, name, provider, config, credential_ref, discovery_interval_minutes, created_by, created_at, updated_at`

func scanConn(row pgx.Row) (store.Connection, error) {
	var c store.Connection
	var cfg []byte
	err := row.Scan(&c.ID, &c.TenantID, &c.Name, &c.Provider, &cfg, &c.CredentialRef, &c.DiscoveryIntervalMinutes, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	_ = json.Unmarshal(cfg, &c.Config)
	if c.Config == nil {
		c.Config = map[string]any{}
	}
	return c, nil
}

// CreateConnection inserts c; a duplicate name in the tenant is ErrConflict.
func (s *Store) CreateConnection(ctx context.Context, c store.Connection) (store.Connection, error) {
	row := s.pool.QueryRow(ctx, `INSERT INTO connections (`+connCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9) RETURNING `+connCols,
		c.ID, c.TenantID, c.Name, c.Provider, jsonb(c.Config), c.CredentialRef, c.DiscoveryIntervalMinutes, c.CreatedBy, c.CreatedAt)
	out, err := scanConn(row)
	if isUnique(err, "connections_tenant_name_uq") {
		return out, fmt.Errorf("%w: a connection named %q already exists", store.ErrConflict, c.Name)
	}
	return out, err
}

// GetConnection returns one connection of the tenant.
func (s *Store) GetConnection(ctx context.Context, tenantID, id string) (store.Connection, error) {
	c, err := scanConn(s.pool.QueryRow(ctx, `SELECT `+connCols+` FROM connections WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	return c, notFound(err)
}

// ListConnections pages by ID (IDs are time-ordered).
func (s *Store) ListConnections(ctx context.Context, tenantID string, p store.Page) ([]store.Connection, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 1)
	if err != nil {
		return nil, "", err
	}
	after := ""
	if keys != nil {
		after = keys[0]
	}
	rows, err := s.pool.Query(ctx, `SELECT `+connCols+` FROM connections WHERE tenant_id=$1 AND id > $2 ORDER BY id LIMIT $3`, tenantID, after, p.Limit+1)
	if err != nil {
		return nil, "", err
	}
	out, err := collect(rows, scanConn)
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

// AllConnections lists every connection (workers only; not exposed by the API).
func (s *Store) AllConnections(ctx context.Context) ([]store.Connection, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+connCols+` FROM connections ORDER BY id LIMIT 10000`)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanConn)
}

func collect[T any](rows pgx.Rows, scan func(pgx.Row) (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------------- jobs

const jobCols = `id, tenant_id, connection_id, status, trigger, requested_by, request_id, coalesce(idempotency_key,''), attempt,
	cancel_requested, phase, pages, resources_seen, relationships_seen, warnings, counts, complete, error, worker_id,
	created_at, started_at, finished_at, heartbeat_at`

func scanJob(row pgx.Row) (store.Job, error) {
	var j store.Job
	var counts []byte
	err := row.Scan(&j.ID, &j.TenantID, &j.ConnectionID, &j.Status, &j.Trigger, &j.RequestedBy, &j.RequestID, &j.IdempotencyKey, &j.Attempt,
		&j.CancelRequested, &j.Progress.Phase, &j.Progress.Pages, &j.Progress.Resources, &j.Progress.Relationships, &j.Progress.Warnings,
		&counts, &j.Complete, &j.Error, &j.WorkerID, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.HeartbeatAt)
	if err != nil {
		return j, err
	}
	_ = json.Unmarshal(counts, &j.Counts)
	return j, nil
}

// CreateJob inserts a queued job honouring idempotency and the one-active rule.
func (s *Store) CreateJob(ctx context.Context, j store.Job) (store.Job, bool, error) {
	if j.IdempotencyKey != "" {
		existing, err := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM discovery_jobs WHERE tenant_id=$1 AND idempotency_key=$2`, j.TenantID, j.IdempotencyKey))
		if err == nil {
			return existing, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return store.Job{}, false, err
		}
	}
	var idem any
	if j.IdempotencyKey != "" {
		idem = j.IdempotencyKey
	}
	row := s.pool.QueryRow(ctx, `INSERT INTO discovery_jobs (id, tenant_id, connection_id, status, trigger, requested_by, request_id, idempotency_key, phase, created_at)
		VALUES ($1,$2,$3,'queued',$4,$5,$6,$7,'pending',$8) RETURNING `+jobCols,
		j.ID, j.TenantID, j.ConnectionID, j.Trigger, j.RequestedBy, j.RequestID, idem, j.CreatedAt)
	out, err := scanJob(row)
	switch {
	case err == nil:
		return out, true, nil
	case isUnique(err, "discovery_jobs_one_active_uq"):
		active, gerr := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM discovery_jobs WHERE tenant_id=$1 AND connection_id=$2 AND status IN ('queued','running')`, j.TenantID, j.ConnectionID))
		if gerr != nil {
			return store.Job{}, false, store.ErrActiveJob
		}
		return active, false, store.ErrActiveJob
	case isUnique(err, "discovery_jobs_idempotency_uq"):
		// Lost a race with an identical request: return the winner.
		existing, gerr := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM discovery_jobs WHERE tenant_id=$1 AND idempotency_key=$2`, j.TenantID, j.IdempotencyKey))
		return existing, false, gerr
	default:
		return store.Job{}, false, err
	}
}

// GetJob returns one job of the tenant.
func (s *Store) GetJob(ctx context.Context, tenantID, id string) (store.Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM discovery_jobs WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	return j, notFound(err)
}

// ListJobs pages newest first on (created_at, id).
func (s *Store) ListJobs(ctx context.Context, tenantID string, f store.JobFilter, p store.Page) ([]store.Job, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 2)
	if err != nil {
		return nil, "", err
	}
	var afterTS *time.Time
	afterID := ""
	if keys != nil {
		t, err := time.Parse(time.RFC3339Nano, keys[0])
		if err != nil {
			return nil, "", store.ErrInvalidCursor
		}
		afterTS, afterID = &t, keys[1]
	}
	rows, err := s.pool.Query(ctx, `SELECT `+jobCols+` FROM discovery_jobs
		WHERE tenant_id=$1 AND ($2='' OR connection_id=$2) AND ($3='' OR status=$3)
		  AND ($4::timestamptz IS NULL OR (created_at, id) < ($4, $5))
		ORDER BY created_at DESC, id DESC LIMIT $6`, tenantID, f.ConnectionID, f.Status, afterTS, afterID, p.Limit+1)
	if err != nil {
		return nil, "", err
	}
	out, err := collect(rows, scanJob)
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
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (connection_id) `+jobCols+` FROM discovery_jobs
		WHERE tenant_id=$1 ORDER BY connection_id, created_at DESC, id DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	js, err := collect(rows, scanJob)
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
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM discovery_jobs WHERE tenant_id=$1 AND connection_id=$2 AND status='succeeded'`, tenantID, connectionID).Scan(&n)
	return n, err
}

// ClaimJob uses FOR UPDATE SKIP LOCKED so concurrent workers (in one or many
// replicas) never claim the same job.
func (s *Store) ClaimJob(ctx context.Context, workerID string, now time.Time) (store.Job, bool, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `UPDATE discovery_jobs SET status='running', worker_id=$1, attempt=attempt+1,
			started_at=coalesce(started_at,$2), heartbeat_at=$2, phase='starting'
		WHERE id = (SELECT id FROM discovery_jobs WHERE status='queued' ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING `+jobCols, workerID, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Job{}, false, nil
	}
	return j, err == nil, err
}

// Heartbeat stores progress if workerID still owns the lease.
func (s *Store) Heartbeat(ctx context.Context, jobID, workerID string, pr store.JobProgress, now time.Time) (bool, error) {
	var cancel bool
	err := s.pool.QueryRow(ctx, `UPDATE discovery_jobs SET heartbeat_at=$3, phase=$4, pages=$5, resources_seen=$6, relationships_seen=$7, warnings=$8
		WHERE id=$1 AND worker_id=$2 AND status='running' RETURNING cancel_requested`,
		jobID, workerID, now, pr.Phase, pr.Pages, pr.Resources, pr.Relationships, pr.Warnings).Scan(&cancel)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, store.ErrNotOwner
	}
	return cancel, err
}

// FinishJob moves an owned running job to a terminal status.
func (s *Store) FinishJob(ctx context.Context, jobID, workerID string, status store.JobStatus, counts topology.Counts, pr store.JobProgress, complete bool, errMsg string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE discovery_jobs SET status=$3, counts=$4, phase=$5, pages=$6, resources_seen=$7, relationships_seen=$8,
			warnings=$9, complete=$10, error=$11, finished_at=$12, heartbeat_at=$12
		WHERE id=$1 AND worker_id=$2 AND status='running'`,
		jobID, workerID, status, jsonb(counts), pr.Phase, pr.Pages, pr.Resources, pr.Relationships, pr.Warnings, complete, truncate(errMsg, 2000), now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotOwner
	}
	return nil
}

// RequestCancel cancels a queued job immediately or flags a running one.
func (s *Store) RequestCancel(ctx context.Context, tenantID, id string, now time.Time) (store.Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `UPDATE discovery_jobs SET
			status = CASE WHEN status='queued' THEN 'cancelled' ELSE status END,
			finished_at = CASE WHEN status='queued' THEN $3::timestamptz ELSE finished_at END,
			phase = CASE WHEN status='queued' THEN 'cancelled' ELSE phase END,
			cancel_requested = true
		WHERE tenant_id=$1 AND id=$2 AND status IN ('queued','running') RETURNING `+jobCols, tenantID, id, now))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, gerr := s.GetJob(ctx, tenantID, id)
		if gerr != nil {
			return store.Job{}, gerr
		}
		return existing, fmt.Errorf("%w: job is already %s", store.ErrConflict, existing.Status)
	}
	return j, err
}

// RecoverExpiredJobs handles workers that died holding a lease.
func (s *Store) RecoverExpiredJobs(ctx context.Context, before time.Time, maxAttempts int, now time.Time) (int, int, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE discovery_jobs SET status='failed', finished_at=$3, phase='failed',
			error='worker lease expired and attempts are exhausted'
		WHERE status='running' AND heartbeat_at < $1 AND attempt >= $2`, before, maxAttempts, now)
	if err != nil {
		return 0, 0, err
	}
	failed := int(tag.RowsAffected())
	tag, err = s.pool.Exec(ctx, `UPDATE discovery_jobs SET status=CASE WHEN cancel_requested THEN 'cancelled' ELSE 'queued' END,
			finished_at=CASE WHEN cancel_requested THEN $2::timestamptz ELSE NULL END, worker_id='', phase='requeued'
		WHERE status='running' AND heartbeat_at < $1`, before, now)
	if err != nil {
		return 0, failed, err
	}
	return int(tag.RowsAffected()), failed, nil
}

// AppendJobEvent appends an event unless the job already has maxEvents; the
// cap keeps job logs bounded regardless of provider behaviour.
func (s *Store) AppendJobEvent(ctx context.Context, tenantID, jobID string, e store.JobEvent, maxEvents int) error {
	var details any
	if len(e.Details) > 0 {
		details = jsonb(e.Details)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO job_events (job_id, seq, tenant_id, at, level, message, details)
		SELECT $1::text, coalesce(max(seq),0)+1, $2::text, $3::timestamptz, $4::text, $5::text, $6::jsonb FROM job_events WHERE job_id=$1::text
		HAVING coalesce(max(seq),0) < $7::int`, jobID, tenantID, e.At, e.Level, truncate(e.Message, 1000), details, maxEvents)
	if isUnique(err, "") {
		return nil // concurrent append for the same seq; dropping one diagnostic event is acceptable
	}
	return err
}

// ListJobEvents returns events after seq.
func (s *Store) ListJobEvents(ctx context.Context, tenantID, jobID string, afterSeq, limit int) ([]store.JobEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT seq, at, level, message, details FROM job_events
		WHERE tenant_id=$1 AND job_id=$2 AND seq > $3 ORDER BY seq LIMIT $4`, tenantID, jobID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Row) (store.JobEvent, error) {
		var e store.JobEvent
		var d []byte
		err := r.Scan(&e.Seq, &e.At, &e.Level, &e.Message, &d)
		if len(d) > 0 {
			_ = json.Unmarshal(d, &e.Details)
		}
		return e, err
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
