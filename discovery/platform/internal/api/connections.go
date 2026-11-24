package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/httpx"
	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

var (
	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
	idemPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

type credentialView struct {
	Configured bool   `json:"configured"`
	Ref        string `json:"ref,omitempty"` // a reference such as env:OBS_SECRET_X; never the value
}

type connectionView struct {
	store.Connection
	Credential    credentialView `json:"credential"`
	Simulated     bool           `json:"simulated"`
	MetricsSource string         `json:"metricsSource"`
	LastJob       *store.Job     `json:"lastJob,omitempty"`
}

func (a *API) view(c store.Connection, last *store.Job) connectionView {
	v := connectionView{Connection: c, Credential: credentialView{Configured: c.CredentialRef != "", Ref: c.CredentialRef}, LastJob: last}
	if p, ok := a.Providers.Get(c.Provider); ok {
		v.Simulated = p.Info().Simulated
		v.MetricsSource = p.Info().MetricsSource
	}
	return v
}

func (a *API) listProviders(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, list(a.Providers.List(), ""))
}

func (a *API) listConnections(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	pg := a.page(r, v)
	if !v.ok() {
		v.fail(w, r)
		return
	}
	p := principal(r)
	cs, next, err := a.Store.ListConnections(r.Context(), p.TenantID, pg)
	if err != nil {
		a.storeError(w, r, err, "connection")
		return
	}
	latest, err := a.Store.LatestJobs(r.Context(), p.TenantID)
	if err != nil {
		a.storeError(w, r, err, "job")
		return
	}
	out := make([]connectionView, 0, len(cs))
	for _, c := range cs {
		var lj *store.Job
		if j, ok := latest[c.ID]; ok {
			lj = &j
		}
		out = append(out, a.view(c, lj))
	}
	httpx.JSON(w, http.StatusOK, list(out, next))
}

type createConnectionRequest struct {
	Name                     string         `json:"name"`
	Provider                 string         `json:"provider"`
	Config                   map[string]any `json:"config"`
	CredentialRef            string         `json:"credentialRef"`
	DiscoveryIntervalMinutes int            `json:"discoveryIntervalMinutes"`
}

func (a *API) createConnection(w http.ResponseWriter, r *http.Request) {
	var req createConnectionRequest
	if err := httpx.DecodeJSON(w, r, a.Config.MaxBodyBytes, &req); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, httpx.ErrBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		httpx.Problem(w, r, status, "invalid_body", err.Error(), nil)
		return
	}
	v := &validator{}
	req.Name = strings.TrimSpace(req.Name)
	if !namePattern.MatchString(req.Name) {
		v.add("name", "must be 1-64 characters: letters, digits, space, dot, dash or underscore, starting with a letter or digit")
	}
	prov, ok := a.Providers.Get(req.Provider)
	if !ok {
		v.add("provider", "unknown or disabled provider")
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	if req.CredentialRef != "" {
		if err := secrets.ValidateRef(req.CredentialRef); err != nil {
			v.add("credentialRef", err.Error())
		}
	}
	if req.DiscoveryIntervalMinutes < 0 || req.DiscoveryIntervalMinutes > 10080 {
		v.add("discoveryIntervalMinutes", "must be between 0 (manual only) and 10080")
	} else if req.DiscoveryIntervalMinutes > 0 && req.DiscoveryIntervalMinutes < 5 {
		v.add("discoveryIntervalMinutes", "must be 0 or at least 5")
	}
	if ok {
		for _, fe := range prov.ValidateConfig(req.Config, req.CredentialRef) {
			field := "config." + fe.Field
			if fe.Field == "credentialRef" {
				field = "credentialRef"
			}
			v.add(field, fe.Message)
		}
	}
	if !v.ok() {
		v.fail(w, r)
		return
	}
	p := principal(r)
	c, err := a.Store.CreateConnection(r.Context(), store.Connection{
		ID: ids.New("con"), TenantID: p.TenantID, Name: req.Name, Provider: req.Provider, Config: req.Config,
		CredentialRef: req.CredentialRef, DiscoveryIntervalMinutes: req.DiscoveryIntervalMinutes, CreatedBy: p.Subject, CreatedAt: a.Now(),
	})
	if err != nil {
		a.storeError(w, r, err, "connection")
		return
	}
	a.audit(r, "connection.create", "connection", c.ID, map[string]any{"provider": c.Provider, "name": c.Name, "credentialConfigured": c.CredentialRef != ""})
	w.Header().Set("Location", "/api/v1/connections/"+c.ID)
	httpx.JSON(w, http.StatusCreated, a.view(c, nil))
}

