package postgres

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

const batchSize = 1000

const resCols = `id, tenant_id, connection_id, provider, scope, external_id, type, native_type, name, region, labels, properties,
	content_hash, status, first_seen_at, last_seen_at, missing_since, last_job_id`

func scanRes(row pgx.Row) (topology.Resource, error) {
	var r topology.Resource
	var labels, props []byte
	err := row.Scan(&r.ID, &r.TenantID, &r.ConnectionID, &r.Provider, &r.Scope, &r.ExternalID, &r.Type, &r.NativeType, &r.Name, &r.Region,
		&labels, &props, &r.ContentHash, &r.Status, &r.FirstSeenAt, &r.LastSeenAt, &r.MissingSince, &r.LastJobID)
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal(labels, &r.Labels)
	_ = json.Unmarshal(props, &r.Properties)
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	if r.Properties == nil {
		r.Properties = map[string]any{}
	}
	return r, nil
}

// LoadExistingState loads what reconciliation needs: rows for the snapshot's
// IDs plus all active rows owned by the connection.
func (s *Store) LoadExistingState(ctx context.Context, tenantID, connectionID string, resourceIDs, edgeIDs []string) (map[string]topology.ExistingState, map[string]topology.ExistingState, error) {
	res := map[string]topology.ExistingState{}
	rows, err := s.pool.Query(ctx, `SELECT id, content_hash, status, connection_id FROM resources
		WHERE tenant_id=$1 AND (id = ANY($2) OR (connection_id=$3 AND status='active'))`, tenantID, resourceIDs, connectionID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var e topology.ExistingState
		if err := rows.Scan(&e.ID, &e.ContentHash, &e.Status, &e.ConnectionID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		res[e.ID] = e
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	edges := map[string]topology.ExistingState{}
	rows, err = s.pool.Query(ctx, `SELECT id, status, connection_id FROM relationships
		WHERE tenant_id=$1 AND (id = ANY($2) OR (connection_id=$3 AND status='active'))`, tenantID, edgeIDs, connectionID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e topology.ExistingState
		if err := rows.Scan(&e.ID, &e.Status, &e.ConnectionID); err != nil {
			return nil, nil, err
		}
		edges[e.ID] = e
	}
	return res, edges, rows.Err()
}

// ApplySnapshot persists a reconciled snapshot in one transaction:
// idempotent upserts of resources and edges, missing-marking (complete runs
// only, and only for rows still owned by this connection and not touched by
// this job), and the change log.
func (s *Store) ApplySnapshot(ctx context.Context, in store.ApplyInput) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '120s'`); err != nil {
			return err
		}
		for start := 0; start < len(in.Resources); start += batchSize {
			end := min(start+batchSize, len(in.Resources))
			if err := upsertResources(ctx, tx, in, in.Resources[start:end]); err != nil {
				return err
			}
		}
		for start := 0; start < len(in.Relationships); start += batchSize {
			end := min(start+batchSize, len(in.Relationships))
			if err := upsertEdges(ctx, tx, in, in.Relationships[start:end]); err != nil {
				return err
			}
		}
		if in.Complete {
			if len(in.Plan.Missing) > 0 {
				if _, err := tx.Exec(ctx, `UPDATE resources SET status='missing', missing_since=$3
					WHERE tenant_id=$1 AND id = ANY($2) AND status='active' AND connection_id=$4 AND last_job_id <> $5`,
					in.TenantID, in.Plan.Missing, in.Now, in.ConnectionID, in.JobID); err != nil {
					return err
				}
			}
			if len(in.Plan.EdgesMissing) > 0 {
				if _, err := tx.Exec(ctx, `UPDATE relationships SET status='missing', missing_since=$3
					WHERE tenant_id=$1 AND id = ANY($2) AND status='active' AND connection_id=$4 AND last_job_id <> $5`,
					in.TenantID, in.Plan.EdgesMissing, in.Now, in.ConnectionID, in.JobID); err != nil {
					return err
				}
			}
		}
		var ids, kinds []string
		add := func(list []string, kind topology.ChangeKind) {
			for _, id := range list {
				ids = append(ids, id)
				kinds = append(kinds, string(kind))
			}
		}
		add(in.Plan.Added, topology.ChangeAdded)
		add(in.Plan.Changed, topology.ChangeChanged)
		add(in.Plan.Reactivated, topology.ChangeReactivated)
		if in.Complete {
			add(in.Plan.Missing, topology.ChangeMissing)
		}
		if len(ids) > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO topology_changes (tenant_id, job_id, resource_id, kind, at)
				SELECT $1, $2, u.id, u.kind, $3 FROM unnest($4::text[], $5::text[]) AS u(id, kind)`,
				in.TenantID, in.JobID, in.Now, ids, kinds); err != nil {
				return err
			}
		}
		return nil
	})
}

