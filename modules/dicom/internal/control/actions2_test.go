package control

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mulcahyio/librekey/license"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/licensing"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// ---- DCM-SRS-087: the real C-ECHO client ----

// The real C-ECHO client proposes the Verification SOP class and nothing
// else, as the given calling AE; it names the stage that failed.
// Verifies: DCM-SRS-087
func TestDICOMEchoProposesVerificationOnlyAndNamesTheStage(t *testing.T) {
	// A closed port: the connection fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stage, err := DICOMEcho(ctx, EchoTarget{Addr: closed, CalledAE: "ANY", CallingAE: "LIBREDCM"})
	if stage != StageConnect || err == nil {
		t.Fatalf("closed port: stage %q err %v, want connect", stage, err)
	}
	if text := echoErrorText(stage, err); text != "connection refused" {
		t.Fatalf("closed port reported as %q", text)
	}

	// A peer that takes the association request and hangs up: the
	// association fails. What the client sent is kept and inspected.
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		head := make([]byte, 6)
		if _, err := io.ReadFull(c, head); err != nil {
			got <- nil
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(head[2:]))
		_, _ = io.ReadFull(c, body)
		got <- append(head, body...)
	}()
	stage, err = DICOMEcho(ctx, EchoTarget{Addr: ln.Addr().String(), CalledAE: "REMOTEPACS", CallingAE: "LIBREDCM"})
	if stage != StageAssociation || err == nil {
		t.Fatalf("peer hung up: stage %q err %v, want association", stage, err)
	}
	pdu := <-got
	if len(pdu) < 74 || pdu[0] != 0x01 {
		t.Fatalf("the client did not send an A-ASSOCIATE-RQ: % x", pdu)
	}
	if called, calling := strings.TrimSpace(string(pdu[10:26])), strings.TrimSpace(string(pdu[26:42])); called != "REMOTEPACS" || calling != "LIBREDCM" {
		t.Fatalf("called %q calling %q", called, calling)
	}
	// Walk the variable items: exactly one presentation context, and its
	// abstract syntax is Verification.
	var abstract []string
	contexts := 0
	for p := 74; p+4 <= len(pdu); {
		typ, n := pdu[p], int(binary.BigEndian.Uint16(pdu[p+2:]))
		item := pdu[p+4 : min(p+4+n, len(pdu))]
		if typ == 0x20 {
			contexts++
			for q := 4; q+4 <= len(item); {
				st, sn := item[q], int(binary.BigEndian.Uint16(item[q+2:]))
				if st == 0x30 {
					abstract = append(abstract, string(item[q+4:q+4+sn]))
				}
				q += 4 + sn
			}
		}
		p += 4 + n
	}
	if contexts != 1 || len(abstract) != 1 || abstract[0] != "1.2.840.10008.1.1" {
		t.Fatalf("presentation contexts %d, abstract syntaxes %v: want only Verification", contexts, abstract)
	}
}

// ---- DCM-SRS-088: reload ----

func (a *actEnv) plan(t *testing.T) map[string]any {
	t.Helper()
	rec, body := a.post(t, "/control/v1/reload/plan", map[string]any{})
	if rec.Code != 200 {
		t.Fatalf("plan = %d %s", rec.Code, rec.Body)
	}
	return body["plan"].(map[string]any)
}

func strs(v any) []string {
	out := []string{}
	for _, x := range v.([]any) {
		switch t := x.(type) {
		case string:
			out = append(out, t)
		case map[string]any:
			out = append(out, fmt.Sprint(t["name"]))
		}
	}
	return out
}

// The plan shows the difference between the running settings and what a
// reload would load, and changes nothing. Apply needs the confirmation and
// the digest of that plan; it reports what changed, and the settings in
// force are then the new ones.
// Verifies: DCM-SRS-088
func TestReloadPlanAndApply(t *testing.T) {
	a := newActEnv(t)
	// Nothing differs yet.
	p := a.plan(t)
	if p["changed"] != false || len(p["warnings"].([]any)) != 0 {
		t.Fatalf("plan without a change = %v", p)
	}
	rec, body := a.post(t, "/control/v1/reload", act(rid(), "confirm", true, "plan_digest", p["plan_digest"]))
	if rec.Code != 200 || body["changed"] != false || body["applied"] != true {
		t.Fatalf("reload without a change = %d %v", rec.Code, body)
	}

	// The file now adds a destination and an allow-list rule.
	a.rl.next = LiveSettings{
		RemoteAEs: map[string]gateway.RemoteAE{"VIEWER1": {Host: "10.0.0.20", Port: 104, AETitle: "VIEWER1"},
			"ENVDEST": {Host: "10.0.0.30", Port: 11112, AETitle: "PACSB"}, "NEWVIEW": {Host: "10.0.0.40", Port: 104}},
		AllowedAEs: []string{"MR1", "CT1@10.0.0.5", "US1@10.0.0.0/24"},
	}
	before := a.get(t, "/control/v1")["instance"].(map[string]any)["config_digest"]
	p = a.plan(t)
	ra, al := p["remote_aes"].(map[string]any), p["allow_list"].(map[string]any)
	if p["changed"] != true || fmt.Sprint(strs(ra["added"])) != "[NEWVIEW]" || len(ra["removed"].([]any)) != 0 || len(ra["changed"].([]any)) != 0 ||
		fmt.Sprint(strs(al["added"])) != "[US1@10.0.0.0/24]" || len(al["removed"].([]any)) != 0 || len(p["warnings"].([]any)) != 0 {
		t.Fatalf("plan = %v", p)
	}
	if added := ra["added"].([]any)[0].(map[string]any); added["host"] != "10.0.0.40" || added["port"] != float64(104) || added["ae_title"] != "NEWVIEW" {
		t.Fatalf("added destination = %v (AE title defaults to the name)", added)
	}
	// The plan changed nothing, and asking twice gives the same digest.
	if a.rl.applied != 1 || len(a.live.remotes) != 2 || a.get(t, "/control/v1")["instance"].(map[string]any)["config_digest"] != before {
		t.Fatal("the plan changed the running settings")
	}
	if again := a.plan(t); again["plan_digest"] != p["plan_digest"] || !strings.HasPrefix(fmt.Sprint(p["plan_digest"]), "sha256:") {
		t.Fatalf("plan digest not stable: %v vs %v", again["plan_digest"], p["plan_digest"])
	}

	// Apply needs confirm and the digest.
	for name, c := range map[string]struct {
		fields []any
		status int
		code   string
	}{
		"not confirmed": {[]any{"plan_digest", p["plan_digest"]}, 422, "not_confirmed"},
		"confirm false": {[]any{"plan_digest", p["plan_digest"], "confirm", false}, 422, "not_confirmed"},
		"no digest":     {[]any{"confirm", true}, 422, "plan_digest_required"},
		"wrong digest":  {[]any{"confirm", true, "plan_digest", "sha256:0000"}, 409, "plan_changed"},
	} {
		rec, body := a.post(t, "/control/v1/reload", act(rid(), c.fields...))
		if rec.Code != c.status || body["code"] != c.code {
			t.Errorf("%s = %d %v, want %d %s", name, rec.Code, body, c.status, c.code)
		}
	}
	if a.rl.applied != 1 || len(a.live.remotes) != 2 || len(a.live.rules) != 2 {
		t.Fatal("a refused reload changed the running settings")
	}

	id := rid()
	rec, body = a.post(t, "/control/v1/reload", act(id, "confirm", true, "plan_digest", p["plan_digest"]))
	if rec.Code != 200 || body["changed"] != true || body["applied"] != true || body["config_digest_before"] != before ||
		body["config_digest_after"] == before {
		t.Fatalf("reload = %d %v", rec.Code, body)
	}
	if len(a.live.remotes) != 3 || len(a.live.rules) != 3 || a.rl.applied != 2 {
		t.Fatalf("after reload: remotes %v rules %v", a.live.remotes, a.live.rules)
	}
	if live := a.get(t, "/control/v1/remote-aes"); len(live["remote_aes"].([]any)) != 3 {
		t.Fatalf("remote-aes after reload = %v", live)
	}
	// The applied plan is now stale: nothing differs any more.
	if p2 := a.plan(t); p2["changed"] != false || p2["plan_digest"] == p["plan_digest"] {
		t.Fatalf("plan after apply = %v", p2)
	}
	// Audit: one configuration-changed result record with what changed and both digests.
	recs := findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditConfigChange, "phase": "result", "request_id": id})
	if len(recs) != 1 || recs[0]["outcome"] != "success" || recs[0]["changed"] != true || recs[0]["digest_before"] != before ||
		recs[0]["digest_after"] != body["config_digest_after"] || fmt.Sprint(recs[0]["remote_aes_added"]) != "[NEWVIEW]" ||
		fmt.Sprint(recs[0]["allow_list_added"]) != "[US1@10.0.0.0/24]" || recs[0]["operator"] != "Pat Admin" {
		t.Fatalf("configuration-changed record = %v", recs)
	}
	// The refusals were recorded too.
	for _, code := range []string{"not_confirmed", "plan_digest_required", "plan_changed"} {
		if len(findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditRefused, "code": code})) == 0 {
			t.Errorf("no audit record for the refusal %s", code)
		}
	}
}

