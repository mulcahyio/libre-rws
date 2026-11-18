#!/usr/bin/env bash
# Evidence for the LibreDCM DICOM Conformance Statement: builds LibreDCM from
# the working tree, starts five containers with different settings, runs the
# DCMTK tools (dcmtk-runs.sh) and pynetdicom probes (pynetdicom-probes.py)
# against them and saves the logs next to this directory.
#
#   bash docs/dhf/libredcm/evidence/dcs-dcmtk/scripts/evidence.sh   # from the repository root
#
# Needs Docker and the images rws-e2e-dcmtk (DCMTK 3.6.7 + pydicom, see
# docs/qms/tool-validation/README.md) and libre/dvt:engines (pynetdicom).
# Scratch data (test files, certificate, copies of stored files) goes to
# $DCS_WORK (default: $TMPDIR/libredcm-dcs). No patient data: test files are
# pydicom's public samples with Libre test-data UIDs (gen-data.py).
set -u
REPO=$(pwd)
VER=0.2.0-dcs.1
IMG=libre/dcm:dcs
OUT="$REPO/docs/dhf/libredcm/evidence/dcs-dcmtk"
SCR="$OUT/scripts"
WORK=${DCS_WORK:-${TMPDIR:-/tmp}/libredcm-dcs}
NET=dcs-net
U="$(id -u):$(id -g)"
[ -f "$SCR/dcmtk-runs.sh" ] || { echo "run from the repository root" >&2; exit 2; }
mkdir -p "$WORK/data" "$WORK/certs" "$WORK/blobs"
chmod 777 "$WORK/certs"
rm -f "$OUT"/*.log "$OUT"/*.txt "$WORK"/data/*.dcm

{
echo "# LibreDCM conformance statement evidence ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
echo "git: $(git rev-parse HEAD) on $(git branch --show-current); modified tracked paths: $(git status --porcelain --untracked-files=no | wc -l)"
echo "docker: $(docker version -f '{{.Server.Version}}')"
docker build -q -f deploy/docker/Dockerfile.dicom --build-arg VERSION=$VER -t $IMG . >/dev/null && echo "image $IMG built with VERSION=$VER: $(docker inspect -f '{{.Id}}' $IMG)"
echo "LibreDCM -version: $(docker run --rm $IMG -version)"
echo "DCMTK image: rws-e2e-dcmtk $(docker inspect -f '{{.Id}}' rws-e2e-dcmtk)"
echo "pynetdicom image: libre/dvt:engines $(docker inspect -f '{{.Id}}' libre/dvt:engines)"
} > "$OUT/00-build.txt" 2>&1

# Synthetic data (pydicom samples with Libre test-data UIDs).
docker run --rm -u "$U" -v "$SCR":/scr:ro -v "$WORK/data":/data --entrypoint python3 rws-e2e-dcmtk /scr/gen-data.py > "$OUT/01-test-data.txt" 2>&1

# Certificate for the TLS node, made by LibreDCM itself.
docker run --rm -u 65532 -v "$WORK/certs":/certs $IMG -gen-cert /certs -gen-cert-hosts dcs-tls -gen-cert-force > "$OUT/02-gen-cert.txt" 2>&1

docker network create $NET >/dev/null 2>&1
start() { # start NAME ENV...
  local name=$1; shift
  local args=()
  for e in "$@"; do args+=(-e "$e"); done
  docker run -d --name "$name" --network $NET -e LIBRE_DCM_LOG_FORMAT=text "${args[@]}" \
    -v "$WORK/certs":/certs:ro $IMG >/dev/null
}
start dcs-gw LIBRE_DCM_REMOTE_AES=DCMTKMOVE=dcs-tools:11113
start dcs-allow LIBRE_DCM_ALLOWED_AES=DCMTKOK
start dcs-reject LIBRE_DCM_PATIENT_CONFLICT_POLICY=reject LIBRE_DCM_FIND_LIMIT=1
start dcs-coerce LIBRE_DCM_PATIENT_CONFLICT_COERCE_PATIENT_ID=true
start dcs-tls LIBRE_DCM_TLS_CERT=/certs/tls.crt LIBRE_DCM_TLS_KEY=/certs/tls.key
for c in dcs-gw dcs-allow dcs-reject dcs-coerce dcs-tls; do
  for i in $(seq 1 40); do [ "$(docker inspect -f '{{.State.Health.Status}}' $c 2>/dev/null)" = healthy ] && break; sleep 1; done
  echo "$c health: $(docker inspect -f '{{.State.Health.Status}}' $c)" >> "$OUT/00-build.txt"
done

docker run --rm --name dcs-tools --hostname dcs-tools --network $NET -u "$U" \
  -v "$SCR":/scr:ro -v "$WORK/data":/data:ro -v "$WORK/certs":/certs:ro -v "$OUT":/out \
  --entrypoint bash rws-e2e-dcmtk /scr/dcmtk-runs.sh > "$OUT/03-runs.txt" 2>&1

docker run --rm --network $NET -v "$SCR":/scr:ro --entrypoint python3 libre/dvt:engines /scr/pynetdicom-probes.py > "$OUT/60-pynetdicom-probes.log" 2>&1
echo "pynetdicom probes exit status: $?" >> "$OUT/60-pynetdicom-probes.log"

# Stored files: transfer syntax of what was archived, and the coerced copy.
# A fresh directory per run, so files from earlier runs are never listed.
B="$WORK/blobs/run-$(date +%s)"
mkdir -p "$B/gw" "$B/coerce"
docker cp dcs-gw:/var/lib/libredcm/blobs/. "$B/gw" >/dev/null 2>&1
docker cp dcs-coerce:/var/lib/libredcm/blobs/. "$B/coerce" >/dev/null 2>&1
{
echo "# Stored Part 10 files in dcs-gw: transfer syntax and implementation identification"
(cd "$B/gw" && find . -name '*.dcm' | sort) | while read -r f; do
  echo "== $f"
  docker run --rm -v "$B/gw":/b:ro --entrypoint dcmdump rws-e2e-dcmtk -M +P 0002,0002 +P 0002,0010 +P 0002,0012 +P 0002,0013 "/b/$f" 2>&1
done
} > "$OUT/70-stored-files.log"
{
echo "# Held conflict copy in dcs-coerce (patient_conflict_coerce_patient_id=true): Patient ID and Original Attributes Sequence"
(cd "$B/coerce" && find . -path '*conflicts*' -name '*.dcm' | sort) | while read -r f; do
  echo "== $f"
  docker run --rm -v "$B/coerce":/b:ro --entrypoint dcmdump rws-e2e-dcmtk -M "/b/$f" 2>&1 | grep -E "\(0010,0020\)|\(0400,05|\(fffe,e000\)|\(fffe,e00d\)|\(fffe,e0dd\)"
done
} > "$OUT/71-coerced-file.log"

for c in dcs-gw dcs-allow dcs-reject dcs-coerce dcs-tls; do
  docker logs $c > "$OUT/80-gateway-$c.log" 2>&1
  docker stop $c >/dev/null; docker container rm $c >/dev/null
done
docker network rm $NET >/dev/null
cat "$OUT/00-build.txt" "$OUT/03-runs.txt"
