package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deeprajsshetty/observability-platform/platform/internal/app"
)

type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(int)             {}

// BenchmarkHandlerStack measures one request through the full production
// handler (routing on net/http ServeMux plus request ID, security headers,
// rate limiting, access log and Prometheus counters) without a database.
// ADR 0013 compares it with the routing cost of alternative routers.
func BenchmarkHandlerStack(b *testing.B) {
	cfg := testConfig(b)
	a, err := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		b.Fatal(err)
	}
	h := a.API.Handler()
	for _, tc := range []struct{ name, path string }{
		{"healthz", "/healthz"},
		{"unauthenticated-api-401", "/api/v1/resources/res_0123456789abcdef01234567/metrics/cpu.utilization"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			req := httptest.NewRequest("GET", tc.path, nil)
			w := &discardWriter{h: http.Header{}}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				clear(w.h)
				h.ServeHTTP(w, req)
			}
		})
	}
}
