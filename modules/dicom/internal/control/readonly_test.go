package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
	"github.com/mulcahyio/libre-rws/platform/config"
	"github.com/mulcahyio/libre-rws/platform/runtime"
)

// Synthetic secrets. No response may ever contain one of them.
const (
	testToken   = "tok-ADMIN-7f3a9c"
	s3Access    = "AKIA-SYNTH-ACCESS-01"
	s3Secret    = "s3-SYNTH-secret-key-02" // gitleaks:allow (synthetic test value)
	dbPassword  = "db-SYNTH-pass-03"       // gitleaks:allow (synthetic test value)
	dbQueryPass = "db-SYNTH-querypass-04"
	busPassword = "bus-SYNTH-pass-05" // gitleaks:allow (synthetic test value)
	futureValue = "future-SYNTH-secret-06"
	kvPassword  = "kv-SYNTH-pass-07" // gitleaks:allow (synthetic test value)
)

var allSecrets = []string{testToken, s3Access, s3Secret, dbPassword, dbQueryPass, busPassword, futureValue, kvPassword}

// Every read-only endpoint, with a query that exercises its filters.
var readOnlyPaths = []string{
	"/control/v1",
	"/control/v1/status",
	"/control/v1/config",
	"/control/v1/remote-aes",
	"/control/v1/allow-list",
	"/control/v1/audit",
	"/control/v1/audit?type=Query&outcome=success&limit=5",
	"/control/v1/logs",
	"/control/v1/logs?level=warn&limit=5",
}

type fakeLive struct {
	remotes  map[string]gateway.RemoteAE
	rules    []string
	enforced bool
}

func (f *fakeLive) RemoteAEs() map[string]gateway.RemoteAE {
	cp := map[string]gateway.RemoteAE{}
	for k, v := range f.remotes {
		cp[k] = v
	}
	return cp
}
func (f *fakeLive) AllowedAEs() ([]string, bool) {
	return append([]string(nil), f.rules...), f.enforced
}

type env struct {
	h    http.Handler
	api  *API
	rt   *runtime.Runtime
	live *fakeLive
	idx  index.Index
	m    struct {
		active                    prometheus.Gauge
		total, rejected, failures prometheus.Counter
		cstore, cfind, subops     *prometheus.CounterVec
		outbox                    prometheus.Gauge
	}
	readyErr string
}

// testSettings is a configuration as config.Describe would return it, with a
// secret in every place one can be, plus a setting nobody has classified.
func testSettings() []config.Setting {
	return []config.Setting{
		{Key: "admin.addr", Value: ":8081", Source: config.SourceDefault},
		{Key: "admin.token", Value: testToken, Source: config.SourceEnv, Env: "LIBRE_DCM_ADMIN_TOKEN"},
		{Key: "admin.token_file", Value: "", Source: config.SourceDefault},
		{Key: "admin.tls_key_file", Value: "/certs/tls.key", Source: config.SourceFile},
		{Key: "dicom.ae_title", Value: "LIBREDCM", Source: config.SourceFile},
		{Key: "dicom.idle_timeout", Value: "5m0s", Source: config.SourceDefault},
		{Key: "dicom.audit_omit_patient_id", Value: false, Source: config.SourceDefault},
		{Key: "dicom.remote_aes", Value: map[string]gateway.RemoteAE{"STALE": {Host: "old", Port: 1}}, Source: config.SourceFile},
		{Key: "dicom.allowed_aes", Value: []string{"STALE"}, Source: config.SourceFile},
		{Key: "storage.driver", Value: "s3", Source: config.SourceFile},
		{Key: "storage.s3.endpoint", Value: "minio:9000", Source: config.SourceFile},
		{Key: "storage.s3.access_key", Value: s3Access, Source: config.SourceEnv, Env: "LIBRE_DCM_STORAGE_S3_ACCESS_KEY"},
		{Key: "storage.s3.secret_key", Value: s3Secret, Source: config.SourceEnv, Env: "LIBRE_DCM_STORAGE_S3_SECRET_KEY"},
		{Key: "index.driver", Value: "postgres", Source: config.SourceFile},
		{Key: "index.dsn", Value: "postgres://dbuser:" + dbPassword + "@db.example:5432/libredcm?sslmode=require&password=" + dbQueryPass,
			Source: config.SourceEnv, Env: "LIBRE_DCM_INDEX_DSN"},
		{Key: "events.driver", Value: "nats", Source: config.SourceFile},
		{Key: "events.url", Value: "nats://bususer:" + busPassword + "@nats-1:4222, nats://nats-2:4222", Source: config.SourceFile},
		{Key: "license.file", Value: "./data/license.lic", Source: config.SourceDefault},
		// A setting added later that nobody put on the allow-list.
		{Key: "storage.s3.session_token", Value: futureValue, Source: config.SourceFile},
		{Key: "future.unset_secret", Value: "", Source: config.SourceDefault},
		{Key: "future.number", Value: 42, Source: config.SourceFile},
	}
}

