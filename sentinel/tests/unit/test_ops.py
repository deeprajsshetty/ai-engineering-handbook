"""Notifier, CLI and logging behaviour."""

from __future__ import annotations

import json
import logging
from pathlib import Path

import httpx
import pytest

from sentinel import cli
from sentinel.api.security import hash_key
from sentinel.config import Settings
from sentinel.infrastructure.notifications import Notifier
from sentinel.observability.logging import JsonFormatter, request_id_var


def test_notifier_disabled_without_url() -> None:
    assert Notifier(Settings(env="test")).send("x") is False


def test_notifier_posts_and_retries() -> None:
    calls: list[httpx.Request] = []

    def h(req: httpx.Request) -> httpx.Response:
        calls.append(req)
        return httpx.Response(500 if len(calls) == 1 else 200)

    n = Notifier(
        Settings(env="test", notification_webhook_url="http://hooks/x"),
        client=httpx.Client(transport=httpx.MockTransport(h)),
    )
    assert n.enabled and n.send("incident opened") is True
    assert len(calls) == 2 and json.loads(calls[-1].content) == {"text": "incident opened"}


def test_notifier_gives_up_quietly() -> None:
    n = Notifier(
        Settings(env="test", notification_webhook_url="http://hooks/x"),
        client=httpx.Client(transport=httpx.MockTransport(lambda r: httpx.Response(503))),
    )
    assert n.send("x") is False


def test_cli_keys(capsys: pytest.CaptureFixture[str]) -> None:
    assert cli.main(["hash-key", "abc"]) == 0
    assert capsys.readouterr().out.strip() == hash_key("abc")
    cli.main(["gen-key", "ci-bot", "viewer"])
    out = capsys.readouterr().out
    key = out.split("shown once): ")[1].split()[0]
    assert f"ci-bot:viewer:{hash_key(key)}" in out


def test_cli_migrate_and_seed(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    from sentinel.config import get_settings

    monkeypatch.setenv("SENTINEL_DATABASE_URL", f"sqlite:///{tmp_path / 'cli.db'}")
    get_settings.cache_clear()
    try:
        assert cli.main(["migrate"]) == 0
        assert cli.main(["seed"]) == 0
        assert cli.main(["drain"]) == 0
    finally:
        get_settings.cache_clear()
    out = capsys.readouterr().out
    assert "migrations applied" in out and "seeded" in out and "processed 0" in out


def test_json_log_line_has_request_id() -> None:
    token = request_id_var.set("req-1")
    try:
        rec = logging.LogRecord("x", logging.INFO, __file__, 1, "hello %s", ("w",), None)
        rec.incident_id = 7
        line = json.loads(JsonFormatter("svc").format(rec))
    finally:
        request_id_var.reset(token)
    assert line["message"] == "hello w" and line["request_id"] == "req-1"
    assert line["incident_id"] == 7 and line["service"] == "svc"
