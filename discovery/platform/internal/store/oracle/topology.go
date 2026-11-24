package oracle

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

const resCols = `r.id, r.tenant_id, r.connection_id, r.provider, r.scope, r.external_id, r.type, r.native_type, r.name, r.region, r.labels, r.properties,
	r.content_hash, r.status, r.first_seen_at, r.last_seen_at, r.missing_since, r.last_job_id`

func scanRes(row scanner) (topology.Resource, error) {
	var r topology.Resource
	var status string
	err := row.Scan(&r.ID, &r.TenantID, &r.ConnectionID, &r.Provider, &r.Scope, &r.ExternalID, &r.Type, nullStr{&r.NativeType}, &r.Name, &r.Region,
		jsonScan{&r.Labels}, jsonScan{&r.Properties}, &r.ContentHash, &status, utc{&r.FirstSeenAt}, utc{&r.LastSeenAt}, utcPtr{&r.MissingSince}, &r.LastJobID)
	r.Status = topology.Status(status)
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	if r.Properties == nil {
		r.Properties = map[string]any{}
	}
	return r, err
}

// LoadExistingState loads the rows reconciliation needs.
func (s *Store) LoadExistingState(ctx context.Context, tenantID, connectionID string, resourceIDs, edgeIDs []string) (map[string]topology.ExistingState, map[string]topology.ExistingState, error) {
	q := &qb{}
	q.w(`SELECT id, content_hash, status, connection_id FROM resources WHERE tenant_id=` + q.b(tenantID) + ` AND (`)
	q.in("id", resourceIDs).w(` OR (connection_id=` + q.b(connectionID) + ` AND status='active'))`)
	rows, err := s.query(ctx, q)
	rs, err := collect(rows, err, func(r scanner) (topology.ExistingState, error) {
		var e topology.ExistingState
		var st string
		err := r.Scan(&e.ID, &e.ContentHash, &st, &e.ConnectionID)
		e.Status = topology.Status(st)
		return e, err
	})
	if err != nil {
		return nil, nil, err
	}
	res := make(map[string]topology.ExistingState, len(rs))
	for _, e := range rs {
		res[e.ID] = e
	}
	q = &qb{}
	q.w(`SELECT id, status, connection_id FROM relationships WHERE tenant_id=` + q.b(tenantID) + ` AND (`)
	q.in("id", edgeIDs).w(` OR (connection_id=` + q.b(connectionID) + ` AND status='active'))`)
	rows, err = s.query(ctx, q)
	es, err := collect(rows, err, func(r scanner) (topology.ExistingState, error) {
		var e topology.ExistingState
		var st string
		err := r.Scan(&e.ID, &st, &e.ConnectionID)
		e.Status = topology.Status(st)
		return e, err
	})
	if err != nil {
		return nil, nil, err
	}
	edges := make(map[string]topology.ExistingState, len(es))
	for _, e := range es {
		edges[e.ID] = e
	}
	return res, edges, nil
}

const mergeResource = `MERGE INTO resources r USING (SELECT :id id FROM dual) s ON (r.id = s.id)
	WHEN MATCHED THEN UPDATE SET connection_id = :conn, type = :typ, native_type = :ntype, name = :name, region = :region,
		labels = :labels, properties = :props, content_hash = :hash, status = 'active', last_seen_at = :now,
		missing_since = NULL, last_job_id = :job
	WHEN NOT MATCHED THEN INSERT (id, tenant_id, connection_id, provider, scope, external_id, type, native_type, name, region, labels,
		properties, content_hash, status, first_seen_at, last_seen_at, missing_since, last_job_id)
		VALUES (:id, :tenant, :conn, :provider, :scope, :ext, :typ, :ntype, :name, :region, :labels, :props, :hash, 'active', :now, :now, NULL, :job)`

const mergeEdge = `MERGE INTO relationships e USING (SELECT :id id FROM dual) s ON (e.id = s.id)
	WHEN MATCHED THEN UPDATE SET connection_id = :conn, status = 'active', last_seen_at = :now, missing_since = NULL, last_job_id = :job
	WHEN NOT MATCHED THEN INSERT (id, tenant_id, connection_id, source_id, target_id, type, status, first_seen_at, last_seen_at, missing_since, last_job_id)
		VALUES (:id, :tenant, :conn, :src, :dst, :typ, 'active', :now, :now, NULL, :job)`

