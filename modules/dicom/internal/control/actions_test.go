package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/licensing"
	"github.com/mulcahyio/libre-rws/platform/runtime"
)

// ---- fakes and helpers for the action endpoints ----

type fakeEcho struct {
	mu    sync.Mutex
	calls []EchoTarget
	stage string
	err   error
	block chan struct{} // when set, a call waits here
	left  time.Duration // time left on the context of the last call
}

func (f *fakeEcho) run(ctx context.Context, t EchoTarget) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, t)
	if dl, ok := ctx.Deadline(); ok {
		f.left = time.Until(dl)
	}
	block, stage, err := f.block, f.stage, f.err
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return stage, err
}

func (f *fakeEcho) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

// fakeReloader stands for the node's reloader: it holds what the
// configuration file would load and applies it to the fake live settings.
type fakeReloader struct {
	live    *fakeLive
	next    LiveSettings
	err     error
	applied int
}

func (f *fakeReloader) Preview() (LiveSettings, error) { return f.next, f.err }

func (f *fakeReloader) Apply(check func(LiveSettings) error) (LiveSettings, error) {
	if f.err != nil {
		return f.next, f.err
	}
	if check != nil {
		if err := check(f.next); err != nil {
			return f.next, err
		}
	}
	f.live.remotes = map[string]gateway.RemoteAE{}
	for k, v := range f.next.RemoteAEs {
		if v.AETitle == "" {
			v.AETitle = k
		}
		f.live.remotes[k] = v
	}
	f.live.rules, f.live.enforced = append([]string(nil), f.next.AllowedAEs...), len(f.next.AllowedAEs) > 0
	f.applied++
	return f.next, nil
}

// fakeLicence records what the licence endpoints ask for.
type fakeLicence struct {
	mu        sync.Mutex
	detail    licensing.Detail
	acks      []string
	requests  [][2]string
	installs  []string
	activates [][2]string
	err       error
	block     chan struct{}
}

func (f *fakeLicence) Detail() licensing.Detail { f.mu.Lock(); defer f.mu.Unlock(); return f.detail }
func (f *fakeLicence) AcknowledgeBy(by string) (licensing.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.detail, f.err
	}
	f.acks = append(f.acks, by)
	f.detail.AckRequired = false
	return f.detail, nil
}
func (f *fakeLicence) RequestText(key, site string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, [2]string{key, site})
	return "-----BEGIN LIBREKEY REQUEST-----\nZW5jb2RlZA==\n-----END LIBREKEY REQUEST-----\n", f.err
}
func (f *fakeLicence) InstallText(text string) (licensing.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.detail, f.err
	}
	f.installs = append(f.installs, text)
	f.detail.State, f.detail.Licensed = "valid", true
	return f.detail, nil
}
func (f *fakeLicence) ActivateOnline(_ context.Context, key, site string) (licensing.Detail, error) {
	f.mu.Lock()
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.detail, f.err
	}
	f.activates = append(f.activates, [2]string{key, site})
	f.detail.State, f.detail.Licensed = "valid", true
	return f.detail, nil
}

type actEnv struct {
	*env
	echo *fakeEcho
	rl   *fakeReloader
	lic  *fakeLicence
	dir  string // backup directory
}

// newActEnv is newEnv plus everything the action endpoints need: the node's
// logger as audit log, a fake C-ECHO, reloader and licence, and a real
// backup of the real SQLite index into a temporary directory.
func newActEnv(t *testing.T) *actEnv {
	t.Helper()
	e := newEnv(t, testToken)
	a := &actEnv{env: e, echo: &fakeEcho{}, dir: t.TempDir()}
	a.rl = &fakeReloader{live: e.live, next: LiveSettings{RemoteAEs: e.live.RemoteAEs(), AllowedAEs: append([]string(nil), e.live.rules...)}}
	a.lic = &fakeLicence{detail: licensing.Detail{Enforced: true, State: "expired", AckRequired: true, Message: "license expired",
		Fingerprint: "fp1-aaaaaaaaaaaa-bbbbbbbbbbbb-cccccccccccc", KeyLast4: "0001", Customer: "Synthetic Clinic"}}
	sql, ok := e.idx.(interface {
		Backup(context.Context, string) error
	})
	if !ok {
		t.Fatal("test index cannot be backed up")
	}
	e.api.AuditLog = e.rt.Logger
	e.api.Echo = a.echo.run
	e.api.Reloader = a.rl
	e.api.Licence = a.lic
	e.api.Backup = &Backup{Dir: a.dir, Driver: "sqlite", Snapshot: sql.Backup,
		IndexBytes: func() (int64, error) { return 4096, nil }}
	return a
}

var ridSeq int

// rid makes a fresh, valid request ID.
func rid() string {
	ridSeq++
	return fmt.Sprintf("req-%04d-abcdefghij", ridSeq)
}

