# Architecture diagrams

All diagrams are [Mermaid](https://mermaid.js.org/) and render natively on GitHub. Edit the
text, not exported images. Each diagram reflects code that exists in this repository; the
"Source of truth" line names the files to check when updating it.

| # | Diagram | Audience |
|---|---|---|
| 1 | [High-level architecture](#1-high-level-architecture) | Everyone |
| 2 | [Component architecture](#2-component-architecture) | Architects, developers |
| 3 | [Service interaction](#3-application-and-service-interaction) | Developers, SRE |
| 4 | [End-to-end data flow](#4-end-to-end-data-flow) | Architects, data/platform engineers |
| 5 | [Agent orchestration](#5-ai-agent-orchestration-and-execution-flow) | AI engineers |
| 6 | [AIDLC workflow](#6-aidlc-workflow) | Delivery leads, FDEs |
| 7 | [Sequence diagrams](#7-sequence-diagrams-for-critical-use-cases) | Developers |
| 8 | [Deployment architecture](#8-deployment-architecture) | Platform/SRE |
| 9 | [CI/CD pipeline](#9-cicd-pipeline) | Developers, release managers |
| 10 | [Observability architecture](#10-observability-and-monitoring-architecture) | SRE |
| 11 | [Security & authentication flow](#11-security-and-authentication-flow) | Security reviewers |

---

## 1. High-level architecture

Source of truth: `deploy/compose/docker-compose.yml`.

```mermaid
flowchart LR
  subgraph Workload["Customer workload (instrumented with OpenTelemetry)"]
    FE[frontend] --> CO[checkout]
    CO --> PA[payments]
    CO --> IN[inventory]
    FE --> IN
  end

  subgraph Telemetry["Telemetry platform"]
    OTC[OpenTelemetry Collector]
    PROM[(Prometheus<br/>metrics + alert rules)]
    LOKI[(Loki<br/>logs)]
    TEMPO[(Tempo<br/>traces)]
    AM[Alertmanager]
    GRAF[Grafana<br/>dashboards]
  end

  subgraph Sentinel["Sentinel AIOps platform"]
    API[Sentinel API<br/>FastAPI + MCP]
    WK[Sentinel Worker<br/>LangGraph agents]
    PG[(PostgreSQL<br/>incidents, audit)]
    UI[Operator console]
  end

  LLM[[LLM provider<br/>optional: OpenAI / Anthropic]]
  OPS((On-call engineer))
  EXT((External AI agent<br/>via MCP))

  Workload -- OTLP --> OTC
  OTC --> PROM & LOKI & TEMPO
  PROM -- firing alerts --> AM -- webhook --> API
  API <--> PG
  WK <--> PG
  WK -- PromQL / LogQL / TraceQL --> PROM & LOKI & TEMPO
  WK -.-> LLM
  WK -- approved remediation --> PA
  OPS --> UI --> API
  OPS --> GRAF
  GRAF --> PROM & LOKI & TEMPO
  EXT -- read-only tools --> API
```

## 2. Component architecture

Source of truth: `sentinel/src/sentinel/` package layout.

```mermaid
flowchart TB
  subgraph api["api/ (interface layer)"]
    R1[routers: alerts, incidents,<br/>remediations, services, health]
    SEC[security: API keys, roles,<br/>webhook token]
    MW[middleware: request id, rate limit,<br/>body limit, security headers, metrics]
    MCP[MCP server<br/>read-only tools]
    STATIC[static console]
  end
  subgraph app["application/ (use cases)"]
    ING[AlertIngestionService]
    INV[InvestigationService<br/>queue + leases]
    REM[RemediationService<br/>HITL state machine]
    TOP[TopologyService]
    AUD[audit]
    C[Container<br/>composition root]
  end
  subgraph agents["agents/ (LangGraph)"]
    G[graph.py] --> T[telemetry_agent]
    G --> AN[anomaly_agent]
    G --> CR[correlation_agent]
    G --> RC[rca_agent]
    G --> RM[remediation_agent]
    G --> RP[reporting_agent]
  end
  subgraph domain["domain/ (pure logic, no I/O)"]
    D1[anomaly: robust z / MAD]
    D2[correlation: incident grouping]
    D3[rca: weighted ranking]
    D4[topology: dependency graph]
    D5[traces: error origin, self time]
  end
  subgraph infra["infrastructure/ (adapters)"]
    DB[db + orm<br/>SQLAlchemy]
    TEL[telemetry clients<br/>Prometheus / Loki / Tempo]
    ACT[action executor]
    NOT[notifier]
    CAT[YAML catalogs]
  end
  subgraph ai["llm/ + knowledge/"]
    LLMP[providers: deterministic,<br/>OpenAI, Anthropic, fallback]
    PR[versioned prompts]
    KB[runbook retriever BM25]
  end
  api --> app
  app --> agents
  app --> domain
  agents --> domain
  agents --> ai
  agents --> TEL
  app --> infra
```

Dependency rule: arrows point inward. `domain/` imports nothing from other layers, so it is
unit-tested without mocks.

## 3. Application and service interaction

```mermaid
flowchart LR
  AM[Alertmanager] -- "POST /api/v1/alerts/alertmanager<br/>Bearer webhook token" --> API
  OPS[Operator console] -- "X-API-Key (viewer/operator/admin)" --> API
  MCPC[MCP client] -- "/mcp + API key" --> API
  API[(sentinel-api)] -- "INSERT investigation (queued)" --> PG[(PostgreSQL)]
  WK[sentinel-worker] -- "SELECT ... FOR UPDATE SKIP LOCKED" --> PG
  WK -- "query_range / instant" --> PROM[Prometheus]
  WK -- "query_range (LogQL)" --> LOKI[Loki]
  WK -- "search + get trace (TraceQL)" --> TEMPO[Tempo]
  WK -- "chat/messages (optional)" --> LLM[LLM API]
  WK -- "POST /admin/faults/clear<br/>X-Admin-Token" --> SVC[target service]
  WK -- "incident notifications" --> HOOK[Slack-compatible webhook]
  PROM -- "scrape /metrics" --> API & WK
```

## 4. End-to-end data flow

```mermaid
flowchart LR
  A[App code] -->|spans, metrics, logs| SDK[OTel SDK]
  SDK -->|OTLP/HTTP :4318| OTC[Collector]
  OTC -->|batch, memory_limiter| P1{pipelines}
  P1 -->|traces| TEMPO[(Tempo)]
  P1 -->|traces -> servicegraph + spanmetrics connectors| M[derived metrics]
  P1 -->|metrics| PROMEXP[Prometheus exporter :8889]
  M --> PROMEXP
  HM[hostmetrics + postgresql receivers] --> PROMEXP
  P1 -->|logs OTLP| LOKI[(Loki)]
  PROMEXP -->|scrape 15s| PROM[(Prometheus)]
  PROM -->|recording rules| RR[shop:error_ratio:1m, ...]
  RR -->|alert rules| AM[Alertmanager]
  AM -->|webhook JSON| ING[Sentinel ingestion]
  ING -->|dedup by fingerprint<br/>correlate by time + topology| INC[(incidents, alerts)]
  INC -->|enqueue| Q[(investigations queue)]
  Q --> AG[Agents]
  PROM & LOKI & TEMPO -->|evidence| AG
  AG -->|hypotheses, report, proposals| RES[(investigation result JSON,<br/>remediations)]
  RES -->|approve| EXEC[Executor] -->|HTTP| A
```

## 5. AI agent orchestration and execution flow

Source of truth: `sentinel/src/sentinel/agents/graph.py`.

```mermaid
stateDiagram-v2
  [*] --> telemetry_analysis
  telemetry_analysis --> anomaly_detection: evidence_ok
  telemetry_analysis --> reporting: no evidence (all backends down / silent)
  anomaly_detection --> incident_correlation
  incident_correlation --> root_cause_analysis
  root_cause_analysis --> remediation_recommendation
  remediation_recommendation --> reporting
  reporting --> [*]
  note right of root_cause_analysis
    Deterministic ranking, then LLM writes
    the narrative (falls back to templates)
  end note
  note right of remediation_recommendation
    RAG over runbooks; only allow-listed
    actions; approval flag from catalog
  end note
```

After the graph: proposals are persisted as `pending_approval` and wait for a human.

```mermaid
stateDiagram-v2
  [*] --> pending_approval: agent proposes
  [*] --> approved: low risk + auto_approve opt-in
  pending_approval --> approved: operator approves
  pending_approval --> rejected: operator rejects
  approved --> executing: worker claims (http executor)
  approved --> succeeded: operator completes (manual executor)
  approved --> failed: operator completes (manual executor)
  executing --> succeeded
  executing --> failed
  executing --> approved: transient error, retry (max 3)
```

## 6. AIDLC workflow

```mermaid
flowchart LR
  R[1. Requirements<br/>discovery, success metrics] --> D[2. Design<br/>ADRs, agent specs, data model]
  D --> I[3. Implementation<br/>deterministic core first,<br/>LLM narrative second]
  I --> E[4. Evaluation & testing<br/>unit, scenario evals,<br/>integration, e2e, load]
  E --> DP[5. Deployment<br/>CI gates, signed images,<br/>migrations, rollout]
  DP --> O[6. Operate & monitor<br/>platform SLOs, LLM cost,<br/>RCA confidence]
  O --> F[7. Feedback & improvement<br/>operator decisions,<br/>post-incident reviews]
  F -->|new scenarios, tuned weights, runbooks| R
  E -->|fails gate| I
```

## 7. Sequence diagrams for critical use cases

### 7a. Alert to identified root cause (UC-02, UC-03)

```mermaid
sequenceDiagram
  autonumber
  participant P as Prometheus
  participant AM as Alertmanager
  participant API as Sentinel API
  participant DB as PostgreSQL
  participant W as Worker
  participant T as Prometheus/Loki/Tempo
  participant L as LLM (optional)
  P->>AM: HighErrorRate{service=checkout} firing
  AM->>API: POST /api/v1/alerts/alertmanager (Bearer token)
  API->>DB: advisory lock; upsert alert by fingerprint
  API->>DB: correlate (window + topology) -> open/join incident
  API->>DB: enqueue investigation (if none queued/running)
  API-->>AM: 200 {incidents_opened:[42]}
  W->>DB: claim job (FOR UPDATE SKIP LOCKED, lease)
  W->>T: golden signals, error logs, error/slow traces
  W->>W: anomaly -> correlation -> RCA ranking
  W->>L: narrative + summary (evidence as delimited data)
  L-->>W: text (or fallback to templates)
  W->>DB: result, root cause, remediation proposals, audit
```

### 7b. Human-approved remediation (UC-04)

```mermaid
sequenceDiagram
  autonumber
  actor O as Operator
  participant API as Sentinel API
  participant DB as PostgreSQL
  participant W as Worker
  participant S as payments service
  O->>API: POST /remediations/7/approve (X-API-Key, role >= operator)
  API->>DB: pending_approval -> approved; incident -> mitigating; audit(actor)
  W->>DB: claim approved http remediation -> executing
  W->>W: validate action in catalog, service in catalog
  W->>S: POST /admin/faults/clear (X-Admin-Token)
  S-->>W: 200 {cleared:true}
  W->>DB: executing -> succeeded; audit
  Note over S,API: error rate drops -> alerts resolve -> incident auto-resolves
```

## 8. Deployment architecture

Source of truth: `deploy/k8s/`.

```mermaid
flowchart TB
  subgraph Internet
    U((Operators))
  end
  subgraph K8s["Kubernetes cluster"]
    ING[Ingress nginx + cert-manager TLS]
    subgraph ns1["namespace: sentinel (PSA restricted, default-deny NetworkPolicy)"]
      APIP[sentinel-api Deployment<br/>2-6 replicas, HPA, PDB]
      WKP[sentinel-worker Deployment<br/>2 replicas, PDB]
      JOB[sentinel-migrate Job<br/>PreSync hook]
      ES[ExternalSecret -> Secret]
    end
    subgraph ns2["namespace: monitoring"]
      OTCD[OTel Collector<br/>DaemonSet + gateway]
      KPS[kube-prometheus-stack<br/>Prometheus, Alertmanager, Grafana]
      LK[Loki]
      TP[Tempo]
    end
    subgraph ns3["namespace: shop (customer workloads)"]
      APPS[instrumented services]
    end
  end
  RDS[(Managed PostgreSQL<br/>PITR backups, Multi-AZ)]
  SM[(Cloud secret manager)]
  OBJ[(Object storage<br/>Loki/Tempo blocks)]
  U --> ING --> APIP
  APIP & WKP & JOB --> RDS
  ES --> SM
  APPS --> OTCD --> KPS & LK & TP
  LK & TP --> OBJ
  KPS -- webhook --> APIP
  WKP --> KPS & LK & TP
```

## 9. CI/CD pipeline

Source of truth: `.github/workflows/ci.yml`, `.github/workflows/release.yml`.

```mermaid
flowchart LR
  PR[Push / PR] --> Q[quality<br/>ruff, format, mypy, lock check]
  PR --> S[security<br/>bandit, pip-audit, gitleaks]
  PR --> DC[deploy-config<br/>compose, kubeconform,<br/>promtool, amtool, dashboards]
  Q --> T[test<br/>SQLite + PostgreSQL,<br/>coverage >= 85%, migrations]
  T --> IMG[images<br/>build + Trivy scan]
  IMG --> E2E[e2e<br/>compose up, fault injection,<br/>approve, recover]
  DC --> E2E
  TAG[tag vX.Y.Z] --> REL[release<br/>multi-arch build, SBOM,<br/>provenance, cosign sign, GHCR]
  REL --> CD[GitOps / Argo CD<br/>PreSync migrate -> rollout]
```

## 10. Observability and monitoring architecture

```mermaid
flowchart TB
  subgraph Signals
    MET[Metrics<br/>shop_server_* golden signals,<br/>host + postgres, sentinel_*]
    LOG[Logs<br/>OTLP, severity + trace_id]
    TRC[Traces<br/>W3C tracecontext]
    DER[Derived<br/>service graph, span RED metrics]
  end
  MET & DER --> PROM[(Prometheus)]
  LOG --> LOKI[(Loki)]
  TRC --> TEMPO[(Tempo)]
  PROM --> RULES[recording + alert rules<br/>shop-slo.yml, platform.yml]
  RULES --> AM[Alertmanager] --> SEN[Sentinel]
  PROM & LOKI & TEMPO --> GRAF[Grafana: Shop golden signals,<br/>Sentinel platform, Infrastructure]
  GRAF -. trace_id link .-> TEMPO
  TEMPO -. traces to logs .-> LOKI
  SEN -->|own metrics, traces, logs| PROM & TEMPO & LOKI
  RULES -->|SentinelApiDown, InvestigationsFailing,<br/>QueueBacklog, TelemetryPipelineDown| AM
```

Sentinel is itself observable: if Sentinel or the pipeline breaks, Prometheus alerts on it
(the `platform` rule group) independently of Sentinel.

## 11. Security and authentication flow

```mermaid
flowchart TB
  REQ[HTTP request] --> MW{EdgeMiddleware}
  MW -->|body > max_request_bytes| E413[413]
  MW -->|bucket empty| E429[429 + Retry-After]
  MW --> ROUTE{route}
  ROUTE -->|/health, /metrics| OPEN[unauthenticated<br/>network-restricted]
  ROUTE -->|/api/v1/alerts/alertmanager| WH{Bearer == webhook token?<br/>constant-time}
  WH -->|no| OPK{API key role >= operator?}
  OPK -->|no| E401[401]
  WH -->|yes| OK1[ingest as 'alertmanager']
  OPK -->|yes| OK2[ingest as key name]
  ROUTE -->|/api/v1/*, /mcp| KEY{X-API-Key or Bearer<br/>sha256 match, constant-time}
  KEY -->|no match| E401
  KEY --> ROLE{role >= required?<br/>viewer < operator < admin}
  ROLE -->|no| E403[403]
  ROLE -->|yes| H[handler -> audit log with actor]
  H -->|approve| HITL[only operator/admin;<br/>never exposed via MCP]
```
