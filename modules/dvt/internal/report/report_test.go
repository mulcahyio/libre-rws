package report

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

func sampleRun() *result.Run {
	t0 := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	r := &result.Run{ID: "20261110-120000-abcdef01", Plan: "LibreDCM conformance", PlanSHA256: strings.Repeat("ab", 32),
		Target: "lab", TargetDesc: "lab (h:104)", Started: t0, Finished: t0.Add(3 * time.Second),
		Versions: map[string]string{"libredvt": "0.1.0", "dicom3tools": "1.00.snapshot.20260927113651", "standard-edition": "2026d"},
		Steps: []result.Step{
			{ID: "echo", Type: "echo", Verifies: []string{"DCM-SRS-001"}, Status: result.StepPassed, Messages: []string{"ok"}},
			{ID: "move", Type: "move", Verifies: []string{"DCM-SRS-010"}, Status: result.StepFailed, Messages: []string{"1 expected instance(s) not received: [<script>]"}},
		}}
	v := result.Validation{ID: "v", Files: []result.FileResult{{Name: "a.dcm", Findings: []result.Finding{{Engine: "dciodvfy", Severity: result.Error, Path: "Modality(0008,0060)", Message: "Missing"}}}}}
	v.Finish()
	r.Steps = append(r.Steps, result.Step{ID: "validate", Type: "validate", Status: result.StepFailed, Validation: &v})
	r.Finish()
	return r
}

// Verifies: DVT-SRS-043
func TestJUnit(t *testing.T) {
	b, err := JUnit(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	var doc junitSuites
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, b)
	}
	s := doc.Suites[0]
	if doc.Tests != 3 || doc.Failures != 2 || len(s.Cases) != 3 {
		t.Fatalf("tests %d failures %d", doc.Tests, doc.Failures)
	}
	if s.Cases[0].Failure != nil || s.Cases[1].Failure == nil || s.Cases[1].Properties[0].Value != "DCM-SRS-010" {
		t.Fatalf("%+v", s.Cases)
	}
	// A plan-level error with no steps still shows as a failure in CI.
	empty := &result.Run{Error: "target unknown"}
	empty.Finish()
	b, _ = JUnit(empty)
	if !strings.Contains(string(b), `failures="1"`) {
		t.Fatalf("%s", b)
	}
}

// Verifies: DVT-SRS-044
func TestHTMLReportSelfContained(t *testing.T) {
	b, err := HTML(sampleRun())
	if err != nil {
		t.Fatal(err)
	}
	h := string(b)
	for _, want := range []string{"fail", strings.Repeat("ab", 32), "DCM-SRS-010", "1.00.snapshot.20260927113651", "2026d", "2026-11-10 12:00:00 UTC", "Modality(0008,0060)", "<style>"} {
		if !strings.Contains(h, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Contains(h, "<script>") {
		t.Fatal("message not escaped")
	}
	if strings.Contains(h, `<link rel="stylesheet"`) || strings.Contains(h, "src=\"http") {
		t.Fatal("report must not load external resources")
	}
	vb, err := ValidationHTML(sampleRun().Steps[2].Validation)
	if err != nil || !strings.Contains(string(vb), "Missing") {
		t.Fatalf("validation report: %v", err)
	}
}