func upsertResources(ctx context.Context, tx pgx.Tx, in store.ApplyInput, rs []topology.Resource) error {
	n := len(rs)
	cols := make([][]string, 11)
	for i := range cols {
		cols[i] = make([]string, n)
	}
	for i, r := range rs {
		cols[0][i], cols[1][i], cols[2][i], cols[3][i] = r.ID, r.Provider, r.Scope, r.ExternalID
		cols[4][i], cols[5][i], cols[6][i], cols[7][i] = r.Type, r.NativeType, r.Name, r.Region
		cols[8][i], cols[9][i], cols[10][i] = string(jsonb(r.Labels)), string(jsonb(r.Properties)), r.ContentHash
	}
	_, err := tx.Exec(ctx, `INSERT INTO resources (`+resCols+`)
		SELECT u.id, $1, $2, u.provider, u.scope, u.external_id, u.type, u.native_type, u.name, u.region, u.labels::jsonb, u.properties::jsonb,
		       u.hash, 'active', $3, $3, NULL, $4
		FROM unnest($5::text[], $6::text[], $7::text[], $8::text[], $9::text[], $10::text[], $11::text[], $12::text[], $13::text[], $14::text[], $15::text[])
		     AS u(id, provider, scope, external_id, type, native_type, name, region, labels, properties, hash)
		ON CONFLICT (id) DO UPDATE SET
			connection_id = EXCLUDED.connection_id, type = EXCLUDED.type, native_type = EXCLUDED.native_type,
			name = EXCLUDED.name, region = EXCLUDED.region, labels = EXCLUDED.labels, properties = EXCLUDED.properties,
			content_hash = EXCLUDED.content_hash, status = 'active', last_seen_at = EXCLUDED.last_seen_at,
			missing_since = NULL, last_job_id = EXCLUDED.last_job_id`,
		in.TenantID, in.ConnectionID, in.Now, in.JobID,
		cols[0], cols[1], cols[2], cols[3], cols[4], cols[5], cols[6], cols[7], cols[8], cols[9], cols[10])
	return err
}

func upsertEdges(ctx context.Context, tx pgx.Tx, in store.ApplyInput, es []topology.Relationship) error {
	n := len(es)
	ids, src, dst, typ := make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	for i, e := range es {
		ids[i], src[i], dst[i], typ[i] = e.ID, e.SourceID, e.TargetID, string(e.Type)
	}
	_, err := tx.Exec(ctx, `INSERT INTO relationships (id, tenant_id, connection_id, source_id, target_id, type, status, first_seen_at, last_seen_at, missing_since, last_job_id)
		SELECT u.id, $1, $2, u.src, u.dst, u.typ, 'active', $3, $3, NULL, $4
		FROM unnest($5::text[], $6::text[], $7::text[], $8::text[]) AS u(id, src, dst, typ)
		ON CONFLICT (id) DO UPDATE SET connection_id = EXCLUDED.connection_id, status = 'active',
			last_seen_at = EXCLUDED.last_seen_at, missing_since = NULL, last_job_id = EXCLUDED.last_job_id`,
		in.TenantID, in.ConnectionID, in.Now, in.JobID, ids, src, dst, typ)
	return err
}

