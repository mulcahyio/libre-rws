#!/usr/bin/env python3
"""Summarize the DVT-0.2/6 release-candidate system run and compare it with
the outcome expected for each run. Expectations were written before the run
from the peers' documented behaviour (LibreDCM's DICOM Conformance Statement
and DCM-SRS-043; DCMTK 3.6.7 storescp/echoscu documentation; the results of
the card DVT-0.2/3 cross-check for the same DCMTK set-ups). Then the DCMTK
echoscu results are compared with the audit's own verdicts, and every
artefact is searched for the synthetic token and private-key material.

usage: summarize.py OUTDIR PKIDIR
Exit 0 only if every run and every DCMTK comparison matches and no secret
leaked.
"""
import base64
import glob
import json
import os
import sys

OUT, PKI = sys.argv[1], sys.argv[2]

ALL5 = ["negotiate-verification", "echo-over-tls", "plaintext-refused", "tls10-refused", "tls11-refused"]


def steps(ids, status="passed", **over):
    d = {i: status for i in ids}
    d.update({k.replace("_", "-"): v for k, v in over.items()})
    return d


# label -> (expected run verdict, {step id: expected status})
EXPECT = {
    # 1. release candidate against real systems
    "rc-libredcm-smoke": ("pass", steps(["negotiate", "echo-over-tls", "store-over-tls", "plaintext-refused", "tls11-refused"])),
    "rc-dicom-tls-audit-libredcm": ("pass", steps(ALL5)),
    "rc-libredcm-conformance-v01": ("pass", steps(["echo", "reject-wrong-called-ae", "store-native-and-compressed",
                                                   "find-stored-studies", "move-and-compare-pixels"])),
    "rc-dicomweb-audit": ("pass", steps(["plaintext-refused", "tls11-refused", "stow-samples", "qido-studies", "qido-series",
                                         "qido-instances", "qido-no-match", "wado-and-compare-pixels"])),
    # 2. Generic DICOM TLS audit against DCMTK storescp
    # +tls (BCP 195 profile, TLS 1.2+): a correct DICOM TLS endpoint.
    "xc-audit-dcmtk-tls": ("pass", steps(ALL5)),
    # No TLS at all: the TLS association and echo fail, plaintext is
    # accepted (finding); a TLS 1.0/1.1 handshake cannot complete against a
    # plaintext port, so those probes count as refused (DVT-SRS-105).
    "xc-audit-dcmtk-plain": ("fail", steps(ALL5, negotiate_verification="failed", echo_over_tls="failed",
                                           plaintext_refused="failed")),
    # Retired AES profile: accepts TLS 1.0 and 1.1 (findings) and offers only
    # AES128-SHA, which LibreDVT's TLS 1.2+ client does not offer, so the
    # verified association fails closed ("no shared cipher" in DCMTK's log,
    # as in card DVT-0.2/3).
    "xc-audit-dcmtk-aes": ("fail", steps(ALL5, negotiate_verification="failed", echo_over_tls="failed",
                                         tls10_refused="failed", tls11_refused="failed")),
    # Certificate for another host name: LibreDVT must not trust it
    # (DVT-SRS-102); the refusals still hold.
    "xc-audit-dcmtk-wrong-host": ("fail", steps(ALL5, negotiate_verification="failed", echo_over_tls="failed")),
    # 3. anonymised sample: LibreDCM as pacs.lab.example
    "sample-dicom-tls-audit": ("pass", steps(ALL5)),
}

# DCMTK echoscu exit status expected per server (0 = C-ECHO succeeded).
# "-tls" uses DCMTK's default TLS profile with the test CA; DCMTK does not
# check host names, so it accepts the wrong-host certificate where LibreDVT
# refuses it (stricter by design). For the AES profile server DCMTK's
# default profile is recorded only (not asserted); the retired-profile
# client (+pa) is expected to connect.
ECHO = {
    "libredcm-tls": 0, "libredcm-plain": "nonzero",
    "dcmtk-tls-tls": 0, "dcmtk-tls-plain": "nonzero",
    "dcmtk-plain-tls": "nonzero", "dcmtk-plain-plain": 0,
    "dcmtk-aes-plain": "nonzero", "dcmtk-aes-tls-aes-profile": 0,
    "dcmtk-wrong-tls": 0, "dcmtk-wrong-plain": "nonzero",
}
# Which audit step each DCMTK observation corresponds to, and whether the
# two must agree. (server, echoscu label, audit run, audit step, relation)
#   same     : echoscu success <=> audit step passed
#   opposite : echoscu success <=> audit step failed (plaintext accepted
#              means plaintext-refused failed)
AGREE = [
    ("LibreDCM TLS", "libredcm-tls", "rc-dicom-tls-audit-libredcm", "echo-over-tls", "same"),
    ("LibreDCM TLS", "libredcm-plain", "rc-dicom-tls-audit-libredcm", "plaintext-refused", "opposite"),
    ("DCMTK +tls", "dcmtk-tls-tls", "xc-audit-dcmtk-tls", "echo-over-tls", "same"),
    ("DCMTK +tls", "dcmtk-tls-plain", "xc-audit-dcmtk-tls", "plaintext-refused", "opposite"),
    ("DCMTK plain", "dcmtk-plain-tls", "xc-audit-dcmtk-plain", "echo-over-tls", "same"),
    ("DCMTK plain", "dcmtk-plain-plain", "xc-audit-dcmtk-plain", "plaintext-refused", "opposite"),
    ("DCMTK AES profile", "dcmtk-aes-plain", "xc-audit-dcmtk-aes", "plaintext-refused", "opposite"),
]


