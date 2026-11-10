#!/usr/bin/env bash
# Run every Go fuzz target for a fixed time (cybersecurity.md §7).
# Usage: scripts/qms/fuzz.sh [duration, default 5m]
# Exit status 1 if any target finds a failure; the failing input is written
# to the package's testdata/fuzz/<Target>/ directory, to be committed as a
# regression seed together with the fix.
set -u
T=${1:-5m}
root=$(cd "$(dirname "$0")/../.." && pwd)
rc=0
targets=(
  ".|./modules/dicom/internal/dcm|FuzzPart10RoundTrip"
  ".|./modules/dicom/internal/dcm|FuzzDatasetOffset"
  ".|./modules/dicom/internal/dcm|FuzzDecodeAndExtract"
  ".|./modules/dicom/internal/gateway|FuzzCFindKeys"
  ".|./modules/dicom/internal/gateway|FuzzCFindIdentifierBytes"
  "third_party/gonetdicom|./pdu|FuzzRead"
  "third_party/gonetdicom|./pdu|FuzzDecodePDataTF"
  "third_party/gonetdicom|./dimse|FuzzDecodeElements"
  "third_party/gonetdicom|./dimse|FuzzCommandDecoders"
)
for t in "${targets[@]}"; do
  IFS='|' read -r dir pkg name <<<"$t"
  echo "=== $name ($pkg, $T) $(date -u +%FT%TZ)"
  out=$(cd "$root/$dir" && go test -run='^$' -fuzz="^${name}\$" -fuzztime="$T" "$pkg" 2>&1)
  r=$?
  # Keep the summary: drop the periodic progress lines but keep the last one.
  printf '%s\n' "$out" | awk '/execs:/{last=$0; next} {print} END{if (last) print last}' | tail -20
  echo "rc=$r"
  [ $r -ne 0 ] && rc=1
done
echo "fuzz.sh finished $(date -u +%FT%TZ), overall rc=$rc"
exit $rc
