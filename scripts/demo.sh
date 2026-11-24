#!/usr/bin/env bash
# Drive the observability use case against a running compose stack.
#   scripts/demo.sh fault payments errors   # inject 5xx into payments
#   scripts/demo.sh fault inventory latency # inject 900ms latency into inventory
#   scripts/demo.sh clear payments          # clear a fault by hand
#   scripts/demo.sh status                  # show incidents
set -euo pipefail
cd "$(dirname "$0")/../deploy/compose"
set -a; source .env; source .env.keys; set +a
port_of() { case "$1" in frontend) echo 8080;; checkout) echo 8081;; payments) echo 8082;; inventory) echo 8083;; *) echo "unknown service $1" >&2; exit 1;; esac; }

case "${1:-status}" in
  fault)
    svc="${2:?service}"; kind="${3:-errors}"
    if [[ "$kind" == "latency" ]]; then body='{"latency_ms": 900, "duration_s": 900}'
    else body='{"error_rate": 0.6, "duration_s": 900}'; fi
    curl -fsS -X POST "http://localhost:$(port_of "$svc")/admin/faults" \
      -H "X-Admin-Token: ${DEMO_ADMIN_TOKEN}" -H 'Content-Type: application/json' -d "$body"; echo ;;
  clear)
    curl -fsS -X POST "http://localhost:$(port_of "${2:?service}")/admin/faults/clear" \
      -H "X-Admin-Token: ${DEMO_ADMIN_TOKEN}"; echo ;;
  status)
    curl -fsS "http://localhost:8000/api/v1/incidents?status=active" -H "X-API-Key: ${SENTINEL_VIEWER_KEY}" \
      | python3 -m json.tool ;;
  *) echo "usage: $0 fault <service> [errors|latency] | clear <service> | status"; exit 1 ;;
esac
