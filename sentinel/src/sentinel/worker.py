"""Background worker: runs investigations and executes approved remediations.

Run several replicas for throughput/HA; claims are safe under concurrency (SKIP LOCKED).
Stops gracefully on SIGTERM (finishes the current unit of work first).
"""

from __future__ import annotations

import logging
import signal
import threading
import time

from prometheus_client import start_http_server

from sentinel.application.container import Container
from sentinel.application.facade import SentinelApp
from sentinel.config import get_settings
from sentinel.observability.logging import configure_logging
from sentinel.observability.tracing import configure_tracing

log = logging.getLogger("sentinel.worker")


def main() -> None:
    settings = get_settings()
    configure_logging(settings, service="sentinel-worker")
    configure_tracing(settings, service="sentinel-worker")
    app = SentinelApp(Container.build(settings))
    app.bootstrap()
    start_http_server(settings.worker_metrics_port)

    stop = threading.Event()

    def _handle(signum: int, _frame: object) -> None:
        log.info("received signal %s, shutting down after current task", signum)
        stop.set()

    signal.signal(signal.SIGTERM, _handle)
    signal.signal(signal.SIGINT, _handle)
    log.info("worker started", extra={"poll_interval_s": settings.worker_poll_interval_s})

    last_gauge = 0.0
    while not stop.is_set():
        try:
            busy = app.process_once()
            if time.monotonic() - last_gauge > 15:
                app.investigations.queue_depth()
                last_gauge = time.monotonic()
        except Exception:
            log.exception("worker iteration failed")
            busy = False
        if not busy:
            stop.wait(settings.worker_poll_interval_s)
    log.info("worker stopped")


if __name__ == "__main__":
    main()
