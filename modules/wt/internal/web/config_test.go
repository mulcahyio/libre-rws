package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fakedcm"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fleet"
)

// The paths of the fake LibreDCM that change something, or would.
var actionPaths = []string{"/control/v1/config", "/control/v1/reload", "/control/v1/config/rollback", "/control/v1/echo"}
var planPaths = []string{"/control/v1/config/plan", "/control/v1/reload/plan", "/control/v1/config/rollback/plan"}

// sent counts the action requests a fake node received.
func sent(n *fakedcm.Server) int {
	total := 0
	for _, p := range actionPaths {
		total += len(n.Posts(p))
	}
	return total
}

// planned counts the plan requests a fake node received.
func planned(n *fakedcm.Server) int {
	total := 0
	for _, p := range planPaths {
		total += len(n.Posts(p))
	}
	return total
}

// auditOf returns the audit records of an action, oldest first.
func auditOf(t *testing.T, e *env, action string) []accounts.Entry {
	t.Helper()
	log, err := e.acct.AuditLog(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []accounts.Entry
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Action == action {
			out = append(out, log[i])
		}
	}
	return out
}

// destForm is the destinations form as the edit page posts it: the
// destination in force (VIEWER1) and one new row.
func destForm(csrf string) url.Values {
	return url.Values{"csrf": {csrf}, "setting": {"remote_aes"},
		"name": {"VIEWER1", "VIEWER2", ""}, "existing": {"VIEWER1"},
		"host": {"10.0.0.20", "10.1.2.3", ""}, "port": {"104", "11112", ""}, "ae_title": {"VIEWER1", "", ""}}
}

// approval is the form of an apply step.
func approval(csrf, confirm, reason string, acks ...string) url.Values {
	v := url.Values{"csrf": {csrf}, "confirm": {confirm}, "approve": {"yes"}, "reason": {reason}}
	for _, a := range acks {
		v.Set(a, "yes")
	}
	return v
}

// review posts a review form and returns the confirmation of the warning
// page.
func (b *browser) review(path string, form url.Values) (string, string) {
	b.t.Helper()
	code, body, _ := b.post(path, form)
	if code != 200 {
		b.t.Fatalf("review %s = %d\n%s", path, code, body)
	}
	return find(b.t, confirmRE, body), body
}

var requestIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// envelopeOK checks what every action must carry to LibreDCM: the logged-in
// user as operator, the reason, a request ID and the expected node.
func envelopeOK(t *testing.T, what string, body map[string]any, operator, reason, node, ae string) {
	t.Helper()
	id, _ := body["request_id"].(string)
	if body["operator"] != operator || body["reason"] != reason || !requestIDRE.MatchString(id) ||
		body["expect_node_id"] != node || body["expect_ae_title"] != ae {
		t.Fatalf("%s: envelope = %v", what, body)
	}
}

// The configuration page shows the running settings with their source and
// whether they change without a restart. A setting LibreDCM marks as
// redacted shows only "set" or "not set", also when the answer holds a
// value by mistake. Restart-only settings are marked read-only. Every
// opening of the page is in the Libre WT audit log; when the log can't be
// written the page is not shown.
// Verifies: WT-SRS-019, WT-SRS-032, WT-SRS-033
func TestConfigPage(t *testing.T) {
	e := newFleetEnv(t, "dcm-1@site-a")
	e.users(t)
	no := false
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Settings = append(f.Cfg.Settings,
			dcmclient.Setting{Key: "admin.token", Redacted: true, Value: "LEAKED-VALUE-1", Source: "file"}, // gitleaks:allow (synthetic test value)
			dcmclient.Setting{Key: "tls.key_file", Redacted: true, Set: &no, Source: "default"},
			dcmclient.Setting{Key: "dicom.find_limit", Value: float64(500), Source: "default"},
			dcmclient.Setting{Key: "html.test", Value: "<script>alert(1)</script>", Source: "file"})
	})
	for _, user := range []string{"op1", "admin1"} {
		b := e.as(t, user)
		code, body, _ := b.get("/i/dcm-1/config")
		if code != 200 {
			t.Fatalf("%s config = %d", user, code)
		}
		has(t, user+" config", body, `<b class="iname">dcm-1</b>`, "<code>node-dcm-1</code>",
			"<code>dicom.ae_title</code>", "<code>FAKEDCM</code>", "<code>:11112</code>", "<code>500</code>",
			"<code>CT1@10.0.0.5</code><br><code>MR1</code>",
			`<span class="state live">live</span>`, `<span class="state restart">restart</span> <small>read-only here</small>`,
			`<span class="state">set</span> <small>secret, not shown</small>`, `<span class="state">not set</span>`, `<span class="state">hidden</span>`,
			"<code>LIBRE_DCM_S3_SECRET_KEY</code>", "postgres://db.example:5432/libredcm", "removed from this value: user, password",
			"sha256:aaaa", "installer script or Docker", "&lt;script&gt;alert(1)&lt;/script&gt;",
			"admin0", "new CT", "<td>previous</td>")
		hasNot(t, user+" config", body, "LEAKED-VALUE-1", "tok-SECRET", "<script>alert")
		// Buttons only for the role that can use them.
		if admin := user == "admin1"; strings.Contains(body, "/config/edit") != admin || strings.Contains(body, "/reload/review") != admin ||
			strings.Contains(body, "/rollback/review") != admin {
			t.Fatalf("%s: change controls on the configuration page:\n%s", user, body)
		}
	}
	views := auditOf(t, e, "config.view")
	if len(views) != 2 || views[0].User != "op1" || views[1].User != "admin1" || views[0].Instance != "dcm-1" || views[0].Outcome != "success" {
		t.Fatalf("audit of the views = %+v", views)
	}
	// No previous version: no rollback button.
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.History.Previous = nil })
	b := e.as(t, "admin1")
	_, body, _ := b.get("/i/dcm-1/config")
	has(t, "no previous version", body, "nothing to roll back to")
	hasNot(t, "no previous version", body, "/rollback/review")
	// No audit record, no page.
	e.breakAudit(t)
	for _, p := range []string{"/config", "/remote-aes", "/allow-list", "/config/edit", "/echo"} {
		code, body, _ := b.get("/i/dcm-1" + p)
		if code != http.StatusInternalServerError || !strings.Contains(body, "audit log can&#39;t be written, so the page is not shown") {
			t.Fatalf("%s without an audit record = %d", p, code)
		}
		hasNot(t, p+" without an audit record", body, "FAKEDCM", "VIEWER1", "CT1@10.0.0.5")
	}
	if strings.Contains(e.log.String(), "tok-SECRET") || strings.Contains(e.log.String(), "LEAKED-VALUE-1") {
		t.Fatal("secret in the log")
	}
}