func (a *API) loadConnection(w http.ResponseWriter, r *http.Request) (store.Connection, bool) {
	v := &validator{}
	id := pathID(r, v, "con")
	if !v.ok() {
		v.fail(w, r)
		return store.Connection{}, false
	}
	c, err := a.Store.GetConnection(r.Context(), principal(r).TenantID, id)
	if err != nil {
		a.storeError(w, r, err, "connection")
		return c, false
	}
	return c, true
}

func (a *API) getConnection(w http.ResponseWriter, r *http.Request) {
	c, ok := a.loadConnection(w, r)
	if !ok {
		return
	}
	latest, err := a.Store.LatestJobs(r.Context(), c.TenantID)
	if err != nil {
		a.storeError(w, r, err, "job")
		return
	}
	var lj *store.Job
	if j, ok := latest[c.ID]; ok {
		lj = &j
	}
	httpx.JSON(w, http.StatusOK, a.view(c, lj))
}

type validationResult struct {
	Valid  bool               `json:"valid"`
	Errors []httpx.FieldError `json:"errors"`
	Check  struct {
		Performed bool   `json:"performed"`
		OK        bool   `json:"ok"`
		Message   string `json:"message,omitempty"`
	} `json:"check"`
}

// validateConnection re-validates stored config and runs the provider's
// read-only connectivity check. Messages name references, never values.
func (a *API) validateConnection(w http.ResponseWriter, r *http.Request) {
	c, ok := a.loadConnection(w, r)
	if !ok {
		return
	}
	res := validationResult{Errors: []httpx.FieldError{}}
	prov, ok := a.Providers.Get(c.Provider)
	if !ok {
		res.Errors = append(res.Errors, httpx.FieldError{Field: "provider", Message: "provider is not enabled in this deployment"})
		httpx.JSON(w, http.StatusOK, res)
		return
	}
	for _, fe := range prov.ValidateConfig(c.Config, c.CredentialRef) {
		res.Errors = append(res.Errors, httpx.FieldError{Field: fe.Field, Message: fe.Message})
	}
	cred, err := a.Secrets.Resolve(c.CredentialRef)
	if err != nil {
		res.Errors = append(res.Errors, httpx.FieldError{Field: "credentialRef", Message: err.Error()})
	}
	if len(res.Errors) == 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		res.Check.Performed = true
		if err := prov.Check(ctx, discovery.Request{TenantID: c.TenantID, Connection: c, Credential: cred}); err != nil {
			res.Check.Message = err.Error()
		} else {
			res.Check.OK = true
			res.Check.Message = "provider reachable with read access"
		}
	}
	res.Valid = len(res.Errors) == 0 && res.Check.OK
	a.audit(r, "connection.validate", "connection", c.ID, map[string]any{"valid": res.Valid})
	httpx.JSON(w, http.StatusOK, res)
}

func (a *API) startCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := a.loadConnection(w, r)
	if !ok {
		return
	}
	run, err := a.Metrics.Trigger(r.Context(), c, "manual", principal(r).Subject)
	switch {
	case errors.Is(err, metrics.ErrInProgress):
		httpx.Problem(w, r, http.StatusConflict, "collection_in_progress", err.Error(), nil)
		return
	case errors.Is(err, metrics.ErrNoCollector):
		httpx.Problem(w, r, http.StatusUnprocessableEntity, "no_collector", err.Error(), nil)
		return
	case err != nil:
		a.storeError(w, r, err, "collection")
		return
	}
	a.audit(r, "metrics.collect", "connection", c.ID, map[string]any{"collectionId": run.ID})
	w.Header().Set("Location", "/api/v1/metric-collections/"+run.ID)
	httpx.JSON(w, http.StatusAccepted, run)
}
