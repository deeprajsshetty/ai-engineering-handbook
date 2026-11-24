"""Outbound notifications (Slack-compatible incoming webhook JSON: ``{"text": ...}``).

Notification failure must never break incident handling, so errors are logged and counted.
"""

from __future__ import annotations

import logging

import httpx
from tenacity import Retrying, stop_after_attempt, wait_exponential

from sentinel.config import Settings
from sentinel.observability.metrics import NOTIFICATIONS

log = logging.getLogger(__name__)


class Notifier:
    def __init__(self, settings: Settings, client: httpx.Client | None = None) -> None:
        self.url = settings.notification_webhook_url
        self.client = client or httpx.Client(timeout=5.0)

    @property
    def enabled(self) -> bool:
        return bool(self.url)

    def send(self, text: str) -> bool:
        if not self.url:
            NOTIFICATIONS.labels("disabled").inc()
            return False
        url = self.url
        try:
            for attempt in Retrying(
                stop=stop_after_attempt(3), wait=wait_exponential(multiplier=0.3), reraise=True
            ):
                with attempt:
                    self.client.post(url, json={"text": text}).raise_for_status()
        except httpx.HTTPError:
            log.exception("notification failed")
            NOTIFICATIONS.labels("error").inc()
            return False
        NOTIFICATIONS.labels("ok").inc()
        return True
