# ADR 0005 — Database-backed job queue with leases

**Status:** Accepted.

**Context.** The legacy scheduler holds jobs in memory and JSON files, which prevents more than one replica (PDF §3.6, Verified in `job-scheduler/scheduler/job/persist.go`). Discovery jobs are few (one per connection run) and long (seconds to minutes).

**Decision.** Jobs live in `discovery_jobs`. Workers claim with `FOR UPDATE SKIP LOCKED`, hold a lease renewed by heartbeat, observe cancellation through the heartbeat, and expired leases are requeued up to a maximum attempt count. Idempotency keys and a partial unique index enforce API-level idempotency and one active job per connection.

**Alternatives rejected.** Redis Streams (another stateful dependency for a low-rate queue; no transactional coupling to job state); in-process channel (lost on restart, single replica); Kubernetes Jobs per discovery (heavy, needs cluster RBAC from the app).

**Consequences.** Any number of worker replicas; job state and topology writes share one database; polling interval (1 s) adds small pickup latency, reduced by in-process notification on the replica that accepted the request.