// When the running settings or the file change after the plan was made,
// apply is refused (409) and nothing changes.
// Verifies: DCM-SRS-088
func TestReloadRefusesAStalePlan(t *testing.T) {
	a := newActEnv(t)
	a.rl.next.AllowedAEs = []string{"MR1", "CT1@10.0.0.5", "US1"}
	p := a.plan(t)

	// The file is edited again after the person looked at the plan.
	a.rl.next.AllowedAEs = []string{"MR1", "CT1@10.0.0.5", "US1", "INTRUDER"}
	rec, body := a.post(t, "/control/v1/reload", act(rid(), "confirm", true, "plan_digest", p["plan_digest"]))
	if rec.Code != http.StatusConflict || body["code"] != "plan_changed" || len(a.live.rules) != 2 {
		t.Fatalf("file changed after the plan = %d %v, rules %v", rec.Code, body, a.live.rules)
	}
	// The running settings change (a SIGHUP by someone else) after the plan.
	a.rl.next.AllowedAEs = []string{"MR1", "CT1@10.0.0.5", "US1"}
	p = a.plan(t)
	a.live.rules = []string{"MR1"}
	rec, body = a.post(t, "/control/v1/reload", act(rid(), "confirm", true, "plan_digest", p["plan_digest"]))
	if rec.Code != http.StatusConflict || body["code"] != "plan_changed" || len(a.live.rules) != 1 {
		t.Fatalf("running settings changed after the plan = %d %v, rules %v", rec.Code, body, a.live.rules)
	}
	if a.rl.applied != 0 {
		t.Fatal("a stale plan was applied")
	}
}

// A configuration that fails to load or validate is reported (422) by plan
// and by apply, and the running settings stay as they are.
// Verifies: DCM-SRS-088
func TestReloadWithBadConfigKeepsRunningSettings(t *testing.T) {
	a := newActEnv(t)
	p := a.plan(t)
	a.rl.next.AllowedAEs = []string{"ATTACKER"}
	a.rl.err = errors.New(`allowed AE "CT1@not-an-ip": "not-an-ip" is not an IP address or CIDR`)
	rec, body := a.post(t, "/control/v1/reload/plan", map[string]any{})
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "invalid_config" || !strings.Contains(fmt.Sprint(body["error"]), "not an IP address") {
		t.Fatalf("plan with a bad config = %d %v", rec.Code, body)
	}
	id := rid()
	rec, body = a.post(t, "/control/v1/reload", act(id, "confirm", true, "plan_digest", p["plan_digest"]))
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "invalid_config" ||
		!strings.Contains(fmt.Sprint(body["error"]), "running settings are unchanged") || !strings.Contains(fmt.Sprint(body["error"]), "not an IP address") {
		t.Fatalf("reload with a bad config = %d %v", rec.Code, body)
	}
	if a.rl.applied != 0 || fmt.Sprint(a.live.rules) != "[MR1 CT1@10.0.0.5]" || len(a.live.remotes) != 2 {
		t.Fatalf("running settings changed: %v %v", a.live.rules, a.live.remotes)
	}
	recs := findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditConfigChange, "phase": "result", "request_id": id})
	if len(recs) != 1 || recs[0]["outcome"] != "minor-failure" || recs[0]["code"] != "invalid_config" {
		t.Fatalf("failed reload record = %v", recs)
	}
}

