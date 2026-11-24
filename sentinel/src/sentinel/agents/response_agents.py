"""Remediation Recommendation and Incident Summary & Reporting agents."""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Any

from sentinel.agents.analysis_agents import _usage
from sentinel.agents.base import AgentDeps, InvestigationState
from sentinel.domain.models import Recommendation, Risk, SignalKind
from sentinel.llm import prompts
from sentinel.llm.providers import LLMRequest

MAX_RECOMMENDATIONS = 3


def remediation_agent(state: InvestigationState, deps: AgentDeps) -> dict[str, Any]:
    """Retrieve runbooks (RAG) for the top hypothesis and propose allow-listed actions.

    The agent can only propose actions that exist in the action catalog AND are declared for
    the hypothesised signal. Whether an action needs approval comes from the catalog, never
    from the model; low-risk auto-approval is an explicit operator opt-in.
    """
    hypotheses = state.get("hypotheses", [])
    if not hypotheses:
        return {"recommendations": [], "_summary": "no hypothesis, nothing to recommend"}
    top = hypotheses[0]
    try:
        kind = SignalKind(top.cause)
    except ValueError:
        return {"recommendations": [], "_summary": f"no runbook for cause {top.cause}"}

    query = f"{kind.value} {top.service} " + " ".join(top.evidence)
    hits = deps.retriever.search(query, k=3)
    recs: list[Recommendation] = []
    seen: set[str] = set()
    for hit in hits:
        if kind.value not in hit.runbook.signals:
            continue
        for action_name in hit.runbook.actions:
            spec = deps.actions.get(action_name)
            if not spec or action_name in seen or kind not in spec.signals:
                continue
            seen.add(action_name)
            needs_approval = spec.requires_approval and not (
                deps.settings.auto_approve_low_risk and spec.risk is Risk.LOW
            )
            recs.append(
                Recommendation(
                    action=spec.name,
                    service=top.service,
                    risk=spec.risk,
                    executor=spec.executor,
                    rationale=(
                        f"Runbook '{hit.runbook.title}' lists {spec.name} for {kind.value} "
                        f"incidents. {spec.description}"
                    ),
                    runbook=hit.runbook.id,
                    requires_approval=needs_approval,
                )
            )
    # Prefer automated, lower-risk mitigations first: fastest safe path to recovery.
    risk_order = {Risk.LOW: 0, Risk.MEDIUM: 1, Risk.HIGH: 2}
    recs.sort(key=lambda r: (risk_order[r.risk], r.executor != "http"))
    recs = recs[:MAX_RECOMMENDATIONS]
    return {
        "recommendations": recs,
        "_summary": f"{len(recs)} recommendation(s): {', '.join(r.action for r in recs)}"
        if recs
        else "no applicable actions",
    }


def _cell(text: str) -> str:
    """Make untrusted text (discovered resource names) safe inside a markdown table cell."""
    return text.replace("|", "\\|").replace("\n", " ")


def _fmt_ts(ts: float | None) -> str:
    if ts is None:
        return "-"
    return datetime.fromtimestamp(ts, UTC).strftime("%H:%M:%S")