// act is an action body: the envelope plus fields.
func act(id string, kv ...any) map[string]any {
	m := map[string]any{"operator": "Pat Admin", "reason": "routine test", "request_id": id}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func (e *env) post(t *testing.T, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return e.postAs(t, path, testToken, body)
}

func (e *env) postAs(t *testing.T, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case string:
		raw = []byte(b)
	case []byte:
		raw = b
	default:
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// auditRecs returns the audit records in the ring, oldest first.
func auditRecs(t *testing.T, ring *runtime.LogBuffer) []map[string]any {
	t.Helper()
	entries, _ := ring.Select(10000, nil)
	var out []map[string]any
	for _, en := range entries {
		m := map[string]any{}
		if err := json.Unmarshal(en.Line, &m); err != nil {
			t.Fatalf("audit line is not JSON: %s", en.Line)
		}
		out = append(out, m)
	}
	return out
}

// findAudit returns the audit records whose fields equal want.
func findAudit(recs []map[string]any, want map[string]any) []map[string]any {
	var out []map[string]any
next:
	for _, r := range recs {
		for k, v := range want {
			if !reflect.DeepEqual(r[k], v) {
				continue next
			}
		}
		out = append(out, r)
	}
	return out
}

// Every action endpoint with a body that would be accepted if the envelope
// were complete.
func actionEndpoints() map[string]map[string]any {
	return map[string]map[string]any{
		"/control/v1/echo":             {"remote_ae": "VIEWER1"},
		"/control/v1/reload":           {"confirm": true, "plan_digest": "sha256:x"},
		"/control/v1/license/ack":      {},
		"/control/v1/license/request":  {"key": "LK-SYNTH-0000-0001"}, // gitleaks:allow (synthetic test value)
		"/control/v1/license/install":  {"license": "-----BEGIN LIBREKEY LICENSE-----\nx\n-----END LIBREKEY LICENSE-----"},
		"/control/v1/license/activate": {"key": "LK-SYNTH-0000-0001"}, // gitleaks:allow (synthetic test value)
		"/control/v1/backup":           {},
	}
}

// nothingDone fails the test when any action had an effect.
func (a *actEnv) nothingDone(t *testing.T, what string) {
	t.Helper()
	if n := a.echo.count(); n != 0 {
		t.Errorf("%s: C-ECHO ran %d times", what, n)
	}
	if a.rl.applied != 0 {
		t.Errorf("%s: reload applied", what)
	}
	a.lic.mu.Lock()
	if len(a.lic.acks)+len(a.lic.requests)+len(a.lic.installs)+len(a.lic.activates) != 0 {
		t.Errorf("%s: a licence function ran", what)
	}
	a.lic.mu.Unlock()
	if files := dirNames(t, a.dir); len(files) != 0 {
		t.Errorf("%s: backup directory holds %v", what, files)
	}
}

// ---- DCM-SRS-086: operator, reason, request ID, audit ----

// Every action is refused without the admin token, and without an operator
// name, a reason or a valid request ID; a refused request does nothing and
// leaves an audit record that names the endpoint and why.
// Verifies: DCM-SRS-086, DCM-SRS-080
func TestActionsRequireTokenOperatorReasonAndRequestID(t *testing.T) {
	a := newActEnv(t)
	long := strings.Repeat("x", 501)
	bad := []struct {
		name string
		edit func(m map[string]any)
		code string
	}{
		{"no operator", func(m map[string]any) { delete(m, "operator") }, "operator_required"},
		{"blank operator", func(m map[string]any) { m["operator"] = "  \t " }, "operator_required"},
		{"long operator", func(m map[string]any) { m["operator"] = long }, "operator_invalid"},
		{"no reason", func(m map[string]any) { delete(m, "reason") }, "reason_required"},
		{"blank reason", func(m map[string]any) { m["reason"] = "\n" }, "reason_required"},
		{"long reason", func(m map[string]any) { m["reason"] = long }, "reason_invalid"},
		{"no request id", func(m map[string]any) { delete(m, "request_id") }, "request_id_invalid"},
		{"short request id", func(m map[string]any) { m["request_id"] = "short-id" }, "request_id_invalid"},
		{"long request id", func(m map[string]any) { m["request_id"] = strings.Repeat("a", 65) }, "request_id_invalid"},
		{"request id with a space", func(m map[string]any) { m["request_id"] = "has space in request id" }, "request_id_invalid"},
	}
	for path, fields := range actionEndpoints() {
		for _, tok := range []string{"", "wrong-token"} {
			m := act(rid())
			for k, v := range fields {
				m[k] = v
			}
			if rec, _ := a.postAs(t, path, tok, m); rec.Code != http.StatusUnauthorized {
				t.Errorf("POST %s with token %q = %d, want 401", path, tok, rec.Code)
			}
		}
		for _, c := range bad {
			m := act(rid())
			for k, v := range fields {
				m[k] = v
			}
			c.edit(m)
			rec, body := a.post(t, path, m)
			if rec.Code != http.StatusUnprocessableEntity || body["code"] != c.code {
				t.Errorf("POST %s, %s = %d %v, want 422 %s", path, c.name, rec.Code, body, c.code)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("POST %s refused without Cache-Control: no-store", path)
			}
			if inst, _ := body["instance"].(map[string]any); inst["node_id"] != "dcm-1" {
				t.Errorf("POST %s refusal does not name the node: %v", path, body)
			}
		}
		// Malformed bodies: unknown field, wrong type, text after the object, too large.
		for name, raw := range map[string]string{
			"unknown field": `{"operator":"a","reason":"b","request_id":"0123456789abcdef","surprise":1}`,
			"wrong type":    `{"operator":7,"reason":"b","request_id":"0123456789abcdef"}`,
			"trailing":      `{"operator":"a","reason":"b","request_id":"0123456789abcdef"} {"again":1}`,
			"not json":      `operator=a`,
			"too large":     `{"operator":"a","reason":"` + strings.Repeat("r", 300<<10) + `","request_id":"0123456789abcdef"}`,
		} {
			if rec, body := a.post(t, path, raw); rec.Code != http.StatusBadRequest || body["code"] != "invalid_body" {
				t.Errorf("POST %s, %s = %d %v, want 400 invalid_body", path, name, rec.Code, body)
			}
		}
		if n := len(findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditRefused, "endpoint": path, "outcome": "minor-failure"})); n != len(bad)+5 {
			t.Errorf("%s: %d refusal audit records, want %d", path, n, len(bad)+5)
		}
	}
	a.nothingDone(t, "refused requests")
	// A refusal record carries what was given, so the attempt can be traced.
	recs := findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditRefused, "code": "reason_required", "endpoint": "/control/v1/backup"})
	if len(recs) == 0 || recs[0]["operator"] != "Pat Admin" || recs[0]["audit"] != true {
		t.Fatalf("refusal record = %v", recs)
	}
}

