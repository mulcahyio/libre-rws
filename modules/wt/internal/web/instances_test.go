package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fakedcm"
)

// The fleet page lists every instance by cluster label with health, version,
// uptime, licence state, associations, store and rejection counts, outbox
// depth and open exceptions. All roles see it. An instance that can't be
// reached is shown as unreachable with the time of the last contact and
// none of its old values. Tokens appear nowhere.
// Verifies: WT-SRS-016, WT-SRS-023
func TestFleetPage(t *testing.T) {
	e := newFleetEnv(t, "a1@site-a", "a2@site-a", "solo")
	e.users(t)
	e.nodes["a2"].Set(func(f *fakedcm.Server) {
		f.Digest, f.Version = "sha256:bbbb", "0.3.1-test"
		f.Status.Ready = false
		f.Status.License = dcmclient.License{Enforced: true, State: "grace", Licensed: true, DaysLeft: 12, AckRequired: true}
	})
	e.nodes["solo"].Set(func(f *fakedcm.Server) {
		f.Status.UptimeSeconds = 7500 // 2h 5m
		f.Status.Counters.CStore = map[string]uint64{"success": 424242, "failure": 31, "invalid": 11}
		f.Status.Counters.AssociationsActive, f.Status.Counters.AssociationsRejected, f.Status.Counters.OutboxPending = 17, 23, 29
		open := uint64(37)
		f.Status.Counters.ConflictsOpen = &open
	})
	for _, user := range []string{"viewer1", "op1", "admin1"} {
		b := e.as(t, user)
		code, body, _ := b.get("/")
		if code != 200 {
			t.Fatalf("%s: fleet = %d", user, code)
		}
		has(t, user+" fleet", body,
			"Cluster <b>site-a</b>", "No cluster label",
			`<a href="/i/a1/"><b>a1</b></a>`, `<a href="/i/a2/"><b>a2</b></a>`, `<a href="/i/solo/"><b>solo</b></a>`,
			"AE_A1 · node-a1", "AE_SOLO · node-solo",
			"0.3.0-test", "0.3.1-test", "version differs in cluster", "settings differ in cluster",
			`<span class="state notready">not ready</span>`, `<span class="state reachable">ready</span>`,
			"grace, 12 days left, notice not acknowledged", "not enforced (source build)",
			"<td>2h 5m</td>", "<td>1d 2h</td>",
			"<td>17</td>", "<td>424242</td>", "<td>42</td>", "<td>23</td>", "<td>29</td>", `<a href="/i/solo/exceptions">37</a>`)
		hasNot(t, user+" fleet", body, "tok-SECRET", "Bearer", "unreachable")
		// Labelled clusters come first, unlabelled instances last.
		if strings.Index(body, "Cluster <b>site-a</b>") > strings.Index(body, "No cluster label") {
			t.Fatal("cluster order")
		}
	}
	// solo goes down: unreachable, last contact shown, none of its values.
	b := e.as(t, "viewer1")
	e.nodes["solo"].Set(func(f *fakedcm.Server) { f.Down = true })
	_, body, _ := b.get("/")
	has(t, "fleet with a node down", body, `<span class="state unreachable">unreachable</span>`, "LibreDCM unreachable", "Last successful contact: 20",
		"AE_SOLO · node-solo") // the remembered identity still names the node
	hasNot(t, "fleet with a node down", body, "424242", "<td>17</td>", "<td>23</td>", "<td>29</td>", "<td>2h 5m</td>", ">37<", "none since Libre WT started")
	// The other instances still show their values.
	has(t, "fleet with a node down", body, "0.3.0-test", "<td>1d 2h</td>")
	// Equal nodes in the cluster: no flags.
	e.nodes["a2"].Set(func(f *fakedcm.Server) { f.Digest, f.Version = "sha256:aaaa", "0.3.0-test" })
	_, body, _ = b.get("/")
	hasNot(t, "equal cluster", body, "differs in cluster", "differ in cluster")
	if strings.Contains(e.log.String(), "tok-SECRET") {
		t.Fatal("token in the log")
	}
}

// A node that never answered since Libre WT started says so.
// Verifies: WT-SRS-016
func TestFleetNeverReached(t *testing.T) {
	e := newFleetEnv(t, "a", "b")
	e.users(t)
	e.nodes["b"].Set(func(f *fakedcm.Server) { f.Down = true })
	_, body, _ := e.as(t, "viewer1").get("/")
	has(t, "fleet", body, "none since Libre WT started", `<span class="state unreachable">unreachable</span>`)
	// The row of b has no identity: nothing is remembered yet.
	if !regexp.MustCompile(`(?s)<b>b</b></a><br><small><code>—</code>`).MatchString(body) {
		t.Fatalf("row of b:\n%s", body)
	}
}

