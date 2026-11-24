# Customer discovery and problem definition (FDE phases 1-2)

This is the discovery record for the Sentinel engagement. The customer is an archetype
("Acme Retail") assembled from common enterprise patterns. Assumptions are labelled as such,
so a real engagement can replace them with interview data.

## 1. Customer environment

| Dimension | Finding |
|---|---|
| Workloads | ~40 services (Python, Java, Go) on Kubernetes across 2 regions; a few VMs and managed databases |
| Existing telemetry | Prometheus + Alertmanager for metrics and alerts; logs in a mix of CloudWatch and files; tracing partial (some services on OpenTelemetry) |
| On-call | 6 teams, follow-the-sun; pager via Alertmanager -> PagerDuty/Slack |
| Change rate | ~30 deploys/day; feature flags used heavily |
| Constraints | No customer data may leave the VPC; LLM use allowed only via an approved provider *or* not at all; SOC 2 audit trail requirements; changes to production need an approver |

## 2. Stakeholders

| Stakeholder | Interest | What they need from the solution |
|---|---|---|
| VP Engineering (sponsor) | MTTR, engineer burnout, audit findings | Measurable MTTR reduction, adoption by teams |
| SRE lead | Fewer pages, faster triage | Correlated incidents, root cause with evidence, runbook at hand |
| On-call engineers | Sleep; not being blamed for a bad 3 a.m. fix | Clear evidence, safe one-click mitigations, approval trail |
| Service owners | Their service not mislabelled as root cause | Transparent ranking, ability to correct the catalog |
| Security / compliance | Data handling, change control | No secrets in logs, RBAC, audit log, LLM optional and controlled |
| Platform team | Operability of the new system | Standard stack, containers, Helm/Kustomize, its own monitoring |

## 3. Discovery activities (as run)

1. **Shadowed two incident retrospectives.** Timeline reconstruction showed that 60-70% of the time
   to mitigate went into locating the origin service.
2. **Alert audit (last 30 days, assumption-based sample).** Median 4 alerts per incident; the worst
   incident produced 23 pages across 7 services.
3. **Runbook inventory.** About 50 runbooks in a wiki; on-call engineers said they rarely found
   the right one during an incident.
4. **Change analysis.** In the sample, 45% of incidents started with a deploy or a flag/config change.
   The fastest mitigation was turning the change off.
5. **Constraint workshop** with security: LLM usage must be optional, no telemetry to unapproved
   vendors, and humans approve production changes.

## 4. Pain points, ranked

| # | Pain | Impact | Evidence |
|---|---|---|---|
| P1 | Cascading alerts page multiple teams for one failure | Alert fatigue; wrong team engaged first | Alert audit |
| P2 | Finding the origin service is slow and manual | 20-40 min of MTTR | Retrospectives |
| P3 | Runbooks are not surfaced in context | Inconsistent, slower fixes | Interviews |
| P4 | Manual fixes under pressure, weak audit trail | Risk, compliance findings | Security workshop |
| P5 | Metrics, logs and traces are not linked | Context switching | Shadowing |

## 5. Requirements

### Functional

| ID | Requirement | Priority |
|---|---|---|
| FR-1 | Collect metrics, logs and traces from services through one open-standard pipeline | Must |
| FR-2 | Alert on golden signals per service | Must |
| FR-3 | Group related alerts into one incident (time + dependency topology) | Must |
| FR-4 | Automatically investigate incidents: anomalies, propagation, ranked root causes with evidence | Must |
| FR-5 | Recommend remediations from runbooks; execute only after human approval | Must |
| FR-6 | Incident timeline, report, and audit log of every action | Must |
| FR-7 | Dashboards for services, infrastructure and the platform itself | Must |
| FR-8 | Service dependency map derived from traces | Should |
| FR-9 | Notifications to chat | Should |
| FR-10 | Expose incident data to other AI agents (MCP) without allowing changes | Could |

### Non-functional

| ID | Requirement | Target |
|---|---|---|
| NFR-1 | Time from first alert to identified root cause | < 2 min (p50) |
| NFR-2 | API latency | p95 < 300 ms reads, < 500 ms alert ingestion |
| NFR-3 | Availability of the platform | 99.9%; no single replica required |
| NFR-4 | Degradation | Any one telemetry backend or the LLM down -> investigation still completes and states the gap |
| NFR-5 | Security | RBAC, hashed credentials, no secrets in repo/logs, signed images, non-root containers |
| NFR-6 | Data residency | Works with no LLM, or with an approved provider via configuration |
| NFR-7 | Operability | Standard containers; its own metrics, logs, traces and alerts |
| NFR-8 | Auditability | Every state change attributable to a human or system actor |

## 6. Problem statement

> On-call engineers at Acme lose most of every incident locating the failing service among
> cascading alerts, then apply fixes manually with weak traceability. We will give them **one
> incident per failure, a ranked and evidenced root cause within two minutes, and a safe,
> approved, audited path to the first mitigation**, using open telemetry standards and without
> requiring an LLM.

## 7. Scope

| In scope (v1) | Out of scope (v1) |
|---|---|
| Golden-signal alerting, correlation, RCA, runbook RAG, HITL remediation (flag disable via HTTP; others manual) | Fully autonomous remediation |
| Service, platform and infrastructure dashboards | Replacing PagerDuty / ticketing |
| Compose demo and Kubernetes manifests | Multi-tenant SaaS, SSO (roadmap) |
| Deterministic and optional LLM narrative | LLM-chosen actions or free-form tool use |

## 8. Use-case prioritisation

| Use case | Value | Effort | Risk | Priority |
|---|---|---|---|---|
| [UC-01](../use-cases/UC-01-unified-telemetry.md) Unified telemetry and dashboards | High (foundation) | Medium | Low | 1 |
| [UC-02](../use-cases/UC-02-alert-correlation.md) Alert ingestion and incident correlation | High (P1) | Low | Low | 2 |
| [UC-03](../use-cases/UC-03-ai-investigation-rca.md) AI investigation and RCA | Very high (P2) | High | Medium | 3 |
| [UC-04](../use-cases/UC-04-hitl-remediation.md) Human-approved remediation | High (P3, P4) | Medium | High (changes prod) | 4 |
| [UC-05](../use-cases/UC-05-dependency-mapping.md) Dependency mapping and service health | Medium (P5) | Low | Low | 5 |
| [UC-06](../use-cases/UC-06-reporting-and-agent-access.md) Reporting, notification, MCP access | Medium | Low | Low | 6 |

## 9. Assumptions and ambiguities

| # | Ambiguity in the brief | Assumption taken |
|---|---|---|
| A1 | "Production-grade" without a target cloud | Cloud-agnostic Kubernetes manifests plus a fully working Compose stack; managed PostgreSQL in production |
| A2 | Which LLM vendor | None required; OpenAI and Anthropic supported by configuration |
| A3 | Kafka "where justified" | Not justified at alert volume (ADR-0002) |
| A4 | Tempo or Jaeger | Tempo (Grafana integration, object storage) |
| A5 | What remediation may be automated | Only allow-listed actions, only after approval; in the demo, disabling a fault flag |
| A6 | Existing repo is a learning handbook, not an observability codebase | Preserve the handbook unchanged; build the platform alongside it |
