package control

// Tests of the configuration edit of the control API (DCM-SRS-093, -094,
// -095) with a stand-in for the node's reloader. The real reloader, the
// overlay file and the loader are tested in cmd/libredcm.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/platform/config"
)

// fakeVersion is one version of the stand-in overlay.
type fakeVersion struct {
	edit ConfigEdit
	meta OverlayMeta
	kind string
}

// fakeEditor stands for the reloader as writer of the overlay: the file
// gives base settings, the overlay replaces a setting as a whole, the
// environment adds its destinations, and the result goes to the fake live
// settings.
type fakeEditor struct {
	live      *fakeLive
	file      LiveSettings
	env       map[string]gateway.RemoteAE
	cur, prev *fakeVersion
	off       bool
	err       error // every preview and apply fails with it
	writeErr  error // an apply fails with it after its check passed
	writes    int
}

func newFakeEditor(live *fakeLive) *fakeEditor {
	return &fakeEditor{live: live,
		file: LiveSettings{RemoteAEs: map[string]gateway.RemoteAE{"VIEWER1": {Host: "10.0.0.20", Port: 104, AETitle: "VIEWER1"}},
			AllowedAEs: []string{"MR1", "CT1@10.0.0.5"}},
		env: map[string]gateway.RemoteAE{"ENVDEST": {Host: "10.0.0.30", Port: 11112, AETitle: "PACSB"}}}
}

func (f *fakeEditor) Enabled() bool { return !f.off }

func (f *fakeEditor) Managed() (bool, bool) {
	return f.cur != nil && f.cur.edit.RemoteAEs != nil, f.cur != nil && f.cur.edit.AllowedAEs != nil
}

func (f *fakeEditor) settings(e ConfigEdit) LiveSettings {
	next := LiveSettings{RemoteAEs: map[string]gateway.RemoteAE{}, AllowedAEs: append([]string{}, f.file.AllowedAEs...)}
	src := f.file.RemoteAEs
	if e.RemoteAEs != nil {
		src = *e.RemoteAEs
	}
	for k, v := range src {
		next.RemoteAEs[k] = v
	}
	for k, v := range f.env {
		next.RemoteAEs[k] = v
	}
	if e.AllowedAEs != nil {
		next.AllowedAEs = append([]string{}, *e.AllowedAEs...)
	}
	return next
}

func (f *fakeEditor) candidate(e ConfigEdit) ConfigEdit {
	var c ConfigEdit
	if f.cur != nil {
		c = f.cur.edit
	}
	if e.RemoteAEs != nil {
		c.RemoteAEs = e.RemoteAEs
	}
	if e.AllowedAEs != nil {
		c.AllowedAEs = e.AllowedAEs
	}
	return c
}

func (f *fakeEditor) PreviewEdit(e ConfigEdit) (LiveSettings, error) {
	if f.err != nil {
		return LiveSettings{}, f.err
	}
	return f.settings(f.candidate(e)), nil
}

func (f *fakeEditor) commit(c ConfigEdit, meta OverlayMeta, kind string, check func(LiveSettings) error) (LiveSettings, error) {
	if f.err != nil {
		return LiveSettings{}, f.err
	}
	next := f.settings(c)
	if check != nil {
		if err := check(next); err != nil {
			return next, err
		}
	}
	if f.writeErr != nil {
		return next, f.writeErr
	}
	f.prev = f.cur
	if f.prev == nil {
		f.prev = &fakeVersion{kind: "initial", meta: OverlayMeta{Time: meta.Time}}
	}
	f.cur = &fakeVersion{edit: c, meta: meta, kind: kind}
	f.writes++
	f.live.remotes = map[string]gateway.RemoteAE{}
	for k, v := range next.RemoteAEs {
		if v.AETitle == "" {
			v.AETitle = k
		}
		f.live.remotes[k] = v
	}
	f.live.rules, f.live.enforced = append([]string(nil), next.AllowedAEs...), len(next.AllowedAEs) > 0
	return next, nil
}

func (f *fakeEditor) ApplyEdit(e ConfigEdit, meta OverlayMeta, check func(LiveSettings) error) (LiveSettings, error) {
	return f.commit(f.candidate(e), meta, "edit", check)
}

func (f *fakeEditor) PreviewRollback() (LiveSettings, error) {
	if f.err != nil {
		return LiveSettings{}, f.err
	}
	if f.prev == nil {
		return LiveSettings{}, ErrNoPreviousVersion
	}
	return f.settings(f.prev.edit), nil
}

func (f *fakeEditor) ApplyRollback(meta OverlayMeta, check func(LiveSettings) error) (LiveSettings, error) {
	if f.prev == nil {
		return LiveSettings{}, ErrNoPreviousVersion
	}
	return f.commit(f.prev.edit, meta, "rollback", check)
}

func (f *fakeEditor) History() (*OverlayVersion, *OverlayVersion, error) {
	view := func(v *fakeVersion) *OverlayVersion {
		if v == nil {
			return nil
		}
		o := &OverlayVersion{Time: v.meta.Time.UTC().Format(time.RFC3339), Operator: v.meta.Operator, Reason: v.meta.Reason,
			RequestID: v.meta.RequestID, Kind: v.kind, Digest: fmt.Sprintf("sha256:%p", v), Settings: []string{}}
		if v.edit.AllowedAEs != nil {
			o.Settings = append(o.Settings, keyAllowedAEs)
		}
		if v.edit.RemoteAEs != nil {
			o.Settings = append(o.Settings, keyRemoteAEs)
		}
		return o
	}
	return view(f.cur), view(f.prev), nil
}

