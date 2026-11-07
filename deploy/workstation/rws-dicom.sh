#!/usr/bin/env bash
# RWS DICOM - workstation manager for macOS / Linux
# RWS (Radiology Workflow System) - developed by Patrick Mulcahy, Mulcahy.io <dev@mulcahy.io>
#
#   ./rws-dicom.sh install | upgrade | verify | status | logs | start | stop | restart | backup | uninstall [--remove-data]
set -euo pipefail
cd "$(dirname "$0")"
CONTAINER=rws-dicom
CMD=${1:-install}

ok()   { printf '  \033[32m[ OK ]\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m[WARN]\033[0m %s\n' "$*"; }
fail() { printf '  \033[31m[FAIL]\033[0m %s\n' "$*"; exit 1; }

envval() { grep -E "^\s*$1\s*=" .env 2>/dev/null | tail -1 | cut -d= -f2- | sed 's/^ *//;s/ *$//'; }
profiles() { [ "$(envval RWS_EVENTS_DRIVER)" = nats ] && echo "--profile events" || true; }
admin() { local t; t=$(envval RWS_ADMIN_TOKEN); curl -fsS ${t:+-H "Authorization: Bearer $t"} "http://127.0.0.1:$(envval RWS_ADMIN_PORT)$1"; }
compose() { docker compose -f docker-compose.yml $(profiles) "$@"; }

check_docker() {
  command -v docker >/dev/null || fail "Docker is not installed (https://docs.docker.com/get-docker/)."
  docker info >/dev/null 2>&1 || fail "Docker is not running (or this user can't reach it)."
  docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is missing."
  ok "Docker is running"
}

init_config() {
  [ -f .env ] || { cp .env.example .env; ok "Created .env from .env.example"; }
  local ae; ae=$(envval RWS_DICOM_AE_TITLE)
  [[ ${#ae} -ge 1 && ${#ae} -le 16 && $ae =~ ^[A-Za-z0-9_.\ -]+$ ]] || fail "RWS_DICOM_AE_TITLE '$ae' is invalid (1-16 chars)."
  local data; data=$(envval RWS_DATA)
  if [ -n "$data" ]; then
    mkdir -p "$data"
    # The container runs as uid 65532 (distroless nonroot).
    if [ "$(uname)" = Linux ] && [ "$(stat -c %u "$data")" != 65532 ]; then
      warn "Setting owner of $data to 65532 (container user) - may ask for sudo"
      sudo chown -R 65532:65532 "$data"
    fi
  fi
  ok "Settings valid (AE $ae, DICOM port $(envval RWS_DICOM_PORT))"
}

wait_healthy() {
  echo "  Waiting for the module to report healthy..."
  for _ in $(seq 1 40); do
    s=$(docker inspect -f '{{.State.Health.Status}}' $CONTAINER 2>/dev/null || true)
    [ "$s" = healthy ] && { ok "Container healthy"; return; }
    [ "$s" = unhealthy ] && break
    sleep 3
  done
  docker logs --tail 40 $CONTAINER; fail "Module did not become healthy (log above)."
}

verify() {
  wait_healthy
  admin /version && echo && ok "Admin endpoint answering" || fail "Admin endpoint not reachable"
  (exec 3<>"/dev/tcp/127.0.0.1/$(envval RWS_DICOM_PORT)") 2>/dev/null && ok "DICOM port $(envval RWS_DICOM_PORT) reachable" || fail "DICOM port not reachable"
  docker exec $CONTAINER /rws-dicom -echo 127.0.0.1:11112 && ok "DICOM C-ECHO succeeded" || fail "DICOM C-ECHO failed"
}

summary() {
  echo; echo "  RWS DICOM is running"
  echo "  AE title   : $(envval RWS_DICOM_AE_TITLE)"
  echo "  DICOM port : $(envval RWS_DICOM_PORT)"
  local data; data=$(envval RWS_DATA || true)
  echo "  Data       : ${data:-Docker volume rws-dicom-data}"
  echo "  Health     : http://127.0.0.1:$(envval RWS_ADMIN_PORT)/readyz"; echo
}

echo; echo "  RWS DICOM - $CMD   (Mulcahy.io)"
case "$CMD" in
  install) check_docker; init_config; compose up -d --build; verify; summary ;;
  upgrade)
    check_docker; init_config; compose build --pull; compose stop dicom
    d=backups/index-$(date +%Y%m%d-%H%M%S); mkdir -p "$d"
    docker cp $CONTAINER:/var/lib/rws/index.db "$d/" 2>/dev/null && ok "Index backed up to $d" || warn "No index to back up"
    compose up -d; verify; summary ;;
  verify)  check_docker; verify; summary ;;
  status)  compose ps; admin /version || warn "Admin endpoint not answering"; echo ;;
  logs)    compose logs -f --tail 200 dicom ;;
  start)   compose up -d; verify ;;
  stop)    compose stop ;;
  restart) compose restart dicom; verify ;;
  backup)
    # Snapshot the index first (consistent while running), then copy images.
    # Images are write-once, so every indexed image is in the copy.
    d=backups/$(date +%Y%m%d-%H%M%S); mkdir -p "$d"
    docker exec $CONTAINER /rws-dicom -backup /var/lib/rws/backup/index.db >/dev/null || fail "Index snapshot failed"
    docker cp $CONTAINER:/var/lib/rws/backup/index.db "$d/index.db" || fail "Copying the index snapshot failed"
    docker cp $CONTAINER:/var/lib/rws/blobs "$d/blobs" || fail "Copying images failed"
    ok "Backed up to $d (index snapshot + images)" ;;
  uninstall)
    if [ "${2:-}" = --remove-data ]; then
      read -rp "  This permanently deletes all stored images and the index. Type DELETE to confirm: " c
      [ "$c" = DELETE ] || fail "Cancelled"
      docker compose -f docker-compose.yml --profile events down --rmi all -v
      if [ -n "$(envval RWS_DATA)" ]; then warn "Bind-mounted folder $(envval RWS_DATA) was NOT deleted."; fi
    else
      docker compose -f docker-compose.yml --profile events down --rmi all
      ok "Removed containers and image. Data is kept."
    fi ;;
  *) fail "unknown command $CMD" ;;
esac
