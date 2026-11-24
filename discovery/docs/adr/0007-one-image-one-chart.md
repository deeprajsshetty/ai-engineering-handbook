# ADR 0007 — One OCI image and one Helm chart for public and private cloud

**Status:** Accepted.

**Context.** The brief requires public- and private-cloud Kubernetes with the same artifacts. Legacy packaging is Helm per service (Verified). Gateway API is the PDF's edge choice (T1) with Ingress as fallback.

**Decision.** One distroless, non-root, static image containing the binary and the built UI; one Helm chart; environment differences only in values overlays (`values-public-cloud.yaml`, `values-private-cloud.yaml`). Config via env, secrets via Kubernetes Secrets synced by an operator of the customer's choice. Migrations run as a pre-install/upgrade hook Job.

**Alternatives rejected.** Kustomize (no existing convention in the repository; Helm is used throughout legacy charts); separate images per environment (breaks promote-the-same-artifact); embedding cloud SDK credentials discovery (vendor lock-in).

**Consequences.** Vendor specifics (load balancer classes, secret back ends, managed database) live in values and cluster add-ons, not in code.
