// Command smoke is the end-to-end smoke test. Against a running server it:
// checks health and readiness, finds (or creates) the demo connection, runs
// fixture discovery, verifies topology nodes and edges, waits for
// topology-driven metric collection, queries a series, and checks that the
// web UI is served. It exits non-zero on the first failed check.
//
//	go run ./cmd/smoke -url http://localhost:8080 -token dev-admin
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	base   = flag.String("url", "http://localhost:8080", "server base URL")
	token  = flag.String("token", "dev-admin", "bearer token (development auth)")
	webUI  = flag.Bool("web", true, "also check that the web UI is served at /")
	client = &http.Client{Timeout: 15 * time.Second}
)

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "SMOKE FAIL: "+format+"\n", a...)
	os.Exit(1)
}

func ok(format string, a ...any) { fmt.Printf("  ok  "+format+"\n", a...) }

func call(method, path string, body any, auth bool) (int, map[string]any, string) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, *base+path, rd)
	if auth {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		fail("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m, string(raw)
}

func must(status int, method, path string, body any) map[string]any {
	s, m, raw := call(method, path, body, true)
	if s != status {
		fail("%s %s: HTTP %d (want %d): %s", method, path, s, status, raw)
	}
	return m
}

func items(m map[string]any) []map[string]any {
	raw, _ := m["items"].([]any)
	out := []map[string]any{}
	for _, x := range raw {
		out = append(out, x.(map[string]any))
	}
	return out
}

func alive() bool {
	resp, err := client.Get(*base + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func main() {
	flag.Parse()
	start := time.Now()
	fmt.Printf("smoke test against %s\n", *base)

	deadline := time.Now().Add(30 * time.Second)
	for !alive() {
		if time.Now().After(deadline) {
			fail("healthz: server not reachable at %s within 30s", *base)
		}
		time.Sleep(500 * time.Millisecond)
	}
	ok("healthz")
	deadline = time.Now().Add(30 * time.Second)
	for {
		s, _, raw := call("GET", "/readyz", nil, false)
		if s == 200 {
			break
		}
		if time.Now().After(deadline) {
			fail("readyz: %d %s", s, raw)
		}
		time.Sleep(time.Second)
	}
	ok("readyz")
	me := must(200, "GET", "/api/v1/me", nil)
	ok("authenticated as %v (tenant %v, %v mode)", me["subject"], me["tenantId"], me["authMode"])

	var conn map[string]any
	for _, c := range items(must(200, "GET", "/api/v1/connections?limit=500", nil)) {
		if c["provider"] == "fixture" {
			conn = c
			break
		}
	}
	if conn == nil {
		conn = must(201, "POST", "/api/v1/connections", map[string]any{"name": "Smoke test (simulated)", "provider": "fixture",
			"config": map[string]any{"pageDelayMs": 50}})
	}
	connID := conn["id"].(string)
	ok("connection %q (%s)", conn["name"], connID)
	if v := must(200, "POST", "/api/v1/connections/"+connID+"/validate", nil); v["valid"] != true {
		fail("connection validation: %v", v)
	}
	ok("connection validated")

	s, job, raw := call("POST", "/api/v1/connections/"+connID+"/discovery-jobs", nil, true)
	switch s {
	case 202:
	case 409:
		job = map[string]any{"id": job["extra"].(map[string]any)["activeJobId"]}
	default:
		fail("start discovery: %d %s", s, raw)
	}
	jobID := job["id"].(string)
	deadline = time.Now().Add(2 * time.Minute)
	for {
		j := must(200, "GET", "/api/v1/discovery-jobs/"+jobID, nil)
		st := j["status"].(string)
		if st == "succeeded" {
			c := j["counts"].(map[string]any)
			ok("discovery %s succeeded: added=%v changed=%v unchanged=%v missing=%v reactivated=%v", jobID, c["added"], c["changed"], c["unchanged"], c["missing"], c["reactivated"])
			break
		}
		if st == "failed" || st == "cancelled" || time.Now().After(deadline) {
			fail("discovery job %s: %s %v", jobID, st, j["error"])
		}
		time.Sleep(300 * time.Millisecond)
	}

	sum := must(200, "GET", "/api/v1/topology/summary?connectionId="+connID, nil)
	nodes, edges := sum["activeResources"].(float64), sum["relationships"].(float64)
	if nodes < 1 || edges < 1 {
		fail("topology empty: %v", sum)
	}
	ok("topology: %v active resources, %v relationships", nodes, edges)
	g := must(200, "GET", "/api/v1/topology/graph?connectionId="+connID, nil)
	if g["cycleBackEdges"].(map[string]any)["acyclicTypes"].(float64) != 0 {
		fail("containment cycle present: %v", g["cycleBackEdges"])
	}
	ok("graph: %d nodes, %d edges, no containment cycles", len(g["nodes"].([]any)), len(g["edges"].([]any)))

	insts := items(must(200, "GET", "/api/v1/resources?type=compute.instance&status=active&connectionId="+connID+"&limit=1", nil))
	if len(insts) == 0 {
		fail("no compute.instance resources")
	}
	res := insts[0]
	rid := res["id"].(string)
	nb := must(200, "GET", "/api/v1/resources/"+rid+"/neighborhood?depth=1", nil)
	ok("neighbourhood of %v: %d nodes", res["name"], len(nb["nodes"].([]any)))

	deadline = time.Now().Add(60 * time.Second)
	var series map[string]any
	for {
		series = must(200, "GET", "/api/v1/resources/"+rid+"/metrics/cpu.utilization?from="+url.QueryEscape(time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)), nil)
		if len(series["points"].([]any)) > 0 {
			break
		}
		if time.Now().After(deadline) {
			fail("no metric points for %s after discovery", rid)
		}
		time.Sleep(time.Second)
	}
	ok("metrics: %d cpu.utilization points (%v), sources=%v, simulated=%v", len(series["points"].([]any)), series["unit"], series["sources"], series["simulated"])
	if series["simulated"] != true {
		fail("fixture metrics must be flagged simulated")
	}

	// Optional: a measured database connection (seeded by scripts/demo.sh).
	for _, c := range items(must(200, "GET", "/api/v1/connections?limit=500", nil)) {
		if c["provider"] != "database" {
			continue
		}
		dbConn := c["id"].(string)
		if v := must(200, "POST", "/api/v1/connections/"+dbConn+"/validate", nil); v["valid"] != true {
			fail("database connection %q validation: %v", c["name"], v)
		}
		s, j, raw := call("POST", "/api/v1/connections/"+dbConn+"/discovery-jobs", nil, true)
		if s != 202 && s != 409 {
			fail("start database discovery: %d %s", s, raw)
		}
		id, _ := j["id"].(string)
		if s == 409 {
			id = j["extra"].(map[string]any)["activeJobId"].(string)
		}
		deadline = time.Now().Add(time.Minute)
		for {
			jj := must(200, "GET", "/api/v1/discovery-jobs/"+id, nil)
			if jj["status"] == "succeeded" {
				break
			}
			if jj["status"] == "failed" || time.Now().After(deadline) {
				fail("database discovery: %v %v", jj["status"], jj["error"])
			}
			time.Sleep(300 * time.Millisecond)
		}
		deadline = time.Now().Add(30 * time.Second)
		for {
			fl := must(200, "GET", "/api/v1/fleet?type=database.instance&connectionId="+dbConn, nil)
			rows := items(fl)
			if len(rows) == 1 {
				if l, _ := rows[0]["latest"].(map[string]any); l["db.connections"] != nil {
					smp := l["db.connections"].(map[string]any)
					if smp["source"] != "measured" {
						fail("database metrics must be measured: %v", smp)
					}
					ok("database %q: %v discovered, db.connections=%v (%v)", c["name"], rows[0]["name"], smp["value"], smp["source"])
					break
				}
			}
			if time.Now().After(deadline) {
				fail("no measured database metrics for %s", dbConn)
			}
			time.Sleep(time.Second)
		}
	}

	if *webUI {
		resp, err := client.Get(*base + "/")
		if err != nil {
			fail("web UI: %v", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(b), `id="root"`) {
			fail("web UI not served at / (HTTP %d); build web/ and set OBS_WEB_DIR, or pass -web=false", resp.StatusCode)
		}
		ok("web UI served")
	}
	fmt.Printf("SMOKE PASS in %s\n", time.Since(start).Round(time.Millisecond))
}
