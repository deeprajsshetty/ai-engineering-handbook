# ADR-0007: Deterministic, weighted root-cause ranking

**Status:** Accepted · **Date:** 2026-11-10

## Context
Operators must trust and verify an RCA quickly. An LLM asked "what is the root cause?" over raw
telemetry is hard to test, may be confidently wrong, and costs tokens per incident.

## Decision
Rank candidate services with explicit weights: anomaly severity 0.30, topology (anomalous with
healthy dependencies) 0.25, trace evidence (error-origin spans or self-time) 0.20, earliness 0.15,
error-log share 0.10. Confidence blends the top score with its margin over the runner-up. The LLM
explains the result.

## Alternatives
LLM-only RCA; learned models (need labelled incident history the customer does not have yet).

## Consequences
Reproducible results, unit-testable scenarios, visible evidence per hypothesis. Weights are a
tuning surface: post-incident reviews feed corrected root causes back as test scenarios (see
[AIDLC.md](../../AIDLC.md)), and a learned ranker can replace the weights later behind the same
function signature.
