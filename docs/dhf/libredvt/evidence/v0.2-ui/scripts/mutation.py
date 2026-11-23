#!/usr/bin/env python3
"""LibreDVT v0.2 card DVT-0.2/5: mutation check of the UI, CLI and report controls.

Each mutant changes one control in the source, builds, and runs the tests
that should catch it. A mutant is killed when those tests fail (and the code
still compiles: a compile error proves nothing and is reported as such).
Every file is restored with `git checkout` after its mutant; at the end the
module tree must equal HEAD.

    python3 docs/dhf/libredvt/evidence/v0.2-ui/scripts/mutation.py   # from the repository root
"""
import subprocess
import sys

M = "modules/dvt/"
RWS = ["rws-go"]

MUTANTS = [
    # id, file, old, new, test package, -run pattern, what it breaks
    ("U1", M + "internal/report/report.html.tmpl", '{{with .Peer}}{{template "cert-dl" .}}{{end}}', "",
     "./modules/dvt/internal/report/", "Real", "per-exchange TLS certificate facts dropped from the HTML report"),
    ("U2", M + "internal/report/report.html.tmpl", '{{.RemoteResult}}</span>{{end}}{{if .Reason}} {{.Reason}}{{end}}', "</span>{{end}}",
     "./modules/dvt/internal/report/", "Real|Failures", "per-instance remote result and reason dropped"),
    ("U3", M + "internal/report/report.html.tmpl", '<td class="wrap">{{.Actual}}</td>', '<td class="wrap">{{.Expected}}</td>',
     "./modules/dvt/internal/report/", "Failures", "assertion table shows the expected value as the actual value"),
    ("U4", M + "internal/report/report.html.tmpl", "{{if .ImplClassUID}}<dt>Implementation Class UID</dt>", "{{if false}}<dt>Implementation Class UID</dt>",
     "./modules/dvt/internal/report/", "Real", "Implementation Class UID dropped from the association section"),
    ("U5", M + "internal/report/report.go", "c.Properties = append(c.Properties, junitEvidence(st)...)", "_ = junitEvidence",
     "./modules/dvt/internal/report/", "Real|Failures", "JUnit loses the evidence properties"),
    ("U6", M + "internal/report/report.go", 'out = append(out, "  "+AssertionLine(a))', '_ = a',
     "./modules/dvt/internal/report/", "Real", "JUnit system-out loses the assertions"),
    ("U7", M + "internal/report/evidence.go", "if t.Verified {\n\t\t\t\t\t\tc.Verified, c.VerifyError = true, \"\"", "if !t.Verified {\n\t\t\t\t\t\tc.Verified, c.VerifyError = true, \"\"",
     "./modules/dvt/internal/report/", "Identity|Failures", "certificate verification inverted in the endpoint identity"),
    ("U8", M + "internal/report/evidence.go", "case !at.Before(na):", "case at.After(na):",
     "./modules/dvt/internal/report/", "Identity", "certificate counted valid at its NotAfter instant"),
    ("U9", M + "internal/report/evidence.go", "if len(id.TLSVersions) == 0 && len(id.Certs) == 0 && len(id.Impl) == 0 {\n\t\treturn nil", "if false {\n\t\treturn nil",
     "./modules/dvt/internal/report/", "V01|Identity", "v0.1 runs get an (empty) identity card: v0.1 report changes"),
    ("U10", M + "internal/report/report.html.tmpl", "    {{- with $r.TargetInfo}}\n", "    {{- with $r.TargetInfo}}{{end}}{{with $r}}\n",
     "./modules/dvt/internal/report/", "V01|Failures", "target rows rendered for every run, v0.1 included (template error or changed v0.1 report)"),
    ("W1", M + "internal/web/server.go", "if !targetFields[k] {", "if false && !targetFields[k] {",
     "./modules/dvt/internal/web/", "TargetForms|TargetsPage", "unknown form fields (inline token, verification switch) accepted"),
    ("W2", M + "internal/web/server.go", 'if g("tls") == "on" || tl != (store.TLS{}) {', 'if g("tls") == "on" {',
     "./modules/dvt/internal/web/", "TargetForms", "TLS fields without the check box silently mean clear text"),
    ("W3", M + "internal/web/server.go", "err = t.CheckRefsUnder(s.secretsDir())", "_ = t.CheckRefsUnder(s.secretsDir())",
     "./modules/dvt/internal/web/", "TargetsPage", "referenced file outside secrets_dir accepted when saved"),
    ("W4", M + "internal/store/target.go", 'return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))', 'return err == nil && rel != ""',
     "./modules/dvt/internal/web/", "TargetsPage", "secrets_dir containment check always true"),
    ("W5", M + "internal/web/server.go", "if err := t.LoadSecrets(s.secretsDir(), &redact.Set{}); err != nil {", "if err := t.LoadSecrets(s.secretsDir(), &redact.Set{}); err != nil && false {",
     "./modules/dvt/internal/web/", "TargetsPage", "targets page reports every referenced file as ok"),
    ("W6", M + "internal/web/server.go", "\tfor k, v := range f {\n\t\tif len(v) > 1 {", "\tfor k, v := range f {\n\t\tif len(v) > 1 && false {",
     "./modules/dvt/internal/web/", "TargetForms", "a repeated form field is accepted (first value silently wins)"),
    ("C1", M + "internal/runner/runner.go", "\t\tr.TargetInfo = &info\n", "\t\t_ = info\n",
     "./modules/dvt/cmd/libredvt/", "SameResult", "runs lose the target's protocol, endpoint and class"),
    ("C2", M + "cmd/libredvt/main.go", 'rn := cfg.runner(work, "")', 'rn := cfg.runner(work, "/nonexistent-secrets")',
     "./modules/dvt/cmd/libredvt/", "SameResult", "CLI runner configured differently from the web runner (parity broken)"),
    ("P1", M + "plans/generic-dicomweb-audit.yaml", "  max_body_excerpt_bytes: 0\n", "  max_body_excerpt_bytes: 65536\n",
     "./modules/dvt/internal/plan/", "Bundled", "DICOMweb audit keeps response-body excerpts"),
    ("P2", M + "plans/libredcm-smoke-tls.yaml", '    tls_version: "1.1"', '    tls_version: "1.0"',
     "./modules/dvt/internal/plan/", "Bundled", "LibreDCM smoke no longer checks TLS 1.1 refusal"),
    ("P3", M + "plans/generic-dicom-tls-audit.yaml", "  - id: echo-over-tls\n    type: echo\n", "  - id: echo-over-tls\n    type: store\n    files: [fixtures/ct_explicit_le.dcm]\n",
     "./modules/dvt/internal/plan/", "Bundled", "DICOM TLS audit writes to the target (lab-only step)"),
]


