package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

func ip(v int) *int { return &v }

// fake is a configurable Engines; zero value = everything succeeds.
type fake struct {
	echo    engine.EchoResult
	reject  engine.RejectResult
	storeFn func(files []string) engine.StoreResult
	find    func(studies []string) engine.FindResult
	move    func(studies []string) engine.MoveResult
	compare func(sent []string) []engine.CompareFile
	err     error
	calls   []string
}

func (f *fake) Versions(context.Context) map[string]string { return map[string]string{"fake": "1"} }
func (f *fake) Validate(_ context.Context, files []string, _ map[string]string) result.Validation {
	v := result.Validation{Files: []result.FileResult{{File: files[0]}}}
	v.Finish()
	return v
}
func (f *fake) Probe(_ context.Context, files []string) ([]engine.ProbeFile, error) {
	var out []engine.ProbeFile
	for _, x := range files {
		out = append(out, engine.ProbeFile{File: x, DICOM: true, SOPInstanceUID: "1.2." + filepath.Base(x), StudyInstanceUID: "9.9"})
	}
	return out, nil
}
func (f *fake) Echo(context.Context, engine.Peer) (engine.EchoResult, string, error) {
	f.calls = append(f.calls, "echo")
	return f.echo, "{}", f.err
}
func (f *fake) Reject(context.Context, engine.Peer) (engine.RejectResult, string, error) {
	f.calls = append(f.calls, "reject")
	return f.reject, "{}", f.err
}
func (f *fake) Store(_ context.Context, _ engine.Peer, files []string) (engine.StoreResult, string, error) {
	f.calls = append(f.calls, "store")
	if f.storeFn != nil {
		return f.storeFn(files), "{}", f.err
	}
	r := engine.StoreResult{Associated: true}
	for _, x := range files {
		r.Files = append(r.Files, engine.StoreFile{File: x, Status: ip(0), SOPInstanceUID: "1.2." + filepath.Base(x), StudyInstanceUID: "9.9"})
	}
	return r, "{}", f.err
}
func (f *fake) Find(_ context.Context, _ engine.Peer, studies []string) (engine.FindResult, string, error) {
	f.calls = append(f.calls, "find")
	if f.find != nil {
		return f.find(studies), "{}", f.err
	}
	r := engine.FindResult{Associated: true}
	for _, s := range studies {
		r.Queries = append(r.Queries, engine.FindQuery{Study: s, FinalStatus: ip(0), Matches: []string{s}})
	}
	return r, "{}", f.err
}
func (f *fake) Move(_ context.Context, _ engine.Peer, _ engine.SCP, _ string, studies []string, _ int) (engine.MoveResult, string, error) {
	f.calls = append(f.calls, "move")
	if f.move != nil {
		return f.move(studies), "{}", f.err
	}
	r := engine.MoveResult{Associated: true}
	for _, s := range studies {
		r.Moves = append(r.Moves, engine.MoveStudy{Study: s, FinalStatus: ip(0), Failed: ip(0)})
	}
	r.Received = []engine.Received{{SOPInstanceUID: "1.2.a.dcm"}, {SOPInstanceUID: "1.2.b.dcm"}}
	return r, "{}", f.err
}
func (f *fake) Compare(_ context.Context, sent []string, _ string) ([]engine.CompareFile, string, error) {
	f.calls = append(f.calls, "compare")
	if f.compare != nil {
		return f.compare(sent), "{}", f.err
	}
	var out []engine.CompareFile
	for _, s := range sent {
		out = append(out, engine.CompareFile{File: s, Match: true})
	}
	return out, "{}", f.err
}

const fullPlan = `
name: full
target: t
steps:
  - {id: echo, type: echo, verifies: [DCM-SRS-001]}
  - {id: reject, type: reject, called_aet: WRONG}
  - {id: store, type: store, files: ["fx/*.dcm"]}
  - {id: find, type: find, from_store: true}
  - {id: move, type: move, from_store: true, compare_pixels: true}
`

