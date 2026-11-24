// Package simulated generates deterministic metric samples for resources
// discovered by the fixture provider. Every sample is stored with source
// "simulated"; the API and UI display that provenance.
//
// The value for (resource, metric, timestamp) is a pure function: a
// per-resource scaled baseline, a sinusoidal cycle with a per-resource phase,
// and hash-based noise. Re-generating a timestamp yields the same value, so
// retries and overlapping backfills are idempotent at the value level as well
// as at the storage level.
package simulated

import (
	"context"
	"hash/fnv"
	"math"
	"strconv"
	"time"

	dm "github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
)

// Name is the collector name.
const Name = "simulated-generator"

type profile struct {
	base, noise, cycle float64
	period             time.Duration
	growthPerHour      float64 // monotonic drift (storage)
}

var profiles = map[string]profile{
	"cpu.utilization":        {base: 35, noise: 6, cycle: 0.45, period: time.Hour},
	"memory.utilization":     {base: 58, noise: 3, cycle: 0.10, period: 3 * time.Hour},
	"network.in.rate":        {base: 2.5e6, noise: 3e5, cycle: 0.5, period: time.Hour},
	"network.out.rate":       {base: 1.8e6, noise: 2e5, cycle: 0.5, period: time.Hour},
	"request.rate":           {base: 120, noise: 12, cycle: 0.55, period: time.Hour},
	"request.latency.p95":    {base: 85, noise: 10, cycle: 0.25, period: 30 * time.Minute},
	"db.connections":         {base: 40, noise: 4, cycle: 0.35, period: time.Hour},
	"db.sessions.active":     {base: 6, noise: 2, cycle: 0.5, period: time.Hour},
	"db.transactions.rate":   {base: 220, noise: 25, cycle: 0.5, period: time.Hour},
	"db.cache.hit_ratio":     {base: 97, noise: 0.8, cycle: 0.01, period: 2 * time.Hour},
	"db.storage.utilization": {base: 62, noise: 0.2, cycle: 0, period: time.Hour, growthPerHour: 0.01},
	"storage.used":           {base: 5e10, noise: 1e7, cycle: 0, period: time.Hour, growthPerHour: 2e7},
	"error.rate":             {base: 0.4, noise: 0.25, cycle: 0.6, period: 20 * time.Minute},
	"container.cpu.usage":    {base: 0.25, noise: 0.04, cycle: 0.5, period: time.Hour},
	"container.memory.usage": {base: 2.6e8, noise: 1.5e7, cycle: 0.1, period: 2 * time.Hour},
	"node.cpu.usage":         {base: 2.2, noise: 0.3, cycle: 0.4, period: time.Hour},
	"node.memory.usage":      {base: 1.2e10, noise: 4e8, cycle: 0.1, period: 2 * time.Hour},
}

func hash(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

func unit(h uint64) float64 { return float64(h%1_000_000) / 1_000_000 } // [0,1)

// Value is the deterministic sample value.
func Value(d dm.Definition, resourceID string, ts time.Time) float64 {
	p, ok := profiles[d.Name]
	if !ok {
		p = profile{base: 1, noise: 0.1, cycle: 0.2, period: time.Hour}
	}
	scale := 0.6 + 0.8*unit(hash(resourceID, d.Name, "scale"))
	phase := 2 * math.Pi * unit(hash(resourceID, d.Name, "phase"))
	t := float64(ts.UTC().Unix())
	cycle := math.Sin(2*math.Pi*t/p.period.Seconds() + phase)
	noise := (unit(hash(resourceID, d.Name, strconv.FormatInt(ts.Unix(), 10))) - 0.5) * 2 * p.noise
	v := p.base*scale*(1+p.cycle*cycle) + noise + p.growthPerHour*scale*math.Mod(t/3600, 24*30)
	v = d.Clamp(v)
	if isCount(d.Unit) {
		return math.Round(v) // connections, sessions: whole numbers
	}
	return math.Round(v*1000) / 1000
}

// isCount reports annotation-only units such as "{connection}" (not rates
// like "{request}/s" and not fractional quantities like "{core}").
func isCount(unit string) bool {
	return unit == "{connection}" || unit == "{session}"
}

// Collector implements metrics.Collector.
type Collector struct{}

// Name returns the collector name.
func (Collector) Name() string { return Name }

// Source returns "simulated".
func (Collector) Source() dm.Source { return dm.SourceSimulated }

// Collect generates aligned samples since the last stored sample, backfilling
// up to req.Backfill for resources that have none.
func (Collector) Collect(ctx context.Context, req metrics.Request) ([]dm.Sample, error) {
	var out []dm.Sample
	for _, r := range req.Resources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		last, has := req.LastSample[r.ID]
		steps := metrics.AlignedSteps(last, has, req.Now, req.Interval, req.Backfill)
		for _, d := range dm.ForType(r.Type) {
			for _, ts := range steps {
				out = append(out, dm.Sample{TenantID: req.TenantID, ResourceID: r.ID, Metric: d.Name, Timestamp: ts,
					Value: Value(d, r.ID, ts), Source: dm.SourceSimulated, Collector: Name})
			}
		}
	}
	return out, nil
}
