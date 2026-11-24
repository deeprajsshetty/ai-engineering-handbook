// Package dbmetrics collects measured metrics from database servers through
// the engine-neutral dbengine contract. One collection opens one guarded
// session, takes one statistics snapshot and maps it to the discovered
// resources of the connection by external ID.
//
// Counters (cumulative values such as committed transactions) become
// per-second rates from two consecutive snapshots held in memory per
// process. The first snapshot after start-up, a counter reset, or a
// statistics reset therefore produces no rate sample for that interval; this
// is reported rather than guessed.
package dbmetrics

import (
	"context"
	"sync"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery/database"
	dm "github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
)

// Name is the collector name.
const Name = "database-statistics"

type counterState struct {
	value float64
	at    time.Time
}

// Collector implements metrics.Collector.
type Collector struct {
	Provider *database.Provider

	mu   sync.Mutex
	prev map[string]counterState // resourceID + "|" + metric
}

// Name returns the collector name.
func (*Collector) Name() string { return Name }

// Source returns "measured".
func (*Collector) Source() dm.Source { return dm.SourceMeasured }

// Collect takes one snapshot.
func (c *Collector) Collect(ctx context.Context, req metrics.Request) ([]dm.Sample, error) {
	sess, err := c.Provider.Open(req.Connection.Config, req.Credential)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	readings, err := sess.Settings.Engine.Sample(ctx, sess.Querier)
	if err != nil {
		return nil, err
	}
	byExt := map[string]string{}
	types := map[string]string{}
	for _, r := range req.Resources {
		byExt[r.ExternalID] = r.ID
		types[r.ID] = r.Type
	}
	ts := req.Now.UTC().Truncate(max(req.Interval, time.Second))
	applies := func(resID, metric string) bool {
		for _, d := range dm.ForType(types[resID]) {
			if d.Name == metric {
				return true
			}
		}
		return false
	}
	var out []dm.Sample
	add := func(resID, metric string, v float64) {
		d, ok := dm.Lookup(metric)
		if !ok || !applies(resID, metric) {
			return
		}
		out = append(out, dm.Sample{TenantID: req.TenantID, ResourceID: resID, Metric: metric, Timestamp: ts,
			Value: d.Clamp(v), Source: dm.SourceMeasured, Collector: Name})
	}
	for ref, vals := range readings.Gauges {
		resID, ok := byExt[database.ExternalID(ref)]
		if !ok {
			continue // object not (yet) discovered
		}
		for m, v := range vals {
			add(resID, m, v)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prev == nil {
		c.prev = map[string]counterState{}
	}
	now := req.Now.UTC()
	for ref, vals := range readings.Counters {
		resID, ok := byExt[database.ExternalID(ref)]
		if !ok {
			continue
		}
		for m, v := range vals {
			k := resID + "|" + m
			p, had := c.prev[k]
			c.prev[k] = counterState{v, now}
			if !had || v < p.value || !now.After(p.at) {
				continue // first snapshot or counter reset
			}
			add(resID, m, (v-p.value)/now.Sub(p.at).Seconds())
		}
	}
	return out, nil
}
