# Documentation index

| Area | Documents |
|---|---|
| **Start here** | [../README.md](../README.md) · [../PROJECT_KNOWLEDGE.md](../PROJECT_KNOWLEDGE.md) · [guide/unified-platform-guide.md](guide/unified-platform-guide.md) (step by step) |
| **Learn** | [../handbook/README.md](../handbook/README.md) (modules 000-012) · [guide/agentic-engineering-guide.md](guide/agentic-engineering-guide.md) (beginner-to-expert course, target architecture, roadmap) |
| **Discovery service** | [../discovery/README.md](../discovery/README.md) · [../discovery/docs/](../discovery/docs/README.md) |
| **Architecture** | [../ARCHITECTURE.md](../ARCHITECTURE.md) · [diagrams/](diagrams/README.md) (all 11 diagrams) · [architecture/data-model.md](architecture/data-model.md) · [adr/](adr/README.md) |
| **AI agents** | [agents/agent-specifications.md](agents/agent-specifications.md) · [../AIDLC.md](../AIDLC.md) |
| **Use cases** | [use-cases/](use-cases/README.md) (UC-01..06 + handbook) · [example report](use-cases/example-incident-report.md) |
| **FDE methodology** | [fde/README.md](fde/README.md) (7 phases, demo script) · [fde/customer-discovery.md](fde/customer-discovery.md) |
| **Build & run** | [../DEPLOYMENT_GUIDE.md](../DEPLOYMENT_GUIDE.md) · [../API_DOCUMENTATION.md](../API_DOCUMENTATION.md) · [../DEVELOPMENT_GUIDELINES.md](../DEVELOPMENT_GUIDELINES.md) |
| **Operate** | [runbooks/](runbooks/README.md) · [../TROUBLESHOOTING.md](../TROUBLESHOOTING.md) |
| **Quality & security** | [testing/test-strategy.md](testing/test-strategy.md) (with validation report) · [../SECURITY.md](../SECURITY.md) |
| **History** | [assessment/initial-assessment.md](assessment/initial-assessment.md) · [../CHANGELOG.md](../CHANGELOG.md) |

## Suggested reading paths

* **Business stakeholder (15 min):** README → [fde/customer-discovery.md](fde/customer-discovery.md) §6
  problem statement → [use-cases/README.md](use-cases/README.md) → the example report.
* **Solution architect (45 min):** ARCHITECTURE → diagrams → ADRs → agent specs → SECURITY.
* **Developer joining the team (1 h):** PROJECT_KNOWLEDGE → DEVELOPMENT_GUIDELINES → run
  `make up && make demo-errors` → read `sentinel/src/sentinel/agents/graph.py` and
  `application/alerts.py`.
* **FDE preparing a similar engagement:** fde/README → customer-discovery → AIDLC → test-strategy.