func newEnv(t *testing.T, token string) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{}
	idx, err := index.Open(ctx, "sqlite", "file:"+filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	e.idx = idx
	r := index.Record{PatientID: "P1", PatientName: "DOE^JANE", StudyUID: "S1", SeriesUID: "SE1", SOPInstanceUID: "I1",
		SOPClassUID: "1.2.840.10008.5.1.4.1.1.2", TransferSyntaxUID: "1.2.840.10008.1.2.1", BlobKey: "S1/SE1/I1.dcm"}
	if err := idx.Upsert(ctx, r); err != nil {
		t.Fatal(err)
	}
	in := r
	in.PatientID, in.SOPInstanceUID, in.BlobKey = "P2", "I2", "conflicts/S1/I2.dcm"
	chk, err := idx.CheckConflict(ctx, in)
	if err != nil || chk.Conflict == nil {
		t.Fatalf("check: %+v %v", chk, err)
	}
	if _, err := idx.RecordConflict(ctx, *chk.Conflict, in, index.StateOpen, false); err != nil {
		t.Fatal(err)
	}

	cfg := runtime.DefaultAdmin()
	cfg.Token, cfg.LogLevel, cfg.NodeID = token, "debug", "dcm-1"
	e.rt = runtime.New("LibreDCM", "0.3.0-test", cfg)
	e.rt.AddReadinessCheck("storage", func(context.Context) error { return nil })
	e.rt.AddReadinessCheck("events", func(context.Context) error {
		if e.readyErr == "" {
			return nil
		}
		return fmt.Errorf("%s", e.readyErr)
	})

	reg := e.rt.Registry
	e.m.active = prometheus.NewGauge(prometheus.GaugeOpts{Name: "libre_dcm_connections_active"})
	e.m.total = prometheus.NewCounter(prometheus.CounterOpts{Name: "libre_dcm_connections_total"})
	e.m.rejected = prometheus.NewCounter(prometheus.CounterOpts{Name: "libre_dcm_associations_rejected_total"})
	e.m.failures = prometheus.NewCounter(prometheus.CounterOpts{Name: "libre_dcm_event_publish_failures_total"})
	e.m.cstore = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "libre_dcm_cstore_total"}, []string{"result"})
	e.m.cfind = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "libre_dcm_cfind_total"}, []string{"level", "result"})
	e.m.subops = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "libre_dcm_retrieve_subops_total"}, []string{"op", "result"})
	e.m.outbox = prometheus.NewGauge(prometheus.GaugeOpts{Name: "libre_dcm_outbox_pending"})
	reg.MustRegister(e.m.active, e.m.total, e.m.rejected, e.m.failures, e.m.cstore, e.m.cfind, e.m.subops, e.m.outbox)

	e.live = &fakeLive{
		remotes:  map[string]gateway.RemoteAE{"VIEWER1": {Host: "10.0.0.20", Port: 104, AETitle: "VIEWER1"}, "ENVDEST": {Host: "10.0.0.30", Port: 11112, AETitle: "PACSB"}},
		rules:    []string{"MR1", "CT1@10.0.0.5"},
		enforced: true,
	}
	settings := testSettings()
	e.api = &API{
		Index: idx, Live: e.live,
		Node: Node{ID: e.rt.NodeID, AETitle: "LIBREDCM", Version: e.rt.Version, Started: time.Now().Add(-90 * time.Second),
			DICOMListen: ":11112", DICOMTLS: true, Storage: "s3", Index: "postgres", Events: "nats"},
		Ready: e.rt.Readiness, Metrics: reg,
		Settings: settings, RemoteAEEnv: map[string]bool{"ENVDEST": true},
		Secrets: append(SecretValues(settings), e.rt.AdminSecrets()...),
		Logs:    e.rt.Logs, Audit: e.rt.Audit,
	}
	e.api.Register(e.rt)
	e.h = e.rt.AdminHandler()
	return e
}

func (e *env) do(t *testing.T, method, path, token string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (e *env) get(t *testing.T, path string) map[string]any {
	t.Helper()
	rec, body := e.do(t, "GET", path, testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
	}
	return body
}

// Every control endpoint refuses a request without the admin token, with a
// wrong token, and on a node that has no token configured; with the token it
// answers JSON that must not be cached and names the node.
// Verifies: DCM-SRS-080, DCM-SRS-077
func TestReadOnlyEndpointsRequireToken(t *testing.T) {
	e := newEnv(t, testToken)
	for _, p := range readOnlyPaths {
		for _, tok := range []string{"", "wrong", testToken + "x", strings.ToLower(testToken)} {
			rec, body := e.do(t, "GET", p, tok)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("GET %s with token %q = %d, want 401", p, tok, rec.Code)
			}
			if len(body) != 1 || body["error"] != "unauthorized" {
				t.Errorf("GET %s refused with body %s", p, rec.Body)
			}
		}
		rec, body := e.do(t, "GET", p, testToken)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s with the token = %d %s", p, rec.Code, rec.Body)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("GET %s Cache-Control = %q, want no-store", p, cc)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("GET %s Content-Type = %q", p, ct)
		}
		inst, _ := body["instance"].(map[string]any)
		if inst["node_id"] != "dcm-1" || inst["ae_title"] != "LIBREDCM" || inst["version"] != "0.3.0-test" ||
			!strings.HasPrefix(fmt.Sprint(inst["config_digest"]), "sha256:") {
			t.Errorf("GET %s instance = %v", p, inst)
		}
		// Read-only: no other method is served on these paths.
		for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			if rec, _ := e.do(t, m, p, testToken); rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405", m, p, rec.Code)
			}
		}
	}

	// No token configured: the control API is off, also for a caller that
	// sends some token.
	open := newEnv(t, "")
	for _, p := range readOnlyPaths {
		for _, tok := range []string{"", "anything"} {
			if rec, _ := open.do(t, "GET", p, tok); rec.Code != http.StatusUnauthorized {
				t.Errorf("no token configured: GET %s = %d, want 401", p, rec.Code)
			}
		}
	}
}

