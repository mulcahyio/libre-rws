#!/bin/bash
# Mutation check for LibreDVT v0.2 card DVT-0.2/2 (issue #34): strict target
# and plan schema, production guard for STOW-RS/QIDO-RS/WADO-RS, protocol
# check, secret-file preflight, verdict rule, redaction and the worker
# argv/environment guard. Each mutant breaks one control in a way that
# still compiles; the package's tests must then fail. Every file is
# restored after its mutant.
#
#   bash docs/dhf/libredvt/evidence/v0.2-foundation/scripts/mutation-check.sh   # from the repository root
cd "$(git rev-parse --show-toplevel)" || exit 1
B=${DVT_WORK:-${TMPDIR:-/tmp}/libredvt-mutants}
mkdir -p "$B"
H=docs/dhf/libredvt/evidence/v0.2-foundation/scripts/replace.py
killed=0 total=0
echo "LibreDVT v0.2 foundation mutation check, commit $(git rev-parse --short HEAD), $(date -u +%Y-%m-%dT%H:%M:%SZ)"
mutate() { # name package file old new [old2 new2]
  local name=$1 pkg=$2 f=$3
  shift 3
  cp "$f" "$B/orig"
  if ! python3 "$H" "$f" "$@"; then
    echo "$name: PATTERN NOT FOUND"
    cp "$B/orig" "$f"
    total=$((total + 1))
    return
  fi
  timeout 300 rws-go test -count=1 -timeout 90s "./modules/dvt/$pkg/" > "$B/$name.log" 2>&1
  local rc=$?
  cp "$B/orig" "$f"
  total=$((total + 1))
  local res=survived
  # A mutant that does not compile proves nothing: count only test failures.
  if grep -q "build failed\|cannot use\|undefined:\|declared and not used\|imported and not used" "$B/$name.log"; then
    res="BUILD-FAILED"
  elif [ $rc -ne 0 ] && grep -q -- "--- FAIL" "$B/$name.log"; then
    res=killed
    killed=$((killed + 1))
  fi
  echo "$name: $res (exit $rc) :: $(grep -m1 -E -- '--- FAIL' "$B/$name.log" | sed 's/^ *//' | cut -c1-90)"
}

R=modules/dvt/internal
# Verdict rule (DVT-SRS-144)
mutate M01-zero-assertions-pass internal/result $R/result/evidence.go 'if e == nil || len(e.Errors) > 0 || len(e.Assertions) == 0 {' 'if e == nil || len(e.Errors) > 0 {'
mutate M02-problems-ignored internal/result $R/result/evidence.go 'if e == nil || len(e.Errors) > 0 || len(e.Assertions) == 0 {' 'if e == nil || len(e.Assertions) == 0 {'
mutate M03-failed-assertion-passes internal/result $R/result/evidence.go '		case AssertPass:
		case AssertWarn:' '		case AssertPass, AssertFail:
		case AssertWarn:'
mutate M04-warning-flipped-to-pass internal/result $R/result/evidence.go '	if warn {
		return PassWithWarnings
	}
	return Pass
}

// Check' '	if warn {
		return Pass
	}
	return Pass
}