// The licence acknowledgement block applies to the control API as it does
// to SIGHUP: plan and reload answer 423 and nothing changes.
// Verifies: DCM-SRS-088, DCM-SRS-060
func TestReloadBlockedUntilLicenceAcknowledged(t *testing.T) {
	a := newActEnv(t)
	p := a.plan(t)
	a.rl.next.AllowedAEs = []string{"MR1"}
	a.rl.err = fmt.Errorf("%w: LibreDCM is unlicensed; an administrator must acknowledge first", ErrReloadBlocked)
	rec, body := a.post(t, "/control/v1/reload/plan", map[string]any{})
	if rec.Code != http.StatusLocked || body["code"] != "license_ack_required" {
		t.Fatalf("plan while blocked = %d %v", rec.Code, body)
	}
	rec, body = a.post(t, "/control/v1/reload", act(rid(), "confirm", true, "plan_digest", p["plan_digest"]))
	if rec.Code != http.StatusLocked || body["code"] != "license_ack_required" || !strings.Contains(fmt.Sprint(body["error"]), "acknowledge") {
		t.Fatalf("reload while blocked = %d %v", rec.Code, body)
	}
	if a.rl.applied != 0 || len(a.live.rules) != 2 {
		t.Fatal("a blocked reload changed the running settings")
	}
}

// A reload that weakens a risk control carries a warning, and apply is
// refused until the request acknowledges each warning by its code.
// Verifies: DCM-SRS-088, DCM-SRS-095
func TestReloadWarningsMustBeAcknowledged(t *testing.T) {
	a := newActEnv(t)
	// The file empties the allow-list, removes ENVDEST and points VIEWER1 elsewhere.
	a.rl.next = LiveSettings{RemoteAEs: map[string]gateway.RemoteAE{"VIEWER1": {Host: "203.0.113.9", Port: 104, AETitle: "VIEWER1"}}}
	p := a.plan(t)
	var codes []string
	for _, w := range p["warnings"].([]any) {
		m := w.(map[string]any)
		if m["must_acknowledge"] != true || m["message"] == "" {
			t.Errorf("warning = %v", m)
		}
		codes = append(codes, m["code"].(string))
	}
	sort.Strings(codes)
	if fmt.Sprint(codes) != "[allow_list_emptied remote_ae_removed remote_ae_retargeted]" {
		t.Fatalf("warnings = %v", codes)
	}
	ra := p["remote_aes"].(map[string]any)
	ch := ra["changed"].([]any)[0].(map[string]any)
	if fmt.Sprint(strs(ra["removed"])) != "[ENVDEST]" || ch["from"].(map[string]any)["host"] != "10.0.0.20" || ch["to"].(map[string]any)["host"] != "203.0.113.9" {
		t.Fatalf("plan = %v", ra)
	}
	al := p["allow_list"].(map[string]any)
	if al["enforced_before"] != true || al["enforced_after"] != false || len(al["removed"].([]any)) != 2 {
		t.Fatalf("allow-list plan = %v", al)
	}

	for name, ack := range map[string][]string{
		"none":         nil,
		"one of three": {WarnAllowListEmptied},
		"two of three": {WarnAllowListEmptied, WarnRemoteAERemoved},
		"wrong codes":  {"everything", "*", "ALLOW_LIST_EMPTIED"},
	} {
		rec, body := a.post(t, "/control/v1/reload", act(rid(), "confirm", true, "plan_digest", p["plan_digest"], "acknowledge_warnings", ack))
		if rec.Code != http.StatusUnprocessableEntity || body["code"] != "warning_not_acknowledged" {
			t.Errorf("acknowledged %s = %d %v", name, rec.Code, body)
		}
	}
	if a.rl.applied != 0 || !a.live.enforced || len(a.live.rules) != 2 || a.live.remotes["VIEWER1"].Host != "10.0.0.20" {
		t.Fatal("an unacknowledged weakening was applied")
	}
	id := rid()
	rec, body := a.post(t, "/control/v1/reload", act(id, "confirm", true, "plan_digest", p["plan_digest"],
		"acknowledge_warnings", []string{WarnRemoteAERetargeted, WarnAllowListEmptied, WarnRemoteAERemoved}))
	if rec.Code != 200 || body["changed"] != true || a.live.enforced || a.live.remotes["VIEWER1"].Host != "203.0.113.9" {
		t.Fatalf("acknowledged reload = %d %v", rec.Code, body)
	}
	recs := findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditConfigChange, "phase": "result", "request_id": id})
	if len(recs) != 1 || fmt.Sprint(recs[0]["acknowledged_warnings"]) != "[allow_list_emptied remote_ae_removed remote_ae_retargeted]" ||
		recs[0]["allow_list_enforced"] != false {
		t.Fatalf("audit record = %v", recs)
	}
}

// ---- DCM-SRS-089: licence ----

// Licence status shows state, machine fingerprint, customer and the last
// four characters of the key; a source build says no licence is needed and
// refuses the licence actions.
// Verifies: DCM-SRS-089
func TestLicenceStatusAndSourceBuild(t *testing.T) {
	a := newActEnv(t)
	body := a.get(t, "/control/v1/license")
	lic := body["license"].(map[string]any)
	if lic["enforced"] != true || lic["state"] != "expired" || lic["ack_required"] != true || lic["key_last4"] != "0001" ||
		lic["machine_fingerprint"] != "fp1-aaaaaaaaaaaa-bbbbbbbbbbbb-cccccccccccc" || lic["customer"] != "Synthetic Clinic" {
		t.Fatalf("license = %v", lic)
	}

	a.api.Licence = nil // source build
	body = a.get(t, "/control/v1/license")
	if lic := body["license"].(map[string]any); lic["enforced"] != false {
		t.Fatalf("source build license = %v", lic)
	}
	for _, p := range []string{"ack", "request", "install", "activate"} {
		m := act(rid(), "key", "LK-SYNTH-0000-0001", "license", "x") // gitleaks:allow (synthetic test value)
		if p == "ack" {
			delete(m, "key")
			delete(m, "license")
		} else if p == "install" {
			delete(m, "key")
		} else {
			delete(m, "license")
		}
		rec, b := a.post(t, "/control/v1/license/"+p, m)
		if rec.Code != http.StatusUnprocessableEntity || b["code"] != "license_not_enforced" {
			t.Errorf("source build: license/%s = %d %v", p, rec.Code, b)
		}
	}
}

