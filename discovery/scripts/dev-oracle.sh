#!/usr/bin/env bash
# Local Oracle Database Free (23ai) for developing and testing the Oracle
# platform store (ADR 0011). Uses the community image gvenzl/oracle-free
# (Oracle Free Use Terms; for development and testing, not production).
# Listens on 127.0.0.1 only. Passwords are generated once into
# .local/oracle.env (git-ignored, mode 600) and never printed by this script.
#
#   scripts/dev-oracle.sh start|stop|status|url|admin-url|destroy
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENVF="$ROOT/.local/oracle.env"
NAME="${OBS_DEV_ORACLE_CONTAINER:-obs-oracle}"
PORT="${OBS_DEV_ORACLE_PORT:-15210}"
IMAGE="${OBS_DEV_ORACLE_IMAGE:-gvenzl/oracle-free:23-slim-faststart}"
SERVICE=FREEPDB1

need_env() {
  mkdir -p "$ROOT/.local"
  if [ ! -s "$ENVF" ]; then
    (umask 077
     p=$(head -c 18 /dev/urandom | base64 | tr -d '/+=' | cut -c1-20)
     printf 'ORACLE_PASSWORD=%sA1\nAPP_USER_PASSWORD=%sB2\n' "$p" "$p" >"$ENVF")
  fi
  # shellcheck disable=SC1090
  set -a; . "$ENVF"; set +a
}

# The image declares no Docker HEALTHCHECK but ships /opt/oracle/healthcheck.sh,
# which succeeds once the database and its PDB are open.
ready() {
  [ -n "$(docker ps -q -f name="^${NAME}$")" ] && docker exec "$NAME" healthcheck.sh >/dev/null 2>&1
}

case "${1:-status}" in
  start)
    need_env
    if [ -z "$(docker ps -q -f name="^${NAME}$")" ]; then
      if [ -n "$(docker ps -aq -f name="^${NAME}$")" ]; then
        docker start "$NAME" >/dev/null
      else
        docker run -d --name "$NAME" -p "127.0.0.1:${PORT}:1521" --env-file "$ENVF" -e APP_USER=obs "$IMAGE" >/dev/null
      fi
    fi
    for _ in $(seq 1 120); do
      ready && { echo "oracle ready on 127.0.0.1:${PORT}/${SERVICE}"; exit 0; }
      sleep 2
    done
    echo "error: oracle did not become healthy; see: docker logs $NAME" >&2; exit 1
    ;;
  stop) docker stop "$NAME" >/dev/null 2>&1 && echo stopped || echo "not running" ;;
  status) ready && echo "running on 127.0.0.1:${PORT}" || { echo "not running"; exit 1; } ;;
  url) need_env; echo "oracle://obs:${APP_USER_PASSWORD}@127.0.0.1:${PORT}/${SERVICE}" ;;
  admin-url) need_env; echo "oracle://system:${ORACLE_PASSWORD}@127.0.0.1:${PORT}/${SERVICE}" ;;
  destroy) docker rm -f "$NAME" >/dev/null 2>&1 || true; echo "removed container $NAME (all local Oracle data deleted)" ;;
  *) echo "usage: $0 start|stop|status|url|admin-url|destroy" >&2; exit 2 ;;
esac
