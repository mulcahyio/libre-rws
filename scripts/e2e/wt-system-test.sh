#!/usr/bin/env bash
# Libre WT system test: real LibreDCM containers (source and official
# build), Libre WT and DCMTK on one Docker network.
#
#   scripts/e2e/wt-system-test.sh OUTDIR [IMAGE_TAG]
#
# Expects the images libre/dcm:TAG, libre/dcm:TAG-official and
# libre/wt:TAG (default TAG wt-system) and rws-e2e-dcmtk. Writes the run
# log, the pages, the comparison of sent and received instances and the
# container logs to OUTDIR. Set WT_SYS_KEEP=1 to leave the stack running
# (for screenshots); otherwise it is removed with its volumes at the end.
# Only synthetic test data is used.
set -uo pipefail
REPO=$(cd "$(dirname "$0")/../.." && pwd)
OUT=${1:?usage: wt-system-test.sh OUTDIR [IMAGE_TAG]}
export WT_SYS_TAG=${2:-wt-system}
export WT_SYS_PORT=${WT_SYS_PORT:-18090}
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
RUN=$(mktemp -d "${TMPDIR:-/tmp}/wtsys.XXXXXX")
export WT_SYS_RUN=$RUN
COMPOSE=(docker compose -p wtsys -f "$REPO/deploy/compose/docker-compose.wt-system.yml")

# Random admin tokens, one per node. Mode 0644: the distroless containers
# run as uid 65532 and read them through a read-only bind mount.
for n in dcm-1 dcm-2; do
  head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$RUN/$n.token"
  chmod 0644 "$RUN/$n.token"
done
cat > "$RUN/librewt.yaml" <<'YAML'
session_idle_timeout: 30m
status_cache: 1s
instances:
  - name: dcm-1
    cluster: wtsys
    url: http://dcm-1:8081
    token_file: /run/wtsys/dcm-1.token
  - name: dcm-2
    cluster: wtsys
    url: http://dcm-2:8081
    token_file: /run/wtsys/dcm-2.token
YAML
chmod 0644 "$RUN/librewt.yaml"

{
  echo "Libre WT system test, $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "git: $(git -C "$REPO" rev-parse HEAD) ($(git -C "$REPO" status --porcelain -- modules platform deploy third_party go.mod go.sum | wc -l) changed product files in the working tree)"
  for i in "libre/dcm:$WT_SYS_TAG" "libre/dcm:$WT_SYS_TAG-official" "libre/wt:$WT_SYS_TAG" rws-e2e-dcmtk; do
    echo "image $i $(docker image inspect -f '{{.Id}} created {{.Created}}' "$i")"
  done
} > "$OUT/environment.txt"

"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1
"${COMPOSE[@]}" up -d 2>&1 | tail -3
python3 "$REPO/scripts/e2e/wt-system-test.py" "http://127.0.0.1:$WT_SYS_PORT" "$RUN" "$OUT" 2>&1 | tee "$OUT/system-test.log"
rc=${PIPESTATUS[0]}
"${COMPOSE[@]}" logs --no-color dcm-1 > "$OUT/libredcm-dcm-1.log" 2>&1
"${COMPOSE[@]}" logs --no-color dcm-2 > "$OUT/libredcm-dcm-2.log" 2>&1
"${COMPOSE[@]}" logs --no-color dcmtk > "$OUT/storescp.log" 2>&1
# The token values never go into the record; check the saved logs too.
for n in dcm-1 dcm-2; do
  if grep -rqF "$(cat "$RUN/$n.token")" "$OUT"; then echo "FAIL token of $n found in $OUT"; rc=1; fi
done
if [ "${WT_SYS_KEEP:-0}" != 1 ]; then
  "${COMPOSE[@]}" down -v >/dev/null 2>&1
fi
echo "exit=$rc" | tee -a "$OUT/system-test.log"
exit "$rc"
