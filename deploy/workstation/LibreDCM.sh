#!/usr/bin/env bash
# LibreDCM - workstation manager for macOS / Linux
# Libre RWS (Radiology Workflow System) - developed by Patrick Mulcahy, Mulcahy.io <dev@mulcahy.io>
#
#   ./LibreDCM.sh install | configure | upgrade | verify | status | logs | start | stop | restart | backup | uninstall [--remove-data]
#
# install asks the install-time questions (storage, TLS, patient IDs) the first
# time; `configure` asks them again. Set LIBREDCM_YES=1 to take the values in
# .env without asking (unattended installs).
set -euo pipefail
cd "$(dirname "$0")"
CONTAINER=libredcm
CMD=${1:-install}
DATA_DIR=/var/lib/libredcm

ok()   { printf '  \033[32m[ OK ]\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m[WARN]\033[0m %s\n' "$*"; }
info() { printf '  %s\n' "$*"; }
fail() { printf '  \033[31m[FAIL]\033[0m %s\n' "$*"; exit 1; }

envval() { grep -E "^\s*$1\s*=" .env 2>/dev/null | tail -1 | cut -d= -f2- | sed 's/^ *//;s/ *$//' || true; }
setenv() { # setenv KEY VALUE: replace or append in .env
  local k=$1 v=$2 tmp
  tmp=$(mktemp)
  if grep -qE "^\s*$k\s*=" .env; then
    awk -v k="$k" -v v="$v" 'BEGIN{FS=OFS="="} $0 ~ "^[ \t]*"k"[ \t]*=" {print k"="v; next} {print}' .env > "$tmp"
  else
    cat .env > "$tmp"; printf '%s=%s\n' "$k" "$v" >> "$tmp"
  fi
  cat "$tmp" > .env; rm -f "$tmp"
}
tls_on() { [ "$(envval LIBRE_DCM_TLS_MODE)" = self-signed ] || [ "$(envval LIBRE_DCM_TLS_MODE)" = files ]; }
profiles() { [ "$(envval LIBRE_DCM_EVENTS_DRIVER)" = nats ] && echo "--profile events" || true; }
admin() {
  local t scheme=http k=""; t=$(envval LIBRE_DCM_ADMIN_TOKEN)
  if tls_on; then scheme=https; k=-k; fi   # self-signed or private CA: this only talks to localhost
  curl -fsS $k ${t:+-H "Authorization: Bearer $t"} "$scheme://127.0.0.1:$(envval LIBRE_DCM_ADMIN_PORT)$1"
}
compose() { docker compose -f docker-compose.yml $(profiles) "$@"; }
ask() { # ask "Question" default -> answer on stdout
  local a; read -rp "  $1 [$2]: " a </dev/tty; echo "${a:-$2}"
}
interactive() { [ -t 0 ] && [ "${LIBREDCM_YES:-}" != 1 ]; }

check_docker() {
  command -v docker >/dev/null || fail "Docker is not installed (https://docs.docker.com/get-docker/)."
  docker info >/dev/null 2>&1 || fail "Docker is not running (or this user can't reach it)."
  docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is missing."
  ok "Docker is running"
}

# migrate_legacy moves a 0.1.x (rws-dicom) install to the LibreDCM names
# without touching stored images or the index.
migrate_legacy() {
  # Only an install directory whose .env still has RWS_* settings is migrated,
  # so a fresh LibreDCM install never touches another copy on the same PC.
  grep -qE '^\s*RWS_' .env || return 0
  local bak; bak=.env.bak-$(date +%Y%m%d-%H%M%S); cp .env "$bak"
  sed -i.tmp -E 's/^(\s*)RWS_DICOM_PORT=/\1LIBRE_DCM_PORT=/; s/^(\s*)RWS_DICOM_/\1LIBRE_DCM_/; s/^(\s*)RWS_/\1LIBRE_DCM_/' .env && rm -f .env.tmp
  ok "Renamed RWS_* settings to LIBRE_DCM_* (old file kept as $bak)"
  for old in rws-dicom rws-dicom-nats; do
    if docker container inspect "$old" >/dev/null 2>&1; then
      docker rm -f "$old" >/dev/null && ok "Removed old container $old (its data volume is kept)"
    fi
  done
  if [ -z "$(envval LIBRE_DCM_VOLUME)" ] && docker volume inspect rws-dicom-data >/dev/null 2>&1; then
    setenv LIBRE_DCM_VOLUME rws-dicom-data; ok "Keeping your existing data volume rws-dicom-data"
  fi
  if [ -z "$(envval LIBRE_DCM_NATS_VOLUME)" ] && docker volume inspect rws-dicom-nats >/dev/null 2>&1; then
    setenv LIBRE_DCM_NATS_VOLUME rws-dicom-nats
  fi
}