// GetResource returns one resource.
func (s *Store) GetResource(ctx context.Context, tenantID, id string) (topology.Resource, error) {
	r, err := scanRes(s.pool.QueryRow(ctx, `SELECT `+resCols+` FROM resources WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	return r, notFound(err)
}

// GetResources returns resources by ID (unknown IDs are skipped).
func (s *Store) GetResources(ctx context.Context, tenantID string, ids []string) ([]topology.Resource, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+resCols+` FROM resources WHERE tenant_id=$1 AND id = ANY($2) ORDER BY name, id`, tenantID, ids)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanRes)
}

func likePattern(q string) string {
	if q == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

const resFilterSQL = ` tenant_id=$1
	AND ($2='' OR name ILIKE $2 OR external_id ILIKE $2)
	AND ($3='' OR type=$3) AND ($4='' OR provider=$4) AND ($5='' OR region=$5)
	AND ($6='' OR status=$6) AND ($7='' OR connection_id=$7)
	AND ($8='' OR labels @> jsonb_build_object($8::text, $9::text))
	AND (cardinality($10::text[]) = 0 OR type = ANY($10::text[]))`

func resFilterArgs(tenantID string, f store.ResourceFilter) []any {
	types := f.Types
	if types == nil {
		types = []string{}
	}
	return []any{tenantID, likePattern(f.Query), f.Type, f.Provider, f.Region, f.Status, f.ConnectionID, f.LabelKey, f.LabelValue, types}
}

// ListResources pages on (name, id).
func (s *Store) ListResources(ctx context.Context, tenantID string, f store.ResourceFilter, p store.Page) ([]topology.Resource, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 2)
	if err != nil {
		return nil, "", err
	}
	var afterName *string
	afterID := ""
	if keys != nil {
		afterName, afterID = &keys[0], keys[1]
	}
	args := append(resFilterArgs(tenantID, f), afterName, afterID, p.Limit+1)
	rows, err := s.pool.Query(ctx, `SELECT `+resCols+` FROM resources WHERE`+resFilterSQL+`
		AND ($11::text IS NULL OR (name, id) > ($11, $12)) ORDER BY name, id LIMIT $13`, args...)
	if err != nil {
		return nil, "", err
	}
	out, err := collect(rows, scanRes)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		last := out[len(out)-1]
		next = store.EncodeCursor(last.Name, last.ID)
	}
	return out, next, nil
}

const relViewCols = `r.id, r.tenant_id, r.connection_id, r.source_id, r.target_id, r.type, r.status, r.first_seen_at, r.last_seen_at,
	r.missing_since, r.last_job_id, s.name, s.type, t.name, t.type`

func scanRelView(row pgx.Row) (store.RelationshipView, error) {
	var v store.RelationshipView
	err := row.Scan(&v.ID, &v.TenantID, &v.ConnectionID, &v.SourceID, &v.TargetID, &v.Type, &v.Status, &v.FirstSeenAt, &v.LastSeenAt,
		&v.MissingSince, &v.LastJobID, &v.SourceName, &v.SourceType, &v.TargetName, &v.TargetType)
	return v, err
}

// ListRelationships pages on id.
func (s *Store) ListRelationships(ctx context.Context, tenantID string, f store.RelationshipFilter, p store.Page) ([]store.RelationshipView, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 1)
	if err != nil {
		return nil, "", err
	}
	after := ""
	if keys != nil {
		after = keys[0]
	}
	rows, err := s.pool.Query(ctx, `SELECT `+relViewCols+` FROM relationships r
		JOIN resources s ON s.id = r.source_id JOIN resources t ON t.id = r.target_id
		WHERE r.tenant_id=$1 AND ($2='' OR r.type=$2) AND ($3='' OR r.status=$3)
		  AND ($4='' OR r.source_id=$4 OR r.target_id=$4) AND ($5='' OR r.connection_id=$5) AND r.id > $6
		ORDER BY r.id LIMIT $7`, tenantID, f.Type, f.Status, f.ResourceID, f.ConnectionID, after, p.Limit+1)
	if err != nil {
		return nil, "", err
	}
	out, err := collect(rows, scanRelView)
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

func scanEdge(row pgx.Row) (topology.Edge, error) {
	var e topology.Edge
	err := row.Scan(&e.ID, &e.From, &e.To, &e.Type)
	return e, err
}

// EdgesTouching returns edges with either endpoint in ids (capped at 5000).
func (s *Store) EdgesTouching(ctx context.Context, tenantID string, ids []string, includeMissing bool) ([]topology.Edge, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, source_id, target_id, type FROM relationships
		WHERE tenant_id=$1 AND (source_id = ANY($2) OR target_id = ANY($2)) AND ($3 OR status='active')
		ORDER BY id LIMIT 5000`, tenantID, ids, includeMissing)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanEdge)
}

