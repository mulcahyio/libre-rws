#!/usr/bin/env python3
"""Check relative Markdown links (files and #anchors) in the repository's
Markdown files (QMS-SOP-001 §5.2). third_party/ is skipped.

    python3 scripts/qms/mdlinks.py [ROOT]

Exit status 1 when a link points to a missing file or heading.
"""
import pathlib, re, sys
root = pathlib.Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[2]
bad = 0
def anchors(p):
    out = set()
    for line in p.read_text(encoding="utf-8").splitlines():
        if line.startswith("#"):
            t = line.lstrip("#").strip().lower()
            t = re.sub(r"[^\w\- ]", "", t).replace(" ", "-")
            out.add(t)
    return out
files = [p for p in root.rglob("*.md") if ".git" not in p.parts and "third_party" not in p.parts]
for f in files:
    txt = f.read_text(encoding="utf-8")
    txt = re.sub(r"```.*?```", "", txt, flags=re.S)
    for m in re.finditer(r"\]\(([^)\s]+)\)", txt):
        link = m.group(1)
        if re.match(r"^[a-z]+:", link) or link.startswith("mailto"):
            continue
        path, _, frag = link.partition("#")
        target = (f.parent / path).resolve() if path else f
        if not target.exists():
            print(f"{f.relative_to(root)}: missing {link}"); bad += 1; continue
        if frag and target.suffix == ".md" and frag.lower() not in anchors(target):
            print(f"{f.relative_to(root)}: missing anchor {link}"); bad += 1
print(f"checked {len(files)} files, {bad} broken links")
sys.exit(1 if bad else 0)
