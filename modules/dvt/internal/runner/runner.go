// Package runner executes LibreDVT test plans. The same code runs plans
// from the web UI and from `libredvt run` (DVT-SRS-041).
package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

// Engines is what the runner needs from the engine package (an interface so
// tests can inject failures).
type Engines interface {
	Versions(ctx context.Context) map[string]string
	Validate(ctx context.Context, files []string, names map[string]string) result.Validation
	Probe(ctx context.Context, files []string) ([]engine.ProbeFile, error)
	Echo(ctx context.Context, p engine.Peer) (engine.EchoResult, string, error)
	Reject(ctx context.Context, p engine.Peer) (engine.RejectResult, string, error)
	Store(ctx context.Context, p engine.Peer, files []string) (engine.StoreResult, string, error)
	Find(ctx context.Context, p engine.Peer, studies []string) (engine.FindResult, string, error)
	Move(ctx context.Context, p engine.Peer, scp engine.SCP, outDir string, studies []string, expected int) (engine.MoveResult, string, error)
	Compare(ctx context.Context, sent []string, dir string) ([]engine.CompareFile, string, error)
}

// Runner runs plans.
type Runner struct {
	Engines Engines
	SCP     engine.SCP
	WorkDir string // scratch space for received instances (removed after each move step)
	Version string // LibreDVT version, recorded in every run (DVT-SRS-008)
}

// sent tracks what earlier store steps sent successfully.
type sent struct {
	files     []string            // successfully stored files
	instances map[string][]string // study UID -> SOP instance UIDs
}

// Run executes p against target t (t may be nil when the plan has no
// network steps). The result is finished; the caller saves it.
func (rn *Runner) Run(ctx context.Context, id string, p *plan.Plan, t *store.Target, progress func(*result.Run)) *result.Run {
	r := &result.Run{ID: id, Plan: p.Name, PlanSHA256: p.SHA256, Target: p.Target, Started: time.Now().UTC(), Running: true}
	r.Versions = rn.Engines.Versions(ctx)
	r.Versions["libredvt"] = rn.Version
	if t != nil {
		r.TargetDesc = t.Describe()
	} else if p.Target != "" {
		r.Error = fmt.Sprintf("target %q is not registered", p.Target)
	}
	st := &sent{instances: map[string][]string{}}
	for _, ps := range p.Steps {
		s := result.Step{ID: ps.ID, Type: ps.Type, Verifies: ps.Verifies, Status: result.StepNotRun, Started: time.Now().UTC()}
		switch {
		case ctx.Err() != nil:
			s.Messages = append(s.Messages, "not run: run cancelled")
		case r.Error != "" && ps.Type != plan.Validate:
			s.Messages = append(s.Messages, "not run: "+r.Error)
		default:
			rn.step(ctx, p, ps, t, st, &s)
		}
		s.Duration = time.Since(s.Started)
		r.Steps = append(r.Steps, s)
		if progress != nil {
			progress(r)
		}
	}
	r.Finished = time.Now().UTC()
	r.Finish()
	return r
}

func (rn *Runner) peer(p *plan.Plan, t *store.Target) engine.Peer {
	return engine.Peer{Host: t.Host, Port: t.Port, CalledAET: t.CalledAET, CallingAET: t.CallingAET, Timeout: p.Timeout}
}

func statusText(st *int) string {
	if st == nil {
		return "no status"
	}
	return fmt.Sprintf("0x%04X", *st)
}

func isSuccess(st *int) bool { return st != nil && *st == 0 }