// A done action writes two audit records: one before it starts and one with
// the outcome. Both carry operator, reason and request ID. They are written
// also when the node's log level would drop an ordinary info line.
// Verifies: DCM-SRS-086
func TestActionAuditRecords(t *testing.T) {
	a := newActEnv(t)
	// A node that logs errors only.
	cfg := runtime.DefaultAdmin()
	cfg.LogLevel, cfg.NodeID = "error", "quiet-node"
	quiet := runtime.New("LibreDCM", "0.3.0-test", cfg)
	quiet.Logger.Info("an ordinary info line")
	a.api.AuditLog = quiet.Logger
	if quiet.Logs.Len() != 0 {
		t.Fatal("precondition: info lines should be dropped at level error")
	}

	id := rid()
	rec, body := a.post(t, "/control/v1/echo", act(id, "remote_ae", "VIEWER1", "operator", " Pat\tAdmin\n", "reason", "new viewer\r\ninstalled"))
	if rec.Code != http.StatusOK || body["ok"] != true {
		t.Fatalf("echo = %d %v", rec.Code, body)
	}
	recs := auditRecs(t, quiet.Audit)
	if len(recs) != 2 {
		t.Fatalf("audit records = %d, want 2 (start and result): %v", len(recs), recs)
	}
	start, result := recs[0], recs[1]
	if start["phase"] != "start" || start["outcome"] != "pending" || result["phase"] != "result" || result["outcome"] != "success" {
		t.Fatalf("phases: %v / %v", start, result)
	}
	for _, r := range recs {
		// Control characters in the name and reason are replaced: one record, one line.
		if r["event"] != auditVerification || r["audit"] != true || r["operator"] != "Pat Admin" ||
			r["reason"] != "new viewer  installed" || r["request_id"] != id || r["endpoint"] != "/control/v1/echo" ||
			r["component"] != "control" || r["node"] != "quiet-node" {
			t.Errorf("audit record = %v", r)
		}
	}
	if result["target"] != "10.0.0.20:104" || result["called_ae"] != "VIEWER1" || result["result"] != "success" {
		t.Errorf("result record = %v", result)
	}

	// A failed test is recorded as a failure, with the stage.
	a.echo.stage, a.echo.err = StageConnect, errors.New("dial tcp 10.0.0.20:104: connect: connection refused")
	if rec, body := a.post(t, "/control/v1/echo", act(rid(), "remote_ae", "VIEWER1")); rec.Code != 200 || body["ok"] != false {
		t.Fatalf("failed echo = %d %v", rec.Code, body)
	}
	recs = auditRecs(t, quiet.Audit)
	last := recs[len(recs)-1]
	if len(recs) != 4 || last["outcome"] != "minor-failure" || last["stage"] != "connect" || last["result"] != "connection refused" {
		t.Fatalf("failure record = %v", last)
	}
}

