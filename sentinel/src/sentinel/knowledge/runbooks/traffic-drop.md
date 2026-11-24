---
title: Sudden drop or spike in request rate
signals: [request_rate]
actions: [restart_service, scale_out]
---
# Request-rate anomaly

## Symptoms
Request rate deviates strongly from baseline: a drop often means an upstream outage, DNS or
load-balancer problem; a spike means a retry storm or traffic surge.

## Diagnosis
1. Compare with upstream callers: if they also dropped, the problem is further upstream.
2. Look for retry storms: error rate rising together with request rate.

## Mitigation
- Retry storm or surge: **scale_out** and enable rate limiting at the edge.
- Instance wedged and not receiving traffic: **restart_service**.
