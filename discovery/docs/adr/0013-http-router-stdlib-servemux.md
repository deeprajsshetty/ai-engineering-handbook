# ADR 0013 — HTTP router: keep the standard-library `net/http` ServeMux

**Status:** Accepted (re-evaluated on request, with measurements).

**Context.** The API routes with Go's standard `net/http.ServeMux`, which since Go 1.22 supports method patterns and path wildcards (`GET /api/v1/resources/{id}`). The review asked whether a faster or more standard framework (for example gin, chi, echo or fiber) should replace it.

**Measurements** (Apple M-series, Go 1.27.4, `-benchtime 2s -count 3`; reproducible with `tools/routerbench` and `platform/internal/api/bench_test.go`):

| Benchmark | Result |
|---|---|
| Routing only, our 32-route table, 5 representative requests — `net/http` ServeMux | 179 ns/op, 12 B, 0 allocs |
| Same — chi v5.5.0 | 203 ns/op, 368 B, 2 allocs |
| Same — gin v1.12.0 | 45 ns/op, 0 B, 0 allocs |
| Full production handler stack (routing + request ID, security headers, rate limiter, access log, metrics), `/healthz`, **before** this review | 6,839 ns/op, 28.7 KB, 76 allocs |
| Same, **after** fixing the metrics label escaper (below) | 1,890 ns/op, 1.5 KB, 39 allocs |
| Real API requests in the demo (database-backed, from the access log) | 5–41 ms |

**Findings.**
1. gin's router is ~0.13 µs faster per request than ServeMux. That is ~7 % of a database-free request and ~0.003 % of a typical 5 ms API request; it is not measurable in throughput or latency of this service, whose cost is the database.
2. chi is slower than ServeMux on this table and allocates more.
3. The measurement exposed a real hot spot unrelated to routing: `telemetry.labelString` built a `strings.Replacer` on every request, ~90 % of all per-request allocations. Building it once made the full stack 3.6× faster with 19× less memory per request. That fix matters far more than any router swap.

**Decision.** Keep `net/http.ServeMux`. It is the industry-standard baseline for Go services in 2026, has no dependencies, follows Go's security fixes with the toolchain, and keeps every handler and middleware a plain `http.Handler` (ADR 0008).

**Alternatives rejected.**
- *gin* — fastest router here, but the gain is immaterial for this workload; it adds ~45 modules to the build graph (the comparison module resolves 56 modules versus 33 for the whole platform), its own context type, and binding/validation conventions that would replace code that is already tested.
- *chi* — `net/http`-compatible and pleasant, but measured slower and allocating more than ServeMux on our routes; its main historical advantage (method and path patterns) is now in the standard library.
- *echo* — similar trade-off to gin (own context, extra dependencies); not benchmarked because the gin result already bounds the possible gain.
- *fiber* (fasthttp) — not `net/http`-compatible: no standard middleware, no HTTP/2, different request lifecycle, and fasthttp's object reuse makes request data unsafe to retain. Not benchmarked; rejected on compatibility grounds.

**Revisit when** profiling of production traffic shows routing in the top contributors, or the route count grows by an order of magnitude. The two benchmarks make that check a one-command exercise.
