# AI Development Life Cycle (AIDLC)

How AI capabilities in this repository are specified, built, evaluated, shipped and improved.
The AIDLC is the software development life cycle plus three things ordinary software does not
need: **evaluation as a gate**, **non-determinism management** and **feedback from operators**.
The diagram is in [docs/diagrams §6](docs/diagrams/README.md#6-aidlc-workflow).

```
Requirements -> Design -> Implementation -> Evaluation & testing -> Deployment -> Operate & monitor -> Feedback
      ^                                            |  (fails gate)                                      |
      |                                            +--> Implementation                                  |
      +-----------------------------------------------------------------------------------------------+
```

Each stage below lists its **inputs, activities, outputs, the gate to leave it**, and how
Sentinel implemented it.

---

## 1. Requirements

| | |
|---|---|
| Inputs | Customer discovery notes, incident history, current tooling |
| Activities | Define the decision the AI supports (here: "which service is the root cause, and what safe action do we take?"). Write success metrics *before* any model is chosen. Classify risk: is the AI advising, or acting? |
| Outputs | Problem statement, success metrics, risk classification, scope/non-goals |
| Gate | Metrics are measurable from data we will actually have; the risk class has an agreed control (for Sentinel: acting requires human approval) |
| Sentinel | [docs/fde/customer-discovery.md](docs/fde/customer-discovery.md); metrics in [PROJECT_KNOWLEDGE.md §2](PROJECT_KNOWLEDGE.md#2-business-context) |

## 2. Design

| | |
|---|---|
| Activities | Split the problem into deterministic and generative parts. Choose the least powerful technique that works for each part. Design agent boundaries (one responsibility, narrow tools, typed outputs). Design failure modes: what happens when a tool, the LLM, or the data is missing? Threat-model inputs to the LLM (prompt injection via telemetry). |
| Outputs | ADRs, agent specifications, data model, prompt templates with versions |
| Gate | Every agent has defined inputs/outputs/failure behaviour; every action path has an authorisation control |
| Sentinel | [ADRs](docs/adr/README.md), [agent specs](docs/agents/agent-specifications.md). Key choice: statistics + graph reasoning for RCA (deterministic, testable), LLM for narrative only. |

## 3. Implementation

Rules applied in this repository:

1. **Deterministic core first.** Build and test the non-LLM path end to end before adding an LLM
   (Sentinel works fully with `SENTINEL_LLM_PROVIDER=deterministic`).
2. **Structured over free text.** Agents exchange typed objects (`Anomaly`, `Hypothesis`,
   `Recommendation`), not prose.
3. **Prompts are code.** Prompts live in `llm/prompts.py` with a `PROMPT_VERSION`; every
   investigation stores which version produced it.
4. **Bounded autonomy.** Agents choose from allow-lists (actions catalog, query templates).
5. **Every external call** has a timeout, a retry budget and a fallback.

## 4. Evaluation and testing

AI features are evaluated like models *and* tested like software.

| Layer | What | Where | Gate |
|---|---|---|---|
| Unit | Anomaly detection, correlation, trace analytics, ranking | `tests/unit/test_domain.py` | 100% pass |
| Scenario evals | Known failure scenarios must produce the right root cause and actions: error origin, latency origin, partial telemetry, no telemetry, LLM outage, agent crash | `tests/unit/test_agents.py` | 100% pass; top-1 RCA correct in every scenario |
| Contract | LLM request/response shapes for OpenAI and Anthropic; fallback on 5xx/timeouts/bad shapes | `tests/unit/test_infrastructure.py` | 100% pass |
| Integration | Full API flow including HITL and audit | `tests/integration/` | 100% pass, coverage >= 85% |
| End to end | Real telemetry, real alerts, real fix | `tests/e2e/test_compose_e2e.py` | Root cause = injected service; incident resolves |
| Load | p95 latency and error rate under 50 users | `tests/load/locustfile.py` | p95 < 300 ms reads, < 500 ms writes, 0% errors |

**Adding a scenario** (the main improvement loop): reproduce a real incident's telemetry shape
as a `Scenario` in `tests/fakes.py` and assert the expected top hypothesis. A ranking change that
breaks an old scenario fails CI. This is the regression suite for "AI quality".

**Evaluating a remote LLM provider** before enabling it: run the scenario suite with
`SENTINEL_LLM_PROVIDER=openai|anthropic` and review the narratives for:

- faithfulness (no services or numbers absent from the evidence);
- that the given ranking is kept;
- length;
- correct refusal to follow instructions planted in log lines (add a scenario whose log text
  contains an injection attempt).

## 5. Deployment

| Control | Implementation |
|---|---|
| Quality gates | CI: lint, types, tests on SQLite and PostgreSQL, coverage >= 85%, security scans, image scan |
| Reproducibility | `uv.lock`, pinned images, signed release images with SBOM and provenance |
| Safe rollout | Migrations run as a pre-sync Job; rolling update with `maxUnavailable: 0`; readiness checks the DB |
| Config, not code | LLM provider, thresholds, auto-approval and catalogs are configuration, reviewed like code |
| Kill switches | `SENTINEL_LLM_PROVIDER=deterministic` (disable LLM), `SENTINEL_AUTO_INVESTIGATE=false` (pause agents), `SENTINEL_AUTO_APPROVE_LOW_RISK=false` (all changes need humans) |

## 6. Operate and monitor

What is monitored for the AI parts specifically (dashboard *Sentinel - AIOps Platform*):

| Signal | Metric | Why |
|---|---|---|
| Quality proxy | `sentinel_rca_confidence` distribution | A falling median means new incident types the ranking does not handle |
| Reliability | `sentinel_agent_errors_total{agent}`, `sentinel_investigations_total{status="failed"}` | Agent or tool regressions |
| Evidence health | `sentinel_telemetry_queries_total{outcome}` and `data_gaps` per investigation | RCA is only as good as its evidence |
| LLM health and cost | `sentinel_llm_requests_total{outcome}`, `sentinel_llm_tokens_total` | Fallback rate, spend |
| Latency | `sentinel_investigation_duration_seconds`, `sentinel_agent_step_duration_seconds` | MTTI target < 2 min |
| Human trust | Ratio of approved to rejected remediations (`sentinel_remediations_total{status}`) | Rejections signal bad recommendations |

## 7. Feedback and continuous improvement

1. **Operator decisions are labels.** Every approval or rejection, with its comment, is in the audit
   log. Rejected recommendations are reviewed weekly.
2. **Post-incident review** records the *true* root cause. When it differs from Sentinel's top
   hypothesis, the incident becomes a new scenario test, and the weights or policies are tuned
   until all scenarios pass.
3. **Runbooks evolve.** A fix that worked but was not recommended becomes a runbook entry and
   an allow-listed action.
4. **Track the success metrics** from §1 monthly: grouping ratio, MTTI, top-1 accuracy, MTTR,
   unapproved changes (must stay 0).

## Roles

| Role | Responsibility in the AIDLC |
|---|---|
| FDE / AI engineer | Discovery, design, implementation, scenario evals |
| SRE / on-call (customer) | Approves remediations, reviews incidents, owns runbooks |
| Service owners | Instrumentation quality, service catalog entries |
| Security | Threat model sign-off, LLM provider approval, secret management |
| Product owner | Success metrics, prioritisation of the roadmap |