// failingHandler is a log handler whose sink is broken.
type failingHandler struct{ slog.Handler }

func (failingHandler) Handle(context.Context, slog.Record) error {
	return errors.New("log sink closed")
}

// When the audit record cannot be written, the action is not done; and it
// can be sent again with the same request ID once the log works.
// Verifies: DCM-SRS-086
func TestActionNotDoneWhenAuditCannotBeWritten(t *testing.T) {
	a := newActEnv(t)
	working := a.api.AuditLog
	for name, log := range map[string]*slog.Logger{
		"broken sink": slog.New(failingHandler{slog.NewJSONHandler(io.Discard, nil)}),
		"no logger":   nil,
	} {
		a.api.AuditLog = log
		ids := map[string]string{}
		for path, fields := range actionEndpoints() {
			id := rid()
			ids[path] = id
			m := act(id)
			for k, v := range fields {
				m[k] = v
			}
			rec, body := a.post(t, path, m)
			if rec.Code != http.StatusInternalServerError || body["code"] != "audit_unavailable" {
				t.Errorf("%s: POST %s = %d %v, want 500 audit_unavailable", name, path, rec.Code, body)
			}
		}
		a.nothingDone(t, name)
		// Not remembered: the same request works once the log does.
		a.api.AuditLog = working
		rec, body := a.post(t, "/control/v1/echo", act(ids["/control/v1/echo"], "remote_ae", "VIEWER1"))
		if rec.Code != 200 || body["ok"] != true || rec.Header().Get("Idempotent-Replay") != "" {
			t.Fatalf("%s: retry after the log works = %d %v", name, rec.Code, body)
		}
		a.echo.mu.Lock()
		a.echo.calls = nil
		a.echo.mu.Unlock()
	}
}

// A request ID sent again returns the first result and does nothing again;
// used for another request it is refused; after 10 minutes it is forgotten.
// Verifies: DCM-SRS-086
func TestActionRequestIDIsRememberedForTenMinutes(t *testing.T) {
	a := newActEnv(t)
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	a.api.requests.now = func() time.Time { return now }

	id := rid()
	body := act(id, "remote_ae", "VIEWER1")
	first, _ := a.post(t, "/control/v1/echo", body)
	if first.Code != 200 || a.echo.count() != 1 {
		t.Fatalf("first = %d, echo ran %d times", first.Code, a.echo.count())
	}
	audits := len(auditRecs(t, a.rt.Audit))
	// The peer now fails: a replay must still give the first answer.
	a.echo.stage, a.echo.err = StageConnect, errors.New("down")
	for i := 0; i < 3; i++ {
		now = now.Add(3 * time.Minute) // 3, 6, 9 minutes after the first
		again, _ := a.post(t, "/control/v1/echo", body)
		if again.Code != first.Code || again.Body.String() != first.Body.String() || again.Header().Get("Idempotent-Replay") != "true" {
			t.Fatalf("repeat %d = %d %s (replay header %q), want the first result", i, again.Code, again.Body, again.Header().Get("Idempotent-Replay"))
		}
	}
	if a.echo.count() != 1 {
		t.Fatalf("a repeated request ID ran the action again: %d runs", a.echo.count())
	}
	if n := len(auditRecs(t, a.rt.Audit)); n != audits {
		t.Fatalf("a replay wrote %d audit records", n-audits)
	}
	if first.Header().Get("Idempotent-Replay") != "" {
		t.Fatal("the first answer is marked as a replay")
	}

	// The same ID for a different request (other target, other endpoint, other reason) is refused.
	for path, other := range map[string]map[string]any{
		"/control/v1/echo":   act(id, "remote_ae", "ENVDEST"),
		"/control/v1/backup": act(id),
	} {
		rec, b := a.post(t, path, other)
		if rec.Code != http.StatusConflict || b["code"] != "request_id_reused" {
			t.Fatalf("reused request id on %s = %d %v", path, rec.Code, b)
		}
	}
	if rec, b := a.post(t, "/control/v1/echo", act(id, "remote_ae", "VIEWER1", "reason", "another reason")); rec.Code != 409 || b["code"] != "request_id_reused" {
		t.Fatalf("same id, other reason = %d %v", rec.Code, b)
	}
	if a.echo.count() != 1 || len(dirNames(t, a.dir)) != 0 {
		t.Fatal("a reused request ID ran an action")
	}

	// A failed action is remembered too: its failure is the first result.
	a.echo.stage, a.echo.err = "", nil
	unknown := act(rid(), "remote_ae", "NOPE")
	r1, _ := a.post(t, "/control/v1/echo", unknown)
	a.live.remotes["NOPE"] = gateway.RemoteAE{Host: "10.9.9.9", Port: 104, AETitle: "NOPE"}
	r2, _ := a.post(t, "/control/v1/echo", unknown)
	if r1.Code != 404 || r2.Code != 404 || r2.Header().Get("Idempotent-Replay") != "true" || a.echo.count() != 1 {
		t.Fatalf("failed action replay = %d then %d", r1.Code, r2.Code)
	}

	// Ten minutes after the first result the ID is forgotten: it acts again.
	now = now.Add(IdempotencyWindow)
	late, _ := a.post(t, "/control/v1/echo", body)
	if late.Code != 200 || late.Header().Get("Idempotent-Replay") != "" || a.echo.count() != 2 {
		t.Fatalf("after the window = %d, runs %d", late.Code, a.echo.count())
	}
}

