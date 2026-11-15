package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The read-only control endpoints work through the real wiring of build():
// the real runtime, gateway, index, licensing and configuration loader. Every
// endpoint is refused without the token, answers with it, reports the live
// settings after they change, and never returns a configured secret, whether
// it came from the file or the environment.
// Verifies: DCM-SRS-080, DCM-SRS-081, DCM-SRS-082, DCM-SRS-083
func TestControlReadOnlyWiring(t *testing.T) {
	const (
		token      = "wiring-admin-token-7f3a9c"
		s3Access   = "AKIAWIRINGACCESS01"
		s3Secret   = "wiring-s3-secret-from-env-55"
		busPass    = "wiring-bus-password-91"
		tokenInEnv = "" // the token comes from the file in this test
	)
	t.Setenv("LIBRE_DCM_ADMIN_TOKEN", tokenInEnv)
	t.Setenv("LIBRE_DCM_REMOTE_AES", "")
	t.Setenv("LIBRE_DCM_ALLOWED_AES", "")
	t.Setenv("LIBRE_DCM_STORAGE_S3_SECRET_KEY", s3Secret)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := `admin:
  addr: "127.0.0.1:0"
  node_id: wiring-node
  token: ` + token + `
dicom:
  ae_title: WIRING
  listen: "127.0.0.1:0"
  allowed_aes: [CT1, "MR1@192.168.1.20"]
  remote_aes:
    VIEWER: {host: 127.0.0.1, port: 11113}
storage:
  driver: fs
  path: ` + filepath.Join(dir, "blobs") + `
  s3:
    access_key: ` + s3Access + `
index:
  driver: sqlite
  dsn: ` + filepath.Join(dir, "index.db") + `
events:
  driver: log
  url: nats://bus:` + busPass + `@nats-1:4222
license:
  data_dir: ` + filepath.Join(dir, "license") + `
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := build(cfgPath)
	if a != nil {
		defer a.close()
	}
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// A careless log line and audit record that name every secret: the
	// response filter must still keep them out (second control behind the
	// setting allow-list).
	a.rt.Logger.Warn("careless", "token", token, "s3", s3Access+" "+s3Secret, "bus", busPass)
	a.rt.Logger.Info("careless audit", "audit", true, "event", "Query", "outcome", "success",
		"detail", token+" "+s3Secret)
	h := a.rt.AdminHandler()
	get := func(path, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	paths := []string{"/control/v1", "/control/v1/status", "/control/v1/config", "/control/v1/remote-aes",
		"/control/v1/allow-list", "/control/v1/audit", "/control/v1/logs", "/control/v1/conflicts"}
	secrets := []string{token, s3Access, s3Secret, busPass}
	for _, p := range paths {
		for _, tok := range []string{"", "wrong-token"} {
			if rec := get(p, tok); rec.Code != http.StatusUnauthorized {
				t.Errorf("GET %s with token %q = %d, want 401", p, tok, rec.Code)
			}
		}
		rec := get(p, token)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d: %s", p, rec.Code, rec.Body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s: Cache-Control = %q", p, rec.Header().Get("Cache-Control"))
		}
		for _, s := range secrets {
			if strings.Contains(rec.Body.String(), s) {
				t.Errorf("GET %s returns the secret %q", p, s)
			}
		}
	}

	var conf struct {
		Settings []map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(get("/control/v1/config", token).Body.Bytes(), &conf); err != nil {
		t.Fatal(err)
	}
	byKey := map[string]map[string]any{}
	for _, s := range conf.Settings {
		byKey[s["key"].(string)] = s
	}
	for key, src := range map[string]string{"admin.token": "file", "storage.s3.access_key": "file", "storage.s3.secret_key": "env"} {
		s := byKey[key]
		if s == nil || s["redacted"] != true || s["set"] != true || s["source"] != src || s["value"] != nil {
			t.Errorf("%s = %v, want redacted, set, source %s", key, s, src)
		}
	}
	if s := byKey["events.url"]; s == nil || s["value"] != "nats://nats-1:4222" {
		t.Errorf("events.url = %v", s)
	}
	if s := byKey["dicom.ae_title"]; s == nil || s["value"] != "WIRING" || s["reloadable"] != false {
		t.Errorf("dicom.ae_title = %v", s)
	}
	if s := byKey["dicom.allowed_aes"]; s == nil || s["reloadable"] != true {
		t.Errorf("dicom.allowed_aes = %v", s)
	}

	// The allow-list shown is the one in force: change it as a reload would.
	before := get("/control/v1/allow-list", token).Body.String()
	if !strings.Contains(before, `"MR1@192.168.1.20"`) {
		t.Fatalf("allow-list = %s", before)
	}
	if err := a.gw.SetAllowedAEs([]string{"US1"}); err != nil {
		t.Fatal(err)
	}
	after := get("/control/v1/allow-list", token).Body.String()
	if !strings.Contains(after, `"US1"`) || strings.Contains(after, "MR1") {
		t.Errorf("allow-list after change = %s", after)
	}
	if rec := get("/control/v1/remote-aes", token); !strings.Contains(rec.Body.String(), `"VIEWER"`) {
		t.Errorf("remote-aes = %s", rec.Body)
	}
	var st map[string]any
	if err := json.Unmarshal(get("/control/v1/status", token).Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	inst := st["instance"].(map[string]any)
	if inst["node_id"] != "wiring-node" || inst["ae_title"] != "WIRING" || st["ready"] != true {
		t.Errorf("status = %v", st)
	}
}
