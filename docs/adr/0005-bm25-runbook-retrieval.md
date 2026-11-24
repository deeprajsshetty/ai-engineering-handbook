# ADR-0005: BM25 retrieval over runbooks

**Status:** Accepted · **Date:** 2026-11-10

## Context
The Remediation agent needs the relevant runbook for a hypothesis such as
"error_rate on payments". The corpus is tens of curated runbooks that use the same vocabulary as
the signals and actions.

## Decision
In-process BM25 over runbook title, front-matter and body. Runbook front-matter declares which
`signals` it covers and which `actions` it recommends; the agent filters on both.

## Alternatives
Embeddings + vector DB (Pinecone/Chroma, as used in handbook modules 005/008): better for
paraphrased, large, unstructured corpora; adds an embedding model, a store and non-determinism.

## Consequences
Deterministic and explainable (the score is visible). Upgrade path: implement the same
`search(query, k)` with a vector store or hybrid BM25 + embeddings when the corpus grows past a
few hundred documents or includes free-text post-mortems.
