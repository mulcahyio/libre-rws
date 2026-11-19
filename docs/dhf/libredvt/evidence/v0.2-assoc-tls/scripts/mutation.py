#!/usr/bin/env python3
"""Mutation check for card DVT-0.2/3 (association and TLS).

Go mutants are applied in a git worktree of HEAD (the main tree is never
touched) and checked with the unit tests (rws-go). Python worker mutants are
applied to a scratch copy of the worker, which is mounted over the image's
worker in a test container that runs the real-engine tests against the Go
test peer. Every mutant must make a test fail; a mutant that survives is
reported as SURVIVED. Mutants are written so they still compile/run.

usage: mutation.py WORKTREE TESTIMAGE LOG
"""
import os
import shutil
import subprocess
import sys
import time

WT, IMAGE, LOG = sys.argv[1], sys.argv[2], sys.argv[3]
REPO = "/home/odin1971/Documents/GitHub/libre-rws"
SCR = os.path.dirname(os.path.abspath(LOG))

GO = [
    # id, file, old, new, packages, -run
    ("G1 ignore a rejected context (Go assertion)", "modules/dvt/internal/runner/assoc.go",
     'ev.Assert("assoc.context.result", subj, c.Expect, ci.Result, ci.Reason)',
     'ev.Assert("assoc.context.result", subj, c.Expect, c.Expect, ci.Reason)',
     "./modules/dvt/internal/runner/", "TestAssociate"),
    ("G2 accept TLS 1.1 in a tls_reject probe (Go verdict)", "modules/dvt/internal/runner/assoc.go",
     'case engine.ObsHandshakeCompleted, engine.ObsCertVerifyFailed:\n\t\t\t// The target negotiated the old version (it sent its certificate).\n\t\t\treturn "accepted", ""',
     'case engine.ObsHandshakeCompleted, engine.ObsCertVerifyFailed:\n\t\t\t// The target negotiated the old version (it sent its certificate).\n\t\t\treturn "refused", ""',
     "./modules/dvt/internal/runner/", "TestProbeVerdictTable|TestTLSRejectStep"),
    ("G3 failed release counted as a clean pass", "modules/dvt/internal/runner/assoc.go",
     'ev.Warn("assoc.release", endpoint, "released", "not released", why)',
     'ev.Assert("assoc.release", endpoint, "released", "released", why)',
     "./modules/dvt/internal/runner/", "TestAssociateAbort"),
    ("G4 A-ASSOCIATE-RJ values not compared", "modules/dvt/internal/runner/assoc.go",
     'ev.Assert("assoc.reject", endpoint, rejectText(want.Result, want.Source, want.Reason), got, "PS3.8 Table 9-21 values")',
     '_ = want\n\t\tev.Assert("assoc.reject", endpoint, got, got, "PS3.8 Table 9-21 values")',
     "./modules/dvt/internal/runner/", "TestAssociateExpectedRejection"),
    ("G5 context missing from the A-ASSOCIATE-AC not a protocol error", "modules/dvt/internal/runner/assoc.go",
     'if ci.Result == "missing" {', 'if ci.Result == "missing" && false {',
     "./modules/dvt/internal/runner/", "TestAssociatePositiveControls"),
    ("G6 max PDU expectation ignored", "modules/dvt/internal/runner/assoc.go",
     'ev.Assert("assoc.max_pdu", endpoint, strconv.Itoa(*e.MaxPDU), strconv.Itoa(a.MaxPDU), "")',
     'ev.Assert("assoc.max_pdu", endpoint, strconv.Itoa(*e.MaxPDU), strconv.Itoa(*e.MaxPDU), "")',
     "./modules/dvt/internal/runner/", "TestAssociatePositiveControls"),
    ("G7 missing role reply read as the expected roles", "modules/dvt/internal/runner/assoc.go",
     'got, msg = rolesText(result.Roles{SCU: true, SCP: false}), "no role selection reply; default roles"',
     'got, msg = want, "no role selection reply; default roles"',
     "./modules/dvt/internal/runner/", "TestAssociatePositiveControls"),
    ("G8 v0.1 step passes without verified TLS facts (clear-text fallback)", "modules/dvt/internal/runner/assoc.go",
     'if info == nil || !info.Verified || info.Version == "" {', 'if false && (info == nil || !info.Verified || info.Version == "") {',
     "./modules/dvt/internal/runner/", "TestSecretFileProblems"),
    ("G9 TLS move without Storage SCP certificate allowed", "modules/dvt/internal/runner/runner.go",
     'if rn.SCP.TLSCert == "" || rn.SCP.TLSKey == "" {', 'if false {',
     "./modules/dvt/internal/runner/", "TestTLSMoveNeedsSCPCertificate"),
    ("G10 associate output schema not checked", "modules/dvt/internal/engine/assoc.go",
     'return r, raw, checkAssociate(r, req, p.TLS != nil)', 'return r, raw, nil',
     "./modules/dvt/internal/engine/", "TestAssociate"),
    ("G11 probe that sent a client certificate accepted", "modules/dvt/internal/engine/assoc.go",
     'if x.TLS != nil && x.TLS.ClientCertSent {', 'if x.TLS != nil && x.TLS.ClientCertSent && false {',
     "./modules/dvt/internal/engine/", "TestTLSProbe"),
    ("G12 client certificate/key pair not checked before connecting", "modules/dvt/internal/store/target.go",
     'if _, err := tls.X509KeyPair(cert, key); err != nil {', 'if _, err := tls.X509KeyPair(cert, key); err != nil && false {',
     "./modules/dvt/internal/store/", "TestLoadSecretsChecksCAAndClientKeyPair"),
    ("G13 a verification bypass added to non-test code", "modules/dvt/internal/engine/assoc.go",
     'package engine\n', 'package engine\n\n// InsecureSkipVerify\n',
     "./modules/dvt/cmd/libredvt/", "TestNoTLSVerificationBypass"),
    ("G14 probe passed the client key", "modules/dvt/internal/engine/assoc.go",
     'args = append(args, p.TLS.trustArgs()...)', 'args = append(args, p.TLS.args()...)',
     "./modules/dvt/internal/engine/", "TestTLSProbe"),
]

