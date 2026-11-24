package oracle

import (
	"context"
	"database/sql"
	"math"
	"strconv"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

// InsertSamples writes samples idempotently: MERGE ... WHEN NOT MATCHED
// leaves an existing (resource, metric, ts) row unchanged, and the join on
// resources rejects samples for another tenant's resource. Returns new rows.
func (s *Store) InsertSamples(ctx context.Context, samples []metrics.Sample) (int64, error) {
	if len(samples) == 0 {
		return 0, nil
	}
	var total int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, `MERGE INTO metric_samples m
			USING (SELECT r.id rid, :met met, :ts ts, r.tenant_id ten FROM resources r WHERE r.id = :rid AND r.tenant_id = :ten) u
			ON (m.resource_id = u.rid AND m.metric = u.met AND m.ts = u.ts)
			WHEN NOT MATCHED THEN INSERT (resource_id, metric, ts, tenant_id, value, source, collector)
				VALUES (u.rid, u.met, u.ts, u.ten, :val, :src, :col)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, x := range samples {
			res, err := st.ExecContext(ctx, sql.Named("met", x.Metric), sql.Named("ts", ts(x.Timestamp)), sql.Named("rid", x.ResourceID),
				sql.Named("ten", x.TenantID), sql.Named("val", x.Value), sql.Named("src", string(x.Source)), sql.Named("col", x.Collector))
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			total += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// LatestSampleTimes returns the newest sample time per resource.
func (s *Store) LatestSampleTimes(ctx context.Context, tenantID string, ids []string) (map[string]time.Time, error) {
	q := &qb{}
	q.w(`SELECT resource_id, MAX(ts) FROM metric_samples WHERE tenant_id=`+q.b(tenantID)+` AND `).in("resource_id", ids).w(` GROUP BY resource_id`)
	rows, err := s.query(ctx, q)
	type pair struct {
		id string
		t  time.Time
	}
	ps, err := collect(rows, err, func(r scanner) (pair, error) {
		var p pair
		err := r.Scan(&p.id, utc{&p.t})
		return p, err
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(ps))
	for _, p := range ps {
		out[p.id] = p.t
	}
	return out, nil
}

func scanSample(tenantID string) func(scanner) (metrics.Sample, error) {
	return func(r scanner) (metrics.Sample, error) {
		x := metrics.Sample{TenantID: tenantID}
		var src string
		err := r.Scan(&x.ResourceID, &x.Metric, utc{&x.Timestamp}, &x.Value, &src, &x.Collector)
		x.Source = metrics.Source(src)
		return x, err
	}
}

// LatestSamples returns the newest sample of each metric of a resource.
func (s *Store) LatestSamples(ctx context.Context, tenantID, resourceID string) (map[string]metrics.Sample, error) {
	q := &qb{}
	q.w(`SELECT resource_id, metric, ts, value, source, collector FROM (
			SELECT m.*, ROW_NUMBER() OVER (PARTITION BY metric ORDER BY ts DESC) rn FROM metric_samples m
			WHERE tenant_id=` + q.b(tenantID) + ` AND resource_id=` + q.b(resourceID) + `) WHERE rn = 1`)
	rows, err := s.query(ctx, q)
	xs, err := collect(rows, err, scanSample(tenantID))
	if err != nil {
		return nil, err
	}
	out := make(map[string]metrics.Sample, len(xs))
	for _, x := range xs {
		out[x.Metric] = x
	}
	return out, nil
}

// LatestSamplesFor returns the newest sample per (resource, metric). For
// each pair a correlated scalar MAX(ts) is answered by a min/max probe of
// the (resource_id, metric, ts) primary key, then the row is fetched by key:
// O(R*M*log n), like the PostgreSQL LATERAL query. (A CROSS APPLY correlated
// to the TABLE() collection returned no rows on Oracle 23ai, even with
// literals, so it is not used.) Resources are processed in chunks.
func (s *Store) LatestSamplesFor(ctx context.Context, tenantID string, resourceIDs, metricNames []string) (map[string]map[string]metrics.Sample, error) {
	out := map[string]map[string]metrics.Sample{}
	if len(resourceIDs) == 0 || len(metricNames) == 0 {
		return out, nil
	}
	list := func(q *qb, vals []string) {
		q.w(`TABLE(sys.odcivarchar2list(`)
		for i, v := range vals {
			if i > 0 {
				q.w(",")
			}
			q.w(q.b(v))
		}
		q.w(`))`)
	}
	for start := 0; start < len(resourceIDs); start += 200 {
		chunk := resourceIDs[start:min(start+200, len(resourceIDs))]
		q := &qb{}
		tenant := q.b(tenantID)
		q.w(`SELECT p.rid, p.met, s.ts, s.value, s.source, s.collector FROM (
				SELECT r.column_value rid, m.column_value met,
					(SELECT MAX(x.ts) FROM metric_samples x WHERE x.resource_id = r.column_value AND x.metric = m.column_value AND x.tenant_id = ` + tenant + `) mts
				FROM `)
		list(q, chunk)
		q.w(` r CROSS JOIN `)
		list(q, metricNames)
		q.w(` m) p
			JOIN metric_samples s ON s.resource_id = p.rid AND s.metric = p.met AND s.ts = p.mts AND s.tenant_id = ` + tenant)
		rows, err := s.query(ctx, q)
		xs, err := collect(rows, err, scanSample(tenantID))
		if err != nil {
			return nil, err
		}
		for _, x := range xs {
			if out[x.ResourceID] == nil {
				out[x.ResourceID] = map[string]metrics.Sample{}
			}
			out[x.ResourceID][x.Metric] = x
		}
	}
	return out, nil
}

// epochExpr converts a UTC TIMESTAMP column to epoch seconds. DATE
// subtraction yields fractional days (5 minutes = 0.003472…), whose product
// with 86400 is not exact, so whole seconds are rounded before the
// sub-second part is added back; without it a sample exactly on a bucket
// boundary fell into the previous bucket (found by the contract test).
const epochExpr = `(ROUND((CAST(ts AS DATE) - DATE '1970-01-01') * 86400) + (EXTRACT(SECOND FROM ts) - TRUNC(EXTRACT(SECOND FROM ts))))`

// QuerySeries buckets samples into step-aligned bins (aligned to the Unix
// epoch, which matches PostgreSQL date_bin with origin 2000-01-01 for any
// step that divides a day) with avg/min/max/count per bin.
func (s *Store) QuerySeries(ctx context.Context, tenantID, resourceID, metric string, from, to time.Time, step time.Duration) (store.Series, error) {
	out := store.Series{Points: []store.Point{}, Sources: []metrics.Source{}}
	stepSec := int64(math.Max(1, step.Seconds()))
	q := &qb{}
	q.w(`SELECT FLOOR(` + epochExpr + ` / ` + strconv.FormatInt(stepSec, 10) + `) * ` + strconv.FormatInt(stepSec, 10) + ` AS b,
			AVG(value), MIN(value), MAX(value), count(*)
		FROM metric_samples WHERE tenant_id=` + q.b(tenantID) + ` AND resource_id=` + q.b(resourceID) + ` AND metric=` + q.b(metric) +
		` AND ts >= ` + q.b(ts(from)) + ` AND ts < ` + q.b(ts(to)) + ` GROUP BY FLOOR(` + epochExpr + ` / ` + strconv.FormatInt(stepSec, 10) + `) ORDER BY b`)
	rows, err := s.query(ctx, q)
	pts, err := collect(rows, err, func(r scanner) (store.Point, error) {
		var p store.Point
		var b float64
		err := r.Scan(&b, &p.Avg, &p.Min, &p.Max, &p.Count)
		p.Timestamp = time.Unix(int64(b), 0).UTC()
		return p, err
	})
	if err != nil {
		return out, err
	}
	if pts != nil {
		out.Points = pts
	}
	q = &qb{}
	q.w(`SELECT DISTINCT source FROM metric_samples WHERE tenant_id=` + q.b(tenantID) + ` AND resource_id=` + q.b(resourceID) +
		` AND metric=` + q.b(metric) + ` AND ts >= ` + q.b(ts(from)) + ` AND ts < ` + q.b(ts(to)) + ` ORDER BY source`)
	rows, err = s.query(ctx, q)
	srcs, err := collect(rows, err, func(r scanner) (metrics.Source, error) {
		var x string
		err := r.Scan(&x)
		return metrics.Source(x), err
	})
	if srcs != nil {
		out.Sources = srcs
	}
	return out, err
}

const runCols = `id, tenant_id, connection_id, trigger_kind, status, requested_by, collector, resources, samples, error, started_at, finished_at`

func scanRun(r scanner) (store.CollectionRun, error) {
	var c store.CollectionRun
	err := r.Scan(&c.ID, &c.TenantID, &c.ConnectionID, &c.Trigger, &c.Status, &c.RequestedBy, &c.Collector, &c.Resources, &c.Samples,
		nullStr{&c.Error}, utc{&c.StartedAt}, utcPtr{&c.FinishedAt})
	return c, err
}

// CreateCollectionRun inserts a running collection run.
func (s *Store) CreateCollectionRun(ctx context.Context, r store.CollectionRun) error {
	q := &qb{}
	q.w(`INSERT INTO metric_collections (` + runCols + `) VALUES (` + q.b(r.ID) + `,` + q.b(r.TenantID) + `,` + q.b(r.ConnectionID) + `,` +
		q.b(r.Trigger) + `,'running',` + q.b(r.RequestedBy) + `,` + q.b(r.Collector) + `,0,0,NULL,` + q.b(ts(r.StartedAt)) + `,NULL)`)
	_, err := s.exec(ctx, q)
	return err
}

// FinishCollectionRun records the outcome.
func (s *Store) FinishCollectionRun(ctx context.Context, tenantID, id, status string, resources, samples int, errMsg string, now time.Time) error {
	q := &qb{}
	q.w(`UPDATE metric_collections SET status=` + q.b(status) + `, resources=` + q.b(resources) + `, samples=` + q.b(samples) +
		`, error=` + q.b(str(truncate(errMsg, 2000))) + `, finished_at=` + q.b(ts(now)) + ` WHERE tenant_id=` + q.b(tenantID) + ` AND id=` + q.b(id))
	_, err := s.exec(ctx, q)
	return err
}

// GetCollectionRun returns one run.
func (s *Store) GetCollectionRun(ctx context.Context, tenantID, id string) (store.CollectionRun, error) {
	q := &qb{}
	q.w(`SELECT ` + runCols + ` FROM metric_collections WHERE tenant_id=` + q.b(tenantID) + ` AND id=` + q.b(id))
	r, err := scanRun(s.db.QueryRowContext(ctx, q.String(), q.args...))
	return r, notFound(err)
}

// ListCollectionRuns pages newest first.
func (s *Store) ListCollectionRuns(ctx context.Context, tenantID, connectionID string, p store.Page) ([]store.CollectionRun, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 1)
	if err != nil {
		return nil, "", err
	}
	q := &qb{}
	q.w(`SELECT ` + runCols + ` FROM metric_collections WHERE tenant_id=` + q.b(tenantID))
	if connectionID != "" {
		q.w(` AND connection_id=` + q.b(connectionID))
	}
	if keys != nil {
		q.w(` AND id < ` + q.b(keys[0]))
	}
	q.w(` ORDER BY id DESC`)
	pageLimit(q, p.Limit+1)
	rows, err := s.query(ctx, q)
	out, err := collect(rows, err, scanRun)
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
	var d any = clob(nil)
	if len(e.Details) > 0 {
		d = clob(e.Details)
	}
	q := &qb{}
	q.w(`INSERT INTO audit_events (id, tenant_id, actor, action, target_type, target_id, request_id, at, details) VALUES (` +
		q.b(e.ID) + `,` + q.b(e.TenantID) + `,` + q.b(e.Actor) + `,` + q.b(e.Action) + `,` + q.b(e.TargetType) + `,` + q.b(str(e.TargetID)) + `,` +
		q.b(str(e.RequestID)) + `,` + q.b(ts(e.At)) + `,` + q.b(d) + `)`)
	_, err := s.exec(ctx, q)
	return err
}

// ListAudit pages newest first on (at, id).
func (s *Store) ListAudit(ctx context.Context, tenantID string, p store.Page) ([]store.AuditEvent, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 2)
	if err != nil {
		return nil, "", err
	}
	q := &qb{}
	q.w(`SELECT id, tenant_id, actor, action, target_type, target_id, request_id, at, details FROM audit_events WHERE tenant_id=` + q.b(tenantID))
	if keys != nil {
		t, err := time.Parse(time.RFC3339Nano, keys[0])
		if err != nil {
			return nil, "", store.ErrInvalidCursor
		}
		a, b := q.b(ts(t)), q.b(keys[1])
		q.w(` AND (at < ` + a + ` OR (at = ` + a + ` AND id < ` + b + `))`)
	}
	q.w(` ORDER BY at DESC, id DESC`)
	pageLimit(q, p.Limit+1)
	rows, err := s.query(ctx, q)
	out, err := collect(rows, err, func(r scanner) (store.AuditEvent, error) {
		var e store.AuditEvent
		err := r.Scan(&e.ID, &e.TenantID, &e.Actor, &e.Action, &e.TargetType, nullStr{&e.TargetID}, nullStr{&e.RequestID}, utc{&e.At}, jsonScan{&e.Details})
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

// PurgeMetricSamples deletes samples older than before in batches.
func (s *Store) PurgeMetricSamples(ctx context.Context, before time.Time, batch int) (int64, error) {
	var total int64
	for {
		q := &qb{}
		q.w(`DELETE FROM metric_samples WHERE ts < ` + q.b(ts(before)) + ` AND ROWNUM <= ` + q.b(batch))
		n, err := s.exec(ctx, q)
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(batch) || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}

// PurgeMissingResources hard-deletes resources missing since before.
func (s *Store) PurgeMissingResources(ctx context.Context, before time.Time) (int64, error) {
	q := &qb{}
	q.w(`DELETE FROM relationships WHERE status='missing' AND missing_since < ` + q.b(ts(before)))
	if _, err := s.exec(ctx, q); err != nil {
		return 0, err
	}
	q = &qb{}
	q.w(`DELETE FROM resources WHERE status='missing' AND missing_since < ` + q.b(ts(before)))
	return s.exec(ctx, q)
}

// PurgeJobs deletes finished jobs older than before, keeping the latest job
// of each connection.
func (s *Store) PurgeJobs(ctx context.Context, before time.Time) (int64, error) {
	q := &qb{}
	q.w(`DELETE FROM discovery_jobs j WHERE finished_at < ` + q.b(ts(before)) + `
		AND id <> (SELECT k.id FROM discovery_jobs k WHERE k.connection_id = j.connection_id ORDER BY k.created_at DESC, k.id DESC FETCH FIRST 1 ROWS ONLY)`)
	return s.exec(ctx, q)
}

// PurgeChanges deletes change records older than before.
func (s *Store) PurgeChanges(ctx context.Context, before time.Time) (int64, error) {
	q := &qb{}
	q.w(`DELETE FROM topology_changes WHERE at < ` + q.b(ts(before)))
	return s.exec(ctx, q)
}

// PurgeAudit deletes audit events older than before.
func (s *Store) PurgeAudit(ctx context.Context, before time.Time) (int64, error) {
	q := &qb{}
	q.w(`DELETE FROM audit_events WHERE at < ` + q.b(ts(before)))
	return s.exec(ctx, q)
}

// PurgeCollectionRuns deletes finished collection runs older than before.
func (s *Store) PurgeCollectionRuns(ctx context.Context, before time.Time) (int64, error) {
	q := &qb{}
	q.w(`DELETE FROM metric_collections WHERE started_at < ` + q.b(ts(before)) + ` AND status <> 'running'`)
	return s.exec(ctx, q)
}
