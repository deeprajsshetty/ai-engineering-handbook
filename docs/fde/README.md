# Forward Deployed Engineering playbook

How this engagement was run, phase by phase. Use it as a template for similar work: replace the
Sentinel specifics, keep the structure and the exit criteria.

An FDE sits with the customer, owns the outcome rather than a ticket, and ships working software
into the customer's environment. That means the plan is organised around **decisions and evidence**,
not documents. Every phase below ends with an exit criterion that someone other than the engineer
can check.

| Phase | Core question | Key artefact |
|---|---|---|
| [1. Customer discovery](#phase-1-customer-discovery) | What hurts, for whom, and how much? | [customer-discovery.md](customer-discovery.md) |
| [2. Problem definition](#phase-2-problem-definition) | What exactly will we change, and how will we know? | Problem statement, metrics, scope |
| [3. Solution architecture](#phase-3-solution-architecture) | What is the simplest design that meets the requirements? | [ARCHITECTURE.md](../../ARCHITECTURE.md), [ADRs](../adr/README.md) |
| [4. Development](#phase-4-development-and-implementation) | Does a thin slice work end to end? | `sentinel/`, `demo-services/`, `deploy/` |
| [5. Validation](#phase-5-validation-and-testing) | Is it correct, fast, safe and resilient? | [test-strategy.md](../testing/test-strategy.md) |
| [6. Deployment](#phase-6-deployment) | Can the customer run it themselves? | [DEPLOYMENT_GUIDE.md](../../DEPLOYMENT_GUIDE.md), CI/CD |
| [7. Adoption and improvement](#phase-7-customer-adoption-and-continuous-improvement) | Is it used, and is it getting better? | Demo script, metrics review, [AIDLC.md](../../AIDLC.md) loop |

---

## Phase 1: Customer discovery

| | |
|---|---|
| **Objectives** | Understand the environment, constraints and pain in the customer's own numbers. |
| **Activities** | Stakeholder interviews (sponsor, SRE lead, on-call, security, platform); shadow incident retrospectives; alert-volume audit; runbook inventory; constraint workshop (data residency, LLM policy, change control). |
| **Deliverables** | Environment map, stakeholder matrix, ranked pain points with evidence, constraints list. |
| **Dependencies** | Sponsor access; read access to alert history and retrospectives. |
| **Exit criteria** | The sponsor agrees the top three pains are the right ones; every pain has a measurable symptom. |
| **Sentinel outcome** | P1 cascading pages, P2 slow origin-finding, P3 unused runbooks, P4 risky manual fixes. Constraint: LLM optional, humans approve changes. |

## Phase 2: Problem definition

| | |
|---|---|
| **Objectives** | Turn pains into a bounded problem with success metrics. |
| **Activities** | Write the problem statement; define metrics and baselines (alerts per incident, MTTI, MTTR, RCA accuracy, unapproved changes); agree scope and non-goals; prioritise use cases by value, effort and risk. |
| **Deliverables** | Problem statement, metric table with targets, scope table, prioritised use-case list, assumptions log. |
| **Dependencies** | Phase 1 evidence; a product owner to arbitrate scope. |
| **Exit criteria** | Signed-off metrics with a measurement method; explicit non-goals (e.g. "no autonomous remediation in v1"). |
| **Sentinel outcome** | MTTI < 2 min, >= 3:1 alert grouping, 0 unapproved changes. Six use cases, UC-01 to UC-06. |

## Phase 3: Solution architecture

| | |
|---|---|
| **Objectives** | Choose the least complex architecture that meets functional and non-functional requirements. |
| **Activities** | Context and component design; technology selection with written alternatives (ADRs); data model; agent specifications; threat model; failure-mode analysis (what if Prometheus, Loki, Tempo, the LLM or a worker dies?). |
| **Deliverables** | Architecture doc and diagrams, ADRs, data model, agent specs, security design. |
| **Dependencies** | Platform team input on what they can operate (they already run Prometheus: build on it). |
| **Exit criteria** | Architecture review passed by SRE + security; every NFR mapped to a mechanism and a test. |
| **Sentinel outcome** | OTel + Prometheus/Loki/Tempo/Grafana; FastAPI + LangGraph; PostgreSQL as DB and queue; deterministic RCA; persisted HITL. Kafka and a vector DB explicitly rejected. |

## Phase 4: Development and implementation

| | |
|---|---|
| **Objectives** | Deliver a thin end-to-end slice first, then widen it. |
| **Activities** | (1) Foundation: repo hygiene, secrets scrub, project skeleton, CI. (2) Telemetry: instrument demo services, collector pipelines, alert rules, dashboards. (3) Backend: ingestion, correlation, persistence, migrations, auth. (4) Agents: deterministic analytics, graph orchestration, RAG, LLM adapters. (5) Console and HITL. |
| **Deliverables** | Working code with tests at every layer; Compose stack that runs the whole use case. |
| **Dependencies** | A representative workload (here, the instrumented demo shop). |
| **Exit criteria** | Inject a fault, then get the right root cause and a working approved fix, on the full stack. |
| **Sentinel outcome** | Done: see the [validation report](../testing/test-strategy.md#validation-report). |

## Phase 5: Validation and testing

| | |
|---|---|
| **Objectives** | Prove correctness, performance, security and resilience with evidence. |
| **Activities** | Unit and scenario evals; integration on SQLite and PostgreSQL; e2e fault injection (errors and latency); load test; static analysis, dependency audit, secret scan, image scan; resilience tests (backend down, LLM down, agent crash, worker crash). |
| **Deliverables** | Test suite, CI gates, validation report with measured numbers. |
| **Dependencies** | Running stack; CI runners with Docker. |
| **Exit criteria** | All gates green; measured MTTI and latency inside targets; no high or critical vulnerabilities. |

## Phase 6: Deployment

| | |
|---|---|
| **Objectives** | Make the customer's platform team self-sufficient. |
| **Activities** | Containerise (non-root, read-only, healthchecks); CI/CD with signed, multi-arch images and SBOMs; Kubernetes manifests (probes, HPA, PDB, NetworkPolicy, PSA restricted, External Secrets); migration job; alerting on the platform itself; backup and DR plan. |
| **Deliverables** | Deployment guide, manifests, pipelines, runbooks. |
| **Dependencies** | Cluster, managed PostgreSQL, secret manager, registry, DNS/TLS from the customer. |
| **Exit criteria** | The platform team deploys to staging from the guide without FDE help; a restore drill passes. |

## Phase 7: Customer adoption and continuous improvement

| | |
|---|---|
| **Objectives** | Get real usage, prove the business outcome, and set up the improvement loop. |
| **Activities** | Live demo with fault injection (script below); shadow mode first (Sentinel investigates, humans act as before), then approvals through Sentinel; weekly review of rejected recommendations and wrong RCAs; monthly metric review against Phase 2 baselines; cost review (LLM tokens, storage retention). |
| **Deliverables** | Demo recording, adoption plan, metrics dashboard, backlog from feedback. |
| **Dependencies** | On-call teams' time; post-incident review discipline. |
| **Exit criteria** | Targets met for 4 consecutive weeks; the customer team owns runbooks, catalogs and tuning. |

### Demo script (15 minutes)

1. **Context (2 min).** Grafana *Shop - Service Golden Signals*: healthy baseline, dependency map
   drawn from traces.
2. **Break it (1 min).** Run `make demo-errors`, which injects a payments fault (a "bad flag").
3. **Without Sentinel (2 min).** Alertmanager shows 3 alerts on 3 services. Ask: which team do you page?
4. **With Sentinel (5 min).** The console shows one incident. Walk through:
   - the root cause (payments) and its evidence: error-origin spans, healthy dependencies, earliest onset;
   - the propagation chain;
   - the agent execution table;
   - the report.
5. **Fix it safely (3 min).** Approve `disable_fault_flag` as an operator (show that a viewer key
   gets a 403). Watch the error rate fall, then the incident auto-resolve and the audit timeline.
6. **Trust and control (2 min).** Show:
   - it works with the LLM disabled;
   - nothing executes without approval;
   - MCP access is read-only;
   - Sentinel monitors itself.

### Running in a real customer environment

| Step | Action |
|---|---|
| 1 | Point your existing Alertmanager at Sentinel's webhook (add a receiver; keep existing receivers). |
| 2 | Load your service catalog via `PUT /api/v1/services/{name}`, or edit `services.yaml`. |
| 3 | Map your metric names in `signals.yaml` (or record `shop:`-style rules on top of existing metrics). |
| 4 | Import your runbooks into `knowledge/runbooks/` with `signals`/`actions` front-matter. |
| 5 | Start in shadow mode: no `http` actions in `actions.yaml`, all `manual`. |
| 6 | Enable automated actions one at a time, each with an owner and a rollback. |
