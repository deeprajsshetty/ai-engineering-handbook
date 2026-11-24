// Package api implements the versioned HTTP API (/api/v1). The route table
// below is the single source of truth for paths, methods and required
// permissions; a test checks it against contracts/openapi.yaml.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/contracts"
	"github.com/deeprajsshetty/observability-platform/platform/internal/auth"
	"github.com/deeprajsshetty/observability-platform/platform/internal/config"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/httpx"
	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/modules"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/telemetry"
)

// Version is set at build time with -ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
)

// Notifier wakes discovery workers.
type Notifier interface{ Notify() }

// API holds handler dependencies.
type API struct {
	Store     store.Store
	Providers *discovery.Registry
	Jobs      Notifier
	Metrics   *metrics.Service
	Auth      auth.Authenticator
	Secrets   *secrets.Resolver
	Config    config.Config
	Log       *slog.Logger
	Telemetry *telemetry.Registry
	Ready     func(ctx context.Context) error
	Modules   []modules.Info
	Now       func() time.Time

	limiter *httpx.RateLimiter
}

// Access levels for routes.
const (
	Public        auth.Permission = "-"
	Authenticated auth.Permission = ""
)

// Route is one API operation.
type Route struct {
	Method  string
	Path    string
	Perm    auth.Permission
	Handler http.HandlerFunc
}

// Routes returns the route table.
func (a *API) Routes() []Route {
	return []Route{
		{"GET", "/healthz", Public, a.healthz},
		{"GET", "/readyz", Public, a.readyz},
		{"GET", "/api/v1/openapi.yaml", Public, a.openapi},
		{"GET", "/api/v1/me", Authenticated, a.me},
		{"GET", "/api/v1/catalog", Authenticated, a.catalogHandler},
		{"GET", "/api/v1/providers", auth.PermConnectionsRead, a.listProviders},
		{"GET", "/api/v1/connections", auth.PermConnectionsRead, a.listConnections},
		{"POST", "/api/v1/connections", auth.PermConnectionsWrite, a.createConnection},
		{"GET", "/api/v1/connections/{id}", auth.PermConnectionsRead, a.getConnection},
		{"POST", "/api/v1/connections/{id}/validate", auth.PermConnectionsWrite, a.validateConnection},
		{"POST", "/api/v1/connections/{id}/discovery-jobs", auth.PermDiscoveryRun, a.startJob},
		{"POST", "/api/v1/connections/{id}/metric-collections", auth.PermMetricsCollect, a.startCollection},
		{"GET", "/api/v1/discovery-jobs", auth.PermDiscoveryRead, a.listJobs},
		{"GET", "/api/v1/discovery-jobs/{id}", auth.PermDiscoveryRead, a.getJob},
		{"POST", "/api/v1/discovery-jobs/{id}/cancel", auth.PermDiscoveryRun, a.cancelJob},
		{"GET", "/api/v1/discovery-jobs/{id}/events", auth.PermDiscoveryRead, a.jobEvents},
		{"GET", "/api/v1/resources", auth.PermTopologyRead, a.listResources},
		{"GET", "/api/v1/resources/facets", auth.PermTopologyRead, a.facets},
		{"GET", "/api/v1/resources/{id}", auth.PermTopologyRead, a.getResource},
		{"GET", "/api/v1/resources/{id}/neighborhood", auth.PermTopologyRead, a.neighborhood},
		{"GET", "/api/v1/resources/{id}/metrics", auth.PermMetricsRead, a.resourceMetrics},
		{"GET", "/api/v1/resources/{id}/metrics/{metric}", auth.PermMetricsRead, a.metricSeries},
		{"GET", "/api/v1/relationships", auth.PermTopologyRead, a.listRelationships},
		{"GET", "/api/v1/relationship-types", auth.PermTopologyRead, a.relationshipTypes},
		{"GET", "/api/v1/fleet", auth.PermMetricsRead, a.fleet},
		{"GET", "/api/v1/topology/summary", auth.PermTopologyRead, a.summary},
		{"GET", "/api/v1/topology/graph", auth.PermTopologyRead, a.graph},
		{"GET", "/api/v1/topology/changes", auth.PermTopologyRead, a.changes},
		{"GET", "/api/v1/metric-definitions", auth.PermMetricsRead, a.metricDefinitions},
		{"GET", "/api/v1/metric-collections", auth.PermMetricsRead, a.listCollections},
		{"GET", "/api/v1/metric-collections/{id}", auth.PermMetricsRead, a.getCollection},
		{"GET", "/api/v1/audit-events", auth.PermAuditRead, a.listAudit},
	}
}

// Handler builds the full HTTP handler: API routes, /metrics and, when
// configured, the static web application with SPA fallback.
func (a *API) Handler() http.Handler {
	if a.Now == nil {
		a.Now = func() time.Time { return time.Now().UTC() }
	}
	a.limiter = httpx.NewRateLimiter(a.Config.RateLimitRPS, a.Config.RateLimitBurst)
	mux := http.NewServeMux()
	for _, rt := range a.Routes() {
		h := http.Handler(rt.Handler)
		if rt.Perm != Public {
			h = a.authorize(rt.Perm, h)
		}
		mux.Handle(rt.Method+" "+rt.Path, h)
	}
	if a.Telemetry != nil {
		mux.Handle("GET /metrics", a.Telemetry.Handler())
	}
	// One catch-all: unknown API paths get a problem document; everything
	// else is the single-page app (when configured) with index.html fallback.
	var web http.Handler
	if a.Config.WebDir != "" {
		web = spa(a.Config.WebDir)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/") || web == nil:
			httpx.Problem(w, r, http.StatusNotFound, "not_found", "no such route", nil)
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			w.Header().Set("Allow", "GET, HEAD")
			httpx.Problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "static content is read-only", nil)
		default:
			web.ServeHTTP(w, r)
		}
	})
	return httpx.Observe(mux, a.Log, a.Telemetry)
}

