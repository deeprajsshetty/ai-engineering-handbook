---
title: High error rate (5xx) on a service
signals: [error_rate]
actions: [disable_fault_flag, rollback_deployment, failover_dependency]
---
# High error rate (5xx)

## Symptoms
- `HighErrorRate` alert: more than 5% of requests return 5xx for 30s+.
- Callers of the service usually alert too (errors propagate upstream as 502s).

## Diagnosis
1. Confirm the origin: in Sentinel's RCA, the root-cause service is the anomalous service whose
   own dependencies are healthy. In Tempo, filter `{ status = error }` and look for the deepest
   error span.
2. Check recent changes: deployments, config pushes and feature-flag flips in the last 30 min.
   A bad feature flag or fault-injection flag is the most common, fastest-to-fix cause.
3. Read the error logs in Loki: `{service_name="<svc>"} | severity_text="ERROR"`.

## Mitigation
- If a feature flag / fault flag is active: **disable_fault_flag** (low risk, instantly reversible).
- If the errors began right after a deploy: **rollback_deployment**.
- If an external provider is failing: **failover_dependency** to the secondary provider.

## Verification
Error rate back under 1% for 5 minutes; dependent services' alerts resolve.