// A request that is still running is not started twice; the number of
// remembered request IDs is bounded, and a full table refuses new actions
// instead of forgetting results early.
// Verifies: DCM-SRS-086, DCM-SRS-096
func TestActionInProgressAndBoundedMemory(t *testing.T) {
	a := newActEnv(t)
	a.echo.block = make(chan struct{})
	id := rid()
	body := act(id, "remote_ae", "VIEWER1")
	done := make(chan *httptest.ResponseRecorder)
	go func() { rec, _ := a.post(t, "/control/v1/echo", body); done <- rec }()
	waitFor(t, func() bool { return a.echo.count() == 1 })
	rec, b := a.post(t, "/control/v1/echo", body)
	if rec.Code != http.StatusConflict || b["code"] != "request_in_progress" || a.echo.count() != 1 {
		t.Fatalf("second send while running = %d %v (runs %d)", rec.Code, b, a.echo.count())
	}
	close(a.echo.block)
	if first := <-done; first.Code != 200 {
		t.Fatalf("first = %d", first.Code)
	}
	if rec, _ := a.post(t, "/control/v1/echo", body); rec.Header().Get("Idempotent-Replay") != "true" || a.echo.count() != 1 {
		t.Fatal("after it finished, the repeat was not a replay")
	}

	// Fill the table.
	a.echo.mu.Lock()
	a.echo.block = nil
	a.echo.mu.Unlock()
	now := time.Now()
	a.api.requests.now = func() time.Time { return now }
	for len(a.api.requests.m) < maxRemembered {
		if rec, _ := a.post(t, "/control/v1/echo", act(rid(), "remote_ae", "VIEWER1")); rec.Code != 200 {
			t.Fatalf("fill = %d", rec.Code)
		}
	}
	runs := a.echo.count()
	rec, b = a.post(t, "/control/v1/echo", act(rid(), "remote_ae", "VIEWER1"))
	if rec.Code != http.StatusTooManyRequests || b["code"] != "busy" || a.echo.count() != runs {
		t.Fatalf("full table = %d %v", rec.Code, b)
	}
	if len(a.api.requests.m) > maxRemembered {
		t.Fatalf("table grew to %d", len(a.api.requests.m))
	}
	// An early result is still remembered.
	if rec, _ := a.post(t, "/control/v1/echo", body); rec.Header().Get("Idempotent-Replay") != "true" {
		t.Fatal("a full table forgot an earlier result")
	}
	// When the window has passed, the table empties and actions run again.
	now = now.Add(IdempotencyWindow + time.Second)
	if rec, _ := a.post(t, "/control/v1/echo", act(rid(), "remote_ae", "VIEWER1")); rec.Code != 200 || len(a.api.requests.m) != 1 {
		t.Fatalf("after the window = %d, table %d", rec.Code, len(a.api.requests.m))
	}
}

