# Documentation index

| Document | Purpose |
|---|---|
| [assessment/inventory-and-reconciliation.md](assessment/inventory-and-reconciliation.md) | What was inspected, baseline builds, PDF claims verified or marked stale |
| [design/hld.md](design/hld.md) | Goals, scope, drivers, current and target architecture, deployment, trust boundaries (diagrams C1–C3, T1–T4, D1–D2, S1) |
| [design/lld.md](design/lld.md) | Packages and interfaces, API contract, job state machine, data model (E1), metric semantics, reliability, security, DSA, tests |
| [adr/](adr/README.md) | Architecture decision records 0001–0013 |
| [assessment/mvp-gap-review.md](assessment/mvp-gap-review.md) | Second-pass gap review: closed, open (MVP / production), accepted |
| [roadmap.md](roadmap.md) | Product roadmap from MVP to production, gates and open questions (diagram R1) |
| [product/ux-benchmark.md](product/ux-benchmark.md) | UX patterns from established platforms: adopted, deferred, evidence |
| [guides/extending.md](guides/extending.md) | Add a database engine, provider or domain (diagram X1) |
| [decision-log.md](decision-log.md) | Dated decisions, including fixes found during verification |
| [security/threat-model.md](security/threat-model.md) | Threats, controls, tests, production gaps |
| [operations/configuration.md](operations/configuration.md) | Every environment variable |
| [operations/deployment.md](operations/deployment.md) | Local, compose, Kubernetes public/private, rollout and rollback |
| [operations/runbook.md](operations/runbook.md) | Health, logs, troubleshooting, migrations, backup/restore, cleanup |
| [operations/sdlc-and-cicd.md](operations/sdlc-and-cicd.md) | Pipeline stages, existing vs new (diagram P1) |
| [migration/plan-and-traceability.md](migration/plan-and-traceability.md) | F1–F24 and M1–M9 traceability, phases (diagram P2) |
| [inventory/tooling-and-datastores.md](inventory/tooling-and-datastores.md) | Tools, versions, removals, datastore choice |
| [inventory/dependencies-and-licenses.md](inventory/dependencies-and-licenses.md) | Generated dependency and licence inventory |
| [verification.md](verification.md) | Commands run and exact outcomes |
| [../platform/contracts/openapi.yaml](../platform/contracts/openapi.yaml) | API specification |

## Diagram catalogue

All diagrams are Mermaid in Markdown. Labels are consistent: *obsplatform API*, *obsplatform worker*, *Web UI (React)*, *PostgreSQL*, *Provider*, *Collector*.

| ID | Title | Phase | Location |
|---|---|---|---|
| C1 | Current system context | current state | HLD §2 |
| C2 | Current containers | current state | HLD §2 |
| C3 | Current collection flow | current state / runtime | HLD §2 |
| T1 | Target system context (MVP) | target design | HLD §3 |
| T2 | Components and dependency direction | target design | HLD §3 |
| T3 | Discovery → topology → metrics sequence | runtime | HLD §3 |
| T4 | Data flow | runtime | HLD §3 |
| D1 | Public-cloud deployment | deployment | HLD §4 |
| D2 | Private-cloud deployment | deployment | HLD §4 |
| S1 | Trust boundaries | security | HLD §5 |
| J1 | Discovery job state machine | design / runtime | LLD §3 |
| E1 | Entity relationships | design | LLD §4 |
| P1 | CI/CD and SDLC flow | SDLC | operations/sdlc-and-cicd.md |
| P2 | Migration phases and gates | planning | migration/plan-and-traceability.md |
| X1 | Where an integration plugs in | target design | guides/extending.md |
| R1 | Roadmap phases and gates | planning | roadmap.md |

Current deployment topology is described but not redrawn (HLD §2), because node layout and sizes are not in the repository.