// Dashboard and status page show the values of LibreDCM's status endpoint
// with the time of the reading, under a banner that names the instance, its
// AE title, node ID and cluster label. The status page names the host
// procedure for start, stop and restart and offers no such action. The
// about page shows the versions.
// Verifies: WT-SRS-014, WT-SRS-017, WT-SRS-035
func TestDashboardStatusAndAbout(t *testing.T) {
	e := newFleetEnv(t, "dcm-1@site-a", "dcm-2@site-a")
	e.users(t)
	e.nodes["dcm-1"].Set(func(f *fakedcm.Server) {
		f.Status.Ready = false
		f.Status.Checks = []dcmclient.Check{{Name: "events", OK: false, Error: "nats: no servers available"}, {Name: "index", OK: true}, {Name: "storage", OK: true}}
		f.Status.Backends.Storage, f.Status.Backends.Index, f.Status.Backends.Events = "s3", "postgres", "nats"
		f.Status.DICOM.TLS, f.Status.DICOM.AllowListEntries = true, 0
		f.Status.License = dcmclient.License{Enforced: true, State: "valid", Licensed: true, DaysLeft: 352, Message: "licensed until 2027-11-01"}
		f.Status.Counters.PublishFailures = 13
		f.Status.CountersIncomplete = true
	})
	for _, user := range []string{"viewer1", "op1", "admin1"} {
		b := e.as(t, user)
		for _, p := range []string{"/i/dcm-1/", "/i/dcm-1/status", "/i/dcm-1/exceptions", "/i/dcm-1/exceptions/c1"} {
			code, body, _ := b.get(p)
			if code != 200 {
				t.Fatalf("%s %s = %d", user, p, code)
			}
			// The banner: name, AE title, node ID, cluster label.
			has(t, user+" banner on "+p, body, `<b class="iname">dcm-1</b>`, "<code>AE_DCM-1</code>", "<code>node-dcm-1</code>", "<b>site-a</b>", "Other instances (2 configured)")
			hasNot(t, user+" banner on "+p, body, "node-dcm-2", "AE_DCM-2", "tok-SECRET")
		}
		code, body, _ := b.get("/i/dcm-1/")
		if code != 200 {
			t.Fatalf("dashboard = %d", code)
		}
		has(t, "dashboard", body, "Read at 20", "<b>not ready</b>", "<b>0.3.0-test</b>", "<b>1d 2h</b>", "<b>valid, 352 days left</b>",
			"<span>Open associations</span><b>3</b>", "<span>Stored</span><b>2040</b>", "<span>Not stored</span><b>5</b>",
			"<span>Rejected associations</span><b>7</b>", "<span>Outbox pending</span><b>5</b>", "<span>Open exceptions</span><b>1</b>",
			"reports that it is not ready", `<a href="/i/dcm-1/status">Status</a>`, `<a href="/i/dcm-1/exceptions">Study Exceptions</a>`,
			"The licence state never stops DICOM services")
		code, body, _ = b.get("/i/dcm-1/status")
		if code != 200 {
			t.Fatalf("status = %d", code)
		}
		has(t, "status", body, "Read by Libre WT at <b>20", "node clock: 20",
			"<dd><code>node-dcm-1</code></dd>", "<dd><code>AE_DCM-1</code></dd>", "sha256:aaaa",
			"<dd>s3</dd>", "<dd>postgres</dd>", "<dd>nats</dd>", "<code>:11112</code>, TLS on", "<dd>TLS off</dd>",
			"0 (every calling AE may connect)",
			`Readiness: <span class="state notready">not ready</span>`,
			`<td>events</td><td><span class="state notready">failed</span> nats: no servers available</td>`,
			`<td>index</td><td><span class="state reachable">ok</span></td>`,
			"valid, 352 days left", "licensed until 2027-11-01", "The licence state never stops DICOM services",
			"<td>open now</td><td>3</td>", "<td>total</td><td>120</td>", "<td>rejected</td><td>7</td>",
			"<td>C-STORE</td><td>success</td><td>2040</td>", "<td>C-STORE</td><td>failure</td><td>2</td>", "<td>C-STORE</td><td>conflict-open</td><td>3</td>",
			"<td>C-FIND</td><td>total</td><td>310</td>",
			"<td>C-MOVE and C-GET sub-operations</td><td>completed</td><td>800</td>", "<td>C-MOVE and C-GET sub-operations</td><td>failed</td><td>1</td>",
			"<td>outbox pending</td><td>5</td>", "<td>publish failures</td><td>13</td>", "<td>Study Exceptions</td><td>open</td><td>1</td>",
			"could not read every counter",
			"Libre WT does not start, stop or restart LibreDCM", "LibreDCM.ps1 restart", "deploy/workstation/INSTALL.md")
		// No action on the status page or the dashboard: the only form is
		// the log-out button.
		for _, p := range []string{"/i/dcm-1/", "/i/dcm-1/status"} {
			_, page, _ := b.get(p)
			if n := strings.Count(page, "<form"); n != 1 || !strings.Contains(page, `action="/logout"`) || strings.Contains(page, "<button class=\"danger\"") {
				t.Fatalf("%s has %d forms", p, n)
			}
		}
		code, body, _ = b.get("/about")
		if code != 200 {
			t.Fatalf("about = %d", code)
		}
		has(t, "about", body, "version <b>wt-test-1.2.3</b>", `<a href="/i/dcm-1/"><b>dcm-1</b></a>`, "<td>0.3.0-test</td>",
			"<td><code>node-dcm-1</code></td>", "<td><code>AE_DCM-1</code></td>", "<td><code>node-dcm-2</code></td>", "<td><code>AE_DCM-2</code></td>")
		hasNot(t, "about", body, "tok-SECRET")
	}
	// An unreachable instance: about says so and shows the remembered
	// identity as remembered; the status page shows no values.
	b := e.as(t, "viewer1")
	e.nodes["dcm-2"].Set(func(f *fakedcm.Server) { f.Down = true })
	_, body, _ := b.get("/about")
	has(t, "about, node down", body, "unknown (not reachable)", "<code>node-dcm-2</code> <small>(remembered)</small>", "<code>AE_DCM-2</code> <small>(remembered)</small>")
	code, body, _ := b.get("/i/dcm-2/status")
	if code != 200 {
		t.Fatalf("status of a node that is down = %d", code)
	}
	has(t, "status, node down", body, "This instance can't be reached", "Last successful contact: 20", "No values are shown", "does not start, stop or restart")
	hasNot(t, "status, node down", body, "<td>C-STORE</td>", "2040", "Readiness:", "1d 2h")
	_, body, _ = b.get("/i/dcm-2/")
	has(t, "dashboard, node down", body, "This instance can't be reached", "unknown (instance not reachable)")
	hasNot(t, "dashboard, node down", body, "2040", "<b>ready</b>", "1d 2h")
}

