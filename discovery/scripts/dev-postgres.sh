#!/usr/bin/env bash
# Local PostgreSQL for development without Docker, using the PostgreSQL
# binaries on PATH (initdb, pg_ctl, psql). Data lives in .local/pg (git-ignored).
# Listens on 127.0.0.1 only, with trust authentication for the local "obs" user:
# acceptable for a developer laptop, never for a shared host.
#
#   scripts/dev-postgres.sh start|stop|status|url|destroy
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIR="$ROOT/.local/pg"
PORT="${OBS_DEV_PG_PORT:-54329}"
DB=obsplatform
export LC_ALL=C LANG=C # postmaster refuses to start on macOS without a valid locale
URL="postgres://obs@127.0.0.1:${PORT}/${DB}?sslmode=disable"

need() { command -v "$1" >/dev/null || { echo "error: $1 not found; install PostgreSQL 16+ or use 'make db-up-docker'" >&2; exit 1; }; }

case "${1:-status}" in
  start)
    need initdb; need pg_ctl; need psql
    mkdir -p "$ROOT/.local"
    if [ ! -f "$DIR/PG_VERSION" ]; then
      initdb -D "$DIR" -U obs --auth=trust -E UTF8 --no-locale >/dev/null
      echo "initialized $DIR"
    fi
    if pg_ctl -D "$DIR" status >/dev/null 2>&1; then
      echo "already running"
    else
      pg_ctl -D "$DIR" -l "$ROOT/.local/pg.log" -w -t 30 \
        -o "-p $PORT -c listen_addresses=127.0.0.1 -c unix_socket_directories=''" start >/dev/null
      echo "started on 127.0.0.1:$PORT (log: .local/pg.log)"
    fi
    psql "postgres://obs@127.0.0.1:${PORT}/postgres" -tAc "SELECT 1 FROM pg_database WHERE datname='${DB}'" | grep -q 1 \
      || psql "postgres://obs@127.0.0.1:${PORT}/postgres" -qc "CREATE DATABASE ${DB}"
    echo "OBS_DATABASE_URL=$URL"
    ;;
  stop)
    need pg_ctl
    if [ -f "$DIR/PG_VERSION" ] && pg_ctl -D "$DIR" status >/dev/null 2>&1; then pg_ctl -D "$DIR" -m fast -w stop >/dev/null; echo stopped; else echo "not running"; fi
    ;;
  status)
    need pg_ctl
    if [ -f "$DIR/PG_VERSION" ] && pg_ctl -D "$DIR" status >/dev/null 2>&1; then echo "running on 127.0.0.1:$PORT"; else echo "not running"; exit 1; fi
    ;;
  monitor-role)
    # Read-only monitoring role for the measured-database demo. The server
    # uses trust authentication locally, so no password is set here; a real
    # server would use scram-sha-256 and the password from credentialRef.
    need psql
    psql "postgres://obs@127.0.0.1:${PORT}/postgres" -qc "DO \$\$ BEGIN
      IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'obs_monitor') THEN
        CREATE ROLE obs_monitor LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE IN ROLE pg_monitor;
      END IF; END \$\$;"
    echo "role obs_monitor (pg_monitor) ready"
    ;;
  url) echo "$URL" ;;
  destroy)
    "$0" stop || true
    rm -rf "$DIR" && echo "removed $DIR (all local data deleted)"
    ;;
  *) echo "usage: $0 start|stop|status|monitor-role|url|destroy" >&2; exit 2 ;;
esac