# fill_missing adds settings that are new in this version, and moves the
# image version to the one this package ships.
fill_missing() {
  local line k
  while IFS= read -r line; do
    [[ $line =~ ^([A-Z_][A-Z0-9_]*)= ]] || continue
    k=${BASH_REMATCH[1]}
    grep -qE "^\s*$k\s*=" .env || printf '%s\n' "$line" >> .env
  done < .env.example
  setenv LIBRE_DCM_VERSION "$(grep -E '^LIBRE_DCM_VERSION=' .env.example | cut -d= -f2-)"
}

configure() {
  echo; info "Install-time settings (Enter keeps the value in brackets)"
  setenv LIBRE_DCM_AE_TITLE "$(ask 'AE title of this node' "$(envval LIBRE_DCM_AE_TITLE)")"

  echo; info "Where should images be stored?"
  info "  1) This PC, Docker volume (recommended)   2) This PC, a folder I choose   3) S3 / S3-compatible store"
  local cur=1; [ -n "$(envval LIBRE_DCM_DATA)" ] && cur=2; [ "$(envval LIBRE_DCM_STORAGE_DRIVER)" = s3 ] && cur=3
  case "$(ask 'Storage' $cur)" in
    1) setenv LIBRE_DCM_STORAGE_DRIVER fs; setenv LIBRE_DCM_DATA "" ;;
    2) setenv LIBRE_DCM_STORAGE_DRIVER fs; setenv LIBRE_DCM_DATA "$(ask 'Local folder' "$(envval LIBRE_DCM_DATA)")" ;;
    3) setenv LIBRE_DCM_STORAGE_DRIVER s3
       setenv LIBRE_DCM_STORAGE_S3_ENDPOINT "$(ask 'S3 endpoint (host[:port])' "$(envval LIBRE_DCM_STORAGE_S3_ENDPOINT)")"
       setenv LIBRE_DCM_STORAGE_S3_BUCKET "$(ask 'Bucket' "$(envval LIBRE_DCM_STORAGE_S3_BUCKET)")"
       setenv LIBRE_DCM_STORAGE_S3_REGION "$(ask 'Region (blank for MinIO/NAS)' "$(envval LIBRE_DCM_STORAGE_S3_REGION)")"
       setenv LIBRE_DCM_STORAGE_S3_ACCESS_KEY "$(ask 'Access key' "$(envval LIBRE_DCM_STORAGE_S3_ACCESS_KEY)")"
       local sk; read -rsp "  Secret key [keep current]: " sk </dev/tty; echo
       [ -n "$sk" ] && setenv LIBRE_DCM_STORAGE_S3_SECRET_KEY "$sk"
       setenv LIBRE_DCM_STORAGE_S3_CREATE_BUCKET "$(ask 'Create the bucket if missing (true/false)' "$(envval LIBRE_DCM_STORAGE_S3_CREATE_BUCKET)")" ;;
    *) fail "Choose 1, 2 or 3" ;;
  esac

  echo; info "TLS for DICOM and the admin endpoint:"
  info "  1) Off   2) Self-signed certificate (created now)   3) My own certificate files"
  cur=1; [ "$(envval LIBRE_DCM_TLS_MODE)" = self-signed ] && cur=2; [ "$(envval LIBRE_DCM_TLS_MODE)" = files ] && cur=3
  case "$(ask 'TLS' $cur)" in
    1) setenv LIBRE_DCM_TLS_MODE off ;;
    2) setenv LIBRE_DCM_TLS_MODE self-signed
       setenv LIBRE_DCM_TLS_HOSTS "$(ask 'Extra DNS names / IPs for the certificate (comma-separated)' "$(envval LIBRE_DCM_TLS_HOSTS)")" ;;
    3) setenv LIBRE_DCM_TLS_MODE files
       local crt key; crt=$(ask 'Certificate file (PEM, full chain)' "")
       key=$(ask 'Private key file (PEM)' "")
       [ -f "$crt" ] && [ -f "$key" ] || fail "Certificate or key file not found"
       mkdir -p tls; cp "$crt" tls/tls.crt; cp "$key" tls/tls.key; chmod 600 tls/tls.key
       setenv LIBRE_DCM_TLS_DIR ./tls; ok "Copied certificate and key into ./tls" ;;
    *) fail "Choose 1, 2 or 3" ;;
  esac

  echo
  local yn
  yn=$(ask 'Include patient IDs in event-bus messages? (y/n)' "$([ "$(envval LIBRE_DCM_EVENTS_OMIT_PATIENT_ID)" = true ] && echo n || echo y)")
  [[ $yn =~ ^[Nn] ]] && setenv LIBRE_DCM_EVENTS_OMIT_PATIENT_ID true || setenv LIBRE_DCM_EVENTS_OMIT_PATIENT_ID false
  yn=$(ask 'Include patient IDs in the audit log? (y/n)' "$([ "$(envval LIBRE_DCM_AUDIT_OMIT_PATIENT_ID)" = true ] && echo n || echo y)")
  [[ $yn =~ ^[Nn] ]] && setenv LIBRE_DCM_AUDIT_OMIT_PATIENT_ID true || setenv LIBRE_DCM_AUDIT_OMIT_PATIENT_ID false
  setenv LIBRE_DCM_CONFIGURED 1
}

