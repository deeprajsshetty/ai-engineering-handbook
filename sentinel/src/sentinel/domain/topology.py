"""Service dependency graph.

Edges point from caller to callee: ``frontend -> checkout`` means frontend depends on checkout.
"""

from __future__ import annotations

from collections import deque
from collections.abc import Iterable


class DependencyGraph:
    def __init__(self, edges: Iterable[tuple[str, str]] = ()) -> None:
        self._down: dict[str, set[str]] = {}
        self._up: dict[str, set[str]] = {}
        for caller, callee in edges:
            self.add_edge(caller, callee)

    def add_edge(self, caller: str, callee: str) -> None:
        if caller == callee:
            return
        self._down.setdefault(caller, set()).add(callee)
        self._up.setdefault(callee, set()).add(caller)
        self._down.setdefault(callee, set())
        self._up.setdefault(caller, set())

    def add_node(self, name: str) -> None:
        self._down.setdefault(name, set())
        self._up.setdefault(name, set())

    @property
    def nodes(self) -> set[str]:
        return set(self._down) | set(self._up)

    @property
    def edges(self) -> list[tuple[str, str]]:
        return sorted((a, b) for a, deps in self._down.items() for b in deps)

    def dependencies(self, service: str) -> set[str]:
        """Direct callees of ``service``."""
        return set(self._down.get(service, set()))

    def dependents(self, service: str) -> set[str]:
        """Direct callers of ``service``."""
        return set(self._up.get(service, set()))

    def downstream(self, service: str, max_depth: int = 10) -> set[str]:
        return self._walk(service, self._down, max_depth)

    def upstream(self, service: str, max_depth: int = 10) -> set[str]:
        return self._walk(service, self._up, max_depth)

    def neighborhood(self, service: str, hops: int) -> set[str]:
        """All services within ``hops`` edges in either direction (excluding itself)."""
        seen = {service}
        frontier = deque([(service, 0)])
        while frontier:
            node, depth = frontier.popleft()
            if depth >= hops:
                continue
            for nxt in self._down.get(node, set()) | self._up.get(node, set()):
                if nxt not in seen:
                    seen.add(nxt)
                    frontier.append((nxt, depth + 1))
        seen.discard(service)
        return seen

    @staticmethod
    def _walk(start: str, adj: dict[str, set[str]], max_depth: int) -> set[str]:
        seen: set[str] = set()
        frontier = deque([(start, 0)])
        while frontier:
            node, depth = frontier.popleft()
            if depth >= max_depth:
                continue
            for nxt in adj.get(node, set()):
                if nxt not in seen and nxt != start:
                    seen.add(nxt)
                    frontier.append((nxt, depth + 1))
        return seen

    def merge(self, other: DependencyGraph) -> DependencyGraph:
        merged = DependencyGraph(self.edges + other.edges)
        for n in self.nodes | other.nodes:
            merged.add_node(n)
        return merged

    def to_dict(self) -> dict[str, list[str]]:
        return {n: sorted(self._down.get(n, set())) for n in sorted(self.nodes)}
