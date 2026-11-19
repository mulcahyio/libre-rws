package result

import (
	"encoding/json"
	"fmt"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/redact"
)

// SecretFound is the message of a step or run in which a secret was found.
func SecretFound(n int) string {
	return fmt.Sprintf("engine error: %d secret value(s) found in the output and replaced by %s; the run fails (DVT-SRS-152)", n, redact.Placeholder)
}

// Redact scans the step result for the run's secrets and replaces every
// hit. A hit fails the step (and so the run): a secret reached a place it
// must never reach (DVT-SRS-152). It returns the number of hits.
func (s *Step) Redact(secrets *redact.Set) int {
	if secrets.Empty() {
		return 0
	}
	b, err := json.Marshal(s)
	if err != nil {
		*s = Step{ID: s.ID, Type: s.Type, Verifies: s.Verifies, Started: s.Started, Duration: s.Duration}
		s.Failf("engine error: the step result could not be checked for secrets")
		return 0
	}
	clean, n := secrets.Bytes(b)
	if n == 0 {
		return 0
	}
	var out Step
	if err := json.Unmarshal(clean, &out); err != nil {
		out = Step{ID: s.ID, Type: s.Type, Verifies: s.Verifies, Started: s.Started, Duration: s.Duration}
	}
	if out.Evidence != nil {
		out.Evidence.Redactions += n
		out.Evidence.Problemf(ProblemEngine, "secret found in output")
		out.Verdict = Fail
	}
	out.Failf(SecretFound(n))
	*s = out
	return n
}

// Redact applies the same scan to the whole run, so run-level text
// (errors, descriptions) is covered too. A hit fails the run. It returns
// the number of hits.
func (r *Run) Redact(secrets *redact.Set) int {
	if secrets.Empty() {
		return 0
	}
	b, err := json.Marshal(r)
	if err != nil {
		r.Error = "engine error: the run result could not be checked for secrets"
		r.Verdict = Fail
		return 0
	}
	clean, n := secrets.Bytes(b)
	if n == 0 {
		return 0
	}
	var out Run
	if err := json.Unmarshal(clean, &out); err != nil {
		out = Run{ID: r.ID, Plan: r.Plan, PlanSHA256: r.PlanSHA256, Started: r.Started, Finished: r.Finished, Versions: r.Versions}
	}
	out.MarkSecretFound(n)
	*r = out
	return n
}

// MarkSecretFound fails the run because n secret values were found in its
// output (DVT-SRS-152).
func (r *Run) MarkSecretFound(n int) {
	msg := SecretFound(n)
	if r.Error == "" {
		r.Error = msg
	} else {
		r.Error += "; " + msg
	}
	r.Verdict = Fail
}