// Discovery lists exactly the features this node implements.
// Verifies: DCM-SRS-080
func TestDiscovery(t *testing.T) {
	e := newEnv(t, testToken)
	body := e.get(t, "/control/v1")
	var got []string
	for _, f := range body["features"].([]any) {
		got = append(got, f.(string))
	}
	want := []string{"conflicts", "status", "config", "remote-aes", "allow-list", "audit", "logs",
		"echo", "reload", "license", "backup"}
	if body["api"] != "v1" || !reflect.DeepEqual(got, want) {
		t.Fatalf("discovery = %v", body)
	}
	// Each listed feature answers; a feature that is not listed is not served.
	// The action features are POST endpoints: a GET on them is refused (405),
	// which shows the route exists; "license" also has a GET.
	for _, f := range want {
		wantCode := http.StatusOK
		switch f {
		case "echo", "reload", "backup":
			wantCode = http.StatusMethodNotAllowed
		}
		if rec, _ := e.do(t, "GET", "/control/v1/"+f, testToken); rec.Code != wantCode {
			t.Errorf("feature %s listed but GET = %d, want %d", f, rec.Code, wantCode)
		}
	}
	for _, f := range []string{"studies", "send", "config/plan", "restart", "stop"} {
		if rec, _ := e.do(t, "GET", "/control/v1/"+f, testToken); rec.Code != http.StatusNotFound {
			t.Errorf("feature %s is not listed but GET = %d", f, rec.Code)
		}
		if rec, _ := e.do(t, "POST", "/control/v1/"+f, testToken); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("feature %s is not listed but POST = %d", f, rec.Code)
		}
	}
}

// Status reports identity, uptime, backends, each readiness check, licence
// state and the live counters, read at request time.
// Verifies: DCM-SRS-081
func TestStatus(t *testing.T) {
	e := newEnv(t, testToken)
	e.m.active.Set(2)
	e.m.total.Add(120)
	e.m.rejected.Add(4)
	e.m.failures.Add(1)
	e.m.outbox.Set(7)
	e.m.cstore.WithLabelValues("success").Add(2040)
	e.m.cstore.WithLabelValues("failure").Add(2)
	e.m.cstore.WithLabelValues("conflict-open").Add(3)
	e.m.cfind.WithLabelValues("STUDY", "success").Add(300)
	e.m.cfind.WithLabelValues("SERIES", "failure").Add(10)
	e.m.subops.WithLabelValues("move", "success").Add(500)
	e.m.subops.WithLabelValues("get", "success").Add(300)
	e.m.subops.WithLabelValues("move", "failure").Add(6)
	e.api.License = func() map[string]any {
		return map[string]any{"enforced": true, "state": "expired", "licensed": false, "ack_required": true, "message": "license expired", "days_left": 0}
	}

	body := e.get(t, "/control/v1/status")
	num := func(m map[string]any, k string) float64 { f, _ := m[k].(float64); return f }
	c := body["counters"].(map[string]any)
	for k, want := range map[string]float64{"associations_active": 2, "associations_total": 120, "associations_rejected_total": 4,
		"cfind_total": 310, "outbox_pending": 7, "event_publish_failures_total": 1, "conflicts_open": 1} {
		if num(c, k) != want {
			t.Errorf("counter %s = %v, want %v", k, c[k], want)
		}
	}
	if cs := c["cstore"].(map[string]any); num(cs, "success") != 2040 || num(cs, "failure") != 2 || num(cs, "conflict-open") != 3 || len(cs) != 3 {
		t.Errorf("cstore = %v", cs)
	}
	if so := c["retrieve_subops"].(map[string]any); num(so, "success") != 800 || num(so, "failure") != 6 {
		t.Errorf("retrieve_subops = %v", so)
	}
	if body["counters_incomplete"] != nil {
		t.Error("counters reported incomplete")
	}
	if up := num(body, "uptime_seconds"); up < 90 || up > 100 {
		t.Errorf("uptime = %v", body["uptime_seconds"])
	}
	if ts, err := time.Parse(time.RFC3339, fmt.Sprint(body["time"])); err != nil || time.Since(ts) > time.Minute {
		t.Errorf("time = %v", body["time"])
	}
	if b := body["backends"].(map[string]any); b["storage"] != "s3" || b["index"] != "postgres" || b["events"] != "nats" {
		t.Errorf("backends = %v", b)
	}
	d := body["dicom"].(map[string]any)
	if d["listen"] != ":11112" || d["tls"] != true || num(d, "allow_list_entries") != 2 || num(d, "remote_aes") != 2 {
		t.Errorf("dicom = %v", d)
	}
	if body["admin"].(map[string]any)["tls"] != false {
		t.Errorf("admin = %v", body["admin"])
	}
	if l := body["license"].(map[string]any); l["state"] != "expired" || l["ack_required"] != true || l["enforced"] != true {
		t.Errorf("license = %v", l)
	}
	checks := body["checks"].([]any)
	if body["ready"] != true || len(checks) != 2 || checks[0].(map[string]any)["name"] != "events" || checks[0].(map[string]any)["ok"] != true {
		t.Errorf("ready=%v checks=%v", body["ready"], checks)
	}

	// A failing dependency shows in the detail; its error text carries no credentials.
	e.readyErr = "nats: dial nats://bususer:" + busPassword + "@nats-1:4222: no servers available"
	e.m.cstore.WithLabelValues("success").Inc() // counters are live, not cached
	body = e.get(t, "/control/v1/status")
	ev := body["checks"].([]any)[0].(map[string]any)
	if body["ready"] != false || ev["ok"] != false {
		t.Fatalf("failing check not reported: %v", body["checks"])
	}
	msg := fmt.Sprint(ev["error"])
	if strings.Contains(msg, busPassword) || strings.Contains(msg, "bususer") || !strings.Contains(msg, "nats-1:4222") || !strings.Contains(msg, "no servers available") {
		t.Errorf("check error = %q, want host kept and credentials removed", msg)
	}
	if got := num(body["counters"].(map[string]any)["cstore"].(map[string]any), "success"); got != 2041 {
		t.Errorf("counter not read at request time: %v", got)
	}

	// Source build: no licence needed.
	e.api.License = nil
	if l := e.get(t, "/control/v1/status")["license"].(map[string]any); l["enforced"] != false || len(l) != 1 {
		t.Errorf("license without enforcement = %v", l)
	}
}

