"""Service catalog and dependency mapping (static catalog + trace-discovered edges)."""

from __future__ import annotations

import logging
import time
from typing import Any

from sqlalchemy import select
from sqlalchemy.orm import Session

from sentinel.application.container import Container
from sentinel.domain.topology import DependencyGraph
from sentinel.infrastructure.catalog import SERVICE_NAME_RE, ServiceSpec, load_services
from sentinel.infrastructure.orm import ServiceRow
from sentinel.infrastructure.telemetry import TelemetryError

log = logging.getLogger(__name__)
_DISCOVERY_TTL_S = 60.0
_VIRTUAL_NODES = {"user", "unknown"}


class TopologyService:
    def __init__(self, c: Container) -> None:
        self.c = c
        self._discovered: tuple[float, DependencyGraph] | None = None

    # -- catalog -------------------------------------------------------------------------------
    def seed_from_file(self, session: Session) -> int:
        """Insert catalog services that do not exist yet (never overwrites operator edits)."""
        existing = set(session.scalars(select(ServiceRow.name)))
        added = 0
        for spec in load_services(self.c.settings.services_file):
            if spec.name not in existing:
                session.add(_row(spec))
                added += 1
        return added

    def upsert(self, session: Session, spec: ServiceSpec) -> ServiceRow:
        row = session.scalar(select(ServiceRow).where(ServiceRow.name == spec.name))
        if row is None:
            row = _row(spec)
            session.add(row)
        else:
            row.team, row.tier, row.description = spec.team, spec.tier, spec.description
            row.dependencies, row.runbook_url = spec.dependencies, spec.runbook_url
        session.flush()
        return row

    def catalog_services(self, session: Session) -> set[str]:
        """Services in the operator-controlled catalog (seed file + admin API).

        This is the only set remediation actions may target. Trace-discovered nodes are
        deliberately excluded: service-graph labels come from telemetry, which any instrumented
        workload can write, so they must never decide where Sentinel sends credentials.
        """
        return set(session.scalars(select(ServiceRow.name)))

    # -- graph ---------------------------------------------------------------------------------
    def static_graph(self, session: Session) -> DependencyGraph:
        g = DependencyGraph()
        for row in session.scalars(select(ServiceRow)):
            g.add_node(row.name)
            for dep in row.dependencies or []:
                g.add_edge(row.name, dep)
        return g

    def discovered(self) -> DependencyGraph:
        """Edges observed in traces (servicegraph connector metrics), cached for 60s."""
        now = time.monotonic()
        if self._discovered and now - self._discovered[0] < _DISCOVERY_TTL_S:
            return self._discovered[1]
        g = DependencyGraph()
        try:
            for series in self.c.telemetry.query_instant(self.c.queries.service_graph):
                client = series.labels.get("client", "")
                server = series.labels.get("server", "")
                value = series.points[-1].value if series.points else 0.0
                if client in _VIRTUAL_NODES or server in _VIRTUAL_NODES:
                    continue  # servicegraph placeholders for un-instrumented peers
                if value > 0 and SERVICE_NAME_RE.match(client) and SERVICE_NAME_RE.match(server):
                    g.add_edge(client, server)
        except TelemetryError as exc:
            log.info("service graph discovery unavailable: %s", exc)
        self._discovered = (now, g)
        return g

    def graph(self, session: Session) -> DependencyGraph:
        return self.static_graph(session).merge(self.discovered())

    def dependency_map(self, session: Session) -> dict[str, Any]:
        static = self.static_graph(session)
        disc = self.discovered()
        merged = static.merge(disc)
        static_edges, disc_edges = set(static.edges), set(disc.edges)
        return {
            "nodes": sorted(merged.nodes),
            "edges": [
                {
                    "caller": a,
                    "callee": b,
                    "source": "both"
                    if (a, b) in static_edges and (a, b) in disc_edges
                    else "catalog"
                    if (a, b) in static_edges
                    else "traces",
                }
                for a, b in merged.edges
            ],
        }


def _row(spec: ServiceSpec) -> ServiceRow:
    return ServiceRow(
        name=spec.name,
        team=spec.team,
        tier=spec.tier,
        description=spec.description,
        dependencies=list(spec.dependencies),
        runbook_url=spec.runbook_url,
    )