// The instance comes from the address of each request and from nowhere
// else: two tabs on two instances never mix, an unknown name is a 404, the
// addresses of 0.1 never pick one of several instances, and an approval
// given for one instance is refused for another.
// Verifies: WT-SRS-014, WT-SRS-013
func TestInstanceFromAddressOnly(t *testing.T) {
	e := newFleetEnv(t, "a", "b")
	e.users(t)
	e.nodes["a"].Set(func(f *fakedcm.Server) { f.Conflict.PatientName = "ALPHA^ANNA" })
	e.nodes["b"].Set(func(f *fakedcm.Server) { f.Conflict.PatientName = "BRAVO^BERT" })
	b := e.as(t, "admin1")
	// Visit a, then b, then a again: each page shows its own instance.
	for _, name := range []string{"a", "b", "a", "b"} {
		other, mine, theirs := "b", "ALPHA^ANNA", "BRAVO^BERT"
		if name == "b" {
			other, mine, theirs = "a", "BRAVO^BERT", "ALPHA^ANNA"
		}
		code, body, _ := b.get("/i/" + name + "/exceptions")
		if code != 200 {
			t.Fatalf("%s = %d", name, code)
		}
		has(t, name, body, mine, `<b class="iname">`+name+`</b>`, `href="/i/`+name+`/exceptions/c1"`, "Study exceptions of instance <b>"+name+"</b>")
		hasNot(t, name, body, theirs, `href="/i/`+other+`/`, `href="/exceptions`)
	}
	for _, p := range []string{"/i/nope/", "/i/nope/status", "/i/nope/exceptions", "/i/A/", "/i/a%20/", "/i/a%2Fb/"} {
		if code, body, _ := b.get(p); code != http.StatusNotFound || !strings.Contains(body, "Unknown instance") {
			t.Fatalf("%s = %d", p, code)
		}
	}
	// An unknown name is never turned into a redirect, however it is written.
	for _, p := range []string{"/i/zzz", "/i/A", "/i/%2F%2Fevil.example", "/i/a%2F..%2F..%2Fx"} {
		if code, _, h := b.get(p); code != http.StatusNotFound || h.Get("Location") != "" {
			t.Fatalf("%s = %d, Location %q", p, code, h.Get("Location"))
		}
	}
	if code, _, h := b.get("/i/a"); code != http.StatusSeeOther || h.Get("Location") != "/i/a/" {
		t.Fatalf("/i/a = %d %s", code, h.Get("Location"))
	}
	// 0.1 addresses with several instances lead to the fleet page.
	for _, p := range []string{"/exceptions", "/exceptions/c1", "/exceptions/c1/review?action=reject"} {
		if code, _, h := b.get(p); code != http.StatusSeeOther || h.Get("Location") != "/" {
			t.Fatalf("%s = %d %s", p, code, h.Get("Location"))
		}
	}
	// No request field selects an instance: a form field or query value
	// naming b on a's address acts on a.
	_, body, _ := b.get("/i/a/exceptions/c1/review?action=reject&inst=b&instance=b")
	has(t, "review on a", body, "The edit is made on instance <b>a</b>", `action="/i/a/exceptions/c1/resolve"`)
	csrf, confirmA := find(t, csrfRE, body), find(t, confirmRE, body)
	form := func(confirm string) url.Values {
		return url.Values{"csrf": {csrf}, "confirm": {confirm}, "action": {"reject"}, "approve": {"yes"}, "reason": {"duplicate"}, "inst": {"b"}, "instance": {"b"}}
	}
	// The approval for a, presented on b's address: refused, nothing sent.
	if code, page, _ := b.post("/i/b/exceptions/c1/resolve", form(confirmA)); code != http.StatusConflict || !strings.Contains(page, "approval expired") {
		t.Fatalf("approval of a used on b = %d", code)
	}
	if len(e.nodes["a"].Resolves())+len(e.nodes["b"].Resolves()) != 0 {
		t.Fatal("an edit was sent with the approval of another instance")
	}
	// A fresh approval for a, on a's address: sent to a only, with a's
	// identity as the expected node.
	_, body, _ = b.get("/i/a/exceptions/c1/review?action=reject")
	if code, page, _ := b.post("/i/a/exceptions/c1/resolve", form(find(t, confirmRE, body))); code != 200 {
		t.Fatalf("resolve on a = %d %s", code, page)
	}
	got := e.nodes["a"].Resolves()
	if len(got) != 1 || got[0]["expect_node_id"] != "node-a" || got[0]["expect_ae_title"] != "AE_A" || got[0]["operator"] != "admin1" || len(e.nodes["b"].Resolves()) != 0 {
		t.Fatalf("a = %v, b = %v", got, e.nodes["b"].Resolves())
	}
	for _, p := range e.nodes["a"].Plans() {
		if p["expect_node_id"] != "node-a" || p["expect_ae_title"] != "AE_A" {
			t.Fatalf("plan without the expected identity: %v", p)
		}
	}
	// The audit log names the instance and can be filtered by it.
	_, _, _ = b.get("/i/b/exceptions/c1/review?action=reject")
	_, all, _ := b.get("/audit")
	has(t, "audit", all, `<a href="/audit?instance=a"`, `<a href="/audit?instance=b"`, "<td>a</td><td>exception.resolve</td>", "<td>b</td><td>exception.review</td>", "<td>—</td><td>login</td>")
	_, onlyB, _ := b.get("/audit?instance=b")
	has(t, "audit of b", onlyB, "<td>b</td><td>exception.review</td>", "for instance <b>b</b>")
	hasNot(t, "audit of b", onlyB, "<td>a</td>", "<td>login</td>")
	_, unknown, _ := b.get("/audit?instance=zzz")
	has(t, "audit with an unknown instance", unknown, "<td>—</td><td>login</td>")
}

