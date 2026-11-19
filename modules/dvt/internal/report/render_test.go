package report

import (
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/redact"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

const synthToken = "dvt-synthetic-bearer-report-0123456789" // gitleaks:allow (synthetic test value)

// Verifies: DVT-SRS-152
func TestRenderScansEveryArtefactBeforeWriting(t *testing.T) {
	secrets := &redact.Set{}
	secrets.AddToken(synthToken)

	// Clean run: nothing found, verdict unchanged.
	r := sampleRun()
	before := r.Verdict
	a, hits, err := Render(r, secrets)
	if err != nil || hits != 0 || r.Verdict != before || len(a.JSON) == 0 || len(a.HTML) == 0 || len(a.JUnit) == 0 {
		t.Fatalf("clean run: hits %d err %v verdict %s", hits, err, r.Verdict)
	}

	// A secret that reached a step message, the run error and evidence.
	r = sampleRun()
	r.Steps[0].Messages = append(r.Steps[0].Messages, "peer echoed Authorization: Bearer "+synthToken)
	ev := result.NewEvidence(result.TargetInfo{Name: "w"})
	ev.Assert("http.status", "", "200", "200", "token "+synthToken)
	r.Steps = append(r.Steps, result.Step{ID: "q", Type: "qido"})
	r.Steps[len(r.Steps)-1].ApplyEvidence(ev)
	r.Error = "x " + synthToken
	r.Finish()
	a, hits, err = Render(r, secrets)
	if err != nil || hits < 3 {
		t.Fatalf("hits %d err %v", hits, err)
	}
	for name, b := range map[string][]byte{"json": a.JSON, "html": a.HTML, "junit": a.JUnit} {
		if strings.Contains(string(b), synthToken) {
			t.Fatalf("secret in %s", name)
		}
		if !strings.Contains(string(b), "secret value(s) found") {
			t.Fatalf("%s does not show why the run failed", name)
		}
	}
	if r.Verdict != result.Fail || !strings.Contains(string(a.JSON), redact.Placeholder) {
		t.Fatalf("run not failed: %s", r.Verdict)
	}
	// An empty set renders as before (v0.1 runs without secrets).
	r = sampleRun()
	if _, hits, err := Render(r, nil); err != nil || hits != 0 {
		t.Fatalf("nil set: %d %v", hits, err)
	}
}
