package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

const sampleBatch = 5000

// InsertSamples writes samples idempotently: an existing (resource, metric,
// ts) row is left unchanged, so a retried collection does not duplicate data.
// It returns the number of new rows.
func (s *Store) InsertSamples(ctx context.Context, samples []metrics.Sample) (int64, error) {
	var total int64
	for start := 0; start < len(samples); start += sampleBatch {
		chunk := samples[start:min(start+sampleBatch, len(samples))]
		n := len(chunk)
		rid, met, ten, src, col := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		ts := make([]time.Time, n)
		val := make([]float64, n)
		for i, x := range chunk {
			rid[i], met[i], ten[i], src[i], col[i] = x.ResourceID, x.Metric, x.TenantID, string(x.Source), x.Collector
			ts[i], val[i] = x.Timestamp.UTC(), x.Value
		}
		tag, err := s.pool.Exec(ctx, `INSERT INTO metric_samples (resource_id, metric, ts, tenant_id, value, source, collector)
			SELECT u.rid, u.met, u.ts, u.ten, u.val, u.src, u.col
			FROM unnest($1::text[], $2::text[], $3::timestamptz[], $4::text[], $5::float8[], $6::text[], $7::text[])
			     AS u(rid, met, ts, ten, val, src, col)
			JOIN resources r ON r.id = u.rid AND r.tenant_id = u.ten
			ON CONFLICT (resource_id, metric, ts) DO NOTHING`, rid, met, ts, ten, val, src, col)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

// LatestSampleTimes returns the newest sample time per resource.
func (s *Store) LatestSampleTimes(ctx context.Context, tenantID string, ids []string) (map[string]time.Time, error) {
	rows, err := s.pool.Query(ctx, `SELECT resource_id, max(ts) FROM metric_samples
		WHERE tenant_id=$1 AND resource_id = ANY($2) GROUP BY resource_id`, tenantID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var t time.Time
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// LatestSamples returns the newest sample of each metric of a resource.
func (s *Store) LatestSamples(ctx context.Context, tenantID, resourceID string) (map[string]metrics.Sample, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (metric) metric, ts, value, source, collector FROM metric_samples
		WHERE tenant_id=$1 AND resource_id=$2 ORDER BY metric, ts DESC`, tenantID, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]metrics.Sample{}
	for rows.Next() {
		x := metrics.Sample{TenantID: tenantID, ResourceID: resourceID}
		if err := rows.Scan(&x.Metric, &x.Timestamp, &x.Value, &x.Source, &x.Collector); err != nil {
			return nil, err
		}
		out[x.Metric] = x
	}
	return out, rows.Err()
}

// LatestSamplesFor returns the newest sample per (resource, metric) for many
// resources at once. Each pair is one backward primary-key probe
// (LATERAL ... ORDER BY ts DESC LIMIT 1), so cost is O(R*M*log n) regardless
// of history length — suitable for fleet tables.
func (s *Store) LatestSamplesFor(ctx context.Context, tenantID string, resourceIDs, metricNames []string) (map[string]map[string]metrics.Sample, error) {
	out := map[string]map[string]metrics.Sample{}
	if len(resourceIDs) == 0 || len(metricNames) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id, m.metric, x.ts, x.value, x.source, x.collector
		FROM unnest($2::text[]) AS r(id) CROSS JOIN unnest($3::text[]) AS m(metric)
		CROSS JOIN LATERAL (SELECT ts, value, source, collector FROM metric_samples
			WHERE resource_id = r.id AND metric = m.metric AND tenant_id = $1 ORDER BY ts DESC LIMIT 1) x`, tenantID, resourceIDs, metricNames)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		x := metrics.Sample{TenantID: tenantID}
		if err := rows.Scan(&x.ResourceID, &x.Metric, &x.Timestamp, &x.Value, &x.Source, &x.Collector); err != nil {
			return nil, err
		}
		if out[x.ResourceID] == nil {
			out[x.ResourceID] = map[string]metrics.Sample{}
		}
		out[x.ResourceID][x.Metric] = x
	}
	return out, rows.Err()
}

// QuerySeries buckets samples with date_bin and returns avg/min/max/count per
// bucket. The primary key (resource_id, metric, ts) makes this a single index
// range scan: O(log n + k) for k samples in range.
func (s *Store) QuerySeries(ctx context.Context, tenantID, resourceID, metric string, from, to time.Time, step time.Duration) (store.Series, error) {
	out := store.Series{Points: []store.Point{}, Sources: []metrics.Source{}}
	rows, err := s.pool.Query(ctx, `SELECT date_bin($4::interval, ts, TIMESTAMPTZ '2000-01-01 00:00:00+00') AS b,
			avg(value), min(value), max(value), count(*)
		FROM metric_samples WHERE tenant_id=$1 AND resource_id=$2 AND metric=$3 AND ts >= $5 AND ts < $6
		GROUP BY b ORDER BY b`, tenantID, resourceID, metric, step, from, to)
	if err != nil {
		return out, err
	}
	pts, err := collect(rows, func(r pgx.Row) (store.Point, error) {
		var p store.Point
		err := r.Scan(&p.Timestamp, &p.Avg, &p.Min, &p.Max, &p.Count)
		p.Timestamp = p.Timestamp.UTC()
		return p, err
	})
	if err != nil {
		return out, err
	}
	if pts != nil {
		out.Points = pts
	}
	rows, err = s.pool.Query(ctx, `SELECT DISTINCT source FROM metric_samples
		WHERE tenant_id=$1 AND resource_id=$2 AND metric=$3 AND ts >= $4 AND ts < $5 ORDER BY source`, tenantID, resourceID, metric, from, to)
	if err != nil {
		return out, err
	}
	srcs, err := collect(rows, func(r pgx.Row) (metrics.Source, error) {
		var x metrics.Source
		return x, r.Scan(&x)
	})
	if srcs != nil {
		out.Sources = srcs
	}
	return out, err
}

const runCols = `id, tenant_id, connection_id, trigger, status, requested_by, collector, resources, samples, error, started_at, finished_at`

func scanRun(r pgx.Row) (store.CollectionRun, error) {
	var c store.CollectionRun
	err := r.Scan(&c.ID, &c.TenantID, &c.ConnectionID, &c.Trigger, &c.Status, &c.RequestedBy, &c.Collector, &c.Resources, &c.Samples, &c.Error, &c.StartedAt, &c.FinishedAt)
	return c, err
}

// CreateCollectionRun inserts a running collection run.
func (s *Store) CreateCollectionRun(ctx context.Context, r store.CollectionRun) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO metric_collections (`+runCols+`) VALUES ($1,$2,$3,$4,'running',$5,$6,0,0,'',$7,NULL)`,
		r.ID, r.TenantID, r.ConnectionID, r.Trigger, r.RequestedBy, r.Collector, r.StartedAt)
	return err
}

// FinishCollectionRun records the outcome.
func (s *Store) FinishCollectionRun(ctx context.Context, tenantID, id, status string, resources, samples int, errMsg string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE metric_collections SET status=$3, resources=$4, samples=$5, error=$6, finished_at=$7
		WHERE tenant_id=$1 AND id=$2`, tenantID, id, status, resources, samples, truncate(errMsg, 2000), now)
	return err
}

