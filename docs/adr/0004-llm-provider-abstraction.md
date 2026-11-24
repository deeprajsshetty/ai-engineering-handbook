# ADR-0004: Provider-neutral LLM adapters with deterministic fallback

**Status:** Accepted · **Date:** 2026-11-10

## Context
Customers differ in which LLM vendors they may use (data residency, contracts), some
environments have no egress, and LLM outages must not stop incident response.

## Decision
A small `LLMProvider` protocol with three implementations: `deterministic` (templates over the
structured evidence, default), `openai` (chat completions) and `anthropic` (messages), called over
plain HTTP with timeouts and retries. `FallbackProvider` wraps the remote provider and falls back to
templates on any error, recording `sentinel_llm_requests_total{outcome="fallback"}`. The LLM only
writes narrative text; it cannot change rankings or select actions.

## Alternatives
Vendor SDKs or LangChain chat models: more features, more dependencies and version churn; we
use two endpoints.

## Consequences
Works offline and in CI with no keys. Switching vendor is an env var. Prompts are versioned
(`PROMPT_VERSION`) and the version is stored with every investigation for evaluation.
