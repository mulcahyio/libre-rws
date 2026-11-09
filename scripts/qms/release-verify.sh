#!/usr/bin/env bash
# Release verification for LibreDCM (docs/qms/release-checklist.md).
# Usage: scripts/qms/release-verify.sh VERSION [OUTDIR]
# Runs against the current checkout (needs Docker, the rws-gocache volume and
# the rws-e2e-dcmtk image); logs to OUTDIR. Copy the logs into
# docs/releases/v<VERSION>-verification/ for the release record.
set -uo pipefail
REPO=$(cd "$(dirname "$0")/../.." && pwd)
OUT=${2:-${TMPDIR:-/tmp}/libredcm-release-verify}
VER=${1:?usage: release-verify.sh VERSION [OUTDIR]}
DATE=$(date +%F)
mkdir -p "$OUT"; cd "$REPO"
GO="docker run --rm -u $(id -u):$(id -g) -e HOME=/tmp -e GOCACHE=/cache/build -e GOMODCACHE=/cache/mod -e GOFLAGS=-mod=mod -v rws-gocache:/cache -v $REPO:/src -w /src --network host"
IMG=golang:1.26.10-bookworm
step() { echo "== $1" | tee -a "$OUT/summary.txt"; }
res()  { echo "   -> $1" | tee -a "$OUT/summary.txt"; }
: > "$OUT/summary.txt"
echo "commit $(git rev-parse HEAD)  branch $(git branch --show-current)  date $(date -Is)" | tee -a "$OUT/summary.txt"
$GO $IMG go version | tee -a "$OUT/summary.txt"

step "go vet (default + official tags)"
$GO $IMG go vet ./... > "$OUT/vet.log" 2>&1 && $GO $IMG go vet -tags official ./modules/... >> "$OUT/vet.log" 2>&1 && res PASS || res FAIL

step "go test -race (all packages)"
$GO $IMG go test -race -count=1 ./... > "$OUT/test-race.log" 2>&1 && res "PASS ($(grep -c '^ok' "$OUT/test-race.log") packages ok)" || res FAIL

step "go test -tags official"
$GO $IMG go test -count=1 -tags official ./modules/... > "$OUT/test-official.log" 2>&1 && res PASS || res FAIL

step "PostgreSQL index suites"
docker rm -f rel-pg >/dev/null 2>&1
docker run -d --rm --name rel-pg -e POSTGRES_PASSWORD=t -e POSTGRES_DB=rws -p 127.0.0.1:55433:5432 postgres:16-alpine >/dev/null
for i in $(seq 1 30); do docker exec rel-pg pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done; sleep 2
$GO -e LIBRE_DCM_TEST_POSTGRES_DSN="postgres://postgres:t@127.0.0.1:55433/rws?sslmode=disable" $IMG go test -p 1 -count=1 ./modules/... ./platform/... > "$OUT/test-postgres.log" 2>&1 && res PASS || res FAIL
docker rm -f rel-pg >/dev/null

step "S3 suites (SeaweedFS)"
docker rm -f rel-s3 >/dev/null 2>&1
printf '{"identities":[{"name":"t","credentials":[{"accessKey":"rwstest","secretKey":"rwstest123"}],"actions":["Admin","Read","Write","List"]}]}' > "$OUT/s3.json"
docker run -d --rm --name rel-s3 -p 127.0.0.1:58334:8333 -v "$OUT/s3.json":/etc/s3.json:ro chrislusf/seaweedfs:latest server -s3 -s3.config=/etc/s3.json -dir=/data >/dev/null
for i in $(seq 1 40); do curl -s -o /dev/null 127.0.0.1:58334 && break; sleep 1; done; sleep 3
$GO -e LIBRE_DCM_TEST_S3_ENDPOINT=127.0.0.1:58334 -e LIBRE_DCM_TEST_S3_ACCESS_KEY=rwstest -e LIBRE_DCM_TEST_S3_SECRET_KEY=rwstest123 $IMG go test -count=1 ./modules/dicom/internal/blob/ ./modules/dicom/internal/gateway/ > "$OUT/test-s3.log" 2>&1 && res PASS || res FAIL
docker rm -f rel-s3 >/dev/null

step "govulncheck"
$GO $IMG go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./... > "$OUT/govulncheck.log" 2>&1; rc=$?
res "exit $rc: $(grep -E 'No vulnerabilities found|Your code is affected' "$OUT/govulncheck.log" | tr '\n' ' ')"

step "SBOM"
$GO $IMG go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.12.0 mod -licenses -json -output docs/dhf/libredcm/generated/sbom.cdx.json . > "$OUT/sbom.log" 2>&1 && res "PASS ($(python3 -c "import json;print(len(json.load(open('docs/dhf/libredcm/generated/sbom.cdx.json'))['components']))") components)" || res FAIL