// GetCollectionRun returns one run.
func (s *Store) GetCollectionRun(ctx context.Context, tenantID, id string) (store.CollectionRun, error) {
	r, err := scanRun(s.pool.QueryRow(ctx, `SELECT `+runCols+` FROM metric_collections WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	return r, notFound(err)
}

// ListCollectionRuns pages newest first.
func (s *Store) ListCollectionRuns(ctx context.Context, tenantID, connectionID string, p store.Page) ([]store.CollectionRun, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 1)
	if err != nil {
		return nil, "", err
	}
	before := "~" // sorts after every id
	if keys != nil {
		before = keys[0]
	}
	rows, err := s.pool.Query(ctx, `SELECT `+runCols+` FROM metric_collections
		WHERE tenant_id=$1 AND ($2='' OR connection_id=$2) AND id < $3 ORDER BY id DESC LIMIT $4`, tenantID, connectionID, before, p.Limit+1)
	if err != nil {
		return nil, "", err
	}
	out, err := collect(rows, scanRun)
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

// ---------------------------------------------------------------------- audit

// InsertAudit stores an audit event.
func (s *Store) InsertAudit(ctx context.Context, e store.AuditEvent) error {
	var d any
	if len(e.Details) > 0 {
		d = jsonb(e.Details)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_events (id, tenant_id, actor, action, target_type, target_id, request_id, at, details)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, e.ID, e.TenantID, e.Actor, e.Action, e.TargetType, e.TargetID, e.RequestID, e.At, d)
	return err
}

// ListAudit pages newest first on (at, id).
func (s *Store) ListAudit(ctx context.Context, tenantID string, p store.Page) ([]store.AuditEvent, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 2)
	if err != nil {
		return nil, "", err
	}
	var at *time.Time
	id := ""
	if keys != nil {
		t, err := time.Parse(time.RFC3339Nano, keys[0])
		if err != nil {
			return nil, "", store.ErrInvalidCursor
		}
		at, id = &t, keys[1]
	}
	rows, err := s.pool.Query(ctx, `SELECT id, tenant_id, actor, action, target_type, target_id, request_id, at, details FROM audit_events
		WHERE tenant_id=$1 AND ($2::timestamptz IS NULL OR (at, id) < ($2, $3)) ORDER BY at DESC, id DESC LIMIT $4`, tenantID, at, id, p.Limit+1)
	if err != nil {
		return nil, "", err
	}
	out, err := collect(rows, func(r pgx.Row) (store.AuditEvent, error) {
		var e store.AuditEvent
		var d []byte
		err := r.Scan(&e.ID, &e.TenantID, &e.Actor, &e.Action, &e.TargetType, &e.TargetID, &e.RequestID, &e.At, &d)
		if len(d) > 0 {
			_ = json.Unmarshal(d, &e.Details)
		}
		return e, err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		l := out[len(out)-1]
		next = store.EncodeCursor(l.At.UTC().Format(time.RFC3339Nano), l.ID)
	}
	return out, next, nil
}

// ------------------------------------------------------------------ retention

// PurgeMetricSamples deletes samples older than before in batches so a large
// backlog never holds one long transaction.
func (s *Store) PurgeMetricSamples(ctx context.Context, before time.Time, batch int) (int64, error) {
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, `DELETE FROM metric_samples WHERE ctid IN (SELECT ctid FROM metric_samples WHERE ts < $1 LIMIT $2)`, before, batch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < int64(batch) || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}

// PurgeMissingResources hard-deletes resources missing since before; their
// relationships and samples cascade.
func (s *Store) PurgeMissingResources(ctx context.Context, before time.Time) (int64, error) {
	if _, err := s.pool.Exec(ctx, `DELETE FROM relationships WHERE status='missing' AND missing_since < $1`, before); err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM resources WHERE status='missing' AND missing_since < $1`, before)
	return tag.RowsAffected(), err
}

// PurgeJobs deletes finished jobs (and their events) older than before, but
// always keeps the latest job of each connection.
func (s *Store) PurgeJobs(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM discovery_jobs j WHERE finished_at < $1
		AND id <> (SELECT id FROM discovery_jobs k WHERE k.connection_id=j.connection_id ORDER BY created_at DESC, id DESC LIMIT 1)`, before)
	return tag.RowsAffected(), err
}

// PurgeChanges deletes change records older than before.
func (s *Store) PurgeChanges(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM topology_changes WHERE at < $1`, before)
	return tag.RowsAffected(), err
}

// PurgeAudit deletes audit events older than before.
func (s *Store) PurgeAudit(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM audit_events WHERE at < $1`, before)
	return tag.RowsAffected(), err
}

// PurgeCollectionRuns deletes finished collection runs older than before.
func (s *Store) PurgeCollectionRuns(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM metric_collections WHERE started_at < $1 AND status <> 'running'`, before)
	return tag.RowsAffected(), err
}
