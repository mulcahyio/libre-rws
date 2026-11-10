package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Real dciodvfy -new output (dicom3tools 1.00.snapshot.20260927113651) for
// pydicom's CT_small.dcm with Modality removed and PatientSex=Q. dciodvfy
// writes the DICOMDIR warnings to stdout and the rest to stderr.
const ctBadStdout = "Warning - </Modality(0008,0060)> - Missing attribute or value that would be needed to build DICOMDIR\n"
const ctBadStderr = `CTImage
Error - </PatientSex(0010,0040)[1]> - Unrecognized enumerated value = <Q>
Warning - </PatientWeight(0010,1030)[1]> - Value is zero
Error - </Modality(0008,0060)> - Missing attribute for Type 1C Conditional - Module=<GeneralSeries>
Warning - </Laterality(0020,0060)> - is only permitted to be empty when actually unknown; should be absent (not empty) if an unpaired body part, and have a value if a paired body part
`

// Real dcentvfy output for two files with different Patient IDs in one study.
const dcentvfyConflict = `Error - Different PatientID for same StudyInstanceUID - StudyInstanceUID=<1.3.6.1.4.1.5962.1.2.4.20040826185059.5457> PatientID=<OTHER> for file </tmp/mr2.dcm> versus existing PatientID=<4MR1>
Error - Different PatientID for same SeriesInstanceUID - SeriesInstanceUID=<1.3.6.1.4.1.5962.1.3.4.1.20040826185059.5457> PatientID=<OTHER> for file </tmp/mr2.dcm> versus existing PatientID=<4MR1>
`

// Verifies: DVT-SRS-002, DVT-SRS-005
func TestParseDciodvfyRealOutput(t *testing.T) {
	fs, errs, raw := d3Check(Dciodvfy, "ct.dcm", Output{Stdout: ctBadStdout, Stderr: ctBadStderr, ExitCode: 1}, nil)
	if len(errs) != 0 {
		t.Fatalf("unexpected engine errors: %v", errs)
	}
	if len(fs) != 5 {
		t.Fatalf("want 5 findings, got %d: %+v", len(fs), fs)
	}
	var sawSex, sawModality bool
	for _, f := range fs {
		if f.Engine != Dciodvfy || f.File != "ct.dcm" {
			t.Errorf("bad attribution: %+v", f)
		}
		if f.Severity == result.Error && f.Path == "PatientSex(0010,0040)[1]" && strings.Contains(f.Message, "<Q>") {
			sawSex = true
		}
		if f.Severity == result.Error && f.Path == "Modality(0008,0060)" && strings.Contains(f.Message, "Type 1C") {
			sawModality = true
		}
	}
	if !sawSex || !sawModality {
		t.Fatalf("expected errors not parsed: %+v", fs)
	}
	if !strings.Contains(raw, "CTImage") {
		t.Fatal("raw output not kept")
	}
	if result.Decide(fs, errs) != result.Fail {
		t.Fatal("errors must fail")
	}
}

// Verifies: DVT-SRS-004
func TestParseDcentvfyRealOutput(t *testing.T) {
	fs, errs, _ := d3Check(Dcentvfy, "", Output{Stderr: dcentvfyConflict, ExitCode: 1}, nil)
	if len(errs) != 0 || len(fs) != 2 || fs[0].Severity != result.Error || !strings.Contains(fs[0].Message, "Different PatientID") {
		t.Fatalf("findings %+v errs %+v", fs, errs)
	}
}

// Verifies: DVT-SRS-007
func TestDicom3toolsUntrustworthyOutputIsEngineError(t *testing.T) {
	cases := map[string]Output{
		"unknown line":           {Stderr: "CTImage\nSegmentation fault in module X\n", ExitCode: 0},
		"abort line":             {Stderr: `Abort - File open for read failed - "/x"` + "\n", ExitCode: 1},
		"exit 1 without error":   {Stderr: "Warning - </A(0008,0001)> - w\n", ExitCode: 1},
		"unexpected exit status": {Stderr: "", ExitCode: 139},
	}
	for name, out := range cases {
		fs, errs, _ := d3Check(Dciodvfy, "f", out, nil)
		if len(errs) == 0 {
			t.Errorf("%s: no engine error", name)
		}
		if result.Decide(fs, errs) != result.Fail {
			t.Errorf("%s: verdict must be fail", name)
		}
	}
	_, errs, _ := d3Check(Dciodvfy, "f", Output{}, os.ErrNotExist)
	if len(errs) != 1 {
		t.Fatal("run error must become an engine error")
	}
}

// --- failure injection with fake engines -----------------------------------

func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeWorker answers probe/iod/versions. WORKER_MODE selects misbehaviour.
const fakeWorker = `
cmd="$1"; shift
case "$MODE" in
  crash) echo "Traceback: boom" >&2; exit 1 ;;
  garbage) echo "this is not json"; exit 0 ;;
  hang) sleep 30 ;;
esac
case "$cmd" in
  versions) echo '{"worker":"fake","standard-edition":"2026d"}' ;;
  probe)
    printf '{"files":['
    sep=""
    for f in "$@"; do
      case "$f" in
        *.txt) printf '%s{"file":"%s","dicom":false,"error":"not a DICOM file"}' "$sep" "$f" ;;
        *) printf '%s{"file":"%s","dicom":true,"sop_class_uid":"1.2.840.10008.5.1.4.1.1.2"}' "$sep" "$f" ;;
      esac
      sep=","
    done
    echo ']}' ;;
  iod)
    shift 4
    if [ "$MODE" = "iodshort" ]; then echo '{"files":[],"edition":"2026d"}'; exit 0; fi
    printf '{"edition":"2026d","files":['
    sep=""
    for f in "$@"; do printf '%s{"file":"%s","status":"Passed","findings":[]}' "$sep" "$f"; sep=","; done
    echo ']}' ;;
esac
`