// Graph returns up to maxNodes resources matching f and the edges among them.
func (s *Store) Graph(ctx context.Context, tenantID string, f store.ResourceFilter, maxNodes int) ([]topology.Resource, []topology.Edge, bool, error) {
	args := append(resFilterArgs(tenantID, f), maxNodes+1)
	rows, err := s.pool.Query(ctx, `SELECT `+resCols+` FROM resources WHERE`+resFilterSQL+` ORDER BY type, name, id LIMIT $11`, args...)
	if err != nil {
		return nil, nil, false, err
	}
	res, err := collect(rows, scanRes)
	if err != nil {
		return nil, nil, false, err
	}
	truncated := len(res) > maxNodes
	if truncated {
		res = res[:maxNodes]
	}
	ids := make([]string, len(res))
	for i, r := range res {
		ids[i] = r.ID
	}
	includeMissing := f.Status == "" || f.Status == string(topology.StatusMissing)
	rows, err = s.pool.Query(ctx, `SELECT id, source_id, target_id, type FROM relationships
		WHERE tenant_id=$1 AND source_id = ANY($2) AND target_id = ANY($2) AND ($3 OR status='active') ORDER BY id`, tenantID, ids, includeMissing)
	if err != nil {
		return nil, nil, false, err
	}
	edges, err := collect(rows, scanEdge)
	return res, edges, truncated, err
}

func countBy(ctx context.Context, s *Store, sql string, args ...any) ([]store.CountBy, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	out, err := collect(rows, func(r pgx.Row) (store.CountBy, error) {
		var c store.CountBy
		err := r.Scan(&c.Key, &c.Count)
		return c, err
	})
	if out == nil {
		out = []store.CountBy{}
	}
	return out, err
}

// facetDims maps facet names to SQL expressions. Label facets read well-known
// label keys used across providers.
var facetDims = []struct{ name, expr string }{
	{"type", "type"}, {"provider", "provider"}, {"region", "region"}, {"status", "status"},
	{"engine", "labels->>'engine'"}, {"env", "labels->>'env'"},
}

// Facets counts resources per dimension. Each dimension is counted with every
// filter applied except its own, so a selected value does not hide the
// alternatives (standard faceted-search behaviour).
func (s *Store) Facets(ctx context.Context, tenantID string, f store.ResourceFilter, limit int) (map[string][]store.CountBy, error) {
	out := map[string][]store.CountBy{}
	for _, d := range facetDims {
		g := f
		switch d.name {
		case "type":
			g.Type, g.Types = "", nil
		case "provider":
			g.Provider = ""
		case "region":
			g.Region = ""
		case "status":
			g.Status = ""
		case "engine", "env":
			if g.LabelKey == d.name {
				g.LabelKey, g.LabelValue = "", ""
			}
		}
		args := append(resFilterArgs(tenantID, g), limit)
		cs, err := countBy(ctx, s, `SELECT `+d.expr+`, count(*) FROM resources WHERE`+resFilterSQL+` AND `+d.expr+` IS NOT NULL
			GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT $11`, args...)
		if err != nil {
			return nil, err
		}
		if cs == nil {
			cs = []store.CountBy{}
		}
		out[d.name] = cs
	}
	return out, nil
}

