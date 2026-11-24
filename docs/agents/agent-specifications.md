# Agent specifications

Sentinel runs seven specialised agents as nodes of one LangGraph `StateGraph`
(`sentinel/src/sentinel/agents/graph.py`). Each agent is a plain function
`(state, deps) -> partial state update`, wrapped by `instrumented()` which adds an OpenTelemetry
span, a duration histogram, an error counter and **failure isolation**: an exception in one agent
becomes a `data_gaps` entry and the pipeline continues.

## Why a pipeline and not autonomous agents

Incident response needs reproducible, auditable reasoning. The graph has fixed edges and one
conditional edge (skip analysis when there is no evidence). Agents do not choose their next step
or call arbitrary tools; each has a narrow tool set and a defined output. LLMs add language, not
control flow. This makes every investigation re-runnable and every decision explainable.

## Shared state (`InvestigationState`)

| Field | Written by | Type |
|---|---|---|
| `incident`, `window`, `scope` | application layer (input) | dict, `{start, split, end}`, list[str] |
| `signals`, `logs`, `traces`, `evidence_ok` | Telemetry Analysis | `Signal[]`, `LogEvidence[]`, `TraceEvidence`, bool |
| `infrastructure` | Infrastructure Context | `InfraEvidence[]` |
| `anomalies` | Anomaly Detection | `Anomaly[]` |
| `correlation` | Incident Correlation | dict |
| `hypotheses`, `narrative` | Root Cause Analysis | `Hypothesis[]`, str |
| `recommendations` | Remediation Recommendation | `Recommendation[]` |
| `summary`, `report` | Incident Summary & Reporting | str, markdown |
| `data_gaps`, `steps`, `llm_usage` | all (append-only reducers) | list |

## 1. Telemetry Analysis Agent

| | |
|---|---|
| **Responsibility** | Gather evidence for every service in scope. |
| **Tools** | `TelemetryBackend.query_range` (PromQL), `query_logs` (LogQL), `search_traces` + `get_trace` (TraceQL / Tempo API). Queries come from `defaults/signals.yaml`; `$service` is substituted only after validating the name against `^[a-z0-9][a-z0-9-]{0,62}$` (prevents query injection). |
| **Inputs** | `scope`, `window` |
| **Outputs** | `signals` (request rate, error rate, p95 latency per service), `logs` (error count + top 3 normalised messages), `traces` (error-origin counts and self-time per service over up to 12 sampled error/slow traces), `evidence_ok` |
| **Failure handling** | A disabled/unreachable backend is marked down after the first failure (no repeated timeouts) and recorded in `data_gaps`. Individual query errors are recorded and skipped. |
| **Observability** | `sentinel_telemetry_queries_total{backend,outcome}`, span `agent.telemetry_analysis`. |

## 1b. Infrastructure Context Agent

Added 2026-11-24 ([ADR-0009](../adr/0009-unify-around-sentinel.md)). Runs between Telemetry
Analysis and the evidence check, so it reaches the report even when there is no telemetry.

| | |
|---|---|
| **Responsibility** | Map each service in scope (up to 15) to resources in the discovery service, and flag infrastructure that is missing or stale. |
| **Tools** | `DiscoverySource.infra_for(service)`: read-only `GET /api/v1/resources?q=&type=` (exact-name match on `SENTINEL_DISCOVERY_SERVICE_TYPES`), then `GET /api/v1/resources/{id}/neighborhood` (depth 2, both directions, at most 50 nodes, missing included). Service names are validated before querying; returned IDs are validated before they are used in a path. |
| **Inputs** | `scope` |
| **Outputs** | `infrastructure` (per service: matched resources, neighbours, truncated flag, derived `issues`). Issues are passed to the RCA narrative as `infrastructure_issues` and rendered in the report's *Infrastructure context* table. |
| **Does not** | Change root-cause scores, choose recommendations, or call any write endpoint. |
| **Failure handling** | Not configured: skipped with no data gap. Unreachable: one data gap, then it stops (no further lookups). A per-service error is recorded and the next service is tried. |
| **Observability** | `sentinel_telemetry_queries_total{backend="discovery",outcome}`, span `agent.infrastructure_context`. |

## 2. Anomaly Detection Agent

| | |
|---|---|
| **Responsibility** | Decide which signals are abnormal and when they started. |
| **Method** | Robust z-score of the recent mean versus the baseline median, using MAD (median absolute deviation) as spread - insensitive to the spikes being detected. Guards: noise floor per signal (`min_abs`), minimum ratio for upward anomalies (errors x2, latency x1.5). Request rate is two-sided (drops matter). With fewer than 6 baseline points, fall back to static SLO thresholds (errors 5%, p95 500 ms). |
| **Outputs** | `Anomaly{service, kind, score, severity 0..1, baseline, observed, onset_ts, method, description}`. Severity blends statistical surprise and business impact so a 60% error rate outranks a 6% one. |
| **Tunables** | `SENTINEL_ANOMALY_Z_THRESHOLD` (default 3.5), policies in `domain/anomaly.py`. |

