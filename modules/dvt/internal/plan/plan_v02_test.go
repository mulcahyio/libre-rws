package plan

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The example plan of plan-schema.md §3.4 plus one step of every v0.2 type.
const webPlan = `
name: Vendor DICOMweb audit
target: vendor-web-lab
timeout: 60s
limits:
  max_parts: 50
  max_body_excerpt_bytes: 0
steps:
  - id: tls-plaintext-refused
    type: tls_reject
    mode: plaintext
  - id: tls11-refused
    type: tls_reject
    mode: tls_version
    tls_version: "1.1"
  - id: stow-fixtures
    type: stow
    files: [fixtures/*.dcm]
    batch_size: 5
    verify_retrieve: true
  - id: qido-studies
    type: qido
    level: studies
    from_store: true
    require_attributes: [StudyInstanceUID, PatientID, StudyDate, 00100010]
  - id: qido-filtered
    type: qido
    level: instances
    study: 1.2.3
    series: 1.2.3.4
    query: {PatientID: "SYNTH*", "00080060": CT}
    includefield: [all]
    fuzzymatching: false
    limit: 10
    offset: 0
    expect_uids: [1.2.3.4.5]
  - id: wado-and-compare
    type: wado
    from_store: true
    compare_pixels: true
  - id: wado-metadata
    type: wado
    level: series
    study: 1.2.3
    series: 1.2.3.4
    metadata: true
    compare_attributes: [PatientID]
    files: [fixtures/a.dcm]
`

const dimsePlan = `
name: DICOM TLS audit
target: vendor-pacs-lab
steps:
  - id: assoc
    type: associate
    contexts:
      - abstract_syntax: 1.2.840.10008.1.1
        transfer_syntaxes: [1.2.840.10008.1.2.1, 1.2.840.10008.1.2]
        expect: accepted
        expect_transfer_syntax: 1.2.840.10008.1.2.1
      - abstract_syntax: 1.2.840.10008.5.1.4.1.1.2
        transfer_syntaxes: [1.2.840.10008.1.2.1]
        expect: rejected
        roles: {scu: true, scp: true}
        expect_roles: {scu: true, scp: false}
    expect:
      max_pdu: 16384
      impl_class_uid: 1.2.3
      impl_version: LIBREDCM_1
    async_ops: {invoked: 1, performed: 1}
  - id: assoc-rejected
    type: associate
    contexts:
      - abstract_syntax: 1.2.840.10008.1.1
        transfer_syntaxes: [1.2.840.10008.1.2]
    expect:
      association: rejected
      reject: {result: 1, source: 1, reason: 7}
  - id: no-client-cert
    type: tls_reject
    mode: no_client_cert
  - id: echo
    type: echo
`

// Verifies: DVT-SRS-142, DVT-SRS-154
func TestParseV02StepTypes(t *testing.T) {
	p, err := Parse([]byte(webPlan), "/plans")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 7 || p.Steps[4].Query["PatientID"] != "SYNTH*" || *p.Steps[4].Limit != 10 || !p.Steps[2].VerifyRetrieve || *p.Steps[2].BatchSize != 5 {
		t.Fatalf("%+v", p.Steps)
	}
	l := DefaultLimits()
	l.MaxParts, l.MaxBodyExcerptBytes = 50, 0
	if p.Limits != l {
		t.Fatalf("limits %+v, want %+v", p.Limits, l)
	}
	p2, err := Parse([]byte(dimsePlan), "/plans")
	if err != nil {
		t.Fatal(err)
	}
	if p2.Steps[0].Contexts[1].ExpectRoles.SCP || !p2.Steps[0].Contexts[1].Roles.SCP || p2.Steps[1].Expect.Reject.Reason != 7 {
		t.Fatalf("%+v", p2.Steps)
	}
	if p2.Limits != DefaultLimits() {
		t.Fatalf("default limits %+v", p2.Limits)
	}
}

func step(typ, body string) string {
	return "name: x\ntarget: t\nsteps:\n  - id: s0\n    type: stow\n    files: [a]\n  - id: s\n    type: " + typ + "\n" + body
}

