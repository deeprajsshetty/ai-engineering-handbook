"""Human-in-the-loop remediation endpoints (approve / reject / complete)."""

from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException, Query, Request
from sqlalchemy import select

from sentinel.api.schemas import CompletionIn, DecisionIn, RemediationOut
from sentinel.api.security import Principal, require
from sentinel.application.remediation import InvalidTransition, NotFound
from sentinel.domain.models import RemediationStatus, Role
from sentinel.infrastructure.orm import RemediationRow

router = APIRouter(prefix="/api/v1/remediations", tags=["Remediations"])


@router.get(
    "",
    response_model=list[RemediationOut],
    operation_id="list_remediations",
    summary="List remediations (e.g. ?status=pending_approval)",
)
def list_remediations(
    request: Request,
    status_: str | None = Query(None, alias="status"),
    limit: int = Query(100, ge=1, le=500),
    _: Principal = Depends(require(Role.VIEWER)),
) -> list[RemediationOut]:
    q = select(RemediationRow).order_by(RemediationRow.id.desc()).limit(limit)
    if status_:
        try:
            q = q.where(RemediationRow.status == RemediationStatus(status_).value)
        except ValueError as exc:
            raise HTTPException(422, f"unknown status {status_}") from exc
    with request.app.state.sentinel.c.db.session() as s:
        return [RemediationOut.model_validate(r) for r in s.scalars(q)]


def _decide(
    request: Request, rid: int, approve: bool, actor: str, comment: str | None
) -> RemediationOut:
    try:
        row = request.app.state.sentinel.remediations.decide(rid, approve, actor, comment)
    except NotFound as exc:
        raise HTTPException(404, str(exc)) from exc
    except InvalidTransition as exc:
        raise HTTPException(409, str(exc)) from exc
    return RemediationOut.model_validate(row)


@router.post(
    "/{remediation_id}/approve",
    response_model=RemediationOut,
    operation_id="approve_remediation",
    summary="Approve a proposed remediation",
)
def approve(
    request: Request,
    remediation_id: int,
    body: DecisionIn,
    principal: Principal = Depends(require(Role.OPERATOR)),
) -> RemediationOut:
    return _decide(request, remediation_id, True, principal.name, body.comment)


@router.post(
    "/{remediation_id}/reject",
    response_model=RemediationOut,
    operation_id="reject_remediation",
    summary="Reject a proposed remediation",
)
def reject(
    request: Request,
    remediation_id: int,
    body: DecisionIn,
    principal: Principal = Depends(require(Role.OPERATOR)),
) -> RemediationOut:
    return _decide(request, remediation_id, False, principal.name, body.comment)


@router.post(
    "/{remediation_id}/complete",
    response_model=RemediationOut,
    operation_id="complete_remediation",
    summary="Record the outcome of an approved manual remediation",
)
def complete(
    request: Request,
    remediation_id: int,
    body: CompletionIn,
    principal: Principal = Depends(require(Role.OPERATOR)),
) -> RemediationOut:
    try:
        row = request.app.state.sentinel.remediations.complete_manual(
            remediation_id, body.success, principal.name, body.notes
        )
    except NotFound as exc:
        raise HTTPException(404, str(exc)) from exc
    except InvalidTransition as exc:
        raise HTTPException(409, str(exc)) from exc
    return RemediationOut.model_validate(row)
