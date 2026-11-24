package telemetry

import (
	"strings"
	"testing"
)

func TestLabelEscapingAndExposition(t *testing.T) {
	if got := labelString([]string{"route", `a"b\c` + "\n", "code", "200"}); got != `{route="a\"b\\c\n",code="200"}` {
		t.Fatalf("labelString: %s", got)
	}
	r := NewRegistry()
	r.Help("obs_x_total", "Test counter.")
	r.Add("obs_x_total", 2, "k", "v")
	r.Add("obs_x_total", 3, "k", "v")
	if r.Value("obs_x_total", "k", "v") != 5 {
		t.Fatal("counter not accumulated")
	}
}

func BenchmarkLabelString(b *testing.B) {
	kv := []string{"method", "GET", "route", "GET /api/v1/resources/{id}", "code", "200"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !strings.HasPrefix(labelString(kv), "{") {
			b.Fatal()
		}
	}
}