def sh(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def main():
    # Baselines: every package used must pass unmutated.
    pkgs = sorted({m[4] for m in MUTANTS})
    for p in pkgs:
        r = sh(RWS + ["test", "-count=1", "-timeout", "300s", p])
        print(f"baseline {p}: {'ok' if r.returncode == 0 else 'FAIL'}")
        if r.returncode != 0:
            print(r.stdout[-3000:], r.stderr[-2000:])
            return 2
    killed = survived = broken = 0
    for mid, path, old, new, pkg, run, what in MUTANTS:
        src = open(path).read()
        if src.count(old) != 1:
            print(f"{mid}: SETUP ERROR, pattern found {src.count(old)} times in {path}")
            broken += 1
            continue
        open(path, "w").write(src.replace(old, new))
        try:
            v = sh(RWS + ["vet", pkg]) if path.endswith(".go") else None
            if v is not None and v.returncode != 0:
                print(f"{mid}: DOES NOT COMPILE ({what})\n{v.stderr[-1500:]}")
                broken += 1
                continue
            r = sh(RWS + ["test", "-count=1", "-timeout", "300s", "-run", run, pkg])
            if r.returncode != 0:
                killed += 1
                fails = [ln.strip() for ln in r.stdout.splitlines() if ln.strip().startswith("--- FAIL")]
                print(f"{mid}: killed  ({what}) by {', '.join(fails[:4]) or 'test failure'}")
            else:
                survived += 1
                print(f"{mid}: SURVIVED ({what})")
        finally:
            sh(["git", "checkout", "--", path])
    clean = sh(["git", "diff", "--quiet", "HEAD", "--", "modules"]).returncode == 0
    print(f"\n{killed} of {len(MUTANTS)} mutants killed, {survived} survived, {broken} invalid; module tree {'clean' if clean else 'DIRTY'}")
    return 0 if killed == len(MUTANTS) and clean else 1


if __name__ == "__main__":
    sys.exit(main())