func (rn *Runner) step(ctx context.Context, p *plan.Plan, ps plan.Step, t *store.Target, st *sent, s *result.Step) {
	s.Status = result.StepPassed // each branch must fail it explicitly
	// Production guard (DVT-SRS-028).
	if (ps.Type == plan.Store || ps.Type == plan.Move) && t.Class != store.Lab {
		s.Failf(fmt.Sprintf("refused: %s steps only run against targets of class lab; %s is %s", ps.Type, t.Name, t.Class))
		return
	}
	switch ps.Type {
	case plan.Validate:
		files, err := p.ResolveFiles(ps.Files)
		if err != nil {
			s.Failf(err.Error())
			return
		}
		names := map[string]string{}
		for _, f := range files {
			if rel, err := filepath.Rel(p.Dir, f); err == nil {
				names[f] = rel
			}
		}
		v := rn.Engines.Validate(ctx, files, names)
		v.ID = s.ID
		s.Validation = &v
		e, w, ee := v.Counts()
		s.Messages = append(s.Messages, fmt.Sprintf("%d files: %d errors, %d warnings, %d engine errors; verdict %s", len(files), e, w, ee, v.Verdict))
		if !v.Verdict.Passed() {
			s.Status = result.StepFailed
		}

	case plan.Echo:
		res, raw, err := rn.Engines.Echo(ctx, rn.peer(p, t))
		s.Details = rawJSON(raw)
		switch {
		case err != nil:
			s.Failf("engine error: " + err.Error())
		case !res.Associated:
			s.Failf("association not established: " + res.Error)
		case !isSuccess(res.Status):
			s.Failf("C-ECHO status " + statusText(res.Status))
		default:
			s.Messages = append(s.Messages, "C-ECHO status 0x0000 (Success)")
		}

	case plan.Reject:
		pe := rn.peer(p, t)
		if ps.CallingAET != "" {
			pe.CallingAET = ps.CallingAET
		}
		if ps.CalledAET != "" {
			pe.CalledAET = ps.CalledAET
		}
		res, raw, err := rn.Engines.Reject(ctx, pe)
		s.Details = rawJSON(raw)
		switch {
		case err != nil:
			s.Failf("engine error: " + err.Error())
		case res.Associated:
			s.Failf(fmt.Sprintf("association with calling AE %q, called AE %q was accepted; expected a rejection", pe.CallingAET, pe.CalledAET))
		case !res.Rejected:
			s.Failf("no association rejection received: " + res.Error)
		default:
			s.Messages = append(s.Messages, fmt.Sprintf("association with calling AE %q, called AE %q rejected as expected", pe.CallingAET, pe.CalledAET))
		}

	case plan.Store:
		files, err := p.ResolveFiles(ps.Files)
		if err != nil {
			s.Failf(err.Error())
			return
		}
		res, raw, err := rn.Engines.Store(ctx, rn.peer(p, t), files)
		s.Details = rawJSON(raw)
		if err != nil {
			s.Failf("engine error: " + err.Error())
			return
		}
		if !res.Associated {
			s.Failf("association not established: " + res.Error)
			return
		}
		if len(res.Files) != len(files) {
			s.Failf(fmt.Sprintf("engine reported %d results for %d files", len(res.Files), len(files)))
			return
		}
		ok := 0
		for i, f := range res.Files {
			if f.File != files[i] {
				s.Failf(fmt.Sprintf("engine result %d is for %s, expected %s", i, f.File, files[i]))
				return
			}
			rel, _ := filepath.Rel(p.Dir, f.File)
			if f.Error != "" || !isSuccess(f.Status) {
				s.Failf(fmt.Sprintf("%s: C-STORE status %s %s", rel, statusText(f.Status), f.Error))
				continue
			}
			ok++
			st.files = append(st.files, f.File)
			st.instances[f.StudyInstanceUID] = appendUnique(st.instances[f.StudyInstanceUID], f.SOPInstanceUID)
		}
		s.Messages = append(s.Messages, fmt.Sprintf("%d of %d files stored with status 0x0000", ok, len(files)))

	case plan.Find:
		studies := ps.Studies
		if ps.FromStore {
			studies = keys(st.instances)
		}
		if len(studies) == 0 {
			s.Failf("no studies to query (no earlier store step succeeded)")
			return
		}
		res, raw, err := rn.Engines.Find(ctx, rn.peer(p, t), studies)
		s.Details = rawJSON(raw)
		if err != nil {
			s.Failf("engine error: " + err.Error())
			return
		}
		if !res.Associated {
			s.Failf("association not established: " + res.Error)
			return
		}
		byStudy := map[string]engine.FindQuery{}
		for _, q := range res.Queries {
			byStudy[q.Study] = q
		}
		for _, uid := range studies {
			q, ok := byStudy[uid]
			switch {
			case !ok:
				s.Failf(uid + ": no result from engine")
			case !isSuccess(q.FinalStatus):
				s.Failf(fmt.Sprintf("%s: final C-FIND status %s", uid, statusText(q.FinalStatus)))
			case !contains(q.Matches, uid):
				s.Failf(uid + ": study not returned")
			default:
				s.Messages = append(s.Messages, fmt.Sprintf("%s: found (%d match(es))", uid, len(q.Matches)))
			}
		}

	case plan.Move:
		rn.move(ctx, p, ps, t, st, s)
	}
}