// With one instance, the addresses of 0.1 lead to that instance.
// Verifies: WT-SRS-013
func TestOldAddressesWithOneInstance(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "viewer1")
	for from, to := range map[string]string{
		"/exceptions":                         "/i/default/exceptions",
		"/exceptions?state=all":               "/i/default/exceptions?state=all",
		"/exceptions/c1":                      "/i/default/exceptions/c1",
		"/exceptions/c1/review?action=reject": "/i/default/exceptions/c1/review?action=reject",
	} {
		if code, _, h := b.get(from); code != http.StatusSeeOther || h.Get("Location") != to {
			t.Fatalf("%s = %d %s", from, code, h.Get("Location"))
		}
	}
	// The redirect does not get a Viewer past the role check.
	if code, _, _ := b.get("/i/default/exceptions/c1/review?action=reject"); code != http.StatusForbidden {
		t.Fatalf("viewer review = %d", code)
	}
	_, body, _ := b.get("/i/default/")
	hasNot(t, "single instance", body, "Other instances")
}

// When the node behind an instance's address reports another node ID or AE
// title, every page of the instance warns, no edit is sent, and only an
// Admin can accept the new identity, through a warning page, a single-use
// confirmation, the approval box and a reason, all audited. LibreDCM's own
// refusal ("not the node you expect") is shown and nothing is changed.
// Verifies: WT-SRS-015
func TestIdentityChangeBlocksActionsUntilAccepted(t *testing.T) {
	e := newFleetEnv(t, "a", "b")
	e.users(t)
	admin := e.as(t, "admin1")
	const exc = "/i/a/exceptions/c1"
	// First contact: the identity is remembered. Take an approval now.
	_, body, _ := admin.get(exc + "/review?action=reject")
	csrf, early := find(t, csrfRE, body), find(t, confirmRE, body)
	resolve := func(confirm string) (int, string) {
		code, page, _ := admin.post(exc+"/resolve", url.Values{"csrf": {csrf}, "confirm": {confirm}, "action": {"reject"}, "approve": {"yes"}, "reason": {"duplicate"}})
		return code, page
	}

	// Another node now answers at a's address.
	e.nodes["a"].Set(func(f *fakedcm.Server) { f.NodeID, f.AETitle = "node-INTRUDER", "AE_OTHER" })
	for _, p := range []string{"/i/a/", "/i/a/status", "/i/a/exceptions", exc} {
		_, page, _ := admin.get(p)
		has(t, p, page, "Another node answers at this instance", "<code>node-a</code>", "<code>AE_A</code>", "<code>node-INTRUDER</code>", "<code>AE_OTHER</code>",
			"sends no change to this instance", `action="/i/a/identity/review"`, `<span class="state identity-changed">`)
	}
	_, fleetPage, _ := admin.get("/")
	has(t, "fleet", fleetPage, `<span class="state identity-changed">identity changed</span>`)
	// The exception page offers no resolve; review and resolve are refused.
	_, page, _ := admin.get(exc)
	has(t, "exception", page, "Edits are blocked for this instance")
	hasNot(t, "exception", page, "/review?action=")
	if code, page, _ := admin.get(exc + "/review?action=reject"); code != http.StatusConflict || !strings.Contains(page, "Edits are blocked for this instance") {
		t.Fatalf("review on a changed node = %d", code)
	}
	if code, page := resolve(early); code != http.StatusConflict || !strings.Contains(page, "not the node Libre WT remembers") {
		t.Fatalf("resolve on a changed node = %d %s", code, page)
	}
	if n := len(e.nodes["a"].Resolves()); n != 0 {
		t.Fatalf("%d edit(s) sent to a node with a changed identity", n)
	}
	// b is not affected.
	if _, page, _ := admin.get("/i/b/exceptions/c1"); strings.Contains(page, "Another node answers") || !strings.Contains(page, "/i/b/exceptions/c1/review?action=") {
		t.Fatal("instance b affected by a's identity change")
	}

	// Viewer and Operator see the warning but no accept button and can't
	// reach the acceptance.
	for _, user := range []string{"viewer1", "op1"} {
		b := e.as(t, user)
		_, page, _ := b.get("/i/a/")
		has(t, user, page, "Another node answers at this instance")
		hasNot(t, user, page, "identity/review", "Review the new identity")
		for _, p := range []string{"/i/a/identity/review", "/i/a/identity/apply"} {
			if code, _, _ := b.post(p, url.Values{"csrf": {b.csrf()}, "approve": {"yes"}, "reason": {"x"}}); code != http.StatusForbidden {
				t.Fatalf("%s %s = %d", user, p, code)
			}
		}
	}

	// Admin: the warning page.
	review := func() string {
		code, page, _ := admin.post("/i/a/identity/review", url.Values{"csrf": {csrf}})
		if code != 200 {
			t.Fatalf("identity review = %d %s", code, page)
		}
		has(t, "identity review", page, "this changes which node Libre WT acts on", "<code>node-a</code>", `<code class="diff">node-INTRUDER</code>`,
			`<code class="diff">AE_OTHER</code>`, "I have checked the node and approve the new identity", "Nothing is sent to LibreDCM")
		return find(t, confirmRE, page)
	}
	apply := func(confirm, approve, reason string) (int, string) {
		code, page, _ := admin.post("/i/a/identity/apply", url.Values{"csrf": {csrf}, "confirm": {confirm}, "approve": {approve}, "reason": {reason}})
		return code, page
	}
	if code, _, _ := admin.post("/i/a/identity/review", url.Values{}); code != http.StatusForbidden {
		t.Fatalf("identity review without CSRF = %d", code)
	}
	if code, page := apply("", "yes", "node replaced"); code != http.StatusConflict || !strings.Contains(page, "approval expired or was already used") {
		t.Fatalf("apply without confirmation = %d", code)
	}
	if code, _ := apply(review(), "", "node replaced"); code != http.StatusUnprocessableEntity {
		t.Fatalf("apply without approval = %d", code)
	}
	if code, _ := apply(review(), "yes", "   "); code != http.StatusUnprocessableEntity {
		t.Fatalf("apply without reason = %d", code)
	}
	// A confirmation of the exception review is not a confirmation of this.
	if code, page := apply(early, "yes", "node replaced"); code != http.StatusConflict || !strings.Contains(page, "approval expired or was already used") {
		t.Fatalf("apply with a foreign confirmation = %d", code)
	}
	// A confirmation issued for instance a does not accept on b.
	tok := review()
	if code, _, _ := admin.post("/i/b/identity/apply", url.Values{"csrf": {csrf}, "confirm": {tok}, "approve": {"yes"}, "reason": {"x"}}); code != http.StatusConflict {
		t.Fatalf("a's confirmation on b = %d", code)
	}
	// The node changes again after the review: what was reviewed is not
	// what would be accepted.
	tok = review()
	e.nodes["a"].Set(func(f *fakedcm.Server) { f.NodeID = "node-THIRD" })
	if code, page := apply(tok, "yes", "node replaced"); code != http.StatusConflict || !strings.Contains(page, "no longer reports the identity you reviewed") {
		t.Fatalf("apply after another change = %d", code)
	}
	e.nodes["a"].Set(func(f *fakedcm.Server) { f.NodeID = "node-INTRUDER" })
	id, _, _ := e.acct.InstanceIdentity(context.Background(), "a")
	if id.NodeID != "node-a" || id.AETitle != "AE_A" || id.AcceptedBy != "" {
		t.Fatalf("identity replaced by a refused request: %+v", id)
	}
	// When the audit log can't be written, nothing is accepted.
	tok = review()
	e.breakAudit(t)
	if code, page := apply(tok, "yes", "node replaced"); code != http.StatusInternalServerError || !strings.Contains(page, "audit log can&#39;t be written") {
		t.Fatalf("apply without audit = %d %s", code, page)
	}
	if id, _, _ = e.acct.InstanceIdentity(context.Background(), "a"); id.NodeID != "node-a" {
		t.Fatalf("identity accepted without an audit record: %+v", id)
	}
}