PY = [
    ("P1 host-name check off (worker), policy guard removed too", [
        ('ctx.hostname_checks_common_name = False  # subjectAltName only (DVT-SRS-102)',
         'ctx.hostname_checks_common_name = False  # subjectAltName only (DVT-SRS-102)\n    setattr(ctx, "check_hostname", False)'),
        ('    _check_client_policy(ctx)\n    return ctx', '    return ctx'),
    ], "TestRealTLSVerification"),
    ("P2 Common Name fallback (worker), policy guard removed too", [
        ('ctx.hostname_checks_common_name = False  # subjectAltName only (DVT-SRS-102)',
         'setattr(ctx, "hostname_checks_common_name", True)'),
        ('    _check_client_policy(ctx)\n    return ctx', '    return ctx'),
    ], "TestRealTLSVerification"),
    ("P3 accept TLS 1.1 (worker minimum version), policy guard removed too", [
        ('TLS_MIN = {"1.2": ssl.TLSVersion.TLSv1_2,', 'TLS_MIN = {"1.2": ssl.TLSVersion.TLSv1_1,'),
        ('    _check_client_policy(ctx)\n    return ctx', '    ctx.set_ciphers("DEFAULT:@SECLEVEL=0")\n    return ctx'),
    ], "TestRealTLSVersions"),
    ("P4 rejected presentation context reported as accepted (worker)", [
        ('            if result == 0:\n                c["result"] = "accepted"', '            if True:\n                c["result"] = "accepted"'),
    ], "TestRealAssociateNegotiation"),
    ("P5 old-version handshake reported as refused (worker probe)", [
        ('finish("handshake_completed", f"handshake completed with {sock.version()}")',
         'finish("handshake_alert", f"handshake completed with {sock.version()}")'),
    ], "TestRealTLSReject"),
    ("P6 Storage SCP accepts targets without a client certificate", [
        ('ctx.verify_mode = ssl.CERT_OPTIONAL if args.scp_client_cert == "optional" else ssl.CERT_REQUIRED',
         'ctx.verify_mode = ssl.CERT_OPTIONAL'),
    ], "TestRealSCPTLSDuringMove"),
    ("P7 abort on release recorded as released", [
        ('        xs.done(x, "error", "protocol", f"the target aborted instead of releasing (A-ABORT source {src}, reason {reason})")\n        return',
         '        info["released"] = True\n        xs.done(x)\n        return'),
    ], "TestRealAssociateNegotiation"),
    ("P8 A-ABORT answer reported as a rejection (worker)", [
        ('        info["result"] = "aborted"\n        info["abort"] = {"source": src, "reason": reason}\n        xs.done(x, "error", "protocol", f"the target aborted the association (A-ABORT source {src}, reason {reason})")',
         '        info["result"] = "rejected"\n        info["reject"] = {"result": 1, "source": 1, "reason": 7}\n        xs.done(x, "refused")'),
    ], "TestRealAssociateRejectAbortCloseGarbageSilence"),
    ("P9 expired certificate accepted (verification off in the worker's trust load)", [
        ('            ctx.load_verify_locations(cafile=args.ca_file)\n        else:\n            ctx.load_default_certs(ssl.Purpose.SERVER_AUTH)',
         '            ctx.load_verify_locations(cafile=args.ca_file)\n            ctx.verify_flags |= ssl.VERIFY_X509_PARTIAL_CHAIN\n            ctx.verify_flags |= 0x200000\n        else:\n            ctx.load_default_certs(ssl.Purpose.SERVER_AUTH)'),
    ], "TestRealTLSVerification"),
]


