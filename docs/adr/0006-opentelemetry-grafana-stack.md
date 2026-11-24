# ADR-0006: OpenTelemetry + Prometheus, Loki, Tempo, Grafana

**Status:** Accepted · **Date:** 2026-11-10

## Context
The customer runs heterogeneous services and wants to avoid vendor lock-in and per-host pricing.

## Decision
Instrument with OpenTelemetry and send OTLP to a Collector. Store metrics in Prometheus, logs in
Loki, traces in Tempo; visualise in Grafana. Derive the dependency graph in the Collector
(`servicegraph` connector) instead of maintaining it by hand.

## Alternatives
* Jaeger for traces: mature, but Tempo integrates better with Grafana/Loki and uses object storage.
* Elasticsearch for logs: powerful full-text search at much higher storage/ops cost.
* Commercial APM (Datadog, New Relic): fastest to adopt, highest recurring cost; Sentinel's
  `TelemetryBackend` interface allows an adapter if a customer already has one.

## Consequences
Standard query languages (PromQL/LogQL/TraceQL) for Sentinel's tools. Prometheus is single-node;
for long retention or HA, add Thanos/Mimir behind the same API.