// The accepted identity becomes the expected one, with the administrator
// and the reason recorded; edits work again and name the new node. A used
// confirmation can't be used again.
// Verifies: WT-SRS-015
func TestIdentityAcceptedThenActionsResume(t *testing.T) {
	e := newFleetEnv(t, "a")
	e.users(t)
	admin := e.as(t, "admin1")
	_, _, _ = admin.get("/i/a/")
	csrf := admin.csrf()
	e.nodes["a"].Set(func(f *fakedcm.Server) { f.NodeID = "node-NEW" })
	// Nothing to accept on an unchanged or unreachable instance.
	_, page, _ := admin.post("/i/a/identity/review", url.Values{"csrf": {csrf}})
	tok := find(t, confirmRE, page)
	form := url.Values{"csrf": {csrf}, "confirm": {tok}, "approve": {"yes"}, "reason": {"planned replacement of the node"}}
	code, page, _ := admin.post("/i/a/identity/apply", form)
	if code != 200 || !strings.Contains(page, "Identity accepted") || !strings.Contains(page, "node-NEW") || strings.Contains(page, "Another node answers") {
		t.Fatalf("accept = %d %s", code, page)
	}
	if code, _, _ := admin.post("/i/a/identity/apply", form); code != http.StatusConflict {
		t.Fatalf("reused confirmation = %d", code)
	}
	id, _, _ := e.acct.InstanceIdentity(context.Background(), "a")
	if id.NodeID != "node-NEW" || id.AETitle != "AE_A" || id.AcceptedBy != "admin1" || id.Reason != "planned replacement of the node" {
		t.Fatalf("stored = %+v", id)
	}
	if code, page, _ := admin.post("/i/a/identity/review", url.Values{"csrf": {csrf}}); code != http.StatusConflict || !strings.Contains(page, "Nothing to accept") {
		t.Fatalf("review without a change = %d", code)
	}
	_, page, _ = admin.get("/i/a/exceptions/c1/review?action=reject")
	hasNot(t, "after accept", page, "Another node answers")
	code, page, _ = admin.post("/i/a/exceptions/c1/resolve", url.Values{"csrf": {csrf}, "confirm": {find(t, confirmRE, page)}, "action": {"reject"}, "approve": {"yes"}, "reason": {"duplicate"}})
	if code != 200 {
		t.Fatalf("resolve after accept = %d %s", code, page)
	}
	if got := e.nodes["a"].Resolves(); len(got) != 1 || got[0]["expect_node_id"] != "node-NEW" {
		t.Fatalf("resolves = %v", got)
	}
	log, _ := e.acct.AuditLogFor(context.Background(), "a", 0)
	seen := map[string]string{}
	for _, l := range log {
		seen[l.Action+"/"+l.Outcome] = l.User + " " + l.Detail
	}
	for _, k := range []string{"instance.identity.review/success", "instance.identity.accept.approved/success", "instance.identity.accept/success", "instance.identity.accept/failure"} {
		if !strings.HasPrefix(seen[k], "admin1 ") {
			t.Fatalf("audit lacks %s: %v", k, seen)
		}
	}
	if d := seen["instance.identity.accept/success"]; !strings.Contains(d, "node=node-NEW") || !strings.Contains(d, "was node=node-a") || !strings.Contains(d, "planned replacement of the node") {
		t.Fatalf("audit detail = %q", d)
	}
}