def reporting_agent(state: InvestigationState, deps: AgentDeps) -> dict[str, Any]:
    """Build the incident report (markdown) and an executive summary."""
    inc = state["incident"]
    hyps = state.get("hypotheses", [])
    recs = state.get("recommendations", [])
    top = hyps[0] if hyps else None
    ctx = {
        "severity": inc.get("severity"),
        "services": inc.get("services", []),
        "alert_count": len(inc.get("alerts", [])),
        "root_cause": top.model_dump() if top else None,
        "recommended_actions": [f"{r.action} on {r.service}" for r in recs],
        "data_gaps": state.get("data_gaps", []),
    }
    resp = deps.llm.generate(
        LLMRequest(
            task="incident_summary",
            system=prompts.SUMMARY_SYSTEM,
            prompt=prompts.render("incident_summary", ctx),
            context=ctx,
            max_tokens=300,
        )
    )
    summary = resp.text

    lines = [
        f"# Incident #{inc.get('id')}: {inc.get('title')}",
        "",
        f"**Severity:** {inc.get('severity')}  ",
        f"**Status:** {inc.get('status')}  ",
        f"**Services:** {', '.join(inc.get('services', []))}  ",
        f"**Started:** {inc.get('started_at')}",
        "",
        "## Executive summary",
        summary,
        "",
        "## Alerts",
        "| Alert | Service | Severity | Status | Started |",
        "|---|---|---|---|---|",
    ]
    for a in inc.get("alerts", []):
        lines.append(
            f"| {a['alertname']} | {a['service']} | {a['severity']} | {a['status']} | "
            f"{a['starts_at']} |"
        )
    lines += [
        "",
        "## Anomalies",
        "| Service | Signal | Observed vs baseline | Onset | Method |",
        "|---|---|---|---|---|",
    ]
    for an in state.get("anomalies", []):
        lines.append(
            f"| {an.service} | {an.kind.value} | {an.description} | {_fmt_ts(an.onset_ts)} | "
            f"{an.method} |"
        )
    if not state.get("anomalies"):
        lines.append("| - | - | none detected | - | - |")

    corr = state.get("correlation") or {}
    if corr.get("propagation"):
        lines += [
            "",
            "## Propagation",
            "Origin first: " + " <- ".join(corr["propagation"]),
            f"Blast radius: {', '.join(corr.get('blast_radius', []))}",
        ]

    infra = state.get("infrastructure") or []
    if infra:
        lines += [
            "",
            "## Infrastructure context",
            "From the discovery service. Context only; it does not change the ranking.",
            "",
            "| Service | Resources | Issues |",
            "|---|---|---|",
        ]
        for ev in infra:
            kinds: dict[str, int] = {}
            for res in [*ev.matched, *ev.neighbours]:
                kinds[res.type] = kinds.get(res.type, 0) + 1
            summary = ", ".join(f"{n} {t}" for t, n in sorted(kinds.items()))
            if ev.truncated:
                summary += " (truncated)"
            issues = "; ".join(ev.issues) or "none"
            lines.append(f"| {ev.service} | {_cell(summary)} | {_cell(issues)} |")

    lines += ["", "## Root cause analysis"]
    if hyps:
        lines += ["| Rank | Service | Cause | Confidence | Score |", "|---|---|---|---|---|"]
        for i, h in enumerate(hyps[:5], 1):
            lines.append(f"| {i} | {h.service} | {h.cause} | {h.confidence:.0%} | {h.score:.2f} |")
        if top is not None:
            lines += ["", "**Evidence for top hypothesis:**", *[f"- {e}" for e in top.evidence]]
    narrative = state.get("narrative")
    if narrative:
        lines += ["", narrative]

    lines += ["", "## Recommended remediation (human approval required)"]
    if recs:
        for r in recs:
            gate = "approval required" if r.requires_approval else "auto-approved (low risk)"
            lines.append(
                f"- **{r.action}** on `{r.service}` - risk {r.risk.value}, "
                f"{r.executor} executor, {gate}. {r.rationale}"
            )
    else:
        lines.append("- No automated recommendation; follow the on-call runbook.")

    gaps = state.get("data_gaps", [])
    if gaps:
        lines += ["", "## Data gaps", *[f"- {g}" for g in gaps]]
    lines += ["", "## Agent execution", "| Agent | Duration | Result |", "|---|---|---|"]
    for st in state.get("steps", []):
        lines.append(f"| {st.agent} | {st.duration_ms:.0f} ms | {st.summary} |")
    return {
        "summary": summary,
        "report": "\n".join(lines) + "\n",
        "llm_usage": [_usage("incident_summary", resp)],
        "_summary": "report generated",
    }