// Check'
mutate M05-status-ignores-verdict internal/result $R/result/evidence.go '	if s.Verdict.Passed() {
		s.Status = StepPassed' '	if true {
		s.Status = StepPassed'
mutate M06-run-drops-warnings internal/result $R/result/result.go '			r.Verdict = PassWithWarnings' '			r.Verdict = Pass'
mutate M07-evidence-check-off internal/result $R/result/evidence.go '		if !oneOf(x.Protocol, "tls", "dimse", "http") {' '		if !oneOf(x.Protocol, "tls", "dimse", "http") && false {'
# Strict plan schema (DVT-SRS-140, -142)
mutate M08-plan-unknown-fields internal/plan $R/plan/plan.go '	dec.KnownFields(true)' '	dec.KnownFields(false)'
mutate M09-plan-other-type-keys internal/plan $R/plan/plan.go '		if !ok[k] {' '		if !ok[k] && false {'
mutate M10-plan-protocol-check internal/plan $R/plan/plan.go '		case want != protocol:' '		case want != protocol && false:'
mutate M11-plan-limits-range internal/plan $R/plan/plan.go '		if *v < lo || *v > hi {' '		if false {'
mutate M12-plan-qido-one-expectation internal/plan $R/plan/plan.go '	if n != 1 {' '	if n > 1 {'
mutate M13-plan-lossy-compare internal/plan $R/plan/plan.go '	if s.ComparePixels && lossyTS[s.TransferSyntax] {' '	if s.ComparePixels && lossyTS[s.TransferSyntax] && false {'
# Production guard and protocol check in the runner (DVT-SRS-134, -142)
mutate M14-guard-back-to-v01 internal/runner $R/runner/runner.go '	if plan.LabOnly(ps.Type) && t.Class != store.Lab {' '	if (ps.Type == plan.Store || ps.Type == plan.Move) && t.Class != store.Lab {'
mutate M15-stow-not-lab-only internal/runner $R/plan/plan.go '	case Store, Move, Qido, Wado, Stow:
		return true' '	case Store, Move, Qido, Wado:
		return true'
mutate M16-protocol-mismatch-runs internal/runner $R/runner/runner.go '			rejected = true' '			rejected = false'
# Secret files before any connection (DVT-SRS-141, -150)
mutate M17-secret-preflight-off internal/runner $R/runner/runner.go '	if err := t.LoadSecrets(rn.SecretsDir, secrets); err != nil {' '	if err := t.LoadSecrets(rn.SecretsDir, secrets); err != nil && false {'
mutate M18-dimse-tls-cleartext internal/runner $R/runner/runner.go '	if t.Proto() == store.DIMSE && t.TLS != nil {' '	if t.Proto() == store.DIMSE && t.TLS != nil && false {'
mutate M19-regular-file-check internal/store $R/store/target.go '	if !info.Mode().IsRegular() {' '	if false {'
mutate M20-blank-file-check internal/store $R/store/target.go '	if len(bytes.TrimSpace(b)) == 0 {' '	if false {'
mutate M21-secrets-dir-off internal/store $R/store/target.go '	if secretsDir != "" {' '	if false {'
# Strict target schema (DVT-SRS-110, -112, -140)
mutate M22-registry-unknown-fields internal/store $R/store/target.go '	dec.DisallowUnknownFields()' '	_ = dec'
mutate M23-target-file-unknown-fields internal/store $R/store/target.go '	dec.KnownFields(true)' '	dec.KnownFields(false)'
mutate M24-dicomweb-takes-dimse-fields internal/store $R/store/target.go '		if set {
			add("%s is not allowed on a dicomweb target", f)' '		if set && false {
			add("%s is not allowed on a dicomweb target", f)'
mutate M25-bearer-http-anywhere internal/store $R/store/target.go '		if u != nil && u.Scheme == "http" && !IsLoopback(u.Hostname()) {' '		if u != nil && u.Scheme == "http" && !IsLoopback(u.Hostname()) && false {'
mutate M26-url-userinfo internal/store $R/store/target.go '	if u.User != nil {' '	if u.User != nil && false {'
# Redaction (DVT-SRS-152)
mutate M27-step-redaction-off internal/runner $R/runner/runner.go '		s.Redact(secrets)' '		_ = secrets'
mutate M28-run-redaction-off internal/runner $R/runner/runner.go '	r.Redact(secrets)
	return r' '	_ = secrets
	return r'
mutate M29-no-base64-forms internal/runner $R/redact/redact.go '		base64.StdEncoding.EncodeToString(b),
' '' '		base64.RawStdEncoding.EncodeToString(b),
' '' '		base64.URLEncoding.EncodeToString(b),
' '' '		base64.RawURLEncoding.EncodeToString(b),
' ''
mutate M30-scanner-never-replaces internal/redact $R/redact/redact.go '		if c == 0 {
			continue
		}' '		if c >= 0 {
			continue
		}'
mutate M31-render-both-layers-off internal/report $R/report/render.go '	hits = r.Redact(secrets)' '	hits = 0' '		a.JSON, n = secrets.Bytes(a.JSON)' '		n = 0'
# (The CLI and web writers call report.Render; the runner already redacts
# the run, so switching off only the writer's scan is caught by M31 in
# Render's own test, not by an end-to-end test: the layers back each other up.)
# Worker argv/environment (DVT-SRS-153)
mutate M32-argv-check-off internal/engine $R/engine/secrets.go '		if s.Contains(a) {' '		if s.Contains(a) && false {'
mutate M33-env-check-off internal/engine $R/engine/secrets.go '		if s.Contains(e) {' '		if s.Contains(e) && false {'

echo "killed $killed of $total"
git diff --quiet HEAD -- modules && echo "modules restored: clean" || echo "MODULES DIRTY"