func fakeConfig(t *testing.T, dciodvfyBody string) (Config, string) {
	t.Helper()
	dir := t.TempDir()
	return Config{
		DciodvfyBin:  script(t, dir, "dciodvfy", dciodvfyBody),
		DcentvfyBin:  script(t, dir, "dcentvfy", "exit 0"),
		Python:       "/bin/sh",
		Worker:       script(t, dir, "worker.sh", fakeWorker),
		StandardPath: dir, Edition: "2026d",
		Timeout: 3 * time.Second,
	}, dir
}

func touch(t *testing.T, dir, name string) string {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Verifies: DVT-SRS-001, DVT-SRS-006
func TestValidateNonDICOMNotPassedToIODValidators(t *testing.T) {
	c, dir := fakeConfig(t, `[ "$1" = -new ] && touch "$2.called"; echo CTImage >&2; exit 0`)
	good := touch(t, dir, "a.dcm")
	bad := touch(t, dir, "notes.txt")
	v := c.Validate(context.Background(), []string{good, bad}, nil)
	if v.Files[0].Verdict != result.Pass {
		t.Fatalf("good file: %+v", v.Files[0])
	}
	if v.Files[1].Verdict != result.Fail || len(v.Files[1].Findings) != 1 || !strings.Contains(v.Files[1].Findings[0].Message, "not a DICOM file") {
		t.Fatalf("non-DICOM file: %+v", v.Files[1])
	}
	if _, err := os.Stat(bad + ".called"); err == nil {
		t.Fatal("dciodvfy was run on a non-DICOM file")
	}
	if _, err := os.Stat(good + ".called"); err != nil {
		t.Fatal("dciodvfy was not run on the DICOM file")
	}
	if v.Verdict != result.Fail {
		t.Fatal("overall verdict must fail")
	}
}

// Verifies: DVT-SRS-007
func TestValidateEngineFailuresNeverPass(t *testing.T) {
	ok := `echo CTImage >&2; exit 0`
	cases := []struct {
		name, dciodvfy, mode string
		missingBinary        bool
	}{
		{"dciodvfy missing", ok, "", true},
		{"dciodvfy crashes", `kill -SEGV $$`, "", false},
		{"dciodvfy hangs", `sleep 30`, "", false},
		{"dciodvfy garbage", `echo "%%% corrupt"; exit 0`, "", false},
		{"worker crashes", ok, "crash", false},
		{"worker garbage", ok, "garbage", false},
		{"worker hangs", ok, "hang", false},
		{"worker drops results", ok, "iodshort", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, dir := fakeConfig(t, tc.dciodvfy)
			if tc.missingBinary {
				c.DciodvfyBin = filepath.Join(dir, "does-not-exist")
			}
			t.Setenv("MODE", tc.mode)
			f := touch(t, dir, "a.dcm")
			start := time.Now()
			v := c.Validate(context.Background(), []string{f}, nil)
			if v.Verdict != result.Fail || v.Files[0].Verdict != result.Fail {
				t.Fatalf("verdict %q / %q, want fail; %+v", v.Verdict, v.Files[0].Verdict, v.Files[0])
			}
			if len(v.Files[0].EngineErrors) == 0 {
				t.Fatalf("no engine error recorded: %+v", v.Files[0])
			}
			if time.Since(start) > 20*time.Second {
				t.Fatal("timeout not enforced")
			}
		})
	}
}

// Verifies: DVT-SRS-008
func TestVersionsReported(t *testing.T) {
	c, _ := fakeConfig(t, `echo "dicom3tools Version: 1.00.snapshot.20260927113651" >&2`)
	v := c.Versions(context.Background())
	if v["dicom3tools"] != "1.00.snapshot.20260927113651" || v["standard-edition"] != "2026d" || v["worker"] != "fake" {
		t.Fatalf("versions %v", v)
	}
	c.DciodvfyBin = "/nonexistent/dciodvfy"
	if v := c.Versions(context.Background()); !strings.HasPrefix(v["dicom3tools"], "unavailable") {
		t.Fatalf("missing engine must be reported, got %v", v)
	}
}

// Verifies: DVT-SRS-029
func TestPeerTimeoutPassedAndBounded(t *testing.T) {
	p := Peer{Host: "h", Port: 104, CalledAET: "A", CallingAET: "B", Timeout: 7 * time.Second}
	args := strings.Join(p.args("echo"), " ")
	if !strings.Contains(args, "--timeout 7") {
		t.Fatalf("timeout not passed to the worker: %s", args)
	}
	if p.processTimeout(1) >= time.Minute {
		t.Fatalf("process timeout too long: %s", p.processTimeout(1))
	}
	// A worker that never answers is killed and reported.
	c, _ := fakeConfig(t, "exit 0")
	t.Setenv("MODE", "hang")
	p.Timeout = 500 * time.Millisecond
	start := time.Now()
	_, _, err := c.Echo(context.Background(), p)
	if err == nil || time.Since(start) > 25*time.Second {
		t.Fatalf("hung worker: err=%v after %s", err, time.Since(start))
	}
}