func setup(t *testing.T, src string) *plan.Plan {
	t.Helper()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "fx"), 0o755)
	for _, n := range []string{"a.dcm", "b.dcm"} {
		_ = os.WriteFile(filepath.Join(dir, "fx", n), []byte("x"), 0o644)
	}
	p, err := plan.Parse([]byte(src), dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func lab() *store.Target {
	return &store.Target{Name: "t", Host: "h", Port: 104, CalledAET: "A", CallingAET: "B", Class: store.Lab}
}

func run(t *testing.T, f *fake, p *plan.Plan, tg *store.Target) *result.Run {
	t.Helper()
	rn := &Runner{Engines: f, SCP: engine.SCP{AETitle: "LIBREDVT", Port: 11115}, WorkDir: t.TempDir(), Version: "test"}
	return rn.Run(context.Background(), "id", p, tg, nil)
}

func stepOf(r *result.Run, id string) result.Step {
	for _, s := range r.Steps {
		if s.ID == id {
			return s
		}
	}
	return result.Step{}
}

// Verifies: DVT-SRS-021, DVT-SRS-022, DVT-SRS-023, DVT-SRS-024, DVT-SRS-025, DVT-SRS-027, DVT-SRS-041
func TestHappyPathPasses(t *testing.T) {
	f := &fake{echo: engine.EchoResult{Associated: true, Status: ip(0)}, reject: engine.RejectResult{Rejected: true}}
	r := run(t, f, setup(t, fullPlan), lab())
	if r.Verdict != result.Pass {
		for _, s := range r.Steps {
			t.Logf("%s %s %v", s.ID, s.Status, s.Messages)
		}
		t.Fatal("expected pass")
	}
	if strings.Join(f.calls, ",") != "echo,reject,store,find,move,compare" {
		t.Fatalf("calls %v", f.calls)
	}
	if r.Versions["libredvt"] != "test" || r.PlanSHA256 == "" {
		t.Fatal("versions/plan hash missing")
	}
}

// Verifies: DVT-SRS-028
func TestProductionGuard(t *testing.T) {
	f := &fake{echo: engine.EchoResult{Associated: true, Status: ip(0)}, reject: engine.RejectResult{Rejected: true}}
	tg := lab()
	tg.Class = store.Production
	r := run(t, f, setup(t, fullPlan), tg)
	if r.Verdict != result.Fail {
		t.Fatal("must fail")
	}
	for _, c := range f.calls {
		if c == "store" || c == "move" {
			t.Fatalf("%s was sent to a production target", c)
		}
	}
	s := stepOf(r, "store")
	if s.Status != result.StepFailed || !strings.Contains(s.Messages[0], "refused") {
		t.Fatalf("%+v", s)
	}
	if stepOf(r, "echo").Status != result.StepPassed {
		t.Fatal("non-destructive steps still run")
	}
}

// Verifies: DVT-SRS-021, DVT-SRS-027, DVT-SRS-029
func TestEchoAndRejectFailures(t *testing.T) {
	p := setup(t, "name: x\ntarget: t\nsteps:\n  - {id: echo, type: echo}\n  - {id: reject, type: reject, calling_aet: EVIL}\n")
	cases := []struct {
		name string
		f    *fake
	}{
		{"echo failure status", &fake{echo: engine.EchoResult{Associated: true, Status: ip(0x0122)}, reject: engine.RejectResult{Rejected: true}}},
		{"echo no status", &fake{echo: engine.EchoResult{Associated: true}, reject: engine.RejectResult{Rejected: true}}},
		{"no association (timeout/down)", &fake{echo: engine.EchoResult{Error: "association failed"}, reject: engine.RejectResult{Rejected: true}}},
		{"reject accepted", &fake{echo: engine.EchoResult{Associated: true, Status: ip(0)}, reject: engine.RejectResult{Associated: true}}},
		{"reject aborted not rejected", &fake{echo: engine.EchoResult{Associated: true, Status: ip(0)}, reject: engine.RejectResult{Aborted: true}}},
		{"engine error", &fake{err: errors.New("worker crashed")}},
	}
	for _, c := range cases {
		if r := run(t, c.f, p, lab()); r.Verdict != result.Fail {
			t.Errorf("%s: passed", c.name)
		}
	}
}

// Verifies: DVT-SRS-022, DVT-SRS-024, DVT-SRS-025
func TestStoreMoveCompareFailures(t *testing.T) {
	base := func() *fake {
		return &fake{echo: engine.EchoResult{Associated: true, Status: ip(0)}, reject: engine.RejectResult{Rejected: true}}
	}
	cases := map[string]func(*fake){
		"store refused": func(f *fake) {
			f.storeFn = func(files []string) engine.StoreResult {
				r := engine.StoreResult{Associated: true}
				for i, x := range files {
					st := 0
					if i == 1 {
						st = 0xA700
					}
					r.Files = append(r.Files, engine.StoreFile{File: x, Status: ip(st), SOPInstanceUID: "1.2." + filepath.Base(x), StudyInstanceUID: "9.9"})
				}
				return r
			}
		},
		"store result count wrong": func(f *fake) {
			f.storeFn = func([]string) engine.StoreResult { return engine.StoreResult{Associated: true} }
		},
		"find misses study": func(f *fake) {
			f.find = func(s []string) engine.FindResult {
				return engine.FindResult{Associated: true, Queries: []engine.FindQuery{{Study: s[0], FinalStatus: ip(0)}}}
			}
		},
		"move missing instance": func(f *fake) {
			f.move = func(s []string) engine.MoveResult {
				return engine.MoveResult{Associated: true, Moves: []engine.MoveStudy{{Study: s[0], FinalStatus: ip(0)}},
					Received: []engine.Received{{SOPInstanceUID: "1.2.a.dcm"}}}
			}
		},
		"move extra instance": func(f *fake) {
			f.move = func(s []string) engine.MoveResult {
				return engine.MoveResult{Associated: true, Moves: []engine.MoveStudy{{Study: s[0], FinalStatus: ip(0)}},
					Received: []engine.Received{{SOPInstanceUID: "1.2.a.dcm"}, {SOPInstanceUID: "1.2.b.dcm"}, {SOPInstanceUID: "7.7"}}}
			}
		},
		"move warning status": func(f *fake) {
			f.move = func(s []string) engine.MoveResult {
				return engine.MoveResult{Associated: true, Moves: []engine.MoveStudy{{Study: s[0], FinalStatus: ip(0xB000)}},
					Received: []engine.Received{{SOPInstanceUID: "1.2.a.dcm"}, {SOPInstanceUID: "1.2.b.dcm"}}}
			}
		},
		"pixels differ": func(f *fake) {
			f.compare = func(sent []string) []engine.CompareFile {
				return []engine.CompareFile{{File: sent[0], Match: true}, {File: sent[1], Match: false, Error: "pixel data differs"}}
			}
		},
		"compressed instance transcoded": func(f *fake) {
			f.compare = func(sent []string) []engine.CompareFile {
				return []engine.CompareFile{{File: sent[0], Match: true}, {File: sent[1], Match: true,
					SentTransferSyntax: "1.2.840.10008.1.2.4.50", ReceivedTransferSyntax: "1.2.840.10008.1.2.1"}}
			}
		},
		"compare drops a file": func(f *fake) {
			f.compare = func(sent []string) []engine.CompareFile { return []engine.CompareFile{{File: sent[0], Match: true}} }
		},
	}
	for name, mut := range cases {
		f := base()
		mut(f)
		r := run(t, f, setup(t, fullPlan), lab())
		if r.Verdict != result.Fail {
			t.Errorf("%s: passed", name)
		}
	}
}

// Verifies: DVT-SRS-042
func TestUnknownTargetMeansStepsNotRun(t *testing.T) {
	f := &fake{}
	r := run(t, f, setup(t, fullPlan), nil)
	if r.Verdict != result.Fail || r.Error == "" {
		t.Fatalf("verdict %q error %q", r.Verdict, r.Error)
	}
	for _, s := range r.Steps {
		if s.Status != result.StepNotRun {
			t.Fatalf("step %s status %s", s.ID, s.Status)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("engines called: %v", f.calls)
	}
}

// Verifies: DVT-SRS-025
func TestNativeReencodingIsNotedNotFailed(t *testing.T) {
	f := &fake{echo: engine.EchoResult{Associated: true, Status: ip(0)}, reject: engine.RejectResult{Rejected: true}}
	f.compare = func(sent []string) []engine.CompareFile {
		return []engine.CompareFile{{File: sent[0], Match: true}, {File: sent[1], Match: true,
			SentTransferSyntax: "1.2.840.10008.1.2.1", ReceivedTransferSyntax: "1.2.840.10008.1.2"}}
	}
	r := run(t, f, setup(t, fullPlan), lab())
	s := stepOf(r, "move")
	if s.Status != result.StepPassed || !strings.Contains(strings.Join(s.Messages, " "), "re-encoded") {
		t.Fatalf("%s %v", s.Status, s.Messages)
	}
}