// An action that panics is reported as failed, gets its outcome record, and
// its request ID is not left "in progress": the repeat returns the stored
// failure and does not run the action again. The node's secrets stay out of
// the answer and the audit record.
// Verifies: DCM-SRS-086, DCM-SRS-096
func TestActionPanicIsRecordedAndNotRepeated(t *testing.T) {
	a := newActEnv(t)
	runs := 0
	a.api.Echo = func(context.Context, EchoTarget) (string, error) {
		runs++
		panic("boom near " + testToken)
	}
	id := rid()
	body := act(id, "remote_ae", "VIEWER1")
	rec, b := a.post(t, "/control/v1/echo", body)
	if rec.Code != http.StatusInternalServerError || b["code"] != "internal" || runs != 1 {
		t.Fatalf("panicking action = %d %v (runs %d)", rec.Code, b, runs)
	}
	if strings.Contains(rec.Body.String(), testToken) || strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("response carries the panic text: %s", rec.Body)
	}
	recs := findAudit(auditRecs(t, a.rt.Audit), map[string]any{"request_id": id, "phase": "result"})
	if len(recs) != 1 || recs[0]["outcome"] != outcomeSerious || recs[0]["panic"] != true {
		t.Fatalf("outcome record of a panicking action: %v", recs)
	}
	if raw, _ := json.Marshal(recs[0]); strings.Contains(string(raw), testToken) {
		t.Fatalf("audit record carries a secret: %s", raw)
	}
	// The repeat is a replay of the failure, not "in progress" and not a second run.
	again, b2 := a.post(t, "/control/v1/echo", body)
	if again.Code != http.StatusInternalServerError || again.Header().Get("Idempotent-Replay") != "true" || b2["code"] != "internal" || runs != 1 {
		t.Fatalf("repeat after a panic = %d %v replay=%q (runs %d)", again.Code, b2, again.Header().Get("Idempotent-Replay"), runs)
	}
	// The concurrency slot was given back: other tests still run.
	a.api.Echo = a.echo.run
	for i := 0; i < echoSlots+1; i++ {
		if rec, _ := a.post(t, "/control/v1/echo", act(rid(), "remote_ae", "VIEWER1")); rec.Code != 200 {
			t.Fatalf("echo %d after a panic = %d", i, rec.Code)
		}
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached in 5 s")
}

// ---- DCM-SRS-097: identity ----

// A state-changing request that names another node ID or AE title is
// refused and changes nothing; with the right identity it runs. Every
// action response names the node.
// Verifies: DCM-SRS-097
func TestActionsRefuseWrongInstance(t *testing.T) {
	a := newActEnv(t)
	plan, _ := a.post(t, "/control/v1/reload/plan", map[string]any{})
	if plan.Code != 200 {
		t.Fatalf("plan = %d", plan.Code)
	}
	paths := actionEndpoints()
	paths["/control/v1/reload/plan"] = map[string]any{}
	for path, fields := range paths {
		for _, wrong := range []map[string]any{
			{"expect_node_id": "dcm-2"},
			{"expect_ae_title": "OTHERDCM"},
			{"expect_node_id": "dcm-1", "expect_ae_title": "OTHERDCM"},
			{"expect_node_id": "DCM-1"}, // exact match only
		} {
			m := act(rid())
			for k, v := range fields {
				m[k] = v
			}
			for k, v := range wrong {
				m[k] = v
			}
			rec, body := a.post(t, path, m)
			if rec.Code != http.StatusConflict || body["code"] != "instance_mismatch" {
				t.Errorf("POST %s with %v = %d %v, want 409 instance_mismatch", path, wrong, rec.Code, body)
			}
			inst, _ := body["instance"].(map[string]any)
			if inst["node_id"] != "dcm-1" || inst["ae_title"] != "LIBREDCM" {
				t.Errorf("POST %s: the refusal does not say which node answered: %v", path, body)
			}
		}
	}
	a.nothingDone(t, "wrong instance")
	if n := len(findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditRefused, "code": "instance_mismatch"})); n != 4*len(actionEndpoints()) {
		t.Errorf("instance_mismatch audit records = %d, want %d", n, 4*len(actionEndpoints()))
	}

	rec, body := a.post(t, "/control/v1/echo", act(rid(), "remote_ae", "VIEWER1", "expect_node_id", "dcm-1", "expect_ae_title", "LIBREDCM"))
	inst, _ := body["instance"].(map[string]any)
	if rec.Code != 200 || a.echo.count() != 1 || inst["node_id"] != "dcm-1" || inst["version"] != "0.3.0-test" ||
		!strings.HasPrefix(fmt.Sprint(inst["config_digest"]), "sha256:") {
		t.Fatalf("right instance = %d %v", rec.Code, body)
	}
}

// ---- DCM-SRS-087: C-ECHO ----

