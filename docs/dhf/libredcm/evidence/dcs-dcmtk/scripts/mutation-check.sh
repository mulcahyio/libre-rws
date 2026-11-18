#!/bin/bash
# Mutation check for the two drift checks of the DICOM Conformance Statement.
# Each mutant changes product code so that the statement no longer matches;
# TestConformanceStatementMatchesCode must fail, and scripts/qms/dcs.py too
# where it checks that table (it does not check default values, so M4 is
# Go-only). Every file is restored after its mutant.
#
#   bash docs/dhf/libredcm/evidence/dcs-dcmtk/scripts/mutation-check.sh   # from the repository root
cd "$(git rev-parse --show-toplevel)" || exit 1
B=${DCS_WORK:-${TMPDIR:-/tmp}/libredcm-dcs}/mutants
mkdir -p "$B"
mutate() { # name file old new
  local name=$1 f=$2 old=$3 new=$4
  cp "$f" "$B/orig"
  python3 - "$f" "$old" "$new" <<'PY' || { echo "$name: PATTERN NOT FOUND"; return; }
import sys
p, old, new = sys.argv[1:4]
t = open(p, encoding="utf-8").read()
if old not in t:
    sys.exit(1)
open(p, "w", encoding="utf-8").write(t.replace(old, new, 1))
PY
  timeout 300 rws-go test -count=1 -timeout 90s -run TestConformanceStatementMatchesCode ./modules/dicom/internal/gateway/ > "$B/$name.go.log" 2>&1
  local go=$?
  python3 scripts/qms/dcs.py > "$B/$name.py.log" 2>&1
  local py=$?
  cp "$B/orig" "$f"
  local gores=survived pyres=survived
  # A mutant that does not compile proves nothing: count only test failures.
  [ $go -ne 0 ] && grep -q "FAIL" "$B/$name.go.log" && ! grep -q "build failed\|cannot use\|undefined:" "$B/$name.go.log" && gores=killed
  [ $py -ne 0 ] && pyres=killed
  echo "$name: go=$gores (exit $go) dcs.py=$pyres (exit $py) :: $(grep -m1 -E 'statement|differs|problem|in the code|matching' "$B/$name.go.log" | cut -c1-120)"
}
mutate M1-transfer-syntax modules/dicom/internal/dcm/dcm.go $'\t"1.2.840.10008.1.2.5",     // RLE Lossless\n' ''
mutate M2-matching-type modules/dicom/internal/gateway/query.go '{"st.accession_number", kString}' '{"st.accession_number", kExact}'
mutate M3-qr-class modules/dicom/internal/gateway/gateway.go $'\t\tae.StudyRootQueryRetrieveInformationModelGet,\n' ''
mutate M4-default modules/dicom/internal/gateway/gateway.go 'FindLimit:        1000,' 'FindLimit:        999,'
mutate M5-return-key modules/dicom/internal/index/index.go ', {"se.series_description", "SeriesDescription"}' ''
git diff --quiet HEAD -- modules && echo "modules restored: clean" || echo "MODULES DIRTY"
