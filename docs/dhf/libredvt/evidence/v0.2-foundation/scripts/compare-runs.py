"""Compare LibreDVT run.json files step by step.

usage: compare-runs.py BASE.json OTHER.json [OTHER2.json ...]
Prints each step's id, type, status and messages for the base run and
fails (exit 1) when any other run differs in step list, status, messages
or run verdict, or uses v0.2 step fields (verdict, evidence) on a v0.1
plan. Timing fields are ignored.
"""
import json
import sys


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def view(run):
    return [(s["id"], s["type"], s["status"], s.get("messages", [])) for s in run["steps"]]


base = load(sys.argv[1])
print(f"{sys.argv[1]}: verdict {base['verdict']}, {len(base['steps'])} steps")
for sid, typ, status, msgs in view(base):
    print(f"  {status:7} {sid:28} {typ:8} {'; '.join(msgs)}")
bad = 0
for path in sys.argv[2:]:
    other = load(path)
    problems = []
    if other["verdict"] != base["verdict"]:
        problems.append(f"verdict {other['verdict']} != {base['verdict']}")
    if view(other) != view(base):
        for a, b in zip(view(base), view(other)):
            if a != b:
                problems.append(f"step differs: {a} vs {b}")
        if len(view(other)) != len(view(base)):
            problems.append("step count differs")
    for s in other["steps"]:
        if "verdict" in s or "evidence" in s:
            problems.append(f"step {s['id']} has v0.2 fields")
    print(f"{path}: verdict {other['verdict']}, target_desc {other.get('target_desc')!r}: " + ("SAME as base" if not problems else "DIFFERS"))
    for p in problems:
        print("  " + p)
    bad += bool(problems)
sys.exit(1 if bad else 0)
