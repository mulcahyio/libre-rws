// Package result holds LibreDVT's result model and the verdict rules. The
// rules live here, in one place, so every caller (web UI, CLI, reports)
// decides pass and fail the same way (DVT-SRS-009, DVT-SRS-042).
package result

import "time"

// Severity of a finding.
type Severity string

// Finding severities.
const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Finding is one problem reported by an engine (DVT-SRS-005).
type Finding struct {
	Engine   string   `json:"engine"`
	File     string   `json:"file,omitempty"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path,omitempty"` // attribute path or tag, when the engine gives one
	Message  string   `json:"message"`
}

// EngineError records that an engine could not produce a trustworthy result
// (not run, crashed, timed out, unparseable output). It always fails the
// verdict (DVT-SRS-007).
type EngineError struct {
	Engine  string `json:"engine"`
	Message string `json:"message"`
}

// Verdict of a file, validation, step or run.
type Verdict string

// Verdicts.
const (
	Pass             Verdict = "pass"
	PassWithWarnings Verdict = "pass with warnings"
	Fail             Verdict = "fail"
)

// Passed reports whether v counts as a pass.
func (v Verdict) Passed() bool { return v == Pass || v == PassWithWarnings }

// Decide applies DVT-SRS-009: fail on any error finding or engine error,
// pass with warnings if only warnings, pass otherwise.
func Decide(findings []Finding, engineErrors []EngineError) Verdict {
	if len(engineErrors) > 0 {
		return Fail
	}
	warn := false
	for _, f := range findings {
		switch f.Severity {
		case Warning:
			warn = true
		default: // errors and anything unexpected fail safe
			return Fail
		}
	}
	if warn {
		return PassWithWarnings
	}
	return Pass
}

// FileResult is the validation result of one file.
type FileResult struct {
	File         string            `json:"file"`
	Name         string            `json:"name"`          // display name (relative to the upload)
	Raw          map[string]string `json:"raw,omitempty"` // engine -> raw output (DVT-SRS-005)
	Findings     []Finding         `json:"findings"`
	EngineErrors []EngineError     `json:"engine_errors,omitempty"`
	Verdict      Verdict           `json:"verdict"`
}

// Validation is one object-validation run over a set of files.
type Validation struct {
	ID          string            `json:"id"`
	Label       string            `json:"label"`
	Created     time.Time         `json:"created"`
	Finished    time.Time         `json:"finished"`
	Running     bool              `json:"running,omitempty"`
	Files       []FileResult      `json:"files"`
	SetFindings []Finding         `json:"set_findings"` // cross-file consistency (dcentvfy)
	SetErrors   []EngineError     `json:"set_engine_errors,omitempty"`
	SetRaw      string            `json:"set_raw,omitempty"`
	Versions    map[string]string `json:"versions"` // DVT-SRS-008
	Verdict     Verdict           `json:"verdict"`
}

// Finish computes the file verdicts and the overall verdict: the worst file
// verdict, also failing on set-level errors. An empty validation fails, so
// "nothing was checked" can never look like a pass.
func (v *Validation) Finish() {
	var all []Finding
	var errs []EngineError
	for i := range v.Files {
		f := &v.Files[i]
		f.Verdict = Decide(f.Findings, f.EngineErrors)
		all = append(all, f.Findings...)
		errs = append(errs, f.EngineErrors...)
	}
	all = append(all, v.SetFindings...)
	errs = append(errs, v.SetErrors...)
	if len(v.Files) == 0 {
		errs = append(errs, EngineError{Engine: "libredvt", Message: "no files to validate"})
	}
	v.Verdict = Decide(all, errs)
	v.Running = false
}

// Counts returns the number of error and warning findings.
func (v *Validation) Counts() (errors, warnings, engineErrors int) {
	count := func(fs []Finding) {
		for _, f := range fs {
			if f.Severity == Warning {
				warnings++
			} else {
				errors++
			}
		}
	}
	for _, f := range v.Files {
		count(f.Findings)
		engineErrors += len(f.EngineErrors)
	}
	count(v.SetFindings)
	engineErrors += len(v.SetErrors)
	return
}

// StepStatus of a plan step.
type StepStatus string

// Step statuses. NotRun counts as failed (DVT-SRS-042).
const (
	StepPassed StepStatus = "passed"
	StepFailed StepStatus = "failed"
	StepNotRun StepStatus = "not run"
)

// Step is the result of one plan step.
type Step struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Verifies   []string      `json:"verifies,omitempty"`
	Status     StepStatus    `json:"status"`
	Messages   []string      `json:"messages,omitempty"` // why it failed, or what was checked
	Started    time.Time     `json:"started"`
	Duration   time.Duration `json:"duration"`
	Validation *Validation   `json:"validation,omitempty"`
	Details    any           `json:"details,omitempty"` // raw worker output
}

// Failf marks the step failed with a message.
func (s *Step) Failf(msg string) {
	s.Status = StepFailed
	s.Messages = append(s.Messages, msg)
}

// Run is the result of a test plan run.
type Run struct {
	ID         string            `json:"id"`
	Plan       string            `json:"plan"`
	PlanSHA256 string            `json:"plan_sha256"`
	Target     string            `json:"target"`
	TargetDesc string            `json:"target_desc"`
	Started    time.Time         `json:"started"`
	Finished   time.Time         `json:"finished"`
	Running    bool              `json:"running,omitempty"`
	Error      string            `json:"error,omitempty"` // plan-level failure (plan invalid, target unknown)
	Steps      []Step            `json:"steps"`
	Versions   map[string]string `json:"versions"`
	Verdict    Verdict           `json:"verdict"`
}

// Finish applies DVT-SRS-042: the run passes only if it has steps, no
// run-level error, and every step passed.
func (r *Run) Finish() {
	r.Running = false
	r.Verdict = Pass
	if r.Error != "" || len(r.Steps) == 0 {
		r.Verdict = Fail
		return
	}
	for _, s := range r.Steps {
		if s.Status != StepPassed {
			r.Verdict = Fail
			return
		}
	}
}