func settingsByKey(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, s := range body["settings"].([]any) {
		m := s.(map[string]any)
		out[m["key"].(string)] = m
	}
	return out
}

// The configuration view gives each setting with its source and whether it
// can change without a restart; secrets are never in it, and a setting that
// is not on the allow-list is hidden.
// Verifies: DCM-SRS-082
func TestConfigRedaction(t *testing.T) {
	e := newEnv(t, testToken)
	rec, body := e.do(t, "GET", "/control/v1/config", testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("config = %d", rec.Code)
	}
	s := settingsByKey(t, body)
	if len(s) != len(testSettings()) {
		t.Fatalf("%d settings returned, want %d", len(s), len(testSettings()))
	}

	// Plain settings: value, source, reloadable.
	if v := s["dicom.ae_title"]; v["value"] != "LIBREDCM" || v["source"] != "file" || v["reloadable"] != false || v["redacted"] != nil {
		t.Errorf("dicom.ae_title = %v", v)
	}
	if v := s["dicom.audit_omit_patient_id"]; v["value"] != false || v["source"] != "default" {
		t.Errorf("a false value must still be shown: %v", v)
	}
	if v := s["admin.tls_key_file"]; v["value"] != "/certs/tls.key" {
		t.Errorf("a key file path is shown (its content never is): %v", v)
	}

	// Secrets: redacted, only "set" is told.
	for _, k := range []string{"admin.token", "storage.s3.access_key", "storage.s3.secret_key", "storage.s3.session_token"} {
		v := s[k]
		if v["redacted"] != true || v["set"] != true || v["value"] != nil || v["redacted_parts"] != nil {
			t.Errorf("%s = %v, want redacted and set", k, v)
		}
	}
	if v := s["admin.token"]; v["source"] != "env" || v["env"] != "LIBRE_DCM_ADMIN_TOKEN" {
		t.Errorf("admin.token source = %v", v)
	}
	// Not on the allow-list: hidden even when it is not a string or is empty.
	if v := s["future.number"]; v["redacted"] != true || v["set"] != true || v["value"] != nil {
		t.Errorf("unlisted setting shown: %v", v)
	}
	if v := s["future.unset_secret"]; v["redacted"] != true || v["set"] != false {
		t.Errorf("unlisted empty setting = %v", v)
	}

	// DSN and URL: host kept, credentials and query gone.
	if v := s["index.dsn"]; v["value"] != "postgres://db.example:5432/libredcm" ||
		!reflect.DeepEqual(v["redacted_parts"], []any{"user", "password", "query"}) {
		t.Errorf("index.dsn = %v", v)
	}
	if v := s["events.url"]; v["value"] != "nats://nats-1:4222,nats://nats-2:4222" ||
		!reflect.DeepEqual(v["redacted_parts"], []any{"user", "password"}) {
		t.Errorf("events.url = %v", v)
	}

	// Live settings show what is in force, not what the node started with.
	ra := s["dicom.remote_aes"]
	if ra["reloadable"] != true || ra["source"] != "file" || strings.Contains(fmt.Sprint(ra["value"]), "STALE") ||
		!strings.Contains(fmt.Sprint(ra["value"]), "VIEWER1") {
		t.Errorf("dicom.remote_aes = %v", ra)
	}
	al := s["dicom.allowed_aes"]
	if al["reloadable"] != true || !reflect.DeepEqual(al["value"], []any{"MR1", "CT1@10.0.0.5"}) {
		t.Errorf("dicom.allowed_aes = %v", al)
	}
	reloadableCount := 0
	for _, v := range s {
		if v["reloadable"] == true {
			reloadableCount++
		}
	}
	if reloadableCount != 2 {
		t.Errorf("%d settings marked reloadable, want 2", reloadableCount)
	}
	if !strings.Contains(fmt.Sprint(body["restart_required_note"]), "restart") {
		t.Error("restart note missing")
	}
	for _, sec := range allSecrets {
		if strings.Contains(rec.Body.String(), sec) {
			t.Errorf("config response contains the secret %q", sec)
		}
	}
	if strings.Contains(rec.Body.String(), "dbuser") || strings.Contains(rec.Body.String(), "bususer") {
		t.Error("config response contains a user name from a DSN or URL")
	}
}

