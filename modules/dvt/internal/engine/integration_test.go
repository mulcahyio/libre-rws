//go:build engines

// Engine integration tests: run only inside the LibreDVT test image
// (`docker build -f deploy/docker/Dockerfile.dvt --target test .`), where the
// real dicom3tools, dicom-validator, pydicom and pynetdicom are installed.
// These are the scripted positive controls of the LibreDVT tool validation
// (docs/dhf/libredvt/risk-management.md §4).

package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// RealConfig is the engine configuration inside the test image.
func RealConfig(t *testing.T) Config {
	t.Helper()
	assets := os.Getenv("LIBRE_DVT_TEST_ASSETS")
	if assets == "" {
		t.Skip("LIBRE_DVT_TEST_ASSETS not set (run in the LibreDVT test image)")
	}
	return Config{DciodvfyBin: "dciodvfy", DcentvfyBin: "dcentvfy", Python: "python3",
		Worker:       filepath.Join(assets, "worker", "libredvt_worker.py"),
		StandardPath: filepath.Join(assets, "standard"), Edition: "2026d", Timeout: time.Minute}
}

// makeFixtures writes deliberately broken files with pydicom.
func makeFixtures(t *testing.T, dir string) map[string]string {
	t.Helper()
	fx := filepath.Join(os.Getenv("LIBRE_DVT_TEST_ASSETS"), "plans", "fixtures")
	script := `
import sys, pydicom
fx, out = sys.argv[1], sys.argv[2]
def ct(): return pydicom.dcmread(fx + "/ct_explicit_le.dcm")
d = ct(); del d.StudyInstanceUID; d.save_as(out + "/missing_type1.dcm")
d = ct(); d.PatientSex = "Q"; d.save_as(out + "/bad_enum.dcm")
d = ct(); d.PatientID = "X" * 80; d.save_as(out + "/too_long_lo.dcm")
d = ct(); d.save_as(out + "/conflict_a.dcm")
d = ct(); d.PatientID = "SOMEONE-ELSE"; d.SOPInstanceUID = d.SOPInstanceUID + ".1"; d.file_meta.MediaStorageSOPInstanceUID = d.SOPInstanceUID; d.save_as(out + "/conflict_b.dcm")
open(out + "/notes.txt", "w").write("this is not DICOM\n")
`
	cmd := exec.Command("python3", "-c", script, fx, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixtures: %v\n%s", err, out)
	}
	return map[string]string{
		"good":          filepath.Join(fx, "ct_explicit_le.dcm"),
		"missing_type1": filepath.Join(dir, "missing_type1.dcm"),
		"bad_enum":      filepath.Join(dir, "bad_enum.dcm"),
		"too_long_lo":   filepath.Join(dir, "too_long_lo.dcm"),
		"conflict_a":    filepath.Join(dir, "conflict_a.dcm"),
		"conflict_b":    filepath.Join(dir, "conflict_b.dcm"),
		"not_dicom":     filepath.Join(dir, "notes.txt"),
	}
}

func hasFinding(fs []result.Finding, engine, substr string) bool {
	for _, f := range fs {
		if f.Engine == engine && f.Severity == result.Error && strings.Contains(f.Message+" "+f.Path, substr) {
			return true
		}
	}
	return false
}

// Verifies: DVT-SRS-002, DVT-SRS-003, DVT-SRS-006, DVT-SRS-009
func TestEnginePositiveControlsPerFile(t *testing.T) {
	c := RealConfig(t)
	f := makeFixtures(t, t.TempDir())
	ctx := context.Background()

	v := c.Validate(ctx, []string{f["good"]}, nil)
	if !v.Verdict.Passed() || len(v.Files[0].EngineErrors) != 0 {
		t.Fatalf("known-good file: verdict %s, %+v", v.Verdict, v.Files[0])
	}

	cases := []struct {
		key, engine, want string
	}{
		{"missing_type1", Dciodvfy, "StudyInstanceUID"},
		{"missing_type1", DicomValidator, "Study Instance UID"},
		{"bad_enum", Dciodvfy, "Unrecognized enumerated value"},
		{"bad_enum", DicomValidator, "enum value 'Q' not allowed"},
		{"too_long_lo", Dciodvfy, "PatientID"},
	}
	for _, tc := range cases {
		v := c.Validate(ctx, []string{f[tc.key]}, nil)
		if v.Verdict != result.Fail {
			t.Errorf("%s: verdict %s, want fail", tc.key, v.Verdict)
		}
		if !hasFinding(v.Files[0].Findings, tc.engine, tc.want) {
			t.Errorf("%s: %s did not report %q; findings: %+v", tc.key, tc.engine, tc.want, v.Files[0].Findings)
		}
	}

	v = c.Validate(ctx, []string{f["not_dicom"]}, nil)
	if v.Verdict != result.Fail || !hasFinding(v.Files[0].Findings, "libredvt", "not a DICOM file") {
		t.Fatalf("non-DICOM: %+v", v.Files[0])
	}
	if _, ran := v.Files[0].Raw[Dciodvfy]; ran {
		t.Fatal("dciodvfy ran on a non-DICOM file")
	}
}

// Verifies: DVT-SRS-004
func TestEnginePositiveControlConsistency(t *testing.T) {
	c := RealConfig(t)
	f := makeFixtures(t, t.TempDir())
	v := c.Validate(context.Background(), []string{f["conflict_a"], f["conflict_b"]}, nil)
	if v.Verdict != result.Fail || !hasFinding(v.SetFindings, Dcentvfy, "Different PatientID") {
		t.Fatalf("conflicting Patient IDs not detected: %+v / %+v", v.SetFindings, v.SetErrors)
	}
}

// Verifies: DVT-SRS-008, DVT-SRS-064
func TestEngineVersionsAndBuiltInStandard(t *testing.T) {
	c := RealConfig(t)
	v := c.Versions(context.Background())
	if !strings.HasPrefix(v["dicom3tools"], "1.00.snapshot.") || v["standard-edition"] != "2026d" || v["dicom-validator"] != "0.9.0" || v["pynetdicom"] == "" {
		t.Fatalf("versions: %v", v)
	}
	if _, err := os.Stat(filepath.Join(c.StandardPath, "2026d", "docbook")); err == nil {
		t.Fatal("DocBook sources left in the image")
	}
}
