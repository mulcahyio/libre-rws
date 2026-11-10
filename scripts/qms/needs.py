#!/usr/bin/env python3
"""Check that every non-retired SRS requirement serves at least one user need.

Reads the requirement table in docs/dhf/<module>/SRS.md and the user-need
table (rows starting with "| UN-") in docs/dhf/<module>/intended-use.md.
In the need table, the SRS column lists IDs as "DCM-SRS-002, -005, -018":
a bare "-005" reuses the prefix of the full ID before it.

Exit status 1 when a non-retired requirement is not traced to a user need,
or a need cites an unknown or retired requirement.

    python3 scripts/qms/needs.py [--module libredcm --prefix DCM]
"""
import argparse
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]


def requirements(srs: pathlib.Path, prefix: str) -> dict:
    pat = re.compile(rf"^\|\s*({prefix}-SRS-\d{{3}})\s*\|(.*)$")
    out = {}
    for line in srs.read_text(encoding="utf-8").splitlines():
        m = pat.match(line)
        if m:
            cells = [c.strip() for c in m.group(2).split("|")]
            out[m.group(1)] = cells[1] if len(cells) > 1 else ""
    return out


def needs(doc: pathlib.Path, prefix: str) -> dict:
    out = {}
    for line in doc.read_text(encoding="utf-8").splitlines():
        if not line.startswith("| UN-"):
            continue
        cells = [c.strip() for c in line.strip().strip("|").split("|")]
        if len(cells) < 4:
            continue
        ids = []
        for m in re.finditer(rf"{prefix}-SRS-(\d{{3}})|(?<![\w])-(\d{{3}})\b", cells[3]):
            ids.append(f"{prefix}-SRS-{m.group(1) or m.group(2)}")
        out[cells[0]] = ids
    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--module", default="libredcm")
    ap.add_argument("--prefix", default="DCM")
    a = ap.parse_args()
    base = ROOT / "docs" / "dhf" / a.module
    reqs = requirements(base / "SRS.md", a.prefix)
    un = needs(base / "intended-use.md", a.prefix)
    if not reqs or not un:
        print("no requirements or no user needs found", file=sys.stderr)
        return 1
    cited = {i for ids in un.values() for i in ids}
    unserved = sorted(i for i, st in reqs.items() if st != "Retired" and i not in cited)
    bad = sorted(i for i in cited if reqs.get(i) in (None, "Retired"))
    print(f"User needs: {len(un)}. Requirements: {len(reqs)} "
          f"({sum(1 for s in reqs.values() if s != 'Retired')} not retired). "
          f"Not traced to a need: {len(unserved)}. Unknown or retired citations: {len(bad)}.")
    for i in unserved:
        print(f"  not traced to a user need: {i}")
    for i in bad:
        print(f"  cited but unknown or retired: {i}")
    return 1 if unserved or bad else 0


if __name__ == "__main__":
    sys.exit(main())
