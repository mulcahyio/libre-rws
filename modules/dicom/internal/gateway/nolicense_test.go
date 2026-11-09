package gateway

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Licensing must never be able to stop DICOM services (HAZ-010): the DIMSE
// gateway may not import any licensing code, so no license state can refuse
// or interrupt an association, a C-STORE or a retrieval.
// Verifies: DCM-SRS-058
func TestGatewayHasNoLicenseDependency(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no gateway sources: %v", err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(p, "/licensing") || strings.Contains(p, "librekey") {
				t.Errorf("%s imports %s: the gateway must not depend on licensing", f, p)
			}
		}
	}
}
