// Package telemetry provides structured logging setup and a minimal
// Prometheus text-format registry for the platform's own operational metrics.
// It avoids a client-library dependency for the handful of counters the MVP
// exposes; an OpenTelemetry exporter is the documented next step.
package telemetry

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// NewLogger builds the process logger.
func NewLogger(level, format string) *slog.Logger {
	var lv slog.Level
	_ = lv.UnmarshalText([]byte(level))
	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h).With("service", "obsplatform")
}

// Registry holds counters and gauges keyed by name and label set.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*series
	gauges   map[string]*series
	help     map[string]string
}

type series struct {
	name   string
	labels string
	v      atomic.Int64 // counters: integer counts; gauges: integer values
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{counters: map[string]*series{}, gauges: map[string]*series{}, help: map[string]string{}}
}

// Help sets HELP text for a metric name.
func (r *Registry) Help(name, text string) {
	r.mu.Lock()
	r.help[name] = text
	r.mu.Unlock()
}

// labelEscaper escapes Prometheus label values. It is built once: a
// strings.Replacer allocates its lookup tables on first use, and building
// one per call made this function ~90 % of all per-request allocations
// (found by BenchmarkHandlerStack).
var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func labelString(kv []string) string {
	if len(kv) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[i])
		b.WriteString(`="`)
		b.WriteString(labelEscaper.Replace(kv[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func (r *Registry) get(m map[string]*series, name string, kv []string) *series {
	ls := labelString(kv)
	k := name + ls
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := m[k]
	if !ok {
		s = &series{name: name, labels: ls}
		m[k] = s
	}
	return s
}

// Add increments counter name{labels} by n. Labels are key, value pairs.
func (r *Registry) Add(name string, n int64, labels ...string) {
	r.get(r.counters, name, labels).v.Add(n)
}

// Set sets gauge name{labels}.
func (r *Registry) Set(name string, v int64, labels ...string) {
	r.get(r.gauges, name, labels).v.Store(v)
}

// Value returns a counter value (tests).
func (r *Registry) Value(name string, labels ...string) int64 {
	return r.get(r.counters, name, labels).v.Load()
}

// Write writes Prometheus text exposition format.
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	write := func(m map[string]*series, typ string) {
		byName := map[string][]*series{}
		for _, s := range m {
			byName[s.name] = append(byName[s.name], s)
		}
		names := make([]string, 0, len(byName))
		for n := range byName {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if h := r.help[n]; h != "" {
				fmt.Fprintf(w, "# HELP %s %s\n", n, h)
			}
			fmt.Fprintf(w, "# TYPE %s %s\n", n, typ)
			ss := byName[n]
			sort.Slice(ss, func(i, j int) bool { return ss[i].labels < ss[j].labels })
			for _, s := range ss {
				fmt.Fprintf(w, "%s%s %d\n", s.name, s.labels, s.v.Load())
			}
		}
	}
	write(r.counters, "counter")
	write(r.gauges, "gauge")
}

// Handler serves the registry at /metrics.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		r.Write(w)
	})
}
