package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fake engines: dciodvfy reports an error for files named bad*.
const fakeD3 = `#!/bin/sh
case "$2" in
  *bad*) echo "Error - </Modality(0008,0060)> - Missing attribute for Type 1" >&2; exit 1 ;;
  *) echo "CTImage" >&2; exit 0 ;;
esac
`

const fakeWorker = `cmd="$1"; shift
case "$cmd" in
  versions) echo '{"worker":"fake"}' ;;
  probe) printf '{"files":['; s=""; for f in "$@"; do printf '%s{"file":"%s","dicom":true}' "$s" "$f"; s=","; done; echo ']}' ;;
  iod) shift 4; printf '{"edition":"2026d","files":['; s=""; for f in "$@"; do printf '%s{"file":"%s","status":"Passed","findings":[]}' "$s" "$f"; s=","; done; echo ']}' ;;
esac
`

func setupFakes(t *testing.T) string {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Setenv("LIBRE_DVT_DCIODVFY", write("dciodvfy", fakeD3, 0o755))
	t.Setenv("LIBRE_DVT_DCENTVFY", write("dcentvfy", "#!/bin/sh\nexit 0\n", 0o755))
	t.Setenv("LIBRE_DVT_PYTHON", "/bin/sh")
	t.Setenv("LIBRE_DVT_WORKER", write("worker.sh", fakeWorker, 0o644))
	t.Setenv("LIBRE_DVT_DATA", filepath.Join(dir, "data"))
	_ = os.MkdirAll(filepath.Join(dir, "fx"), 0o755)
	write("fx/good.dcm", "x", 0o644)
	write("fx/bad.dcm", "x", 0o644)
	return dir
}

// Verifies: DVT-SRS-041, DVT-SRS-043, DVT-SRS-044
func TestRunCommandExitStatusAndOutputs(t *testing.T) {
	dir := setupFakes(t)
	goodPlan := filepath.Join(dir, "good.yaml")
	_ = os.WriteFile(goodPlan, []byte("name: good\nsteps:\n  - {id: v, type: validate, files: [fx/good.dcm], verifies: [DVT-SRS-002]}\n"), 0o644)
	badPlan := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(badPlan, []byte("name: bad\nsteps:\n  - {id: v, type: validate, files: [fx/*.dcm]}\n"), 0o644)
	junit := filepath.Join(dir, "j.xml")
	html := filepath.Join(dir, "r.html")

	if code := runCmd([]string{"-junit", junit, "-html", html, goodPlan}); code != 0 {
		t.Fatalf("passing plan exit %d", code)
	}
	b, _ := os.ReadFile(junit)
	var doc struct {
		Failures int `xml:"failures,attr"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil || doc.Failures != 0 || !strings.Contains(string(b), "DVT-SRS-002") {
		t.Fatalf("junit: %v %s", err, b)
	}
	h, _ := os.ReadFile(html)
	if !strings.Contains(string(h), "good") {
		t.Fatal("html report missing")
	}
	if code := runCmd([]string{badPlan}); code != 1 {
		t.Fatalf("failing plan exit %d, want 1", code)
	}
	_ = os.WriteFile(badPlan, []byte("name: bad\nsteps:\n  - {id: v, type: validate, filez: [x]}\n"), 0o644)
	if code := runCmd([]string{badPlan}); code != 2 {
		t.Fatalf("invalid plan exit %d, want 2", code)
	}
	// Network plan whose target is not registered: steps not run, exit 1.
	net := filepath.Join(dir, "net.yaml")
	_ = os.WriteFile(net, []byte("name: n\ntarget: nowhere\nsteps:\n  - {id: e, type: echo}\n"), 0o644)
	if code := runCmd([]string{net}); code != 1 {
		t.Fatalf("unknown target exit %d, want 1", code)
	}
}

// Verifies: DVT-SRS-001
func TestValidateCommandDirectories(t *testing.T) {
	dir := setupFakes(t)
	if code := validateCmd([]string{filepath.Join(dir, "fx", "good.dcm")}); code != 0 {
		t.Fatalf("good file exit %d", code)
	}
	out := filepath.Join(dir, "v.json")
	if code := validateCmd([]string{"-json", out, filepath.Join(dir, "fx")}); code != 1 {
		t.Fatalf("directory with a bad file exit %d, want 1", code)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), "bad.dcm") || !strings.Contains(string(b), "good.dcm") {
		t.Fatalf("directory not walked: %s", b)
	}
}