// The destinations and allow-list pages show the entries in force with
// their source. An empty allow-list is stated clearly: every calling AE may
// connect. Both views are audited.
// Verifies: WT-SRS-020, WT-SRS-033
func TestDestinationsAndAllowList(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "op1")
	code, body, _ := b.get("/i/default/remote-aes")
	if code != 200 {
		t.Fatalf("destinations = %d", code)
	}
	has(t, "destinations", body, "<b>VIEWER1</b>", "<code>10.0.0.20</code>", "<td>104</td>", "<td>file</td>", `href="/i/default/echo"`)
	hasNot(t, "destinations as operator", body, "/config/edit")
	code, body, _ = b.get("/i/default/allow-list")
	if code != 200 {
		t.Fatalf("allow-list = %d", code)
	}
	has(t, "allow-list", body, "<code>CT1@10.0.0.5</code>", "<code>MR1</code>", "<td>overlay</td>", "Only the calling AEs below may connect")
	hasNot(t, "enforced allow-list", body, "every calling AE may connect", "/config/edit")

	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.AllowList = dcmclient.AllowList{Enforced: false}
		f.Cfg.RemoteAEs = nil
	})
	_, body, _ = b.get("/i/default/allow-list")
	has(t, "empty allow-list", body, "The allow-list is empty: every calling AE may connect", `role="alert"`)
	hasNot(t, "empty allow-list", body, "Only the calling AEs below may connect")
	_, body, _ = b.get("/i/default/remote-aes")
	has(t, "no destinations", body, "No destination is configured")

	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.AllowList = dcmclient.AllowList{Enforced: true, Invalid: true} })
	_, body, _ = b.get("/i/default/allow-list")
	has(t, "invalid allow-list", body, "The configured allow-list is invalid", "refuses every association")
	hasNot(t, "invalid allow-list", body, "every calling AE may connect")

	// The Admin gets the edit links.
	_, body, _ = e.as(t, "admin1").get("/i/default/allow-list")
	has(t, "allow-list as admin", body, `href="/i/default/config/edit"`)
	if n, m := len(auditOf(t, e, "remote-aes.view")), len(auditOf(t, e, "allow-list.view")); n != 2 || m != 4 {
		t.Fatalf("audited views: destinations %d, allow-list %d", n, m)
	}
}

// An Admin changes the destinations: the form goes to LibreDCM as a plan,
// the warning page shows LibreDCM's exact difference and the instance, and
// the change is sent only with the single-use confirmation of that page
// (same session, instance, action), the approval box and a reason. What is
// sent is what was reviewed, with LibreDCM's plan digest, the logged-in user
// as operator, the reason, a request ID and the expected node. Review,
// approval and result are in the Libre WT audit log.
// Verifies: WT-SRS-021, WT-SRS-031, WT-SRS-033
func TestConfigEditFlow(t *testing.T) {
	e := newFleetEnv(t, "a", "b")
	e.users(t)
	b := e.as(t, "admin1")
	code, body, _ := b.get("/i/a/config/edit")
	if code != 200 {
		t.Fatalf("edit page = %d", code)
	}
	has(t, "edit page", body, `name="name" value="VIEWER1"`, `name="existing" value="VIEWER1"`, `name="host" value="10.0.0.20"`, `name="port" value="104"`,
		`name="remove" value="VIEWER1"`, "CT1@10.0.0.5\nMR1</textarea>", "Nothing changes when you press a button below")
	csrf := find(t, csrfRE, body)
	if planned(e.nodes["a"]) != 0 {
		t.Fatal("opening the form asked for a plan")
	}

	confirm, page := b.review("/i/a/config/review", destForm(csrf))
	has(t, "warning page", page, "Review: Change the C-MOVE destinations", "The change is made on instance <b>a</b>", "<code>node-a</code>", "<code>AE_A</code>",
		`<span class="state success">added</span></td><td><b>VIEWER2</b>`, `<code class="diff">10.1.2.3:11112</code>`,
		"I have checked the change above and approve it for instance a", "valid for 10 minutes, once, and for this plan only")
	plans := e.nodes["a"].Posts("/control/v1/config/plan")
	wantTable := []any{
		map[string]any{"name": "VIEWER1", "host": "10.0.0.20", "port": float64(104), "ae_title": "VIEWER1"},
		map[string]any{"name": "VIEWER2", "host": "10.1.2.3", "port": float64(11112), "ae_title": ""},
	}
	wantSettings := map[string]any{"dicom.remote_aes": wantTable}
	if len(plans) != 1 || !reflect.DeepEqual(plans[0]["settings"], wantSettings) || plans[0]["expect_node_id"] != "node-a" || plans[0]["expect_ae_title"] != "AE_A" {
		t.Fatalf("plan request = %v", plans)
	}
	if sent(e.nodes["a"]) != 0 {
		t.Fatal("the review changed something")
	}

	refused := func(what string, br *browser, path string, form url.Values, wantCode int, wantText string) {
		t.Helper()
		code, body, _ := br.post(path, form)
		if code != wantCode || !strings.Contains(body, wantText) {
			t.Fatalf("%s = %d, want %d with %q\n%s", what, code, wantCode, wantText, body)
		}
		if n := sent(e.nodes["a"]) + sent(e.nodes["b"]); n != 0 {
			t.Fatalf("%s: %d action(s) reached LibreDCM", what, n)
		}
	}
	// No confirmation.
	refused("no confirmation", b, "/i/a/config/apply", approval(csrf, "", "new viewer"), http.StatusConflict, "approval expired or was already used")
	// The confirmation of instance a on instance b: refused, and used up.
	refused("other instance", b, "/i/b/config/apply", approval(csrf, confirm, "new viewer"), http.StatusConflict, "approval expired or was already used")
	refused("used confirmation", b, "/i/a/config/apply", approval(csrf, confirm, "new viewer"), http.StatusConflict, "approval expired or was already used")
	// Another action with this confirmation.
	confirm, _ = b.review("/i/a/config/review", destForm(csrf))
	refused("other action", b, "/i/a/reload/apply", approval(csrf, confirm, "new viewer"), http.StatusConflict, "approval expired or was already used")
	// Another session, also of the same user.
	confirm, _ = b.review("/i/a/config/review", destForm(csrf))
	other := e.as(t, "admin1")
	refused("other session", other, "/i/a/config/apply", approval(other.csrf(), confirm, "new viewer"), http.StatusConflict, "approval expired or was already used")
	// Approval box, reason.
	confirm, _ = b.review("/i/a/config/review", destForm(csrf))
	noBox := approval(csrf, confirm, "new viewer")
	noBox.Del("approve")
	refused("no approval box", b, "/i/a/config/apply", noBox, http.StatusUnprocessableEntity, "Tick the approval box")
	confirm, _ = b.review("/i/a/config/review", destForm(csrf))
	refused("no reason", b, "/i/a/config/apply", approval(csrf, confirm, "   "), http.StatusUnprocessableEntity, "A reason is required")
	confirm, _ = b.review("/i/a/config/review", destForm(csrf))
	refused("long reason", b, "/i/a/config/apply", approval(csrf, confirm, strings.Repeat("x", 501)), http.StatusUnprocessableEntity, "too long")
	// An expired confirmation.
	confirm, _ = b.review("/i/a/config/review", destForm(csrf))
	e.web.ss.mu.Lock()
	for k, c := range e.web.ss.confirms {
		c.expires = c.expires.Add(-confirmTTL - 1)
		e.web.ss.confirms[k] = c
	}
	e.web.ss.mu.Unlock()
	refused("expired", b, "/i/a/config/apply", approval(csrf, confirm, "new viewer"), http.StatusConflict, "approval expired or was already used")

	// The proper approval. The browser adds form fields of another table:
	// they are ignored, what was reviewed is sent.
	confirm, _ = b.review("/i/a/config/review", destForm(csrf))
	form := approval(csrf, confirm, "new viewer in room 2")
	form["name"], form["host"], form["port"], form["ae_title"], form["setting"] = []string{"EVIL"}, []string{"203.0.113.9"}, []string{"104"}, []string{"EVIL"}, []string{"remote_aes"}
	form.Set("plan_digest", "sha256:other")
	code, body, _ = b.post("/i/a/config/apply", form)
	if code != 200 {
		t.Fatalf("apply = %d\n%s", code, body)
	}
	applies := e.nodes["a"].Posts("/control/v1/config")
	if len(applies) != 1 || sent(e.nodes["a"]) != 1 || sent(e.nodes["b"]) != 0 {
		t.Fatalf("applies = %v", applies)
	}
	got := applies[0]
	envelopeOK(t, "config apply", got, "admin1", "new viewer in room 2", "node-a", "AE_A")
	if !reflect.DeepEqual(got["settings"], wantSettings) || got["plan_digest"] != "sha256:plan-1" || got["confirm"] != true || got["acknowledge_warnings"] != nil {
		t.Fatalf("apply request = %v", got)
	}
	id := got["request_id"].(string)
	has(t, "result page", body, "Done: Configuration change", "LibreDCM applied the change on instance <b>a</b>", "sha256:aaaa", "sha256:after", id,
		"Destination <b>VIEWER2</b> added")
	// The confirmation is used up.
	code, _, _ = b.post("/i/a/config/apply", approval(csrf, confirm, "again"))
	if code != http.StatusConflict || sent(e.nodes["a"]) != 1 {
		t.Fatalf("second apply with the same confirmation = %d", code)
	}
	// The destinations page shows what is in force now.
	_, body, _ = b.get("/i/a/remote-aes")
	has(t, "destinations after the change", body, "<b>VIEWER2</b>", "<td>overlay</td>")

	// Audit: review, approval before the action, result; all with the instance.
	reviews, approvals, results := auditOf(t, e, "config.edit.review"), auditOf(t, e, "config.edit.approved"), auditOf(t, e, "config.edit")
	if len(reviews) < 8 || len(approvals) != 1 {
		t.Fatalf("audit: %d reviews, %d approvals", len(reviews), len(approvals))
	}
	ap := approvals[0]
	if ap.User != "admin1" || ap.Instance != "a" || ap.Target != "dicom.remote_aes" || !strings.Contains(ap.Detail, "+destination VIEWER2=10.1.2.3:11112") ||
		!strings.Contains(ap.Detail, "request_id="+id) || !strings.Contains(ap.Detail, "reason: new viewer in room 2") {
		t.Fatalf("approval record = %+v", ap)
	}
	last := results[len(results)-2] // the last one is the reused confirmation
	if last.Outcome != "success" || last.Instance != "a" || !strings.Contains(last.Detail, "applied=true changed=true") || !strings.Contains(last.Detail, id) {
		t.Fatalf("result record = %+v", last)
	}
	failures := 0
	for _, r := range results {
		if r.Outcome == "failure" {
			failures++
		}
	}
	if failures != 9 {
		t.Fatalf("%d refused applies in the audit log, want 9", failures)
	}
}