// Summary aggregates the topology of a tenant, optionally one connection.
func (s *Store) Summary(ctx context.Context, tenantID, connectionID string) (store.Summary, error) {
	var sum store.Summary
	where := ` WHERE tenant_id=$1 AND ($2='' OR connection_id=$2)`
	err := s.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status='active'), count(*) FILTER (WHERE status='missing'),
		max(last_seen_at), min(last_seen_at) FILTER (WHERE status='active') FROM resources`+where, tenantID, connectionID).
		Scan(&sum.Resources, &sum.ActiveResources, &sum.MissingResources, &sum.LastSeenAt, &sum.OldestActiveSeen)
	if err != nil {
		return sum, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM relationships`+where+` AND status='active'`, tenantID, connectionID).Scan(&sum.Relationships); err != nil {
		return sum, err
	}
	act := where + ` AND status='active'`
	if sum.ByType, err = countBy(ctx, s, `SELECT type, count(*) FROM resources`+act+` GROUP BY type ORDER BY count(*) DESC, type`, tenantID, connectionID); err != nil {
		return sum, err
	}
	if sum.ByProvider, err = countBy(ctx, s, `SELECT provider, count(*) FROM resources`+act+` GROUP BY provider ORDER BY count(*) DESC, provider`, tenantID, connectionID); err != nil {
		return sum, err
	}
	if sum.ByRegion, err = countBy(ctx, s, `SELECT region, count(*) FROM resources`+act+` GROUP BY region ORDER BY count(*) DESC, region`, tenantID, connectionID); err != nil {
		return sum, err
	}
	sum.RelationshipTypes, err = countBy(ctx, s, `SELECT type, count(*) FROM relationships`+act+` GROUP BY type ORDER BY count(*) DESC, type`, tenantID, connectionID)
	return sum, err
}

// ListChanges pages newest first on id.
func (s *Store) ListChanges(ctx context.Context, tenantID string, f store.ChangeFilter, p store.Page) ([]store.Change, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 1)
	if err != nil {
		return nil, "", err
	}
	var before int64 = 1<<62 - 1
	if keys != nil {
		if before, err = strconv.ParseInt(keys[0], 10, 64); err != nil {
			return nil, "", store.ErrInvalidCursor
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.job_id, c.resource_id, coalesce(r.name, ''), coalesce(r.type, ''), c.kind, c.at
		FROM topology_changes c LEFT JOIN resources r ON r.id = c.resource_id
		WHERE c.tenant_id=$1 AND ($2='' OR c.job_id=$2) AND ($3='' OR c.resource_id=$3) AND c.id < $4
		ORDER BY c.id DESC LIMIT $5`, tenantID, f.JobID, f.ResourceID, before, p.Limit+1)
	if err != nil {
		return nil, "", err
	}
	out, err := collect(rows, func(r pgx.Row) (store.Change, error) {
		var c store.Change
		err := r.Scan(&c.ID, &c.JobID, &c.ResourceID, &c.Name, &c.Type, &c.Kind, &c.At)
		return c, err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		next = store.EncodeCursor(strconv.FormatInt(out[len(out)-1].ID, 10))
	}
	return out, next, nil
}

// ActiveResourcesOfTypes lists active resources of a connection for metric collection.
func (s *Store) ActiveResourcesOfTypes(ctx context.Context, tenantID, connectionID string, types []string, limit int) ([]topology.Resource, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+resCols+` FROM resources
		WHERE tenant_id=$1 AND connection_id=$2 AND status='active' AND type = ANY($3) ORDER BY id LIMIT $4`, tenantID, connectionID, types, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanRes)
}
