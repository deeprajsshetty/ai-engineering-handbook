// Package discovery defines the provider-neutral discovery contract and runs
// discovery jobs: claim from the queue, stream pages from a provider into a
// snapshot, reconcile against stored topology, persist, report progress.
package discovery

import (
	"context"
	"fmt"
	"sort"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

// Request is what a provider receives for one run.
type Request struct {
	TenantID   string
	Connection store.Connection
	Credential secrets.Value // resolved from Connection.CredentialRef; may be empty
	Revision   int           // 1 + number of previous successful runs (used by deterministic fixtures)
}

// Page is one batch of observations. Relationships may reference resources
// that arrive on later pages; dangling references are dropped at the end.
type Page struct {
	Resources     []topology.Observation
	Relationships []topology.RelationshipObservation
}

// Emit receives pages. It returns an error to stop discovery (for example
// cancellation or a snapshot limit); providers must return that error.
type Emit func(Page) error

// FieldError reports an invalid configuration field without echoing values.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (f FieldError) Error() string { return f.Field + ": " + f.Message }

// ConfigField documents one provider configuration key for the API and UI.
type ConfigField struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // string | integer | boolean | stringList
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`
	Description string `json:"description"`
	// Options, when set, is the closed set of allowed values; the UI renders
	// a select and ValidateKnownFields rejects other values.
	Options []Option `json:"options,omitempty"`
	// Secret-free placeholder shown by the UI.
	Placeholder string `json:"placeholder,omitempty"`
}

// Option is one allowed value of a ConfigField.
type Option struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Info describes a provider.
type Info struct {
	Name               string        `json:"name"`
	DisplayName        string        `json:"displayName"`
	Description        string        `json:"description"`
	Simulated          bool          `json:"simulated"`
	RequiresCredential bool          `json:"requiresCredential"`
	MetricsSource      string        `json:"metricsSource"` // simulated | measured | none
	ConfigFields       []ConfigField `json:"configFields"`
	ResourceTypes      []string      `json:"resourceTypes"`
}

// Provider is a read-only discovery adapter. Implementations translate
// provider responses into observations; no provider SDK type crosses this
// boundary.
type Provider interface {
	Info() Info
	// ValidateConfig checks configuration statically (no network).
	ValidateConfig(cfg map[string]any, credentialRef string) []FieldError
	// Check performs a cheap read-only connectivity and permission probe.
	Check(ctx context.Context, req Request) error
	// Discover lists resources page by page, calling emit for each page.
	Discover(ctx context.Context, req Request, emit Emit) error
}

// Registry holds the enabled providers.
type Registry struct{ providers map[string]Provider }

// NewRegistry builds a registry.
func NewRegistry(ps ...Provider) *Registry {
	r := &Registry{providers: map[string]Provider{}}
	for _, p := range ps {
		r.providers[p.Info().Name] = p
	}
	return r
}

// Get returns a provider by name.
func (r *Registry) Get(name string) (Provider, bool) { p, ok := r.providers[name]; return p, ok }

// List returns provider infos sorted by name.
func (r *Registry) List() []Info {
	out := make([]Info, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ValidateKnownFields rejects unknown keys and keys that look like secrets;
// providers call it first in ValidateConfig.
func ValidateKnownFields(cfg map[string]any, fields []ConfigField) []FieldError {
	known := map[string]ConfigField{}
	for _, f := range fields {
		known[f.Name] = f
	}
	var errs []FieldError
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if secrets.LooksSecret(k) {
			errs = append(errs, FieldError{k, "secrets must not be stored in config; use credentialRef"})
			continue
		}
		f, ok := known[k]
		if !ok {
			errs = append(errs, FieldError{k, "unknown configuration field"})
			continue
		}
		if msg := checkType(cfg[k], f.Type); msg != "" {
			errs = append(errs, FieldError{k, msg})
		} else if len(f.Options) > 0 {
			ok := false
			for _, o := range f.Options {
				ok = ok || cfg[k] == o.Value
			}
			if !ok {
				errs = append(errs, FieldError{k, "is not one of the allowed values"})
			}
		}
	}
	for _, f := range fields {
		if _, ok := cfg[f.Name]; f.Required && !ok {
			errs = append(errs, FieldError{f.Name, "is required"})
		}
	}
	return errs
}

func checkType(v any, typ string) string {
	switch typ {
	case "string":
		if _, ok := v.(string); !ok {
			return "must be a string"
		}
	case "integer":
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) {
			return "must be an integer"
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return "must be a boolean"
		}
	case "stringList":
		l, ok := v.([]any)
		if !ok {
			return "must be a list of strings"
		}
		for _, x := range l {
			if _, ok := x.(string); !ok {
				return "must be a list of strings"
			}
		}
	}
	return ""
}

// Str reads a string config value with a default.
func Str(cfg map[string]any, k, def string) string {
	if v, ok := cfg[k].(string); ok && v != "" {
		return v
	}
	return def
}

// Int reads an integer config value with a default.
func Int(cfg map[string]any, k string, def int) int {
	if v, ok := cfg[k].(float64); ok {
		return int(v)
	}
	return def
}

// Bool reads a boolean config value with a default.
func Bool(cfg map[string]any, k string, def bool) bool {
	if v, ok := cfg[k].(bool); ok {
		return v
	}
	return def
}

// StrList reads a string list config value.
func StrList(cfg map[string]any, k string) []string {
	l, _ := cfg[k].([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// RangeCheck validates an optional integer field range.
func RangeCheck(cfg map[string]any, k string, min, max int) []FieldError {
	if _, ok := cfg[k]; !ok {
		return nil
	}
	v := Int(cfg, k, min)
	if v < min || v > max {
		return []FieldError{{k, fmt.Sprintf("must be between %d and %d", min, max)}}
	}
	return nil
}
