"""Loaders for the YAML configuration catalogs (actions, services, signal queries)."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

from sentinel.domain.models import Risk, SignalKind

SERVICE_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9\-]{0,62}$")


@dataclass(frozen=True)
class ActionSpec:
    name: str
    description: str
    executor: str  # "http" | "manual"
    risk: Risk
    requires_approval: bool
    signals: tuple[SignalKind, ...]
    method: str = "POST"
    url: str | None = None


@dataclass(frozen=True)
class ActionCatalog:
    actions: dict[str, ActionSpec]

    def get(self, name: str) -> ActionSpec | None:
        return self.actions.get(name)

    def for_signal(self, kind: SignalKind) -> list[ActionSpec]:
        return [a for a in self.actions.values() if kind in a.signals]

    @classmethod
    def load(cls, path: Path) -> ActionCatalog:
        raw = yaml.safe_load(path.read_text()) or {}
        actions: dict[str, ActionSpec] = {}
        for name, spec in (raw.get("actions") or {}).items():
            executor = spec.get("executor", "manual")
            if executor not in {"http", "manual"}:
                raise ValueError(f"action {name}: unsupported executor {executor!r}")
            if executor == "http" and not spec.get("url"):
                raise ValueError(f"action {name}: http executor requires url")
            actions[name] = ActionSpec(
                name=name,
                description=" ".join(str(spec.get("description", "")).split()),
                executor=executor,
                risk=Risk(spec.get("risk", "medium")),
                requires_approval=bool(spec.get("requires_approval", True)),
                signals=tuple(SignalKind(s) for s in spec.get("signals", [])),
                method=str(spec.get("method", "POST")).upper(),
                url=spec.get("url"),
            )
        return cls(actions)


@dataclass(frozen=True)
class ServiceSpec:
    name: str
    team: str | None = None
    tier: str | None = None
    description: str | None = None
    dependencies: list[str] = field(default_factory=list)
    runbook_url: str | None = None


def load_services(path: Path) -> list[ServiceSpec]:
    raw = yaml.safe_load(path.read_text()) or {}
    out = []
    for s in raw.get("services", []):
        name = str(s["name"])
        if not SERVICE_NAME_RE.match(name):
            raise ValueError(f"invalid service name {name!r}")
        out.append(
            ServiceSpec(
                name=name,
                team=s.get("team"),
                tier=str(s["tier"]) if s.get("tier") is not None else None,
                description=s.get("description"),
                dependencies=[str(d) for d in s.get("dependencies", [])],
                runbook_url=s.get("runbook_url"),
            )
        )
    return out


@dataclass(frozen=True)
class SignalQueries:
    metrics: dict[SignalKind, str]
    log_errors: str
    trace_errors: str
    trace_slow: str
    service_graph: str

    @classmethod
    def load(cls, path: Path) -> SignalQueries:
        raw: dict[str, Any] = yaml.safe_load(path.read_text()) or {}
        return cls(
            metrics={SignalKind(k): " ".join(v.split()) for k, v in raw["metrics"].items()},
            log_errors=raw["logs"]["errors"],
            trace_errors=raw["traces"]["errors"],
            trace_slow=raw["traces"]["slow"],
            service_graph=raw["service_graph"]["edges"],
        )

    @staticmethod
    def render(template: str, service: str) -> str:
        if not SERVICE_NAME_RE.match(service):
            raise ValueError(f"refusing to template unsafe service name {service!r}")
        return template.replace("$service", service)