// Each warning LibreDCM marks as needing acknowledgement has its own box.
// Without every box ticked nothing is sent; with them, the codes go to
// LibreDCM. A change LibreDCM refuses as invalid is shown with LibreDCM's
// reason and is not applied. Text from LibreDCM is never markup.
// Verifies: WT-SRS-021, WT-SRS-032
func TestConfigEditWarningsAndRefusals(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Plan.AllowList.Removed = []string{"MR1", "CT1@10.0.0.5"}
		f.Cfg.Plan.AllowList.EnforcedAfter = false
		f.Cfg.Plan.RemoteAEs.Added = nil
		f.Cfg.Plan.RemoteAEs.Removed = []dcmclient.RemoteAE{{Name: "OLD1", Host: "10.9.9.1", Port: 104, AETitle: "OLD1"}, {Name: "OLD2", Host: "10.9.9.2", Port: 104, AETitle: "OLD2"}}
		f.Cfg.Plan.Warnings = []dcmclient.PlanWarning{
			{Code: "remote_ae_removed", Message: "The destination OLD1 is removed.", MustAcknowledge: true},
			{Code: "remote_ae_removed", Message: "The destination OLD2 is removed.", MustAcknowledge: true},
			{Code: "allow_list_emptied", Message: "The allow-list becomes empty: every calling AE may then connect <b>now</b>.", MustAcknowledge: true},
			{Code: "note_only", Message: "A note without a box."},
		}
	})
	rules := url.Values{"csrf": {csrf}, "setting": {"allow_list"}, "rules": {"  \n\n"}}
	confirm, page := b.review("/i/default/config/review", rules)
	has(t, "warning page", page, "Review: Change the calling-AE allow-list", `name="ack_0"`, `name="ack_1"`, `name="ack_2"`,
		"The destination OLD1 is removed.", "The destination OLD2 is removed.", "every calling AE may then connect &lt;b&gt;now&lt;/b&gt;.",
		"A note without a box.", `<b class="diff">the list is empty, every calling AE may connect</b>`,
		`<span class="state open">removed</span></td><td><code>MR1</code>`, `<span class="state open">removed</span></td><td><b>OLD1</b>`)
	hasNot(t, "warning page", page, `name="ack_3"`, "<b>now</b>")
	// An emptied list is sent as an empty list, never as "no value".
	plans := e.dcm.Posts("/control/v1/config/plan")
	if list, ok := plans[0]["settings"].(map[string]any)["dicom.allowed_aes"].([]any); !ok || len(list) != 0 {
		t.Fatalf("plan request for an emptied list = %v", plans[0])
	}
	for _, ticked := range [][]string{nil, {"ack_0", "ack_1"}, {"ack_0", "ack_2"}, {"ack_1", "ack_2"}} {
		code, body, _ := b.post("/i/default/config/apply", approval(csrf, confirm, "close the site", ticked...))
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "Tick the box of every warning") || sent(e.dcm) != 0 {
			t.Fatalf("boxes %v: %d, %d sent", ticked, code, sent(e.dcm))
		}
		confirm, _ = b.review("/i/default/config/review", rules)
	}
	code, body, _ := b.post("/i/default/config/apply", approval(csrf, confirm, "close the site", "ack_0", "ack_1", "ack_2"))
	if code != 200 {
		t.Fatalf("apply with every box = %d\n%s", code, body)
	}
	got := e.dcm.Posts("/control/v1/config")
	if len(got) != 1 || !reflect.DeepEqual(got[0]["acknowledge_warnings"], []any{"remote_ae_removed", "allow_list_emptied"}) {
		t.Fatalf("apply = %v", got)
	}
	if ap := auditOf(t, e, "config.edit.approved"); len(ap) != 1 || !strings.Contains(ap[0].Detail, "acknowledged: remote_ae_removed, allow_list_emptied") ||
		!strings.Contains(ap[0].Detail, "-rule MR1") || !strings.Contains(ap[0].Detail, "allow-list enforced true -> false") {
		t.Fatalf("approval record = %+v", ap)
	}

	// LibreDCM refuses the entry: its reason is shown with what was typed.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Fail["/control/v1/config/plan"] = fakedcm.Failure{Status: 422, Code: "invalid_config", Msg: `remote AE "BAD<1>": host is required`}
	})
	bad := destForm(csrf)
	bad["name"][1], bad["host"][1] = "BAD<1>", ""
	code, body, _ = b.post("/i/default/config/review", bad)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("refused entry = %d", code)
	}
	has(t, "refused entry", body, "remote AE &#34;BAD&lt;1&gt;&#34;: host is required", "Nothing was changed", `name="name" value="BAD&lt;1&gt;"`, `name="port" value="11112"`)
	hasNot(t, "refused entry", body, `name="confirm"`, "BAD<1>")
	// A port that is no number never reaches LibreDCM.
	before := planned(e.dcm)
	bad = destForm(csrf)
	bad["port"][1] = "eleven"
	code, body, _ = b.post("/i/default/config/review", bad)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "the port must be a number from 1 to 65535") || planned(e.dcm) != before {
		t.Fatalf("bad port = %d", code)
	}
	// An unknown setting name.
	code, _, _ = b.post("/i/default/config/review", url.Values{"csrf": {csrf}, "setting": {"dicom.ae_title"}, "value": {"X"}})
	if code != http.StatusBadRequest || planned(e.dcm) != before {
		t.Fatalf("unknown setting = %d", code)
	}
	// A plan without a digest can't be approved.
	e.dcm.Set(func(f *fakedcm.Server) {
		delete(f.Cfg.Fail, "/control/v1/config/plan")
		f.Cfg.Plan.PlanDigest = ""
	})
	code, body, _ = b.post("/i/default/config/review", destForm(csrf))
	if code != http.StatusBadGateway || !strings.Contains(body, "plan without a digest") || strings.Contains(body, `name="confirm"`) {
		t.Fatalf("plan without digest = %d", code)
	}
	if sent(e.dcm) != 1 {
		t.Fatalf("%d actions sent", sent(e.dcm))
	}
}

