# ADR-0001: Layered package structure

**Status:** Accepted · **Date:** 2026-11-10

## Context
The platform mixes pure analytics (anomaly detection, ranking), I/O-heavy adapters (Prometheus,
Loki, Tempo, PostgreSQL, LLM APIs) and an HTTP interface. Mixing them makes the analytics hard to
test and the adapters hard to swap.

## Decision
`domain/` (pure functions and value objects, no I/O) <- `application/` (use cases, transactions)
<- `api/` and `worker.py` (interfaces). `infrastructure/`, `llm/`, `knowledge/` are adapters
injected through a `Container` composition root. `agents/` sits between application and domain:
each agent is a function of `(state, deps)`.

## Alternatives
* Feature folders (`incidents/`, `alerts/`): good for large teams, but cross-cutting analytics
  would be duplicated.
* Full ports-and-adapters with abstract interfaces everywhere: more ceremony than value here;
  a `Protocol` exists only where we have two implementations (telemetry backend, LLM provider).

## Consequences
Domain logic is unit-tested directly, without mocks. Tests build a `Container` with fakes instead
of patching globals. New adapters (e.g. Datadog metrics) implement `TelemetryBackend`.