// The allow-list decides, not the name of the setting.
// Verifies: DCM-SRS-082
func TestViewSettingRules(t *testing.T) {
	cases := []struct {
		s        config.Setting
		value    any
		redacted bool
		parts    []string
	}{
		{config.Setting{Key: "index.dsn", Value: "file:./data/index.db"}, "file:./data/index.db", false, nil},
		{config.Setting{Key: "index.dsn", Value: "file:/var/lib/x.db?_pragma=busy_timeout(5000)"}, "file:/var/lib/x.db", false, []string{"query"}},
		{config.Setting{Key: "index.dsn", Value: "host=db user=u password=" + kvPassword + " dbname=x"}, nil, true, nil},
		{config.Setting{Key: "index.dsn", Value: "postgres://db/x"}, "postgres://db/x", false, nil},
		{config.Setting{Key: "index.dsn", Value: ""}, "", false, nil},
		{config.Setting{Key: "index.dsn", Value: 5}, nil, true, nil},
		{config.Setting{Key: "events.url", Value: "not a url"}, nil, true, nil},
		{config.Setting{Key: "events.url", Value: "nats://h:4222#frag"}, "nats://h:4222", false, []string{"fragment"}},
		{config.Setting{Key: "license.server", Value: "https://u:" + kvPassword + "@keys.example/api?token=abc"}, "https://keys.example/api", false, []string{"user", "password", "query"}},
		{config.Setting{Key: "admin.token", Value: ""}, nil, true, nil},
		{config.Setting{Key: "dicom.ae_title_typo", Value: "LIBREDCM"}, nil, true, nil},
		{config.Setting{Key: "DICOM.AE_TITLE", Value: "LIBREDCM"}, nil, true, nil},
	}
	for _, c := range cases {
		v := viewSetting(c.s)
		if v.Redacted != c.redacted || !reflect.DeepEqual(v.Value, c.value) || !reflect.DeepEqual(v.RedactedParts, c.parts) {
			t.Errorf("%s=%v -> value=%v redacted=%v parts=%v; want value=%v redacted=%v parts=%v",
				c.s.Key, c.s.Value, v.Value, v.Redacted, v.RedactedParts, c.value, c.redacted, c.parts)
		}
		if v.Redacted && v.Set == nil {
			t.Errorf("%s: redacted without the set flag", c.s.Key)
		}
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), kvPassword) {
			t.Errorf("%s: view contains the password: %s", c.s.Key, b)
		}
	}
	if SettingClass("index.dsn") != "dsn" || SettingClass("admin.token") != "" || SettingClass("dicom.ae_title") != "plain" {
		t.Error("SettingClass")
	}
	// SecretValues finds every secret of the test configuration.
	got := strings.Join(SecretValues(append(testSettings(),
		config.Setting{Key: "index.dsn", Value: "host=db user=u password=" + kvPassword + " dbname=x"})), "\n")
	for _, sec := range []string{testToken, s3Access, s3Secret, dbPassword, dbQueryPass, busPassword, futureValue, kvPassword} {
		if !strings.Contains(got, sec) {
			t.Errorf("SecretValues missed %q", sec)
		}
	}
	for _, notSecret := range []string{"LIBREDCM", "minio:9000", "postgres\n", "/certs/tls.key"} {
		if strings.Contains(got+"\n", notSecret) {
			t.Errorf("SecretValues treats %q as a secret", notSecret)
		}
	}
}

// No control response contains a configured secret, wherever it could come
// from: the configuration, a readiness error, a log line, an audit record or
// an error text. Every endpoint is searched for every secret.
// Verifies: DCM-SRS-082, DCM-SRS-080
func TestNoSecretInAnyResponse(t *testing.T) {
	e := newEnv(t, testToken)
	dsn := "postgres://dbuser:" + dbPassword + "@db.example:5432/libredcm"
	e.readyErr = "connect " + dsn + " failed; fallback host=db password=" + kvPassword + " token=" + testToken
	// A careless log line and audit record that name secrets.
	e.rt.Logger.Error("index unreachable", "dsn", dsn, "s3_secret", s3Secret, "note", "key "+s3Access+" refused")
	e.rt.Logger.Warn("bus down", "url", "nats://bususer:"+busPassword+"@nats-1:4222", "detail", []string{"x", futureValue})
	e.rt.Logger.Info("audit", "audit", true, "event", "Query", "outcome", "success",
		"calling_ae", "CT1", "debug", map[string]any{"token": testToken, "n": 1}, "q", "password="+dbQueryPass)

	paths := append([]string{}, readOnlyPaths...)
	paths = append(paths,
		"/control/v1/conflicts", "/control/v1/conflicts?state=all", "/control/v1/conflicts/none",
		"/control/v1/conflicts?state="+url.QueryEscape(testToken),
		"/control/v1/logs?level="+url.QueryEscape(s3Secret), "/control/v1/audit?since="+url.QueryEscape(dbPassword),
		"/control/v1/audit?limit="+url.QueryEscape(testToken), "/control/v1/logs?limit=1000", "/control/v1/audit?limit=500")
	searched := 0
	for _, p := range paths {
		rec, _ := e.do(t, "GET", p, testToken)
		raw := rec.Body.String()
		if !json.Valid(rec.Body.Bytes()) {
			t.Errorf("GET %s: response is not valid JSON after filtering: %s", p, raw)
		}
		for _, sec := range allSecrets {
			searched++
			if strings.Contains(raw, sec) {
				t.Errorf("GET %s (%d) contains the secret %q:\n%s", p, rec.Code, sec, raw)
			}
		}
		for _, user := range []string{"dbuser:", "bususer:"} {
			if strings.Contains(raw, user) {
				t.Errorf("GET %s contains URL credentials (%s)", p, user)
			}
		}
	}
	if searched != len(paths)*len(allSecrets) || len(paths) < 16 {
		t.Fatalf("searched %d combinations", searched)
	}
	// The lines are still there, with the rest of their content: the filter
	// removes the secret, not the record.
	logs := e.get(t, "/control/v1/logs")
	raw, _ := json.Marshal(logs["lines"])
	for _, want := range []string{"index unreachable", "db.example:5432/libredcm", "bus down", "nats-1:4222", "[redacted]"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("log lines lost %q: %s", want, raw)
		}
	}
	audit := e.get(t, "/control/v1/audit")
	if recs := audit["records"].([]any); len(recs) != 1 || recs[0].(map[string]any)["calling_ae"] != "CT1" {
		t.Errorf("audit records = %v", audit["records"])
	}
	// Also with a wrong token: the refusal itself must not echo anything.
	rec, _ := e.do(t, "GET", "/control/v1/config", "wrong")
	for _, sec := range allSecrets {
		if strings.Contains(rec.Body.String(), sec) {
			t.Errorf("401 body contains %q", sec)
		}
	}
}

