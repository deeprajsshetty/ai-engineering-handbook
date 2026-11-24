"""Runtime configuration.

All settings come from environment variables prefixed with ``SENTINEL_`` (12-factor).
Secrets are typed as ``SecretStr`` so they never appear in logs or reprs.
"""

from __future__ import annotations

from functools import lru_cache
from pathlib import Path
from typing import Literal

from pydantic import Field, SecretStr, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

PACKAGE_DIR = Path(__file__).resolve().parent
DEFAULT_CONFIG_DIR = PACKAGE_DIR / "defaults"


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_prefix="SENTINEL_", env_file=".env", extra="ignore", case_sensitive=False
    )

    # --- General ---------------------------------------------------------------------------
    env: Literal["dev", "test", "prod"] = "dev"
    service_name: str = "sentinel-api"
    log_level: str = "INFO"
    log_json: bool = True

    # --- Persistence -----------------------------------------------------------------------
    database_url: str = "sqlite:///./sentinel.db"
    db_pool_size: int = 5
    db_max_overflow: int = 10

    # --- Security --------------------------------------------------------------------------
    # Comma separated "name:role:sha256(key)" entries. Generate with `sentinel hash-key`.
    api_keys: str = ""
    # Bearer token Alertmanager presents on the webhook (compared in constant time).
    webhook_token: SecretStr = SecretStr("")
    # Token Sentinel sends to remediation targets (e.g. the demo services' admin API).
    action_token: SecretStr = SecretStr("")
    rate_limit_per_minute: int = 600
    rate_limit_burst: int = 120
    max_request_bytes: int = 1_000_000

    # --- Telemetry backends (unset = backend disabled) -----------------------------------
    prometheus_url: str | None = None
    loki_url: str | None = None
    tempo_url: str | None = None
    telemetry_timeout_s: float = 5.0
    telemetry_retries: int = 2

    # --- Discovery service (discovery/, the Go platform; unset = disabled) ---------------
    # Read-only: Sentinel only calls GET /api/v1/resources and /resources/{id}/neighborhood.
    discovery_url: str | None = None
    discovery_token: SecretStr = SecretStr("")  # a viewer-role token is enough
    discovery_timeout_s: float = 5.0
    # Resource types that represent an application service, matched by exact name.
    discovery_service_types: str = "k8s.deployment,k8s.service"
    discovery_depth: int = Field(default=2, ge=1, le=4)
    discovery_max_nodes: int = Field(default=50, ge=1, le=500)

    # --- AI / LLM --------------------------------------------------------------------------
    llm_provider: Literal["deterministic", "openai", "anthropic"] = "deterministic"
    llm_model: str | None = None
    llm_timeout_s: float = 30.0
    llm_max_tokens: int = 700
    openai_api_key: SecretStr = SecretStr("")
    openai_base_url: str = "https://api.openai.com/v1"
    anthropic_api_key: SecretStr = SecretStr("")
    anthropic_base_url: str = "https://api.anthropic.com"

    # --- Correlation & investigation -------------------------------------------------------
    auto_investigate: bool = True
    correlation_window_minutes: int = 15
    correlation_max_hops: int = 2
    investigation_lookback_minutes: int = 30
    investigation_recent_minutes: int = 5
    investigation_step_seconds: int = 15
    investigation_max_attempts: int = 3
    anomaly_z_threshold: float = 3.5

    # --- Worker ----------------------------------------------------------------------------
    worker_poll_interval_s: float = 2.0
    worker_lease_s: int = 180
    worker_metrics_port: int = 9100

    # --- Remediation & notification --------------------------------------------------------
    auto_approve_low_risk: bool = False
    # How long an `executing` remediation may go without a recorded result before it is marked
    # failed/unknown_outcome. Must exceed the executor's HTTP timeout (10 s).
    remediation_lease_s: int = Field(default=120, ge=30)
    notification_webhook_url: str | None = None

    # --- Config files ----------------------------------------------------------------------
    config_dir: Path = Field(default=DEFAULT_CONFIG_DIR)

    # --- Integrations ----------------------------------------------------------------------
    mcp_enabled: bool = True
    otel_enabled: bool = False

    @model_validator(mode="after")
    def _validate_prod(self) -> Settings:
        if self.env == "prod":
            problems = []
            if not self.api_keys:
                problems.append("SENTINEL_API_KEYS must be set")
            if len(self.webhook_token.get_secret_value()) < 24:
                problems.append("SENTINEL_WEBHOOK_TOKEN must be at least 24 characters")
            if self.database_url.startswith("sqlite"):
                problems.append("SQLite is not supported in prod; use PostgreSQL")
            if problems:
                raise ValueError("; ".join(problems))
        return self

    @property
    def actions_file(self) -> Path:
        return self.config_dir / "actions.yaml"

    @property
    def services_file(self) -> Path:
        return self.config_dir / "services.yaml"

    @property
    def signals_file(self) -> Path:
        return self.config_dir / "signals.yaml"


@lru_cache
def get_settings() -> Settings:
    return Settings()
