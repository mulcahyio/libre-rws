#!/usr/bin/env python3
"""Summarize the DVT-0.2/3 cross-check runs and compare them with the
expected outcome of each run (decided before running, from the peers'
documented behaviour). Also greps every artefact for private-key material.

usage: summarize.py OUTDIR [PKIDIR]
Exit 0 only if every run matches its expectation and no key leaked.
"""
import glob
import json
import os
import sys

OUT = sys.argv[1]
PKI = sys.argv[2] if len(sys.argv) > 2 else ""

# label -> (expected run verdict, {step id: expected status})
EXPECT = {
    "plain-negotiation": ("pass", {"negotiation": "passed"}),
    "refuse": ("pass", {"refused": "passed"}),
    "tls-negotiation": ("pass", {"negotiation": "passed", "echo-over-tls": "passed"}),
    "tls-refusals": ("pass", {"plaintext-refused": "passed", "tls10-refused": "passed", "tls11-refused": "passed"}),
    # Negative controls: the peer misbehaves (or LibreDVT must not trust it).
    "tls-no-client-cert-not-required": ("fail", {"no-client-cert-refused": "failed"}),
    "mtls": ("pass", {"associate-with-client-cert": "passed", "no-client-cert-refused": "passed", "plaintext-refused": "passed"}),
    "mtls-stranger": ("fail", {"echo": "failed"}),
    "wrong-host": ("fail", {"echo": "failed"}),
    # DCMTK's retired AES profile offers only AES128-SHA (RSA key exchange,
    # no forward secrecy). LibreDVT's normal TLS 1.2+ client does not offer
    # that suite, so its echo fails with a handshake alert (fail closed),
    # while the TLS 1.0/1.1 probes see the server negotiate the old versions.
    # First run (2026-11-19) expected the echo to pass; the DCMTK server log
    # ("no shared cipher" for LibreDVT, AES128-SHA for echoscu +pa) showed
    # the expectation was wrong, not LibreDVT. Recorded as a deviation.
    "aes-profile-old-versions": ("fail", {"echo-tls12": "failed", "tls10-refused": "failed", "tls11-refused": "failed"}),
    "libredcm-tls": ("pass", {"negotiation": "passed", "echo-over-tls": "passed", "plaintext-refused": "passed",
                              "tls10-refused": "passed", "tls11-refused": "passed"}),
    "libredcm-no-client-cert": ("fail", {"no-client-cert-refused": "failed"}),
    "store-dcmtk-tls": ("pass", {"store-over-tls": "passed"}),
    "store-libredcm-tls": ("pass", {"store-over-tls": "passed", "find-over-tls": "passed"}),
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
    lines = []
    seen = {}
    for s in run.get("steps") or []:
        seen[s.get("id")] = s.get("status")
        msg = " | ".join(s.get("messages") or [])
        lines.append(f"    {s.get('id')}: {s.get('status')} :: {msg[:400]}")
    for sid, st in steps.items():
        if seen.get(sid) != st:
            ok = False
            lines.append(f"    !! step {sid}: expected {st}, got {seen.get(sid)}")
    if set(seen) != set(steps):
        ok = False
        lines.append(f"    !! steps run {sorted(seen)} differ from expected {sorted(steps)}")
    print(f"{'OK      ' if ok else 'MISMATCH'} {label}: verdict {got} (expected {want}){' run error: ' + run['error'] if run.get('error') else ''}")
    print("\n".join(lines))
    bad += 0 if ok else 1

# Secret check: no private-key header or key body line in any artefact.
needles = ["PRIVATE KEY"]
if PKI:
    for k in glob.glob(os.path.join(PKI, "*.key")):
        with open(k) as f:
            body = [l.strip() for l in f if l.strip() and not l.startswith("-----")]
        needles += [l for l in body if len(l) >= 40]
leaks = 0
files = [p for p in glob.glob(os.path.join(OUT, "*")) if os.path.isfile(p)]
for p in files:
    with open(p, errors="replace") as f:
        text = f.read()
    for n in needles:
        if n in text:
            leaks += 1
            print(f"LEAK {os.path.basename(p)}: holds private-key material")
            break
print(f"secret check: {len(files)} artefacts, {len(needles)} needles, {leaks} leaks")
print(f"== {len(EXPECT) - bad} of {len(EXPECT)} runs as expected")
sys.exit(1 if bad or leaks else 0)
