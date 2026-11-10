package store

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Verifies: DVT-SRS-020
func TestTargetsDefaultToProduction(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutTarget(Target{Name: "pacs", Host: "10.0.0.5", Port: 104, CalledAET: "PACS", CallingAET: "DVT"}); err != nil {
		t.Fatal(err)
	}
	tg, ok, _ := s.Target("pacs")
	if !ok || tg.Class != Production {
		t.Fatalf("missing class must mean production, got %+v", tg)
	}
	// Hand-edited registry without a class is also production.
	_ = os.WriteFile(filepath.Join(s.Dir, "targets.json"), []byte(`[{"name":"x","host":"h","port":1,"called_aet":"A","calling_aet":"B"}]`), 0o600)
	tg, _, _ = s.Target("x")
	if tg.Class != Production {
		t.Fatalf("hand-edited target: %+v", tg)
	}
	if err := s.PutTarget(Target{Name: "lab", Host: "libredcm", Port: 11112, CalledAET: "LIBREDCM", CallingAET: "LIBREDVT", Class: "LAB"}); err != nil {
		t.Fatal(err)
	}
	ts, _ := s.Targets()
	if len(ts) != 2 || ts[0].Name != "lab" || ts[0].Class != Lab {
		t.Fatalf("%+v", ts)
	}
	if err := s.DeleteTarget("lab"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Target("lab"); ok {
		t.Fatal("not deleted")
	}
}

func TestTargetValidation(t *testing.T) {
	base := Target{Name: "t", Host: "h", Port: 104, CalledAET: "A", CallingAET: "B", Class: Lab}
	bad := []func(*Target){
		func(t *Target) { t.Name = "bad name" },
		func(t *Target) { t.Host = "" },
		func(t *Target) { t.Host = "-oProxyCommand" },
		func(t *Target) { t.Host = "a b" },
		func(t *Target) { t.Port = 0 },
		func(t *Target) { t.Port = 70000 },
		func(t *Target) { t.CalledAET = "THIS-IS-TOO-LONG-1" },
		func(t *Target) { t.CallingAET = "" },
		func(t *Target) { t.Class = "staging" },
	}
	for i, mut := range bad {
		x := base
		mut(&x)
		if err := x.Normalize(); err == nil {
			t.Errorf("case %d accepted: %+v", i, x)
		}
	}
	x := base
	if err := x.Normalize(); err != nil {
		t.Fatal(err)
	}
}

// Verifies: DVT-SRS-045
func TestResultsNewestFirstAndDelete(t *testing.T) {
	s, _ := Open(t.TempDir())
	ids := []string{"20261110-100000-00000001", "20261110-120000-00000002", "20261110-110000-00000003"}
	for _, id := range ids {
		if err := s.SaveRun(&result.Run{ID: id, Started: time.Now()}); err != nil {
			t.Fatal(err)
		}
		_ = s.SaveValidation(&result.Validation{ID: id})
		_ = os.MkdirAll(s.UploadDir(id), 0o750)
	}
	runs := s.Runs()
	if len(runs) != 3 || runs[0].ID != ids[1] || runs[2].ID != ids[0] {
		t.Fatalf("order: %v %v %v", runs[0].ID, runs[1].ID, runs[2].ID)
	}
	if err := s.Delete(ids[1]); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{s.RunDir(ids[1]), s.ValidationDir(ids[1]), s.UploadDir(ids[1])} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", d)
		}
	}
	if err := s.Delete("../../etc"); err == nil {
		t.Fatal("invalid ID accepted")
	}
	if len(s.Runs()) != 2 || len(s.Validations()) != 2 {
		t.Fatal("wrong counts after delete")
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"a.dcm": "a.dcm", "dir/b.dcm": "dir/b.dcm", "dir\\c.dcm": "dir/c.dcm",
		"../x": "", "/etc/passwd": "", "a/../../x": "", "..": "", "-rf": "", "ok/-x": "",
	} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for n, c := range files {
		f, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(c))
	}
	_ = w.Close()
	return b.Bytes()
}

func TestSaveUploadZip(t *testing.T) {
	dir := t.TempDir()
	budget := int64(1 << 20)
	got, err := SaveUpload(dir, "study.zip", bytes.NewReader(zipOf(t, map[string]string{"s/1.dcm": "a", "s/2.dcm": "b", "__MACOSX/._1.dcm": "junk"})), &budget)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for p, name := range got {
		if !strings.HasPrefix(p, dir) || !strings.HasPrefix(name, "study.zip/s/") {
			t.Fatalf("%s -> %s", p, name)
		}
	}
	// Zip slip.
	budget = 1 << 20
	if _, err := SaveUpload(dir, "evil.zip", bytes.NewReader(zipOf(t, map[string]string{"../../escape.dcm": "x"})), &budget); err == nil {
		t.Fatal("zip entry escaping the upload dir accepted")
	}
	// Size budget.
	budget = 3
	if _, err := SaveUpload(dir, "big.dcm", strings.NewReader("12345"), &budget); err == nil {
		t.Fatal("upload over budget accepted")
	}
}
