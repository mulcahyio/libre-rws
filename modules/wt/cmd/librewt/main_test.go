package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/platform/config"
)

func load(t *testing.T, yaml string) Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "wt.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaults()
	if err := config.Load(p, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func open(t *testing.T, cfg Config) (names []string, logged string, err error) {
	t.Helper()
	acct, aerr := accounts.Open(context.Background(), filepath.Join(t.TempDir(), "wt.db"))
	if aerr != nil {
		t.Fatal(aerr)
	}
	defer acct.Close()
	var buf bytes.Buffer
	reg, err := openFleet(cfg, acct, slog.New(slog.NewJSONHandler(&buf, nil)))
	if err != nil {
		return nil, buf.String(), err
	}
	return reg.Names(), buf.String(), nil
}

// The configuration names one or more LibreDCM instances, each with its own
// token (value or file), optional CA file and cluster label. The dcm: block
// of 0.1 still works, as one instance named "default". Libre WT refuses to
// start on duplicate names, names with other characters, a missing token, or
// when both forms are given. Plain HTTP to another machine is logged at
// startup. No token is logged.
// Verifies: WT-SRS-013, WT-SRS-036
func TestInstancesConfiguration(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "dcm-2.token")
	if err := os.WriteFile(tokenFile, []byte("file-token-SECRET\n"), 0o600); err != nil { // gitleaks:allow (synthetic test value)
		t.Fatal(err)
	}
	fleetYAML := `
status_cache: 9s
instances:
  - name: dcm-1
    cluster: site-a
    url: https://dcm-1.example:8081
    token: inline-token-SECRET  # gitleaks:allow (synthetic test value)
  - name: dcm-2
    cluster: site-a
    url: http://10.20.30.40:8081
    token_file: ` + tokenFile + `
  - name: local_3
    url: http://127.0.0.1:8081
    token: third-token-SECRET  # gitleaks:allow (synthetic test value)
`
	cfg := load(t, fleetYAML)
	if cfg.StatusCache.Seconds() != 9 || len(cfg.Instances) != 3 || cfg.Instances[0].Cluster != "site-a" || cfg.Instances[1].TokenFile != tokenFile {
		t.Fatalf("cfg = %+v", cfg)
	}
	names, logged, err := open(t, cfg)
	if err != nil || strings.Join(names, ",") != "dcm-1,dcm-2,local_3" {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	if strings.Contains(logged, "SECRET") {
		t.Fatalf("token in the log: %s", logged)
	}
	// The plain-HTTP warning names dcm-2 only: dcm-1 is HTTPS, local_3 is
	// on this machine.
	var warned []string
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, "plain HTTP") {
			warned = append(warned, line)
		}
	}
	if len(warned) != 1 || !strings.Contains(warned[0], `"instance":"dcm-2"`) || !strings.Contains(warned[0], `"level":"WARN"`) {
		t.Fatalf("plain HTTP warnings = %v", warned)
	}

	// The example file that ships with Libre WT: one instance, "default".
	example := defaults()
	if err := config.Load(filepath.Join("..", "..", "config.example.yaml"), &example); err != nil {
		t.Fatal(err)
	}
	if conns, err := example.instances(); err != nil || len(conns) != 1 || conns[0].Name != "default" || conns[0].TokenFile != "./dcm-admin-token" || example.StatusCache.Seconds() != 5 {
		t.Fatalf("example configuration: %+v, %v", conns, err)
	}

	// The 0.1 form: one instance named "default".
	old := load(t, "dcm:\n  url: http://127.0.0.1:8081\n  token: old-token-SECRET\n") // gitleaks:allow (synthetic test value)
	if names, _, err := open(t, old); err != nil || len(names) != 1 || names[0] != "default" {
		t.Fatalf("0.1 configuration: names = %v, err = %v", names, err)
	}
	// Environment variables of 0.1 still give that one instance.
	t.Setenv("LIBRE_WT_DCM_URL", "http://127.0.0.1:9999")
	t.Setenv("LIBRE_WT_DCM_TOKEN", "env-token-SECRET") // gitleaks:allow (synthetic test value)
	if names, _, err := open(t, load(t, "")); err != nil || len(names) != 1 || names[0] != "default" {
		t.Fatalf("0.1 environment: names = %v, err = %v", names, err)
	}
	// ... and are refused next to an instances list: which LibreDCM is
	// meant would not be clear.
	if _, _, err := open(t, load(t, fleetYAML)); err == nil || !strings.Contains(err.Error(), "both instances") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("instances plus dcm token: %v", err)
	}
	t.Setenv("LIBRE_WT_DCM_TOKEN", "")
	t.Setenv("LIBRE_WT_DCM_URL", "")

	for name, yaml := range map[string]string{
		"no token at all": "",
		"duplicate name":  "instances:\n  - {name: a, url: 'http://127.0.0.1:1', token: t}\n  - {name: a, url: 'http://127.0.0.1:2', token: t}\n",
		"duplicate case":  "instances:\n  - {name: a, url: 'http://127.0.0.1:1', token: t}\n  - {name: A, url: 'http://127.0.0.1:2', token: t}\n",
		"bad character":   "instances:\n  - {name: 'a/b', url: 'http://127.0.0.1:1', token: t}\n",
		"dots":            "instances:\n  - {name: '..', url: 'http://127.0.0.1:1', token: t}\n",
		"missing token":   "instances:\n  - {name: a, url: 'http://127.0.0.1:1', token: t}\n  - {name: b, url: 'http://127.0.0.1:2'}\n",
		"missing file":    "instances:\n  - {name: a, url: 'http://127.0.0.1:1', token_file: /nonexistent/token}\n",
		"bad url":         "instances:\n  - {name: a, url: 'ftp://127.0.0.1:1', token: t}\n",
		"no name":         "instances:\n  - {url: 'http://127.0.0.1:1', token: t}\n",
	} {
		if names, _, err := open(t, load(t, yaml)); err == nil {
			t.Errorf("%s: started with instances %v", name, names)
		}
	}
}