// Verifies: DVT-SRS-140, DVT-SRS-142, DVT-SRS-154
func TestV02PlanRulesRejectBeforeRunning(t *testing.T) {
	ctx := "    contexts:\n      - abstract_syntax: 1.2.840.10008.1.1\n        transfer_syntaxes: [1.2.840.10008.1.2]\n"
	cases := map[string]struct{ src, want string }{
		// Fields of another step type, also with zero values.
		"fuzzymatching on wado":   {step("wado", "    from_store: true\n    fuzzymatching: false\n"), "fuzzymatching is not allowed for a wado step"},
		"batch_size on qido":      {step("qido", "    level: studies\n    expect_none: true\n    batch_size: 1\n"), "batch_size is not allowed for a qido step"},
		"mode on echo":            {step("echo", "    mode: plaintext\n"), "mode is not allowed for a echo step"},
		"contexts on stow":        {step("stow", "    files: [a]\n    contexts: []\n"), "contexts is not allowed for a stow step"},
		"files on qido":           {step("qido", "    level: studies\n    expect_none: true\n    files: [a]\n"), "files is not allowed for a qido step"},
		"studies on wado":         {step("wado", "    from_store: true\n    studies: [1.2]\n"), "studies is not allowed for a wado step"},
		"verifies typo":           {step("qido", "    level: studies\n    expect_none: true\n    verfies: [DVT-SRS-001]\n"), "field verfies not found"},
		"insecure_skip_verify":    {step("tls_reject", "    mode: plaintext\n    insecure_skip_verify: true\n"), "field insecure_skip_verify not found"},
		"inline token":            {step("qido", "    level: studies\n    expect_none: true\n    token: abc\n"), "field token not found"},
		"unknown limit":           {"name: x\nlimits: {max_everything: 1}\nsteps:\n  - {id: v, type: validate, files: [a]}\n", "field max_everything not found"},
		"limit too low":           {"name: x\nlimits: {max_response_bytes: 10}\nsteps:\n  - {id: v, type: validate, files: [a]}\n", "limits.max_response_bytes must be between"},
		"excerpt too big":         {"name: x\nlimits: {max_body_excerpt_bytes: 2097152}\nsteps:\n  - {id: v, type: validate, files: [a]}\n", "limits.max_body_excerpt_bytes must be between"},
		"pages zero":              {"name: x\nlimits: {max_pages: 0}\nsteps:\n  - {id: v, type: validate, files: [a]}\n", "limits.max_pages must be between"},
		"unknown context field":   {step("associate", ctx+"        expect: accepted\n        colour: red\n"), "field colour not found"},
		"associate no contexts":   {step("associate", "    contexts: []\n"), "associate needs 1-128 contexts"},
		"context without expect":  {step("associate", ctx), "expect must be accepted or rejected"},
		"bad abstract syntax":     {step("associate", "    contexts:\n      - abstract_syntax: CT\n        transfer_syntaxes: [1.2]\n        expect: accepted\n"), "abstract_syntax \"CT\" is not a valid UID"},
		"no transfer syntaxes":    {step("associate", "    contexts:\n      - abstract_syntax: 1.2\n        transfer_syntaxes: []\n        expect: accepted\n"), "needs 1-16 transfer_syntaxes"},
		"expect ts not proposed":  {step("associate", ctx+"        expect: accepted\n        expect_transfer_syntax: 1.2.840.10008.1.2.1\n"), "must be one of the proposed transfer_syntaxes"},
		"expect ts on rejected":   {step("associate", ctx+"        expect: rejected\n        expect_transfer_syntax: 1.2.840.10008.1.2\n"), "expect_transfer_syntax is only allowed with expect: accepted"},
		"expect_roles alone":      {step("associate", ctx+"        expect: accepted\n        expect_roles: {scu: true}\n"), "expect_roles needs roles"},
		"rejected without values": {step("associate", ctx[:len(ctx)]+"    expect: {association: rejected}\n"), "expect.reject is required"},
		"reject on accepted":      {step("associate", ctx+"        expect: accepted\n    expect: {reject: {result: 1, source: 1, reason: 1}}\n"), "expect.reject is only allowed with expect.association: rejected"},
		"bad reject combination":  {step("associate", ctx+"    expect: {association: rejected, reject: {result: 1, source: 2, reason: 7}}\n"), "is not a PS3.8 Table 9-21 combination"},
		"bad association value":   {step("associate", ctx+"        expect: accepted\n    expect: {association: maybe}\n"), "expect.association must be accepted or rejected"},
		"async out of range":      {step("associate", ctx+"        expect: accepted\n    async_ops: {invoked: 70000, performed: 1}\n"), "async_ops values must be 0-65535"},
		"tls_reject no mode":      {step("tls_reject", ""), "tls_reject needs mode"},
		"tls_reject bad mode":     {step("tls_reject", "    mode: none\n"), "mode must be plaintext, tls_version or no_client_cert"},
		"tls version 1.2":         {step("tls_reject", "    mode: tls_version\n    tls_version: \"1.2\"\n"), "tls_version must be \"1.0\" or \"1.1\""},
		"tls version w/o mode":    {step("tls_reject", "    mode: plaintext\n    tls_version: \"1.0\"\n"), "tls_version is only allowed with mode: tls_version"},
		"qido no level":           {step("qido", "    expect_none: true\n"), "qido needs level"},
		"qido bad level":          {step("qido", "    level: patients\n    expect_none: true\n"), "level must be studies, series or instances"},
		"qido no expectation":     {step("qido", "    level: studies\n"), "exactly one of expect_uids, from_store or expect_none"},
		"qido two expectations":   {step("qido", "    level: studies\n    expect_none: true\n    from_store: true\n"), "exactly one of expect_uids, from_store or expect_none"},
		"qido study on studies":   {step("qido", "    level: studies\n    study: 1.2\n    expect_none: true\n"), "study and series are not allowed with level: studies"},
		"qido series w/o study":   {step("qido", "    level: instances\n    series: 1.2\n    expect_none: true\n"), "series needs study"},
		"qido bad keyword":        {step("qido", "    level: studies\n    query: {\"Patient Name\": x}\n    expect_none: true\n"), "is not a DICOM keyword"},
		"qido control char":       {step("qido", "    level: studies\n    query: {PatientID: \"a\\nb\"}\n    expect_none: true\n"), "must be at most 1024 printable characters"},
		"qido all with others":    {step("qido", "    level: studies\n    includefield: [all, PatientID]\n    expect_none: true\n"), "includefield all must stand alone"},
		"qido limit 0":            {step("qido", "    level: studies\n    limit: 0\n    expect_none: true\n"), "limit must be 1-1000"},
		"qido offset w/o limit":   {step("qido", "    level: studies\n    offset: 5\n    expect_none: true\n"), "offset needs limit"},
		"qido negative offset":    {step("qido", "    level: studies\n    limit: 5\n    offset: -1\n    expect_none: true\n"), "offset must be 0 or more"},
		"qido bad expect uid":     {step("qido", "    level: studies\n    expect_uids: [x]\n"), "expect_uids \"x\" is not a valid UID"},
		"wado no level":           {step("wado", "    files: [a]\n"), "wado needs level or from_store"},
		"wado level w/o uids":     {step("wado", "    level: instance\n    study: 1.2\n    files: [a]\n"), "level instance needs study, series and instance"},
		"wado no files":           {step("wado", "    level: study\n    study: 1.2\n"), "wado needs files"},
		"wado from_store + uid":   {step("wado", "    from_store: true\n    study: 1.2\n"), "use either from_store or level/study/series/instance/files"},
		"wado lossy compare":      {step("wado", "    from_store: true\n    compare_pixels: true\n    transfer_syntax: 1.2.840.10008.1.2.4.50\n"), "compare_pixels is not allowed with the lossy transfer syntax"},
		"wado bad ts":             {step("wado", "    from_store: true\n    transfer_syntax: jpeg\n"), "transfer_syntax must be a UID or *"},
		"wado metadata+pixels":    {step("wado", "    from_store: true\n    metadata: true\n    compare_pixels: true\n"), "metadata excludes compare_pixels"},
		"compare_attrs w/o meta":  {step("wado", "    from_store: true\n    compare_attributes: [PatientID]\n"), "compare_attributes needs metadata: true"},
		"stow no files":           {step("stow", ""), "stow needs files"},
		"stow batch 0":            {step("stow", "    files: [a]\n    batch_size: 0\n"), "batch_size must be 1-100"},
		"stow batch 101":          {step("stow", "    files: [a]\n    batch_size: 101\n"), "batch_size must be 1-100"},
		"stow bad study":          {step("stow", "    files: [a]\n    study: abc\n"), "study \"abc\" is not a valid UID"},
		"from_store w/o stow":     {"name: x\ntarget: t\nsteps:\n  - id: q\n    type: qido\n    level: studies\n    from_store: true\n", "from_store needs an earlier store or stow step"},
		"no target":               {"name: x\nsteps:\n  - id: q\n    type: tls_reject\n    mode: plaintext\n", "target is required for network steps"},
	}
	for name, c := range cases {
		_, err := Parse([]byte(c.src), "/plans")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error %q, got %v", name, c.want, err)
		}
	}
	// The controls above must fail for their own reason, not for a broken
	// base: the base plan with a valid step parses.
	if _, err := Parse([]byte(step("qido", "    level: studies\n    expect_none: true\n")), "/plans"); err != nil {
		t.Fatalf("base plan rejected: %v", err)
	}
	if _, err := Parse([]byte(step("associate", ctx+"        expect: accepted\n")), "/plans"); err != nil {
		t.Fatalf("base associate rejected: %v", err)
	}
	// The error names the offending field.
	_, err := Parse([]byte(step("wado", "    from_store: true\n    fuzzymatching: false\n")), "/plans")
	if err == nil || !strings.Contains(err.Error(), "fuzzymatching is not allowed for a wado step") {
		t.Fatalf("error does not name the field: %v", err)
	}
}

