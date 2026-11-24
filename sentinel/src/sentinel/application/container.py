"""Composition root: builds every long-lived dependency exactly once.

Tests construct a Container with fakes (e.g. an in-memory telemetry backend) instead of
patching globals.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from sentinel.config import Settings
from sentinel.infrastructure.actions import ActionExecutor
from sentinel.infrastructure.catalog import ActionCatalog, SignalQueries
from sentinel.infrastructure.db import Database
from sentinel.infrastructure.discovery import DiscoverySource, HttpDiscoverySource
from sentinel.infrastructure.notifications import Notifier
from sentinel.infrastructure.telemetry import HttpTelemetryBackend, TelemetryBackend
from sentinel.knowledge.retriever import RunbookRetriever
from sentinel.llm.providers import LLMProvider, build_provider


@dataclass
class Container:
    settings: Settings
    db: Database
    telemetry: TelemetryBackend
    llm: LLMProvider
    retriever: RunbookRetriever
    actions: ActionCatalog
    queries: SignalQueries
    executor: ActionExecutor
    notifier: Notifier
    discovery: DiscoverySource
    extras: dict[str, object] = field(default_factory=dict)

    @classmethod
    def build(
        cls,
        settings: Settings,
        *,
        db: Database | None = None,
        telemetry: TelemetryBackend | None = None,
        llm: LLMProvider | None = None,
        executor: ActionExecutor | None = None,
        notifier: Notifier | None = None,
        discovery: DiscoverySource | None = None,
    ) -> Container:
        return cls(
            settings=settings,
            db=db or Database(settings),
            telemetry=telemetry or HttpTelemetryBackend(settings),
            llm=llm or build_provider(settings),
            retriever=RunbookRetriever.from_directory(),
            actions=ActionCatalog.load(settings.actions_file),
            queries=SignalQueries.load(settings.signals_file),
            executor=executor or ActionExecutor(settings),
            notifier=notifier or Notifier(settings),
            discovery=discovery or HttpDiscoverySource(settings),
        )