def exit_code(label):
    """echoscu's exit status, appended by the run script as "exit N"."""
    path = os.path.join(OUT, f"dcmtk-echoscu-{label}.log")
    try:
        with open(path, errors="replace") as f:
            last = [l for l in f.read().splitlines() if l.strip()][-1]
    except (OSError, IndexError):
        return None
    if not last.startswith("exit "):
        return None
    return int(last.split()[1])


bad = 0
runs = {}
for label, (want, exp) in EXPECT.items():
    path = os.path.join(OUT, f"{label}-run.json")
    try:
        with open(path) as f:
            run = json.load(f)
    except (OSError, ValueError) as e:
        print(f"MISMATCH {label}: no readable run JSON ({e.__class__.__name__})")
        bad += 1
        continue
    runs[label] = run
    got = run.get("verdict")
    ok = got == want and not run.get("error")
    lines, seen = [], {}
    for s in run.get("steps") or []:
        seen[s.get("id")] = s.get("status")
        msg = " | ".join(s.get("messages") or [])
        lines.append(f"    {s.get('id')}: {s.get('status')} :: {msg[:300]}")
    for sid, st in exp.items():
        if seen.get(sid) != st:
            ok = False
            lines.append(f"    !! step {sid}: expected {st}, got {seen.get(sid)}")
    if set(seen) != set(exp):
        ok = False
        lines.append(f"    !! steps run {sorted(seen)} differ from expected {sorted(exp)}")
    print(f"{'OK      ' if ok else 'MISMATCH'} {label}: verdict {got} (expected {want})"
          f"{' run error: ' + run['error'] if run.get('error') else ''}")
    print("\n".join(lines))
    bad += 0 if ok else 1

print("== DCMTK echoscu")
for label, want in ECHO.items():
    got = exit_code(label)
    ok = got is not None and ((want == 0 and got == 0) or (want == "nonzero" and got != 0))
    print(f"{'OK      ' if ok else 'MISMATCH'} echoscu {label}: {'success' if got == 0 else 'failed' if got else 'no log'}"
          f" (expected {'success' if want == 0 else 'failed'})")
    bad += 0 if ok else 1
got = exit_code("dcmtk-aes-tls")
print(f"recorded echoscu dcmtk-aes-tls (DCMTK default profile vs retired AES profile): {'success' if got == 0 else 'failed'}")

print("== DCMTK and the audit state the same facts")
for server, el, run, sid, rel in AGREE:
    e = exit_code(el)
    st = next((s.get("status") for s in (runs.get(run) or {}).get("steps") or [] if s.get("id") == sid), None)
    ok = e is not None and st in ("passed", "failed") and ((e == 0) == (st == "passed")) == (rel == "same")
    print(f"{'AGREE   ' if ok else 'DISAGREE'} {server}: echoscu {el} {'success' if e == 0 else 'failed'}; audit {sid} {st}")
    bad += 0 if ok else 1

# Secret check: the token (plain, base64, base64url) and private-key
# material must be in no artefact.
needles = ["PRIVATE KEY"]
with open(os.path.join(PKI, "token")) as f:
    tok = f.read().strip()
needles += [tok, base64.b64encode(tok.encode()).decode().rstrip("="), base64.urlsafe_b64encode(tok.encode()).decode().rstrip("=")]
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
            print(f"LEAK {os.path.basename(p)}")
            break
print(f"secret check: {len(files)} artefacts, {len(needles)} needles (token in 3 forms, key lines), {leaks} leaks")
total = len(EXPECT) + len(ECHO) + len(AGREE)
print(f"== {total - bad} of {total} checks as expected ({len(EXPECT)} LibreDVT runs, {len(ECHO)} echoscu runs, {len(AGREE)} agreement checks)")
sys.exit(1 if bad or leaks else 0)
