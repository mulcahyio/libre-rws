package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const synthToken = "dvt-synthetic-bearer-cli-0123456789abc" // gitleaks:allow (synthetic test value)

// Verifies: DVT-SRS-140, DVT-SRS-142, DVT-SRS-152
func TestRunWithTargetFileKeepsSecretsOutOfArtefacts(t *testing.T) {
	dir := setupFakes(t)
	// dciodvfy that "leaks" the token into its finding, as a misbehaving
	// engine or a peer echoing a header would.
	leak := filepath.Join(dir, "dciodvfy-leak")
	if err := os.WriteFile(leak, []byte("#!/bin/sh\necho \"Error - upstream said Bearer "+synthToken+"\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBRE_DVT_DCIODVFY", leak)
	tok := filepath.Join(dir, "token")
	_ = os.WriteFile(tok, []byte(synthToken+"\n"), 0o600)
	targets := filepath.Join(dir, "targets.yaml")
	_ = os.WriteFile(targets, []byte("targets:\n  - name: web\n    class: lab\n    protocol: dicomweb\n    base_url: https://pacs.lab.example/dicom-web\n    auth: {type: bearer, token_file: "+tok+"}\n"), 0o600)

	plan := filepath.Join(dir, "web.yaml")
	_ = os.WriteFile(plan, []byte("name: web\ntarget: web\nsteps:\n  - {id: v, type: validate, files: [fx/good.dcm]}\n  - {id: q, type: qido, level: studies, expect_none: true}\n"), 0o644)
	out := map[string]string{"-junit": filepath.Join(dir, "j.xml"), "-html": filepath.Join(dir, "r.html"), "-json": filepath.Join(dir, "r.json")}
	args := []string{"-targets", targets}
	for k, v := range out {
		args = append(args, k, v)
	}
	if code := runCmd(append(args, plan)); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	forms := []string{synthToken, base64.StdEncoding.EncodeToString([]byte(synthToken))}
	// Every artefact and every file in the data directory.
	files := []string{out["-junit"], out["-html"], out["-json"]}
	_ = filepath.Walk(filepath.Join(dir, "data"), func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range forms {
			if strings.Contains(string(b), s) {
				t.Fatalf("secret form in %s", f)
			}
		}
	}
	j, _ := os.ReadFile(out["-json"])
	for _, want := range []string{"[REDACTED]", "secret value(s) found", "worker qido", `"kind": "engine"`} {
		if !strings.Contains(string(j), want) {
			t.Fatalf("JSON report lacks %q", want)
		}
	}

	// The same plan against a dimse target from the file is rejected
	// before any step runs.
	_ = os.WriteFile(targets, []byte("targets:\n  - {name: web, class: lab, host: h, port: 104, called_aet: A}\n"), 0o600)
	if code := runCmd([]string{"-targets", targets, "-json", out["-json"], plan}); code != 1 {
		t.Fatalf("protocol mismatch exit %d, want 1", code)
	}
	j, _ = os.ReadFile(out["-json"])
	if !strings.Contains(string(j), "plan rejected for this target") || !strings.Contains(string(j), `"status": "not run"`) {
		t.Fatalf("mismatch not reported: %s", j)
	}
	// A target file with an unknown or inline-secret field is refused (exit 2).
	_ = os.WriteFile(targets, []byte("targets:\n  - {name: web, class: lab, protocol: dicomweb, base_url: \"https://h/dw\", auth: {type: bearer, token: x}}\n"), 0o600)
	if code := runCmd([]string{"-targets", targets, plan}); code != 2 {
		t.Fatalf("inline token exit %d, want 2", code)
	}
}
