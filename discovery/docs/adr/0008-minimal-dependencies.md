# ADR 0008 — Standard library first; minimal permissive dependencies

**Status:** Accepted.

**Decision.** Go: standard library (`net/http` routing with method patterns, `log/slog`, `crypto`) plus `pgx` (MIT) for PostgreSQL and, since ADR 0010, `go-ora` (MIT, pure Go) for monitoring Oracle servers; `gopkg.in/yaml.v3` (Apache-2.0/MIT) is test-only (OpenAPI parity test). Web: React and React DOM (MIT) at runtime; Vite, TypeScript, ESLint, Vitest and Testing Library as dev tools. No router, state, chart, graph, UUID, Prometheus client, or validation library: each would replace fewer than ~200 lines of reviewed code here.

**Versions.** Exact pins (go.sum, package-lock.json), each release at least two weeks old at selection time; TypeScript held at 6.0.3 because typescript-eslint 8.72 supports `<6.1`.

**Consequences.** Six Go modules in the binary, three npm packages in the bundle (all MIT/BSD-3-Clause; [inventory](../inventory/dependencies-and-licenses.md)). Revisit when a feature needs, for example, ECharts (PDF T13) or OpenTelemetry.