// The C-ECHO test goes to a configured destination by name or to a given
// host, port and AE title, as this node's AE title, under a 15 second
// timeout. It reports success with the duration, or the stage that failed
// with a fixed text: nothing of the peer's or the system's own error text.
// Verifies: DCM-SRS-087
func TestEchoAction(t *testing.T) {
	a := newActEnv(t)
	rec, body := a.post(t, "/control/v1/echo", act(rid(), "remote_ae", "ENVDEST"))
	if rec.Code != 200 || body["ok"] != true || body["target"] != "10.0.0.30:11112" || body["called_ae"] != "PACSB" ||
		body["calling_ae"] != "LIBREDCM" || body["source"] != "configured" || body["tls"] != false {
		t.Fatalf("echo by name = %d %v", rec.Code, body)
	}
	if _, ok := body["duration_ms"].(float64); !ok {
		t.Fatalf("no duration: %v", body)
	}
	got := a.echo.calls[0]
	if got != (EchoTarget{Addr: "10.0.0.30:11112", CalledAE: "PACSB", CallingAE: "LIBREDCM"}) {
		t.Fatalf("echo target = %+v", got)
	}
	if a.echo.left <= 0 || a.echo.left > EchoTimeout {
		t.Fatalf("echo ran with %v left, want a timeout of at most %v", a.echo.left, EchoTimeout)
	}

	rec, body = a.post(t, "/control/v1/echo", act(rid(), "host", "fd00::20", "port", 2762, "ae_title", "NEWPACS", "tls", true))
	if rec.Code != 200 || body["target"] != "[fd00::20]:2762" || body["source"] != "ad-hoc" || body["tls"] != true {
		t.Fatalf("ad hoc echo = %d %v", rec.Code, body)
	}
	if got := a.echo.calls[1]; got != (EchoTarget{Addr: "[fd00::20]:2762", CalledAE: "NEWPACS", CallingAE: "LIBREDCM", TLS: true}) {
		t.Fatalf("ad hoc target = %+v", got)
	}

	// Failures: a result with the stage and a fixed text.
	const leak = "peer said: PATIENT^SECRET 0xDEADBEEF /etc/shadow"
	for _, c := range []struct {
		stage string
		err   error
		want  string
	}{
		{StageConnect, fmt.Errorf("ae: dial 10.0.0.30:11112: dial tcp: connect: connection refused %s", leak), "connection refused"},
		{StageConnect, fmt.Errorf("ae: dial x: %w %s", context.DeadlineExceeded, leak), "no answer (timed out)"},
		{StageConnect, fmt.Errorf("unusual %s", leak), "connection failed"},
		{StageAssociation, fmt.Errorf("A-ASSOCIATE-RJ %s", leak), "association rejected or aborted by the peer"},
		{StageAssociation, fmt.Errorf("%w %s", context.DeadlineExceeded, leak), "no answer to the association request (timed out)"},
		{StageEcho, fmt.Errorf("status 0x0110 %s", leak), "C-ECHO failed"},
	} {
		a.echo.stage, a.echo.err = c.stage, c.err
		rec, body := a.post(t, "/control/v1/echo", act(rid(), "remote_ae", "ENVDEST"))
		if rec.Code != 200 || body["ok"] != false || body["stage"] != c.stage || body["error"] != c.want {
			t.Errorf("failure %s/%v = %d %v, want error %q", c.stage, c.err, rec.Code, body, c.want)
		}
		if strings.Contains(rec.Body.String(), "SECRET") || strings.Contains(rec.Body.String(), "DEADBEEF") {
			t.Errorf("the response carries the peer's own text: %s", rec.Body)
		}
	}
	for _, r := range auditRecs(t, a.rt.Audit) {
		if raw, _ := json.Marshal(r); strings.Contains(string(raw), "DEADBEEF") {
			t.Errorf("an audit record carries the peer's own text: %s", raw)
		}
	}
}

// An unknown remote AE name is refused (404) and no connection is made; a
// malformed target is refused (422). Nothing is sent anywhere.
// Verifies: DCM-SRS-087
func TestEchoRefusesUnknownAndMalformedTargets(t *testing.T) {
	a := newActEnv(t)
	for _, name := range []string{"NOPE", "viewer1", "VIEWER1 ", "10.0.0.20:104", "../VIEWER1", strings.Repeat("A", 4000)} {
		rec, body := a.post(t, "/control/v1/echo", act(rid(), "remote_ae", name))
		if rec.Code != http.StatusNotFound || body["code"] != "unknown_remote_ae" {
			t.Errorf("remote_ae %.20q = %d %v, want 404", name, rec.Code, body)
		}
	}
	for name, fields := range map[string][]any{
		"no target":           {},
		"name and host":       {"remote_ae", "VIEWER1", "host", "10.0.0.20", "port", 104, "ae_title", "VIEWER1"},
		"name and tls":        {"remote_ae", "VIEWER1", "tls", true},
		"no host":             {"port", 104, "ae_title", "X"},
		"no port":             {"host", "10.0.0.20", "ae_title", "X"},
		"port 0":              {"host", "10.0.0.20", "port", 0, "ae_title", "X"},
		"port too high":       {"host", "10.0.0.20", "port", 65536, "ae_title", "X"},
		"negative port":       {"host", "10.0.0.20", "port", -1, "ae_title", "X"},
		"no ae title":         {"host", "10.0.0.20", "port", 104},
		"ae title 17 chars":   {"host", "10.0.0.20", "port", 104, "ae_title", "ABCDEFGHIJKLMNOPQ"},
		"ae title backslash":  {"host", "10.0.0.20", "port", 104, "ae_title", `A\B`},
		"ae title control":    {"host", "10.0.0.20", "port", 104, "ae_title", "A\nB"},
		"host with port":      {"host", "10.0.0.20:104", "port", 104, "ae_title", "X"},
		"host is a url":       {"host", "http://10.0.0.20/", "port", 104, "ae_title", "X"},
		"host with user":      {"host", "user@10.0.0.20", "port", 104, "ae_title", "X"},
		"host with space":     {"host", "10.0.0.20 10.0.0.21", "port", 104, "ae_title", "X"},
		"host with path":      {"host", "../../var/run/docker.sock", "port", 104, "ae_title", "X"},
		"host too long":       {"host", strings.Repeat("a", 254), "port", 104, "ae_title", "X"},
		"host with a newline": {"host", "pacs\nHost: evil", "port", 104, "ae_title", "X"},
	} {
		rec, body := a.post(t, "/control/v1/echo", act(rid(), fields...))
		if rec.Code != http.StatusUnprocessableEntity || body["code"] != "invalid_target" {
			t.Errorf("%s = %d %v, want 422 invalid_target", name, rec.Code, body)
		}
	}
	if a.echo.count() != 0 {
		t.Fatalf("a refused target was contacted %d times", a.echo.count())
	}
	if n := len(findAudit(auditRecs(t, a.rt.Audit), map[string]any{"event": auditRefused, "code": "unknown_remote_ae", "phase": "result"})); n != 6 {
		t.Errorf("unknown_remote_ae audit records = %d, want 6", n)
	}
}

