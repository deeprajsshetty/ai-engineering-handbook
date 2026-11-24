# ADR-0002: PostgreSQL as the work queue

**Status:** Accepted · **Date:** 2026-11-10

## Context
Investigations and remediations are background work. Volume is modest (an alert storm is
hundreds of alerts that collapse into a handful of incidents), but work must survive restarts,
must not run twice concurrently, and must be retried if a worker dies.

## Decision
Store jobs in the `investigations` / `remediations` tables. Workers claim with
`SELECT ... FOR UPDATE SKIP LOCKED LIMIT 1`, set a lease (`lease_expires_at`), and an expired
lease makes the job claimable again, up to `investigation_max_attempts`.

## Alternatives
* **Kafka**: excellent for high-volume streams; we would still need a database for state, and it
  adds brokers, topics, consumer-group operations.
* **RabbitMQ/SQS/Celery**: simpler than Kafka but still a second stateful system and a dual-write
  problem (DB commit + enqueue).

## Consequences
One stateful dependency. Enqueue is transactional with the incident update (no dual write). Throughput
is bounded by PostgreSQL (thousands of claims/second, far above need). Revisit if Sentinel ingests raw
telemetry events rather than alerts (then a stream processor belongs in front).