// authorize authenticates, enforces the permission and rate limits.
func (a *API) authorize(perm auth.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.Auth.Authenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="obsplatform"`)
			httpx.Problem(w, r, http.StatusUnauthorized, "unauthenticated", "missing or invalid bearer token", nil)
			return
		}
		httpx.Fields(r.Context()).Add("tenant", p.TenantID, "subject", p.Subject)
		if ok, wait := a.limiter.Allow(p.RateKey()); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			httpx.Problem(w, r, http.StatusTooManyRequests, "rate_limited", "request rate limit exceeded", nil)
			return
		}
		if perm != Authenticated && !p.Can(perm) {
			httpx.Problem(w, r, http.StatusForbidden, "forbidden", "missing permission "+string(perm), nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.FromContext(r.Context())
	return p
}

func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean("/" + r.URL.Path)
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean))); err != nil || st.IsDir() {
			if !strings.HasPrefix(clean, "/assets/") {
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeFile(w, r, filepath.Join(dir, "index.html"))
				return
			}
		}
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fs.ServeHTTP(w, r)
	})
}

// ----------------------------------------------------------------- helpers

func (a *API) storeError(w http.ResponseWriter, r *http.Request, err error, what string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Problem(w, r, http.StatusNotFound, "not_found", what+" not found", nil)
	case errors.Is(err, store.ErrInvalidCursor):
		httpx.Problem(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or from another query", nil)
	case errors.Is(err, store.ErrConflict):
		httpx.Problem(w, r, http.StatusConflict, "conflict", err.Error(), nil)
	case errors.Is(err, context.Canceled):
		// client went away; nothing useful to send
	default:
		a.Log.Error("request failed", "requestId", httpx.RequestID(r.Context()), "error", err)
		httpx.Problem(w, r, http.StatusInternalServerError, "internal", "internal error; quote the request ID when reporting", nil)
	}
}

type validator struct{ errs []httpx.FieldError }

func (v *validator) add(field, msg string) {
	v.errs = append(v.errs, httpx.FieldError{Field: field, Message: msg})
}
func (v *validator) ok() bool { return len(v.errs) == 0 }

func (v *validator) fail(w http.ResponseWriter, r *http.Request) {
	httpx.Problem(w, r, http.StatusBadRequest, "validation_failed", "request validation failed", v.errs)
}

func (a *API) page(r *http.Request, v *validator) store.Page {
	q := r.URL.Query()
	limit := 50
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 500 {
			v.add("limit", "must be an integer between 1 and 500")
		} else {
			limit = n
		}
	}
	c := q.Get("cursor")
	if len(c) > 1024 {
		v.add("cursor", "is too long")
		c = ""
	}
	return store.Page{Limit: limit, Cursor: c}
}

type listResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func list[T any](items []T, next string) listResponse[T] {
	if items == nil {
		items = []T{}
	}
	return listResponse[T]{Items: items, NextCursor: next}
}

func pathID(r *http.Request, v *validator, prefix string) string {
	id := r.PathValue("id")
	if !ids.Valid(prefix, id) {
		v.add("id", "is not a valid "+prefix+" identifier")
	}
	return id
}

func optID(r *http.Request, v *validator, param, prefix string) string {
	id := r.URL.Query().Get(param)
	if id != "" && !ids.Valid(prefix, id) {
		v.add(param, "is not a valid "+prefix+" identifier")
	}
	return id
}

func (a *API) audit(r *http.Request, action, targetType, targetID string, details map[string]any) {
	p := principal(r)
	e := store.AuditEvent{ID: ids.New("aud"), TenantID: p.TenantID, Actor: p.Subject, Action: action, TargetType: targetType,
		TargetID: targetID, RequestID: httpx.RequestID(r.Context()), At: a.Now(), Details: details}
	if err := a.Store.InsertAudit(context.WithoutCancel(r.Context()), e); err != nil {
		a.Log.Error("writing audit event failed", "action", action, "error", err)
	}
	a.Log.Info("audit", "action", action, "tenant", p.TenantID, "actor", p.Subject, "targetType", targetType, "targetId", targetID, "requestId", e.RequestID)
}

// ------------------------------------------------------------- system routes

func (a *API) healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok", "version": Version, "commit": Commit})
}

func (a *API) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if a.Ready != nil {
		if err := a.Ready(ctx); err != nil {
			httpx.JSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "reason": err.Error()})
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (a *API) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(contracts.OpenAPI)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"subject": p.Subject, "tenantId": p.TenantID, "role": p.Role, "permissions": p.Permissions(),
		"authMode": p.AuthMode, "environment": a.Config.Env, "version": Version,
	})
}