// dest is one destination of a request.
func dest(name, host string, port int, ae string) map[string]any {
	return map[string]any{"name": name, "host": host, "port": port, "ae_title": ae}
}

func settings(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return map[string]any{"settings": m}
}

// configPlan asks for the plan of an edit and returns it.
func (d *dataEnv) configPlan(t *testing.T, s map[string]any) map[string]any {
	t.Helper()
	rec, body := d.post(t, "/control/v1/config/plan", s)
	if rec.Code != 200 {
		t.Fatalf("config plan = %d %s", rec.Code, rec.Body)
	}
	return body["plan"].(map[string]any)
}

// configApply sends an edit with the envelope and the given fields.
func (d *dataEnv) configApply(t *testing.T, id string, s map[string]any, kv ...any) (int, map[string]any) {
	t.Helper()
	m := act(id, kv...)
	m["settings"] = s["settings"]
	rec, body := d.post(t, "/control/v1/config", m)
	return rec.Code, body
}

func warningCodes(plan map[string]any) string {
	var codes []string
	for _, w := range plan["warnings"].([]any) {
		m := w.(map[string]any)
		if m["must_acknowledge"] == true {
			codes = append(codes, m["code"].(string))
		}
	}
	sort.Strings(codes)
	return strings.Join(codes, " ")
}

// liveState is a short text of what is in force.
func (d *dataEnv) liveState() string {
	var names []string
	for k, v := range d.live.remotes {
		names = append(names, fmt.Sprintf("%s=%s:%d/%s", k, v.Host, v.Port, v.AETitle))
	}
	sort.Strings(names)
	return fmt.Sprint(names, d.live.rules, d.live.enforced)
}