// The plan digest shown is sent with the action: when LibreDCM's state
// changed after the warning page, LibreDCM refuses and Libre WT says that
// nothing was changed. A row ticked "remove" leaves the table.
// Verifies: WT-SRS-021, WT-SRS-031
func TestPlanChangedAfterReview(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	form := destForm(csrf)
	form["remove"] = []string{"VIEWER1"}
	confirm, _ := b.review("/i/default/config/review", form)
	plans := e.dcm.Posts("/control/v1/config/plan")
	table := plans[0]["settings"].(map[string]any)["dicom.remote_aes"].([]any)
	if len(table) != 1 || table[0].(map[string]any)["name"] != "VIEWER2" {
		t.Fatalf("table with a removed row = %v", table)
	}
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.Plan.PlanDigest = "sha256:plan-2" })
	code, body, _ := b.post("/i/default/config/apply", approval(csrf, confirm, "replace viewer"))
	if code != http.StatusConflict || !strings.Contains(body, "changed after you reviewed this. Nothing was changed") {
		t.Fatalf("stale plan = %d\n%s", code, body)
	}
	got := e.dcm.Posts("/control/v1/config")
	if len(got) != 1 || got[0]["plan_digest"] != "sha256:plan-1" {
		t.Fatalf("the digest sent is not the one shown: %v", got)
	}
	res := auditOf(t, e, "config.edit")
	if len(res) != 1 || res[0].Outcome != "failure" || !strings.Contains(res[0].Detail, "request_id=") {
		t.Fatalf("audit = %+v", res)
	}
	_, body, _ = b.get("/i/default/remote-aes")
	hasNot(t, "destinations after a refused change", body, "VIEWER2")
}

// Reload and rollback each go through a plan page with the difference and
// the same approval rule. The rollback page names the version that comes
// back. When LibreDCM blocks the change until the licence notice is
// acknowledged, Libre WT says so and that DICOM services keep running.
// Verifies: WT-SRS-022, WT-SRS-031
func TestReloadAndRollback(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	start := url.Values{"csrf": {csrf}}

	confirm, page := b.review("/i/default/reload/review", start)
	has(t, "reload page", page, "Review: Reload the configuration", "reads its configuration files again", "<b>VIEWER2</b>", "The change is made on instance <b>default</b>")
	if n := len(e.dcm.Posts("/control/v1/reload/plan")); n != 1 || sent(e.dcm) != 0 {
		t.Fatalf("reload review: %d plan(s), %d sent", n, sent(e.dcm))
	}
	// The confirmation of a reload does not approve a rollback or an edit.
	for _, p := range []string{"/i/default/rollback/apply", "/i/default/config/apply", "/i/default/echo/apply"} {
		if code, _, _ := b.post(p, approval(csrf, confirm, "x")); code != http.StatusConflict || sent(e.dcm) != 0 {
			t.Fatalf("reload confirmation on %s = %d", p, code)
		}
		confirm, _ = b.review("/i/default/reload/review", start)
	}
	code, body, _ := b.post("/i/default/reload/apply", approval(csrf, confirm, "file edited by IT"))
	if code != 200 {
		t.Fatalf("reload = %d\n%s", code, body)
	}
	has(t, "reload result", body, "Done: Reload", "sha256:after")
	got := e.dcm.Posts("/control/v1/reload")
	if len(got) != 1 || got[0]["plan_digest"] != "sha256:plan-1" || got[0]["confirm"] != true || got[0]["settings"] != nil {
		t.Fatalf("reload request = %v", got)
	}
	envelopeOK(t, "reload", got[0], "admin1", "file edited by IT", "node-default", "AE_DEFAULT")

	// A reload that changes nothing says so on the warning page.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Plan = dcmclient.ChangePlan{PlanDigest: "sha256:plan-same"}
	})
	_, page = b.review("/i/default/reload/review", start)
	has(t, "reload without a change", page, "Nothing would change")

	// Rollback.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Plan.Changed, f.Cfg.Plan.PlanDigest = true, "sha256:plan-rb"
		f.Cfg.Plan.AllowList.Removed = []string{"MR1"}
		f.Cfg.Plan.AllowList.EnforcedBefore, f.Cfg.Plan.AllowList.EnforcedAfter = true, true
		f.Cfg.History.Previous = &dcmclient.OverlayVersion{Time: "2026-10-01T09:00:00Z", Operator: "admin9", Reason: "first setup", Kind: "edit"}
	})
	confirm, page = b.review("/i/default/rollback/review", start)
	has(t, "rollback page", page, "Review: Roll back to the previous version", "made 2026-10-01T09:00:00Z by <b>admin9</b> (edit), reason: first setup",
		"<code>MR1</code>")
	if code, _, _ := b.post("/i/default/reload/apply", approval(csrf, confirm, "x")); code != http.StatusConflict {
		t.Fatalf("rollback confirmation on reload = %d", code)
	}
	confirm, _ = b.review("/i/default/rollback/review", start)
	code, body, _ = b.post("/i/default/rollback/apply", approval(csrf, confirm, "wrong rule removed"))
	if code != 200 || !strings.Contains(body, "Done: Rollback") {
		t.Fatalf("rollback = %d\n%s", code, body)
	}
	rb := e.dcm.Posts("/control/v1/config/rollback")
	if len(rb) != 1 || rb[0]["plan_digest"] != "sha256:plan-rb" || len(e.dcm.Posts("/control/v1/reload")) != 1 {
		t.Fatalf("rollback request = %v", rb)
	}
	envelopeOK(t, "rollback", rb[0], "admin1", "wrong rule removed", "node-default", "AE_DEFAULT")
	if a, r := auditOf(t, e, "config.rollback.approved"), auditOf(t, e, "config.rollback"); len(a) != 1 || r[len(r)-1].Outcome != "success" {
		t.Fatalf("rollback audit: %+v %+v", a, r)
	}

	// No previous version: LibreDCM's reason, nothing to approve.
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.History.Previous = nil })
	code, body, _ = b.post("/i/default/rollback/review", start)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "no previous configuration version") || strings.Contains(body, `name="confirm"`) {
		t.Fatalf("rollback without a previous version = %d", code)
	}

	// The licence block: at the plan and at the apply step.
	block := fakedcm.Failure{Status: http.StatusLocked, Code: "license_ack_required", Msg: "configuration reload blocked until the licence notice is acknowledged"}
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.Fail["/control/v1/reload/plan"] = block })
	code, body, _ = b.post("/i/default/reload/review", start)
	if code != http.StatusLocked {
		t.Fatalf("blocked reload plan = %d", code)
	}
	has(t, "licence block", body, "until the licence notice is acknowledged", "Nothing was changed", "DICOM services keep running")
	hasNot(t, "licence block without a licence page", body, "Open the licence page")
	// Once Libre WT has a licence page for the instance, the message links
	// to it.
	saved := functions
	functions = append(append([]function(nil), functions...), function{"license", "Licence", "license", accounts.Viewer})
	e.dcm.Set(func(f *fakedcm.Server) { f.Features = append(f.Features, "license") })
	_, body, _ = b.post("/i/default/reload/review", start)
	functions = saved
	has(t, "licence block with a licence page", body, `<a href="/i/default/license">Open the licence page of this instance</a>`)
	e.dcm.Set(func(f *fakedcm.Server) {
		delete(f.Cfg.Fail, "/control/v1/reload/plan")
	})
	confirm, _ = b.review("/i/default/reload/review", start)
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.Fail["/control/v1/reload"] = block })
	code, body, _ = b.post("/i/default/reload/apply", approval(csrf, confirm, "again"))
	if code != http.StatusLocked || !strings.Contains(body, "until the licence notice is acknowledged") {
		t.Fatalf("blocked reload = %d", code)
	}
}

