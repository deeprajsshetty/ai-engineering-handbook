# Incident #1: HighErrorRate on frontend

**Severity:** critical  
**Status:** investigating  
**Services:** checkout, frontend, payments  
**Started:** 2026-11-09T19:18:23.979000+00:00

## Executive summary
CRITICAL incident affecting checkout, frontend, payments, triggered by 3 alert(s). Root cause: payments (error_rate, 68% confidence). Recommended next step: disable_fault_flag on payments (awaiting human approval).

## Alerts
| Alert | Service | Severity | Status | Started |
|---|---|---|---|---|
| HighErrorRate | frontend | critical | firing | 2026-11-09T19:18:23.979000+00:00 |
| HighErrorRate | checkout | critical | firing | 2026-11-09T19:18:23.979000+00:00 |
| HighErrorRate | payments | critical | firing | 2026-11-09T19:18:23.979000+00:00 |

## Anomalies
| Service | Signal | Observed vs baseline | Onset | Method |
|---|---|---|---|---|
| checkout | error_rate | error_rate at 16.3% exceeds static threshold 5.0% (insufficient baseline) | 19:17:53 | static_threshold |
| payments | error_rate | error_rate at 16.3% exceeds static threshold 5.0% (insufficient baseline) | 19:17:53 | static_threshold |
| frontend | error_rate | error_rate at 6.2% exceeds static threshold 5.0% (insufficient baseline) | 19:17:53 | static_threshold |

## Propagation
Origin first: payments <- checkout <- frontend
Blast radius: checkout, frontend, payments

## Root cause analysis
| Rank | Service | Cause | Confidence | Score |
|---|---|---|---|---|
| 1 | payments | error_rate | 68% | 0.80 |
| 2 | checkout | error_rate | 27% | 0.60 |
| 3 | frontend | error_rate | 14% | 0.42 |

**Evidence for top hypothesis:**
- error_rate at 16.3% exceeds static threshold 5.0% (insufficient baseline)
- it has no downstream dependencies, so the failure starts here
- 12 error span(s) originate here across 12 sampled traces
- 153 error log line(s) in the window

The most likely root cause is **payments** (error_rate), confidence 68%. Evidence: error_rate at 16.3% exceeds static threshold 5.0% (insufficient baseline); it has no downstream dependencies, so the failure starts here; 12 error span(s) originate here across 12 sampled traces; 153 error log line(s) in the window. The failure appears to propagate frontend -> checkout -> payments (caller to origin), which explains why upstream services alerted as well. Alternative hypothesis: checkout (error_rate, confidence 27%).

## Recommended remediation (human approval required)
- **disable_fault_flag** on `payments` - risk low, http executor, approval required. Runbook 'High error rate (5xx) on a service' lists disable_fault_flag for error_rate incidents. Turn off the faulty feature flag (fault injection) on the service. Fastest, fully reversible mitigation when a bad flag or config push caused errors/latency.
- **rollback_deployment** on `payments` - risk medium, manual executor, approval required. Runbook 'High error rate (5xx) on a service' lists rollback_deployment for error_rate incidents. Roll the service back to the previous known-good release.
- **failover_dependency** on `payments` - risk high, manual executor, approval required. Runbook 'High error rate (5xx) on a service' lists failover_dependency for error_rate incidents. Route traffic to a secondary provider / region for the failing dependency.

## Agent execution
| Agent | Duration | Result |
|---|---|---|
| telemetry_analysis | 170 ms | 12/12 series with data across 4 services, 461 error logs, 12 traces |
| anomaly_detection | 0 ms | 3 anomalies on 3 services: checkout, frontend, payments |
| incident_correlation | 0 ms | propagation payments <- checkout <- frontend |
| root_cause_analysis | 0 ms | root cause payments (error_rate, 68%) |
| remediation_recommendation | 0 ms | 3 recommendation(s): disable_fault_flag, rollback_deployment, failover_dependency |