// The plan of an edit shows the difference to the running settings and
// changes nothing. Apply needs the confirmation and the digest of that
// plan; then the change is written, in force and audited, the previous
// version is kept, and the settings are reported as coming from the overlay.
// Verifies: DCM-SRS-093, DCM-SRS-094, DCM-SRS-082, DCM-SRS-083
func TestConfigEditPlanAndApply(t *testing.T) {
	d := newDataEnv(t)
	start := d.liveState()
	edit := settings(keyRemoteAEs, []any{dest("VIEWER1", "10.0.0.20", 104, "VIEWER1"), dest("NEWVIEW", "viewer-2.example", 11112, "")},
		keyAllowedAEs, []string{"MR1", "CT1@10.0.0.5", "US1@10.0.0.0/24"})
	p := d.configPlan(t, edit)
	ra, al := p["remote_aes"].(map[string]any), p["allow_list"].(map[string]any)
	if p["changed"] != true || fmt.Sprint(strs(ra["added"])) != "[NEWVIEW]" || len(ra["removed"].([]any)) != 0 || len(ra["changed"].([]any)) != 0 ||
		fmt.Sprint(strs(al["added"])) != "[US1@10.0.0.0/24]" || len(al["removed"].([]any)) != 0 || warningCodes(p) != "" {
		t.Fatalf("plan = %v", p)
	}
	if added := ra["added"].([]any)[0].(map[string]any); added["source"] != "overlay" || added["ae_title"] != "NEWVIEW" || added["host"] != "viewer-2.example" {
		t.Fatalf("added destination = %v", added)
	}
	if d.ed.writes != 0 || d.liveState() != start || len(auditRecs(t, d.rt.Audit)) != 0 {
		t.Fatal("a plan changed something or wrote an audit record")
	}
	if again := d.configPlan(t, edit); again["plan_digest"] != p["plan_digest"] {
		t.Fatal("the digest of an unchanged plan is not stable")
	}

	// The guard: confirmation and the digest of this plan.
	other := d.configPlan(t, settings(keyAllowedAEs, []string{"MR1", "CT1@10.0.0.5", "XA1"}))
	d.rl.next = d.ed.settings(d.ed.candidate(ConfigEdit{}))
	d.rl.next.AllowedAEs = []string{"MR1", "CT1@10.0.0.5", "US1@10.0.0.0/24"}
	d.rl.next.RemoteAEs["NEWVIEW"] = gateway.RemoteAE{Host: "viewer-2.example", Port: 11112}
	reloadPlan := d.plan(t) // a reload plan to the very same settings
	if fmt.Sprint(reloadPlan["remote_aes"].(map[string]any)["added"]) == "[]" || reloadPlan["plan_digest"] == p["plan_digest"] {
		t.Fatalf("a reload plan to the same settings has the digest of the edit plan: %v", reloadPlan)
	}
	for name, c := range map[string]struct {
		fields []any
		status int
		code   string
	}{
		"not confirmed":         {[]any{"plan_digest", p["plan_digest"]}, 422, "not_confirmed"},
		"confirm false":         {[]any{"plan_digest", p["plan_digest"], "confirm", false}, 422, "not_confirmed"},
		"no digest":             {[]any{"confirm", true}, 422, "plan_digest_required"},
		"wrong digest":          {[]any{"confirm", true, "plan_digest", "sha256:0000"}, 409, "plan_changed"},
		"digest of other edit":  {[]any{"confirm", true, "plan_digest", other["plan_digest"]}, 409, "plan_changed"},
		"digest of reload plan": {[]any{"confirm", true, "plan_digest", reloadPlan["plan_digest"]}, 409, "plan_changed"},
	} {
		code, body := d.configApply(t, rid(), edit, c.fields...)
		if code != c.status || body["code"] != c.code {
			t.Errorf("%s = %d %v, want %d %s", name, code, body, c.status, c.code)
		}
		if len(findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": auditRefused, "code": c.code})) == 0 {
			t.Errorf("%s: no refusal audit record", name)
		}
	}
	if d.ed.writes != 0 || d.liveState() != start {
		t.Fatal("a refused edit was written or applied")
	}

	before := d.get(t, "/control/v1")["instance"].(map[string]any)["config_digest"]
	id := rid()
	code, body := d.configApply(t, id, edit, "confirm", true, "plan_digest", p["plan_digest"])
	if code != 200 || body["applied"] != true || body["changed"] != true || body["config_digest_before"] != before || body["config_digest_after"] == before {
		t.Fatalf("apply = %d %v", code, body)
	}
	if d.ed.writes != 1 || d.live.remotes["NEWVIEW"].Host != "viewer-2.example" || d.live.remotes["NEWVIEW"].AETitle != "NEWVIEW" ||
		len(d.live.remotes) != 3 || fmt.Sprint(d.live.rules) != "[MR1 CT1@10.0.0.5 US1@10.0.0.0/24]" {
		t.Fatalf("in force after apply: %s", d.liveState())
	}
	if m := d.ed.cur.meta; m.Operator != "Pat Admin" || m.Reason != "routine test" || m.RequestID != id || time.Since(m.Time) > time.Minute {
		t.Fatalf("overlay version written with %+v", m)
	}
	cur, prev := body["current"].(map[string]any), body["previous"].(map[string]any)
	if cur["operator"] != "Pat Admin" || cur["kind"] != "edit" || prev["kind"] != "initial" {
		t.Fatalf("versions in the answer: %v / %v", cur, prev)
	}
	recs := findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": auditConfigChange, "phase": "result", "request_id": id})
	if len(recs) != 1 || recs[0]["outcome"] != "success" || recs[0]["change"] != "edit" || recs[0]["changed"] != true ||
		recs[0]["operator"] != "Pat Admin" || recs[0]["digest_before"] != before || recs[0]["digest_after"] != body["config_digest_after"] ||
		fmt.Sprint(recs[0]["remote_aes_added"]) != "[NEWVIEW]" || fmt.Sprint(recs[0]["allow_list_added"]) != "[US1@10.0.0.0/24]" {
		t.Fatalf("configuration-changed record = %v", recs)
	}
	// The same request again does not write again.
	if code, again := d.configApply(t, id, edit, "confirm", true, "plan_digest", p["plan_digest"]); code != 200 || again["applied"] != true || d.ed.writes != 1 {
		t.Fatalf("repeat = %d %v, %d writes", code, again, d.ed.writes)
	}
	// The applied plan is stale now.
	if code, body := d.configApply(t, rid(), edit, "confirm", true, "plan_digest", p["plan_digest"]); code != 409 || body["code"] != "plan_changed" {
		t.Fatalf("stale plan = %d %v", code, body)
	}
	// Asking for what is in force already writes nothing.
	same := d.configPlan(t, edit)
	if same["changed"] != false {
		t.Fatalf("plan to the settings in force = %v", same)
	}
	if code, body := d.configApply(t, rid(), edit, "confirm", true, "plan_digest", same["plan_digest"]); code != 200 || body["applied"] != false ||
		body["changed"] != false || d.ed.writes != 1 {
		t.Fatalf("apply without a change = %d %v, %d writes", code, body, d.ed.writes)
	}

	// The read-only views name the overlay as the source; the environment's destination stays "env".
	src := map[string]string{}
	for _, r := range d.get(t, "/control/v1/remote-aes")["remote_aes"].([]any) {
		m := r.(map[string]any)
		src[m["name"].(string)] = m["source"].(string)
	}
	if fmt.Sprint(src) != "map[ENVDEST:env NEWVIEW:overlay VIEWER1:overlay]" {
		t.Fatalf("remote AE sources = %v", src)
	}
	for _, r := range d.get(t, "/control/v1/allow-list")["entries"].([]any) {
		if r.(map[string]any)["source"] != "overlay" {
			t.Fatalf("allow-list entry = %v", r)
		}
	}
	byKey := settingsByKey(t, d.get(t, "/control/v1/config"))
	if byKey[keyAllowedAEs]["source"] != config.SourceOverlay || byKey[keyRemoteAEs]["source"] != config.SourceOverlay {
		t.Fatalf("config sources: %v / %v", byKey[keyAllowedAEs], byKey[keyRemoteAEs])
	}
	h := d.get(t, "/control/v1/config/history")
	if h["enabled"] != true || h["current"].(map[string]any)["request_id"] != id || h["previous"].(map[string]any)["kind"] != "initial" ||
		fmt.Sprint(h["in_force_from_overlay"]) != "[dicom.allowed_aes dicom.remote_aes]" {
		t.Fatalf("history = %v", h)
	}
}