// Operators and Admins run a C-ECHO test to a configured destination and
// see success, or the stage that failed, and the duration. A free host,
// port and AE title is for an Admin only. The test follows the approval
// rule and names the user to LibreDCM.
// Verifies: WT-SRS-024, WT-SRS-031, WT-SRS-033
func TestEcho(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	op := e.as(t, "op1")
	csrf := op.csrf()
	code, body, _ := op.get("/i/default/echo")
	if code != 200 {
		t.Fatalf("echo page = %d", code)
	}
	has(t, "echo page", body, "<b>VIEWER1</b>", `name="remote_ae" value="VIEWER1"`, "No patient data is sent")
	hasNot(t, "echo page as operator", body, `name="host"`, "Another address")

	confirm, page := op.review("/i/default/echo/review", url.Values{"csrf": {csrf}, "remote_ae": {"VIEWER1"}})
	has(t, "echo warning page", page, "configured destination <b>VIEWER1</b>", "<code>10.0.0.20:104</code>", "Instance <b>default</b>", "<code>AE_DEFAULT</code>", "No patient data is sent")
	if sent(e.dcm) != 0 {
		t.Fatal("the review ran the test")
	}
	// Without a reason: nothing is sent.
	if code, _, _ := op.post("/i/default/echo/apply", approval(csrf, confirm, "")); code != http.StatusUnprocessableEntity || sent(e.dcm) != 0 {
		t.Fatalf("echo without a reason = %d", code)
	}
	confirm, _ = op.review("/i/default/echo/review", url.Values{"csrf": {csrf}, "remote_ae": {"VIEWER1"}})
	code, body, _ = op.post("/i/default/echo/apply", approval(csrf, confirm, "viewer reports no images"))
	if code != 200 {
		t.Fatalf("echo = %d\n%s", code, body)
	}
	has(t, "echo result", body, `<span class="state success">success</span>`, "answered the C-ECHO in 42 ms", "<code>10.0.0.20:104</code>", "configured destination")
	got := e.dcm.Posts("/control/v1/echo")
	if len(got) != 1 || got[0]["remote_ae"] != "VIEWER1" || got[0]["host"] != nil || got[0]["port"] != nil {
		t.Fatalf("echo request = %v", got)
	}
	envelopeOK(t, "echo", got[0], "op1", "viewer reports no images", "node-default", "AE_DEFAULT")
	if code, _, _ := op.post("/i/default/echo/apply", approval(csrf, confirm, "again")); code != http.StatusConflict || sent(e.dcm) != 1 {
		t.Fatalf("echo confirmation used twice = %d", code)
	}

	// A test that fails is a result: the stage and the reason are shown.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Cfg.Echo.OK, f.Cfg.Echo.Stage, f.Cfg.Echo.Error, f.Cfg.Echo.DurationMS = false, "association", "association rejected or aborted by the peer", 87
	})
	confirm, _ = op.review("/i/default/echo/review", url.Values{"csrf": {csrf}, "remote_ae": {"VIEWER1"}})
	code, body, _ = op.post("/i/default/echo/apply", approval(csrf, confirm, "second try"))
	if code != 200 {
		t.Fatalf("failed echo = %d", code)
	}
	has(t, "failed echo", body, `<span class="state open">failed</span>`, "failed at the stage <b>association</b>: association rejected or aborted by the peer (after 87 ms)")
	res := auditOf(t, e, "echo")
	if len(res) != 4 || res[1].Outcome != "success" || res[3].Outcome != "failure" || !strings.Contains(res[3].Detail, "stage association") ||
		res[3].User != "op1" || res[3].Target != "VIEWER1" || res[3].Instance != "default" {
		t.Fatalf("echo audit = %+v", res)
	}

	// An Operator can't test a free address, with a valid form or not.
	free := url.Values{"csrf": {csrf}, "host": {"192.0.2.7"}, "port": {"104"}, "ae_title": {"NEWCT"}, "tls": {"yes"}}
	n := sent(e.dcm)
	code, body, _ = op.post("/i/default/echo/review", free)
	if code != http.StatusForbidden || !strings.Contains(body, "free address is for administrators") || strings.Contains(body, `name="confirm"`) {
		t.Fatalf("operator, free address = %d", code)
	}
	// An unknown destination name.
	code, _, _ = op.post("/i/default/echo/review", url.Values{"csrf": {csrf}, "remote_ae": {"NOPE"}})
	if code != http.StatusNotFound {
		t.Fatalf("unknown destination = %d", code)
	}

	// The Admin can.
	ad := e.as(t, "admin1")
	acsrf := ad.csrf()
	_, body, _ = ad.get("/i/default/echo")
	has(t, "echo page as admin", body, `name="host"`, `name="tls"`, "Another address (administrators)")
	free.Set("csrf", acsrf)
	confirm, page = ad.review("/i/default/echo/review", free)
	has(t, "free address warning page", page, `<b class="diff">free address</b>`, "<code>192.0.2.7:104</code>", "<code>NEWCT</code>", "certificate is not checked")
	// The Admin's confirmation in the Operator's session is refused.
	if code, _, _ := op.post("/i/default/echo/apply", approval(csrf, confirm, "x")); code != http.StatusConflict || sent(e.dcm) != n {
		t.Fatalf("admin confirmation used by the operator = %d", code)
	}
	confirm, _ = ad.review("/i/default/echo/review", free)
	code, body, _ = ad.post("/i/default/echo/apply", approval(acsrf, confirm, "new CT in room 4"))
	if code != 200 {
		t.Fatalf("free echo = %d\n%s", code, body)
	}
	has(t, "free echo result", body, "free address", "yes, certificate not checked")
	got = e.dcm.Posts("/control/v1/echo")
	last := got[len(got)-1]
	if last["host"] != "192.0.2.7" || last["port"] != float64(104) || last["ae_title"] != "NEWCT" || last["tls"] != true || last["remote_ae"] != nil {
		t.Fatalf("free echo request = %v", last)
	}
	envelopeOK(t, "free echo", last, "admin1", "new CT in room 4", "node-default", "AE_DEFAULT")
	// Targets that can't be an address never reach LibreDCM.
	n = sent(e.dcm)
	for _, bad := range []url.Values{
		{"host": {""}, "port": {"104"}, "ae_title": {"X"}},
		{"host": {"http://192.0.2.7/x"}, "port": {"104"}, "ae_title": {"X"}},
		{"host": {"192.0.2.7"}, "port": {"0"}, "ae_title": {"X"}},
		{"host": {"192.0.2.7"}, "port": {"70000"}, "ae_title": {"X"}},
		{"host": {"192.0.2.7"}, "port": {"abc"}, "ae_title": {"X"}},
		{"host": {"192.0.2.7"}, "port": {"104"}, "ae_title": {""}},
		{"host": {"192.0.2.7"}, "port": {"104"}, "ae_title": {"SEVENTEEN_CHARS_X"}},
	} {
		bad.Set("csrf", acsrf)
		if code, body, _ := ad.post("/i/default/echo/review", bad); code != http.StatusUnprocessableEntity || strings.Contains(body, `name="confirm"`) {
			t.Fatalf("bad target %v = %d", bad, code)
		}
	}

	// The destination gets another address between the warning page and
	// the approval: nothing is sent.
	confirm, _ = ad.review("/i/default/echo/review", url.Values{"csrf": {acsrf}, "remote_ae": {"VIEWER1"}})
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.RemoteAEs[0].Host = "203.0.113.9" })
	code, body, _ = ad.post("/i/default/echo/apply", approval(acsrf, confirm, "x"))
	if code != http.StatusConflict || !strings.Contains(body, "destination changed after you reviewed the test") || sent(e.dcm) != n {
		t.Fatalf("retargeted destination = %d, %d sent", code, sent(e.dcm)-n)
	}
}