// The filter changes string values only: keys, numbers and structure stay;
// a body it cannot read is not sent at all.
// Verifies: DCM-SRS-082
func TestScrub(t *testing.T) {
	in := `{"secretvalue":"key stays","n":12345678,"big":123456789012345678901234567890,"f":1.50,"ok":true,"nil":null,` +
		`"list":["a secretvalue b",{"deep":"xsecretvaluex"}],"url":"see https://u:p@h/x and nats://only-user@h2","short":"abc abcd",` +
		`"kv":"host=h password=hunter22 sslmode=x; TOKEN='t t' pwd=\"q q\"","n2":"12345678"}`
	out, ok := scrub([]byte(in), []string{"secretvalue", "12345678", "abc", "secretvalue b"})
	if !ok {
		t.Fatal("scrub refused valid JSON")
	}
	var got map[string]any
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("output is not JSON: %v: %s", err, out)
	}
	want := map[string]any{
		"secretvalue": "key stays", // keys are never rewritten
		"n":           json.Number("12345678"),
		"big":         json.Number("123456789012345678901234567890"),
		"f":           json.Number("1.50"),
		"ok":          true, "nil": nil,
		// "secretvalue b" is the longer secret: it is replaced whole.
		"list":  []any{"a [redacted]", map[string]any{"deep": "x[redacted]x"}},
		"url":   "see https://[redacted]@h/x and nats://[redacted]@h2",
		"short": "abc abcd", // shorter than the minimum: left alone
		"kv":    "host=h password=[redacted] sslmode=x; TOKEN=[redacted] pwd=[redacted]",
		"n2":    "[redacted]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scrub =\n%v\nwant\n%v", got, want)
	}
	for _, bad := range []string{``, `{"a":`, `not json`} {
		if _, ok := scrub([]byte(bad), nil); ok {
			t.Errorf("scrub accepted %q", bad)
		}
	}

	// A response that cannot be filtered is replaced by a plain 500.
	a := &API{Secrets: []string{"secretvalue"}}
	rec := httptest.NewRecorder()
	a.writeJSON(rec, http.StatusOK, map[string]any{"bad": func() {}, "leak": "secretvalue"})
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secretvalue") {
		t.Errorf("unencodable response: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	a.writeJSON(rec, http.StatusTeapot, map[string]any{"leak": "the secretvalue"})
	if rec.Code != http.StatusTeapot || strings.TrimSpace(rec.Body.String()) != `{"leak":"the [redacted]"}` {
		t.Errorf("writeJSON = %d %s", rec.Code, rec.Body)
	}
}

// Remote AEs and the allow-list are reported as they are in force, each
// entry with its source; the fail-closed state of an invalid list is told;
// the config digest follows the live settings.
// Verifies: DCM-SRS-083
func TestRemoteAEsAndAllowList(t *testing.T) {
	e := newEnv(t, testToken)
	body := e.get(t, "/control/v1/remote-aes")
	want := []any{
		map[string]any{"name": "ENVDEST", "host": "10.0.0.30", "port": float64(11112), "ae_title": "PACSB", "source": "env"},
		map[string]any{"name": "VIEWER1", "host": "10.0.0.20", "port": float64(104), "ae_title": "VIEWER1", "source": "file"},
	}
	if !reflect.DeepEqual(body["remote_aes"], want) {
		t.Fatalf("remote-aes = %v", body["remote_aes"])
	}
	digest := func() string {
		return e.get(t, "/control/v1")["instance"].(map[string]any)["config_digest"].(string)
	}
	d0 := digest()
	if d0 != body["instance"].(map[string]any)["config_digest"] || d0 != digest() {
		t.Fatal("digest is not stable")
	}

	al := e.get(t, "/control/v1/allow-list")
	if al["enforced"] != true || al["invalid"] != nil || !reflect.DeepEqual(al["entries"], []any{
		map[string]any{"rule": "MR1", "source": "file"}, map[string]any{"rule": "CT1@10.0.0.5", "source": "file"}}) {
		t.Fatalf("allow-list = %v", al)
	}

	// Order of allow-list rules does not matter to the digest; content does.
	e.live.rules = []string{"CT1@10.0.0.5", "MR1"}
	if digest() != d0 {
		t.Error("digest depends on the order of allow-list rules")
	}
	e.live.rules = []string{"CT1@10.0.0.6", "MR1"}
	d1 := digest()
	if d1 == d0 {
		t.Error("digest did not change with an allow-list rule")
	}
	e.live.remotes["VIEWER1"] = gateway.RemoteAE{Host: "10.0.0.21", Port: 104, AETitle: "VIEWER1"}
	d2 := digest()
	if d2 == d1 {
		t.Error("digest did not change with a destination address")
	}
	if got := e.get(t, "/control/v1/remote-aes")["remote_aes"].([]any)[1].(map[string]any)["host"]; got != "10.0.0.21" {
		t.Errorf("remote-aes shows %v, not what is in force", got)
	}

	// Empty list: not enforced, and the note says what that means.
	e.live.rules, e.live.enforced = nil, false
	al = e.get(t, "/control/v1/allow-list")
	if al["enforced"] != false || len(al["entries"].([]any)) != 0 || !strings.Contains(fmt.Sprint(al["note"]), "every calling AE") {
		t.Fatalf("empty allow-list = %v", al)
	}
	dOpen := digest()
	// Invalid list at start: enforced with no rules; a different state from "open".
	e.live.enforced = true
	al = e.get(t, "/control/v1/allow-list")
	if al["enforced"] != true || al["invalid"] != true || !strings.Contains(fmt.Sprint(al["note"]), "refused") {
		t.Fatalf("invalid allow-list = %v", al)
	}
	if digest() == dOpen {
		t.Error("digest is the same for an open list and a fail-closed list")
	}
	e.live.remotes = nil
	if got := e.get(t, "/control/v1/remote-aes")["remote_aes"].([]any); len(got) != 0 {
		t.Errorf("no destinations = %v", got)
	}

	// Allow-list from the environment.
	for i := range e.api.Settings {
		if e.api.Settings[i].Key == "dicom.allowed_aes" {
			e.api.Settings[i].Source = config.SourceEnv
		}
	}
	e.live.rules = []string{"CT9"}
	if got := e.get(t, "/control/v1/allow-list")["entries"].([]any)[0].(map[string]any)["source"]; got != "env" {
		t.Errorf("allow-list source = %v, want env", got)
	}
}