init_config() {
  local fresh=0
  [ -f .env ] || { cp .env.example .env; ok "Created .env from .env.example"; fresh=1; }
  migrate_legacy
  fill_missing
  if [ "$CMD" = configure ] || { [ "$(envval LIBRE_DCM_CONFIGURED)" != 1 ] && interactive; }; then
    configure
  elif [ $fresh = 1 ]; then
    info "Unattended install: using the settings in .env"
  fi
  [ -n "$(envval LIBRE_DCM_ADMIN_TOKEN)" ] || { setenv LIBRE_DCM_ADMIN_TOKEN "$(od -An -tx1 -N24 /dev/urandom | tr -d ' \n')"; ok "Generated an admin token (in .env)"; }

  local ae; ae=$(envval LIBRE_DCM_AE_TITLE)
  [[ ${#ae} -ge 1 && ${#ae} -le 16 && $ae =~ ^[A-Za-z0-9_.\ -]+$ ]] || fail "LIBRE_DCM_AE_TITLE '$ae' is invalid (1-16 chars)."
  local data; data=$(envval LIBRE_DCM_DATA)
  if [ -n "$data" ]; then
    mkdir -p "$data"
    # The container runs as uid 65532 (distroless nonroot).
    if [ "$(uname)" = Linux ] && [ "$(stat -c %u "$data")" != 65532 ]; then
      warn "Setting owner of $data to 65532 (container user) - may ask for sudo"
      sudo chown -R 65532:65532 "$data"
    fi
  fi
  if [ "$(envval LIBRE_DCM_STORAGE_DRIVER)" = s3 ]; then
    [ -n "$(envval LIBRE_DCM_STORAGE_S3_ENDPOINT)" ] && [ -n "$(envval LIBRE_DCM_STORAGE_S3_BUCKET)" ] \
      || fail "S3 storage needs LIBRE_DCM_STORAGE_S3_ENDPOINT and LIBRE_DCM_STORAGE_S3_BUCKET"
  fi
  apply_tls_settings
  ok "Settings valid (AE $ae, DICOM port $(envval LIBRE_DCM_PORT), storage $(envval LIBRE_DCM_STORAGE_DRIVER), TLS $(envval LIBRE_DCM_TLS_MODE))"
}

# apply_tls_settings turns LIBRE_DCM_TLS_MODE into the certificate paths the
# module reads (inside the container).
apply_tls_settings() {
  local crt="" key="" dir
  case "$(envval LIBRE_DCM_TLS_MODE)" in
    off|"") ;;
    self-signed) crt=$DATA_DIR/tls/tls.crt; key=$DATA_DIR/tls/tls.key ;;
    files)
      dir=$(envval LIBRE_DCM_TLS_DIR); dir=${dir:-./tls}
      [ -f "$dir/tls.crt" ] && [ -f "$dir/tls.key" ] || fail "TLS mode 'files' needs tls.crt and tls.key in $dir"
      if [ "$(uname)" = Linux ] && [ "$(stat -c %u "$dir/tls.key")" != 65532 ]; then
        warn "Letting the container user (65532) read $dir/tls.key - may ask for sudo"
        sudo chown 65532 "$dir/tls.key"
      fi
      crt=/etc/libredcm/tls/tls.crt; key=/etc/libredcm/tls/tls.key ;;
    *) fail "LIBRE_DCM_TLS_MODE must be off, self-signed or files" ;;
  esac
  mkdir -p tls
  setenv LIBRE_DCM_TLS_CERT "$crt"; setenv LIBRE_DCM_TLS_KEY "$key"
  setenv LIBRE_DCM_ADMIN_TLS_CERT "$crt"; setenv LIBRE_DCM_ADMIN_TLS_KEY "$key"
}