// Every new address is refused below its role with a valid form, and every
// state-changing address is refused without the session's CSRF token.
// Nothing reaches LibreDCM in either case, not even a plan request. Pages
// show no control the role can't use.
// Verifies: WT-SRS-004, WT-SRS-031, WT-SRS-034
func TestConfigRolesAndCSRF(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	type row struct {
		method, path string
		min          accounts.Role
		form         url.Values
	}
	O, A := accounts.Operator, accounts.Admin
	rows := []row{
		{"GET", "/config", O, nil},
		{"GET", "/remote-aes", O, nil},
		{"GET", "/allow-list", O, nil},
		{"GET", "/config/edit", A, nil},
		{"POST", "/config/review", A, destForm("")},
		{"POST", "/config/apply", A, approval("", "x", "r")},
		{"POST", "/reload/review", A, url.Values{}},
		{"POST", "/reload/apply", A, approval("", "x", "r")},
		{"POST", "/rollback/review", A, url.Values{}},
		{"POST", "/rollback/apply", A, approval("", "x", "r")},
		{"GET", "/echo", O, nil},
		{"POST", "/echo/review", O, url.Values{"remote_ae": {"VIEWER1"}}},
		{"POST", "/echo/apply", O, approval("", "x", "r")},
	}
	users := map[accounts.Role]string{accounts.Viewer: "viewer1", accounts.Operator: "op1", accounts.Admin: "admin1"}
	for _, role := range []accounts.Role{accounts.Viewer, O, A} {
		b := e.as(t, users[role])
		csrf := b.csrf()
		for _, rw := range rows {
			allowed := rank(role) >= rank(rw.min)
			if rw.method == "GET" {
				code, body, _ := b.get("/i/default" + rw.path)
				if refused := code == http.StatusForbidden && strings.Contains(body, "can&#39;t open this page"); refused == allowed {
					t.Errorf("GET %s as %s: HTTP %d, allowed=%v", rw.path, role, code, allowed)
				}
				continue
			}
			// With the right CSRF token: the role decides.
			form := url.Values{}
			for k, v := range rw.form {
				form[k] = v
			}
			form.Set("csrf", csrf)
			before := planned(e.dcm) + sent(e.dcm)
			code, body, _ := b.post("/i/default"+rw.path, form)
			refused := code == http.StatusForbidden && strings.Contains(body, "can&#39;t open this page")
			if refused == allowed {
				t.Errorf("POST %s as %s: HTTP %d, allowed=%v", rw.path, role, code, allowed)
			}
			if !allowed && planned(e.dcm)+sent(e.dcm) != before {
				t.Errorf("POST %s as %s reached LibreDCM", rw.path, role)
			}
			// Without it, or with another session's: refused for every role.
			for _, token := range []string{"", "0123456789abcdef", e.as(t, users[role]).csrf()} {
				form.Set("csrf", token)
				before := planned(e.dcm) + sent(e.dcm)
				code, body, _ := b.post("/i/default"+rw.path, form)
				if code != http.StatusForbidden || (allowed && !strings.Contains(body, "Request refused")) || planned(e.dcm)+sent(e.dcm) != before {
					t.Errorf("POST %s as %s with CSRF token %q: HTTP %d", rw.path, role, token, code)
				}
			}
		}
	}
	if sent(e.dcm) != 0 {
		t.Fatalf("%d action(s) sent by the role walk", sent(e.dcm))
	}
	// What each role sees on the dashboard and in the instance tabs.
	for role, want := range map[accounts.Role]bool{accounts.Viewer: false, O: true, A: true} {
		_, body, _ := e.as(t, users[role]).get("/i/default/")
		for _, link := range []string{`href="/i/default/config"`, `href="/i/default/remote-aes"`, `href="/i/default/allow-list"`, `href="/i/default/echo"`} {
			if strings.Contains(body, link) != want {
				t.Errorf("%s: link %s shown=%v", role, link, !want)
			}
		}
		has(t, "dashboard", body, `href="/i/default/status"`, `href="/i/default/exceptions"`)
	}
	// The Operator's pages offer no change.
	op := e.as(t, "op1")
	for _, p := range []string{"/config", "/remote-aes", "/allow-list"} {
		_, body, _ := op.get("/i/default" + p)
		hasNot(t, "operator "+p, body, "/config/edit", "/reload/review", "/rollback/review")
	}
}

