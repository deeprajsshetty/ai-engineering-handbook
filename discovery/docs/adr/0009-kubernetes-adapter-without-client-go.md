# ADR 0009 — Kubernetes adapter on net/http instead of client-go

**Status:** Accepted.

**Context.** The legacy Kubernetes collector uses client-go (Verified in its `go.mod`). The MVP needs six list calls plus `/version` and metrics.k8s.io, read-only, through an SSRF-safe transport.

**Decision.** `internal/kubeclient`: GET-only, bearer token, limit/continue pagination, retry on 429/5xx, 32 MiB response cap, all requests through `netguard`.

**Alternatives rejected.** client-go (dozens of transitive modules, kubeconfig/exec-plugin credential paths that bypass the egress policy, much larger review surface).

**Consequences.** Watch/informers and exec-based auth are out of scope; adding them is a reason to revisit. Contract tests pin the response shapes the adapter relies on. Not validated against a live cluster in this workspace.
