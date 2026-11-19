package report

import (
	"encoding/json"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/redact"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Artefacts are the files written for a run.
type Artefacts struct {
	JSON, HTML, JUnit []byte
}

// Render produces a run's JSON, HTML and JUnit artefacts and scans each
// for the target's secrets before the caller writes anything
// (DVT-SRS-152, evidence-model.md §5). A hit is replaced by [REDACTED]
// and fails the run; the artefacts are then rendered again from the failed
// run, so the returned bytes never hold a secret and always show the
// failure. Hits reports the number of secret values found.
func Render(r *result.Run, secrets *redact.Set) (a Artefacts, hits int, err error) {
	hits = r.Redact(secrets) // the data itself, also for run.json in the store
	for range 2 {
		var n, total int
		if a.JSON, err = json.MarshalIndent(r, "", "  "); err != nil {
			return a, hits, err
		}
		a.JSON = append(a.JSON, '\n')
		if a.HTML, err = HTML(r); err != nil {
			return a, hits, err
		}
		if a.JUnit, err = JUnit(r); err != nil {
			return a, hits, err
		}
		a.JSON, n = secrets.Bytes(a.JSON)
		total += n
		a.HTML, n = secrets.Bytes(a.HTML)
		total += n
		a.JUnit, n = secrets.Bytes(a.JUnit)
		total += n
		if total == 0 {
			return a, hits, nil
		}
		hits += total
		r.MarkSecretFound(total)
	}
	return a, hits, nil
}