// While the node behind an instance is not the one Libre WT remembers, or
// can't be reached, no plan is asked for and no action is sent. A change of
// the node between the warning page and the approval is caught, and a
// refusal by LibreDCM ("not the node you expect") is reported as such.
// Verifies: WT-SRS-015, WT-SRS-031
func TestConfigActionsNeedTheRememberedNode(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	start := url.Values{"csrf": {csrf}}
	b.get("/i/default/") // the identity is remembered
	confirm, _ := b.review("/i/default/reload/review", start)
	echoConfirm, _ := b.review("/i/default/echo/review", url.Values{"csrf": {csrf}, "remote_ae": {"VIEWER1"}})
	plans := planned(e.dcm)

	e.dcm.Set(func(f *fakedcm.Server) { f.NodeID = "node-replaced" })
	for _, c := range []struct {
		path string
		form url.Values
	}{
		{"/reload/review", start}, {"/rollback/review", start}, {"/config/review", destForm(csrf)},
		{"/echo/review", url.Values{"csrf": {csrf}, "remote_ae": {"VIEWER1"}}},
		{"/echo/review", url.Values{"csrf": {csrf}, "host": {"192.0.2.7"}, "port": {"104"}, "ae_title": {"X"}}},
		{"/reload/apply", approval(csrf, confirm, "x")}, {"/echo/apply", approval(csrf, echoConfirm, "x")},
	} {
		code, body, _ := b.post("/i/default"+c.path, c.form)
		if code != http.StatusConflict || !strings.Contains(body, "not the node Libre WT remembers") {
			t.Fatalf("%s with a changed node = %d\n%s", c.path, code, body)
		}
	}
	if planned(e.dcm) != plans || sent(e.dcm) != 0 {
		t.Fatalf("requests to a changed node: %d plans, %d actions", planned(e.dcm)-plans, sent(e.dcm))
	}
	// The pages offer no action while the node differs.
	_, body, _ := b.get("/i/default/config")
	hasNot(t, "config page with a changed node", body, "/reload/review", `href="/i/default/config/edit"`)

	// The node comes back; LibreDCM itself refuses with "instance mismatch".
	e.dcm.Set(func(f *fakedcm.Server) { f.NodeID = "node-default" })
	confirm, _ = b.review("/i/default/reload/review", start)
	e.dcm.Set(func(f *fakedcm.Server) { f.MismatchOnPost = true })
	code, body, _ := b.post("/i/default/reload/apply", approval(csrf, confirm, "x"))
	if code != http.StatusConflict || !strings.Contains(body, "is not the node Libre WT expects for this instance. Nothing was changed") {
		t.Fatalf("mismatch at apply = %d\n%s", code, body)
	}
	code, body, _ = b.post("/i/default/reload/review", start)
	if code != http.StatusConflict || !strings.Contains(body, "Nothing was changed") {
		t.Fatalf("mismatch at plan = %d\n%s", code, body)
	}
	e.dcm.Set(func(f *fakedcm.Server) { f.MismatchOnPost = false })

	// Unreachable: a plain page, nothing sent.
	e.dcm.Set(func(f *fakedcm.Server) { f.Down = true })
	for _, p := range []string{"/config", "/remote-aes", "/allow-list", "/echo", "/config/edit"} {
		code, body, _ := b.get("/i/default" + p)
		if code != 200 || !strings.Contains(body, "can't be reached, so this page has nothing to show") {
			t.Fatalf("%s with the node down = %d\n%s", p, code, body)
		}
		hasNot(t, p+" with the node down", body, "VIEWER1", "FAKEDCM")
	}
}

// When another node answers a read than the node the page is for, nothing
// of the answer is shown.
// Verifies: WT-SRS-014, WT-SRS-015
func TestReadAnsweredByAnotherNode(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	for page, path := range map[string]string{"/config": "/control/v1/config", "/remote-aes": "/control/v1/remote-aes",
		"/allow-list": "/control/v1/allow-list", "/config/edit": "/control/v1/allow-list", "/echo": "/control/v1/remote-aes"} {
		e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.OtherNode = map[string]bool{path: true} })
		code, body, _ := b.get("/i/default" + page)
		if code != http.StatusConflict || !strings.Contains(body, "Another node answered") {
			t.Fatalf("%s answered by another node = %d", page, code)
		}
		hasNot(t, page, body, "VIEWER1", "CT1@10.0.0.5", "<code>FAKEDCM</code>")
	}
	// The destination list of a C-ECHO review, too.
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.OtherNode = map[string]bool{"/control/v1/remote-aes": true} })
	code, body, _ := b.post("/i/default/echo/review", url.Values{"csrf": {b.csrf()}, "remote_ae": {"VIEWER1"}})
	if code != http.StatusBadGateway || strings.Contains(body, `name="confirm"`) {
		t.Fatalf("echo review with another node's list = %d", code)
	}
	// The history of another node is left out of the configuration page.
	e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.OtherNode = map[string]bool{"/control/v1/config/history": true} })
	_, body, _ = b.get("/i/default/config")
	has(t, "history of another node", body, "The version history could not be read")
	hasNot(t, "history of another node", body, "admin0", "/rollback/review")
}

// A LibreDCM that does not offer a function gets a plain page that says so,
// for views and for actions; nothing is sent to it.
// Verifies: WT-SRS-018
func TestConfigFunctionsNotOffered(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	e.dcm.Set(func(f *fakedcm.Server) { f.Features = []string{"conflicts", "status"} })
	b := e.as(t, "admin1")
	csrf := b.csrf()
	for _, p := range []string{"/config", "/remote-aes", "/allow-list", "/config/edit", "/echo"} {
		code, body, _ := b.get("/i/default" + p)
		if code != 200 || !strings.Contains(body, "Not available on this instance") {
			t.Fatalf("%s = %d", p, code)
		}
	}
	for p, form := range map[string]url.Values{"/config/review": destForm(csrf), "/reload/review": {"csrf": {csrf}}, "/rollback/review": {"csrf": {csrf}},
		"/echo/review": {"csrf": {csrf}, "remote_ae": {"VIEWER1"}}} {
		code, body, _ := b.post("/i/default"+p, form)
		if code != 200 || !strings.Contains(body, "Not available on this instance") {
			t.Fatalf("%s = %d", p, code)
		}
	}
	_, body, _ := b.get("/i/default/")
	has(t, "dashboard", body, "<td>Configuration</td>", "not available: this LibreDCM version does not support it")
	// With the views but without the overlay file: read-only, and said so.
	e.dcm.Set(func(f *fakedcm.Server) {
		f.Features = []string{"conflicts", "status", "config", "remote-aes", "allow-list", "reload", "echo"}
	})
	_, body, _ = b.get("/i/default/config")
	has(t, "config without edit", body, "Configuration edit is off on this node", "/reload/review")
	hasNot(t, "config without edit", body, "/config/edit", "/rollback/review")
	if planned(e.dcm)+sent(e.dcm) != 0 {
		t.Fatal("a request reached a node that does not offer the function")
	}
}

