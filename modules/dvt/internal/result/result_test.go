package result

import "testing"

// Verifies: DVT-SRS-009
func TestDecideVerdictRules(t *testing.T) {
	warn := Finding{Severity: Warning, Message: "w"}
	errF := Finding{Severity: Error, Message: "e"}
	odd := Finding{Severity: "info", Message: "unknown severity must fail safe"}
	cases := []struct {
		name string
		f    []Finding
		e    []EngineError
		want Verdict
	}{
		{"nothing", nil, nil, Pass},
		{"warnings only", []Finding{warn, warn}, nil, PassWithWarnings},
		{"one error", []Finding{warn, errF}, nil, Fail},
		{"engine error without findings", nil, []EngineError{{Engine: "x", Message: "crashed"}}, Fail},
		{"unknown severity", []Finding{odd}, nil, Fail},
	}
	for _, c := range cases {
		if got := Decide(c.f, c.e); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if !Pass.Passed() || !PassWithWarnings.Passed() || Fail.Passed() {
		t.Fatal("Passed() wrong")
	}
}

// Verifies: DVT-SRS-007, DVT-SRS-009
func TestValidationFinish(t *testing.T) {
	v := Validation{Files: []FileResult{
		{File: "a", Findings: []Finding{{Severity: Warning}}},
		{File: "b"},
	}}
	v.Finish()
	if v.Verdict != PassWithWarnings || v.Files[0].Verdict != PassWithWarnings || v.Files[1].Verdict != Pass {
		t.Fatalf("got %q / %q / %q", v.Verdict, v.Files[0].Verdict, v.Files[1].Verdict)
	}
	v.SetErrors = []EngineError{{Engine: "dcentvfy", Message: "timeout"}}
	v.Finish()
	if v.Verdict != Fail {
		t.Fatalf("set-level engine error must fail, got %q", v.Verdict)
	}
	empty := Validation{}
	empty.Finish()
	if empty.Verdict != Fail {
		t.Fatalf("an empty validation must fail, got %q", empty.Verdict)
	}
	e, w, ee := v.Counts()
	if e != 0 || w != 1 || ee != 1 {
		t.Fatalf("counts %d %d %d", e, w, ee)
	}
}

// Verifies: DVT-SRS-042
func TestRunFinish(t *testing.T) {
	r := Run{Steps: []Step{{Status: StepPassed}, {Status: StepPassed}}}
	r.Finish()
	if r.Verdict != Pass {
		t.Fatalf("all passed: %q", r.Verdict)
	}
	r.Steps = append(r.Steps, Step{Status: StepNotRun})
	r.Finish()
	if r.Verdict != Fail {
		t.Fatalf("a step that did not run must fail the run, got %q", r.Verdict)
	}
	r = Run{}
	r.Finish()
	if r.Verdict != Fail {
		t.Fatal("a run without steps must fail")
	}
	r = Run{Steps: []Step{{Status: StepPassed}}, Error: "target unknown"}
	r.Finish()
	if r.Verdict != Fail {
		t.Fatal("a run-level error must fail")
	}
}