step "image build (source build)"
docker build -q -f deploy/docker/Dockerfile.dicom --build-arg VERSION=$VER --build-arg BUILD_DATE=$DATE -t libre/dcm:$VER . > "$OUT/build.log" 2>&1 && res "PASS image id $(docker inspect -f '{{.Id}}' libre/dcm:$VER)" || res FAIL
step "image build (official build)"
docker build -q -f deploy/docker/Dockerfile.dicom --build-arg VERSION=$VER --build-arg BUILD_DATE=$DATE --build-arg BUILD_TAGS=official -t libre/dcm:$VER-official . > "$OUT/build-official.log" 2>&1 && res "PASS image id $(docker inspect -f '{{.Id}}' libre/dcm:$VER-official)" || res FAIL

# ---- system tests on a Docker network ----
NET=rel-net; docker network rm $NET >/dev/null 2>&1; docker network create $NET >/dev/null
SAMPLES='CT_small.dcm MR_small_RLE.dcm MR_small_jp2klossless.dcm JPEG2000.dcm MR_small_jpeg_ls_lossless.dcm SC_rgb_jpeg_dcmtk.dcm'
conf() { # $1 = gateway container
  docker run --rm --name dcmtk-runner --network $NET -v "$REPO/scripts":/scripts:ro --entrypoint bash rws-e2e-dcmtk -c \
    "set -e; D=\$(python3 -c 'import pydicom,os;print(os.path.join(os.path.dirname(pydicom.__file__),\"data\",\"test_files\"))'); mkdir -p /tmp/s; for f in $SAMPLES; do cp \$D/\$f /tmp/s/ 2>/dev/null || true; done; ls /tmp/s; bash /scripts/conformance.sh /tmp/s $1 11112 LIBREDCM"
}
start_gw() { # $1 name $2 image
  docker rm -f $1 >/dev/null 2>&1
  docker run -d --name $1 --network $NET -e LIBRE_DCM_REMOTE_AES=STORESCP=dcmtk-runner:11113 -e LIBRE_DCM_LOG_FORMAT=text $2 >/dev/null
  for i in $(seq 1 40); do [ "$(docker inspect -f '{{.State.Health.Status}}' $1 2>/dev/null)" = healthy ] && return 0; sleep 1; done; return 1
}

step "DCMTK conformance (source build container)"
start_gw rel-gw libre/dcm:$VER && res "container healthy" || res "container NOT healthy"
conf rel-gw > "$OUT/conformance-source.log" 2>&1 && res "PASS: $(grep -ciE 'pass|ok' "$OUT/conformance-source.log") pass lines" || res "FAIL (see conformance-source.log)"
docker run --rm --network $NET curlimages/curl:latest -s http://rel-gw:8081/version > "$OUT/version-source.json" 2>&1; res "/version: $(cat "$OUT/version-source.json")"
docker rm -f rel-gw >/dev/null

step "Official build, NO licence: DICOM must keep working (DCM-SRS-058..061)"
start_gw rel-off libre/dcm:$VER-official && res "container healthy without a licence" || res "container NOT healthy"
conf rel-off > "$OUT/conformance-official-unlicensed.log" 2>&1 && res "PASS: C-ECHO/STORE/FIND/MOVE all work while unlicensed" || res "FAIL (see conformance-official-unlicensed.log)"
docker run --rm --network $NET curlimages/curl:latest -s http://rel-off:8081/version > "$OUT/version-official.json" 2>&1; res "/version: $(cat "$OUT/version-official.json")"
docker logs rel-off > "$OUT/official.log" 2>&1
res "unlicensed error lines: $(grep -c 'LibreDCM is unlicensed' "$OUT/official.log")"
docker kill -s HUP rel-off >/dev/null; sleep 2; docker logs rel-off > "$OUT/official.log" 2>&1
res "reload blocked lines after SIGHUP: $(grep -c 'config reload blocked' "$OUT/official.log")"
docker exec rel-off /LibreDCM -license-ack "Hermes release test" > "$OUT/ack.txt" 2>&1; res "ack: $(cat "$OUT/ack.txt")"
sleep 65; docker logs rel-off > "$OUT/official.log" 2>&1
res "audit license_acknowledged lines: $(grep -c 'event=license_acknowledged' "$OUT/official.log")"
docker kill -s HUP rel-off >/dev/null; sleep 2; docker logs rel-off > "$OUT/official.log" 2>&1
res "configuration reloaded lines after ack: $(grep -c 'configuration reloaded' "$OUT/official.log")"
docker run --rm --network $NET curlimages/curl:latest -s http://rel-off:8081/version > "$OUT/version-official-acked.json" 2>&1; res "/version after ack: $(cat "$OUT/version-official-acked.json")"
docker rm -f rel-off >/dev/null; docker network rm $NET >/dev/null
step "done"
