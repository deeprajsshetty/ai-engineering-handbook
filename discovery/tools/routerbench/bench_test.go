// Package routerbench compares HTTP routers on the platform's real route
// table (ADR 0013). It is a separate module: chi and gin are NOT dependencies
// of the product. Run: cd tools/routerbench && go test -bench . -benchtime 2s

package routerbench

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
)

// The platform's actual route table (internal/api/api.go), 34 routes.
var routes = [][2]string{
	{"GET", "/healthz"}, {"GET", "/readyz"}, {"GET", "/api/v1/openapi.yaml"}, {"GET", "/api/v1/me"}, {"GET", "/api/v1/catalog"},
	{"GET", "/api/v1/providers"}, {"GET", "/api/v1/connections"}, {"POST", "/api/v1/connections"}, {"GET", "/api/v1/connections/{id}"},
	{"POST", "/api/v1/connections/{id}/validate"}, {"POST", "/api/v1/connections/{id}/discovery-jobs"}, {"POST", "/api/v1/connections/{id}/metric-collections"},
	{"GET", "/api/v1/discovery-jobs"}, {"GET", "/api/v1/discovery-jobs/{id}"}, {"POST", "/api/v1/discovery-jobs/{id}/cancel"}, {"GET", "/api/v1/discovery-jobs/{id}/events"},
	{"GET", "/api/v1/resources"}, {"GET", "/api/v1/resources/facets"}, {"GET", "/api/v1/resources/{id}"}, {"GET", "/api/v1/resources/{id}/neighborhood"},
	{"GET", "/api/v1/resources/{id}/metrics"}, {"GET", "/api/v1/resources/{id}/metrics/{metric}"}, {"GET", "/api/v1/relationships"},
	{"GET", "/api/v1/relationship-types"}, {"GET", "/api/v1/fleet"}, {"GET", "/api/v1/topology/summary"}, {"GET", "/api/v1/topology/graph"},
	{"GET", "/api/v1/topology/changes"}, {"GET", "/api/v1/metric-definitions"}, {"GET", "/api/v1/metric-collections"},
	{"GET", "/api/v1/metric-collections/{id}"}, {"GET", "/api/v1/audit-events"},
}

var requests = []*http.Request{
	httptest.NewRequest("GET", "/healthz", nil),
	httptest.NewRequest("GET", "/api/v1/resources", nil),
	httptest.NewRequest("GET", "/api/v1/resources/res_0123456789abcdef01234567/metrics/cpu.utilization", nil),
	httptest.NewRequest("POST", "/api/v1/connections/con_01a1ea1b6d027b0418fc2b9a45bc/discovery-jobs", nil),
	httptest.NewRequest("GET", "/api/v1/audit-events", nil),
}

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }

type nopW struct{ h http.Header }

func (n *nopW) Header() http.Header         { return n.h }
func (n *nopW) Write(b []byte) (int, error) { return len(b), nil }
func (n *nopW) WriteHeader(int)             {}

func run(b *testing.B, h http.Handler) {
	w := &nopW{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, requests[i%len(requests)])
	}
}

func BenchmarkStdlibServeMux(b *testing.B) {
	m := http.NewServeMux()
	for _, r := range routes {
		m.HandleFunc(r[0]+" "+r[1], ok)
	}
	run(b, m)
}

func BenchmarkChi(b *testing.B) {
	r := chi.NewRouter()
	for _, rt := range routes {
		r.MethodFunc(rt[0], rt[1], ok)
	}
	run(b, r)
}

func BenchmarkGin(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	g := gin.New()
	for _, rt := range routes {
		p := strings.NewReplacer("{id}", ":id", "{metric}", ":metric").Replace(rt[1])
		g.Handle(rt[0], p, func(c *gin.Context) { c.Status(204) })
	}
	run(b, g)
}