// LibreDCM itself refuses a request that names another node. Libre WT shows
// that refusal, records it, and reports that nothing was changed.
// Verifies: WT-SRS-015
func TestLibreDCMRefusesWrongNode(t *testing.T) {
	e := newFleetEnv(t, "a")
	e.users(t)
	admin := e.as(t, "admin1")
	_, page, _ := admin.get("/i/a/exceptions/c1/review?action=reject")
	form := url.Values{"csrf": {find(t, csrfRE, page)}, "confirm": {find(t, confirmRE, page)}, "action": {"reject"}, "approve": {"yes"}, "reason": {"duplicate"}}
	// The address now reaches a node that answers status reads like the old
	// one (a load balancer in between) but is another node for changes.
	e.nodes["a"].Set(func(f *fakedcm.Server) { f.MismatchOnPost = true })
	code, page, _ := admin.post("/i/a/exceptions/c1/resolve", form)
	if code != http.StatusConflict || !strings.Contains(page, "is not the node Libre WT expects") || !strings.Contains(page, "Nothing was changed") {
		t.Fatalf("resolve = %d %s", code, page)
	}
	if code, page, _ := admin.get("/i/a/exceptions/c1/review?action=reject"); code != http.StatusConflict || !strings.Contains(page, "Wrong node") {
		t.Fatalf("review = %d %s", code, page)
	}
	if n := len(e.nodes["a"].Resolves()); n != 0 {
		t.Fatalf("resolves = %d", n)
	}
	log, _ := e.acct.AuditLogFor(context.Background(), "a", 1)
	if len(log) != 1 || log[0].Action != "exception.resolve" || log[0].Outcome != "failure" || !strings.Contains(log[0].Detail, "not the node Libre WT expects") {
		t.Fatalf("audit = %+v", log)
	}
}

