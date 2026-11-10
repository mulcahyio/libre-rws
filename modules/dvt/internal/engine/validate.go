package engine

import (
	"context"
	"path/filepath"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Validate validates files with every engine (DVT-SRS-002…009). names maps a
// file path to its display name (may be nil). The returned Validation is
// finished (verdicts set).
func (c Config) Validate(ctx context.Context, files []string, names map[string]string) result.Validation {
	v := result.Validation{Created: time.Now().UTC(), Versions: c.Versions(ctx)}
	v.Files = make([]result.FileResult, len(files))
	for i, f := range files {
		name := names[f]
		if name == "" {
			name = filepath.Base(f)
		}
		v.Files[i] = result.FileResult{File: f, Name: name, Raw: map[string]string{}, Findings: []result.Finding{}}
	}
	if len(files) == 0 {
		v.Finish()
		v.Finished = time.Now().UTC()
		return v
	}

	// 1. Which files are DICOM at all (DVT-SRS-006)?
	probes, err := c.Probe(ctx, files)
	var dicomIdx []int
	if err != nil {
		for i := range v.Files {
			v.Files[i].EngineErrors = append(v.Files[i].EngineErrors, result.EngineError{Engine: Worker, Message: err.Error()})
		}
	} else {
		for i, p := range probes {
			if !p.DICOM {
				msg := "not a DICOM file"
				if p.Error != "" && p.Error != msg {
					msg += " (" + p.Error + ")"
				}
				v.Files[i].Findings = append(v.Files[i].Findings, result.Finding{Engine: "libredvt", File: files[i], Severity: result.Error, Message: msg})
				continue
			}
			dicomIdx = append(dicomIdx, i)
		}
	}

	// 2. dciodvfy per file.
	for _, i := range dicomIdx {
		fs, errs, raw := c.Dciodvfy(ctx, files[i])
		v.Files[i].Findings = append(v.Files[i].Findings, fs...)
		v.Files[i].EngineErrors = append(v.Files[i].EngineErrors, errs...)
		v.Files[i].Raw[Dciodvfy] = raw
	}

	// 3. dicom-validator, one call for all DICOM files.
	if len(dicomIdx) > 0 {
		dfiles := make([]string, len(dicomIdx))
		for k, i := range dicomIdx {
			dfiles[k] = files[i]
		}
		per, raw, err := c.DicomValidator(ctx, dfiles)
		for k, i := range dicomIdx {
			if err != nil {
				v.Files[i].EngineErrors = append(v.Files[i].EngineErrors, result.EngineError{Engine: DicomValidator, Message: err.Error()})
				continue
			}
			v.Files[i].Findings = append(v.Files[i].Findings, per[k]...)
		}
		if len(raw) > 200000 {
			raw = raw[:200000] + "\n... (truncated)"
		}
		v.SetRaw = "dicom-validator:\n" + raw

		// 4. Cross-file consistency (DVT-SRS-004).
		fs, errs, raw3 := c.Dcentvfy(ctx, dfiles)
		v.SetFindings = append(v.SetFindings, fs...)
		v.SetErrors = append(v.SetErrors, errs...)
		v.SetRaw += "\n\ndcentvfy:\n" + raw3
	}
	if v.SetFindings == nil {
		v.SetFindings = []result.Finding{}
	}
	v.Finish()
	v.Finished = time.Now().UTC()
	return v
}
