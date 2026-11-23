#!/usr/bin/env python3
"""Mutation check for card DVT-0.2/4 (DICOMweb QIDO-RS, WADO-RS, STOW-RS).

Go mutants are applied in a git worktree of HEAD (the main tree is never
touched) and checked with the unit tests (rws-go). Python worker mutants
are applied to a scratch copy of the worker, which is mounted over the
image's worker in a test container that runs the real-engine tests against
the Go DICOMweb test server. Every mutant must make a test fail; a mutant
that survives is reported as SURVIVED. Mutants are written so they still
compile/run.

usage: mutation.py WORKTREE TESTIMAGE LOG
"""
import os
import subprocess
import sys
import time

WT, IMAGE, LOG = sys.argv[1], sys.argv[2], sys.argv[3]
SCR = os.path.dirname(os.path.abspath(LOG))
R = "modules/dvt/internal/runner/dicomweb.go"
EN = "modules/dvt/internal/engine/dicomweb.go"
RP = "./modules/dvt/internal/runner/"
EP = "./modules/dvt/internal/engine/"

GO = [
    # id, file, old, new, package, -run
    ("G1 expected QIDO UID not checked", R,
     'ev.Assert("qido.uid.present", u, "present", got, "")', 'ev.Assert("qido.uid.present", u, "present", "present", got)',
     RP, "TestQidoPositiveControls"),
    ("G2 QIDO Content-Type not checked", R,
     '\t\tev.Assert("qido.content_type", subj, mediaDICOMJSON, mt, "")\n\t\tev.AssertIf("qido.json"',
     '\t\t_ = mt\n\t\tev.AssertIf("qido.json"', RP, "TestQidoPositiveControls"),
    ("G3 returned object not matched against the query", R,
     'if !valueMatches(o.Values[q.Tag], q.Value, q.VR) {', 'if false && !valueMatches(o.Values[q.Tag], q.Value, q.VR) {',
     RP, "TestQidoPositiveControls"),
    ("G4 page larger than limit accepted", R,
     'len(pg.Objects) <= *ps.Limit, "")', 'true, "")', RP, "TestQidoNoMatchAndPaging"),
    ("G5 endless paging accepted", R, 'if res.PageLimitReached {', 'if false {', RP, "TestQidoNoMatchAndPaging"),
    ("G6 redirect not a problem", R, 'x.HTTP.Status >= 300 && x.HTTP.Status < 400', 'x.HTTP.Status >= 300 && x.HTTP.Status < 300',
     RP, "TestQidoPositiveControls"),
    ("G7 QIDO unique key not checked", R, 'if v := o.Values[key]; len(v) == 0 || v[0] == "" {', 'if v := o.Values[key]; false {',
     RP, "TestQidoPositiveControls"),
    ("G8 required attribute not checked", R, 'if !present[tag] {', 'if false && !present[tag] {', RP, "TestQidoPositiveControls"),
    ("G9 expect_none passes with matches", R, 'ok := pg.Status == 204 ||', 'ok := true || pg.Status == 204 ||',
     RP, "TestQidoNoMatchAndPaging"),
    ("G10 WADO 206 accepted", R, 'ev.Assert(o.prefix+".status", subj, "200", statusStr(x.HTTP.Status), "")',
     'ev.Assert(o.prefix+".status", subj, "200", "200", "")', RP, "TestStowThenQidoWadoFromStore"),
    ("G11 missing WADO instance ignored", R,
     'ev.AssertIf(o.prefix+".uid.set", u, "received once", "missing", false, "")',
     'ev.AssertIf(o.prefix+".uid.set", u, "received once", "missing", true, "")', RP, "TestStowThenQidoWadoFromStore"),
    ("G12 extra WADO instance ignored", R,
     'ev.AssertIf(o.prefix+".uid.set", u, "not expected", "received", false, "unexpected instance")',
     'ev.AssertIf(o.prefix+".uid.set", u, "not expected", "received", true, "unexpected instance")', RP, "TestStowThenQidoWadoFromStore"),
    ("G13 duplicate WADO instance ignored", R,
     'ev.AssertIf(o.prefix+".uid.set", u, "received once", fmt.Sprintf("received %d times", got[u]), false, "")',
     'ev.AssertIf(o.prefix+".uid.set", u, "received once", fmt.Sprintf("received %d times", got[u]), true, "")', RP, "TestStowThenQidoWadoFromStore"),
    ("G14 requested transfer syntax not checked", R,
     'ev.Assert(o.prefix+".part.transfer_syntax", pt.SOPInstanceUID, o.ts, pt.TransferSyntax,',
     'ev.Assert(o.prefix+".part.transfer_syntax", pt.SOPInstanceUID, o.ts, o.ts,', RP, "TestStowThenQidoWadoFromStore"),
    ("G15 part transfer-syntax header not compared", R, 'if pt.TSParam != "" {', 'if false {', RP, "TestStowThenQidoWadoFromStore"),
    ("G16 part media type not checked (Go assertion and worker error, whole family)", R,
     '\t\tev.Assert(o.prefix+".part.content_type", subj, mediaDICOM, mt, "")\n\t\tif pt.Error != "" {\n\t\t\tev.Problemf(result.ProblemProtocol, "%s: %s", subj, pt.Error)\n\t\t\tcontinue\n\t\t}',
     '\t\t_ = mt\n\t\tif pt.Error != "" {\n\t\t\tcontinue\n\t\t}', RP, "TestStowThenQidoWadoFromStore"),
    ("G17 pixel difference ignored", R,
     'ev.AssertIf(prefix+".pixels", subj, "pixel data identical to the sent file", actual, ok, "")',
     'ev.AssertIf(prefix+".pixels", subj, "pixel data identical to the sent file", actual, ok || true, "")', RP, "TestStowThenQidoWadoFromStore|TestStowVerifyRetrieve"),
    ("G18 metadata attribute not compared", R,
     'ev.Assert("wado.metadata.attribute", o.SOPInstanceUID+" "+k, o.Attrs[tag], got,',
     'ev.Assert("wado.metadata.attribute", o.SOPInstanceUID+" "+k, o.Attrs[tag], o.Attrs[tag]+got[:0],', RP, "TestWadoMetadata"),
    ("G19 failed STOW instances ignored", R,
     '\t\tx := res.Exchanges[q.Exchange-1]\n\t\tsubj := x.Endpoint\n\t\tif x.Outcome',
     '\t\tq.Failed = nil\n\t\tx := res.Exchanges[q.Exchange-1]\n\t\tsubj := x.Endpoint\n\t\tif x.Outcome', RP, "TestStowPositiveControls"),
    ("G20 STOW instance missing from the Referenced SOP Sequence ignored", R,
     'ev.AssertIf("stow.instance.referenced", uid, "in the Referenced SOP Sequence once", "missing", false, "")',
     'ev.AssertIf("stow.instance.referenced", uid, "in the Referenced SOP Sequence once", "missing", true, "")', RP, "TestStowPositiveControls"),
    ("G21 STOW warning passes without allow_warnings", R,
     'ev.AssertIf("stow.instance.warning", subj, "no warning", what, false, "set allow_warnings to accept warnings")',
     'ev.AssertIf("stow.instance.warning", subj, "no warning", what, true, "set allow_warnings to accept warnings")', RP, "TestStowPositiveControls"),
    ("G22 extra referenced UID ignored", R, 'if !sentHere[u] {\n\t\t\t\tev.AssertIf("stow.instance.extra"',
     'if false && !sentHere[u] {\n\t\t\t\tev.AssertIf("stow.instance.extra"', RP, "TestStowPositiveControls"),
    ("G23 STOW response type not checked", R, 'if q.JSON != "object" {', 'if false {', RP, "TestStowPositiveControls"),
    ("G24 STOW SOP Class not compared", R,
     'ok := ev.Assert("stow.instance.referenced", uid, "referenced with SOP Class "+f.SOPClassUID, "referenced with SOP Class "+r.SOPClassUID, "")',
     'ok := ev.Assert("stow.instance.referenced", uid, "referenced with SOP Class "+f.SOPClassUID, "referenced with SOP Class "+f.SOPClassUID, "")', RP, "TestStowPositiveControls"),
    ("G25 non-DICOM file sent", R, 'if !pr.DICOM || pr.SOPInstanceUID == "" || pr.TransferSyntaxUID == "" {', 'if false {',
     RP, "TestStowRefusesNonDICOMWithoutSending"),
    ("G26 bearer token over clear text off-host allowed (runner second line)", R,
     'if u.Scheme != "https" && !store.IsLoopback(u.Hostname()) {', 'if false {', RP, "TestBearerOverClearTextRefusedBeforeEngine"),
    ("G27 any HTTP reply counted as a refusal", R, 'if status == 400 {', 'if status >= 200 {', RP, "TestTLSRejectStep"),
    ("G28 HTTP exchanges not schema-checked (qido)", EN,
     'if err := checkWebExchanges(r.Exchanges, https); err != nil {\n\t\treturn fmt.Errorf("worker qido: %w", err)',
     'if err := checkWebExchanges(r.Exchanges, https); err != nil && false {\n\t\treturn fmt.Errorf("worker qido: %w", err)', EP, "TestQidoOutputSchemaFailsClosed"),
    ("G29 unredacted Authorization accepted", EN, 'if v != result.Redacted {', 'if v != result.Redacted && false {', EP, "TestQidoOutputSchemaFailsClosed"),
    ("G30 STOW output with requests after an unusable file accepted", EN,
     'return errors.New("worker stow: requests sent although a file or setting was unusable")', 'return nil',
     EP, "TestStowOutputSchemaFailsClosed"),
    ("G31 probe that sent headers accepted", EN, 'case x.HTTP != nil && len(x.HTTP.RequestHeaders) > 0:', 'case false:',
     EP, "TestTLSProbeWebSchema"),
]

