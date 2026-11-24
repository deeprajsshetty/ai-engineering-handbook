---
title: Telemetry pipeline degraded
signals: []
actions: []
---
# Telemetry pipeline degraded

If Sentinel reports "insufficient evidence", check the pipeline before trusting a quiet
dashboard:
1. OpenTelemetry Collector health: `http://otel-collector:13133/`.
2. Prometheus targets page: is `otel-collector` UP?
3. Loki and Tempo readiness endpoints: `/ready`.
4. Services export to `OTEL_EXPORTER_OTLP_ENDPOINT`; check for exporter errors in their logs.