// Acknowledgement is recorded under the operator of the request; request,
// install and activate call the licensing code with what was sent. The
// licence key is never in a response (beyond its last four characters), in
// an audit record or in a log line, and the request text is not kept.
// Verifies: DCM-SRS-089
func TestLicenceActionsAndKeySecrecy(t *testing.T) {
	a := newActEnv(t)
	const key = "LK-SYNTH-KEY7-QX4Z-9911" // gitleaks:allow (synthetic test value)
	const file = "-----BEGIN LIBREKEY LICENSE-----\nc3ludGhldGljLWxpY2VuY2UtZmlsZQ\n-----END LIBREKEY LICENSE-----"
	var responses strings.Builder
	post := func(path string, m map[string]any) (*httptest.ResponseRecorder, map[string]any) {
		rec, body := a.post(t, path, m)
		responses.WriteString(rec.Body.String())
		return rec, body
	}

	rec, body := post("/control/v1/license/ack", act(rid(), "operator", "Dana Admin"))
	if rec.Code != 200 || body["acknowledged"] != true || fmt.Sprint(a.lic.acks) != "[Dana Admin]" ||
		body["license"].(map[string]any)["ack_required"] != false {
		t.Fatalf("ack = %d %v (acks %v)", rec.Code, body, a.lic.acks)
	}

	reqID := rid()
	reqBody := act(reqID, "key", key, "site", "Main site")
	rec, body = post("/control/v1/license/request", reqBody)
	if rec.Code != 200 || !strings.Contains(fmt.Sprint(body["request"]), "BEGIN LIBREKEY REQUEST") || body["key_last4"] != "9911" ||
		len(a.lic.requests) != 1 || a.lic.requests[0] != [2]string{key, "Main site"} {
		t.Fatalf("request = %d %v", rec.Code, body)
	}
	// The request text holds the key, so it is not remembered: no entry stays.
	a.api.requests.mu.Lock()
	_, kept := a.api.requests.m[reqID]
	a.api.requests.mu.Unlock()
	if kept {
		t.Fatal("the activation request text was kept in memory for replay")
	}
	if rec, _ := post("/control/v1/license/request", reqBody); rec.Header().Get("Idempotent-Replay") != "" || len(a.lic.requests) != 2 {
		t.Fatal("license/request was replayed from memory")
	}
	// Request without a key is allowed (the portal can assign one).
	if rec, _ := post("/control/v1/license/request", act(rid())); rec.Code != 200 {
		t.Fatalf("request without key = %d", rec.Code)
	}

	rec, body = post("/control/v1/license/install", act(rid(), "license", file))
	if rec.Code != 200 || body["installed"] != true || len(a.lic.installs) != 1 || a.lic.installs[0] != file {
		t.Fatalf("install = %d %v", rec.Code, body)
	}
	rec, body = post("/control/v1/license/activate", act(rid(), "key", key, "site", "Main site"))
	if rec.Code != 200 || body["activated"] != true || len(a.lic.activates) != 1 || a.lic.activates[0] != [2]string{key, "Main site"} {
		t.Fatalf("activate = %d %v", rec.Code, body)
	}

	// Refusals.
	for name, c := range map[string]struct {
		path   string
		fields []any
		code   string
	}{
		"activate without key": {"activate", nil, "license_key_required"},
		"key with a newline":   {"activate", []any{"key", "LK-1\nLK-2"}, "license_key_invalid"},
		"very long key":        {"request", []any{"key", strings.Repeat("K", 200)}, "license_key_invalid"},
		"very long site":       {"request", []any{"site", strings.Repeat("s", 300)}, "license_site_invalid"},
		"install without text": {"install", []any{"license", "  \n "}, "license_required"},
	} {
		rec, body := post("/control/v1/license/"+c.path, act(rid(), c.fields...))
		if rec.Code != http.StatusUnprocessableEntity || body["code"] != c.code {
			t.Errorf("%s = %d %v, want 422 %s", name, rec.Code, body, c.code)
		}
	}
	// A failure whose error text repeats the key (as a server might).
	a.lic.err = fmt.Errorf("key %s is not known to the server", key)
	rec, body = post("/control/v1/license/activate", act(rid(), "key", key))
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "activation_failed" || !strings.Contains(fmt.Sprint(body["error"]), "installed licence is unchanged") {
		t.Fatalf("failed activation = %d %v", rec.Code, body)
	}
	a.lic.err = errors.New("license: signature is not valid")
	rec, body = post("/control/v1/license/install", act(rid(), "license", file))
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "license_invalid" || !strings.Contains(fmt.Sprint(body["error"]), "signature is not valid") {
		t.Fatalf("invalid licence = %d %v", rec.Code, body)
	}

	// The key and the licence file: nowhere.
	if strings.Contains(responses.String(), key) || strings.Contains(responses.String(), "c3ludGhldGljLWxpY2VuY2UtZmlsZQ") {
		t.Error("a response holds the licence key or the licence file")
	}
	recs := auditRecs(t, a.rt.Audit)
	logs, _ := a.rt.Logs.Select(10000, nil)
	var all strings.Builder
	for _, r := range recs {
		raw, _ := json.Marshal(r)
		all.Write(raw)
	}
	for _, l := range logs {
		all.Write(l.Line)
	}
	if strings.Contains(all.String(), key) || strings.Contains(all.String(), "KEY7") || strings.Contains(all.String(), "c3ludGhldGljLWxpY2VuY2UtZmlsZQ") {
		t.Error("an audit record or log line holds the licence key or the licence file")
	}
	// Every licence action is audited with what it was and the state before and after.
	for action, n := range map[string]int{"ack": 1, "request": 3, "install": 2, "activate": 2} {
		got := findAudit(recs, map[string]any{"event": auditLicense, "phase": "result", "license_action": action})
		if len(got) != n {
			t.Errorf("license-action records for %s = %d, want %d", action, len(got), n)
		}
		for _, r := range got {
			if r["state_before"] == nil || r["operator"] == nil || r["reason"] == nil {
				t.Errorf("license-action record = %v", r)
			}
		}
	}
	if got := findAudit(recs, map[string]any{"license_action": "activate", "outcome": "success"}); len(got) != 1 || got[0]["key_last4"] != "0001" {
		t.Errorf("activate record = %v", got)
	}
}

