"""Infrastructure Context Agent.

Responsibility: for every service in scope, ask the discovery service (``discovery/``) which
infrastructure resources it runs on or depends on (pods, nodes, databases), and flag any that are
missing or stale. Deterministic and read-only.

The findings are *context*: they appear in the report and in the RCA narrative's evidence, but
they do not change the root-cause ranking. Weights change only through the evaluation set
(docs/guide/agentic-engineering-guide.md §8.2).
"""

from __future__ import annotations

from typing import Any

from sentinel.agents.base import AgentDeps, InvestigationState
from sentinel.domain.models import InfraEvidence
from sentinel.infrastructure.discovery import DiscoveryError, DiscoveryUnavailable

MAX_SERVICES = 15


def run(state: InvestigationState, deps: AgentDeps) -> dict[str, Any]:
    if not deps.discovery.enabled:
        return {"infrastructure": [], "_summary": "discovery not configured; skipped"}
    gaps: list[str] = []
    found: list[InfraEvidence] = []
    for svc in state["scope"][:MAX_SERVICES]:
        try:
            ev = deps.discovery.infra_for(svc)
        except DiscoveryUnavailable as exc:
            gaps.append(f"infrastructure context unavailable: {exc}")
            break
        except DiscoveryError as exc:
            gaps.append(f"infrastructure lookup failed for {svc}: {exc}")
            continue
        if ev.matched:
            found.append(ev)
    issues = [i for ev in found for i in ev.issues]
    unmatched = len(state["scope"][:MAX_SERVICES]) - len(found)
    summary = f"{len(found)} service(s) mapped to infrastructure"
    if unmatched and not gaps:
        summary += f", {unmatched} without a discovered resource"
    summary += f"; {len(issues)} issue(s)" if issues else "; no infrastructure issues"
    return {"infrastructure": found, "data_gaps": gaps, "_summary": summary}
