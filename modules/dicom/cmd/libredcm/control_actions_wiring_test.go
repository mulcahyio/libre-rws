package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/licensing"
)

// The action endpoints work through the real wiring of build(): the real
// runtime, DICOM gateway (listening), index, licensing, reloader and
// configuration loader. The test runs in source builds and, with
// `-tags official`, in builds that enforce a licence.
//
// It shows end to end that:
//   - every action needs the admin token;
//   - a C-ECHO test from the node reaches a real DICOM listener and reports
//     a refused association as a failed stage;
//   - reload goes through the node's one reload path: the plan shows the
//     difference, apply puts it in force in the gateway, and a broken or
//     invalid configuration is reported and leaves the gateway as it was;
//   - an unlicensed, unacknowledged official build blocks plan and reload
//     (423) until the acknowledgement is made through the API;
//   - licence actions, also failing ones, never stop the DICOM listener;
//   - a backup lands only in the configured directory;
//   - the audit records of the actions are in the audit view.
//
// Verifies: DCM-SRS-086, DCM-SRS-087, DCM-SRS-088, DCM-SRS-089, DCM-SRS-090, DCM-SRS-097, DCM-SRS-058, DCM-SRS-060
func TestControlActionsWiring(t *testing.T) {
	const token = "wiring-admin-token-4be1d7"
	t.Setenv("LIBRE_DCM_ADMIN_TOKEN", "")
	t.Setenv("LIBRE_DCM_REMOTE_AES", "")
	t.Setenv("LIBRE_DCM_ALLOWED_AES", "")
	t.Setenv("LIBRE_DCM_CONTROL_BACKUP_DIR", "")
	t.Setenv("LIBREKEY_HOST_ID", "wiring-host")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	cfgPath := filepath.Join(dir, "config.yaml")
	config := func(allowed, remotes string) string {
		return `admin:
  addr: "127.0.0.1:0"
  node_id: wiring-node
  token: ` + token + `
dicom:
  ae_title: WIRING
  listen: "127.0.0.1:0"
  allowed_aes: ` + allowed + `
  remote_aes:
    SELF: {host: 127.0.0.1, port: ` + fmt.Sprint(port) + `, ae_title: WIRING}
` + remotes + `storage:
  driver: fs
  path: ` + filepath.Join(dir, "blobs") + `
index:
  driver: sqlite
  dsn: ` + filepath.Join(dir, "index.db") + `
events:
  driver: log
license:
  file: ` + filepath.Join(dir, "license", "license.lic") + `
  data_dir: ` + filepath.Join(dir, "license") + `
control:
  backup_dir: ` + backups + `
`
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(cfgPath, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(config(`[CT1, WIRING]`, ""))
	a, err := build(cfgPath)
	if a != nil {
		defer a.close()
	}
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.gw.Serve(ctx, ln) }()

	h := a.rt.AdminHandler()
	seq := 0
	call := func(method, path, tok string, body map[string]any) (int, map[string]any, string) {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			if _, has := body["request_id"]; !has && method == "POST" && !strings.HasSuffix(path, "/plan") {
				seq++
				body["request_id"] = fmt.Sprintf("wiring-request-%04d", seq)
				body["operator"], body["reason"] = "Pat Admin", "wiring test"
			}
			raw, _ := json.Marshal(body)
			rd = bytes.NewReader(raw)
		} else {
			rd = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rd)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if strings.Contains(rec.Body.String(), token) {
			t.Errorf("%s %s returns the admin token", method, path)
		}
		return rec.Code, out, rec.Body.String()
	}
	echoSelf := func(when string) {
		t.Helper()
		code, body, raw := call("POST", "/control/v1/echo", token, map[string]any{"remote_ae": "SELF"})
		if code != 200 || body["ok"] != true || body["called_ae"] != "WIRING" || body["calling_ae"] != "WIRING" {
			t.Fatalf("%s: C-ECHO to the node's own DICOM listener = %d %s", when, code, raw)
		}
	}

	// Token.
	for _, p := range []string{"echo", "reload", "reload/plan", "license/ack", "license/request", "license/install", "license/activate", "backup"} {
		for _, tok := range []string{"", "wrong"} {
			if code, _, _ := call("POST", "/control/v1/"+p, tok, map[string]any{}); code != http.StatusUnauthorized {
				t.Errorf("POST %s with token %q = %d, want 401", p, tok, code)
			}
		}
	}
	for _, p := range []string{"license", "backups"} {
		if code, _, _ := call("GET", "/control/v1/"+p, "", nil); code != http.StatusUnauthorized {
			t.Errorf("GET %s without token = %d, want 401", p, code)
		}
	}

	// C-ECHO: a real association with the real gateway.
	echoSelf("at start")
	code, body, raw := call("POST", "/control/v1/echo", token, map[string]any{"host": "127.0.0.1", "port": port, "ae_title": "NOTTHISNODE"})
	if code != 200 || body["ok"] != false || body["stage"] != "association" {
		t.Fatalf("C-ECHO to a wrong called AE = %d %s, want a failed association", code, raw)
	}
	code, body, raw = call("POST", "/control/v1/echo", token, map[string]any{"remote_ae": "SELF", "expect_node_id": "another-node"})
	if code != http.StatusConflict || body["code"] != "instance_mismatch" {
		t.Fatalf("echo for another node = %d %s", code, raw)
	}

	// Licence. An official build without a licence blocks reloads until an
	// administrator acknowledges; a source build needs none.
	code, body, raw = call("GET", "/control/v1/license", token, nil)
	lic, _ := body["license"].(map[string]any)
	if code != 200 || lic["enforced"] != licensing.Enforced() {
		t.Fatalf("license = %d %s (enforced build: %v)", code, raw, licensing.Enforced())
	}
	if licensing.Enforced() {
		if lic["state"] != "missing" || lic["ack_required"] != true {
			t.Fatalf("official build without a licence: %s", raw)
		}
		code, body, raw = call("POST", "/control/v1/reload/plan", token, map[string]any{})
		if code != http.StatusLocked || body["code"] != "license_ack_required" {
			t.Fatalf("plan on an unacknowledged node = %d %s, want 423", code, raw)
		}
		code, body, raw = call("POST", "/control/v1/reload", token, map[string]any{"confirm": true, "plan_digest": "sha256:any"})
		if code != http.StatusLocked {
			t.Fatalf("reload on an unacknowledged node = %d %s, want 423", code, raw)
		}
		echoSelf("while reloads are blocked by the licence") // DICOM is not blocked
		// An invalid licence is refused; the node stays unlicensed and running.
		code, body, raw = call("POST", "/control/v1/license/install", token, map[string]any{"license": "-----BEGIN LIBREKEY LICENSE-----\nbm90IGEgbGljZW5jZQ\n-----END LIBREKEY LICENSE-----"})
		if code != http.StatusUnprocessableEntity || body["code"] != "license_invalid" {
			t.Fatalf("invalid licence = %d %s", code, raw)
		}
		code, body, raw = call("POST", "/control/v1/license/activate", token, map[string]any{"key": "LK-SYNTH-WIRE-0001"}) // gitleaks:allow (synthetic test value)
		if code != http.StatusUnprocessableEntity || body["code"] != "activation_failed" {
			t.Fatalf("activation without a server = %d %s", code, raw)
		}
		code, body, raw = call("POST", "/control/v1/license/request", token, map[string]any{"site": "Wiring site"})
		if code != 200 || !strings.Contains(fmt.Sprint(body["request"]), "LIBREKEY") {
			t.Fatalf("activation request = %d %s", code, raw)
		}
		code, _, raw = call("POST", "/control/v1/license/ack", token, map[string]any{})
		if code != 200 {
			t.Fatalf("ack = %d %s", code, raw)
		}
		if _, body, _ := call("GET", "/control/v1/license", token, nil); body["license"].(map[string]any)["ack_required"] != false {
			t.Fatalf("after the acknowledgement: %v", body)
		}
	} else {
		for p, fields := range map[string]map[string]any{
			"ack":      {},
			"request":  {"site": "Wiring site"},
			"activate": {"key": "LK-SYNTH-WIRE-0001"}, // gitleaks:allow (synthetic test value)
			"install":  {"license": "x"},
		} {
			if code, body, raw := call("POST", "/control/v1/license/"+p, token, fields); code != 422 || body["code"] != "license_not_enforced" {
				t.Fatalf("source build: license/%s = %d %s", p, code, raw)
			}
		}
	}
	echoSelf("after the licence actions") // DCM-SRS-058

	// Reload: nothing differs yet.
	plan := func() map[string]any {
		t.Helper()
		code, body, raw := call("POST", "/control/v1/reload/plan", token, map[string]any{})
		if code != 200 {
			t.Fatalf("plan = %d %s", code, raw)
		}
		return body["plan"].(map[string]any)
	}
	if p := plan(); p["changed"] != false {
		t.Fatalf("plan with an unchanged file = %v", p)
	}
	// The administrator edits the file: one more modality, one more destination.
	write(config(`[CT1, WIRING, "US1@192.168.7.0/24"]`, "    VIEWER2: {host: 10.1.2.3, port: 104}\n"))
	p := plan()
	if p["changed"] != true || !strings.Contains(fmt.Sprint(p["allow_list"]), "US1@192.168.7.0/24") || !strings.Contains(fmt.Sprint(p["remote_aes"]), "VIEWER2") {
		t.Fatalf("plan = %v", p)
	}
	if rules, _ := a.gw.AllowedAEs(); len(rules) != 2 {
		t.Fatalf("the plan changed the gateway: %v", rules)
	}
	code, body, raw = call("POST", "/control/v1/reload", token, map[string]any{"confirm": true, "plan_digest": p["plan_digest"], "expect_node_id": "wiring-node", "expect_ae_title": "WIRING"})
	if code != 200 || body["changed"] != true {
		t.Fatalf("reload = %d %s", code, raw)
	}
	rules, _ := a.gw.AllowedAEs()
	if fmt.Sprint(rules) != "[CT1 WIRING US1@192.168.7.0/24]" || a.gw.RemoteAEs()["VIEWER2"].Host != "10.1.2.3" || a.gw.RemoteAEs()["VIEWER2"].AETitle != "VIEWER2" {
		t.Fatalf("gateway after reload: %v %v", rules, a.gw.RemoteAEs())
	}
	echoSelf("after a reload")

	// Bad configurations: reported, and the gateway keeps what it runs.
	var rl *reloader
	for _, c := range a.components {
		if r, ok := c.(*reloader); ok {
			rl = r
		}
	}
	if rl == nil {
		t.Fatal("no reloader among the components")
	}
	for name, bad := range map[string]string{
		"invalid allow-list rule": config(`[CT1, "MR1@not-an-ip"]`, ""),
		"AE title too long":       config(`[CT1, "`+strings.Repeat("A", 17)+`"]`, ""),
		"broken yaml":             "dicom: [this is not valid",
	} {
		write(bad)
		code, body, raw := call("POST", "/control/v1/reload/plan", token, map[string]any{})
		if code != http.StatusUnprocessableEntity || body["code"] != "invalid_config" {
			t.Errorf("%s: plan = %d %s, want 422 invalid_config", name, code, raw)
		}
		code, body, raw = call("POST", "/control/v1/reload", token, map[string]any{"confirm": true, "plan_digest": p["plan_digest"]})
		if code != http.StatusUnprocessableEntity || body["code"] != "invalid_config" {
			t.Errorf("%s: reload = %d %s, want 422 invalid_config", name, code, raw)
		}
		// SIGHUP takes the same path and refuses the same file.
		if err := rl.reload(); err == nil {
			t.Errorf("%s: the SIGHUP path accepted it", name)
		}
		rules, _ := a.gw.AllowedAEs()
		if fmt.Sprint(rules) != "[CT1 WIRING US1@192.168.7.0/24]" || len(a.gw.RemoteAEs()) != 2 {
			t.Fatalf("%s: the gateway's settings changed: %v %v", name, rules, a.gw.RemoteAEs())
		}
		echoSelf("after the refused reload (" + name + ")")
	}

	// A reload that empties the allow-list needs the acknowledged warning.
	write(config(`[]`, ""))
	p = plan()
	code, body, raw = call("POST", "/control/v1/reload", token, map[string]any{"confirm": true, "plan_digest": p["plan_digest"]})
	if code != http.StatusUnprocessableEntity || body["code"] != "warning_not_acknowledged" {
		t.Fatalf("emptying the allow-list without acknowledgement = %d %s", code, raw)
	}
	if _, enforced := a.gw.AllowedAEs(); !enforced {
		t.Fatal("the allow-list was emptied without the acknowledgement")
	}

	// Backup: only into the configured directory.
	code, body, raw = call("POST", "/control/v1/backup", token, map[string]any{})
	if code != 200 {
		t.Fatalf("backup = %d %s", code, raw)
	}
	name := fmt.Sprint(body["file"])
	data, err := os.ReadFile(filepath.Join(backups, name))
	if err != nil {
		t.Fatalf("snapshot not in the configured directory: %v", err)
	}
	sum := sha256.Sum256(data)
	if body["sha256"] != hex.EncodeToString(sum[:]) || !strings.HasPrefix(name, "index-wiring-node-") || !bytes.HasPrefix(data, []byte("SQLite format 3")) {
		t.Fatalf("snapshot %s: reported %v, file %x", name, body["sha256"], sum)
	}
	entries, _ := os.ReadDir(backups)
	if len(entries) != 1 {
		t.Fatalf("backup directory holds %d entries", len(entries))
	}
	if code, body, raw := call("POST", "/control/v1/backup", token, map[string]any{"path": filepath.Join(dir, "elsewhere.db")}); code != 400 || body["code"] != "invalid_body" {
		t.Fatalf("backup with a path = %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "elsewhere.db")); err == nil {
		t.Fatal("a client-chosen backup path was written")
	}
	code, body, raw = call("GET", "/control/v1/backups", token, nil)
	if code != 200 || !strings.Contains(raw, name) || body["configured"] != true {
		t.Fatalf("backups = %d %s", code, raw)
	}
	// The setting is shown; it is a path, not a secret.
	if _, _, raw := call("GET", "/control/v1/config", token, nil); !strings.Contains(raw, `"control.backup_dir"`) || !strings.Contains(raw, backups) {
		t.Fatalf("config does not show control.backup_dir: %s", raw)
	}

	// The audit view holds the action records.
	for event, min := range map[string]int{"NetworkEntry.verification": 2, "SecurityAlert.configuration-changed": 2,
		"SecurityAlert.backup": 2, "SecurityAlert.control-refused": 3} {
		_, body, raw := call("GET", "/control/v1/audit?limit=500&type="+event, token, nil)
		if n, _ := body["count"].(float64); int(n) < min {
			t.Errorf("audit view holds %v %s records, want at least %d: %s", body["count"], event, min, raw)
		}
		if !strings.Contains(raw, `"operator":"Pat Admin"`) {
			t.Errorf("%s records do not name the operator", event)
		}
	}
}
