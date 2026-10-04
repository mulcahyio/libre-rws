#!/usr/bin/env bash
# DICOM conformance smoke test against a running rws-dicom using DCMTK, the
# reference toolkit (apt install dcmtk). Exercises echo, native + compressed
# storage, query and retrieve, and checks pixel data survives byte-for-byte.
#
#   scripts/conformance.sh <dir-of-.dcm-files> [host] [port] [ae]
# Requires: dcmtk, python3 + pydicom (for byte comparison).
set -euo pipefail
DIR=${1:?dir of DICOM files}; HOST=${2:-127.0.0.1}; PORT=${3:-11112}; AE=${4:-RWS_DICOM}
DEST_PORT=${DEST_PORT:-11113}  # rws-dicom must list STORESCP -> this port in remote_aes
WORK=$(mktemp -d); trap 'kill ${SCP:-0} 2>/dev/null; rm -rf "$WORK"' EXIT
pass() { echo "PASS  $*"; }; fail() { echo "FAIL  $*"; exit 1; }

echoscu -aec "$AE" "$HOST" "$PORT" && pass "C-ECHO" || fail "C-ECHO"

# Send each file proposing its own transfer syntax (compressed stays compressed).
for f in "$DIR"/*.dcm; do
  ts=$(dcmdump -q +P 0002,0010 "$f" | sed -E 's/.*=([A-Za-z0-9]+).*/\1/')
  case "$ts" in
    JPEGBaseline) opt=-xy ;; JPEGExtended*) opt=-xx ;; JPEGLossless*) opt=-xs ;;
    JPEG2000LosslessOnly) opt=-xv ;; JPEG2000) opt=-xw ;; RLELossless) opt=-xr ;;
    JPEGLSLossless) opt=-xt ;; JPEGLSLossy) opt=-xu ;; *) opt="" ;;
  esac
  storescu $opt -aec "$AE" "$HOST" "$PORT" "$f" >/dev/null 2>&1 \
    && pass "C-STORE $(basename "$f") ($ts)" || fail "C-STORE $(basename "$f") ($ts)"
done

n=$(findscu -S -aec "$AE" -k QueryRetrieveLevel=STUDY -k StudyInstanceUID "$HOST" "$PORT" 2>&1 | grep -ac '0020,000d' || true)
[ "$n" -gt 0 ] && pass "C-FIND STUDY ($n studies)" || fail "C-FIND"

mkdir -p "$WORK/recv"; storescp +xa -aet STORESCP -od "$WORK/recv" "$DEST_PORT" >/dev/null 2>&1 & SCP=$!; sleep 1
for st in $(python3 -c "import pydicom,glob,sys; print(' '.join(sorted({pydicom.dcmread(f,stop_before_pixels=True).StudyInstanceUID for f in glob.glob(sys.argv[1]+'/*.dcm')})))" "$DIR"); do
  movescu -S -aec "$AE" -aem STORESCP -k QueryRetrieveLevel=STUDY -k StudyInstanceUID="$st" "$HOST" "$PORT" >/dev/null 2>&1 || fail "C-MOVE $st"
done
pass "C-MOVE ($(ls "$WORK/recv" | wc -l) instances received)"

python3 - "$DIR" "$WORK/recv" <<'PY'
import glob, sys, pydicom
orig = {pydicom.dcmread(f).SOPInstanceUID: pydicom.dcmread(f) for f in glob.glob(sys.argv[1] + "/*.dcm")}
bad = 0
for f in glob.glob(sys.argv[2] + "/*"):
    d = pydicom.dcmread(f); o = orig[d.SOPInstanceUID]
    same_ts = d.file_meta.TransferSyntaxUID == o.file_meta.TransferSyntaxUID
    if o.file_meta.TransferSyntaxUID.is_compressed and not (same_ts and d.PixelData == o.PixelData):
        bad += 1; print("FAIL  compressed pixel data changed:", d.SOPInstanceUID)
print("PASS  compressed instances retrieved byte-identical" if not bad else "")
sys.exit(1 if bad else 0)
PY
echo "All checks passed."