def log(msg):
    with open(LOG, "a") as f:
        f.write(msg + "\n")
    print(msg, flush=True)


def sh(cmd, cwd=None, timeout=900):
    try:
        p = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout)
        return p.returncode, p.stdout + p.stderr
    except subprocess.TimeoutExpired as e:
        return 124, str(e)


def go_test(pkg, run):
    return sh(["rws-go", "test", "-count=1", "-timeout", "120s", "-run", run, pkg], cwd=WT)


def py_test(worker, run):
    uid = os.getuid()
    gid = os.getgid()
    cmd = ["docker", "run", "--rm", "-u", f"{uid}:{gid}", "-v", "rws-gocache:/cache", "-v", f"{WT}:/src:ro",
           "-v", f"{worker}:/opt/libredvt/worker/libredvt_worker.py:ro", "-w", "/src", "-e", "HOME=/tmp",
           "-e", "GOCACHE=/cache/build", "-e", "GOMODCACHE=/cache/mod", "-e", "GOFLAGS=-mod=mod",
           IMAGE, "go", "test", "-count=1", "-timeout", "300s", "-tags", "engines", "-run", run,
           "./modules/dvt/internal/runner/"]
    return sh(cmd, timeout=600)


def tail(out, n=6):
    lines = [l for l in out.splitlines() if l.strip()]
    return "\n      ".join(lines[-n:])


results = []
log(f"== mutation check {time.strftime('%Y-%m-%d %H:%M:%S %Z')}, head {sh(['git', 'rev-parse', 'HEAD'], cwd=WT)[1].strip()}")
# Baselines: every test used below must pass unmutated.
rc, out = go_test("./modules/dvt/...", ".")
log(f"baseline Go unit tests: rc={rc}")
if rc != 0:
    log(tail(out, 20))
    sys.exit(2)
base_worker = os.path.join(WT, "modules/dvt/worker/libredvt_worker.py")
rc, out = py_test(base_worker, "TestReal")
log(f"baseline real-engine tests (TestReal*): rc={rc}")
if rc != 0:
    log(tail(out, 20))
    sys.exit(2)

for mid, path, old, new, pkg, run in GO:
    f = os.path.join(WT, path)
    src = open(f).read()
    if src.count(old) != 1:
        log(f"{mid}: MUTANT NOT APPLIED (pattern count {src.count(old)})")
        results.append((mid, "NOT APPLIED"))
        continue
    open(f, "w").write(src.replace(old, new))
    rc, out = go_test(pkg, run)
    open(f, "w").write(src)
    verdict = "KILLED" if rc != 0 and "build failed" not in out and "[setup failed]" not in out else ("BUILD FAILED" if rc != 0 else "SURVIVED")
    log(f"{mid}: {verdict} (rc={rc})\n      {tail(out)}")
    results.append((mid, verdict))

for mid, edits, run in PY:
    src = open(base_worker).read()
    mut = src
    ok = True
    for old, new in edits:
        if mut.count(old) != 1:
            ok = False
            break
        mut = mut.replace(old, new)
    if not ok:
        log(f"{mid}: MUTANT NOT APPLIED")
        results.append((mid, "NOT APPLIED"))
        continue
    mw = os.path.join(SCR, "mutant_worker.py")
    open(mw, "w").write(mut)
    rc, out = py_test(mw, run)
    verdict = "KILLED" if rc != 0 and "SyntaxError" not in out and "build failed" not in out else ("BROKEN" if rc != 0 else "SURVIVED")
    log(f"{mid}: {verdict} (rc={rc})\n      {tail(out)}")
    results.append((mid, verdict))

rc, out = sh(["git", "status", "--porcelain"], cwd=WT)
log(f"worktree clean after run: {'yes' if out.strip() == '' else 'NO: ' + out}")
k = sum(1 for _, v in results if v == "KILLED")
log(f"== {k} of {len(results)} mutants killed")
for mid, v in results:
    log(f"   {v:12s} {mid}")
