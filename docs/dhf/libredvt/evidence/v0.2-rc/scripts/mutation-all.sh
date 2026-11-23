#!/bin/bash
# LibreDVT v0.2 card DVT-0.2/6: re-run every v0.2 mutation check on the
# release-candidate commit, plus the card 6 mutants (printable report).
# Everything runs in a git worktree of HEAD, so the main tree is never
# touched. Logs go to docs/dhf/libredvt/evidence/v0.2-rc/mutation/.
#
#   bash docs/dhf/libredvt/evidence/v0.2-rc/scripts/mutation-all.sh TESTIMAGE   # from the repository root
# TESTIMAGE: the Dockerfile.dvt test stage of this commit (Go + engines),
# e.g. built with --target test -t libre/dvt-test:0.2-rc.
cd "$(git rev-parse --show-toplevel)" || exit 1
IMG=${1:?test image}
REPO=$PWD
OUT=$REPO/docs/dhf/libredvt/evidence/v0.2-rc/mutation
WT=${TMPDIR:-/tmp}/dvt06-mutation-wt
mkdir -p "$OUT"
git worktree remove --force "$WT" >/dev/null 2>&1
git worktree add --detach "$WT" HEAD >/dev/null 2>&1 || { echo "worktree failed"; exit 1; }
echo "mutation re-run, head $(git -C "$WT" rev-parse HEAD), image $IMG $(docker inspect -f '{{.Id}}' "$IMG"), $(date -u +%Y-%m-%dT%H:%M:%SZ)"
cd "$WT" || exit 1
E=docs/dhf/libredvt/evidence

echo "== card 2 (foundation)"
DVT_WORK=${TMPDIR:-/tmp}/dvt06-mut-foundation bash $E/v0.2-foundation/scripts/mutation-check.sh > "$OUT/card2-foundation.log" 2>&1
echo "card 2 exit $? :: $(tail -1 "$OUT/card2-foundation.log")"

echo "== card 3 (association, TLS)"
mkdir -p "${TMPDIR:-/tmp}/dvt06-mut3"
python3 $E/v0.2-assoc-tls/scripts/mutation.py "$WT" "$IMG" "${TMPDIR:-/tmp}/dvt06-mut3/mutation.log" > /dev/null 2>&1
rc=$?
cp "${TMPDIR:-/tmp}/dvt06-mut3/mutation.log" "$OUT/card3-assoc-tls.log"
echo "card 3 exit $rc :: $(tail -1 "$OUT/card3-assoc-tls.log")"

echo "== card 4 (DICOMweb)"
mkdir -p "${TMPDIR:-/tmp}/dvt06-mut4"
python3 $E/v0.2-dicomweb/scripts/mutation.py "$WT" "$IMG" "${TMPDIR:-/tmp}/dvt06-mut4/mutation.log" > /dev/null 2>&1
rc=$?
cp "${TMPDIR:-/tmp}/dvt06-mut4/mutation.log" "$OUT/card4-dicomweb.log"
echo "card 4 exit $rc :: $(tail -1 "$OUT/card4-dicomweb.log")"

echo "== card 5 (UI, CLI, reports, bundled plans)"
python3 $E/v0.2-ui/scripts/mutation.py > "$OUT/card5-ui.log" 2>&1
echo "card 5 exit $? :: $(tail -1 "$OUT/card5-ui.log")"

echo "== card 6 (printable report)"
{
k=0
mut() { # id file old new what
  cp "$2" "$2.orig"
  python3 - "$2" "$3" "$4" <<'PY' || { echo "$1: PATTERN NOT FOUND"; cp "$2.orig" "$2"; rm -f "$2.orig"; return; }
import sys
p, old, new = sys.argv[1:4]
s = open(p).read()
if s.count(old) != 1:
    sys.exit(1)
open(p, "w").write(s.replace(old, new))
PY
  timeout 300 rws-go test -count=1 -timeout 90s -run TestReportPrintsAllEvidence ./modules/dvt/internal/report/ > "$2.log" 2>&1
  local rc=$?
  cp "$2.orig" "$2"; rm -f "$2.orig"
  if grep -q -- "--- FAIL" "$2.log"; then echo "$1 KILLED ($5)"; k=$((k + 1)); else echo "$1 SURVIVED rc=$rc ($5)"; cat "$2.log"; fi
  rm -f "$2.log"
}
CSS=modules/dvt/internal/report/style.css
mut R1 $CSS "details::details-content { content-visibility: visible; display: block; }" "" "closed <details> stay closed in print"
mut R2 $CSS "pre { max-height: none; overflow: visible; }" "pre { overflow: visible; }" "long raw output clipped in print"
mut R3 $CSS "--surface-100: #FFFFFF; --surface-200: #FFFFFF; --surface-300: #F4F7F7;" "--surface-100: #0B1216; --surface-200: #121B21; --surface-300: #1A252C;" "dark backgrounds in print"
mut R4 $CSS "td.mono:first-child { word-break: normal; overflow-wrap: break-word; }" "td.mono:first-child { }" "step names break mid-word"
echo "card 6: $k of 4 killed"
} > "$OUT/card6-print.log" 2>&1
echo "card 6 :: $(tail -1 "$OUT/card6-print.log")"

cd "$REPO" || exit 1
if git -C "$WT" diff --quiet HEAD; then echo "worktree clean after all mutants"; else echo "WORKTREE DIRTY"; git -C "$WT" diff --stat; fi
git worktree remove --force "$WT"
echo "done $(date -u +%Y-%m-%dT%H:%M:%SZ)"