func fieldValues(t *testing.T, body map[string]any, key, field string) []string {
	t.Helper()
	var out []string
	for _, r := range body[key].([]any) {
		out = append(out, fmt.Sprint(r.(map[string]any)[field]))
	}
	return out
}

// Audit records are filtered by event type (prefix), outcome and time, and
// limited in number; the newest are returned, oldest first.
// Verifies: DCM-SRS-084
func TestAuditFiltersAndBounds(t *testing.T) {
	e := newEnv(t, testToken)
	log := e.rt.Logger
	log.Info("audit", "audit", true, "event", "DICOMInstancesTransferred.received", "outcome", "success", "n", 1)
	log.Info("audit", "audit", true, "event", "Query", "outcome", "success", "n", 2)
	log.Info("audit", "audit", true, "event", "SecurityAlert.association-rejected", "outcome", "minor-failure", "n", 3)
	time.Sleep(20 * time.Millisecond)
	mark := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	log.Info("audit", "audit", true, "event", "DICOMInstancesTransferred.sent", "outcome", "serious-failure", "n", 4)
	log.Info("audit", "audit", true, "event", "DICOMInstancesTransferred.received", "outcome", "success", "n", 5)
	log.Info("an ordinary line", "n", 6)

	all := e.get(t, "/control/v1/audit")
	if got := fieldValues(t, all, "records", "n"); !reflect.DeepEqual(got, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("all records = %v", got)
	}
	if all["count"] != float64(5) || all["more"] != false || all["limit"] != float64(100) ||
		all["buffer_size"] != float64(runtime.AuditRingSize) || all["audit_log_off"] != false {
		t.Fatalf("envelope = %v", all)
	}
	for q, want := range map[string][]string{
		"type=DICOMInstancesTransferred":          {"1", "4", "5"},
		"type=DICOMInstancesTransferred.received": {"1", "5"},
		"type=Query":            {"2"},
		"type=query":            nil, // event names are case-sensitive
		"type=Nothing":          nil,
		"outcome=success":       {"1", "2", "5"},
		"outcome=minor-failure": {"3"},
		"outcome=succ":          nil, // outcome is matched whole
		"type=DICOMInstancesTransferred&outcome=success":                                               {"1", "5"},
		"since=" + url.QueryEscape(mark.Format(time.RFC3339Nano)):                                      {"4", "5"},
		"since=" + url.QueryEscape(mark.Format(time.RFC3339Nano)) + "&outcome=success":                 {"5"},
		"since=" + url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339)):               nil,
		"since=" + url.QueryEscape(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)) + "&limit=2": {"4", "5"},
		"limit=1": {"5"},
	} {
		body := e.get(t, "/control/v1/audit?"+q)
		got := fieldValues(t, body, "records", "n")
		if !reflect.DeepEqual(got, want) {
			t.Errorf("audit?%s = %v, want %v", q, got, want)
		}
		if body["count"] != float64(len(want)) {
			t.Errorf("audit?%s count = %v", q, body["count"])
		}
	}
	if body := e.get(t, "/control/v1/audit?limit=2"); body["more"] != true {
		t.Error("more is false although older records were left out")
	}
	for _, q := range []string{"limit=0", "limit=-1", "limit=abc", "limit=1.5", "since=yesterday", "since=2026-11-14"} {
		rec, body := e.do(t, "GET", "/control/v1/audit?"+q, testToken)
		if rec.Code != http.StatusBadRequest || body["code"] == nil || body["error"] == nil {
			t.Errorf("audit?%s = %d %s, want 400 with a code", q, rec.Code, rec.Body)
		}
	}

	// Bounds: the ring never holds more than its size, and no request gets
	// more than the maximum, whatever limit it asks for.
	for i := 0; i < runtime.AuditRingSize+250; i++ {
		log.Info("audit", "audit", true, "event", "Query", "outcome", "success", "seq", i)
	}
	if n := e.rt.Audit.Len(); n != runtime.AuditRingSize {
		t.Fatalf("audit ring holds %d records, size is %d", n, runtime.AuditRingSize)
	}
	body := e.get(t, "/control/v1/audit?limit=100000")
	seq := fieldValues(t, body, "records", "seq")
	if len(seq) != auditMaxLimit || body["limit"] != float64(auditMaxLimit) || body["more"] != true {
		t.Fatalf("limit=100000 returned %d records (limit %v more %v), want %d", len(seq), body["limit"], body["more"], auditMaxLimit)
	}
	if last := fmt.Sprint(runtime.AuditRingSize + 249); seq[len(seq)-1] != last {
		t.Fatalf("newest record is seq %s, want %s", seq[len(seq)-1], last)
	}
	if got := len(e.get(t, "/control/v1/audit")["records"].([]any)); got != auditDefaultLimit {
		t.Fatalf("default limit returned %d records, want %d", got, auditDefaultLimit)
	}
	// The oldest records are gone: the ring is a view, not the audit trail.
	if got := fieldValues(t, e.get(t, "/control/v1/audit?type=DICOM"), "records", "n"); len(got) != 0 {
		t.Errorf("records older than the ring are still returned: %v", got)
	}

	// audit_log_off is told to the client.
	e.api.Node.AuditLogOff = true
	if body := e.get(t, "/control/v1/audit"); body["audit_log_off"] != true || !strings.Contains(fmt.Sprint(body["note"]), "audit_log_off") {
		t.Errorf("audit_log_off not reported: %v", body["note"])
	}
}

