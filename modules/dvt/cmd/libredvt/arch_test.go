package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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

// Verifies: DVT-SRS-080
func TestNoLibreDCMDICOMLibraries(t *testing.T) {
	root := filepath.Join(repoRoot(t), "modules", "dvt")
	banned := []string{"github.com/godicom-dev/", "github.com/mulcahyio/libre-rws/modules/dicom", "github.com/mulcahyio/libre-rws/third_party"}
	n := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		n++
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, b := range banned {
				if strings.HasPrefix(path, b) {
					t.Errorf("%s imports %s: LibreDVT must stay independent of LibreDCM's DICOM stack", p, path)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 10 {
		t.Fatalf("only %d Go files scanned; walk is broken", n)
	}
	// The Python worker must not use LibreDCM's libraries either (it cannot
	// import Go, but guard against a future pip dependency on a Go binding).
	req, err := os.ReadFile(filepath.Join(root, "worker", "requirements.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(req)), "godicom") {
		t.Fatal("worker depends on godicom")
	}
}

// Verifies: DVT-SRS-063
func TestComposePublishesOnLocalhostByDefault(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "compose", "docker-compose.dvt.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ports := regexp.MustCompile(`(?m)^\s+- "([^"]+)"\s*$`).FindAllStringSubmatch(string(b), -1)
	if len(ports) < 2 {
		t.Fatalf("no port mappings found")
	}
	for _, p := range ports {
		if !strings.HasPrefix(p[1], "${LIBRE_DVT_BIND_IP:-127.0.0.1}:") {
			t.Errorf("port %q is not bound to 127.0.0.1 by default", p[1])
		}
	}
}

// Verifies: DVT-SRS-064, DVT-SRS-065
func TestImageIsPinnedNonRootAndOffline(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "deploy", "docker", "Dockerfile.dvt"))
	if err != nil {
		t.Fatal(err)
	}
	df := string(b)
	for _, m := range regexp.MustCompile(`(?m)^FROM\s+(\S+)`).FindAllStringSubmatch(df, -1) {
		img := m[1]
		if img == "engines" {
			continue
		}
		if !strings.Contains(img, "@sha256:") {
			t.Errorf("base image %s is not pinned by digest", img)
		}
	}
	if !regexp.MustCompile(`(?m)^USER 10001`).MatchString(df) {
		t.Error("runtime image does not switch to a non-root user")
	}
	if !strings.Contains(df, "build_assets.py") {
		t.Error("standard is not built into the image")
	}
	req, _ := os.ReadFile(filepath.Join(root, "modules", "dvt", "worker", "requirements.txt"))
	for _, line := range strings.Split(string(req), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+==[0-9][0-9A-Za-z.]*$`).MatchString(line) {
			t.Errorf("engine requirement %q is not pinned to an exact version", line)
		}
	}
	w, _ := os.ReadFile(filepath.Join(root, "modules", "dvt", "worker", "libredvt_worker.py"))
	if !strings.Contains(string(w), "load_dicom_info(args.edition)") || strings.Contains(string(w), "get_edition_path") {
		t.Error("worker must read the built-in standard, not download one at runtime")
	}
}