# make_cert creates the self-signed certificate in the data volume (kept on
# later runs, so peers that trusted it keep working).
make_cert() {
  [ "$(envval LIBRE_DCM_TLS_MODE)" = self-signed ] || return 0
  compose run --rm --no-deps -T dicom -gen-cert $DATA_DIR/tls -gen-cert-hosts "$(envval LIBRE_DCM_TLS_HOSTS)" \
    || fail "Creating the self-signed certificate failed"
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
  admin /version && echo && ok "Admin endpoint answering$(tls_on && echo ' (HTTPS)')" || fail "Admin endpoint not reachable"
  (exec 3<>"/dev/tcp/127.0.0.1/$(envval LIBRE_DCM_PORT)") 2>/dev/null && ok "DICOM port $(envval LIBRE_DCM_PORT) reachable" || fail "DICOM port not reachable"
  docker exec $CONTAINER /LibreDCM -echo 127.0.0.1:11112 -echo-ae "$(envval LIBRE_DCM_AE_TITLE)" \
    && ok "DICOM C-ECHO succeeded$(tls_on && echo ' (TLS)')" || fail "DICOM C-ECHO failed"
}

summary() {
  local data storage vol; data=$(envval LIBRE_DCM_DATA); vol=$(envval LIBRE_DCM_VOLUME)
  storage="this PC (${data:-Docker volume ${vol:-libredcm-data}})"
  [ "$(envval LIBRE_DCM_STORAGE_DRIVER)" = s3 ] && storage="S3 $(envval LIBRE_DCM_STORAGE_S3_ENDPOINT)/$(envval LIBRE_DCM_STORAGE_S3_BUCKET) (index on this PC)"
  echo; echo "  LibreDCM is running"
  echo "  AE title   : $(envval LIBRE_DCM_AE_TITLE)"
  echo "  DICOM port : $(envval LIBRE_DCM_PORT)$(tls_on && echo ' (DICOM TLS)')"
  echo "  Storage    : $storage"
  echo "  TLS        : $(envval LIBRE_DCM_TLS_MODE)"
  echo "  Health     : $(tls_on && echo https || echo http)://127.0.0.1:$(envval LIBRE_DCM_ADMIN_PORT)/readyz"; echo
}

backup() {
  # Snapshot the index first (consistent while running), then copy images.
  # Images are write-once, so every indexed image is in the copy.
  local d; d=backups/$(date +%Y%m%d-%H%M%S); mkdir -p "$d"
  docker exec $CONTAINER /LibreDCM -backup $DATA_DIR/backup/index.db >/dev/null || fail "Index snapshot failed"
  docker cp $CONTAINER:$DATA_DIR/backup/index.db "$d/index.db" || fail "Copying the index snapshot failed"
  if [ "$(envval LIBRE_DCM_STORAGE_DRIVER)" = s3 ]; then
    ok "Backed up the index to $d (images are in S3: use the store's own versioning/replication)"
  else
    docker cp $CONTAINER:$DATA_DIR/blobs "$d/blobs" || fail "Copying images failed"
    ok "Backed up to $d (index snapshot + images)"
  fi
}

echo; echo "  LibreDCM - $CMD   (Mulcahy.io)"
case "$CMD" in
  install|configure)
    check_docker; init_config; compose build; make_cert; compose up -d; verify; summary ;;
  upgrade)
    check_docker; init_config; compose build --pull; make_cert
    if docker container inspect $CONTAINER >/dev/null 2>&1; then
      compose stop dicom
      d=backups/index-$(date +%Y%m%d-%H%M%S); mkdir -p "$d"
      docker cp $CONTAINER:$DATA_DIR/index.db "$d/" 2>/dev/null && ok "Index backed up to $d" || warn "No index to back up"
    fi
    compose up -d; verify; summary ;;
  verify)  check_docker; verify; summary ;;
  status)  compose ps; admin /version || warn "Admin endpoint not answering"; echo ;;
  logs)    compose logs -f --tail 200 dicom ;;
  start)   compose up -d; verify ;;
  stop)    compose stop ;;
  restart) compose restart dicom; verify ;;
  backup)  backup ;;
  uninstall)
    if [ "${2:-}" = --remove-data ]; then
      read -rp "  This permanently deletes all stored images and the index. Type DELETE to confirm: " c
      [ "$c" = DELETE ] || fail "Cancelled"
      docker compose -f docker-compose.yml --profile events down --rmi all -v
      if [ -n "$(envval LIBRE_DCM_DATA)" ]; then warn "Bind-mounted folder $(envval LIBRE_DCM_DATA) was NOT deleted."; fi
      if [ "$(envval LIBRE_DCM_STORAGE_DRIVER)" = s3 ]; then warn "Images in the S3 bucket were NOT deleted."; fi
    else
      docker compose -f docker-compose.yml --profile events down --rmi all
      ok "Removed containers and image. Data is kept."
    fi ;;
  *) fail "unknown command $CMD" ;;
esac
