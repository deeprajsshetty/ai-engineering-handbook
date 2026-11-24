---
title: High p95 latency on a service
signals: [latency_p95]
actions: [disable_fault_flag, scale_out, restart_service, rollback_deployment]
---
# High p95 latency

## Symptoms
- `HighLatencyP95` alert: p95 request latency above 500ms for 30s+.
- Upstream callers show the same latency added on top of their own (latency is cumulative).

## Diagnosis
1. Find where the time goes: Sentinel computes per-service *self time* from traces; the service
   with the largest self time is where requests are actually waiting.
2. Check saturation: CPU, memory, connection pools, request rate versus normal.
3. Check recent flag flips and deployments; injected delay or a slow query path are common.

## Mitigation
- Fault/feature flag introduced the delay: **disable_fault_flag**.
- Saturation from load: **scale_out**.
- Stuck workers / leaked connections: **restart_service**.
- Regression in a new release: **rollback_deployment**.

## Verification
p95 latency back within baseline for 5 minutes.