// Verifies: DVT-SRS-142
func TestCheckTargetMatchesStepTypeToProtocol(t *testing.T) {
	web, _ := Parse([]byte(webPlan), "/plans")
	dimse, _ := Parse([]byte(dimsePlan), "/plans")
	if err := web.CheckTarget("w", ProtocolDICOMweb, true); err != nil {
		t.Fatalf("dicomweb plan on https dicomweb target: %v", err)
	}
	if err := dimse.CheckTarget("d", ProtocolDIMSE, true); err != nil {
		t.Fatalf("dimse plan on TLS dimse target: %v", err)
	}
	if err := web.CheckTarget("d", ProtocolDIMSE, true); err == nil || !strings.Contains(err.Error(), "a qido step needs a dicomweb target") {
		t.Fatalf("qido on dimse: %v", err)
	}
	if err := dimse.CheckTarget("w", ProtocolDICOMweb, true); err == nil || !strings.Contains(err.Error(), "an echo step") && !strings.Contains(err.Error(), "a echo step needs a dimse target") {
		t.Fatalf("echo on dicomweb: %v", err)
	}
	if err := web.CheckTarget("w", ProtocolDICOMweb, false); err == nil || !strings.Contains(err.Error(), "needs a target with TLS") {
		t.Fatalf("tls_reject on http target: %v", err)
	}
	// validate steps run with any target.
	v, _ := Parse([]byte("name: x\nsteps:\n  - {id: v, type: validate, files: [a]}\n"), "/plans")
	if v.CheckTarget("w", ProtocolDICOMweb, false) != nil || v.CheckTarget("d", ProtocolDIMSE, false) != nil {
		t.Fatal("validate step tied to a protocol")
	}
	if !LabOnly(Stow) || !LabOnly(Qido) || !LabOnly(Wado) || !LabOnly(Store) || !LabOnly(Move) || LabOnly(Find) || LabOnly(Echo) || LabOnly(Associate) || LabOnly(TLSReject) {
		t.Fatal("lab-only set wrong")
	}
}

// Verifies: DVT-SRS-142
func TestBundledV01PlanParsesUnchanged(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(here), "..", "..", "plans", "libredcm-conformance.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(data, filepath.Dir(path))
	if err != nil {
		t.Fatalf("bundled v0.1 plan rejected: %v", err)
	}
	for _, s := range p.Steps {
		if StepProtocol(s.Type) == ProtocolDICOMweb || s.Type == Associate || s.Type == TLSReject {
			t.Fatalf("bundled v0.1 plan has a v0.2 step: %s", s.ID)
		}
	}
	if err := p.CheckTarget("libredcm-lab", ProtocolDIMSE, false); err != nil {
		t.Fatalf("bundled plan does not fit a v0.1 DIMSE target: %v", err)
	}
	// v0.1 rules: zero-valued v0.1 fields on other v0.1 step types stay
	// accepted, YAML merge keys too.
	old := "name: x\ntarget: t\nsteps:\n  - {id: e, type: echo, compare_pixels: false, files: []}\n  - &s {id: st, type: store, files: [a]}\n  - {<<: *s, id: st2}\n"
	if _, err := Parse([]byte(old), "/plans"); err != nil {
		t.Fatalf("v0.1-valid plan rejected: %v", err)
	}
}
