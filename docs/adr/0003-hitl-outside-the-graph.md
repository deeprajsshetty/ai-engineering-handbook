# ADR-0003: Human approval as persisted state, outside the agent graph

**Status:** Accepted · **Date:** 2026-11-10

## Context
Remediation changes production. Approval may take minutes or hours, may happen on another
replica, and must be attributable to a person.

## Decision
The graph ends after proposing remediations. Proposals are rows in `remediations` with a state
machine (`pending_approval -> approved/rejected -> executing -> succeeded/failed`). Only
authenticated `operator`/`admin` principals can transition them, every transition is audited,
and approval endpoints are excluded from the MCP tool list.

## Alternatives
LangGraph `interrupt()` with a checkpointer: elegant for short-lived chats, but ties approval to a
paused graph run and its checkpoint store, and makes audit/authorization a graph concern.

## Consequences
Approvals survive restarts and are trivially queryable ("what is waiting for me?"). The graph
stays a pure, re-runnable analysis. Auto-approval exists only as an explicit opt-in for `low`
risk actions (`SENTINEL_AUTO_APPROVE_LOW_RISK`).
