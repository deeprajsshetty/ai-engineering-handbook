"""Generate the provisioned Grafana dashboards.

Dashboards are code: edit this file and run ``python build_dashboards.py`` rather than
hand-editing JSON (keeps panels consistent and diffs reviewable).
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

OUT = Path(__file__).parent / "dashboards"
PROM = {"type": "prometheus", "uid": "prometheus"}
LOKI = {"type": "loki", "uid": "loki"}
TEMPO = {"type": "tempo", "uid": "tempo"}


def ts(title: str, exprs: list[tuple[str, str]], unit: str, x: int, y: int, w: int = 12,
       h: int = 8, thresholds: list[tuple[str, float | None]] | None = None) -> dict[str, Any]:
    steps = [{"color": c, "value": v} for c, v in (thresholds or [("green", None)])]
    return {
        "type": "timeseries", "title": title, "datasource": PROM,
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "fieldConfig": {"defaults": {"unit": unit, "thresholds": {"mode": "absolute", "steps": steps},
                                     "custom": {"thresholdsStyle": {"mode": "line"}}},
                        "overrides": []},
        "options": {"legend": {"displayMode": "list", "placement": "bottom"},
                    "tooltip": {"mode": "multi"}},
        "targets": [{"datasource": PROM, "expr": e, "legendFormat": lf, "refId": chr(65 + i)}
                    for i, (e, lf) in enumerate(exprs)],
    }


def stat(title: str, expr: str, unit: str, x: int, y: int, w: int = 6,
         thresholds: list[tuple[str, float | None]] | None = None) -> dict[str, Any]:
    steps = [{"color": c, "value": v} for c, v in (thresholds or [("green", None)])]
    return {
        "type": "stat", "title": title, "datasource": PROM,
        "gridPos": {"x": x, "y": y, "w": w, "h": 4},
        "fieldConfig": {"defaults": {"unit": unit,
                                     "thresholds": {"mode": "absolute", "steps": steps}},
                        "overrides": []},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"]}, "colorMode": "background"},
        "targets": [{"datasource": PROM, "expr": expr, "refId": "A"}],
    }


def dashboard(uid: str, title: str, panels: list[dict[str, Any]],
              templating: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    for i, p in enumerate(panels, 1):
        p["id"] = i
    return {
        "uid": uid, "title": title, "schemaVersion": 39, "version": 1, "editable": False,
        "time": {"from": "now-30m", "to": "now"}, "refresh": "10s", "tags": ["sentinel"],
        "templating": {"list": templating or []}, "panels": panels,
        "annotations": {"list": []},
    }


SERVICE_VAR = {
    "name": "service", "label": "Service", "type": "query", "datasource": PROM,
    "query": {"query": "label_values(shop_server_requests_total, service)", "refId": "q"},
    "definition": "label_values(shop_server_requests_total, service)",
    "includeAll": True, "multi": True, "refresh": 2, "current": {"text": "All", "value": "$__all"},
}

service_overview = dashboard("shop-services", "Shop - Service Golden Signals", [
    stat("Active alerts", 'count(ALERTS{alertstate="firing"}) or vector(0)', "none", 0, 0,
         thresholds=[("green", None), ("red", 1)]),
    stat("Total RPS", "sum(shop:request_rate:1m)", "reqps", 6, 0),
    stat("Worst error ratio", "max(shop:error_ratio:1m)", "percentunit", 12, 0,
         thresholds=[("green", None), ("orange", 0.01), ("red", 0.05)]),
    stat("Worst p95 latency", "max(shop:latency_p95:1m)", "s", 18, 0,
         thresholds=[("green", None), ("orange", 0.25), ("red", 0.5)]),
    ts("Request rate", [('shop:request_rate:1m{service=~"$service"}', "{{service}}")],
       "reqps", 0, 4, 8),
    ts("Error ratio (5xx)", [('shop:error_ratio:1m{service=~"$service"}', "{{service}}")],
       "percentunit", 8, 4, 8, thresholds=[("green", None), ("red", 0.05)]),
    ts("p95 latency", [('shop:latency_p95:1m{service=~"$service"}', "{{service}}")],
       "s", 16, 4, 8, thresholds=[("green", None), ("red", 0.5)]),
    {
        "type": "nodeGraph", "title": "Service dependency map (from traces)", "datasource": TEMPO,
        "gridPos": {"x": 0, "y": 12, "w": 12, "h": 10},
        "targets": [{"datasource": TEMPO, "queryType": "serviceMap", "refId": "A"}],
    },
    {
        "type": "logs", "title": "Error logs", "datasource": LOKI,
        "gridPos": {"x": 12, "y": 12, "w": 12, "h": 10},
        "options": {"showTime": True, "wrapLogMessage": True, "sortOrder": "Descending"},
        "targets": [{"datasource": LOKI, "refId": "A",
                     "expr": '{service_namespace="shop"} | severity_text=~"ERROR|WARN"'}],
    },
    {
        "type": "table", "title": "Recent error traces", "datasource": TEMPO,
        "gridPos": {"x": 0, "y": 22, "w": 24, "h": 8},
        "targets": [{"datasource": TEMPO, "queryType": "traceql", "refId": "A",
                     "query": "{ status = error }", "limit": 20}],
    },
], [SERVICE_VAR])

sentinel_platform = dashboard("sentinel-platform", "Sentinel - AIOps Platform", [
    stat("Active incidents", "max(sentinel_incidents_active) or vector(0)", "none", 0, 0,
         thresholds=[("green", None), ("orange", 1)]),
    stat("Investigation queue", 'max(sentinel_queue_depth{queue="investigations"}) or vector(0)',
         "none", 6, 0, thresholds=[("green", None), ("orange", 5), ("red", 20)]),
    stat("Investigations (1h)", "sum(increase(sentinel_investigations_total[1h])) or vector(0)",
         "none", 12, 0),
    stat("LLM fallbacks (1h)",
         'sum(increase(sentinel_llm_requests_total{outcome="fallback"}[1h])) or vector(0)', "none",
         18, 0, thresholds=[("green", None), ("orange", 1)]),
    ts("Alerts ingested / min", [("sum by (source, status) (rate(sentinel_alerts_received_total[5m])) * 60",
                                  "{{source}} {{status}}")], "none", 0, 4, 8),
    ts("Investigation duration p50/p95", [
        ("histogram_quantile(0.5, sum by (le) (rate(sentinel_investigation_duration_seconds_bucket[10m])))", "p50"),
        ("histogram_quantile(0.95, sum by (le) (rate(sentinel_investigation_duration_seconds_bucket[10m])))", "p95"),
    ], "s", 8, 4, 8),
    ts("Agent step p95", [("histogram_quantile(0.95, sum by (le, agent) (rate(sentinel_agent_step_duration_seconds_bucket[10m])))",
                           "{{agent}}")], "s", 16, 4, 8),
    ts("Telemetry tool calls", [("sum by (backend, outcome) (rate(sentinel_telemetry_queries_total[5m]))",
                                 "{{backend}} {{outcome}}")], "reqps", 0, 12, 8),
    ts("LLM tokens / min", [("sum by (provider, direction) (rate(sentinel_llm_tokens_total[5m])) * 60",
                             "{{provider}} {{direction}}")], "none", 8, 12, 8),
    ts("Remediation transitions", [("sum by (action, status) (increase(sentinel_remediations_total[10m]))",
                                    "{{action}} {{status}}")], "none", 16, 12, 8),
    ts("API latency p95 by route", [("histogram_quantile(0.95, sum by (le, route) (rate(sentinel_http_request_duration_seconds_bucket[5m])))",
                                     "{{route}}")], "s", 0, 20, 12),
    ts("RCA confidence (p50)", [("histogram_quantile(0.5, sum by (le) (rate(sentinel_rca_confidence_bucket[1h])))",
                                 "median confidence")], "percentunit", 12, 20, 12),
])

infrastructure = dashboard("infrastructure", "Infrastructure - Host & Database", [
    ts("CPU utilisation by state", [('sum by (state) (rate(system_cpu_time_seconds_total{state!="idle"}[1m]))',
                                     "{{state}}")], "percentunit", 0, 0, 12),
    ts("Memory used", [('sum(system_memory_usage_bytes{state="used"})', "used"),
                       ("sum(system_memory_usage_bytes)", "total")], "bytes", 12, 0, 12),
    ts("Load average", [("system_cpu_load_average_1m", "1m"), ("system_cpu_load_average_5m", "5m")],
       "none", 0, 8, 8),
    ts("Network I/O", [("sum by (direction) (rate(system_network_io_bytes_total[1m]))", "{{direction}}")],
       "Bps", 8, 8, 8),
    ts("Postgres backends", [("sum(postgresql_backends)", "connections")], "none", 16, 8, 8),
    ts("Postgres commits / rollbacks", [("sum(rate(postgresql_commits_total[1m]))", "commits"),
                                       ("sum(rate(postgresql_rollbacks_total[1m]))", "rollbacks")],
       "ops", 0, 16, 12),
    ts("Collector throughput", [
        ("sum(rate(otelcol_receiver_accepted_spans_total[1m]))", "spans accepted"),
        ("sum(rate(otelcol_receiver_accepted_metric_points_total[1m]))", "metric points accepted"),
        ("sum(rate(otelcol_receiver_accepted_log_records_total[1m]))", "log records accepted"),
        ("sum(rate(otelcol_exporter_send_failed_spans_total[1m]))", "spans failed"),
    ], "none", 12, 16, 12),
])

if __name__ == "__main__":
    OUT.mkdir(exist_ok=True)
    for name, d in (("shop-services.json", service_overview),
                    ("sentinel-platform.json", sentinel_platform),
                    ("infrastructure.json", infrastructure)):
        (OUT / name).write_text(json.dumps(d, indent=2) + "\n")
        print("wrote", OUT / name)
