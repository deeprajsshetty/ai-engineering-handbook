// Package kubernetes is an optional, read-only discovery adapter for a
// Kubernetes cluster. It lists nodes, namespaces, deployments, pods and
// services with limit/continue pagination and derives relationships from
// node placement, owner references and service selectors.
//
// Validation status: contract-tested against recorded API fixtures
// (including pagination, 403 and 429/5xx retry). It has not been validated
// against a live cluster in this repository. Required RBAC is the read-only
// ClusterRole in deploy/helm/obs-platform/templates/discovery-rbac-example.yaml.
package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/kubeclient"
	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/retry"
)

// Name is the provider name.
const Name = "kubernetes"

var fields = []discovery.ConfigField{
	{Name: "server", Type: "string", Required: true, Description: "API server URL, https://host[:port]. The host must be in OBS_EGRESS_ALLOWLIST."},
	{Name: "clusterName", Type: "string", Required: true, Description: "Display name for the cluster."},
	{Name: "caCertificate", Type: "string", Description: "PEM CA certificate of the API server (public, not secret)."},
	{Name: "region", Type: "string", Default: "global", Description: "Region recorded on resources that carry no topology.kubernetes.io/region label."},
	{Name: "namespaces", Type: "stringList", Description: "Limit pod, deployment and service discovery to these namespaces."},
	{Name: "pageSize", Type: "integer", Default: 250, Description: "Items per list call (1-1000)."},
}

