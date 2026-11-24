# Operational runbooks

Two kinds of runbook live in this repository:

| Kind | Location | Read by |
|---|---|---|
| **Incident runbooks**: how to fix a failing *customer service* | `sentinel/src/sentinel/knowledge/runbooks/` | Humans *and* the Remediation agent. Front-matter `signals` and `actions` drive recommendations, so keep the format. |
| **Platform runbooks**: how to operate *Sentinel itself* | this folder | Platform team / SRE |

Incident runbooks (agent knowledge base):
[high error rate](../../sentinel/src/sentinel/knowledge/runbooks/high-error-rate.md) ·
[high latency](../../sentinel/src/sentinel/knowledge/runbooks/high-latency.md) ·
[traffic drop](../../sentinel/src/sentinel/knowledge/runbooks/traffic-drop.md) ·
[telemetry pipeline](../../sentinel/src/sentinel/knowledge/runbooks/telemetry-pipeline.md)

Platform runbooks:

| Alert / situation | Runbook |
|---|---|
| `SentinelApiDown` | [sentinel-platform.md#sentinelapidown](sentinel-platform.md#sentinelapidown) |
| `SentinelInvestigationsFailing` / `SentinelQueueBacklog` | [sentinel-platform.md#investigations-failing-or-backlog](sentinel-platform.md#investigations-failing-or-backlog) |
| `TelemetryPipelineDown` | [sentinel-platform.md#telemetrypipelinedown](sentinel-platform.md#telemetrypipelinedown) |
| Database outage / restore | [sentinel-platform.md#database-outage-and-restore](sentinel-platform.md#database-outage-and-restore) |
| Secret rotation | [sentinel-platform.md#secret-rotation](sentinel-platform.md#secret-rotation) |
| A bad automated action executed | [sentinel-platform.md#bad-remediation-executed](sentinel-platform.md#bad-remediation-executed) |

## Adding an incident runbook

```markdown
---
title: Database connection pool exhausted
signals: [latency_p95, error_rate]
actions: [restart_service, scale_out]
---
# Database connection pool exhausted
## Symptoms ...
## Diagnosis ...
## Mitigation ...   (name actions exactly as in actions.yaml)
## Verification ...
```

Then add a scenario test asserting the recommendation (`tests/unit/test_agents.py`).
