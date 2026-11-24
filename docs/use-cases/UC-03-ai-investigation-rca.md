# UC-03: AI-powered incident investigation and root cause analysis

| Field | |
|---|---|
| **ID / Name** | UC-03 · AI investigation: anomaly detection, correlation, RCA |
| **Priority** | 3 (highest value) |

## Business view

**Problem.** Engineers spend 20 to 40 minutes per incident finding where a failure started.

**Objectives.**

- Within 2 minutes of the first alert, produce ranked root-cause hypotheses.
- Back each hypothesis with evidence an engineer can verify in seconds.
- State clearly what evidence was missing.

**Stakeholders and actors.**

| Actor | Role in this use case |
|---|---|
| On-call engineer | Consumer of the results |
| Service owners | Check the evidence about their service |
| Sentinel worker | Runs the investigation |

**Expected outcomes.** Lower MTTI and MTTR, the right team engaged first, and consistent triage
quality across engineers.

## Functional requirements

1. Investigate automatically whenever an incident opens or changes scope, and on demand with
   `POST /api/v1/incidents/{id}/investigations` (role operator or higher).
2. Scope is the incident's services plus their downstream dependencies, up to depth 2. Root causes
   are often services that never alerted.
3. Collect evidence for each service in scope:
   - golden signals over a baseline window and a recent window;
   - error logs;
   - sampled error and slow traces.
4. Detect anomalies: robust z-score with MAD, and static thresholds when the baseline is too short.
5. Correlate the anomalies with the dependency graph and the alerts: propagation order, blast
   radius, and silent anomalies.
6. Rank root-cause hypotheses using weighted evidence. Report confidence and per-component scores.
7. Generate a narrative and a summary, using an LLM when one is configured and templates otherwise.
8. Persist the full result and update the incident's root cause, confidence and status
   (`identified`).

## Non-functional requirements

| Requirement | Target |
|---|---|
| Time from first alert to identified root cause | Under 2 min (measured: 75 to 80 s end to end, including the alert `for` duration) |
| Investigation runtime | Seconds (measured: 1.3 s with Tempo down; agents run in milliseconds) |
| Degradation | Any backend or the LLM may be down |
| Determinism | Same telemetry gives the same ranking |

## Prerequisites and dependencies

- UC-01 telemetry and the UC-02 incident.
- Prometheus, Loki and Tempo URLs configured.
- Optional: an LLM provider and its API key.

## Inputs and outputs

| | |
|---|---|
| **Inputs** | The incident (services, alerts, start time), the dependency graph, the query templates (`signals.yaml`) |
| **Outputs** | `investigations.result`: anomalies, correlation, hypotheses, narrative, recommendations, summary, report, data gaps, agent steps and LLM usage. Schema in [data-model.md](../architecture/data-model.md). |

## End-to-end workflow

1. The worker claims the job with `FOR UPDATE SKIP LOCKED` and takes a lease.
2. Build the window. The split point is 120 s before the first alert, with a baseline of about
   25 min before it. Build the scope.
3. Run the LangGraph pipeline:
   - **Telemetry Analysis**: run PromQL, LogQL and TraceQL queries; compute error-origin spans and
     per-service self-time.
   - **Anomaly Detection**: flag abnormal signals.
   - **Incident Correlation**: compute the propagation order and find silent anomalies.
   - **Root Cause Analysis**: rank hypotheses and write the narrative.
   - **Remediation Recommendation**: see UC-04.
   - **Reporting**: write the summary and the report.
   - If no evidence is available, the graph skips directly to Reporting.
4. Persist everything in one transaction and write an audit event. Send a notification.

## Component interactions

See [agent orchestration diagram 5](../diagrams/README.md#5-ai-agent-orchestration-and-execution-flow)
and [agent-specifications.md](../agents/agent-specifications.md).

## AI agent responsibilities

| Agent | Responsibility |
|---|---|
| Telemetry Analysis | Gather evidence and record gaps |
| Anomaly Detection | Decide which signals are abnormal and when they started |
| Incident Correlation | Determine propagation, blast radius and silent anomalies |
| Root Cause Analysis | Rank hypotheses deterministically; the LLM writes the narrative |
| Reporting | Write the executive summary and the markdown report |

## Exception and failure handling

| Failure | Behaviour |
|---|---|
| A backend is unreachable or disabled | Recorded in `data_gaps`. The remaining evidence is still used. |
| No backends | The graph routes straight to Reporting, which states "insufficient evidence". |
| LLM error or timeout | Falls back to templates (`llm_usage[].fallback=true`). The narrative is still produced. |
| An agent crashes | Isolated: that step is marked `ok=false` and the pipeline continues. |
| Worker crash | The lease expires, the job is retried (up to 3 attempts), then marked failed, which triggers the `SentinelInvestigationsFailing` alert. |

## Security considerations

- Service names are validated before they are inserted into query templates (prevents PromQL and
  LogQL injection).
- Log text sent to the LLM is delimited and marked as data, and the LLM cannot select actions.
- No telemetry leaves the network unless a remote LLM is configured.

## Observability requirements

`sentinel_investigation_duration_seconds`, `sentinel_agent_step_duration_seconds{agent}`,
`sentinel_agent_errors_total`, `sentinel_rca_confidence`, `sentinel_telemetry_queries_total`,
`sentinel_llm_*`, and OTel spans `investigation.run` and `agent.<name>`.

## Acceptance criteria

- [x] Errors injected into payments: top hypothesis is payments with cause `error_rate`. Measured
  confidence: 68% live, at least 50% in the scenario test.
- [x] Latency injected into inventory: top hypothesis is inventory with cause `latency_p95`.
  Measured confidence: 76% live.
- [x] Payments is flagged as a silent anomaly when it did not alert (scenario test).
- [x] With Tempo down, the investigation completes, names the gap, and keeps the correct root
  cause (live test).
- [x] With all backends down, there are no hypotheses, three data gaps, and the report has a
  "Data gaps" section.

## Testing scenarios

`tests/unit/test_agents.py` covers six scenarios: errors, latency, all backends down, partial
backends, LLM outage and agent crash. `tests/unit/test_domain.py` covers the algorithms. The e2e
test is `tests/e2e/test_compose_e2e.py`. An example output is
[example-incident-report.md](example-incident-report.md).