var (
	dnsLabel    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	clusterName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// Provider implements discovery.Provider.
type Provider struct {
	Policy      netguard.Policy
	PageTimeout time.Duration
	Transport   http.RoundTripper // tests only
	Retry       retry.Policy
}

// Info describes the provider.
func (p *Provider) Info() discovery.Info {
	return discovery.Info{
		Name: Name, DisplayName: "Kubernetes (read-only)",
		Description:        "Discovers nodes, namespaces, deployments, pods and services from a Kubernetes API server using a read-only bearer token. Contract-tested; not validated against a live cluster in this repository.",
		RequiresCredential: true,
		MetricsSource:      "measured",
		ConfigFields:       fields,
		ResourceTypes:      []string{"k8s.cluster", "k8s.node", "k8s.namespace", "k8s.deployment", "k8s.pod", "k8s.service"},
	}
}

// ValidateConfig checks configuration and endpoint policy statically.
func (p *Provider) ValidateConfig(cfg map[string]any, credentialRef string) []discovery.FieldError {
	errs := discovery.ValidateKnownFields(cfg, fields)
	if s := discovery.Str(cfg, "server", ""); s != "" {
		if _, err := p.Policy.ValidateURL(s); err != nil {
			errs = append(errs, discovery.FieldError{Field: "server", Message: err.Error()})
		}
	}
	if n := discovery.Str(cfg, "clusterName", ""); n != "" && !clusterName.MatchString(n) {
		errs = append(errs, discovery.FieldError{Field: "clusterName", Message: "must be 1-64 letters, digits, dots, dashes or underscores"})
	}
	for _, ns := range discovery.StrList(cfg, "namespaces") {
		if !dnsLabel.MatchString(ns) {
			errs = append(errs, discovery.FieldError{Field: "namespaces", Message: "contains an invalid namespace name"})
			break
		}
	}
	errs = append(errs, discovery.RangeCheck(cfg, "pageSize", 1, 1000)...)
	if credentialRef == "" {
		errs = append(errs, discovery.FieldError{Field: "credentialRef", Message: "a bearer token reference is required"})
	}
	return errs
}

func (p *Provider) client(req discovery.Request) (*kubeclient.Client, error) {
	return kubeclient.New(kubeclient.Options{
		Server:        discovery.Str(req.Connection.Config, "server", ""),
		Token:         req.Credential,
		CACertPEM:     discovery.Str(req.Connection.Config, "caCertificate", ""),
		Policy:        p.Policy,
		Timeout:       p.PageTimeout,
		Retry:         p.Retry,
		HTTPTransport: p.Transport,
	})
}

// Check reads /version and lists one node to confirm reachability and RBAC.
func (p *Provider) Check(ctx context.Context, req discovery.Request) error {
	c, err := p.client(req)
	if err != nil {
		return err
	}
	var v struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := c.Get(ctx, "/version", nil, &v); err != nil {
		return err
	}
	var page kubeclient.ListPage
	return c.Get(ctx, "/api/v1/nodes", map[string][]string{"limit": {"1"}}, &page)
}

type node struct {
	Metadata kubeclient.ObjectMeta `json:"metadata"`
	Status   struct {
		Capacity map[string]string `json:"capacity"`
		NodeInfo struct {
			KubeletVersion string `json:"kubeletVersion"`
			OSImage        string `json:"osImage"`
		} `json:"nodeInfo"`
	} `json:"status"`
}

type pod struct {
	Metadata kubeclient.ObjectMeta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

type deployment struct {
	Metadata kubeclient.ObjectMeta `json:"metadata"`
	Spec     struct {
		Replicas *int `json:"replicas"`
	} `json:"spec"`
}

type service struct {
	Metadata kubeclient.ObjectMeta `json:"metadata"`
	Spec     struct {
		Type     string            `json:"type"`
		Selector map[string]string `json:"selector"`
	} `json:"spec"`
}

type podRef struct {
	key    topology.Key
	labels map[string]string
}

// Discover lists the cluster.
func (p *Provider) Discover(ctx context.Context, req discovery.Request, emit discovery.Emit) error {
	c, err := p.client(req)
	if err != nil {
		return err
	}
	cfg := req.Connection.Config
	limit := discovery.Int(cfg, "pageSize", 250)
	defRegion := discovery.Str(cfg, "region", topology.DefaultRegion)
	name := discovery.Str(cfg, "clusterName", "cluster")

	// Cluster identity: the kube-system namespace UID is stable for the
	// cluster's lifetime; fall back to the configured name if it is hidden.
	scope := "name:" + name
	var ks struct {
		Metadata kubeclient.ObjectMeta `json:"metadata"`
	}
	if err := c.Get(ctx, "/api/v1/namespaces/kube-system", nil, &ks); err == nil && ks.Metadata.UID != "" {
		scope = "uid:" + ks.Metadata.UID
	} else if err != nil && !errors.Is(err, kubeclient.ErrForbidden) && !errors.Is(err, kubeclient.ErrNotFound) {
		return err
	}
	key := func(uid string) topology.Key { return topology.Key{Provider: Name, Scope: scope, ExternalID: uid} }
	clusterKey := key("cluster")
	if err := emit(discovery.Page{Resources: []topology.Observation{{
		Key: clusterKey, Type: "k8s.cluster", NativeType: "Cluster", Name: name, Region: defRegion,
		Properties: map[string]any{"server": discovery.Str(cfg, "server", "")},
	}}}); err != nil {
		return err
	}

	nodeKeys := map[string]topology.Key{}
	if err := c.List(ctx, "/api/v1/nodes", limit, func(items []json.RawMessage) error {
		var pg discovery.Page
		for _, raw := range items {
			var n node
			if json.Unmarshal(raw, &n) != nil || n.Metadata.UID == "" {
				continue
			}
			k := key(n.Metadata.UID)
			nodeKeys[n.Metadata.Name] = k
			region := defRegion
			if r := n.Metadata.Labels["topology.kubernetes.io/region"]; r != "" {
				region = r
			}
			pg.Resources = append(pg.Resources, topology.Observation{
				Key: k, Type: "k8s.node", NativeType: "Node", Name: n.Metadata.Name, Region: region, Labels: n.Metadata.Labels,
				Properties: map[string]any{"name": n.Metadata.Name, "kubeletVersion": n.Status.NodeInfo.KubeletVersion,
					"osImage": n.Status.NodeInfo.OSImage, "capacityCpu": n.Status.Capacity["cpu"], "capacityMemory": n.Status.Capacity["memory"]},
			})
			pg.Relationships = append(pg.Relationships, topology.RelationshipObservation{Source: clusterKey, Target: k, Type: topology.RelContains})
		}
		return emit(pg)
	}); err != nil {
		return fmt.Errorf("listing nodes: %w", err)
	}

	nsKeys := map[string]topology.Key{}
	if err := c.List(ctx, "/api/v1/namespaces", limit, func(items []json.RawMessage) error {
		var pg discovery.Page
		for _, raw := range items {
			var ns struct {
				Metadata kubeclient.ObjectMeta `json:"metadata"`
			}
			if json.Unmarshal(raw, &ns) != nil || ns.Metadata.UID == "" {
				continue
			}
			k := key(ns.Metadata.UID)
			nsKeys[ns.Metadata.Name] = k
			pg.Resources = append(pg.Resources, topology.Observation{Key: k, Type: "k8s.namespace", NativeType: "Namespace",
				Name: ns.Metadata.Name, Region: defRegion, Labels: ns.Metadata.Labels, Properties: map[string]any{"name": ns.Metadata.Name}})
			pg.Relationships = append(pg.Relationships, topology.RelationshipObservation{Source: clusterKey, Target: k, Type: topology.RelContains})
		}
		return emit(pg)
	}); err != nil {
		return fmt.Errorf("listing namespaces: %w", err)
	}

	paths := func(kind, group string) []string {
		nss := discovery.StrList(cfg, "namespaces")
		if len(nss) == 0 {
			return []string{group + "/" + kind}
		}
		out := make([]string, 0, len(nss))
		for _, ns := range nss {
			out = append(out, group+"/namespaces/"+ns+"/"+kind)
		}
		return out
	}
	nsContains := func(ns string, k topology.Key) []topology.RelationshipObservation {
		if nk, ok := nsKeys[ns]; ok {
			return []topology.RelationshipObservation{{Source: nk, Target: k, Type: topology.RelContains}}
		}
		return nil
	}

	// ReplicaSets are read only to resolve pod -> deployment ownership.
	rsToDeploy := map[string]string{}
	for _, path := range paths("replicasets", "/apis/apps/v1") {
		if err := c.List(ctx, path, limit, func(items []json.RawMessage) error {
			for _, raw := range items {
				var rs struct {
					Metadata kubeclient.ObjectMeta `json:"metadata"`
				}
				if json.Unmarshal(raw, &rs) != nil {
					continue
				}
				for _, o := range rs.Metadata.OwnerReferences {
					if o.Kind == "Deployment" {
						rsToDeploy[rs.Metadata.UID] = o.UID
					}
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("listing replicasets: %w", err)
		}
	}

	deployKeys := map[string]topology.Key{}
	for _, path := range paths("deployments", "/apis/apps/v1") {
		if err := c.List(ctx, path, limit, func(items []json.RawMessage) error {
			var pg discovery.Page
			for _, raw := range items {
				var d deployment
				if json.Unmarshal(raw, &d) != nil || d.Metadata.UID == "" {
					continue
				}
				k := key(d.Metadata.UID)
				deployKeys[d.Metadata.UID] = k
				props := map[string]any{"name": d.Metadata.Name, "namespace": d.Metadata.Namespace}
				if d.Spec.Replicas != nil {
					props["replicas"] = *d.Spec.Replicas
				}
				pg.Resources = append(pg.Resources, topology.Observation{Key: k, Type: "k8s.deployment", NativeType: "Deployment",
					Name: d.Metadata.Namespace + "/" + d.Metadata.Name, Region: defRegion, Labels: d.Metadata.Labels, Properties: props})
				pg.Relationships = append(pg.Relationships, nsContains(d.Metadata.Namespace, k)...)
			}
			return emit(pg)
		}); err != nil {
			return fmt.Errorf("listing deployments: %w", err)
		}
	}

	podsByNS := map[string][]podRef{}
	for _, path := range paths("pods", "/api/v1") {
		if err := c.List(ctx, path, limit, func(items []json.RawMessage) error {
			var pg discovery.Page
			for _, raw := range items {
				var pd pod
				if json.Unmarshal(raw, &pd) != nil || pd.Metadata.UID == "" {
					continue
				}
				k := key(pd.Metadata.UID)
				podsByNS[pd.Metadata.Namespace] = append(podsByNS[pd.Metadata.Namespace], podRef{k, pd.Metadata.Labels})
				pg.Resources = append(pg.Resources, topology.Observation{Key: k, Type: "k8s.pod", NativeType: "Pod",
					Name: pd.Metadata.Namespace + "/" + pd.Metadata.Name, Region: defRegion, Labels: pd.Metadata.Labels,
					Properties: map[string]any{"name": pd.Metadata.Name, "namespace": pd.Metadata.Namespace, "phase": pd.Status.Phase, "nodeName": pd.Spec.NodeName}})
				pg.Relationships = append(pg.Relationships, nsContains(pd.Metadata.Namespace, k)...)
				if nk, ok := nodeKeys[pd.Spec.NodeName]; ok {
					pg.Relationships = append(pg.Relationships, topology.RelationshipObservation{Source: nk, Target: k, Type: topology.RelContains})
				}
				for _, o := range pd.Metadata.OwnerReferences {
					if o.Kind != "ReplicaSet" {
						continue
					}
					if dk, ok := deployKeys[rsToDeploy[o.UID]]; ok {
						pg.Relationships = append(pg.Relationships, topology.RelationshipObservation{Source: dk, Target: k, Type: topology.RelComposedOf})
					}
				}
			}
			return emit(pg)
		}); err != nil {
			return fmt.Errorf("listing pods: %w", err)
		}
	}

	for _, path := range paths("services", "/api/v1") {
		if err := c.List(ctx, path, limit, func(items []json.RawMessage) error {
			var pg discovery.Page
			for _, raw := range items {
				var s service
				if json.Unmarshal(raw, &s) != nil || s.Metadata.UID == "" {
					continue
				}
				k := key(s.Metadata.UID)
				pg.Resources = append(pg.Resources, topology.Observation{Key: k, Type: "k8s.service", NativeType: "Service",
					Name: s.Metadata.Namespace + "/" + s.Metadata.Name, Region: defRegion, Labels: s.Metadata.Labels,
					Properties: map[string]any{"name": s.Metadata.Name, "namespace": s.Metadata.Namespace, "serviceType": s.Spec.Type}})
				pg.Relationships = append(pg.Relationships, nsContains(s.Metadata.Namespace, k)...)
				if len(s.Spec.Selector) > 0 {
					for _, pr := range podsByNS[s.Metadata.Namespace] {
						if selects(s.Spec.Selector, pr.labels) {
							pg.Relationships = append(pg.Relationships, topology.RelationshipObservation{Source: k, Target: pr.key, Type: topology.RelRoutesTo})
						}
					}
				}
			}
			return emit(pg)
		}); err != nil {
			return fmt.Errorf("listing services: %w", err)
		}
	}
	return nil
}

func selects(selector, labels map[string]string) bool {
	keys := make([]string, 0, len(selector))
	for k := range selector {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if labels[k] != selector[k] {
			return false
		}
	}
	return true
}
