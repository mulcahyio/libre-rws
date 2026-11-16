package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fleet"
)

// controlAudit reads the audit records of the real node.
func controlAudit(t *testing.T, adminURL, token string) []map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, adminURL+"/control/v1/audit?limit=500", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Records
}

// Libre WT against the real LibreDCM of this repository (built and run as a
// separate process): the configuration page with the admin token hidden,
// a destination added through the edit flow, a C-ECHO test to it (nothing
// listens: the connect stage fails), an allow-list rule added, the
// rollback of that change and a reload plan. LibreDCM's own audit records
// name the Libre WT user and the reason, with the request ID Libre WT
// recorded.
// Verifies: WT-SRS-019, WT-SRS-020, WT-SRS-021, WT-SRS-022, WT-SRS-024, WT-SRS-031
func TestConfigAgainstRealLibreDCM(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs LibreDCM")
	}
	const token = "real-token-SECRET-2" // gitleaks:allow (synthetic test value)
	adminURL := startLibreDCM(t, "real-node-2", "REALDCM2", token)
	acct, err := accounts.Open(context.Background(), filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = acct.Close() })
	members, err := fleet.Connect([]fleet.InstanceConfig{{Name: "real", URL: adminURL, Token: token}})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg, err := fleet.New(members, acct, time.Nanosecond, logger)
	if err != nil {
		t.Fatal(err)
	}
	w, err := New(Config{Version: "wt-test"}, acct, reg, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(w.Handler())
	t.Cleanup(srv.Close)
	e := &env{srv: srv, acct: acct, web: w}
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()

	code, body, _ := b.get("/i/real/config")
	if code != 200 {
		t.Fatalf("config = %d\n%s", code, body)
	}
	has(t, "real config", body, "<code>dicom.ae_title</code>", "<code>REALDCM2</code>", `<span class="state live">live</span>`,
		`<span class="state restart">restart</span>`, "secret, not shown")
	hasNot(t, "real config", body, "SECRET-2")
	_, body, _ = b.get("/i/real/allow-list")
	has(t, "real allow-list", body, "every calling AE may connect")

	// Add a destination on a port where nothing listens.
	dead := freePort(t)
	host, port, _ := strings.Cut(dead, ":")
	form := url.Values{"csrf": {csrf}, "setting": {"remote_aes"}, "name": {"VIEWERX"}, "host": {host}, "port": {port}, "ae_title": {"VIEWERX"}}
	confirm, page := b.review("/i/real/config/review", form)
	has(t, "real plan", page, `<span class="state success">added</span></td><td><b>VIEWERX</b>`, "Review: Change the C-MOVE destinations")
	code, body, _ = b.post("/i/real/config/apply", approval(csrf, confirm, "add test viewer"))
	if code != 200 || !strings.Contains(body, "LibreDCM applied the change on instance <b>real</b>") {
		t.Fatalf("real apply = %d\n%s", code, body)
	}
	_, body, _ = b.get("/i/real/remote-aes")
	has(t, "real destinations", body, "<b>VIEWERX</b>", "<td>overlay</td>", "<code>"+host+"</code>")

	// C-ECHO test to it as an Operator: the connection is refused.
	op := e.as(t, "op1")
	ocsrf := op.csrf()
	confirm, _ = op.review("/i/real/echo/review", url.Values{"csrf": {ocsrf}, "remote_ae": {"VIEWERX"}})
	code, body, _ = op.post("/i/real/echo/apply", approval(ocsrf, confirm, "check new viewer"))
	if code != 200 {
		t.Fatalf("real echo = %d\n%s", code, body)
	}
	has(t, "real echo", body, `<span class="state open">failed</span>`, "the stage <b>connect</b>", "<code>REALDCM2</code>")

	// Add an allow-list rule (the list starts to be enforced: LibreDCM
	// warns, the box must be ticked), then roll it back.
	confirm, page = b.review("/i/real/config/review", url.Values{"csrf": {csrf}, "setting": {"allow_list"}, "rules": {"CT1\nMR1@127.0.0.1"}})
	has(t, "real allow-list plan", page, "<code class=\"diff\">CT1</code>", "<code class=\"diff\">MR1@127.0.0.1</code>")
	form = approval(csrf, confirm, "restrict callers")
	for i := 0; strings.Contains(page, `name="ack_`+string(rune('0'+i))+`"`); i++ {
		form.Set("ack_"+string(rune('0'+i)), "yes")
	}
	code, body, _ = b.post("/i/real/config/apply", form)
	if code != 200 {
		t.Fatalf("real allow-list apply = %d\n%s", code, body)
	}
	_, body, _ = b.get("/i/real/allow-list")
	has(t, "real allow-list after", body, "<code>CT1</code>", "<code>MR1@127.0.0.1</code>", "Only the calling AEs below may connect")

	_, body, _ = b.get("/i/real/config")
	has(t, "real history", body, "admin1", "restrict callers", "/rollback/review")
	confirm, page = b.review("/i/real/rollback/review", url.Values{"csrf": {csrf}})
	has(t, "real rollback plan", page, "add test viewer", `<span class="state open">removed</span></td><td><code>CT1</code>`)
	form = approval(csrf, confirm, "undo restriction")
	for i := 0; strings.Contains(page, `name="ack_`+string(rune('0'+i))+`"`); i++ {
		form.Set("ack_"+string(rune('0'+i)), "yes")
	}
	code, body, _ = b.post("/i/real/rollback/apply", form)
	if code != 200 || !strings.Contains(body, "Done: Rollback") {
		t.Fatalf("real rollback = %d\n%s", code, body)
	}
	_, body, _ = b.get("/i/real/allow-list")
	has(t, "real allow-list after rollback", body, "every calling AE may connect")

	// A reload: nothing changed in the files.
	confirm, page = b.review("/i/real/reload/review", url.Values{"csrf": {csrf}})
	code, body, _ = b.post("/i/real/reload/apply", approval(csrf, confirm, "routine reload"))
	if code != 200 || !strings.Contains(body, "Done: Reload") {
		t.Fatalf("real reload = %d\n%s", code, body)
	}

	// LibreDCM's own records name the Libre WT user, the reason and the
	// request ID Libre WT wrote in its audit log.
	recs := controlAudit(t, adminURL, token)
	found := map[string]bool{}
	for _, a := range auditOf(t, e, "config.edit.approved") {
		id := a.Detail[strings.Index(a.Detail, "request_id=")+len("request_id="):]
		id = id[:32]
		for _, r := range recs {
			if r["request_id"] == id && r["operator"] == "admin1" && r["phase"] == "result" {
				found[id] = true
			}
		}
	}
	if len(found) != 2 {
		t.Fatalf("LibreDCM records with the request IDs of Libre WT: %d of 2\n%v", len(found), recs)
	}
	echoed := false
	for _, r := range recs {
		if r["operator"] == "op1" && r["reason"] == "check new viewer" && r["phase"] == "result" {
			echoed = true
		}
	}
	if !echoed {
		t.Fatalf("no LibreDCM record of the C-ECHO by op1: %v", recs)
	}
}
