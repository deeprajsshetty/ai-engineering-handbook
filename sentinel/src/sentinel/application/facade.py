"""Application facade: one object that wires the use-case services together."""

from __future__ import annotations

from sentinel.application.alerts import AlertIngestionService
from sentinel.application.container import Container
from sentinel.application.investigations import InvestigationService
from sentinel.application.remediation import RemediationService
from sentinel.application.topology import TopologyService


class SentinelApp:
    def __init__(self, container: Container) -> None:
        self.c = container
        self.topology = TopologyService(container)
        self.investigations = InvestigationService(container, self.topology)
        self.alerts = AlertIngestionService(container, self.topology, self.investigations)
        self.remediations = RemediationService(container, self.topology)

    def bootstrap(self) -> None:
        """Seed the service catalog (idempotent)."""
        with self.c.db.session() as s:
            self.topology.seed_from_file(s)

    def process_once(self) -> bool:
        """Do one unit of background work. Returns True if anything was processed."""
        did = False
        inv_id = self.investigations.claim_next()
        if inv_id is not None:
            self.investigations.run(inv_id)
            did = True
        rem_id = self.remediations.claim_next_approved()
        if rem_id is not None:
            self.remediations.execute(rem_id)
            did = True
        return did

    def drain(self, max_iterations: int = 50) -> int:
        """Process work until the queues are empty (used by tests and the CLI)."""
        n = 0
        while n < max_iterations and self.process_once():
            n += 1
        return n
