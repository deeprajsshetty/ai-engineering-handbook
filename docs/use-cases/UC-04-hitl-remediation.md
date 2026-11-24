# UC-04: Human-approved remediation

| Field | |
|---|---|
| **ID / Name** | UC-04 · Remediation recommendation and human-in-the-loop execution |
| **Priority** | 4 (changes production, so it carries the highest risk) |

## Business view

**Problem.** Fixes are applied by hand under pressure. The right runbook is rarely found, and
there is weak evidence of who changed what.

**Objectives.**

- Recommend the safest effective action, taken from the customer's own runbooks.
- Execute only after a human approves.
- Audit every step.

**Stakeholders and actors.**

| Actor | Role in this use case |
|---|---|
| On-call engineer | Operator role: approves or rejects |
| Viewers | Can see recommendations, but cannot act on them |
| Security and compliance | Rely on the audit trail |
| Target service | Receives the action |

**Expected outcomes.**

- Faster mitigation of changes caused by flags or configuration.
- Zero unapproved production changes.
- Audit-ready evidence.

## Functional requirements

1. The Remediation Recommendation agent retrieves runbooks (BM25) for the top hypothesis. It
   proposes at most 3 actions that meet all of these conditions:
   - a runbook covering that signal lists the action;
   - the action exists in `actions.yaml`;
   - `actions.yaml` declares the action for that signal.
2. Proposals are persisted as `pending_approval`. Low-risk actions are auto-approved only when
   `SENTINEL_AUTO_APPROVE_LOW_RISK=true` is set.
3. Operators and admins can approve or reject a proposal, with a comment. A proposal is decided
   only once; a second decision returns 409.
4. Approved `http` actions are executed by the worker against the allow-listed URL template, only
   for services in the catalog.
5. Approved `manual` actions are performed by an operator, who then records the outcome with
   `POST /complete`.
6. Transient execution failures (network errors) are retried, up to 3 attempts. HTTP error
   responses are marked `failed` with the response attached.
7. When an incident resolves, its proposals that are still `pending_approval` expire, so they can
   never execute against a finished incident.

## Non-functional requirements

| Requirement | Target |
|---|---|
| Time from approval to execution | Under 5 s (worker poll interval 2 s; measured about 2 s) |
| Unapproved changes | 0 (every `remediation.executed` audit event is preceded by `remediation.approved`, or by the explicit low-risk policy) |

## Prerequisites and dependencies

- UC-03 hypotheses.
- Runbooks with front-matter listing their signals and actions.
- An action catalog.
- `SENTINEL_ACTION_TOKEN`, shared with the targets' admin API.

## Inputs and outputs

| | |
|---|---|
| **Inputs** | The top hypothesis (service and cause), the runbooks, the action catalog, the operator's decision |
| **Outputs** | `remediations` rows with `status`, `decided_by`, `decision_comment` and `result` (HTTP status and response), plus audit events |

## End-to-end workflow

See [sequence 7b](../diagrams/README.md#7b-human-approved-remediation-uc-04) and the
[state machine](../diagrams/README.md#5-ai-agent-orchestration-and-execution-flow).

1. The agent proposes, for example, `disable_fault_flag` on `payments` (low risk, `http` executor).
2. An operator reviews the evidence in the console and clicks **Approve**. The proposal moves to
   `approved` and the incident moves to `mitigating`.
3. The worker claims the proposal and moves it to `executing`. It then checks that the action is
   still in the catalog and that the service is in the catalog.
4. The worker sends `POST http://payments:8080/admin/faults/clear` with `X-Admin-Token`.
5. On a 2xx response the proposal moves to `succeeded`, and an audit event is written.
6. The error rate recovers, the alerts resolve, the incident auto-resolves, and any remaining
   pending proposals expire.

## AI agent responsibilities

The Remediation Recommendation agent proposes actions only. It cannot execute anything, cannot
invent actions, and cannot change approval requirements.

## Exception and failure handling

| Case | Result |
|---|---|
| A viewer tries to approve | 403 |
| The proposal was already decided | 409 |
| The action was removed from the catalog after approval | `failed` with the message "no longer in catalog" |
| The service is not in the catalog (for example, a crafted name) | `failed` with the message "not in catalog" (SSRF guard) |
| The target returns a 5xx | `failed`, with the status code recorded |
| A network error | Retried up to 3 times, then `failed` |

## Security considerations

- Role-based access control: approving requires the operator role or higher.
- Approval endpoints are deliberately absent from MCP tools, so external AI agents cannot approve.
- URL templates are fixed in configuration, and the service name is validated against both a regex
  and the catalog.
- The admin token is sent only to catalog targets.
- The audit log is append-only.

## Observability requirements

- `sentinel_remediations_total{action,status}`.
- Audit events: `remediation.proposed`, `remediation.approved`, `remediation.rejected`,
  `remediation.executed`, `remediation.expired`.
- The approval and rejection ratio is a measure of trust (see [AIDLC.md](../../AIDLC.md#6-operate-and-monitor)).

## Acceptance criteria (verified)

- [x] Viewer approval returns 403. Operator approval succeeds and records `decided_by`.
- [x] The worker calls exactly `http://payments:8080/admin/faults/clear`, and the proposal ends in
  `succeeded` (integration test and live e2e).
- [x] A target 500 results in `failed` with `status_code` 500.
- [x] The manual flow (approve, then complete) works. Rejecting and then completing returns 409.
- [x] Unapproved proposals expire when the incident resolves.
- [x] With the auto-approve opt-in, low-risk proposals are created already `approved`.

## Testing scenarios

`tests/integration/test_api_flow.py` (`test_end_to_end_incident_lifecycle`,
`test_generic_alert_and_manual_flows`, `test_failed_execution_is_recorded`) and
`tests/unit/test_infrastructure.py::TestActionExecutor`.
