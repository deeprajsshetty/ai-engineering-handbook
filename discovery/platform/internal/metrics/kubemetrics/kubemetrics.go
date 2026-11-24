// Package kubemetrics collects measured CPU and memory usage for Kubernetes
// nodes and pods from the metrics.k8s.io API (metrics-server). Samples are
// stored with source "measured". Validation status: contract-tested against
// recorded responses; not validated against a live cluster in this repository.
package kubemetrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	dm "github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/kubeclient"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/retry"
)

// Name is the collector name.
const Name = "kubernetes-metrics-api"

// Collector implements metrics.Collector.
type Collector struct {
	Policy    netguard.Policy
	Timeout   time.Duration
	Transport http.RoundTripper // tests only
	Retry     retry.Policy
}

// Name returns the collector name.
func (c *Collector) Name() string { return Name }

// Source returns "measured".
func (c *Collector) Source() dm.Source { return dm.SourceMeasured }

type usage struct {
	Metadata   kubeclient.ObjectMeta `json:"metadata"`
	Timestamp  time.Time             `json:"timestamp"`
	Usage      map[string]string     `json:"usage"`
	Containers []struct {
		Usage map[string]string `json:"usage"`
	} `json:"containers"`
}

// Collect reads current node and pod usage.
func (c *Collector) Collect(ctx context.Context, req metrics.Request) ([]dm.Sample, error) {
	cl, err := kubeclient.New(kubeclient.Options{
		Server: discovery.Str(req.Connection.Config, "server", ""), Token: req.Credential,
		CACertPEM: discovery.Str(req.Connection.Config, "caCertificate", ""), Policy: c.Policy,
		Timeout: c.Timeout, Retry: c.Retry, HTTPTransport: c.Transport,
	})
	if err != nil {
		return nil, err
	}
	nodes := map[string]string{}   // node name -> resource id
	pods := map[string]string{}    // ns/name -> resource id
	capCPU := map[string]float64{} // resource id -> cores
	capMem := map[string]float64{} // resource id -> bytes
	for _, r := range req.Resources {
		name, _ := r.Properties["name"].(string)
		switch r.Type {
		case "k8s.node":
			nodes[name] = r.ID
			if s, ok := r.Properties["capacityCpu"].(string); ok {
				capCPU[r.ID], _ = kubeclient.ParseQuantity(s)
			}
			if s, ok := r.Properties["capacityMemory"].(string); ok {
				capMem[r.ID], _ = kubeclient.ParseQuantity(s)
			}
		case "k8s.pod":
			ns, _ := r.Properties["namespace"].(string)
			pods[ns+"/"+name] = r.ID
		}
	}
	var out []dm.Sample
	add := func(rid, metric string, ts time.Time, v float64) {
		if ts.IsZero() {
			ts = req.Now
		}
		out = append(out, dm.Sample{TenantID: req.TenantID, ResourceID: rid, Metric: metric, Timestamp: ts.UTC().Truncate(time.Second),
			Value: v, Source: dm.SourceMeasured, Collector: Name})
	}
	list := func(path string, fn func(u usage)) error {
		return cl.List(ctx, path, 500, func(items []json.RawMessage) error {
			for _, raw := range items {
				var u usage
				if json.Unmarshal(raw, &u) == nil {
					fn(u)
				}
			}
			return nil
		})
	}
	if len(nodes) > 0 {
		err := list("/apis/metrics.k8s.io/v1beta1/nodes", func(u usage) {
			rid, ok := nodes[u.Metadata.Name]
			if !ok {
				return
			}
			cpu, err1 := kubeclient.ParseQuantity(u.Usage["cpu"])
			mem, err2 := kubeclient.ParseQuantity(u.Usage["memory"])
			if err1 == nil {
				add(rid, "node.cpu.usage", u.Timestamp, cpu)
				if c := capCPU[rid]; c > 0 {
					add(rid, "cpu.utilization", u.Timestamp, min(100, cpu/c*100))
				}
			}
			if err2 == nil {
				add(rid, "node.memory.usage", u.Timestamp, mem)
				if c := capMem[rid]; c > 0 {
					add(rid, "memory.utilization", u.Timestamp, min(100, mem/c*100))
				}
			}
		})
		if err != nil {
			return nil, explain(err)
		}
	}
	if len(pods) > 0 {
		err := list("/apis/metrics.k8s.io/v1beta1/pods", func(u usage) {
			rid, ok := pods[u.Metadata.Namespace+"/"+u.Metadata.Name]
			if !ok {
				return
			}
			var cpu, mem float64
			for _, ct := range u.Containers {
				if v, err := kubeclient.ParseQuantity(ct.Usage["cpu"]); err == nil {
					cpu += v
				}
				if v, err := kubeclient.ParseQuantity(ct.Usage["memory"]); err == nil {
					mem += v
				}
			}
			add(rid, "container.cpu.usage", u.Timestamp, cpu)
			add(rid, "container.memory.usage", u.Timestamp, mem)
		})
		if err != nil {
			return nil, explain(err)
		}
	}
	return out, nil
}

func explain(err error) error {
	if errors.Is(err, kubeclient.ErrNotFound) {
		return fmt.Errorf("metrics.k8s.io API is not available (is metrics-server installed?): %w", err)
	}
	return err
}
