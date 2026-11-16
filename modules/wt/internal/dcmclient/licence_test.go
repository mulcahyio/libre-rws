package dcmclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// What LibreDCM reports as the end of a licence key is cut to four
// characters, so a full key sent by mistake is never kept; KeyLast4 shows
// nothing of a key of four characters or fewer.
// Verifies: WT-SRS-027, WT-SRS-032
func TestLicenceKeyIsMasked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"instance":{"node_id":"n1","ae_title":"A"},"license":{"enforced":true,"state":"valid","licensed":true,` +
			`"key_last4":"LK-FULL-KEY-SYNTH-Q9Z7","acknowledgement":{"by":"admin1","valid":true}},` + // gitleaks:allow (synthetic test value)
			`"acknowledged":true,"key_last4":"LK-FULL-KEY-SYNTH-Q9Z7","request":"REQ"}`)) // gitleaks:allow (synthetic test value)
	}))
	defer srv.Close()
	c, err := New(Config{URL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v, err := c.License(ctx)
	if err != nil || v.License.KeyLast4 != "Q9Z7" || v.License.Ack == nil || v.License.Ack.By != "admin1" || !v.License.Licensed {
		t.Fatalf("licence = %+v, %v", v, err)
	}
	env := Envelope{Operator: "admin1", Reason: "r", RequestID: NewRequestID()}
	for name, got := range map[string]func() string{
		"ack":      func() string { r, _ := c.LicenseAck(ctx, env); return r.License.KeyLast4 },
		"install":  func() string { r, _ := c.LicenseInstall(ctx, "LIC", env); return r.License.KeyLast4 },
		"activate": func() string { r, _ := c.LicenseActivate(ctx, "k", "", env); return r.License.KeyLast4 },
		"request":  func() string { r, _ := c.LicenseRequest(ctx, "", "", env); return r.KeyLast4 },
	} {
		if k := got(); k != "Q9Z7" {
			t.Fatalf("%s: key_last4 = %q", name, k)
		}
	}
	for key, want := range map[string]string{"": "", "ABCD": "", " LK-1234-WXYZ ": "WXYZ"} {
		if got := KeyLast4(key); got != want {
			t.Fatalf("KeyLast4(%q) = %q, want %q", key, got, want)
		}
	}
}

// Licence and backup actions carry the envelope and only their own fields:
// key and site when given, the licence file text, nothing for the backup
// (no path, no file name). A licence key that LibreDCM's error text
// repeats, in any case, is replaced by its masked form.
// Verifies: WT-SRS-027, WT-SRS-028, WT-SRS-031, WT-SRS-032
func TestLicenceAndBackupRequests(t *testing.T) {
	const key = "LK-SYNTH-5R8T-ABCD" // gitleaks:allow (synthetic test value)
	var mu sync.Mutex
	bodies := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies[r.URL.Path] = b
		mu.Unlock()
		switch r.URL.Path {
		case "/control/v1/license/activate":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":"activation failed: key ` + strings.ToLower(key) + ` (` + key + `) is revoked","code":"activation_failed"}`))
		case "/control/v1/backups":
			_, _ = w.Write([]byte(`{"instance":{"node_id":"n1"},"configured":true,"directory":"/b","backups":[{"file":"index-n1-20261101T020000Z.db","bytes":9,"time":"2026-11-01T02:00:00Z"}],"count":1}`))
		case "/control/v1/backup":
			_, _ = w.Write([]byte(`{"instance":{"node_id":"n1"},"file":"index-n1-20261116T100000Z.db","bytes":10,"sha256":"abc","duration_ms":5,"note":"index only"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c, err := New(Config{URL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if c.backup != DefaultBackupTimeout || DefaultBackupTimeout >= 2*time.Minute {
		t.Fatalf("backup wait = %v; it must stay below the 2-minute client timeout", c.backup)
	}
	ctx := context.Background()
	env := Envelope{Operator: "admin1", Reason: "why", RequestID: NewRequestID(), Expect: Identity{NodeID: "n1", AETitle: "A"}}
	body := func(p string) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return bodies[p]
	}
	_, err = c.LicenseActivate(ctx, " "+key+" ", "Site 1", env)
	if err == nil || Code(err) != "activation_failed" || strings.Contains(err.Error(), key) || strings.Contains(err.Error(), strings.ToLower(key)) ||
		strings.Count(err.Error(), "••••ABCD") != 2 {
		t.Fatalf("activation error = %v", err)
	}
	if b := body("/control/v1/license/activate"); len(b) != 7 || b["key"] != key || b["site"] != "Site 1" || b["operator"] != "admin1" || b["expect_node_id"] != "n1" {
		t.Fatalf("activate body = %v", b)
	}
	if _, err := c.LicenseRequest(ctx, "", "", env); err != nil {
		t.Fatal(err)
	}
	if b := body("/control/v1/license/request"); b["key"] != nil || b["site"] != nil || b["reason"] != "why" {
		t.Fatalf("request body without key = %v", b)
	}
	if _, err := c.LicenseInstall(ctx, "LICENCE TEXT", env); err != nil {
		t.Fatal(err)
	}
	if b := body("/control/v1/license/install"); b["license"] != "LICENCE TEXT" || b["request_id"] != env.RequestID {
		t.Fatalf("install body = %v", b)
	}
	if _, err := c.LicenseAck(ctx, env); err != nil {
		t.Fatal(err)
	}
	if b := body("/control/v1/license/ack"); len(b) != 5 || b["operator"] != "admin1" {
		t.Fatalf("ack body = %v", b)
	}
	res, err := c.Backup(ctx, env)
	if err != nil || res.File != "index-n1-20261116T100000Z.db" || res.SHA256 != "abc" || res.Bytes != 10 {
		t.Fatalf("backup = %+v, %v", res, err)
	}
	if b := body("/control/v1/backup"); len(b) != 5 {
		t.Fatalf("backup body = %v; it must hold the envelope only", b)
	}
	list, err := c.Backups(ctx)
	if err != nil || !list.Configured || len(list.Backups) != 1 || list.Backups[0].Bytes != 9 {
		t.Fatalf("backups = %+v, %v", list, err)
	}
}