// Only the two live settings can be edited. Every other setting, whatever
// its value, is refused: a setting that needs a restart never reaches the
// node. Invalid values are refused at plan time and at apply.
// Verifies: DCM-SRS-093
func TestConfigEditRefusesEverythingElse(t *testing.T) {
	d := newDataEnv(t)
	start := d.liveState()
	both := func(name string, s map[string]any, status int, code string) {
		t.Helper()
		rec, body := d.post(t, "/control/v1/config/plan", s)
		if rec.Code != status || body["code"] != code {
			t.Errorf("plan, %s = %d %v, want %d %s", name, rec.Code, body, status, code)
		}
		c, body := d.configApply(t, rid(), s, "confirm", true, "plan_digest", "sha256:x")
		if c != status || body["code"] != code {
			t.Errorf("apply, %s = %d %v, want %d %s", name, c, body, status, code)
		}
	}
	// Every setting of the node other than the two.
	n := 0
	for _, s := range d.api.Settings {
		if s.Key == keyRemoteAEs || s.Key == keyAllowedAEs {
			continue
		}
		n++
		both(s.Key, settings(s.Key, "x"), 422, "restart_required")
		// Also next to a valid edit: the whole request is refused.
		both(s.Key+" with a valid edit", settings(s.Key, "x", keyAllowedAEs, []string{"MR1"}), 422, "restart_required")
	}
	if n < 10 {
		t.Fatalf("only %d other settings tried", n)
	}
	for _, key := range []string{"dicom.tls_cert_file", "dicom.tls_key_file", "dicom.ae_title", "dicom.listen", "admin.token",
		"dicom.patient_conflict_policy", "dicom.audit_log_off", "control.overlay_file", "control.backup_dir"} {
		rec, body := d.post(t, "/control/v1/config/plan", settings(key, ""))
		if rec.Code != 422 || (body["code"] != "restart_required" && body["code"] != "unknown_setting") {
			t.Errorf("%s = %d %v", key, rec.Code, body)
		}
	}
	both("unknown key", settings("dicom.no_such_setting", true), 422, "unknown_setting")
	both("key in other case", settings("DICOM.ALLOWED_AES", []string{}), 422, "unknown_setting")
	both("no settings", map[string]any{"settings": map[string]any{}}, 422, "nothing_to_change")
	both("settings missing", map[string]any{}, 422, "nothing_to_change")

	for name, s := range map[string]map[string]any{
		"allow-list not a list":   settings(keyAllowedAEs, "MR1"),
		"allow-list null":         settings(keyAllowedAEs, nil),
		"allow-list of numbers":   settings(keyAllowedAEs, []int{1}),
		"allow rule bad network":  settings(keyAllowedAEs, []string{"CT1@10.0.0.0/99"}),
		"allow rule bad address":  settings(keyAllowedAEs, []string{"CT1@not-an-ip"}),
		"allow rule empty":        settings(keyAllowedAEs, []string{"MR1", ""}),
		"allow rule too long":     settings(keyAllowedAEs, []string{strings.Repeat("A", 17)}),
		"remote AEs a map":        settings(keyRemoteAEs, map[string]any{"V": dest("V", "10.0.0.1", 104, "")}),
		"remote AEs null":         settings(keyRemoteAEs, nil),
		"remote AE unknown field": settings(keyRemoteAEs, []any{map[string]any{"name": "V", "host": "10.0.0.1", "port": 104, "tls": false}}),
		"remote AE port text":     settings(keyRemoteAEs, []any{map[string]any{"name": "V", "host": "10.0.0.1", "port": "104"}}),
		"remote AE no name":       settings(keyRemoteAEs, []any{dest("", "10.0.0.1", 104, "")}),
		"remote AE long name":     settings(keyRemoteAEs, []any{dest(strings.Repeat("V", 17), "10.0.0.1", 104, "")}),
		"remote AE no host":       settings(keyRemoteAEs, []any{dest("V", "", 104, "")}),
		"remote AE host and path": settings(keyRemoteAEs, []any{dest("V", "10.0.0.1/x", 104, "")}),
		"remote AE port 0":        settings(keyRemoteAEs, []any{dest("V", "10.0.0.1", 0, "")}),
		"remote AE port 70000":    settings(keyRemoteAEs, []any{dest("V", "10.0.0.1", 70000, "")}),
		"remote AE twice":         settings(keyRemoteAEs, []any{dest("V", "10.0.0.1", 104, ""), dest("V", "10.0.0.2", 104, "")}),
		"remote AE long AE title": settings(keyRemoteAEs, []any{dest("V", "10.0.0.1", 104, strings.Repeat("A", 17))}),
	} {
		both(name, s, 422, "invalid_config")
	}
	if d.ed.writes != 0 || d.liveState() != start {
		t.Fatalf("a refused edit was written or applied: %s", d.liveState())
	}
}

