package result

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

func pass(id string) Assertion {
	return Assertion{ID: id, Expected: "x", Actual: "x", Outcome: AssertPass}
}

// Verifies: DVT-SRS-144
func TestDecideStepVerdictRule(t *testing.T) {
	ev := func(as []Assertion, ps []Problem) *Evidence {
		e := NewEvidence(TargetInfo{Name: "t"})
		e.Assertions, e.Errors = as, ps
		return e
	}
	warn := Assertion{ID: "w", Outcome: AssertWarn}
	fail := Assertion{ID: "f", Outcome: AssertFail}
	odd := Assertion{ID: "o", Outcome: "skipped"}
	engineErr := Problem{Kind: ProblemEngine, Message: "worker crashed"}
	cases := []struct {
		name string
		e    *Evidence
		want Verdict
	}{
		{"missing evidence", nil, Fail},
		{"zero assertions", ev(nil, nil), Fail},
		{"one passed assertion", ev([]Assertion{pass("a")}, nil), Pass},
		{"one failed assertion", ev([]Assertion{pass("a"), fail}, nil), Fail},
		{"engine error with all assertions passed", ev([]Assertion{pass("a"), pass("b")}, []Problem{engineErr}), Fail},
		{"guard problem without assertions", ev(nil, []Problem{{Kind: ProblemGuard, Message: "refused"}}), Fail},
		{"warning only", ev([]Assertion{pass("a"), warn}, nil), PassWithWarnings},
		{"warning and failure", ev([]Assertion{warn, fail}, nil), Fail},
		{"unknown outcome fails safe", ev([]Assertion{pass("a"), odd}, nil), Fail},
	}
	for _, c := range cases {
		if got := DecideStep(c.e); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Verifies: DVT-SRS-144
func TestApplyEvidenceSetsStatusFromVerdictOnly(t *testing.T) {
	// Zero assertions: "nothing was checked".
	s := Step{ID: "s", Type: "qido", Status: StepPassed}
	s.ApplyEvidence(NewEvidence(TargetInfo{}))
	if s.Status != StepFailed || s.Verdict != Fail || !strings.Contains(strings.Join(s.Messages, ";"), "nothing was checked") {
		t.Fatalf("zero assertions: %+v", s)
	}
	// Engine error with passing assertions.
	e := NewEvidence(TargetInfo{})
	e.Assert("a", "", "200", "200", "")
	e.Problemf(ProblemProtocol, "connection reset")
	s = Step{Status: StepPassed}
	s.ApplyEvidence(e)
	if s.Status != StepFailed || s.Verdict != Fail {
		t.Fatalf("problem with passing assertion: %+v", s)
	}
	// Failed assertion message names expected and actual.
	e = NewEvidence(TargetInfo{})
	if e.Assert("http.status", "GET /studies", "200", "206", "partial content") {
		t.Fatal("Assert returned pass for different values")
	}
	s = Step{}
	s.ApplyEvidence(e)
	if s.Status != StepFailed || !strings.Contains(strings.Join(s.Messages, ";"), "expected 200, got 206") {
		t.Fatalf("failed assertion: %+v", s)
	}
	// Pass with warnings counts as passed and makes the run pass with warnings.
	e = NewEvidence(TargetInfo{})
	e.Assert("a", "", "x", "x", "")
	e.Assertions = append(e.Assertions, Assertion{ID: "stow.warning", Expected: "success", Actual: "warning", Outcome: AssertWarn})
	s = Step{ID: "w"}
	s.ApplyEvidence(e)
	if s.Status != StepPassed || s.Verdict != PassWithWarnings {
		t.Fatalf("warning: %+v", s)
	}
	r := Run{Steps: []Step{{Status: StepPassed}, s}}
	r.Finish()
	if r.Verdict != PassWithWarnings {
		t.Fatalf("run verdict %q, want pass with warnings", r.Verdict)
	}
	// Evidence outside the model fails as an engine error.
	e = NewEvidence(TargetInfo{})
	e.Assertions = append(e.Assertions, pass("a"))
	e.Exchanges = append(e.Exchanges, Exchange{Seq: 1, Protocol: "ftp", Operation: "GET", Endpoint: "x", Outcome: ExchangeCompleted})
	s = Step{}
	s.ApplyEvidence(e)
	if s.Status != StepFailed {
		t.Fatalf("invalid evidence passed: %+v", s)
	}
}

// Verifies: DVT-SRS-143
func TestEvidenceCheckClosedValueSets(t *testing.T) {
	good := NewEvidence(TargetInfo{Name: "t", Class: "lab", Protocol: "dicomweb", Endpoint: "https://h/dicom-web"})
	good.Exchanges = []Exchange{
		{Seq: 1, Protocol: "tls", Operation: "TLS-HANDSHAKE", Endpoint: "h:443", Outcome: ExchangeCompleted, TLS: &TLSInfo{Version: "TLS 1.3", Verified: true}},
		{Seq: 2, Protocol: "http", Operation: "GET", Endpoint: "https://h/dicom-web/studies", Outcome: ExchangeCompleted, HTTP: &HTTPInfo{Method: "GET", Status: 200}},
		{Seq: 3, Protocol: "dimse", Operation: "A-ASSOCIATE", Endpoint: "h:104", Outcome: ExchangeError, Error: &Problem{Kind: ProblemTimeout, Message: "no answer"}},
	}
	good.Assertions = []Assertion{pass("http.status"), {ID: "w", Outcome: AssertWarn}, {ID: "f", Outcome: AssertFail}}
	good.Errors = []Problem{{Kind: ProblemLimit, Message: "max_parts"}}
	if err := good.Check(); err != nil {
		t.Fatalf("valid evidence rejected: %v", err)
	}
	mutate := map[string]func(e *Evidence){
		"schema":            func(e *Evidence) { e.Schema = "libredvt.evidence/2" },
		"seq gap":           func(e *Evidence) { e.Exchanges[1].Seq = 5 },
		"protocol":          func(e *Evidence) { e.Exchanges[0].Protocol = "smtp" },
		"exchange outcome":  func(e *Evidence) { e.Exchanges[0].Outcome = "ok" },
		"error w/o problem": func(e *Evidence) { e.Exchanges[2].Error = nil },
		"no operation":      func(e *Evidence) { e.Exchanges[1].Operation = "" },
		"no endpoint":       func(e *Evidence) { e.Exchanges[1].Endpoint = "" },
		"assertion id":      func(e *Evidence) { e.Assertions[0].ID = "" },
		"assertion outcome": func(e *Evidence) { e.Assertions[0].Outcome = "skip" },
		"problem kind":      func(e *Evidence) { e.Errors[0].Kind = "oops" },
		"redactions":        func(e *Evidence) { e.Redactions = -1 },
	}
	for name, m := range mutate {
		b, _ := json.Marshal(good)
		var e Evidence
		_ = json.Unmarshal(b, &e)
		m(&e)
		if err := e.Check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var nilEv *Evidence
	if nilEv.Check() == nil {
		t.Error("nil evidence accepted")
	}
}

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Verifies: DVT-SRS-143
func TestEvidenceFieldNamesMatchTheModel(t *testing.T) {
	// The JSON names are fixed by evidence-model.md §3.
	want := map[string]struct {
		v    any
		keys string
	}{
		"Evidence":  {Evidence{Instances: []Instance{{}}, Errors: []Problem{{}}}, "assertions errors exchanges instances redactions schema target"},
		"Target":    {TargetInfo{}, "class endpoint name protocol"},
		"Exchange":  {Exchange{Error: &Problem{}, TLS: &TLSInfo{}, Association: &AssociationInfo{}, HTTP: &HTTPInfo{}, DIMSEStatus: "0x0000"}, "association dimse_status duration_ms endpoint error http operation outcome protocol seq started tls"},
		"TLS":       {TLSInfo{VerifyError: "x", Peer: &PeerCert{}}, "cipher client_cert_sent peer verified verify_error version"},
		"Peer":      {PeerCert{SANDNS: []string{"a"}, SANIP: []string{"b"}}, "issuer not_after not_before san_dns san_ip sha256 subject"},
		"Assoc":     {AssociationInfo{Reject: &RejectInfo{}, Abort: &AbortInfo{}, MaxPDU: 1, ImplClassUID: "1", ImplVersion: "v", AsyncOps: &AsyncOps{}, Contexts: []ContextInfo{{}}}, "abort async_ops contexts impl_class_uid impl_version max_pdu reject released result"},
		"Context":   {ContextInfo{AcceptedTransferSyntax: "1", Reason: "r", Roles: &Roles{}}, "abstract_syntax accepted_transfer_syntax id proposed_transfer_syntaxes reason result roles"},
		"HTTP":      {HTTPInfo{RequestHeaders: map[string][]string{"a": nil}, ResponseHeaders: map[string][]string{"a": nil}, Body: &BodyInfo{}, Parts: []PartInfo{{}}}, "body method parts request_headers response_headers status url"},
		"Body":      {BodyInfo{Excerpt: "x", ExcerptTruncated: true}, "content_type excerpt excerpt_truncated sha256 size"},
		"Part":      {PartInfo{TransferSyntax: "1", SOPInstanceUID: "2"}, "content_type index sha256 size sop_instance_uid transfer_syntax"},
		"Instance":  {Instance{"1", "2", "3", "f", 1, "s", "success", "r", "u", "a", "b"}, "file pixel_sha256_received pixel_sha256_sent reason remote_result retrieve_url sha256 size sop_class_uid sop_instance_uid transfer_syntax"},
		"Assertion": {Assertion{Subject: "s", Message: "m"}, "actual expected id message outcome subject"},
		"Problem":   {Problem{}, "kind message"},
	}
	for name, w := range want {
		if got := strings.Join(jsonKeys(t, w.v), " "); got != w.keys {
			t.Errorf("%s keys:\n got  %s\n want %s", name, got, w.keys)
		}
	}
}

// Verifies: DVT-SRS-143
func TestV01StepsKeepTheirJSON(t *testing.T) {
	// A v0.1 step has no verdict or evidence field, so stored v0.1 runs and
	// new v0.1 runs read the same.
	s := Step{ID: "echo", Type: "echo", Status: StepPassed, Started: time.Unix(0, 0).UTC(), Messages: []string{"ok"}}
	got := strings.Join(jsonKeys(t, s), " ")
	if got != "duration id messages started status type" {
		t.Fatalf("v0.1 step keys changed: %s", got)
	}
	v01 := `{"id":"r","plan":"p","plan_sha256":"x","target":"t","target_desc":"d","started":"2026-11-10T00:00:00Z","finished":"2026-11-10T00:00:01Z","steps":[{"id":"echo","type":"echo","status":"passed","messages":["C-ECHO status 0x0000 (Success)"],"started":"2026-11-10T00:00:00Z","duration":1000}],"versions":{"libredvt":"0.1.0"},"verdict":"pass"}`
	var r Run
	if err := json.Unmarshal([]byte(v01), &r); err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Evidence != nil || r.Steps[0].Verdict != "" || r.Verdict != Pass {
		t.Fatalf("v0.1 run read wrong: %+v", r)
	}
	r.Finish()
	if r.Verdict != Pass {
		t.Fatalf("v0.1 run re-finished as %q", r.Verdict)
	}
}