// ApplySnapshot persists a reconciled snapshot in one transaction with the
// same semantics as the PostgreSQL store: idempotent upserts, label index
// refresh, missing-marking on complete runs only, and the change log.
// Statements are prepared once per transaction and executed per row.
func (s *Store) ApplySnapshot(ctx context.Context, in store.ApplyInput) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		now, job := ts(in.Now), in.JobID
		if len(in.Resources) > 0 {
			st, err := tx.PrepareContext(ctx, mergeResource)
			if err != nil {
				return err
			}
			defer st.Close()
			for _, r := range in.Resources {
				if _, err := st.ExecContext(ctx, sql.Named("id", r.ID), sql.Named("tenant", in.TenantID), sql.Named("conn", in.ConnectionID),
					sql.Named("provider", r.Provider), sql.Named("scope", r.Scope), sql.Named("ext", r.ExternalID), sql.Named("typ", r.Type),
					sql.Named("ntype", str(r.NativeType)), sql.Named("name", r.Name), sql.Named("region", r.Region),
					sql.Named("labels", clob(nonNilLabels(r.Labels))), sql.Named("props", clob(nonNilProps(r.Properties))),
					sql.Named("hash", r.ContentHash), sql.Named("now", now), sql.Named("job", job)); err != nil {
					return err
				}
			}
			if err := refreshLabels(ctx, tx, in.TenantID, in.Resources); err != nil {
				return err
			}
		}
		if len(in.Relationships) > 0 {
			st, err := tx.PrepareContext(ctx, mergeEdge)
			if err != nil {
				return err
			}
			defer st.Close()
			for _, e := range in.Relationships {
				if _, err := st.ExecContext(ctx, sql.Named("id", e.ID), sql.Named("tenant", in.TenantID), sql.Named("conn", in.ConnectionID),
					sql.Named("src", e.SourceID), sql.Named("dst", e.TargetID), sql.Named("typ", string(e.Type)),
					sql.Named("now", now), sql.Named("job", job)); err != nil {
					return err
				}
			}
		}
		if in.Complete {
			for _, t := range []struct {
				table string
				ids   []string
			}{{"resources", in.Plan.Missing}, {"relationships", in.Plan.EdgesMissing}} {
				if len(t.ids) == 0 {
					continue
				}
				q := &qb{}
				q.w(`UPDATE ` + t.table + ` SET status='missing', missing_since=` + q.b(now) + ` WHERE tenant_id=` + q.b(in.TenantID) + ` AND `)
				q.in("id", t.ids).w(` AND status='active' AND connection_id=` + q.b(in.ConnectionID) + ` AND last_job_id <> ` + q.b(job))
				if _, err := txExec(ctx, tx, q); err != nil {
					return err
				}
			}
		}
		type change struct{ id, kind string }
		var changes []change
		add := func(list []string, kind topology.ChangeKind) {
			for _, id := range list {
				changes = append(changes, change{id, string(kind)})
			}
		}
		add(in.Plan.Added, topology.ChangeAdded)
		add(in.Plan.Changed, topology.ChangeChanged)
		add(in.Plan.Reactivated, topology.ChangeReactivated)
		if in.Complete {
			add(in.Plan.Missing, topology.ChangeMissing)
		}
		if len(changes) > 0 {
			st, err := tx.PrepareContext(ctx, `INSERT INTO topology_changes (tenant_id, job_id, resource_id, kind, at) VALUES (:tenant, :job, :rid, :kind, :at)`)
			if err != nil {
				return err
			}
			defer st.Close()
			for _, c := range changes {
				if _, err := st.ExecContext(ctx, sql.Named("tenant", in.TenantID), sql.Named("job", job), sql.Named("rid", c.id),
					sql.Named("kind", c.kind), sql.Named("at", now)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func nonNilLabels(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonNilProps(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// refreshLabels rewrites resource_labels for the upserted resources.
func refreshLabels(ctx context.Context, tx *sql.Tx, tenantID string, rs []topology.Resource) error {
	ids := make([]string, len(rs))
	for i, r := range rs {
		ids[i] = r.ID
	}
	q := &qb{}
	q.w(`DELETE FROM resource_labels WHERE `).in("resource_id", ids)
	if _, err := txExec(ctx, tx, q); err != nil {
		return err
	}
	st, err := tx.PrepareContext(ctx, `INSERT INTO resource_labels (resource_id, tenant_id, k, v) VALUES (:rid, :tenant, :k, :v)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, r := range rs {
		for k, v := range r.Labels {
			if _, err := st.ExecContext(ctx, sql.Named("rid", r.ID), sql.Named("tenant", tenantID), sql.Named("k", k), sql.Named("v", str(v))); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetResource returns one resource.
func (s *Store) GetResource(ctx context.Context, tenantID, id string) (topology.Resource, error) {
	q := &qb{}
	q.w(`SELECT ` + resCols + ` FROM resources r WHERE r.tenant_id=` + q.b(tenantID) + ` AND r.id=` + q.b(id))
	r, err := scanRes(s.db.QueryRowContext(ctx, q.String(), q.args...))
	return r, notFound(err)
}

// GetResources returns resources by ID (unknown IDs are skipped).
func (s *Store) GetResources(ctx context.Context, tenantID string, ids []string) ([]topology.Resource, error) {
	q := &qb{}
	q.w(`SELECT `+resCols+` FROM resources r WHERE r.tenant_id=`+q.b(tenantID)+` AND `).in("r.id", ids).w(` ORDER BY r.name, r.id`)
	rows, err := s.query(ctx, q)
	return collect(rows, err, scanRes)
}

func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + strings.ToUpper(r.Replace(s)) + "%"
}

// resFilter appends the predicates of f (alias r). Empty fields add nothing.
func resFilter(q *qb, tenantID string, f store.ResourceFilter) {
	q.w(` r.tenant_id=` + q.b(tenantID))
	if f.Query != "" {
		p := q.b(likePattern(f.Query))
		q.w(` AND (UPPER(r.name) LIKE ` + p + ` ESCAPE '\' OR UPPER(r.external_id) LIKE ` + p + ` ESCAPE '\')`)
	}
	for _, c := range []struct{ col, v string }{{"r.type", f.Type}, {"r.provider", f.Provider}, {"r.region", f.Region}, {"r.status", f.Status}, {"r.connection_id", f.ConnectionID}} {
		if c.v != "" {
			q.w(` AND ` + c.col + `=` + q.b(c.v))
		}
	}
	if f.LabelKey != "" {
		q.w(` AND EXISTS (SELECT 1 FROM resource_labels l WHERE l.resource_id = r.id AND l.k=` + q.b(f.LabelKey))
		if f.LabelValue == "" {
			q.w(` AND l.v IS NULL)`)
		} else {
			q.w(` AND l.v=` + q.b(f.LabelValue) + `)`)
		}
	}
	if len(f.Types) > 0 {
		q.w(` AND `).in("r.type", f.Types)
	}
}

// ListResources pages on (name, id).
func (s *Store) ListResources(ctx context.Context, tenantID string, f store.ResourceFilter, p store.Page) ([]topology.Resource, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 2)
	if err != nil {
		return nil, "", err
	}
	q := &qb{}
	q.w(`SELECT ` + resCols + ` FROM resources r WHERE`)
	resFilter(q, tenantID, f)
	if keys != nil {
		n, i := q.b(keys[0]), q.b(keys[1])
		q.w(` AND (r.name > ` + n + ` OR (r.name = ` + n + ` AND r.id > ` + i + `))`)
	}
	q.w(` ORDER BY r.name, r.id`)
	pageLimit(q, p.Limit+1)
	rows, err := s.query(ctx, q)
	out, err := collect(rows, err, scanRes)
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

// Facets counts resources per dimension; each ignores its own filter.
func (s *Store) Facets(ctx context.Context, tenantID string, f store.ResourceFilter, limit int) (map[string][]store.CountBy, error) {
	out := map[string][]store.CountBy{}
	for _, d := range []string{"type", "provider", "region", "status", "engine", "env"} {
		g := f
		q := &qb{}
		switch d {
		case "type":
			g.Type, g.Types = "", nil
		case "provider":
			g.Provider = ""
		case "region":
			g.Region = ""
		case "status":
			g.Status = ""
		case "engine", "env":
			if g.LabelKey == d {
				g.LabelKey, g.LabelValue = "", ""
			}
		}
		if d == "engine" || d == "env" {
			q.w(`SELECT lf.v, count(*) FROM resources r JOIN resource_labels lf ON lf.resource_id = r.id AND lf.k=` + q.b(d) + ` WHERE`)
			resFilter(q, tenantID, g)
			q.w(` AND lf.v IS NOT NULL GROUP BY lf.v ORDER BY 2 DESC, 1`)
		} else {
			q.w(`SELECT r.` + d + `, count(*) FROM resources r WHERE`) // d is from the fixed list above
			resFilter(q, tenantID, g)
			q.w(` GROUP BY r.` + d + ` ORDER BY 2 DESC, 1`)
		}
		pageLimit(q, limit)
		cs, err := s.countBy(ctx, q)
		if err != nil {
			return nil, err
		}
		out[d] = cs
	}
	return out, nil
}

func (s *Store) countBy(ctx context.Context, q *qb) ([]store.CountBy, error) {
	rows, err := s.query(ctx, q)
	out, err := collect(rows, err, func(r scanner) (store.CountBy, error) {
		var c store.CountBy
		err := r.Scan(nullStr{&c.Key}, &c.Count)
		return c, err
	})
	if out == nil {
		out = []store.CountBy{}
	}
	return out, err
}

const relViewCols = `e.id, e.tenant_id, e.connection_id, e.source_id, e.target_id, e.type, e.status, e.first_seen_at, e.last_seen_at,
	e.missing_since, e.last_job_id, s.name, s.type, t.name, t.type`

// ListRelationships pages on id.
func (s *Store) ListRelationships(ctx context.Context, tenantID string, f store.RelationshipFilter, p store.Page) ([]store.RelationshipView, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 1)
	if err != nil {
		return nil, "", err
	}
	q := &qb{}
	q.w(`SELECT ` + relViewCols + ` FROM relationships e JOIN resources s ON s.id = e.source_id JOIN resources t ON t.id = e.target_id
		WHERE e.tenant_id=` + q.b(tenantID))
	if f.Type != "" {
		q.w(` AND e.type=` + q.b(f.Type))
	}
	if f.Status != "" {
		q.w(` AND e.status=` + q.b(f.Status))
	}
	if f.ResourceID != "" {
		id := q.b(f.ResourceID)
		q.w(` AND (e.source_id=` + id + ` OR e.target_id=` + id + `)`)
	}
	if f.ConnectionID != "" {
		q.w(` AND e.connection_id=` + q.b(f.ConnectionID))
	}
	if keys != nil {
		q.w(` AND e.id > ` + q.b(keys[0]))
	}
	q.w(` ORDER BY e.id`)
	pageLimit(q, p.Limit+1)
	rows, err := s.query(ctx, q)
	out, err := collect(rows, err, func(r scanner) (store.RelationshipView, error) {
		var v store.RelationshipView
		var typ, st string
		err := r.Scan(&v.ID, &v.TenantID, &v.ConnectionID, &v.SourceID, &v.TargetID, &typ, &st, utc{&v.FirstSeenAt}, utc{&v.LastSeenAt},
			utcPtr{&v.MissingSince}, &v.LastJobID, &v.SourceName, &v.SourceType, &v.TargetName, &v.TargetType)
		v.Type, v.Status = topology.RelationType(typ), topology.Status(st)
		return v, err
	})
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

func scanEdge(r scanner) (topology.Edge, error) {
	var e topology.Edge
	var typ string
	err := r.Scan(&e.ID, &e.From, &e.To, &typ)
	e.Type = topology.RelationType(typ)
	return e, err
}

// EdgesTouching returns edges with either endpoint in ids (capped at 5000).
func (s *Store) EdgesTouching(ctx context.Context, tenantID string, ids []string, includeMissing bool) ([]topology.Edge, error) {
	q := &qb{}
	q.w(`SELECT id, source_id, target_id, type FROM relationships WHERE tenant_id=` + q.b(tenantID) + ` AND (`)
	q.in("source_id", ids).w(` OR `).in("target_id", ids).w(`)`)
	if !includeMissing {
		q.w(` AND status='active'`)
	}
	q.w(` ORDER BY id FETCH FIRST 5000 ROWS ONLY`)
	rows, err := s.query(ctx, q)
	return collect(rows, err, scanEdge)
}

// Graph returns up to maxNodes resources matching f and the edges between them.
func (s *Store) Graph(ctx context.Context, tenantID string, f store.ResourceFilter, maxNodes int) ([]topology.Resource, []topology.Edge, bool, error) {
	q := &qb{}
	q.w(`SELECT ` + resCols + ` FROM resources r WHERE`)
	resFilter(q, tenantID, f)
	q.w(` ORDER BY r.type, r.name, r.id`)
	pageLimit(q, maxNodes+1)
	rows, err := s.query(ctx, q)
	res, err := collect(rows, err, scanRes)
	if err != nil {
		return nil, nil, false, err
	}
	truncated := len(res) > maxNodes
	if truncated {
		res = res[:maxNodes]
	}
	if len(res) == 0 {
		return res, nil, false, nil
	}
	ids := make([]string, len(res))
	for i, r := range res {
		ids[i] = r.ID
	}
	q = &qb{}
	q.w(`SELECT id, source_id, target_id, type FROM relationships WHERE tenant_id=` + q.b(tenantID) + ` AND `)
	q.in("source_id", ids).w(` AND `).in("target_id", ids)
	if !(f.Status == "" || f.Status == string(topology.StatusMissing)) {
		q.w(` AND status='active'`)
	}
	q.w(` ORDER BY id`)
	rows, err = s.query(ctx, q)
	edges, err := collect(rows, err, scanEdge)
	return res, edges, truncated, err
}

// Summary aggregates the topology of a tenant, optionally one connection.
func (s *Store) Summary(ctx context.Context, tenantID, connectionID string) (store.Summary, error) {
	var sum store.Summary
	where := func(q *qb, alias string) {
		q.w(` WHERE ` + alias + `tenant_id=` + q.b(tenantID))
		if connectionID != "" {
			q.w(` AND ` + alias + `connection_id=` + q.b(connectionID))
		}
	}
	q := &qb{}
	q.w(`SELECT count(*), NVL(SUM(CASE WHEN status='active' THEN 1 ELSE 0 END), 0), NVL(SUM(CASE WHEN status='missing' THEN 1 ELSE 0 END), 0),
		MAX(last_seen_at), MIN(CASE WHEN status='active' THEN last_seen_at END) FROM resources`)
	where(q, "")
	if err := s.db.QueryRowContext(ctx, q.String(), q.args...).Scan(&sum.Resources, &sum.ActiveResources, &sum.MissingResources,
		utcPtr{&sum.LastSeenAt}, utcPtr{&sum.OldestActiveSeen}); err != nil {
		return sum, err
	}
	q = &qb{}
	q.w(`SELECT count(*) FROM relationships`)
	where(q, "")
	q.w(` AND status='active'`)
	if err := s.db.QueryRowContext(ctx, q.String(), q.args...).Scan(&sum.Relationships); err != nil {
		return sum, err
	}
	group := func(table, col string) ([]store.CountBy, error) {
		q := &qb{}
		q.w(`SELECT ` + col + `, count(*) FROM ` + table) // fixed identifiers only
		where(q, "")
		q.w(` AND status='active' GROUP BY ` + col + ` ORDER BY 2 DESC, 1`)
		return s.countBy(ctx, q)
	}
	var err error
	if sum.ByType, err = group("resources", "type"); err != nil {
		return sum, err
	}
	if sum.ByProvider, err = group("resources", "provider"); err != nil {
		return sum, err
	}
	if sum.ByRegion, err = group("resources", "region"); err != nil {
		return sum, err
	}
	sum.RelationshipTypes, err = group("relationships", "type")
	return sum, err
}

// ListChanges pages newest first on id.
func (s *Store) ListChanges(ctx context.Context, tenantID string, f store.ChangeFilter, p store.Page) ([]store.Change, string, error) {
	keys, err := store.DecodeCursor(p.Cursor, 1)
	if err != nil {
		return nil, "", err
	}
	q := &qb{}
	q.w(`SELECT c.id, c.job_id, c.resource_id, r.name, r.type, c.kind, c.at FROM topology_changes c
		LEFT JOIN resources r ON r.id = c.resource_id WHERE c.tenant_id=` + q.b(tenantID))
	if f.JobID != "" {
		q.w(` AND c.job_id=` + q.b(f.JobID))
	}
	if f.ResourceID != "" {
		q.w(` AND c.resource_id=` + q.b(f.ResourceID))
	}
	if keys != nil {
		before, err := strconv.ParseInt(keys[0], 10, 64)
		if err != nil {
			return nil, "", store.ErrInvalidCursor
		}
		q.w(` AND c.id < ` + q.b(before))
	}
	q.w(` ORDER BY c.id DESC`)
	pageLimit(q, p.Limit+1)
	rows, err := s.query(ctx, q)
	out, err := collect(rows, err, func(r scanner) (store.Change, error) {
		var c store.Change
		err := r.Scan(&c.ID, &c.JobID, &c.ResourceID, nullStr{&c.Name}, nullStr{&c.Type}, &c.Kind, utc{&c.At})
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
	q := &qb{}
	q.w(`SELECT `+resCols+` FROM resources r WHERE r.tenant_id=`+q.b(tenantID)+` AND r.connection_id=`+q.b(connectionID)+
		` AND r.status='active' AND `).in("r.type", types).w(` ORDER BY r.id`)
	pageLimit(q, limit)
	rows, err := s.query(ctx, q)
	return collect(rows, err, scanRes)
}