// A LibreDCM that does not support a function shows it as unavailable,
// without error pages; what it does support keeps working.
// Verifies: WT-SRS-018
func TestOlderLibreDCM(t *testing.T) {
	e := newFleetEnv(t, "new", "old")
	e.users(t)
	e.nodes["old"].Set(func(f *fakedcm.Server) { f.Legacy, f.Version = true, "0.2.0" })
	b := e.as(t, "admin1")
	_, body, _ := b.get("/")
	has(t, "fleet", body, "This LibreDCM version does not report status", `<a href="/i/old/exceptions">Study Exceptions</a> work`, "0.2.0", `<span class="state reachable">reachable</span>`)
	hasNot(t, "fleet", body, "unreachable")
	code, body, _ := b.get("/i/old/")
	if code != 200 {
		t.Fatalf("dashboard = %d", code)
	}
	has(t, "dashboard", body, "does not report status", "<td>Status</td>", "not available: this LibreDCM version does not support it",
		`<td><a href="/i/old/exceptions">Study Exceptions</a></td>`, "<td>available</td>", "<code>node-old</code>")
	hasNot(t, "dashboard", body, `<a href="/i/old/status">Status</a></td>`, "Open associations")
	code, body, _ = b.get("/i/old/status")
	if code != 200 {
		t.Fatalf("status of an older LibreDCM = %d (must not be an error page)", code)
	}
	has(t, "status", body, "Status is not available for this instance", "0.2.0", "does not start, stop or restart")
	hasNot(t, "status", body, "<td>C-STORE</td>", "Readiness:", "can't be reached")
	// Study Exceptions work, and the edit carries no fields the old node
	// would refuse.
	code, body, _ = b.get("/i/old/exceptions/c1/review?action=reject")
	if code != 200 {
		t.Fatalf("review on an older LibreDCM = %d %s", code, body)
	}
	code, body, _ = b.post("/i/old/exceptions/c1/resolve", url.Values{"csrf": {find(t, csrfRE, body)}, "confirm": {find(t, confirmRE, body)}, "action": {"reject"}, "approve": {"yes"}, "reason": {"duplicate"}})
	if code != 200 {
		t.Fatalf("resolve on an older LibreDCM = %d %s", code, body)
	}
	got := e.nodes["old"].Resolves()
	if len(got) != 1 || len(got[0]) != 4 {
		t.Fatalf("body = %v", got)
	}
	// The new node, with every function, is unaffected; a node that lists
	// no conflicts function says so on the page.
	_, body, _ = b.get("/i/new/")
	has(t, "new dashboard", body, `<a href="/i/new/status">Status</a>`, `<a href="/i/new/exceptions">Study Exceptions</a>`)
	e.nodes["new"].Set(func(f *fakedcm.Server) { f.Features = []string{"status"} })
	code, body, _ = b.get("/i/new/exceptions")
	if code != 200 || !strings.Contains(body, "Study Exceptions are not available for this instance") {
		t.Fatalf("exceptions without the function = %d", code)
	}
	if strings.Contains(body, "ROE^RICHARD") {
		t.Fatal("conflicts read from a node that does not list the function")
	}
}

// A connection over plain HTTP to another machine is flagged on every page
// of the instance and on the fleet page; a loopback connection is not.
// Verifies: WT-SRS-036
func TestPlainHTTPWarningOnPages(t *testing.T) {
	e := newFleetEnv(t, "plain-1", "local")
	e.users(t)
	b := e.as(t, "viewer1")
	for _, p := range []string{"/i/plain-1/", "/i/plain-1/status", "/i/plain-1/exceptions", "/i/plain-1/exceptions/c1"} {
		_, body, _ := b.get(p)
		has(t, p, body, "uses plain HTTP over the network", "the admin token and patient data are not encrypted")
	}
	for _, p := range []string{"/i/local/", "/i/local/status", "/i/local/exceptions"} {
		_, body, _ := b.get(p)
		hasNot(t, p, body, "plain HTTP")
	}
	_, body, _ := b.get("/")
	if n := strings.Count(body, `<span class="flag warn">plain HTTP</span>`); n != 1 {
		t.Fatalf("fleet flags %d instance(s) for plain HTTP, want 1", n)
	}
}

// Text that comes from LibreDCM (AE title, node ID, version, error text,
// check names, patient names, counter names) is shown as text and never
// becomes markup.
// Verifies: WT-SRS-001, WT-SRS-017
func TestTextFromLibreDCMIsNeverMarkup(t *testing.T) {
	e := newFleetEnv(t, "a@site")
	e.users(t)
	const evil = `<script>alert(1)</script><img src=x onerror=alert(2)>"'`
	e.nodes["a"].Set(func(f *fakedcm.Server) {
		f.AETitle, f.NodeID, f.Version, f.Digest = evil, "node"+evil, "v"+evil, "d"+evil
		f.Status.Checks = []dcmclient.Check{{Name: "chk" + evil, OK: false, Error: "err" + evil}}
		f.Status.License = dcmclient.License{Enforced: true, State: "st" + evil, Message: "msg" + evil}
		f.Status.Counters.CStore = map[string]uint64{"res" + evil: 1}
		f.Status.Backends.Storage, f.Status.DICOM.Listen = "sto"+evil, "lis"+evil
		f.Conflict.PatientName, f.Conflict.SourceAE, f.Conflict.StudyDescription = "pn"+evil, "ae"+evil, "sd"+evil
	})
	b := e.as(t, "admin1")
	pages := []string{"/", "/about", "/i/a/", "/i/a/status", "/i/a/exceptions", "/i/a/exceptions/c1", "/i/a/exceptions/c1/review?action=reject", "/audit"}
	for round := range 2 {
		if round == 1 { // identity-changed and error texts
			e.nodes["a"].Set(func(f *fakedcm.Server) { f.NodeID = "second" + evil })
		}
		for _, p := range pages {
			_, body, _ := b.get(p)
			hasNot(t, fmt.Sprintf("round %d %s", round, p), body, "<script>", "<img src=x", "onerror=alert(2)>")
		}
	}
	_, body, _ := b.get("/i/a/status")
	has(t, "status", body, "&lt;script&gt;alert(1)&lt;/script&gt;")
}