// One online activation at a time; a second is refused as busy.
// Verifies: DCM-SRS-096, DCM-SRS-089
func TestLicenceActivateOneAtATime(t *testing.T) {
	a := newActEnv(t)
	a.lic.block = make(chan struct{})
	done := make(chan int)
	go func() {
		rec, _ := a.post(t, "/control/v1/license/activate", act(rid2(), "key", "LK-SYNTH-0000-0001")) // gitleaks:allow (synthetic test value)
		done <- rec.Code
	}()
	// The start record is written once the first activation holds the slot.
	waitFor(t, func() bool {
		return len(findAudit(auditRecs(t, a.rt.Audit), map[string]any{"phase": "start", "endpoint": "/control/v1/license/activate"})) == 1
	})
	rec, body := a.post(t, "/control/v1/license/activate", act(rid2(), "key", "LK-SYNTH-0000-0002")) // gitleaks:allow (synthetic test value)
	if rec.Code != http.StatusTooManyRequests || body["code"] != "busy" {
		t.Fatalf("second activation = %d %v", rec.Code, body)
	}
	close(a.lic.block)
	if code := <-done; code != 200 || len(a.lic.activates) != 1 {
		t.Fatalf("first activation = %d, %d activations", code, len(a.lic.activates))
	}
}

// realLicensing builds the real licensing code around a test key pair, with
// a stand-in for the LibreKey server.
type realLic struct {
	l      *licensing.Licensing
	signer *license.Signer
	fp     string
	file   string
	server *httptest.Server
	answer func(w http.ResponseWriter)
	logs   *syncBuf
}

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func newRealLic(t *testing.T) *realLic {
	t.Helper()
	t.Setenv("LIBREKEY_HOST_ID", "control-test-host")
	pub, priv, err := license.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	r := &realLic{signer: license.NewSigner(priv), logs: &syncBuf{}}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if r.answer != nil {
			r.answer(w)
			return
		}
		http.Error(w, `{"error":"unknown license key"}`, http.StatusForbidden)
	}))
	t.Cleanup(r.server.Close)
	dir := t.TempDir()
	r.file = filepath.Join(dir, "license.lic")
	r.l, err = licensing.NewWithKeys(license.MarshalPublicKey(pub),
		licensing.Config{File: r.file, DataDir: filepath.Join(dir, "install"), Server: r.server.URL},
		"0.3.0-test", time.Time{}, slog.New(slog.NewJSONHandler(r.logs, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.fp = r.l.Detail().Fingerprint
	return r
}

const realKey = "LK-SYNTH-REAL-7Q2W-4455" // gitleaks:allow (synthetic test value)

func (r *realLic) sign(t *testing.T, edit func(*license.License)) string {
	t.Helper()
	exp := time.Now().Add(200 * 24 * time.Hour)
	l := license.License{ID: "lic-synth-1", Key: realKey, Product: licensing.Product, Customer: "Synthetic Clinic",
		Term: license.TermAnnual, Expires: &exp, GraceDays: 30, Fingerprint: r.fp, Issued: time.Now()}
	if edit != nil {
		edit(&l)
	}
	text, err := r.signer.Sign(l)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// Through the real licensing code: a valid licence file is installed; a
// licence that is damaged, signed by another key, for another machine, for
// another product or long expired is refused with 422 and the installed
// licence file stays byte for byte; online activation installs what the
// server returns and a server refusal changes nothing; the acknowledgement
// carries the operator's name. The key is shown as its last four characters.
// Verifies: DCM-SRS-089
func TestLicenceEndpointsWithRealLicensing(t *testing.T) {
	a := newActEnv(t)
	r := newRealLic(t)
	a.api.Licence = r.l
	lic := func() map[string]any { return a.get(t, "/control/v1/license")["license"].(map[string]any) }

	if l := lic(); l["state"] != "missing" || l["ack_required"] != true || l["licensed"] != false || l["machine_fingerprint"] != r.fp {
		t.Fatalf("status before = %v", l)
	}
	// Acknowledge: recorded under the operator, lifts the block.
	rec, body := a.post(t, "/control/v1/license/ack", act(rid(), "operator", "Dana Admin"))
	if rec.Code != 200 {
		t.Fatalf("ack = %d %v", rec.Code, body)
	}
	ack, _ := lic()["acknowledgement"].(map[string]any)
	if ack["by"] != "Dana Admin" || ack["valid"] != true || lic()["ack_required"] != false {
		t.Fatalf("acknowledgement = %v", ack)
	}
	if ok, _ := r.l.ConfigChangesAllowed(); !ok {
		t.Fatal("the acknowledgement did not lift the reload block")
	}
	if !strings.Contains(r.logs.String(), `"event":"license_acknowledged"`) || !strings.Contains(r.logs.String(), `"by":"Dana Admin"`) {
		t.Fatal("the licensing code did not write its acknowledgement audit record at once")
	}

	// Activation request: decodes to this machine and product.
	rec, body = a.post(t, "/control/v1/license/request", act(rid(), "key", realKey, "site", "Main site"))
	if rec.Code != 200 || !strings.Contains(fmt.Sprint(body["request"]), "LIBREKEY") || strings.Contains(rec.Body.String(), realKey) {
		t.Fatalf("request = %d %s", rec.Code, rec.Body)
	}

	// Install a valid licence.
	valid := r.sign(t, nil)
	rec, body = a.post(t, "/control/v1/license/install", act(rid(), "license", valid))
	if rec.Code != 200 || body["installed"] != true {
		t.Fatalf("install = %d %v", rec.Code, body)
	}
	l := lic()
	if l["state"] != "valid" || l["licensed"] != true || l["key_last4"] != "4455" || l["customer"] != "Synthetic Clinic" || l["term"] != "annual" {
		t.Fatalf("status after install = %v", l)
	}
	installed, err := os.ReadFile(r.file)
	if err != nil || string(installed) != valid {
		t.Fatalf("licence file on disk: %v", err)
	}
	if raw, _ := json.Marshal(a.get(t, "/control/v1/license")); strings.Contains(string(raw), realKey) || strings.Contains(string(raw), valid[40:80]) {
		t.Fatal("licence status holds the key or the licence file")
	}

	// Invalid licences: each refused, the installed one stays.
	other, otherPriv, _ := license.GenerateKey()
	_ = other
	otherSigned, err := license.NewSigner(otherPriv).Sign(license.License{ID: "x", Key: "LK-OTHER", Product: licensing.Product, // gitleaks:allow (synthetic test value)
		Customer: "Forger", Term: license.TermPerpetual, Fingerprint: r.fp})
	if err != nil {
		t.Fatal(err)
	}
	longAgo := time.Now().Add(-400 * 24 * time.Hour)
	tampered := strings.Replace(valid, valid[60:64], "AAAA", 1)
	if tampered == valid {
		tampered = strings.Replace(valid, valid[60:64], "BBBB", 1)
	}
	for name, text := range map[string]string{
		"garbage":           "this is not a licence",
		"empty armor":       "-----BEGIN LIBREKEY LICENSE-----\n-----END LIBREKEY LICENSE-----",
		"tampered":          tampered,
		"unknown signer":    otherSigned,
		"another machine":   r.sign(t, func(l *license.License) { l.Fingerprint = "fp1-111111111111-222222222222-333333333333" }),
		"another product":   r.sign(t, func(l *license.License) { l.Product = "librewt" }),
		"expired, no grace": r.sign(t, func(l *license.License) { l.Expires = &longAgo }),
	} {
		id := rid()
		rec, body := a.post(t, "/control/v1/license/install", act(id, "license", text))
		if rec.Code != http.StatusUnprocessableEntity || body["code"] != "license_invalid" {
			t.Errorf("%s licence = %d %v, want 422 license_invalid", name, rec.Code, body)
		}
		now, err := os.ReadFile(r.file)
		if err != nil || string(now) != string(installed) {
			t.Errorf("%s licence: the installed licence file changed", name)
		}
		if l := lic(); l["state"] != "valid" || l["key_last4"] != "4455" {
			t.Errorf("%s licence: status is now %v", name, l)
		}
		got := findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditLicense, "phase": "result", "request_id": id})
		if len(got) != 1 || got[0]["outcome"] != "minor-failure" || got[0]["state_after"] != "valid" {
			t.Errorf("%s licence: audit record %v", name, got)
		}
	}

	// Online activation: the server refuses; nothing changes.
	rec, body = a.post(t, "/control/v1/license/activate", act(rid(), "key", realKey))
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "activation_failed" || !strings.Contains(fmt.Sprint(body["error"]), "unknown license key") {
		t.Fatalf("refused activation = %d %v", rec.Code, body)
	}
	// The server returns a licence for another machine: refused, nothing changes.
	wrong := r.sign(t, func(l *license.License) { l.Fingerprint = "fp1-111111111111-222222222222-333333333333" })
	r.answer = func(w http.ResponseWriter) { _ = json.NewEncoder(w).Encode(map[string]string{"license": wrong}) }
	rec, body = a.post(t, "/control/v1/license/activate", act(rid(), "key", realKey))
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "activation_failed" {
		t.Fatalf("activation with a wrong-machine licence = %d %v", rec.Code, body)
	}
	if now, _ := os.ReadFile(r.file); string(now) != string(installed) {
		t.Fatal("a failed activation changed the installed licence")
	}
	// The server returns a good, newer licence: installed.
	renewed := r.sign(t, func(l *license.License) { l.Customer = "Synthetic Clinic Renewed" })
	r.answer = func(w http.ResponseWriter) { _ = json.NewEncoder(w).Encode(map[string]string{"license": renewed}) }
	rec, body = a.post(t, "/control/v1/license/activate", act(rid(), "key", realKey, "site", "Main site"))
	if rec.Code != 200 || body["activated"] != true || lic()["customer"] != "Synthetic Clinic Renewed" {
		t.Fatalf("activation = %d %v", rec.Code, body)
	}
	if now, _ := os.ReadFile(r.file); string(now) != renewed {
		t.Fatal("the activated licence is not on disk")
	}
	for _, rec := range auditRecs(t, a.rt.Audit) {
		if raw, _ := json.Marshal(rec); strings.Contains(string(raw), realKey) {
			t.Fatalf("an audit record holds the licence key: %s", raw)
		}
	}
	if strings.Contains(r.logs.String(), realKey) {
		t.Fatal("the licensing log holds the licence key")
	}
}

// ---- DCM-SRS-090: backup ----

var reSnapshot = regexp.MustCompile(`^index-dcm-1-\d{8}T\d{6}Z\.db$`)

// A backup writes a consistent snapshot of the index into the configured
// directory, under a name LibreDCM makes, and reports name, size and
// SHA-256. The snapshot opens as an index and holds the indexed data.
// Verifies: DCM-SRS-090
func TestBackupAction(t *testing.T) {
	a := newActEnv(t)
	id := rid()
	rec, body := a.post(t, "/control/v1/backup", act(id))
	if rec.Code != 200 {
		t.Fatalf("backup = %d %s", rec.Code, rec.Body)
	}
	name, _ := body["file"].(string)
	if !reSnapshot.MatchString(name) || strings.ContainsAny(name, `/\`) {
		t.Fatalf("file name = %q", name)
	}
	if files := dirNames(t, a.dir); len(files) != 1 || files[0] != name {
		t.Fatalf("backup directory = %v", files)
	}
	path := filepath.Join(a.dir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if body["bytes"] != float64(len(raw)) || body["sha256"] != hex.EncodeToString(sum[:]) || len(raw) == 0 {
		t.Fatalf("reported %v bytes sha256 %v; file has %d bytes sha256 %x", body["bytes"], body["sha256"], len(raw), sum)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("snapshot mode = %v, want 0600 (it holds patient data)", st.Mode().Perm())
	}
	if !strings.Contains(fmt.Sprint(body["note"]), "Images are not included") {
		t.Errorf("the response does not say that images are not included: %v", body["note"])
	}
	// The snapshot is a usable index with the data. A copy is opened, so
	// the test leaves the backup directory as the node wrote it.
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := index.Open(context.Background(), "sqlite", "file:"+copyPath)
	if err != nil {
		t.Fatalf("snapshot does not open as an index: %v", err)
	}
	defer snap.Close()
	if dem, ok, err := snap.StudyDemographics(context.Background(), "S1"); err != nil || !ok || dem.PatientID != "P1" {
		t.Fatalf("snapshot content: %+v %v %v", dem, ok, err)
	}
	if n, err := snap.CountOpenConflicts(context.Background()); err != nil || n != 1 {
		t.Fatalf("snapshot conflicts = %d %v", n, err)
	}
	// Audit record with name, size and checksum.
	recs := findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditBackup, "phase": "result", "request_id": id})
	if len(recs) != 1 || recs[0]["outcome"] != "success" || recs[0]["file"] != name || recs[0]["sha256"] != body["sha256"] ||
		recs[0]["bytes"] != body["bytes"] || recs[0]["operator"] != "Pat Admin" {
		t.Fatalf("backup audit record = %v", recs)
	}
	// The same request again: the first result, no second file.
	again, _ := a.post(t, "/control/v1/backup", act(id))
	if again.Body.String() != rec.Body.String() || len(dirNames(t, a.dir)) != 1 {
		t.Fatalf("a repeated backup request made another snapshot: %d %s vs %s; %v", again.Code, again.Body, rec.Body, dirNames(t, a.dir))
	}

	// The list shows it.
	list := a.get(t, "/control/v1/backups")
	files := list["backups"].([]any)
	if list["configured"] != true || len(files) != 1 || files[0].(map[string]any)["file"] != name ||
		files[0].(map[string]any)["bytes"] != float64(len(raw)) {
		t.Fatalf("backups = %v", list)
	}
}

// The client cannot choose where a backup goes: a request with any path,
// file or directory field is refused and nothing is written anywhere; a
// node ID with path elements cannot leave the directory.
// Verifies: DCM-SRS-090
func TestBackupAcceptsNoPathFromTheClient(t *testing.T) {
	a := newActEnv(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "stolen.db")
	for _, field := range []string{"path", "file", "dir", "directory", "dest", "destination", "name", "filename", "backup_dir", "to", "output"} {
		for _, value := range []string{target, "../../../../" + target, "..", "/etc/cron.d/x", `C:\Windows\x`, "index-dcm-1-20261114T150405Z.db"} {
			rec, body := a.post(t, "/control/v1/backup", act(rid(), field, value))
			if rec.Code != http.StatusBadRequest || body["code"] != "invalid_body" {
				t.Fatalf("backup with %s=%q = %d %v, want 400", field, value, rec.Code, body)
			}
		}
	}
	// Query string and headers are not a way in either.
	req := httptest.NewRequest("POST", "/control/v1/backup?path="+target+"&dir="+outside, strings.NewReader(`{"operator":"a","reason":"b","request_id":"path-test-0123456789"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Backup-Path", target)
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("backup = %d %s", rec.Code, rec.Body)
	}
	if got := dirNames(t, outside); len(got) != 0 {
		t.Fatalf("something was written outside the backup directory: %v", got)
	}
	if got := dirNames(t, a.dir); len(got) != 1 || !reSnapshot.MatchString(got[0]) {
		t.Fatalf("backup directory = %v", got)
	}

	// A hostile node ID.
	for _, nodeID := range []string{"../../../evil", "/abs/olute", `..\..\evil`, "a/b", "..", ".", "", "node\x00x", strings.Repeat("n", 300)} {
		b := newActEnv(t)
		b.api.Node.ID = nodeID
		parent := t.TempDir()
		b.api.Backup.Dir = filepath.Join(parent, "backups")
		rec, body := b.post(t, "/control/v1/backup", act(rid()))
		if rec.Code != 200 {
			t.Fatalf("node id %q: backup = %d %v", nodeID, rec.Code, body)
		}
		name := body["file"].(string)
		if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || !reBackupName.MatchString(name) || len(name) > 100 {
			t.Errorf("node id %q gave the file name %q", nodeID, name)
		}
		if got := dirNames(t, parent); len(got) != 1 || got[0] != "backups" {
			t.Errorf("node id %q: wrote outside the backup directory: %v", nodeID, got)
		}
		if got := dirNames(t, b.api.Backup.Dir); len(got) != 1 || got[0] != name {
			t.Errorf("node id %q: backup directory = %v", nodeID, got)
		}
	}
}

// A backup never replaces an existing file, also not through a symbolic
// link; it is refused when no directory is configured, when the index is
// not SQLite, when free space is too low or cannot be read, and while
// another backup runs. A failed snapshot leaves nothing behind.
// Verifies: DCM-SRS-090, DCM-SRS-096
func TestBackupRefusals(t *testing.T) {
	a := newActEnv(t)
	fixed := time.Date(2026, 11, 14, 15, 4, 5, 0, time.UTC)
	a.api.Backup.Now = func() time.Time { return fixed }
	name := "index-dcm-1-20261114T150405Z.db"

	// An existing file with the name this second would give.
	if err := os.WriteFile(filepath.Join(a.dir, name), []byte("earlier snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, body := a.post(t, "/control/v1/backup", act(rid()))
	if rec.Code != http.StatusConflict || body["code"] != "backup_exists" {
		t.Fatalf("existing file = %d %v", rec.Code, body)
	}
	if raw, _ := os.ReadFile(filepath.Join(a.dir, name)); string(raw) != "earlier snapshot" {
		t.Fatal("an existing backup file was replaced")
	}
	_ = os.Remove(filepath.Join(a.dir, name))

	// A symbolic link with that name, pointing at a file outside.
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(a.dir, name)); err != nil {
		t.Fatal(err)
	}
	rec, body = a.post(t, "/control/v1/backup", act(rid()))
	if rec.Code != http.StatusConflict || body["code"] != "backup_exists" {
		t.Fatalf("symlink = %d %v", rec.Code, body)
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "do not touch" {
		t.Fatal("the backup wrote through a symbolic link")
	}
	// A dangling link is refused as well.
	_ = os.Remove(filepath.Join(a.dir, name))
	dangling := filepath.Join(t.TempDir(), "not-there.db")
	if err := os.Symlink(dangling, filepath.Join(a.dir, name)); err != nil {
		t.Fatal(err)
	}
	if rec, body := a.post(t, "/control/v1/backup", act(rid())); rec.Code != http.StatusConflict {
		t.Fatalf("dangling symlink = %d %v", rec.Code, body)
	}
	if _, err := os.Lstat(dangling); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the backup created the target of a dangling link")
	}
	_ = os.Remove(filepath.Join(a.dir, name))

	check := func(what string, wantStatus int, wantCode string) {
		t.Helper()
		rec, body := a.post(t, "/control/v1/backup", act(rid()))
		if rec.Code != wantStatus || body["code"] != wantCode {
			t.Errorf("%s = %d %v, want %d %s", what, rec.Code, body, wantStatus, wantCode)
		}
		if got := dirNames(t, a.dir); len(got) != 0 {
			t.Errorf("%s: left %v in the backup directory", what, got)
		}
	}
	good := *a.api.Backup

	// Not configured.
	a.api.Backup.Dir = ""
	check("no directory", 422, "backup_not_configured")
	if list := a.get(t, "/control/v1/backups"); list["configured"] != false || len(list["backups"].([]any)) != 0 {
		t.Errorf("backups without a directory = %v", list)
	}
	a.api.Backup = nil
	check("no backup settings", 422, "backup_not_configured")
	a.api.Backup = &good

	// PostgreSQL index.
	pg := good
	pg.Driver = "postgres"
	a.api.Backup = &pg
	check("postgres", 422, "backup_unsupported")

	// Free space: the index is 1000 bytes, so 1100 are needed.
	space := good
	space.IndexBytes = func() (int64, error) { return 1000, nil }
	space.FreeBytes = func(dir string) (uint64, error) {
		if dir != a.dir {
			t.Errorf("free space asked for %q, want the backup directory", dir)
		}
		return 1099, nil
	}
	a.api.Backup = &space
	check("1099 bytes free, 1100 needed", 422, "insufficient_space")
	space.FreeBytes = func(string) (uint64, error) { return 0, errors.New("statfs failed") }
	check("free space unreadable", 422, "space_check_failed")
	space.FreeBytes = func(string) (uint64, error) { return 1 << 40, nil }
	space.IndexBytes = func() (int64, error) { return 0, errors.New("stat failed") }
	check("index size unreadable", 422, "space_check_failed")
	// Exactly enough is enough.
	space.IndexBytes = func() (int64, error) { return 1000, nil }
	space.FreeBytes = func(string) (uint64, error) { return 1100, nil }
	if rec, body := a.post(t, "/control/v1/backup", act(rid())); rec.Code != 200 {
		t.Fatalf("1100 bytes free = %d %v", rec.Code, body)
	}
	_ = os.Remove(filepath.Join(a.dir, name))

	// A snapshot that fails, and one that fails after writing a partial file.
	broken := good
	broken.Snapshot = func(_ context.Context, dest string) error {
		_ = os.WriteFile(dest+".partial", []byte("half"), 0o600)
		return errors.New("disk I/O error")
	}
	a.api.Backup = &broken
	check("failed snapshot", 500, "backup_failed")
	broken.Snapshot = func(context.Context, string) error { return nil } // writes nothing: an empty file is not a backup
	check("empty snapshot", 500, "backup_failed")

	// One at a time.
	started, release := make(chan struct{}), make(chan struct{})
	slow := good
	slow.Snapshot = func(ctx context.Context, dest string) error {
		close(started)
		<-release
		return good.Snapshot(ctx, dest)
	}
	a.api.Backup = &slow
	done := make(chan int)
	go func() { rec, _ := a.post(t, "/control/v1/backup", act(rid2())); done <- rec.Code }()
	<-started
	rec, body = a.post(t, "/control/v1/backup", act(rid2()))
	if rec.Code != http.StatusTooManyRequests || body["code"] != "busy" {
		t.Fatalf("second backup while one runs = %d %v", rec.Code, body)
	}
	close(release)
	if code := <-done; code != 200 {
		t.Fatalf("first backup = %d", code)
	}
	if got := dirNames(t, a.dir); len(got) != 1 || got[0] != name {
		t.Fatalf("after the slow backup: %v", got)
	}
}

// A backup that does not finish in time is cancelled and leaves nothing.
// Verifies: DCM-SRS-096, DCM-SRS-090
func TestBackupTimeout(t *testing.T) {
	a := newActEnv(t)
	var left time.Duration
	a.api.Backup.Snapshot = func(ctx context.Context, dest string) error {
		if dl, ok := ctx.Deadline(); ok {
			left = time.Until(dl)
		}
		_ = os.WriteFile(dest+".partial", []byte("half"), 0o600)
		return context.DeadlineExceeded
	}
	rec, body := a.post(t, "/control/v1/backup", act(rid()))
	if rec.Code != 500 || body["code"] != "backup_failed" || len(dirNames(t, a.dir)) != 0 {
		t.Fatalf("timed-out backup = %d %v, directory %v", rec.Code, body, dirNames(t, a.dir))
	}
	if left <= 0 || left > BackupTimeout {
		t.Fatalf("the snapshot ran with %v left, want a timeout of at most %v", left, BackupTimeout)
	}
}

// The list shows only snapshots: regular files with a name LibreDCM makes.
// There is no way to download or delete a snapshot through the API.
// Verifies: DCM-SRS-090
func TestBackupListAndNoDownloadOrDelete(t *testing.T) {
	a := newActEnv(t)
	rec, body := a.post(t, "/control/v1/backup", act(rid()))
	if rec.Code != 200 {
		t.Fatalf("backup = %d", rec.Code)
	}
	name := body["file"].(string)
	other := "index-dcm-2-20250101T000000Z.db"
	for _, f := range []string{other, "secret-notes.txt", "index-dcm-1-20261114T150405Z.db.partial", "index.db", ".hidden.db",
		"index-dcm-1-20261114.db", "index-dcm-1-20261114T150405Z.db.bak"} {
		if err := os.WriteFile(filepath.Join(a.dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(a.dir, "index-dcm-9-20260101T000000Z.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(a.dir, "index-dcm-8-20260101T000000Z.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	list := a.get(t, "/control/v1/backups")
	var got []string
	for _, f := range list["backups"].([]any) {
		got = append(got, f.(map[string]any)["file"].(string))
	}
	sort.Strings(got)
	want := []string{name, other}
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) || list["count"] != float64(2) || list["more"] != false {
		t.Fatalf("backups = %v, want %v", got, want)
	}
	if raw, _ := json.Marshal(list); strings.Contains(string(raw), "secret-notes") || strings.Contains(string(raw), "passwd") {
		t.Fatalf("the list shows other files: %s", raw)
	}

	// No download, no delete, no other method.
	for _, p := range []string{"/control/v1/backups/" + name, "/control/v1/backup/" + name, "/control/v1/backups/..%2f..%2fetc%2fpasswd",
		"/control/v1/backups?file=" + name, "/control/v1/backup?download=" + name} {
		for _, m := range []string{"GET", "DELETE", "PUT", "PATCH"} {
			rec, _ := a.do(t, m, p, testToken)
			if strings.Contains(p, "?") && m == "GET" && strings.HasPrefix(p, "/control/v1/backups?") {
				// The list ignores query fields; it never returns file content.
				if rec.Code != 200 || strings.Contains(rec.Body.String(), "SQLite format") {
					t.Errorf("%s %s = %d", m, p, rec.Code)
				}
				continue
			}
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 404 or 405", m, p, rec.Code)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(a.dir, name)); err != nil {
		t.Fatalf("the snapshot is gone: %v", err)
	}
}
