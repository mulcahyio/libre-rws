package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	htmltemplate "html/template"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Test data:
//   - v01-run.json, v01-report.html, v01-junit.xml: a run written by the
//     v0.1 code (master at b932eb3, report_test.go sampleRun) with its HTML
//     and JUnit output, rendered there.
//   - v02-dimse-tls-run.json: the real LibreDCM TLS cross-check run of card
//     DVT-0.2/3 (docs/dhf/libredvt/evidence/v0.2-assoc-tls/cross-check).
//   - v02-dicomweb-run.json: the real Orthanc DICOMweb audit run of card
//     DVT-0.2/4 (docs/dhf/libredvt/evidence/v0.2-dicomweb/cross-check).
// Synthetic and public sample data only.

func loadRun(t *testing.T, name string) (*result.Run, []byte) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var r result.Run
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &r, b
}

var (
	styleRe = regexp.MustCompile(`(?s)<style>.*?</style>`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// normal drops the inlined stylesheet (which gains v0.2 rules) and folds
// white space, so only content and markup are compared.
func normal(b []byte) string {
	return strings.TrimSpace(spaceRe.ReplaceAllString(styleRe.ReplaceAllString(string(b), "<style></style>"), " "))
}

// Verifies: DVT-SRS-145
func TestStoredV01RunLoadsAndRendersUnchanged(t *testing.T) {
	r, raw := loadRun(t, "v01-run.json")
	j, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(append(j, '\n')) != string(raw) {
		t.Errorf("v0.1 run JSON does not round-trip unchanged:\n%s", j)
	}
	h, err := HTML(r)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile("testdata/v01-report.html")
	if normal(h) != normal(want) {
		t.Errorf("v0.1 HTML report changed:\n got: %s\nwant: %s", normal(h), normal(want))
	}
	ju, err := JUnit(r)
	if err != nil {
		t.Fatal(err)
	}
	wantJ, _ := os.ReadFile("testdata/v01-junit.xml")
	if string(ju) != string(wantJ) {
		t.Errorf("v0.1 JUnit changed:\n%s", ju)
	}
}

// section returns the HTML of one step's evidence card.
func section(t *testing.T, h, id string) string {
	t.Helper()
	start := strings.Index(h, `id="evidence-`+id+`"`)
	if start < 0 {
		t.Fatalf("no evidence section for step %s", id)
	}
	rest := h[start+1:]
	end := strings.Index(rest, `id="evidence-`)
	if v := strings.Index(rest, "<h2>Versions</h2>"); end < 0 || (v >= 0 && v < end) {
		end = v
	}
	return rest[:end]
}

var escT = htmltemplate.Must(htmltemplate.New("esc").Parse("{{.}}"))

// esc escapes text as html/template does in element content (it also
// escapes '+' and quotes).
func esc(s string) string {
	var b strings.Builder
	_ = escT.Execute(&b, s)
	return b.String()
}

// checkEvidenceInHTML finds every evidence field of every step in that
// step's own section of the HTML report.
func checkEvidenceInHTML(t *testing.T, r *result.Run, h string) (fields int) {
	t.Helper()
	needRaw := func(sec, step, what, v string) {
		t.Helper()
		if v == "" {
			return
		}
		fields++
		if !strings.Contains(sec, v) {
			t.Errorf("step %s: %s %q missing from the HTML report", step, what, v)
		}
	}
	need := func(sec, step, what, v string) {
		t.Helper()
		if v != "" {
			needRaw(sec, step, what, esc(v))
		}
	}
	for _, s := range r.Steps {
		if s.Evidence == nil {
			continue
		}
		sec := section(t, h, s.ID)
		e := s.Evidence
		needRaw(sec, s.ID, "verdict", `badge `+map[result.Verdict]string{result.Pass: "pass", result.PassWithWarnings: "warn", result.Fail: "fail"}[s.Verdict]+`">`+string(s.Verdict))
		need(sec, s.ID, "protocol", e.Target.Protocol)
		need(sec, s.ID, "endpoint", e.Target.Endpoint)
		need(sec, s.ID, "class", "class "+e.Target.Class)
		for _, x := range e.Exchanges {
			need(sec, s.ID, "exchange endpoint", x.Endpoint)
			need(sec, s.ID, "exchange result", ExchangeResult(x))
			if tl := x.TLS; tl != nil {
				need(sec, s.ID, "TLS version", tl.Version)
				need(sec, s.ID, "cipher", tl.Cipher)
				need(sec, s.ID, "verify error", tl.VerifyError)
				if c := tl.Peer; c != nil {
					need(sec, s.ID, "subject", c.Subject)
					need(sec, s.ID, "issuer", c.Issuer)
					need(sec, s.ID, "not before", c.NotBefore)
					need(sec, s.ID, "not after", c.NotAfter)
					need(sec, s.ID, "fingerprint", c.SHA256)
					for _, n := range append(append([]string{}, c.SANDNS...), c.SANIP...) {
						need(sec, s.ID, "SAN", n)
					}
				}
			}
			if a := x.Association; a != nil {
				need(sec, s.ID, "association result", a.Result)
				need(sec, s.ID, "implementation class UID", a.ImplClassUID)
				need(sec, s.ID, "implementation version", a.ImplVersion)
				if a.Reject != nil {
					need(sec, s.ID, "reject", fmt.Sprintf("result %d, source %d, reason %d", a.Reject.Result, a.Reject.Source, a.Reject.Reason))
				}
				for _, c := range a.Contexts {
					needRaw(sec, s.ID, "context", fmt.Sprintf(`<td>%d</td><td class="mono">%s</td>`, c.ID, esc(c.AbstractSyntax)))
					need(sec, s.ID, "context result", c.Result)
					need(sec, s.ID, "accepted transfer syntax", c.AcceptedTransferSyntax)
				}
			}
			if hp := x.HTTP; hp != nil {
				needRaw(sec, s.ID, "HTTP request", esc(hp.Method)+` <span class="mono">`+esc(hp.URL)+`</span>`)
				if hp.Status != 0 {
					need(sec, s.ID, "HTTP status", fmt.Sprintf("→ %d", hp.Status))
				}
				for _, l := range headerLines(hp.RequestHeaders) {
					need(sec, s.ID, "request header", l)
				}
			}
		}
		for _, in := range e.Instances {
			need(sec, s.ID, "SOP Instance UID", in.SOPInstanceUID)
			need(sec, s.ID, "remote result", in.RemoteResult)
			need(sec, s.ID, "retrieve URL", in.RetrieveURL)
			need(sec, s.ID, "pixel comparison", pixels(in.PixelSHA256Sent, in.PixelSHA256Received))
		}
		for _, a := range e.Assertions {
			needRaw(sec, s.ID, "assertion", fmt.Sprintf(`<span class="badge %s">%s</span></td><td class="mono">%s</td>`, assertClass(a.Outcome), esc(a.Outcome), esc(a.ID)))
			need(sec, s.ID, "expected", a.Expected)
			need(sec, s.ID, "actual", a.Actual)
		}
		for _, p := range e.Errors {
			need(sec, s.ID, "problem", p.Message)
		}
	}
	return fields
}

// checkEvidenceInJUnit finds the same facts as JUnit properties and every
// assertion in system-out.
func checkEvidenceInJUnit(t *testing.T, r *result.Run, b []byte) (fields int) {
	t.Helper()
	var doc junitSuites
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("JUnit is not XML: %v", err)
	}
	cases := map[string]junitCase{}
	for _, c := range doc.Suites[0].Cases {
		cases[c.Name] = c
	}
	for _, s := range r.Steps {
		if s.Evidence == nil {
			continue
		}
		c := cases[s.ID]
		props := map[string]string{}
		for _, p := range c.Properties {
			props[p.Name] = p.Value
		}
		need := func(name, want string) {
			t.Helper()
			fields++
			if props[name] != want {
				t.Errorf("step %s: JUnit property %s = %q, want %q", s.ID, name, props[name], want)
			}
		}
		need("verdict", string(s.Verdict))
		need("target.endpoint", s.Evidence.Target.Endpoint)
		for _, x := range s.Evidence.Exchanges {
			p := fmt.Sprintf("exchange.%d", x.Seq)
			need(p, exchangeLine(x))
			if tl := x.TLS; tl != nil {
				need(p+".tls.version", tl.Version)
				need(p+".tls.verified", yesno(tl.Verified))
				if tl.Peer != nil {
					need(p+".tls.peer_subject", tl.Peer.Subject)
					need(p+".tls.peer_issuer", tl.Peer.Issuer)
					need(p+".tls.peer_not_after", tl.Peer.NotAfter)
					need(p+".tls.peer_sha256", tl.Peer.SHA256)
				}
			}
			if a := x.Association; a != nil {
				need(p+".association.result", a.Result)
				if a.ImplClassUID != "" {
					need(p+".association.impl_class_uid", a.ImplClassUID)
				}
			}
		}
		for i, in := range s.Evidence.Instances {
			fields++
			if v := props[fmt.Sprintf("instance.%d", i+1)]; !strings.HasPrefix(v, in.SOPInstanceUID) || !strings.Contains(v, in.RemoteResult) {
				t.Errorf("step %s: instance %d property %q", s.ID, i+1, v)
			}
		}
		for _, a := range s.Evidence.Assertions {
			fields++
			if !strings.Contains(c.SystemOut, AssertionLine(a)) {
				t.Errorf("step %s: assertion %q missing from system-out", s.ID, AssertionLine(a))
			}
		}
	}
	return fields
}

// Verifies: DVT-SRS-145, DVT-SRS-107, DVT-SRS-143
func TestReportsShowRealDIMSEAndTLSEvidence(t *testing.T) {
	r, _ := loadRun(t, "v02-dimse-tls-run.json")
	h, err := HTML(r)
	if err != nil {
		t.Fatal(err)
	}
	n := checkEvidenceInHTML(t, r, string(h))
	ju, _ := JUnit(r)
	m := checkEvidenceInJUnit(t, r, ju)
	t.Logf("HTML fields checked: %d, JUnit fields checked: %d", n, m)
	if n < 60 || m < 30 {
		t.Fatalf("too few fields checked (%d, %d): the test data lost its evidence", n, m)
	}
	// The endpoint identity card shows the certificate once and LibreDCM's
	// implementation identification.
	s := string(h)
	for _, want := range []string{`id="endpoint-identity"`, "TLSv1.3", "1.3.6.1.4.1.67686.1.1.1.0.0", "LIBREDCM_0_0", "valid at the start of the run, 0 day(s) left"} {
		if !strings.Contains(s, want) {
			t.Errorf("identity card lacks %q", want)
		}
	}
	if c := strings.Count(s, "<h3>Server certificate</h3>"); c != 1 {
		t.Errorf("identity card lists %d certificates, want 1", c)
	}
	if strings.Contains(strings.ToLower(s), "<script") {
		t.Error("report contains script")
	}
}

// Verifies: DVT-SRS-145, DVT-SRS-118, DVT-SRS-129, DVT-SRS-143
func TestReportsShowRealDICOMwebEvidence(t *testing.T) {
	r, _ := loadRun(t, "v02-dicomweb-run.json")
	h, err := HTML(r)
	if err != nil {
		t.Fatal(err)
	}
	n := checkEvidenceInHTML(t, r, string(h))
	ju, _ := JUnit(r)
	m := checkEvidenceInJUnit(t, r, ju)
	t.Logf("HTML fields checked: %d, JUnit fields checked: %d", n, m)
	if n < 500 || m < 150 {
		t.Fatalf("too few fields checked (%d, %d)", n, m)
	}
	// Per-instance STOW results: each of the six fixtures appears as a
	// success with identical pixels in the stow section.
	sec := section(t, string(h), "stow")
	if c := strings.Count(sec, `<span class="badge pass">success</span>`); c < 6 {
		t.Errorf("stow section shows %d successful instances, want at least 6", c)
	}
	if c := strings.Count(sec, "<td>identical</td>"); c < 6 {
		t.Errorf("stow section shows %d identical pixel comparisons, want at least 6", c)
	}
}

// failingRun is a v0.2 run with a target record, a failed assertion, a
// problem, an unverified certificate, a rejected association and STOW
// instance failures.
func failingRun() *result.Run {
	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	ti := result.TargetInfo{Name: "vendor-lab", Class: "lab", Protocol: "dicomweb", Endpoint: "https://pacs.lab.example/dicom-web"}
	e := result.NewEvidence(ti)
	e.Exchanges = append(e.Exchanges, result.Exchange{Seq: 1, Protocol: "http", Operation: "POST", Endpoint: ti.Endpoint + "/studies", Outcome: result.ExchangeCompleted,
		TLS: &result.TLSInfo{Version: "TLSv1.2", Cipher: "ECDHE-RSA-AES128-GCM-SHA256", Verified: false, VerifyError: "certificate has expired",
			Peer: &result.PeerCert{Subject: "CN=pacs.lab.example", Issuer: "CN=Vendor CA", SANDNS: []string{"pacs.lab.example"}, SANIP: []string{"192.0.2.10"},
				NotBefore: "2025-01-01T00:00:00Z", NotAfter: "2026-01-01T00:00:00Z", SHA256: strings.Repeat("ab", 32)}},
		HTTP: &result.HTTPInfo{Method: "POST", URL: ti.Endpoint + "/studies", Status: 409,
			RequestHeaders: map[string][]string{"Authorization": {"Bearer [REDACTED]"}, "Accept": {"application/dicom+json"}},
			Body:           &result.BodyInfo{ContentType: "application/dicom+json", Size: 12, SHA256: strings.Repeat("cd", 32), Excerpt: `{"<b>":1}`}}})
	e.Instances = []result.Instance{
		{SOPInstanceUID: "1.3.6.1.4.1.67686.9.3.1", RemoteResult: "failure", Reason: "0xA700 out of resources"},
		{SOPInstanceUID: "1.3.6.1.4.1.67686.9.3.2", RemoteResult: "warning", Reason: "0xB000 coercion"},
	}
	e.Assert("stow.status", "", "200", "409", "")
	e.Warn("stow.instance.warning", "1.3.6.1.4.1.67686.9.3.2", "success", "warning", "allow_warnings is off")
	e.Problemf(result.ProblemTLS, "certificate verify failed: certificate has expired")
	s := result.Step{ID: "stow-fixtures", Type: "stow", Started: t0}
	s.ApplyEvidence(e)

	d := result.NewEvidence(result.TargetInfo{Name: "dimse-lab", Class: "lab", Protocol: "dimse", Endpoint: "pacs.lab.example:2762"})
	d.Exchanges = append(d.Exchanges, result.Exchange{Seq: 1, Protocol: "dimse", Operation: "A-ASSOCIATE", Endpoint: "pacs.lab.example:2762", Outcome: result.ExchangeRefused,
		Association: &result.AssociationInfo{Result: "rejected", Reject: &result.RejectInfo{Result: 1, Source: 1, Reason: 7}}})
	d.Assert("assoc.result", "", "accepted", "rejected", "")
	a := result.Step{ID: "negotiate", Type: "associate", Started: t0}
	a.ApplyEvidence(d)

	r := &result.Run{ID: "20261122-120000-abcdef01", Plan: "Generic DICOMweb audit", PlanSHA256: strings.Repeat("ef", 32), Target: ti.Name,
		TargetDesc: "vendor-lab (dicomweb https://pacs.lab.example/dicom-web, auth bearer, class lab)", TargetInfo: &ti,
		Started: t0, Finished: t0.Add(2 * time.Second), Versions: map[string]string{"libredvt": "0.2.0"}, Steps: []result.Step{s, a}}
	r.Finish()
	return r
}

// Verifies: DVT-SRS-145, DVT-SRS-152
func TestReportsShowFailuresTargetAndIdentity(t *testing.T) {
	r := failingRun()
	h, err := HTML(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(h)
	checkEvidenceInHTML(t, r, s)
	ju, _ := JUnit(r)
	checkEvidenceInJUnit(t, r, ju)
	for _, want := range []string{
		"<dt>Protocol</dt><dd>dicomweb</dd>", `<dt>Endpoint</dt><dd class="mono">https://pacs.lab.example/dicom-web</dd>`, "<dt>Class</dt><dd>lab</dd>",
		"not verified: certificate has expired", "expired at the start of the run", "192.0.2.10",
		`<span class="badge fail">failure</span> 0xA700 out of resources`, `<span class="badge warn">warning</span> 0xB000 coercion`,
		"Authorization: Bearer [REDACTED]", "{&#34;&lt;b&gt;&#34;:1}", "<b>tls</b>: certificate verify failed",
		"result 1, source 1, reason 7", `<span class="badge fail">fail</span>`, "0 passed, 1 warned, 1 failed",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	j := string(ju)
	for _, want := range []string{
		`name="target.protocol" value="dicomweb"`, `name="target.endpoint" value="https://pacs.lab.example/dicom-web"`, `name="target.class" value="lab"`,
		`name="identity.cert.1.verified" value="no"`, `name="identity.cert.1.verify_error" value="certificate has expired"`,
		`name="identity.cert.1.at_run" value="expired at the start of the run"`, `name="identity.tls_versions" value="TLSv1.2"`,
		`name="exchange.1.association.reject" value="result 1, source 1, reason 7"`, `name="problem.1" value="tls: certificate verify failed: certificate has expired"`,
		`name="instance.1" value="1.3.6.1.4.1.67686.9.3.1: failure (0xA700 out of resources)"`, `failures="2"`,
	} {
		if !strings.Contains(j, want) {
			t.Errorf("JUnit lacks %s", want)
		}
	}
}

// Verifies: DVT-SRS-145
func TestEndpointIdentity(t *testing.T) {
	at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c := result.PeerCert{NotBefore: "2026-01-01T00:00:00Z", NotAfter: "2026-06-11T00:00:00Z", SHA256: "aa"}
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{at, "valid at the start of the run, 10 day(s) left"},
		{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), "not yet valid at the start of the run"},
		{time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC), "expired at the start of the run"},
		{time.Time{}, "unknown"},
	} {
		if got := validity(c, tc.at); got != tc.want {
			t.Errorf("validity at %v = %q, want %q", tc.at, got, tc.want)
		}
	}
	if got := validity(result.PeerCert{NotBefore: "x", NotAfter: "y"}, at); got != "unknown" {
		t.Errorf("unparseable dates: %q", got)
	}
	// One certificate seen unverified first and verified later counts as
	// verified once; a second certificate stays separate; implementation
	// pairs are deduplicated; v0.1 runs have no identity.
	ex := func(seq int, sha string, ok bool, uid string) result.Exchange {
		x := result.Exchange{Seq: seq, TLS: &result.TLSInfo{Version: "TLSv1.3", Cipher: "C", Verified: ok, VerifyError: map[bool]string{false: "bad"}[ok], Peer: &result.PeerCert{SHA256: sha}}}
		if uid != "" {
			x.Association = &result.AssociationInfo{ImplClassUID: uid, ImplVersion: "V"}
		}
		return x
	}
	r := &result.Run{Started: at, Steps: []result.Step{{Evidence: &result.Evidence{Exchanges: []result.Exchange{ex(1, "aa", false, "1.2"), ex(2, "aa", true, "1.2"), ex(3, "bb", false, "1.3")}}}}}
	id := EndpointIdentity(r)
	if id == nil || len(id.Certs) != 2 || !id.Certs[0].Verified || id.Certs[0].VerifyError != "" || id.Certs[1].Verified || id.Certs[1].VerifyError != "bad" ||
		len(id.Impl) != 2 || len(id.TLSVersions) != 1 || len(id.Ciphers) != 1 {
		t.Fatalf("identity %+v", id)
	}
	if EndpointIdentity(sampleRun()) != nil {
		t.Fatal("v0.1 run has an identity")
	}
}