// Every address behind the login has one row in the role table, and the
// server refuses each one for a role below its row, whatever the page links
// show. Without a session every address leads to the login page.
// Verifies: WT-SRS-004, WT-SRS-014, WT-SRS-034
func TestRoleTable(t *testing.T) {
	e := newFleetEnv(t, "a", "b")
	e.users(t)
	want := map[string]accounts.Role{
		"GET /{$}":                               accounts.Viewer,
		"GET /about":                             accounts.Viewer,
		"GET /i/{inst}":                          accounts.Viewer,
		"GET /i/{inst}/{$}":                      accounts.Viewer,
		"GET /i/{inst}/status":                   accounts.Viewer,
		"GET /i/{inst}/exceptions":               accounts.Viewer,
		"GET /i/{inst}/exceptions/{id}":          accounts.Viewer,
		"GET /i/{inst}/exceptions/{id}/review":   accounts.Admin,
		"POST /i/{inst}/exceptions/{id}/resolve": accounts.Admin,
		"POST /i/{inst}/identity/review":         accounts.Admin,
		"POST /i/{inst}/identity/apply":          accounts.Admin,
		"GET /i/{inst}/config":                   accounts.Operator,
		"GET /i/{inst}/remote-aes":               accounts.Operator,
		"GET /i/{inst}/allow-list":               accounts.Operator,
		"GET /i/{inst}/config/edit":              accounts.Admin,
		"POST /i/{inst}/config/review":           accounts.Admin,
		"POST /i/{inst}/config/apply":            accounts.Admin,
		"POST /i/{inst}/reload/review":           accounts.Admin,
		"POST /i/{inst}/reload/apply":            accounts.Admin,
		"POST /i/{inst}/rollback/review":         accounts.Admin,
		"POST /i/{inst}/rollback/apply":          accounts.Admin,
		"GET /i/{inst}/echo":                     accounts.Operator,
		"POST /i/{inst}/echo/review":             accounts.Operator,
		"POST /i/{inst}/echo/apply":              accounts.Operator,
		"GET /exceptions":                        accounts.Viewer,
		"GET /exceptions/{rest...}":              accounts.Viewer,
		"GET /users":                             accounts.Admin,
		"POST /users":                            accounts.Admin,
		"POST /users/{id}":                       accounts.Admin,
		"GET /account":                           accounts.Viewer,
		"POST /account/password":                 accounts.Viewer,
		"GET /audit":                             accounts.Admin,
		"POST /logout":                           accounts.Viewer,
	}
	routes := e.web.routes()
	if len(routes) != len(want) {
		t.Fatalf("%d routes, the reviewed table has %d: review the role of the new address and add it here", len(routes), len(want))
	}
	fill := strings.NewReplacer("{inst}", "a", "{id}", "c1", "{rest...}", "c1", "{$}", "")
	anon := e.browser(t)
	users := map[accounts.Role]string{accounts.Viewer: "viewer1", accounts.Operator: "op1", accounts.Admin: "admin1"}
	for _, rt := range routes {
		min, ok := want[rt.pattern]
		if !ok || min != rt.min {
			t.Errorf("%s: role %q, reviewed role %q", rt.pattern, rt.min, min)
			continue
		}
		method, path, _ := strings.Cut(rt.pattern, " ")
		path = fill.Replace(path)
		do := func(b *browser) (int, string, http.Header) {
			if method == http.MethodPost {
				return b.post(path, url.Values{}) // no CSRF token: nothing is done
			}
			return b.get(path)
		}
		if code, _, h := do(anon); code != http.StatusSeeOther || h.Get("Location") != "/login" {
			t.Errorf("%s without a session = %d %s", rt.pattern, code, h.Get("Location"))
		}
		for role, user := range users {
			code, body, _ := do(e.as(t, user))
			refused := code == http.StatusForbidden && strings.Contains(body, "can&#39;t open this page")
			if wantRefused := rank(role) < rank(min); refused != wantRefused {
				t.Errorf("%s as %s: refused=%v, want %v (HTTP %d)", rt.pattern, role, refused, wantRefused, code)
			}
		}
	}
	if n := len(e.nodes["a"].Resolves()) + len(e.nodes["b"].Resolves()); n != 0 {
		t.Fatalf("%d edit(s) sent by the role walk", n)
	}
}
