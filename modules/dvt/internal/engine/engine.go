// Package engine runs LibreDVT's external validation and DICOM network
// engines (dicom3tools and the Python worker) and turns their output into
// result.Findings. Any doubt about an engine's output becomes an
// EngineError, never a silent pass (DVT-SRS-007).
//
// LibreDVT must not decode DICOM with the libraries LibreDCM uses
// (DVT-SRS-080): this package only starts processes and parses text/JSON.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Engine names used in findings and versions.
const (
	Dciodvfy       = "dciodvfy"
	Dcentvfy       = "dcentvfy"
	DicomValidator = "dicom-validator"
	Worker         = "worker"
)

// Config says where the engines are.
type Config struct {
	DciodvfyBin  string        // path or name of dciodvfy
	DcentvfyBin  string        // path or name of dcentvfy
	Python       string        // python interpreter for the worker
	Worker       string        // path of libredvt_worker.py
	StandardPath string        // dicom-validator standard directory
	Edition      string        // DICOM standard edition, e.g. 2026d
	Timeout      time.Duration // per engine invocation
}

// Output of one process.
type Output struct {
	Stdout, Stderr string
	ExitCode       int
}

// run starts a process with an argument vector (never a shell) and a timeout.
// A start failure, a timeout or death by signal is an error.
func run(ctx context.Context, timeout time.Duration, name string, args ...string) (Output, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- engine path from admin config; args are validated file paths and plan values, no shell
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	out := Output{Stdout: so.String(), Stderr: se.String(), ExitCode: -1}
	if cmd.ProcessState != nil {
		out.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("timed out after %s", timeout)
	}
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return out, fmt.Errorf("could not run %s: %w", name, err)
	}
	if out.ExitCode < 0 {
		return out, fmt.Errorf("%s was killed (%v)", name, err)
	}
	return out, nil
}

// --- dicom3tools ---------------------------------------------------------

var (
	d3Line    = regexp.MustCompile(`^(Error|Warning) - (?:<([^>]*)> - )?(.*)$`)
	d3IODName = regexp.MustCompile(`^[A-Za-z0-9]+$`) // e.g. "CTImage": the IOD dciodvfy chose
)

// ParseDicom3tools parses dciodvfy -new / dcentvfy output (stdout and stderr
// combined). It returns the findings, the IOD name if printed, and any line
// it does not understand. Unknown lines make the result untrustworthy; the
// caller turns them into an EngineError.
func ParseDicom3tools(engine, file, text string) (findings []result.Finding, iod string, unknown []string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := d3Line.FindStringSubmatch(line); m != nil {
			sev := result.Error
			if m[1] == "Warning" {
				sev = result.Warning
			}
			path := strings.TrimPrefix(m[2], "/")
			findings = append(findings, result.Finding{Engine: engine, File: file, Severity: sev, Path: path, Message: m[3]})
			continue
		}
		if d3IODName.MatchString(line) && iod == "" {
			iod = line
			continue
		}
		unknown = append(unknown, line)
	}
	return findings, iod, unknown
}

// d3Check turns a dicom3tools invocation into findings or an engine error.
// dciodvfy and dcentvfy exit 0 (no errors) or 1 (errors reported); anything
// else, unknown output, "Abort" lines, or exit 1 with no error finding is an
// engine error.
func d3Check(engine, file string, out Output, runErr error) ([]result.Finding, []result.EngineError, string) {
	raw := out.Stdout + out.Stderr
	if runErr != nil {
		return nil, []result.EngineError{{Engine: engine, Message: runErr.Error()}}, raw
	}
	findings, _, unknown := ParseDicom3tools(engine, file, out.Stdout+"\n"+out.Stderr)
	var errs []result.EngineError
	if len(unknown) > 0 {
		errs = append(errs, result.EngineError{Engine: engine, Message: "unrecognised output: " + firstN(unknown, 3)})
	}
	nerr := 0
	for _, f := range findings {
		if f.Severity == result.Error {
			nerr++
		}
	}
	switch {
	case out.ExitCode != 0 && out.ExitCode != 1:
		errs = append(errs, result.EngineError{Engine: engine, Message: fmt.Sprintf("exit status %d", out.ExitCode)})
	case out.ExitCode == 1 && nerr == 0:
		errs = append(errs, result.EngineError{Engine: engine, Message: "exit status 1 without a reported error"})
	}
	return findings, errs, raw
}

func firstN(lines []string, n int) string {
	if len(lines) > n {
		return strings.Join(lines[:n], " | ") + fmt.Sprintf(" (+%d more)", len(lines)-n)
	}
	return strings.Join(lines, " | ")
}

// Dciodvfy validates one file (DVT-SRS-002).
func (c Config) Dciodvfy(ctx context.Context, file string) ([]result.Finding, []result.EngineError, string) {
	out, err := run(ctx, c.Timeout, c.DciodvfyBin, "-new", file)
	return d3Check(Dciodvfy, file, out, err)
}

// Dcentvfy checks consistency across files (DVT-SRS-004).
func (c Config) Dcentvfy(ctx context.Context, files []string) ([]result.Finding, []result.EngineError, string) {
	out, err := run(ctx, c.Timeout, c.DcentvfyBin, files...)
	return d3Check(Dcentvfy, "", out, err)
}

// --- Python worker -------------------------------------------------------