// Log lines are filtered by minimum level and time and limited in number;
// audit records are not among them.
// Verifies: DCM-SRS-085
func TestLogFiltersAndBounds(t *testing.T) {
	e := newEnv(t, testToken)
	log := e.rt.Logger
	log.Debug("d1")
	log.Info("i1")
	log.Warn("w1")
	log.Info("audit", "audit", true, "event", "Query", "outcome", "success")
	time.Sleep(20 * time.Millisecond)
	mark := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	log.Error("e1")
	log.Info("i2")

	all := e.get(t, "/control/v1/logs")
	if got := fieldValues(t, all, "lines", "msg"); !reflect.DeepEqual(got, []string{"d1", "i1", "w1", "e1", "i2"}) {
		t.Fatalf("all lines = %v (audit records must be left out)", got)
	}
	if all["buffer_size"] != float64(runtime.LogRingSize) || all["limit"] != float64(200) || all["more"] != false {
		t.Fatalf("envelope = %v", all)
	}
	if l := all["lines"].([]any)[0].(map[string]any); l["node"] != "dcm-1" || l["module"] != "LibreDCM" || l["level"] != "DEBUG" || l["time"] == nil {
		t.Fatalf("line = %v, want the fields the logger wrote", l)
	}
	for q, want := range map[string][]string{
		"level=debug": {"d1", "i1", "w1", "e1", "i2"},
		"level=info":  {"i1", "w1", "e1", "i2"},
		"level=warn":  {"w1", "e1"},
		"level=WARN":  {"w1", "e1"},
		"level=error": {"e1"},
		"since=" + url.QueryEscape(mark.Format(time.RFC3339Nano)):             {"e1", "i2"},
		"level=error&since=" + url.QueryEscape(mark.Format(time.RFC3339Nano)): {"e1"},
		"level=warn&limit=1": {"e1"},
		"limit=2":            {"e1", "i2"},
		"since=" + url.QueryEscape(time.Now().Add(time.Hour).Format(time.RFC3339)): nil,
	} {
		if got := fieldValues(t, e.get(t, "/control/v1/logs?"+q), "lines", "msg"); !reflect.DeepEqual(got, want) {
			t.Errorf("logs?%s = %v, want %v", q, got, want)
		}
	}
	for _, q := range []string{"level=trace", "level=info%2B2", "level=4", "limit=0", "limit=x", "since=now"} {
		rec, body := e.do(t, "GET", "/control/v1/logs?"+q, testToken)
		if rec.Code != http.StatusBadRequest || body["code"] == nil {
			t.Errorf("logs?%s = %d %s, want 400 with a code", q, rec.Code, rec.Body)
		}
	}

	for i := 0; i < runtime.LogRingSize+300; i++ {
		log.Info("flood", "seq", i)
	}
	if n := e.rt.Logs.Len(); n != runtime.LogRingSize {
		t.Fatalf("log ring holds %d lines, size is %d", n, runtime.LogRingSize)
	}
	body := e.get(t, "/control/v1/logs?limit=999999")
	seq := fieldValues(t, body, "lines", "seq")
	if len(seq) != logsMaxLimit || body["limit"] != float64(logsMaxLimit) || body["more"] != true {
		t.Fatalf("limit=999999 returned %d lines, want %d", len(seq), logsMaxLimit)
	}
	if last := fmt.Sprint(runtime.LogRingSize + 299); seq[len(seq)-1] != last {
		t.Fatalf("newest line is seq %s, want %s", seq[len(seq)-1], last)
	}
	if got := len(e.get(t, "/control/v1/logs")["lines"].([]any)); got != logsDefaultLimit {
		t.Fatalf("default limit returned %d lines, want %d", got, logsDefaultLimit)
	}
	if got := fieldValues(t, e.get(t, "/control/v1/logs?level=warn"), "lines", "msg"); len(got) != 0 {
		t.Errorf("lines older than the ring are still returned: %v", got)
	}
}

// Reading changes nothing: after every GET the live settings, the conflict
// list, the counters and the two rings are as they were.
// Verifies: DCM-SRS-080
func TestGetNeverChangesState(t *testing.T) {
	e := newEnv(t, testToken)
	e.rt.Logger.Info("one line")
	e.rt.Logger.Info("audit", "audit", true, "event", "Query", "outcome", "success")
	snapshot := func() string {
		conflicts, err := e.idx.ListConflicts(context.Background(), "all", 0)
		if err != nil {
			t.Fatal(err)
		}
		families, err := e.rt.Registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		var metrics []string
		for _, f := range families {
			if strings.HasPrefix(f.GetName(), "libre_dcm_") {
				metrics = append(metrics, f.String())
			}
		}
		rules, enforced := e.live.AllowedAEs()
		b, _ := json.Marshal([]any{conflicts, metrics, e.live.RemoteAEs(), rules, enforced, e.rt.Logs.Len(), e.rt.Audit.Len()})
		return string(b)
	}
	before := snapshot()
	for i := 0; i < 3; i++ {
		for _, p := range append(append([]string{}, readOnlyPaths...), "/control/v1/conflicts", "/control/v1/logs?limit=bad") {
			e.do(t, "GET", p, testToken)
			e.do(t, "GET", p, "wrong")
		}
	}
	if after := snapshot(); after != before {
		t.Fatalf("state changed by GET requests:\nbefore %s\nafter  %s", before, after)
	}
}
