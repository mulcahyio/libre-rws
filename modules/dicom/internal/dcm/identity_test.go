package dcm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/buildinfo"
	"github.com/mulcahyio/libre-rws/platform/dicomuid"
)

// Verifies: DCM-SRS-098
func TestImplementationFromBuildVersion(t *testing.T) {
	uid, name := dicomuid.Implementation(dicomuid.LibreDCM, buildinfo.Version)
	if ImplementationClassUID != uid || ImplementationVersionName != name {
		t.Fatalf("dcm identifies as %q / %q, build version %q gives %q / %q",
			ImplementationClassUID, ImplementationVersionName, buildinfo.Version, uid, name)
	}
	if !strings.HasPrefix(ImplementationClassUID, "1.3.6.1.4.1.67686.1.1.1.") || !strings.HasPrefix(ImplementationVersionName, "LIBREDCM_") {
		t.Fatalf("not under the LibreDCM product arc: %q / %q", ImplementationClassUID, ImplementationVersionName)
	}
	if buildinfo.Version == "dev" && (ImplementationClassUID != "1.3.6.1.4.1.67686.1.1.1.0.0" || ImplementationVersionName != "LIBREDCM_0_0") {
		t.Fatalf("dev build identifies as %q / %q", ImplementationClassUID, ImplementationVersionName)
	}
	file, err := Part10("1.2.840.10008.5.1.4.1.1.7", "1.3.6.1.4.1.67686.9.1.99", ExplicitVRLittleEndian, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// (0002,0012) UI and (0002,0013) SH, explicit VR, even-padded.
	for _, want := range [][]byte{
		append([]byte{0x02, 0x00, 0x12, 0x00, 'U', 'I', byte(len(padUI(uid))), 0}, padUI(uid)...),
		append([]byte{0x02, 0x00, 0x13, 0x00, 'S', 'H', byte(len(padText(name))), 0}, padText(name)...),
	} {
		if !strings.Contains(string(file), string(want)) {
			t.Fatalf("Part 10 meta lacks %q", want)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	d, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			t.Fatal("repo root not found")
		}
		d = p
	}
}

// goFiles lists the shipped (non-test) Go files of our own code.
func goFiles(t *testing.T, dirs ...string) []string {
	t.Helper()
	var out []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// Every association LibreDCM accepts or requests must carry its own
// Implementation Class UID and Version Name. gonetdicom fills in its own
// (1.2.826.0.1.3680043.10.541.1, GONETDICOM_001) for a field left empty, so
// every ae.Config and ae.ServerConfig literal in shipped code must set both
// from package dcm.
// Verifies: DCM-SRS-098
func TestEveryAssociationConfigSetsImplementation(t *testing.T) {
	root := repoRoot(t)
	n := 0
	for _, p := range goFiles(t, filepath.Join(root, "modules"), filepath.Join(root, "platform")) {
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		aeName := ""
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == "github.com/godicom-dev/gonetdicom/ae" {
				aeName = "ae"
				if imp.Name != nil {
					aeName = imp.Name.Name
				}
			}
		}
		if aeName == "" {
			continue
		}
		ast.Inspect(f, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != aeName || (sel.Sel.Name != "Config" && sel.Sel.Name != "ServerConfig") {
				return true
			}
			n++
			set := map[string]string{}
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				k, _ := kv.Key.(*ast.Ident)
				if v, ok := kv.Value.(*ast.SelectorExpr); ok && k != nil {
					if pkg, ok := v.X.(*ast.Ident); ok {
						set[k.Name] = pkg.Name + "." + v.Sel.Name
					}
				}
			}
			for _, field := range []string{"ImplementationClassUID", "ImplementationVersionName"} {
				if set[field] != "dcm."+field {
					t.Errorf("%s: ae.%s literal does not set %s: dcm.%s", p, sel.Sel.Name, field, field)
				}
			}
			return true
		})
	}
	// gateway (accept), C-MOVE sub-operations, control C-ECHO test, -echo.
	if n < 4 {
		t.Fatalf("found %d association configs; the scan is broken", n)
	}
}

// The test-data arc (1.3.6.1.4.1.67686.9) and gonetdicom's root must never
// appear in shipped code. Test-only fake packages (fake*) are allowed the
// test-data arc if nothing but tests imports them.
// Verifies: DCM-SRS-100
func TestTestDataArcOnlyInTests(t *testing.T) {
	root := repoRoot(t)
	files := goFiles(t, filepath.Join(root, "modules"), filepath.Join(root, "platform"))
	files = append(files, filepath.Join(root, "modules", "dvt", "worker", "libredvt_worker.py"))
	fakes := map[string]bool{}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if strings.Contains(s, "3680043.10.541") {
			t.Errorf("%s contains gonetdicom's UID root", p)
		}
		if strings.Contains(s, "67686.9") {
			if strings.HasPrefix(filepath.Base(filepath.Dir(p)), "fake") {
				fakes[filepath.Dir(p)] = true
				continue
			}
			t.Errorf("%s contains the test-data UID arc 1.3.6.1.4.1.67686.9", p)
		}
	}
	if len(files) < 50 {
		t.Fatalf("only %d files scanned; the walk is broken", len(files))
	}
	// A fake package with test data must only be imported by tests.
	for dir := range fakes {
		rel, _ := filepath.Rel(root, dir)
		imp := "github.com/mulcahyio/libre-rws/" + filepath.ToSlash(rel)
		for _, p := range files {
			if !strings.HasSuffix(p, ".go") || filepath.Dir(p) == dir {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range f.Imports {
				if strings.Trim(i.Path.Value, `"`) == imp {
					t.Errorf("%s (shipped code) imports test fake %s, which holds test-data UIDs", p, imp)
				}
			}
		}
	}
}