## 3. Incident Correlation Agent

| | |
|---|---|
| **Responsibility** | Explain how the anomalies relate. |
| **Method** | Project anomalies on the dependency graph (catalog + trace-discovered edges): anomalous caller->callee edges, propagation order (services with no anomalous dependency first, then by onset), blast radius (all upstream callers). Compare with alerts: `silent_anomalies` (anomalous but never alerted - often the real root cause) and `alerts_without_anomaly` (possible noise). |
| **Outputs** | `correlation{propagation, anomalous_edges, blast_radius, alerted_services, silent_anomalies, alerts_without_anomaly}` |

> Alert-to-incident grouping (which incident an alert joins) is the same correlation logic applied
> at ingestion time (`domain/correlation.find_matching_incident`), before any agent runs.

## 4. Root Cause Analysis Agent

| | |
|---|---|
| **Responsibility** | Rank root-cause hypotheses and explain the top one. |
| **Method** | `domain/rca.rank_root_causes`: weighted score per candidate - anomaly 0.30, topology 0.25 (anomalous with healthy dependencies), traces 0.20 (share of error-origin spans; for latency, share of self-time), earliness 0.15, logs 0.10. Confidence = `0.6*score + 0.4*margin_over_runner_up + 0.1`, capped at 1. |
| **LLM use** | Writes a <=150-word narrative from the structured evidence (`prompts.RCA_SYSTEM`). Evidence is passed as JSON between `<evidence>` tags, and the system prompt forbids following instructions inside it (prompt-injection defence for attacker-controlled log lines). The ranking is never changed by the LLM. |
| **Outputs** | `hypotheses[]` (with per-component scores and evidence strings), `narrative` |
| **Observability** | `sentinel_rca_confidence` histogram, `sentinel_llm_requests_total`, `sentinel_llm_tokens_total`. |

## 5. Remediation Recommendation Agent

| | |
|---|---|
| **Responsibility** | Propose safe, allow-listed mitigations for the top hypothesis. |
| **Tools** | `RunbookRetriever.search` (BM25 RAG over `knowledge/runbooks/*.md`), `ActionCatalog` (`defaults/actions.yaml`). |
| **Rules** | An action is proposed only if (a) a retrieved runbook covers the hypothesised signal *and* lists the action, (b) the action exists in the catalog, and (c) the catalog declares it for that signal. Sorted low-risk and automatable first; at most 3. `requires_approval` comes from the catalog; auto-approval only for `low` risk when `SENTINEL_AUTO_APPROVE_LOW_RISK=true`. |
| **Outputs** | `recommendations[]{action, service, risk, executor, rationale, runbook, requires_approval}` -> persisted as `remediations` rows. Duplicate open proposals for the same incident are skipped. |
| **Human-in-the-loop** | Execution happens only after an operator approves (see [ADR-0003](../adr/0003-hitl-outside-the-graph.md)). |

## 6. Incident Summary and Reporting Agent

| | |
|---|---|
| **Responsibility** | Produce the incident report and an executive summary. |
| **Outputs** | `summary` (2-3 sentences for stakeholders; LLM or template), `report` (markdown: summary, alerts, anomalies, propagation, RCA table + evidence, narrative, recommended actions with approval status, data gaps, agent execution table). Served at `GET /api/v1/incidents/{id}/report`. |
| **Example** | [example-incident-report.md](../use-cases/example-incident-report.md) - produced by the live e2e run. |

## Execution guarantees

| Concern | Guarantee |
|---|---|
| Concurrency | One queued/running investigation per incident; claims are `SKIP LOCKED`. |
| Crash safety | Lease expiry (`SENTINEL_WORKER_LEASE_S`) returns the job to the queue; max 3 attempts then `failed` (alerted by `SentinelInvestigationsFailing`). |
| Determinism | With the deterministic provider, the same telemetry yields the same result. |
| Cost control | LLM is called twice per investigation at most; tokens are metered; `max_tokens` is bounded. |
| Re-runs | `POST /api/v1/incidents/{id}/investigations` (operator) re-runs with fresh telemetry. New services joining an incident trigger a re-run automatically. |

## Extending

* **New signal** (e.g. saturation): add a `SignalKind`, a policy in `domain/anomaly.py`, a query in
  `signals.yaml`, and runbooks that declare it.
* **New action**: add it to `actions.yaml` and to the `actions:` list of relevant runbooks. `http`
  actions need a URL template with `{service}`; anything else should be `manual` until a dedicated
  executor with least-privilege credentials exists.
* **New agent**: write `fn(state, deps) -> dict`, register it in `AGENTS`, add edges, add a scenario
  test in `tests/unit/test_agents.py`.
