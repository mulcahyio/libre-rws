#!/usr/bin/env python3
"""LibreDVT v0.2 card DVT-0.2/6: show that every v0.2 requirement
(DVT-SRS-090 and later) is verified by tests that actually ran and passed.

For each requirement row it finds the Go test functions tagged
`// Verifies: ...` directly above them (the same rule trace.py uses) and
looks each one up in the real-engine test log (go test -v -tags engines of
every LibreDVT package): "--- PASS: TestName". A requirement passes this
check when it has at least one tagged test and every tagged test passed.
Manual-verification entries are listed too.

usage: trace-evidence.py ENGINE_TEST_LOG
Exit 0 only if every v0.2 row is Active and every tagged test passed.
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[6]
LOG = pathlib.Path(sys.argv[1]).read_text(errors="replace")
SRS = (ROOT / "docs/dhf/libredvt/SRS.md").read_text()
MANUAL = (ROOT / "docs/dhf/libredvt/manual-verification.md").read_text()

rows = {}
for line in SRS.splitlines():
    m = re.match(r"\|\s*(DVT-SRS-(\d+))\s*\|", line)
    if m and int(m.group(2)) >= 90:
        rows[m.group(1)] = [c.strip() for c in line.split("|")][3]

passed = set(re.findall(r"--- PASS: (Test\w+)", LOG))
failed = set(re.findall(r"--- FAIL: (Test\w+)", LOG))
tests = {}  # id -> set of (file, func)
for p in (ROOT / "modules/dvt").rglob("*_test.go"):
    lines = p.read_text().splitlines()
    for i, l in enumerate(lines):
        if "Verifies:" not in l:
            continue
        j = i + 1
        while j < len(lines) and lines[j].startswith("//"):
            j += 1
        m = re.match(r"func (Test\w+)\(", lines[j]) if j < len(lines) else None
        if not m:
            continue
        for rid in re.findall(r"DVT-SRS-\d+", l):
            tests.setdefault(rid, set()).add((str(p.relative_to(ROOT)), m.group(1)))
manual = {rid for rid in re.findall(r"^- (DVT-SRS-\d+):", MANUAL, re.M)}

bad = 0
print("| Requirement | Status | Tagged tests | Passed in the engine log | Manual record |")
print("|---|---|---|---|---|")
for rid in sorted(rows, key=lambda r: int(r.split("-")[-1])):
    ts = sorted(tests.get(rid, ()))
    ok_tests = [t for _, t in ts if t in passed and t not in failed]
    ok = rows[rid] == "Active" and ts and len(ok_tests) == len(ts)
    bad += 0 if ok else 1
    names = ", ".join(t for _, t in ts) or "none"
    print(f"| {rid} | {rows[rid]} | {names} | {len(ok_tests)} of {len(ts)}{'' if ok else ' **CHECK**'} | {'yes' if rid in manual else ''} |")
print()
print(f"v0.2 rows: {len(rows)}; fully verified by passing tests: {len(rows) - bad}; problems: {bad}")
print(f"engine log: {len(passed)} distinct passing tests, {len(failed)} failing")
sys.exit(1 if bad else 0)