// A setting the environment fixes cannot be edited: the answer names the
// variable. A destination of the environment may be sent back unchanged (a
// client sends the whole table); it is then not copied into the overlay.
// Verifies: DCM-SRS-093, DCM-SRS-050
func TestConfigEditSetByEnvironment(t *testing.T) {
	d := newDataEnv(t)
	for name, list := range map[string][]any{
		"other host":     {dest("VIEWER1", "10.0.0.20", 104, "VIEWER1"), dest("ENVDEST", "203.0.113.9", 11112, "PACSB")},
		"other port":     {dest("ENVDEST", "10.0.0.30", 104, "PACSB")},
		"other AE title": {dest("ENVDEST", "10.0.0.30", 11112, "OTHER")},
		"no AE title":    {dest("ENVDEST", "10.0.0.30", 11112, "")},
	} {
		rec, body := d.post(t, "/control/v1/config/plan", settings(keyRemoteAEs, list))
		if rec.Code != 422 || body["code"] != "set_by_environment" || !strings.Contains(fmt.Sprint(body["error"]), "LIBRE_DCM_REMOTE_AES") {
			t.Errorf("%s = %d %v", name, rec.Code, body)
		}
		if code, body := d.configApply(t, rid(), settings(keyRemoteAEs, list), "confirm", true, "plan_digest", "sha256:x"); code != 422 || body["code"] != "set_by_environment" {
			t.Errorf("apply, %s = %d %v", name, code, body)
		}
	}
	// Sent back as it is: accepted, stays in the environment only.
	edit := settings(keyRemoteAEs, []any{dest("VIEWER1", "10.0.0.20", 104, "VIEWER1"), dest("ENVDEST", "10.0.0.30", 11112, "PACSB"), dest("NEWVIEW", "10.0.0.40", 104, "")})
	p := d.configPlan(t, edit)
	if code, body := d.configApply(t, rid(), edit, "confirm", true, "plan_digest", p["plan_digest"]); code != 200 || body["applied"] != true {
		t.Fatalf("apply = %d %v", code, body)
	}
	if _, copied := (*d.ed.cur.edit.RemoteAEs)["ENVDEST"]; copied || len(*d.ed.cur.edit.RemoteAEs) != 2 || d.live.remotes["ENVDEST"].Host != "10.0.0.30" {
		t.Fatalf("overlay holds %v", *d.ed.cur.edit.RemoteAEs)
	}
	// Leaving the environment's destination out does not remove it.
	p = d.configPlan(t, settings(keyRemoteAEs, []any{dest("VIEWER1", "10.0.0.20", 104, "VIEWER1"), dest("NEWVIEW", "10.0.0.40", 104, "")}))
	if p["changed"] != false {
		t.Fatalf("plan without the environment's destination = %v", p)
	}

	// The allow-list fixed by the environment.
	for i, s := range d.api.Settings {
		if s.Key == keyAllowedAEs {
			d.api.Settings[i].Source, d.api.Settings[i].Env = config.SourceEnv, "LIBRE_DCM_ALLOWED_AES"
		}
	}
	writes := d.ed.writes
	rec, body := d.post(t, "/control/v1/config/plan", settings(keyAllowedAEs, []string{"MR1"}))
	if rec.Code != 422 || body["code"] != "set_by_environment" || !strings.Contains(fmt.Sprint(body["error"]), "LIBRE_DCM_ALLOWED_AES") {
		t.Fatalf("allow-list set by the environment: plan = %d %v", rec.Code, body)
	}
	if code, body := d.configApply(t, rid(), settings(keyAllowedAEs, []string{"MR1"}), "confirm", true, "plan_digest", "sha256:x"); code != 422 ||
		body["code"] != "set_by_environment" || d.ed.writes != writes {
		t.Fatalf("allow-list set by the environment: apply = %d %v", code, body)
	}
}