// When the audit log can't be written, the approved action is not sent.
// Verifies: WT-SRS-033
func TestNoAuditNoConfigAction(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	tokens := map[string]string{}
	tokens["/config/apply"], _ = b.review("/i/default/config/review", destForm(csrf))
	tokens["/reload/apply"], _ = b.review("/i/default/reload/review", url.Values{"csrf": {csrf}})
	tokens["/rollback/apply"], _ = b.review("/i/default/rollback/review", url.Values{"csrf": {csrf}})
	tokens["/echo/apply"], _ = b.review("/i/default/echo/review", url.Values{"csrf": {csrf}, "remote_ae": {"VIEWER1"}})
	e.breakAudit(t)
	for p, tok := range tokens {
		code, body, _ := b.post("/i/default"+p, approval(csrf, tok, "approved"))
		if code != http.StatusInternalServerError || !strings.Contains(body, "audit log can&#39;t be written, so nothing was sent") {
			t.Fatalf("%s without an audit record = %d\n%s", p, code, body)
		}
	}
	if sent(e.dcm) != 0 {
		t.Fatalf("%d action(s) sent without an audit record", sent(e.dcm))
	}
}

// mute is a client whose actions get no answer.
type mute struct{ fleet.Client }

var errMute = errors.New("LibreDCM unreachable: context deadline exceeded (test)")

func (mute) Reload(context.Context, string, []string, dcmclient.Envelope) (dcmclient.ChangeResult, error) {
	return dcmclient.ChangeResult{}, errMute
}

func (mute) Echo(context.Context, dcmclient.EchoTarget, dcmclient.Envelope) (dcmclient.EchoResult, error) {
	return dcmclient.EchoResult{}, errMute
}

// When LibreDCM gives no answer to an action, Libre WT does not claim an
// outcome: the page and the audit record say it is not known and give the
// request ID to look for.
// Verifies: WT-SRS-033
func TestNoAnswerMeansUnknownOutcome(t *testing.T) {
	e := newFleetEnv(t, "mute-1")
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	confirm, _ := b.review("/i/mute-1/reload/review", url.Values{"csrf": {csrf}})
	code, body, _ := b.post("/i/mute-1/reload/apply", approval(csrf, confirm, "x"))
	if code != http.StatusGatewayTimeout {
		t.Fatalf("reload without an answer = %d", code)
	}
	has(t, "no answer", body, "Reload: outcome not known", "It is not known whether the action was done", "request ID")
	hasNot(t, "no answer", body, "Nothing was changed", "Done:")
	res := auditOf(t, e, "config.reload")
	if len(res) != 1 || res[0].Outcome != "unknown" || !regexp.MustCompile(`request_id=[0-9a-f]{32}`).MatchString(res[0].Detail) {
		t.Fatalf("audit = %+v", res)
	}
	confirm, _ = b.review("/i/mute-1/echo/review", url.Values{"csrf": {csrf}, "remote_ae": {"VIEWER1"}})
	code, body, _ = b.post("/i/mute-1/echo/apply", approval(csrf, confirm, "x"))
	if code != http.StatusGatewayTimeout || !strings.Contains(body, "C-ECHO test: outcome not known") {
		t.Fatalf("echo without an answer = %d", code)
	}
}

// Other answers of LibreDCM to an action are shown with LibreDCM's text and
// never as success.
// Verifies: WT-SRS-021, WT-SRS-032
func TestActionRefusalsAreShown(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	for _, c := range []struct {
		f        fakedcm.Failure
		code     int
		text     string
		noChange bool
	}{
		{fakedcm.Failure{Status: 422, Code: "set_by_environment", Msg: "dicom.allowed_aes is set by the environment variable LIBRE_DCM_ALLOWED_AES"}, 422, "LIBRE_DCM_ALLOWED_AES", true},
		{fakedcm.Failure{Status: 429, Code: "busy", Msg: "another action of this kind is running"}, 429, "another action of this kind is running", true},
		{fakedcm.Failure{Status: 500, Code: "config_write_failed", Msg: "the configuration could not be written <i>disk</i>"}, 502, "could not be written &lt;i&gt;disk&lt;/i&gt;", false},
		{fakedcm.Failure{Status: 500, Code: "audit_unavailable", Msg: "the audit record could not be written"}, 502, "audit record could not be written", false},
	} {
		e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.Fail = map[string]fakedcm.Failure{} })
		confirm, _ := b.review("/i/default/config/review", destForm(csrf))
		e.dcm.Set(func(f *fakedcm.Server) { f.Cfg.Fail["/control/v1/config"] = c.f })
		code, body, _ := b.post("/i/default/config/apply", approval(csrf, confirm, "x"))
		if code != c.code || !strings.Contains(body, c.text) || strings.Contains(body, "Done:") || !strings.Contains(body, "Configuration change: not done") {
			t.Fatalf("%s = %d\n%s", c.f.Code, code, body)
		}
		if strings.Contains(body, "Nothing was changed") != c.noChange {
			t.Fatalf("%s: \"Nothing was changed\" shown=%v", c.f.Code, !c.noChange)
		}
	}
	res := auditOf(t, e, "config.edit")
	if len(res) != 4 {
		t.Fatalf("audit = %+v", res)
	}
	for _, r := range res {
		if r.Outcome != "failure" {
			t.Fatalf("audit outcome = %+v", r)
		}
	}
}

// A session keeps a bounded number of open approvals.
// Verifies: WT-SRS-031
func TestOpenApprovalsAreBounded(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	csrf := b.csrf()
	first, _ := b.review("/i/default/reload/review", url.Values{"csrf": {csrf}})
	var last string
	for i := 0; i < maxConfirmsPerSession; i++ {
		last, _ = b.review("/i/default/reload/review", url.Values{"csrf": {csrf}})
	}
	e.web.ss.mu.Lock()
	n := len(e.web.ss.confirms)
	e.web.ss.mu.Unlock()
	if n != maxConfirmsPerSession {
		t.Fatalf("%d open approvals, want %d", n, maxConfirmsPerSession)
	}
	if code, _, _ := b.post("/i/default/reload/apply", approval(csrf, first, "x")); code != http.StatusConflict {
		t.Fatalf("the oldest approval still works: %d", code)
	}
	if code, _, _ := b.post("/i/default/reload/apply", approval(csrf, last, "x")); code != 200 {
		t.Fatalf("the newest approval = %d", code)
	}
}

// The allow-list form gives an empty list, never "no value", when every
// line is blank: an emptied list is a change LibreDCM must see and warn
// about. Lines are trimmed and blank lines left out.
// Verifies: WT-SRS-021
func TestParseRules(t *testing.T) {
	got, err := parseRules(" \n\t\n")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("blank form = %#v, %v", got, err)
	}
	got, err = parseRules(" CT1@10.0.0.5 \n\nMR1\r\n")
	if err != nil || !reflect.DeepEqual(got, []string{"CT1@10.0.0.5", "MR1"}) {
		t.Fatalf("rules = %#v, %v", got, err)
	}
	if _, err := parseRules(strings.Repeat("AE\n", maxAllowRules+1)); err == nil {
		t.Fatal("too many rules accepted")
	}
}