// WorkerCall runs the worker and decodes its JSON into v. A non-zero exit,
// a timeout or output that is not one JSON document is an error.
func (c Config) WorkerCall(ctx context.Context, timeout time.Duration, v any, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = c.Timeout
	}
	out, err := run(ctx, timeout, c.Python, append([]string{c.Worker}, args...)...)
	if err != nil {
		return out.Stdout, fmt.Errorf("worker %s: %w: %s", args[0], err, strings.TrimSpace(tail(out.Stderr)))
	}
	if out.ExitCode != 0 {
		return out.Stdout, fmt.Errorf("worker %s: exit status %d: %s", args[0], out.ExitCode, strings.TrimSpace(tail(out.Stderr)))
	}
	dec := json.NewDecoder(strings.NewReader(out.Stdout))
	if err := dec.Decode(v); err != nil {
		return out.Stdout, fmt.Errorf("worker %s: unparseable output: %w", args[0], err)
	}
	if dec.More() {
		return out.Stdout, fmt.Errorf("worker %s: more than one JSON document in output", args[0])
	}
	return out.Stdout, nil
}

func tail(s string) string {
	if len(s) > 600 {
		return "..." + s[len(s)-600:]
	}
	return s
}

// ProbeFile is the worker's view of one file.
type ProbeFile struct {
	File              string `json:"file"`
	DICOM             bool   `json:"dicom"`
	Error             string `json:"error"`
	SOPClassUID       string `json:"sop_class_uid"`
	SOPInstanceUID    string `json:"sop_instance_uid"`
	StudyInstanceUID  string `json:"study_instance_uid"`
	TransferSyntaxUID string `json:"transfer_syntax_uid"`
}

// Probe reads the identifying attributes of files (DVT-SRS-006).
func (c Config) Probe(ctx context.Context, files []string) ([]ProbeFile, error) {
	var doc struct {
		Files []ProbeFile `json:"files"`
	}
	if _, err := c.WorkerCall(ctx, 0, &doc, append([]string{"probe"}, files...)...); err != nil {
		return nil, err
	}
	if len(doc.Files) != len(files) {
		return nil, fmt.Errorf("worker probe: %d results for %d files", len(doc.Files), len(files))
	}
	for i := range files {
		if doc.Files[i].File != files[i] {
			return nil, fmt.Errorf("worker probe: result %d is for %q, expected %q", i, doc.Files[i].File, files[i])
		}
	}
	return doc.Files, nil
}

type iodFinding struct {
	Severity string `json:"severity"`
	Module   string `json:"module"`
	Tag      string `json:"tag"`
	Message  string `json:"message"`
}

type iodFile struct {
	File     string       `json:"file"`
	Status   string       `json:"status"`
	Findings []iodFinding `json:"findings"`
}

// DicomValidator validates files with dicom-validator (DVT-SRS-003). It
// returns per-file findings in input order, or an error for the whole call.
func (c Config) DicomValidator(ctx context.Context, files []string) ([][]result.Finding, string, error) {
	var doc struct {
		Files   []iodFile `json:"files"`
		Edition string    `json:"edition"`
	}
	raw, err := c.WorkerCall(ctx, c.Timeout+time.Duration(len(files))*5*time.Second, &doc,
		append([]string{"iod", "--standard-path", c.StandardPath, "--edition", c.Edition}, files...)...)
	if err != nil {
		return nil, raw, err
	}
	if len(doc.Files) != len(files) {
		return nil, raw, fmt.Errorf("dicom-validator: %d results for %d files", len(doc.Files), len(files))
	}
	outF := make([][]result.Finding, len(files))
	for i, f := range doc.Files {
		if f.File != files[i] {
			return nil, raw, fmt.Errorf("dicom-validator: result %d is for %q, expected %q", i, f.File, files[i])
		}
		switch f.Status {
		case "Passed", "Failed", "MissingSOPClassUID", "UnknownSOPClassUID", "InvalidFile", "MissingFile":
		default:
			return nil, raw, fmt.Errorf("dicom-validator: unknown status %q for %s", f.Status, f.File)
		}
		if f.Status == "Failed" && len(f.Findings) == 0 {
			return nil, raw, fmt.Errorf("dicom-validator: status Failed without findings for %s", f.File)
		}
		for _, x := range f.Findings {
			sev := result.Error
			if x.Severity == "warning" {
				sev = result.Warning
			}
			msg := x.Message
			if x.Module != "" {
				msg = "Module " + x.Module + ": " + msg
			}
			outF[i] = append(outF[i], result.Finding{Engine: DicomValidator, File: files[i], Severity: sev, Path: x.Tag, Message: msg})
		}
	}
	return outF, raw, nil
}

// Versions reports the versions of every engine (DVT-SRS-008). Missing
// engines are reported as "unavailable: ..." so a report never hides them.
func (c Config) Versions(ctx context.Context) map[string]string {
	v := map[string]string{}
	vt := 30 * time.Second
	if c.Timeout > 0 && c.Timeout < vt {
		vt = c.Timeout
	}
	out, err := run(ctx, vt, c.DciodvfyBin, "-version")
	if err != nil {
		v["dicom3tools"] = "unavailable: " + err.Error()
	} else if m := regexp.MustCompile(`dicom3tools Version: (\S+)`).FindStringSubmatch(out.Stdout + out.Stderr); m != nil {
		v["dicom3tools"] = m[1]
	} else {
		v["dicom3tools"] = "unknown"
	}
	var w map[string]string
	if _, err := c.WorkerCall(ctx, vt, &w, "versions", "--standard-path", c.StandardPath, "--edition", c.Edition); err != nil {
		v["worker"] = "unavailable: " + err.Error()
		return v
	}
	for k, val := range w {
		v[k] = val
	}
	return v
}
