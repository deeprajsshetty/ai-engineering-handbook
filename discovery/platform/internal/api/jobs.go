package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/deeprajsshetty/observability-platform/platform/internal/httpx"
	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

// startJob queues a discovery job. With an Idempotency-Key header, a retried
// request returns the original job (200) instead of creating another.
func (a *API) startJob(w http.ResponseWriter, r *http.Request) {
	c, ok := a.loadConnection(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key != "" && !idemPattern.MatchString(key) {
		v := &validator{}
		v.add("Idempotency-Key", "must be 1-128 characters of letters, digits, _ . : -")
		v.fail(w, r)
		return
	}
	if _, ok := a.Providers.Get(c.Provider); !ok {
		httpx.Problem(w, r, http.StatusUnprocessableEntity, "provider_disabled", "provider is not enabled in this deployment", nil)
		return
	}
	p := principal(r)
	job, created, err := a.Store.CreateJob(r.Context(), store.Job{
		ID: ids.New("job"), TenantID: p.TenantID, ConnectionID: c.ID, Trigger: "manual", RequestedBy: p.Subject,
		RequestID: httpx.RequestID(r.Context()), IdempotencyKey: key, CreatedAt: a.Now(),
	})
	if errors.Is(err, store.ErrActiveJob) {
		httpx.ProblemExtra(w, r, http.StatusConflict, "job_active", "the connection already has a queued or running discovery job", nil,
			map[string]any{"activeJobId": job.ID})
		return
	}
	if err != nil {
		a.storeError(w, r, err, "job")
		return
	}
	if job.ConnectionID != c.ID {
		httpx.Problem(w, r, http.StatusUnprocessableEntity, "idempotency_key_reused", "Idempotency-Key was already used for a different connection", nil)
		return
	}
	w.Header().Set("Location", "/api/v1/discovery-jobs/"+job.ID)
	if !created {
		httpx.JSON(w, http.StatusOK, job)
		return
	}
	a.audit(r, "discovery.start", "discovery_job", job.ID, map[string]any{"connectionId": c.ID})
	if a.Jobs != nil {
		a.Jobs.Notify()
	}
	httpx.JSON(w, http.StatusAccepted, job)
}

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	pg := a.page(r, v)
	f := store.JobFilter{ConnectionID: optID(r, v, "connectionId", "con"), Status: r.URL.Query().Get("status")}
	if f.Status != "" && !store.ValidJobStatus(f.Status) {
		v.add("status", "must be one of queued, running, succeeded, failed, cancelled")
	}
	if !v.ok() {
		v.fail(w, r)
		return
	}
	js, next, err := a.Store.ListJobs(r.Context(), principal(r).TenantID, f, pg)
	if err != nil {
		a.storeError(w, r, err, "job")
		return
	}
	httpx.JSON(w, http.StatusOK, list(js, next))
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	id := pathID(r, v, "job")
	if !v.ok() {
		v.fail(w, r)
		return
	}
	j, err := a.Store.GetJob(r.Context(), principal(r).TenantID, id)
	if err != nil {
		a.storeError(w, r, err, "discovery job")
		return
	}
	httpx.JSON(w, http.StatusOK, j)
}

func (a *API) cancelJob(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	id := pathID(r, v, "job")
	if !v.ok() {
		v.fail(w, r)
		return
	}
	j, err := a.Store.RequestCancel(r.Context(), principal(r).TenantID, id, a.Now())
	if err != nil {
		a.storeError(w, r, err, "discovery job")
		return
	}
	a.audit(r, "discovery.cancel", "discovery_job", j.ID, nil)
	httpx.JSON(w, http.StatusAccepted, j)
}

func (a *API) jobEvents(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	id := pathID(r, v, "job")
	after := 0
	if s := r.URL.Query().Get("afterSeq"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			v.add("afterSeq", "must be a non-negative integer")
		}
		after = n
	}
	if !v.ok() {
		v.fail(w, r)
		return
	}
	tenant := principal(r).TenantID
	if _, err := a.Store.GetJob(r.Context(), tenant, id); err != nil {
		a.storeError(w, r, err, "discovery job")
		return
	}
	evs, err := a.Store.ListJobEvents(r.Context(), tenant, id, after, 500)
	if err != nil {
		a.storeError(w, r, err, "job events")
		return
	}
	httpx.JSON(w, http.StatusOK, list(evs, ""))
}

func (a *API) listCollections(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	pg := a.page(r, v)
	conn := optID(r, v, "connectionId", "con")
	if !v.ok() {
		v.fail(w, r)
		return
	}
	runs, next, err := a.Store.ListCollectionRuns(r.Context(), principal(r).TenantID, conn, pg)
	if err != nil {
		a.storeError(w, r, err, "collection")
		return
	}
	httpx.JSON(w, http.StatusOK, list(runs, next))
}

func (a *API) getCollection(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	id := pathID(r, v, "col")
	if !v.ok() {
		v.fail(w, r)
		return
	}
	run, err := a.Store.GetCollectionRun(r.Context(), principal(r).TenantID, id)
	if err != nil {
		a.storeError(w, r, err, "collection")
		return
	}
	httpx.JSON(w, http.StatusOK, run)
}

func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	pg := a.page(r, v)
	if !v.ok() {
		v.fail(w, r)
		return
	}
	evs, next, err := a.Store.ListAudit(r.Context(), principal(r).TenantID, pg)
	if err != nil {
		a.storeError(w, r, err, "audit")
		return
	}
	httpx.JSON(w, http.StatusOK, list(evs, next))
}
