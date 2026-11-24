#!/usr/bin/env bash
# One-command local demo: database, migrations, seed, web build, server,
# end-to-end smoke test. Leaves the server running at http://localhost:8080.
#   scripts/demo.sh        start the demo
#   scripts/demo.sh stop   stop the server and the local database
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCAL="$ROOT/.local"
PIDFILE="$LOCAL/obsplatform.pid"
ADDR="${OBS_HTTP_ADDR:-:8080}"
PORT="${ADDR##*:}"
mkdir -p "$LOCAL"

stop_server() {
  if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
    kill "$(cat "$PIDFILE")"; echo "server stopped"
  fi
  rm -f "$PIDFILE"
}

if [ "${1:-}" = "stop" ]; then
  stop_server
  [ -z "${OBS_DATABASE_URL:-}" ] && "$ROOT/scripts/dev-postgres.sh" stop || true
  if [ -z "${OBS_DATABASE_URL:-}" ] && command -v docker >/dev/null; then "$ROOT/scripts/dev-oracle.sh" stop >/dev/null 2>&1 || true; fi
  exit 0
fi

# Platform store: PostgreSQL (default) or Oracle (OBS_STORE_DRIVER=oracle,
# local Oracle Database Free container from scripts/dev-oracle.sh).
export OBS_STORE_DRIVER="${OBS_STORE_DRIVER:-postgres}"
if [ -z "${OBS_DATABASE_URL:-}" ]; then
  # The local PostgreSQL always starts: it is the store in postgres mode and,
  # in both modes, the server monitored by the measured-database demo.
  "$ROOT/scripts/dev-postgres.sh" start >/dev/null
  if [ "$OBS_STORE_DRIVER" = oracle ]; then
    "$ROOT/scripts/dev-oracle.sh" start
    export OBS_DATABASE_URL="$("$ROOT/scripts/dev-oracle.sh" url)"
  else
    export OBS_DATABASE_URL="$("$ROOT/scripts/dev-postgres.sh" url)"
  fi
  # Measured database demo: the generic database integration monitors the
  # local PostgreSQL through a least-privilege role (pg_monitor only).
  PG_PORT="${OBS_DEV_PG_PORT:-54329}"
  "$ROOT/scripts/dev-postgres.sh" monitor-role >/dev/null
  mkdir -p "$LOCAL/secrets" && chmod 700 "$LOCAL/secrets"
  [ -s "$LOCAL/secrets/demo-db-password" ] || { umask 077; head -c 24 /dev/urandom | base64 | tr -d '/+=' >"$LOCAL/secrets/demo-db-password"; }
  export OBS_SEED_DATABASE_TARGET="127.0.0.1:${PG_PORT}/obsplatform"
  export OBS_SECRET_DIR="$LOCAL/secrets"
  export OBS_EGRESS_ALLOWLIST="${OBS_EGRESS_ALLOWLIST:-127.0.0.1}"
  export OBS_ALLOW_INSECURE_ENDPOINTS="${OBS_ALLOW_INSECURE_ENDPOINTS:-true}" # loopback, plaintext: local demo only
fi
echo "==> platform store (${OBS_STORE_DRIVER}): $(echo "$OBS_DATABASE_URL" | sed -E 's#//([^:@/]+):[^@]*@#//\1:***@#')"

echo "==> building web UI"
(cd "$ROOT/platform/web" && { [ -d node_modules ] || npm ci --no-audit --no-fund; } && npm run build >/dev/null)
echo "==> building server"
(cd "$ROOT/platform" && go build -o "$LOCAL/obsplatform" ./cmd/obsplatform)

echo "==> migrating and seeding"
"$LOCAL/obsplatform" migrate up
"$LOCAL/obsplatform" seed

stop_server
echo "==> starting server on $ADDR (log: .local/obsplatform.log)"
OBS_WEB_DIR="$ROOT/platform/web/dist" OBS_LOG_FORMAT="${OBS_LOG_FORMAT:-text}" \
  nohup "$LOCAL/obsplatform" serve >"$LOCAL/obsplatform.log" 2>&1 &
echo $! >"$PIDFILE"

echo "==> smoke test"
(cd "$ROOT/platform" && go run ./cmd/smoke -url "http://localhost:${PORT}" -token dev-admin)

cat <<MSG

Demo is running: http://localhost:${PORT}
Sign in with the development token "dev-admin" (development auth only).
Stop with: make demo-stop
MSG
