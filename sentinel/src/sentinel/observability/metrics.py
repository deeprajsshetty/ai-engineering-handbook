"""Prometheus metrics for Sentinel itself ("who watches the watchers").

Metric names follow Prometheus conventions: ``sentinel_<noun>_<unit>`` and ``_total`` counters.
Label cardinality is bounded: routes are templated paths, never raw URLs or ids.
"""

from __future__ import annotations

from prometheus_client import Counter, Gauge, Histogram

HTTP_REQUESTS = Counter(
    "sentinel_http_requests_total", "HTTP requests served", ["method", "route", "status"]
)
HTTP_LATENCY = Histogram(
    "sentinel_http_request_duration_seconds",
    "HTTP request latency",
    ["method", "route"],
    buckets=(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5),
)
ALERTS_RECEIVED = Counter("sentinel_alerts_received_total", "Alerts ingested", ["source", "status"])
INCIDENTS_CREATED = Counter("sentinel_incidents_created_total", "Incidents opened", ["severity"])
INCIDENTS_ACTIVE = Gauge("sentinel_incidents_active", "Incidents not yet resolved")
INVESTIGATIONS = Counter("sentinel_investigations_total", "Investigations finished", ["status"])
INVESTIGATION_DURATION = Histogram(
    "sentinel_investigation_duration_seconds",
    "End-to-end investigation duration",
    buckets=(0.5, 1, 2, 5, 10, 20, 30, 60, 120),
)
AGENT_STEP_DURATION = Histogram(
    "sentinel_agent_step_duration_seconds",
    "Duration of each agent node",
    ["agent"],
    buckets=(0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30),
)
AGENT_ERRORS = Counter("sentinel_agent_errors_total", "Agent node failures", ["agent"])
RCA_CONFIDENCE = Histogram(
    "sentinel_rca_confidence",
    "Confidence of the top root-cause hypothesis",
    buckets=(0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0),
)
LLM_REQUESTS = Counter("sentinel_llm_requests_total", "LLM calls", ["provider", "outcome"])
LLM_TOKENS = Counter("sentinel_llm_tokens_total", "LLM tokens consumed", ["provider", "direction"])
TELEMETRY_QUERIES = Counter(
    "sentinel_telemetry_queries_total", "Queries to telemetry backends", ["backend", "outcome"]
)
REMEDIATIONS = Counter(
    "sentinel_remediations_total", "Remediation state transitions", ["action", "status"]
)
REMEDIATION_UNKNOWN_OUTCOME = Counter(
    "sentinel_remediation_unknown_outcome_total",
    "Executions whose worker died before recording a result (needs human verification)",
    ["action"],
)
NOTIFICATIONS = Counter("sentinel_notifications_total", "Outbound notifications", ["outcome"])
QUEUE_DEPTH = Gauge("sentinel_queue_depth", "Pending work items", ["queue"])
