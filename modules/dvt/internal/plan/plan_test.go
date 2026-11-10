package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const good = `
name: LibreDCM conformance
target: lab
steps:
  - id: v
    type: validate
    files: [fixtures/*.dcm]
  - id: echo
    type: echo
    verifies: [DCM-SRS-001]
  - id: reject
    type: reject
    called_aet: WRONG
  - id: store
    type: store
    files: [fixtures/*.dcm]
  - id: find
    type: find
    from_store: true
  - id: move
    type: move
    from_store: true
    compare_pixels: true
    verifies: [DCM-SRS-010]
`

// Verifies: DVT-SRS-040
func TestParseGoodPlan(t *testing.T) {
	p, err := Parse([]byte(good), "/plans")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "LibreDCM conformance" || len(p.Steps) != 6 || p.Steps[5].Verifies[0] != "DCM-SRS-010" || !p.Steps[5].ComparePixels {
		t.Fatalf("%+v", p)
	}
	if len(p.SHA256) != 64 || p.Timeout.Seconds() != 30 {
		t.Fatalf("sha %q timeout %s", p.SHA256, p.Timeout)
	}
}

// Verifies: DVT-SRS-040
func TestParseRejectsMistakesBeforeRunning(t *testing.T) {
	cases := map[string]string{
		"typo in key":              strings.Replace(good, "verifies: [DCM-SRS-001]", "verfies: [DCM-SRS-001]", 1),
		"unknown step type":        strings.Replace(good, "type: echo", "type: ehco", 1),
		"bad requirement ID":       strings.Replace(good, "DCM-SRS-001", "DCM-001", 1),
		"duplicate id":             strings.Replace(good, "id: echo", "id: v", 1),
		"no name":                  strings.Replace(good, "name: LibreDCM conformance", "", 1),
		"no target":                strings.Replace(good, "target: lab", "", 1),
		"field for wrong type":     strings.Replace(good, "    type: echo\n", "    type: echo\n    compare_pixels: true\n", 1),
		"from_store first":         "name: x\ntarget: t\nsteps:\n  - id: f\n    type: find\n    from_store: true\n",
		"move without expectation": "name: x\ntarget: t\nsteps:\n  - id: m\n    type: move\n    studies: [1.2.3]\n",
		"bad UID":                  "name: x\ntarget: t\nsteps:\n  - id: f\n    type: find\n    studies: [abc]\n",
		"bad AE":                   strings.Replace(good, "called_aet: WRONG", "called_aet: WAY-TOO-LONG-AE-TITLE", 1),
		"no steps":                 "name: x\nsteps: []\n",
		"two documents":            good + "\n---\nname: y\n",
		"empty":                    "",
	}
	for name, src := range cases {
		if _, err := Parse([]byte(src), "/plans"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Verifies: DVT-SRS-040
func TestResolveFilesEveryPatternMustMatch(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "fx"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "fx", "a.dcm"), []byte("x"), 0o644)
	p := &Plan{Dir: dir}
	got, err := p.ResolveFiles([]string{"fx/*.dcm", "fx/a.dcm"})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := p.ResolveFiles([]string{"fx/*.dcm", "missing/*.dcm"}); err == nil {
		t.Fatal("a pattern that matches nothing must be an error")
	}
}

func TestValidAET(t *testing.T) {
	for _, ok := range []string{"A", "LIBREDCM", "1234567890123456", "MY AE"} {
		if !ValidAET(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "   ", "12345678901234567", "A\\B", "A\nB"} {
		if ValidAET(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
