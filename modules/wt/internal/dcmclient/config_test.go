package dcmclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Libre WT drops the value of every setting LibreDCM marks as redacted, also
// when the answer holds one by mistake, so it can never reach a page.
// Verifies: WT-SRS-019, WT-SRS-032
func TestConfigDropsRedactedValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"instance":{"node_id":"n1","ae_title":"A"},"settings":[` +
			`{"key":"admin.token","redacted":true,"value":"LEAKED-1","set":true,"source":"file"},` + // gitleaks:allow (synthetic test value)
			`{"key":"dicom.ae_title","value":"A","source":"file"}]}`))
	}))
	defer srv.Close()
	c, err := New(Config{URL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Config(context.Background())
	if err != nil || len(v.Settings) != 2 {
		t.Fatalf("config = %+v, %v", v, err)
	}
	if s := v.Settings[0]; s.Value != nil || !s.Redacted || s.Set == nil || !*s.Set {
		t.Fatalf("redacted setting = %+v", s)
	}
	if v.Settings[1].Value != "A" {
		t.Fatalf("plain setting = %+v", v.Settings[1])
	}
}

// Actions carry the envelope (operator, reason, request ID, expected node),
// the approval (confirm, plan digest, acknowledged warnings) and only the
// fields of their target. Error answers keep LibreDCM's code and status; no
// answer is told apart from a refusal.
// Verifies: WT-SRS-031
func TestActionRequests(t *testing.T) {
	var mu sync.Mutex
	bodies := map[string]map[string]any{}
	body := func(p string) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return bodies[p]
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies[r.URL.Path] = b
		mu.Unlock()
		switch r.URL.Path {
		case "/control/v1/reload":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"plan changed","code":"plan_changed"}`))
		case "/control/v1/echo":
			time.Sleep(200 * time.Millisecond)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			_, _ = w.Write([]byte(`{"applied":true,"changed":true,"plan":{"plan_digest":"sha256:p"}}`))
		}
	}))
	defer srv.Close()
	c, err := New(Config{URL: srv.URL, Token: "tok", ActionTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	env := Envelope{Operator: "admin1", Reason: "why", RequestID: NewRequestID(), Expect: Identity{NodeID: "n1", AETitle: "A"}}
	if len(env.RequestID) != 32 || env.RequestID == NewRequestID() {
		t.Fatalf("request ID %q", env.RequestID)
	}
	rules := []string{}
	if _, err := c.ConfigApply(ctx, ConfigEdit{AllowedAEs: &rules}, "sha256:p", []string{"allow_list_emptied"}, env); err != nil {
		t.Fatal(err)
	}
	b := body("/control/v1/config")
	if b["operator"] != "admin1" || b["reason"] != "why" || b["request_id"] != env.RequestID || b["expect_node_id"] != "n1" || b["expect_ae_title"] != "A" ||
		b["confirm"] != true || b["plan_digest"] != "sha256:p" || len(b["acknowledge_warnings"].([]any)) != 1 {
		t.Fatalf("config body = %v", b)
	}
	settings := b["settings"].(map[string]any)
	if list, ok := settings[KeyAllowedAEs].([]any); !ok || len(list) != 0 || settings[KeyRemoteAEs] != nil {
		t.Fatalf("settings = %v", settings)
	}
	_, err = c.Reload(ctx, "sha256:p", nil, env)
	if Code(err) != "plan_changed" || HTTPStatus(err) != http.StatusConflict || NoAnswer(err) {
		t.Fatalf("reload error = %v", err)
	}
	if _, ok := body("/control/v1/reload")["acknowledge_warnings"]; ok {
		t.Fatal("acknowledge_warnings sent without warnings")
	}
	// No answer in time: not a refusal.
	_, err = c.Echo(ctx, EchoTarget{RemoteAE: "VIEWER1"}, env)
	if !NoAnswer(err) || HTTPStatus(err) != 0 || Code(err) != "" || !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("echo without an answer = %v", err)
	}
	srv.Close() // waits for the echo handler
	if e := body("/control/v1/echo"); e["remote_ae"] != "VIEWER1" || e["host"] != nil || e["port"] != nil || e["tls"] != nil {
		t.Fatalf("echo body = %v", e)
	}
}