func (rn *Runner) move(ctx context.Context, p *plan.Plan, ps plan.Step, t *store.Target, st *sent, s *result.Step) {
	// Work out what must come back.
	expected := map[string][]string{} // study -> instances
	var sentFiles []string
	if ps.FromStore {
		for k, v := range st.instances {
			expected[k] = v
		}
		sentFiles = st.files
	} else {
		files, err := p.ResolveFiles(ps.Files)
		if err != nil {
			s.Failf(err.Error())
			return
		}
		probes, err := rn.Engines.Probe(ctx, files)
		if err != nil {
			s.Failf("engine error: " + err.Error())
			return
		}
		for _, pr := range probes {
			if !pr.DICOM || pr.SOPInstanceUID == "" || pr.StudyInstanceUID == "" {
				s.Failf(pr.File + ": not a DICOM instance with Study and SOP Instance UIDs")
				return
			}
			if len(ps.Studies) > 0 && !contains(ps.Studies, pr.StudyInstanceUID) {
				continue
			}
			expected[pr.StudyInstanceUID] = appendUnique(expected[pr.StudyInstanceUID], pr.SOPInstanceUID)
			sentFiles = append(sentFiles, pr.File)
		}
	}
	studies := keys(expected)
	if len(studies) == 0 {
		s.Failf("no studies to retrieve (no earlier store step succeeded or no file matched)")
		return
	}
	want := map[string]bool{}
	for _, insts := range expected {
		for _, i := range insts {
			want[i] = true
		}
	}
	if err := os.MkdirAll(rn.WorkDir, 0o750); err != nil {
		s.Failf(err.Error())
		return
	}
	dir, err := os.MkdirTemp(rn.WorkDir, "move-")
	if err != nil {
		s.Failf(err.Error())
		return
	}
	defer os.RemoveAll(dir)

	res, raw, err := rn.Engines.Move(ctx, rn.peer(p, t), rn.SCP, dir, studies, len(want))
	s.Details = rawJSON(raw)
	if err != nil {
		s.Failf("engine error: " + err.Error())
		return
	}
	if !res.Associated {
		s.Failf("association not established: " + res.Error)
		return
	}
	byStudy := map[string]engine.MoveStudy{}
	for _, m := range res.Moves {
		byStudy[m.Study] = m
	}
	for _, uid := range studies {
		m, ok := byStudy[uid]
		switch {
		case !ok:
			s.Failf(uid + ": no result from engine")
		case !isSuccess(m.FinalStatus):
			s.Failf(fmt.Sprintf("%s: final C-MOVE status %s", uid, statusText(m.FinalStatus)))
		case m.Failed != nil && *m.Failed > 0:
			s.Failf(fmt.Sprintf("%s: %d failed sub-operations", uid, *m.Failed))
		}
	}
	// Received set must equal the expected set (DVT-SRS-024).
	got := map[string]bool{}
	for _, rc := range res.Received {
		got[rc.SOPInstanceUID] = true
	}
	var missing, extra []string
	for i := range want {
		if !got[i] {
			missing = append(missing, i)
		}
	}
	for i := range got {
		if !want[i] {
			extra = append(extra, i)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		s.Failf(fmt.Sprintf("%d expected instance(s) not received: %v", len(missing), missing))
	}
	if len(extra) > 0 {
		s.Failf(fmt.Sprintf("%d unexpected instance(s) received: %v", len(extra), extra))
	}
	s.Messages = append(s.Messages, fmt.Sprintf("%d of %d expected instances received", len(want)-len(missing), len(want)))

	// Pixel comparison (DVT-SRS-025).
	if ps.ComparePixels {
		cmp, _, err := rn.Engines.Compare(ctx, sentFiles, dir)
		if err != nil {
			s.Failf("engine error (compare): " + err.Error())
			return
		}
		if len(cmp) != len(sentFiles) {
			s.Failf(fmt.Sprintf("compare: %d results for %d files", len(cmp), len(sentFiles)))
			return
		}
		same := 0
		for _, c := range cmp {
			rel, _ := filepath.Rel(p.Dir, c.File)
			if !c.Match {
				s.Failf(fmt.Sprintf("%s: %s", rel, firstNonEmpty(c.Error, "pixel data differs")))
				continue
			}
			if c.SentTransferSyntax != c.ReceivedTransferSyntax {
				if !nativeTS[c.SentTransferSyntax] || !nativeTS[c.ReceivedTransferSyntax] {
					// Compressed data arriving in another syntax was transcoded.
					s.Failf(fmt.Sprintf("%s: transcoded from %s to %s", rel, c.SentTransferSyntax, c.ReceivedTransferSyntax))
					continue
				}
				s.Messages = append(s.Messages, fmt.Sprintf("note: %s re-encoded from %s to %s (uncompressed; pixel data identical)", rel, c.SentTransferSyntax, c.ReceivedTransferSyntax))
			}
			same++
		}
		s.Messages = append(s.Messages, fmt.Sprintf("pixel data identical for %d of %d sent files", same, len(cmp)))
	}
}

// nativeTS are the uncompressed transfer syntaxes; switching between them
// keeps pixel values (a little-endian switch keeps the bytes too).
var nativeTS = map[string]bool{
	"1.2.840.10008.1.2":      true, // Implicit VR Little Endian
	"1.2.840.10008.1.2.1":    true, // Explicit VR Little Endian
	"1.2.840.10008.1.2.1.99": true, // Deflated Explicit VR Little Endian
	"1.2.840.10008.1.2.2":    true, // Explicit VR Big Endian (retired)
}

func firstNonEmpty(a ...string) string {
	for _, x := range a {
		if x != "" {
			return x
		}
	}
	return ""
}

func rawJSON(raw string) any {
	if len(raw) > 100000 {
		return raw[:100000] + "... (truncated)"
	}
	return raw
}

func keys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func appendUnique(list []string, s string) []string {
	if contains(list, s) {
		return list
	}
	return append(list, s)
}
