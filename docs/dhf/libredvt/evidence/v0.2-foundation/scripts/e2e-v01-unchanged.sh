#!/bin/bash
# LibreDVT v0.2 card DVT-0.2/2: v0.1 behaviour unchanged.
#  1. Real-engine positive controls (Dockerfile.dvt test stage, as CI job
#     `libredvt` and release-verify.sh run them).
#  2. Image build and offline validation (CI job steps).
#  3. The bundled LibreDCM plan, unchanged, against a fresh LibreDCM
#     container: once with the parent branch's image (v0.1 code), once with
#     this branch's image (ad-hoc -host target), once with this branch's
#     image and a v0.2 target file (-targets). Step verdicts and messages
#     are compared.
#
#   bash docs/dhf/libredvt/evidence/v0.2-foundation/scripts/e2e-v01-unchanged.sh   # from the repository root
# Needs: libre/dvt:v02-base (built from feat/libredvt-0.2-design) and
# libre/dcm:v02-foundation (LibreDCM, Dockerfile.dicom; modules/dicom is
# identical on both branches).
cd "$(git rev-parse --show-toplevel)" || exit 1
E=docs/dhf/libredvt/evidence/v0.2-foundation
OUT=$E/libredcm-plan
BASE_IMG=${BASE_IMG:-libre/dvt:v02-base}
DCM_IMG=${DCM_IMG:-libre/dcm:v02-foundation}
NEW_IMG=libre/dvt:v02-foundation
NET=dvt02-net
mkdir -p "$OUT"; chmod 777 "$OUT"
echo "commit $(git rev-parse --short HEAD), $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "base image $BASE_IMG $(docker inspect -f '{{.Id}}' "$BASE_IMG"), LibreDCM $DCM_IMG $(docker inspect -f '{{.Id}}' "$DCM_IMG")"

echo "== 1. engine positive controls (Dockerfile.dvt --target test)"
docker build --progress plain --no-cache-filter test -f deploy/docker/Dockerfile.dvt --target test . > "$E/02-engine-tests.log" 2>&1
rc=$?
echo "exit $rc; 'ok' lines: $(grep -cE '^#[0-9]+ [0-9.]+ ok ' "$E/02-engine-tests.log"); FAIL lines: $(grep -cE -- '--- FAIL|^#[0-9]+ [0-9.]+ FAIL' "$E/02-engine-tests.log")"
grep -E '^#[0-9]+ [0-9.]+ (ok|FAIL|---)' "$E/02-engine-tests.log" | sed 's/^#[0-9]* [0-9.]* //'

echo "== 2. image build and offline validation"
docker build -q -f deploy/docker/Dockerfile.dvt --build-arg VERSION=0.2.0-dev -t $NEW_IMG . && echo "image $(docker inspect -f '{{.Id}}' $NEW_IMG)"
docker run --rm --network none $NEW_IMG validate /opt/libredvt/plans/fixtures/ct_explicit_le.dcm; echo "validate exit $?"

echo "== 3. bundled LibreDCM plan"
docker network rm $NET >/dev/null 2>&1; docker network create $NET >/dev/null
start_gw() {
  docker rm -f dvt02-gw >/dev/null 2>&1
  docker run -d --name dvt02-gw --network $NET -e LIBRE_DCM_REMOTE_AES=LIBREDVT=dvt-runner:11115 -e LIBRE_DCM_LOG_FORMAT=text "$DCM_IMG" >/dev/null
  for _ in $(seq 1 40); do [ "$(docker inspect -f '{{.State.Health.Status}}' dvt02-gw 2>/dev/null)" = healthy ] && return 0; sleep 1; done
  echo "LibreDCM not healthy"; return 1
}
printf 'targets:\n  - {name: libredcm-lab, class: lab, protocol: dimse, host: dvt02-gw, port: 11112, called_aet: LIBREDCM}\n' > "$OUT/targets.yaml"
chmod 644 "$OUT/targets.yaml"
run_plan() { # name image extra-args...
  local name=$1 img=$2; shift 2
  start_gw || return 1
  docker run --rm --name dvt-runner --network $NET -v "$PWD/$OUT":/out "$img" run "$@" \
    -junit /out/$name-junit.xml -html /out/$name-report.html -json /out/$name-run.json /opt/libredvt/plans/libredcm-conformance.yaml > "$OUT/$name.log" 2>&1
  echo "$name: exit $? :: $(tail -1 "$OUT/$name.log")"
}
run_plan v01-base "$BASE_IMG" -host dvt02-gw -port 11112 -called-aet LIBREDCM -lab
run_plan v02-adhoc $NEW_IMG -host dvt02-gw -port 11112 -called-aet LIBREDCM -lab
run_plan v02-targets $NEW_IMG -targets /out/targets.yaml
docker rm -f dvt02-gw >/dev/null 2>&1; docker network rm $NET >/dev/null 2>&1
python3 $E/scripts/compare-runs.py "$OUT/v01-base-run.json" "$OUT/v02-adhoc-run.json" "$OUT/v02-targets-run.json"
echo "compare exit $?"