// gatewayAudit puts a record into the audit ring as the gateway writes it
// for a store, at the given time.
func gatewayAudit(t *testing.T, d *dataEnv, at time.Time, event, callingAE, remote string) {
	t.Helper()
	rec := slog.NewRecord(at, slog.LevelInfo, "audit", 0)
	rec.AddAttrs(slog.Bool("audit", true), slog.String("event", event), slog.String("outcome", "success"),
		slog.String("calling_ae", callingAE), slog.String("called_ae", "LIBREDCM"), slog.String("remote", remote))
	if err := d.rt.Logger.Handler().Handle(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

// A change that weakens a risk control carries a warning, and apply is
// refused until each warning is acknowledged by its code: an allow-list
// that becomes empty, an allow-list that would refuse a calling AE that was
// active in the last 24 hours, a destination that is removed or pointed
// somewhere else.
// Verifies: DCM-SRS-095
func TestConfigEditWarnings(t *testing.T) {
	d := newDataEnv(t)
	start := d.liveState()
	viewer := dest("VIEWER1", "10.0.0.20", 104, "VIEWER1")
	now := time.Now()
	gatewayAudit(t, d, now.Add(-2*time.Hour), "DICOMInstancesTransferred.received", "CT1", "10.0.0.5:40001")
	gatewayAudit(t, d, now.Add(-3*time.Hour), "Query", "MR1", "10.0.0.77:40002")
	gatewayAudit(t, d, now.Add(-30*time.Hour), "DICOMInstancesTransferred.received", "OLDCT", "10.0.0.8:40003")    // too long ago
	gatewayAudit(t, d, now.Add(-1*time.Hour), "SecurityAlert.association-rejected", "STRANGER", "10.0.0.99:40004") // was refused already

	for name, c := range map[string]struct {
		edit map[string]any
		want string
		text string
	}{
		"nothing weakened":       {settings(keyAllowedAEs, []string{"MR1", "CT1@10.0.0.5", "XA1"}), "", ""},
		"allow-list emptied":     {settings(keyAllowedAEs, []string{}), WarnAllowListEmptied, ""},
		"active CT1 removed":     {settings(keyAllowedAEs, []string{"MR1"}), WarnAllowListEntryRemoved, "CT1"},
		"active CT1 other host":  {settings(keyAllowedAEs, []string{"MR1", "CT1@10.0.0.6"}), WarnAllowListEntryRemoved, "CT1"},
		"active MR1 narrowed":    {settings(keyAllowedAEs, []string{"MR1@10.0.0.1", "CT1@10.0.0.5"}), WarnAllowListEntryRemoved, "MR1"},
		"both active removed":    {settings(keyAllowedAEs, []string{"XA1"}), WarnAllowListEntryRemoved, "CT1, MR1"},
		"CT1 by network, kept":   {settings(keyAllowedAEs, []string{"MR1", "CT1@10.0.0.0/24"}), "", ""},
		"destination removed":    {settings(keyRemoteAEs, []any{}), WarnRemoteAERemoved, "VIEWER1"},
		"destination other host": {settings(keyRemoteAEs, []any{dest("VIEWER1", "203.0.113.9", 104, "VIEWER1")}), WarnRemoteAERetargeted, "VIEWER1"},
		"destination other port": {settings(keyRemoteAEs, []any{dest("VIEWER1", "10.0.0.20", 11112, "VIEWER1")}), WarnRemoteAERetargeted, "VIEWER1"},
		"destination other AE":   {settings(keyRemoteAEs, []any{dest("VIEWER1", "10.0.0.20", 104, "ELSE")}), WarnRemoteAERetargeted, "VIEWER1"},
		"destination added":      {settings(keyRemoteAEs, []any{viewer, dest("NEWVIEW", "10.0.0.40", 104, "")}), "", ""},
		"everything at once": {settings(keyRemoteAEs, []any{dest("VIEWER1", "203.0.113.9", 104, "VIEWER1")}, keyAllowedAEs, []string{}),
			WarnAllowListEmptied + " " + WarnRemoteAERetargeted, ""},
	} {
		p := d.configPlan(t, c.edit)
		if got := warningCodes(p); got != c.want {
			t.Errorf("%s: warnings %q, want %q", name, got, c.want)
			continue
		}
		if c.text != "" && !strings.Contains(fmt.Sprint(p["warnings"]), c.text) {
			t.Errorf("%s: warning does not name %s: %v", name, c.text, p["warnings"])
		}
		if strings.Contains(fmt.Sprint(p["warnings"]), "OLDCT") || strings.Contains(fmt.Sprint(p["warnings"]), "STRANGER") {
			t.Errorf("%s: warning names a peer that was not active in the last 24 hours: %v", name, p["warnings"])
		}
		if c.want == "" {
			continue
		}
		// Without the acknowledgement of every code: refused, nothing written.
		codes := strings.Fields(c.want)
		for ackName, ack := range map[string][]string{"none": nil, "wrong": {"all", "*", strings.ToUpper(codes[0])}, "all but one": codes[1:]} {
			code, body := d.configApply(t, rid(), c.edit, "confirm", true, "plan_digest", p["plan_digest"], "acknowledge_warnings", ack)
			if code != 422 || body["code"] != "warning_not_acknowledged" {
				t.Errorf("%s, acknowledged %s = %d %v", name, ackName, code, body)
			}
		}
	}
	if d.ed.writes != 0 || d.liveState() != start {
		t.Fatalf("an unacknowledged weakening was written or applied: %s", d.liveState())
	}

	// Acknowledged: applied, and the audit record says what was acknowledged.
	edit := settings(keyRemoteAEs, []any{dest("VIEWER1", "203.0.113.9", 104, "VIEWER1")}, keyAllowedAEs, []string{})
	p := d.configPlan(t, edit)
	id := rid()
	code, body := d.configApply(t, id, edit, "confirm", true, "plan_digest", p["plan_digest"],
		"acknowledge_warnings", []string{WarnRemoteAERetargeted, WarnAllowListEmptied})
	if code != 200 || body["applied"] != true || d.live.enforced || d.live.remotes["VIEWER1"].Host != "203.0.113.9" {
		t.Fatalf("acknowledged apply = %d %v", code, body)
	}
	recs := findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": auditConfigChange, "phase": "result", "request_id": id})
	if len(recs) != 1 || fmt.Sprint(recs[0]["acknowledged_warnings"]) != "[allow_list_emptied remote_ae_retargeted]" || recs[0]["allow_list_enforced"] != false {
		t.Fatalf("audit record = %v", recs)
	}

	// The list is empty now (every AE may connect). Enforcing a list that
	// lacks an active peer locks that peer out: same warning.
	p = d.configPlan(t, settings(keyAllowedAEs, []string{"MR1"}))
	if warningCodes(p) != WarnAllowListEntryRemoved || !strings.Contains(fmt.Sprint(p["warnings"]), "CT1") {
		t.Fatalf("enforcing a list without an active peer: %v", p["warnings"])
	}
	if p = d.configPlan(t, settings(keyAllowedAEs, []string{"MR1", "CT1"})); warningCodes(p) != "" {
		t.Fatalf("enforcing a list with every active peer: %v", p["warnings"])
	}
	// A reload plan gets the same warning.
	d.rl.next = LiveSettings{RemoteAEs: d.live.RemoteAEs(), AllowedAEs: []string{"MR1"}}
	if rp := d.plan(t); warningCodes(rp) != WarnAllowListEntryRemoved {
		t.Fatalf("reload plan warnings = %v", rp["warnings"])
	}

	// With audit records off the node cannot tell who was active: a
	// narrower list always carries the warning.
	d.api.Node.AuditLogOff = true
	if p = d.configPlan(t, settings(keyAllowedAEs, []string{"MR1", "CT1"})); warningCodes(p) != WarnAllowListEntryRemoved {
		t.Fatalf("audit off: %v", p["warnings"])
	}
}

// The previous overlay version is kept, and a rollback puts it back through
// the same plan and apply steps. A rollback can itself be rolled back.
// Verifies: DCM-SRS-094, DCM-SRS-095
func TestConfigRollback(t *testing.T) {
	d := newDataEnv(t)
	start := d.liveState()
	// Nothing to go back to yet.
	if rec, body := d.post(t, "/control/v1/config/rollback/plan", map[string]any{}); rec.Code != 422 || body["code"] != "no_previous_version" {
		t.Fatalf("rollback plan without a previous version = %d %v", rec.Code, body)
	}
	if rec, body := d.post(t, "/control/v1/config/rollback", act(rid(), "confirm", true, "plan_digest", "sha256:x")); rec.Code != 422 || body["code"] != "no_previous_version" {
		t.Fatalf("rollback without a previous version = %d %v", rec.Code, body)
	}
	apply := func(s map[string]any, ack ...string) string {
		t.Helper()
		p := d.configPlan(t, s)
		if code, body := d.configApply(t, rid(), s, "confirm", true, "plan_digest", p["plan_digest"], "acknowledge_warnings", ack); code != 200 || body["applied"] != true {
			t.Fatalf("apply = %d %v", code, body)
		}
		return d.liveState()
	}
	first := apply(settings(keyAllowedAEs, []string{"MR1", "CT1@10.0.0.5", "US1"}))
	second := apply(settings(keyRemoteAEs, []any{dest("VIEWER1", "10.0.0.20", 104, "VIEWER1"), dest("NEWVIEW", "10.0.0.40", 104, "")}))
	if first == start || second == first {
		t.Fatal("the edits changed nothing")
	}

	rec, body := d.post(t, "/control/v1/config/rollback/plan", map[string]any{})
	if rec.Code != 200 {
		t.Fatalf("rollback plan = %d %v", rec.Code, body)
	}
	p := body["plan"].(map[string]any)
	if p["changed"] != true || fmt.Sprint(strs(p["remote_aes"].(map[string]any)["removed"])) != "[NEWVIEW]" || warningCodes(p) != WarnRemoteAERemoved ||
		body["previous"].(map[string]any)["kind"] != "edit" {
		t.Fatalf("rollback plan = %v", body)
	}
	if d.liveState() != second || d.ed.writes != 2 {
		t.Fatal("the rollback plan changed something")
	}
	// The same guard as an edit; the digest of an edit plan to the same
	// settings does not approve a rollback.
	editPlan := d.configPlan(t, settings(keyRemoteAEs, []any{dest("VIEWER1", "10.0.0.20", 104, "VIEWER1")}))
	for name, c := range map[string]struct {
		fields []any
		status int
		code   string
	}{
		"not confirmed":       {[]any{"plan_digest", p["plan_digest"]}, 422, "not_confirmed"},
		"no digest":           {[]any{"confirm", true}, 422, "plan_digest_required"},
		"wrong digest":        {[]any{"confirm", true, "plan_digest", "sha256:0000", "acknowledge_warnings", []string{WarnRemoteAERemoved}}, 409, "plan_changed"},
		"digest of edit plan": {[]any{"confirm", true, "plan_digest", editPlan["plan_digest"], "acknowledge_warnings", []string{WarnRemoteAERemoved}}, 409, "plan_changed"},
		"not acknowledged":    {[]any{"confirm", true, "plan_digest", p["plan_digest"]}, 422, "warning_not_acknowledged"},
	} {
		rec, body := d.post(t, "/control/v1/config/rollback", act(rid(), c.fields...))
		if rec.Code != c.status || body["code"] != c.code {
			t.Errorf("%s = %d %v, want %d %s", name, rec.Code, body, c.status, c.code)
		}
	}
	if d.liveState() != second || d.ed.writes != 2 {
		t.Fatal("a refused rollback changed something")
	}
	id := rid()
	rec, body = d.post(t, "/control/v1/config/rollback", act(id, "confirm", true, "plan_digest", p["plan_digest"], "acknowledge_warnings", []string{WarnRemoteAERemoved}))
	if rec.Code != 200 || body["applied"] != true || d.liveState() != first || d.ed.writes != 3 {
		t.Fatalf("rollback = %d %v; in force %s, want %s", rec.Code, body, d.liveState(), first)
	}
	if cur := body["current"].(map[string]any); cur["kind"] != "rollback" || cur["operator"] != "Pat Admin" || cur["request_id"] != id {
		t.Fatalf("version after rollback = %v", cur)
	}
	recs := findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": auditConfigChange, "phase": "result", "request_id": id})
	if len(recs) != 1 || recs[0]["change"] != "rollback" || fmt.Sprint(recs[0]["remote_aes_removed"]) != "[NEWVIEW]" ||
		fmt.Sprint(recs[0]["acknowledged_warnings"]) != "[remote_ae_removed]" {
		t.Fatalf("audit record = %v", recs)
	}
	// Rolling back again returns to the second state.
	rec, body = d.post(t, "/control/v1/config/rollback/plan", map[string]any{})
	p = body["plan"].(map[string]any)
	if rec, body := d.post(t, "/control/v1/config/rollback", act(rid(), "confirm", true, "plan_digest", p["plan_digest"])); rec.Code != 200 || d.liveState() != second {
		t.Fatalf("second rollback = %d %v; in force %s", rec.Code, body, d.liveState())
	}
}

// A change that does not load, a blocked reload and a failed write are
// reported as what they are, and leave the running settings and the
// overlay as they were.
// Verifies: DCM-SRS-093, DCM-SRS-060
func TestConfigEditFailuresChangeNothing(t *testing.T) {
	d := newDataEnv(t)
	start := d.liveState()
	edit := settings(keyAllowedAEs, []string{"MR1", "CT1@10.0.0.5", "US1"})
	p := d.configPlan(t, edit)
	for name, c := range map[string]struct {
		err, writeErr error
		status        int
		code          string
		planStatus    int
	}{
		"does not validate": {fmt.Errorf("config: parse /etc/libredcm/config.yaml: line 3"), nil, 422, "invalid_config", 422},
		"licence block":     {fmt.Errorf("%w: unlicensed", ErrReloadBlocked), nil, 423, "license_ack_required", 423},
		"write failed":      {nil, fmt.Errorf("%w: disk full", ErrConfigWrite), 500, "config_write_failed", 200},
	} {
		d.ed.err, d.ed.writeErr = c.err, c.writeErr
		if rec, body := d.post(t, "/control/v1/config/plan", edit); rec.Code != c.planStatus || (c.planStatus != 200 && body["code"] != c.code) {
			t.Errorf("%s: plan = %d %v", name, rec.Code, body)
		}
		id := rid()
		code, body := d.configApply(t, id, edit, "confirm", true, "plan_digest", p["plan_digest"])
		if code != c.status || body["code"] != c.code {
			t.Errorf("%s: apply = %d %v, want %d %s", name, code, body, c.status, c.code)
		}
		if recs := findAudit(auditRecs(t, d.rt.Audit), map[string]any{"request_id": id, "phase": "result"}); len(recs) != 1 || recs[0]["outcome"] != "minor-failure" {
			t.Errorf("%s: audit = %v", name, recs)
		}
		if c.planStatus == 200 { // the rollback path reports the same failure
			d.ed.prev = &fakeVersion{kind: "initial"}
			rec, rb := d.post(t, "/control/v1/config/rollback/plan", map[string]any{})
			if rec.Code != 200 {
				t.Fatalf("%s: rollback plan = %d", name, rec.Code)
			}
			d.ed.prev = nil
			_ = rb
		}
	}
	if d.ed.writes != 0 || d.liveState() != start || d.ed.cur != nil {
		t.Fatalf("a failed change left something behind: %s", d.liveState())
	}
	d.ed.err, d.ed.writeErr = nil, nil
	if code, body := d.configApply(t, rid(), edit, "confirm", true, "plan_digest", p["plan_digest"]); code != 200 || body["applied"] != true {
		t.Fatalf("apply after the failures = %d %v", code, body)
	}
}

// A node without an overlay file does not serve or list configuration edit.
// Verifies: DCM-SRS-093, DCM-SRS-080
func TestConfigEditOffWithoutOverlayFile(t *testing.T) {
	a := newActEnv(t)
	ed := newFakeEditor(a.live)
	ed.off = true
	a.api.Editor = ed
	a.api.registerConfigEdit(a.rt)
	a.h = a.rt.AdminHandler()
	for _, p := range []string{"/control/v1/config/plan", "/control/v1/config/rollback/plan", "/control/v1/config/rollback"} {
		if rec, _ := a.post(t, p, act(rid(), "settings", map[string]any{keyAllowedAEs: []string{}}, "confirm", true, "plan_digest", "sha256:x")); rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, rec.Code)
		}
	}
	if rec, _ := a.post(t, "/control/v1/config", act(rid(), "confirm", true, "plan_digest", "sha256:x")); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /control/v1/config = %d, want 405 (GET only)", rec.Code)
	}
	if rec, _ := a.do(t, "GET", "/control/v1/config/history", testToken); rec.Code != http.StatusNotFound {
		t.Errorf("GET history = %d, want 404", rec.Code)
	}
	for _, f := range a.get(t, "/control/v1")["features"].([]any) {
		if f == "config-edit" || f == "studies" || f == "send" {
			t.Errorf("feature %v listed on a node that does not serve it", f)
		}
	}
	if ed.writes != 0 {
		t.Fatal("a change was written")
	}
	// The handlers refuse too, should they ever be reached.
	if off := a.api.editorOff(); off == nil || off.code != "config_edit_off" {
		t.Fatalf("editorOff = %v", off)
	}
	if res := a.api.configApply(context.Background(), &ConfigRequest{Confirm: true, PlanDigest: "x"}); res.status != 422 || ed.writes != 0 {
		t.Fatalf("configApply on a node without overlay = %+v", res)
	}
	if res := a.api.rollbackApply(context.Background(), &ReloadRequest{Confirm: true, PlanDigest: "x"}); res.status != 422 {
		t.Fatalf("rollbackApply on a node without overlay = %+v", res)
	}
	if !errors.Is(fmt.Errorf("%w: x", ErrConfigWrite), ErrConfigWrite) {
		t.Fatal("ErrConfigWrite does not wrap")
	}
}
