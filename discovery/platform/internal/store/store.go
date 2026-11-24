// Package store defines persistence types and the repository interfaces the
// application depends on. The only implementation in the MVP is PostgreSQL
// (package store/postgres). Every method that reads or writes tenant data
// takes the tenant ID explicitly and every query filters by it.
package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
)

// Errors shared by implementations.
var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrActiveJob     = errors.New("connection already has an active discovery job")
	ErrInvalidCursor = errors.New("invalid cursor")
	ErrNotOwner      = errors.New("job lease is held by another worker")
)

// Page is a keyset pagination request.
type Page struct {
	Limit  int
	Cursor string // opaque; produced by a previous response
}

// Cursor encodes keyset values into an opaque token.
func EncodeCursor(keys ...string) string {
	b, _ := json.Marshal(keys)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor decodes a token produced by EncodeCursor and checks arity.
func DecodeCursor(c string, n int) ([]string, error) {
	if c == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var keys []string
	if json.Unmarshal(b, &keys) != nil || len(keys) != n {
		return nil, ErrInvalidCursor
	}
	return keys, nil
}

// Connection is a named discovery target (account, project, subscription or
// cluster) with non-secret configuration and a reference to its credential.
type Connection struct {
	ID                       string         `json:"id"`
	TenantID                 string         `json:"tenantId"`
	Name                     string         `json:"name"`
	Provider                 string         `json:"provider"`
	Config                   map[string]any `json:"config"`
	CredentialRef            string         `json:"-"`
	DiscoveryIntervalMinutes int            `json:"discoveryIntervalMinutes"`
	CreatedBy                string         `json:"createdBy"`
	CreatedAt                time.Time      `json:"createdAt"`
	UpdatedAt                time.Time      `json:"updatedAt"`
}

// JobStatus is the discovery job state. See docs/design/lld.md for the state machine.
type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// Terminal reports whether s is final.
func (s JobStatus) Terminal() bool {
	return s == JobSucceeded || s == JobFailed || s == JobCancelled
}

// ValidJobStatus reports whether s is a known status.
func ValidJobStatus(s string) bool {
	switch JobStatus(s) {
	case JobQueued, JobRunning, JobSucceeded, JobFailed, JobCancelled:
		return true
	}
	return false
}

// JobProgress is updated while a job runs.
type JobProgress struct {
	Phase         string `json:"phase"`
	Pages         int    `json:"pages"`
	Resources     int    `json:"resources"`
	Relationships int    `json:"relationships"`
	Warnings      int    `json:"warnings"`
}

// Job is one discovery run.
type Job struct {
	ID              string          `json:"id"`
	TenantID        string          `json:"tenantId"`
	ConnectionID    string          `json:"connectionId"`
	Status          JobStatus       `json:"status"`
	Trigger         string          `json:"trigger"`
	RequestedBy     string          `json:"requestedBy"`
	RequestID       string          `json:"requestId"`
	IdempotencyKey  string          `json:"-"`
	Attempt         int             `json:"attempt"`
	CancelRequested bool            `json:"cancelRequested"`
	Progress        JobProgress     `json:"progress"`
	Counts          topology.Counts `json:"counts"`
	Complete        bool            `json:"complete"`
	Error           string          `json:"error,omitempty"`
	WorkerID        string          `json:"-"`
	CreatedAt       time.Time       `json:"createdAt"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	HeartbeatAt     *time.Time      `json:"heartbeatAt,omitempty"`
}

// JobEvent is a bounded, per-job progress log entry.
type JobEvent struct {
	Seq     int            `json:"seq"`
	At      time.Time      `json:"at"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// JobFilter filters job listings.
type JobFilter struct {
	ConnectionID string
	Status       string
}

// ResourceFilter filters resource listings. Empty fields do not filter.
type ResourceFilter struct {
	Query        string // case-insensitive substring of name or external ID
	Type         string
	Provider     string
	Region       string
	Status       string
	ConnectionID string
	LabelKey     string
	LabelValue   string
	Types        []string // any of these types (used for domain filters); empty means all
}

// RelationshipFilter filters relationship listings.
type RelationshipFilter struct {
	Type         string
	Status       string
	ResourceID   string // either endpoint
	ConnectionID string
}

// RelationshipView is a relationship with endpoint display fields.
type RelationshipView struct {
	topology.Relationship
	SourceName string `json:"sourceName"`
	SourceType string `json:"sourceType"`
	TargetName string `json:"targetName"`
	TargetType string `json:"targetType"`
}

// Change records a reconciliation outcome for one resource.
type Change struct {
	ID         int64     `json:"id"`
	JobID      string    `json:"jobId"`
	ResourceID string    `json:"resourceId"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Kind       string    `json:"kind"`
	At         time.Time `json:"at"`
}

// ChangeFilter filters change listings.
type ChangeFilter struct {
	JobID      string
	ResourceID string
}

// CountBy is a grouped count.
type CountBy struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// Summary is the topology overview for a tenant or connection.
type Summary struct {
	Resources         int        `json:"resources"`
	ActiveResources   int        `json:"activeResources"`
	MissingResources  int        `json:"missingResources"`
	Relationships     int        `json:"relationships"`
	ByType            []CountBy  `json:"byType"`
	ByProvider        []CountBy  `json:"byProvider"`
	ByRegion          []CountBy  `json:"byRegion"`
	RelationshipTypes []CountBy  `json:"relationshipTypes"`
	LastSeenAt        *time.Time `json:"lastSeenAt,omitempty"`
	OldestActiveSeen  *time.Time `json:"oldestActiveSeenAt,omitempty"`
}

// ApplyInput is a reconciled snapshot to persist atomically.
type ApplyInput struct {
	TenantID      string
	ConnectionID  string
	JobID         string
	Now           time.Time
	Resources     []topology.Resource
	Relationships []topology.Relationship
	Plan          topology.Plan
	Complete      bool
}

// CollectionRun is one metrics collection pass for a connection.
type CollectionRun struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenantId"`
	ConnectionID string     `json:"connectionId"`
	Trigger      string     `json:"trigger"`
	Status       string     `json:"status"`
	RequestedBy  string     `json:"requestedBy"`
	Collector    string     `json:"collector"`
	Resources    int        `json:"resources"`
	Samples      int        `json:"samples"`
	Error        string     `json:"error,omitempty"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
}

// Point is one aggregated bucket of a metric series.
type Point struct {
	Timestamp time.Time `json:"ts"`
	Avg       float64   `json:"avg"`
	Min       float64   `json:"min"`
	Max       float64   `json:"max"`
	Count     int       `json:"count"`
}

// Series is a metric query result.
type Series struct {
	Points  []Point          `json:"points"`
	Sources []metrics.Source `json:"sources"`
}

// AuditEvent records a security-relevant action.
type AuditEvent struct {
	ID         string         `json:"id"`
	TenantID   string         `json:"tenantId"`
	Actor      string         `json:"actor"`
	Action     string         `json:"action"`
	TargetType string         `json:"targetType"`
	TargetID   string         `json:"targetId"`
	RequestID  string         `json:"requestId"`
	At         time.Time      `json:"at"`
	Details    map[string]any `json:"details,omitempty"`
}

// Connections persists connection profiles.
type Connections interface {
	CreateConnection(ctx context.Context, c Connection) (Connection, error)
	GetConnection(ctx context.Context, tenantID, id string) (Connection, error)
	ListConnections(ctx context.Context, tenantID string, p Page) ([]Connection, string, error)
	// AllConnections lists connections across tenants; for workers only.
	AllConnections(ctx context.Context) ([]Connection, error)
}

// Jobs persists discovery jobs, their events and the work queue.
type Jobs interface {
	// CreateJob inserts a queued job. If IdempotencyKey matches an existing
	// job of the tenant it returns that job with created=false. If the
	// connection already has a queued or running job it returns ErrActiveJob
	// and the active job.
	CreateJob(ctx context.Context, j Job) (job Job, created bool, err error)
	GetJob(ctx context.Context, tenantID, id string) (Job, error)
	ListJobs(ctx context.Context, tenantID string, f JobFilter, p Page) ([]Job, string, error)
	LatestJobs(ctx context.Context, tenantID string) (map[string]Job, error)
	CountSucceededJobs(ctx context.Context, tenantID, connectionID string) (int, error)
	// ClaimJob atomically moves the oldest queued job to running.
	ClaimJob(ctx context.Context, workerID string, now time.Time) (Job, bool, error)
	// Heartbeat extends the lease, stores progress and reports cancellation.
	Heartbeat(ctx context.Context, jobID, workerID string, pr JobProgress, now time.Time) (cancelRequested bool, err error)
	FinishJob(ctx context.Context, jobID, workerID string, status JobStatus, counts topology.Counts, pr JobProgress, complete bool, errMsg string, now time.Time) error
	RequestCancel(ctx context.Context, tenantID, id string, now time.Time) (Job, error)
	// RecoverExpiredJobs requeues running jobs whose lease expired, or fails
	// them once attempts are exhausted. Discovery is read-only, so a re-run
	// is safe.
	RecoverExpiredJobs(ctx context.Context, leaseExpiredBefore time.Time, maxAttempts int, now time.Time) (requeued, failed int, err error)
	AppendJobEvent(ctx context.Context, tenantID, jobID string, e JobEvent, maxEvents int) error
	ListJobEvents(ctx context.Context, tenantID, jobID string, afterSeq, limit int) ([]JobEvent, error)
}

// Topology persists resources, relationships and change history.
type Topology interface {
	LoadExistingState(ctx context.Context, tenantID, connectionID string, resourceIDs, edgeIDs []string) (map[string]topology.ExistingState, map[string]topology.ExistingState, error)
	ApplySnapshot(ctx context.Context, in ApplyInput) error
	GetResource(ctx context.Context, tenantID, id string) (topology.Resource, error)
	GetResources(ctx context.Context, tenantID string, ids []string) ([]topology.Resource, error)
	ListResources(ctx context.Context, tenantID string, f ResourceFilter, p Page) ([]topology.Resource, string, error)
	Facets(ctx context.Context, tenantID string, f ResourceFilter, limit int) (map[string][]CountBy, error)
	ListRelationships(ctx context.Context, tenantID string, f RelationshipFilter, p Page) ([]RelationshipView, string, error)
	EdgesTouching(ctx context.Context, tenantID string, ids []string, includeMissing bool) ([]topology.Edge, error)
	Graph(ctx context.Context, tenantID string, f ResourceFilter, maxNodes int) ([]topology.Resource, []topology.Edge, bool, error)
	Summary(ctx context.Context, tenantID, connectionID string) (Summary, error)
	ListChanges(ctx context.Context, tenantID string, f ChangeFilter, p Page) ([]Change, string, error)
	ActiveResourcesOfTypes(ctx context.Context, tenantID, connectionID string, types []string, limit int) ([]topology.Resource, error)
}

// Metrics persists metric samples and collection runs.
type Metrics interface {
	InsertSamples(ctx context.Context, samples []metrics.Sample) (int64, error)
	LatestSampleTimes(ctx context.Context, tenantID string, resourceIDs []string) (map[string]time.Time, error)
	LatestSamples(ctx context.Context, tenantID, resourceID string) (map[string]metrics.Sample, error)
	LatestSamplesFor(ctx context.Context, tenantID string, resourceIDs, metricNames []string) (map[string]map[string]metrics.Sample, error)
	QuerySeries(ctx context.Context, tenantID, resourceID, metric string, from, to time.Time, step time.Duration) (Series, error)
	CreateCollectionRun(ctx context.Context, r CollectionRun) error
	FinishCollectionRun(ctx context.Context, tenantID, id, status string, resources, samples int, errMsg string, now time.Time) error
	GetCollectionRun(ctx context.Context, tenantID, id string) (CollectionRun, error)
	ListCollectionRuns(ctx context.Context, tenantID, connectionID string, p Page) ([]CollectionRun, string, error)
}

// Audit persists audit events.
type Audit interface {
	InsertAudit(ctx context.Context, e AuditEvent) error
	ListAudit(ctx context.Context, tenantID string, p Page) ([]AuditEvent, string, error)
}

// Retention deletes aged data in bounded batches.
type Retention interface {
	PurgeMetricSamples(ctx context.Context, before time.Time, batch int) (int64, error)
	PurgeMissingResources(ctx context.Context, missingBefore time.Time) (int64, error)
	PurgeJobs(ctx context.Context, finishedBefore time.Time) (int64, error)
	PurgeChanges(ctx context.Context, before time.Time) (int64, error)
	PurgeAudit(ctx context.Context, before time.Time) (int64, error)
	PurgeCollectionRuns(ctx context.Context, before time.Time) (int64, error)
}

// Store is the full repository set.
type Store interface {
	Connections
	Jobs
	Topology
	Metrics
	Audit
	Retention
	Ping(ctx context.Context) error
	Close()
}

// MigrationState is one schema migration and when it was applied (nil = pending).
type MigrationState struct {
	Version   int
	Name      string
	AppliedAt *time.Time
}

// Backend is a Store plus the schema management of its database. Each
// supported database (PostgreSQL, Oracle) provides one; the application and
// CLI depend only on this interface.
type Backend interface {
	Store
	// Driver names the database ("postgres", "oracle").
	Driver() string
	// MigrateUp applies pending forward-only migrations and returns them.
	MigrateUp(ctx context.Context) ([]MigrationState, error)
	// MigrationStatus lists every known migration and its state.
	MigrationStatus(ctx context.Context) ([]MigrationState, error)
}

// PendingMigrations counts migrations not yet applied (used by readiness).
func PendingMigrations(ctx context.Context, b Backend) (int, error) {
	st, err := b.MigrationStatus(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range st {
		if s.AppliedAt == nil {
			n++
		}
	}
	return n, nil
}

// ClampLimit applies default and maximum page sizes.
func ClampLimit(l, def, max int) int {
	if l <= 0 {
		return def
	}
	if l > max {
		return max
	}
	return l
}