PY = [
    ("P1 truncated multipart accepted (no closing delimiter)", [
        ('raise ValueError(f"part {len(parts) + 1}: no closing boundary delimiter (truncated body)")',
         'parts.append((headers, body[dstart:]))\n                return parts'),
    ], "TestRealWadoMalformedResponsesFail"),
    ("P2 boundary taken from the body when the header's is absent", [
        ('raise ValueError("the body does not contain the boundary from the Content-Type")',
         'delim = body.split(b"\\r\\n", 1)[0]\n            pos = 0'),
    ], "TestRealWadoMalformedResponsesFail"),
    ("P3 boundary text inside part data splits the part", [
        ('k = body.find(b"\\r\\n" + delim, k)', 'k = body.find(delim, k) - 2'),
        ('if rest == b"--" or body[m:m + 2] == b"\\r\\n" or after == len(body):', 'if True:'),
    ], "TestRealWadoMalformedResponsesFail"),
    ("P4 Authorization value written to the evidence", [
        ('rec.setdefault(k, []).append(REDACTED if k.lower() == "authorization" else v)', 'rec.setdefault(k, []).append(v)'),
    ], "TestRealDICOMwebHappyPath"),
    ("P5 DICOM JSON size limit ignored", [
        ('def _load_json(data, limit):\n    if len(data) > limit:', 'def _load_json(data, limit):\n    if False:'),
    ], "TestRealQidoMalformedResponsesFail"),
    ("P6 Failed SOP Sequence not read", [
        ('for it in _seq_items(doc, "00081198"):', 'for it in []:'),
    ], "TestRealStowControls"),
    ("P7 truncated body (short Content-Length) accepted", [
        ('            if resp.length:  # Content-Length promised more than arrived', '            if False:'),
    ], "TestRealQidoMalformedResponsesFail"),
    ("P8 paging stops after the first page", [
        ('            offset += args.limit', '            break'),
    ], "TestRealDICOMwebHappyPath"),
    ("P9 STOW sends changed bytes", [
        ('b"\\r\\nContent-Type: application/dicom\\r\\n\\r\\n" + fh.read() + b"\\r\\n"',
         'b"\\r\\nContent-Type: application/dicom\\r\\n\\r\\n" + (lambda d: d[:-1] + bytes([d[-1] ^ 1]))(fh.read()) + b"\\r\\n"'),
    ], "TestRealDICOMwebHappyPath"),
    ("P10 refusal probe sends an Authorization header", [
        ('f"Connection: close\\r\\n\\r\\n".encode("latin-1"))', 'f"Connection: close\\r\\nAuthorization: Bearer probe\\r\\n\\r\\n".encode("latin-1"))'),
    ], "TestRealDICOMwebOverTLS"),
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
    cmd = ["docker", "run", "--rm", "-u", f"{os.getuid()}:{os.getgid()}", "-v", "rws-gocache:/cache", "-v", f"{WT}:/src:ro",
           "-v", f"{worker}:/opt/libredvt/worker/libredvt_worker.py:ro", "-w", "/src", "-e", "HOME=/tmp",
           "-e", "GOCACHE=/cache/build", "-e", "GOMODCACHE=/cache/mod", "-e", "GOFLAGS=-mod=mod",
           IMAGE, "go", "test", "-count=1", "-timeout", "300s", "-tags", "engines", "-run", run,
           "./modules/dvt/internal/runner/"]
    return sh(cmd, timeout=600)


def tail(out, n=6):
    """The failing-test lines (why the mutant was killed) and the last lines."""
    lines = [l for l in out.splitlines() if l.strip()]
    why = [l for l in lines if "--- FAIL" in l or ", want " in l or "panic:" in l][:8]
    return "\n      ".join(why + ["..."] + lines[-n:] if why else lines[-n:])


results = []
log(f"== mutation check {time.strftime('%Y-%m-%d %H:%M:%S %Z')}, head {sh(['git', 'rev-parse', 'HEAD'], cwd=WT)[1].strip()}")
rc, out = go_test("./modules/dvt/...", ".")
log(f"baseline Go unit tests: rc={rc}")
if rc != 0:
    log(tail(out, 20))
    sys.exit(2)
base_worker = os.path.join(WT, "modules/dvt/worker/libredvt_worker.py")
rc, out = py_test(base_worker, "TestRealDICOMweb|TestRealQido|TestRealWado|TestRealStow")
log(f"baseline real-engine DICOMweb tests: rc={rc}")
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
