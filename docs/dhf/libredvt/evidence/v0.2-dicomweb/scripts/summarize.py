#!/usr/bin/env python3
"""Summarize the DVT-0.2/4 DICOMweb cross-check runs against Orthanc and
compare them with the outcome expected for each run (set before running,
from the Orthanc Book and PS3.18). Also greps every artefact for the
synthetic bearer token (plain, base64, URL-safe base64) and private-key
material.

usage: summarize.py OUTDIR PKIDIR TOKEN
Exit 0 only if every run matches its expectation and nothing leaked.
"""
import base64
import glob
import json
import os
import sys

OUT, PKI, TOKEN = sys.argv[1], sys.argv[2], sys.argv[3]

AUDIT = ["plaintext-refused", "tls11-refused", "stow", "qido-studies", "qido-series", "qido-instances",
         "qido-patient", "qido-paged", "qido-none", "wado-store", "wado-explicit", "wado-meta"]

# label -> (expected run verdict, {step id: expected status})
EXPECT = {
    "orthanc-audit": ("pass", {s: "passed" for s in AUDIT}),
    "orthanc-mtls": ("pass", {"no-client-cert-refused": "passed", "search-with-client-cert": "passed"}),
    "orthanc-token-loopback": ("pass", {"stow": "passed", "qido": "passed", "wado": "passed"}),
    # Negative controls.
    "orthanc-no-client-cert": ("fail", {"no-client-cert-refused": "failed"}),  # Orthanc does not ask for one
    "orthanc-wrong-host": ("fail", {"search": "failed"}),                      # certificate is for "orthanc"
}

bad = 0
for label, (want, steps) in EXPECT.items():
    path = os.path.join(OUT, f"{label}-run.json")
    try:
        with open(path) as f:
            run = json.load(f)
    except (OSError, ValueError) as e:
        print(f"MISMATCH {label}: no readable run JSON ({e.__class__.__name__})")
        bad += 1
        continue
    got = run.get("verdict")
    ok = got == want and not run.get("error")
    lines, seen = [], {}
    for s in run.get("steps") or []:
        seen[s.get("id")] = s.get("status")
        msg = " | ".join(s.get("messages") or [])
        lines.append(f"    {s.get('id')}: {s.get('status')} ({s.get('verdict')}) :: {msg[:300]}")
    for sid, st in steps.items():
        if seen.get(sid) != st:
            ok = False
            lines.append(f"    !! step {sid}: expected {st}, got {seen.get(sid)}")
    if set(seen) != set(steps):
        ok = False
        lines.append(f"    !! steps run {sorted(seen)} differ from expected {sorted(steps)}")
    print(f"{'OK      ' if ok else 'MISMATCH'} {label}: verdict {got} (expected {want})"
          f"{' run error: ' + run['error'] if run.get('error') else ''}")
    print("\n".join(lines))
    bad += 0 if ok else 1

needles = [TOKEN, base64.b64encode(TOKEN.encode()).decode(), base64.urlsafe_b64encode(TOKEN.encode()).decode().rstrip("="),
           "PRIVATE KEY"]
for k in glob.glob(os.path.join(PKI, "*.key")):
    with open(k) as f:
        needles += [l.strip() for l in f if l.strip() and not l.startswith("-----") and len(l.strip()) >= 40]
leaks = 0
files = [p for p in glob.glob(os.path.join(OUT, "*")) if os.path.isfile(p)]
for p in files:
    with open(p, errors="replace") as f:
        text = f.read()
    for n in needles:
        if n in text:
            leaks += 1
            print(f"LEAK {os.path.basename(p)}")
            break
print(f"secret check: {len(files)} artefacts, {len(needles)} needles (token forms and key lines), {leaks} leaks")
print(f"== {len(EXPECT) - bad} of {len(EXPECT)} runs as expected")
sys.exit(1 if bad or leaks else 0)