// At most two C-ECHO tests run at once on a node; a third is refused as
// busy, and may be sent again with the same request ID afterwards.
// Verifies: DCM-SRS-096, DCM-SRS-087
func TestEchoConcurrencyLimit(t *testing.T) {
	a := newActEnv(t)
	a.echo.block = make(chan struct{})
	done := make(chan int, echoSlots)
	for i := 0; i < echoSlots; i++ {
		go func() { rec, _ := a.post(t, "/control/v1/echo", act(rid2(), "remote_ae", "VIEWER1")); done <- rec.Code }()
	}
	waitFor(t, func() bool { return a.echo.count() == echoSlots })
	third := act(rid2(), "remote_ae", "VIEWER1")
	rec, body := a.post(t, "/control/v1/echo", third)
	if rec.Code != http.StatusTooManyRequests || body["code"] != "busy" || a.echo.count() != echoSlots {
		t.Fatalf("third echo = %d %v", rec.Code, body)
	}
	close(a.echo.block)
	for i := 0; i < echoSlots; i++ {
		if code := <-done; code != 200 {
			t.Fatalf("echo = %d", code)
		}
	}
	if rec, _ := a.post(t, "/control/v1/echo", third); rec.Code != 200 || rec.Header().Get("Idempotent-Replay") != "" {
		t.Fatalf("retry of the refused request = %d", rec.Code)
	}
}

var (
	rid2Mu  sync.Mutex
	rid2Seq int
)

// rid2 is rid for use from several goroutines.
func rid2() string {
	rid2Mu.Lock()
	defer rid2Mu.Unlock()
	rid2Seq++
	return fmt.Sprintf("par-%04d-abcdefghij", rid2Seq)
}

// No action response and no audit record ever holds one of the node's
// secrets, also when an error text or a name carries it.
// Verifies: DCM-SRS-082, DCM-SRS-080
func TestActionResponsesNeverLeakSecrets(t *testing.T) {
	a := newActEnv(t)
	a.rl.err = fmt.Errorf("config: parse /etc/libredcm/config.yaml: line 3: token: %s and postgres://u:%s@db/x", testToken, dbPassword)
	a.echo.stage, a.echo.err = StageConnect, fmt.Errorf("dial %s", s3Secret)
	a.lic.err = fmt.Errorf("server said %s", busPassword)
	var all strings.Builder
	paths := actionEndpoints()
	paths["/control/v1/reload/plan"] = map[string]any{}
	for path, fields := range paths {
		m := act(rid())
		for k, v := range fields {
			m[k] = v
		}
		rec, _ := a.post(t, path, m)
		all.WriteString(rec.Body.String())
	}
	for _, p := range []string{"/control/v1/license", "/control/v1/backups"} {
		rec, _ := a.do(t, "GET", p, testToken)
		all.WriteString(rec.Body.String())
	}
	// The audit records of these actions go to the log: no secret there either.
	var audit strings.Builder
	entries, _ := a.rt.Audit.Select(10000, nil)
	for _, e := range entries {
		audit.Write(e.Line)
	}
	if !strings.Contains(audit.String(), "invalid_config") || !strings.Contains(audit.String(), "activation_failed") {
		t.Fatalf("precondition: the failures were not audited: %s", audit.String())
	}
	for _, s := range allSecrets {
		if strings.Contains(all.String(), s) {
			t.Errorf("an action response holds the secret %q", s)
		}
		if strings.Contains(audit.String(), s) {
			t.Errorf("an audit record holds the secret %q", s)
		}
	}
}
