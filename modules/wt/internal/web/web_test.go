package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

func init() { accounts.ArgonMemory, accounts.ArgonTime, accounts.ArgonThreads = 1024, 1, 1 }

// fakeDCM stands in for LibreDCM's control API.
type fakeDCM struct {
	mu       sync.Mutex
	conflict dcmclient.Conflict
	plan     dcmclient.Plan
	resolves []string // "id action operator reason"
	down     bool
}

func newFake() *fakeDCM {
	c := dcmclient.Conflict{ID: "c1", Kind: "patient_id", State: "open", StudyUID: "1.2.3", ExistingPatientID: "P1",
		ExistingStudyUID: "1.2.3", IncomingPatientID: "P2", DisplayPatientID: "P2", PatientName: "ROE^RICHARD",
		SourceAE: "CT1", Instances: 2, FirstSeen: time.Now(), LastSeen: time.Now()}
	return &fakeDCM{conflict: c, plan: dcmclient.Plan{ConflictID: "c1", Action: "correct_incoming", Summary: "Change the patient of 2 incoming instance(s)",
		IncomingInstances: 2, IncomingChanges: []dcmclient.Change{{Attribute: "PatientID", From: "P2", To: "P1"}}}}
}

func (f *fakeDCM) URL() string { return "http://dcm.test:8081" }
func (f *fakeDCM) Conflicts(context.Context, string) ([]dcmclient.Conflict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, errors.New("LibreDCM unreachable: connection refused")
	}
	return []dcmclient.Conflict{f.conflict}, nil
}
func (f *fakeDCM) Conflict(_ context.Context, id string) (dcmclient.Detail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.conflict.ID {
		return dcmclient.Detail{}, &dcmclient.APIError{Status: 404, Message: "conflict not found"}
	}
	return dcmclient.Detail{Conflict: f.conflict, IndexedPatient: &dcmclient.Demographics{PatientID: "P1", PatientName: "DOE^JANE"},
		IndexedInstances: 3, IncomingInstances: []dcmclient.Instance{{SOPInstanceUID: "1.2.3.4", Modality: "CT"}}}, nil
}
func (f *fakeDCM) Plan(_ context.Context, id, action string) (dcmclient.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.plan
	p.Action = action
	return p, nil
}
func (f *fakeDCM) Resolve(_ context.Context, id, action, operator, reason string) (dcmclient.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves = append(f.resolves, strings.Join([]string{id, action, operator, reason}, " "))
	f.conflict.State, f.conflict.ResolvedBy = "corrected", operator
	return dcmclient.Result{Plan: f.plan, State: "corrected", Rewritten: 2}, nil
}
func (f *fakeDCM) Version(context.Context) (map[string]any, error) {
	return map[string]any{"version": "test"}, nil
}

type env struct {
	srv  *httptest.Server
	acct *accounts.Store
	dcm  *fakeDCM
	web  *Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	acct, err := accounts.Open(context.Background(), filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = acct.Close() })
	dcm := newFake()
	w, err := New(Config{Version: "test"}, acct, dcm, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(w.Handler())
	t.Cleanup(srv.Close)
	return &env{srv: srv, acct: acct, dcm: dcm, web: w}
}

// browser is a cookie-keeping client that doesn't follow redirects.
type browser struct {
	t *testing.T
	c *http.Client
	e *env
}

func (e *env) browser(t *testing.T) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, e: e, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *browser) get(path string) (int, string, http.Header) {
	b.t.Helper()
	resp, err := b.c.Get(b.e.srv.URL + path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

func (b *browser) post(path string, form url.Values) (int, string, http.Header) {
	b.t.Helper()
	resp, err := b.c.PostForm(b.e.srv.URL+path, form)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

var (
	csrfRE    = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)
	confirmRE = regexp.MustCompile(`name="confirm" value="([0-9a-f]+)"`)
)

func find(t *testing.T, re *regexp.Regexp, body string) string {
	t.Helper()
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("pattern %s not in page", re)
	}
	return m[1]
}

func (b *browser) login(user, pw string) {
	b.t.Helper()
	code, body, h := b.post("/login", url.Values{"username": {user}, "password": {pw}})
	if code != http.StatusSeeOther || h.Get("Location") != "/exceptions" {
		b.t.Fatalf("login %s = %d %s", user, code, body)
	}
}

func (b *browser) csrf() string {
	b.t.Helper()
	_, body, _ := b.get("/account")
	return find(b.t, csrfRE, body)
}

// A fresh install redirects everything to first-run setup; setup works once
// and logs the administrator in; afterwards /setup goes to the login page.
// Verifies: WT-SRS-001, WT-SRS-002
func TestSetupFlow(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	for _, p := range []string{"/", "/exceptions", "/login"} {
		if code, _, h := b.get(p); code != http.StatusSeeOther || h.Get("Location") != "/setup" {
			t.Fatalf("%s = %d %s", p, code, h.Get("Location"))
		}
	}
	if code, body, _ := b.post("/setup", url.Values{"username": {"admin1"}, "password": {"admin one password"}, "password2": {"different!!!!"}}); code != 422 || !strings.Contains(body, "don&#39;t match") {
		t.Fatalf("mismatch = %d", code)
	}
	code, _, h := b.post("/setup", url.Values{"username": {"admin1"}, "password": {"admin one password"}, "password2": {"admin one password"}})
	if code != http.StatusSeeOther || h.Get("Location") != "/exceptions" {
		t.Fatalf("setup = %d", code)
	}
	if code, body, _ := b.get("/exceptions"); code != 200 || !strings.Contains(body, "Study Exceptions") {
		t.Fatalf("after setup = %d", code)
	}
	other := e.browser(t)
	if code, _, h := other.post("/setup", url.Values{"username": {"eve"}, "password": {"eve eve eve eve"}, "password2": {"eve eve eve eve"}}); code != http.StatusSeeOther || h.Get("Location") != "/login" {
		t.Fatalf("second setup = %d %s", code, h.Get("Location"))
	}
	if users, _ := e.acct.List(context.Background()); len(users) != 1 {
		t.Fatalf("users = %v", users)
	}
}

// Cookies are HttpOnly + SameSite=Strict; every page carries the security
// headers; state-changing requests without the CSRF token are refused.
// Verifies: WT-SRS-001, WT-SRS-004
func TestSessionCookieHeadersAndCSRF(t *testing.T) {
	e := newEnv(t)
	_, _ = e.acct.CreateFirstAdmin(context.Background(), "admin1", "admin one password")
	b := e.browser(t)
	resp, err := b.c.PostForm(e.srv.URL+"/login", url.Values{"username": {"admin1"}, "password": {"admin one password"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	sc := resp.Header.Get("Set-Cookie")
	if !strings.Contains(sc, "HttpOnly") || !strings.Contains(sc, "SameSite=Strict") {
		t.Fatalf("cookie = %s", sc)
	}
	_, _, h := b.get("/exceptions")
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", csp)
	}
	if h.Get("X-Frame-Options") != "DENY" || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", h)
	}
	if code, _, _ := b.post("/users", url.Values{"username": {"x1"}, "password": {"x1 password long"}, "role": {"viewer"}}); code != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d", code)
	}
	if code, _, _ := b.post("/users", url.Values{"csrf": {"deadbeef"}, "username": {"x1"}, "password": {"x1 password long"}, "role": {"viewer"}}); code != http.StatusForbidden {
		t.Fatalf("POST with wrong CSRF = %d", code)
	}
	if code, _, _ := b.post("/users", url.Values{"csrf": {b.csrf()}, "username": {"x1"}, "password": {"x1 password long"}, "role": {"viewer"}}); code != 200 {
		t.Fatalf("POST with CSRF = %d", code)
	}
}

// Failed logins are limited per username (and per address) and audited.
// Verifies: WT-SRS-004, WT-SRS-011
func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	_, _ = e.acct.CreateFirstAdmin(context.Background(), "admin1", "admin one password")
	b := e.browser(t)
	for range loginMaxPerUser {
		if code, _, _ := b.post("/login", url.Values{"username": {"admin1"}, "password": {"wrong"}}); code != http.StatusUnauthorized {
			t.Fatalf("wrong password = %d", code)
		}
	}
	if code, _, _ := b.post("/login", url.Values{"username": {"admin1"}, "password": {"admin one password"}}); code != http.StatusTooManyRequests {
		t.Fatalf("after limit = %d (the right password must wait too)", code)
	}
	log, _ := e.acct.AuditLog(context.Background(), 0)
	if len(log) < loginMaxPerUser+1 || log[0].Detail != "rate limited" || log[1].Outcome != "failure" {
		t.Fatalf("audit = %+v", log[:2])
	}
}

// Viewers see the queue and details but no resolve buttons and can't open
// the review page, the users page or the audit log.
// Verifies: WT-SRS-004, WT-SRS-007, WT-SRS-008
func TestViewerRole(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, _ = e.acct.CreateFirstAdmin(ctx, "admin1", "admin one password")
	_, _ = e.acct.CreateUser(ctx, "viewer1", "viewer one password", accounts.Viewer)
	b := e.browser(t)
	b.login("viewer1", "viewer one password")
	code, body, _ := b.get("/exceptions")
	if code != 200 || !strings.Contains(body, "Patient ID differs") || !strings.Contains(body, "ROE^RICHARD") {
		t.Fatalf("list = %d", code)
	}
	code, body, _ = b.get("/exceptions/c1")
	if code != 200 || !strings.Contains(body, "In the archive") || !strings.Contains(body, "DOE^JANE") || strings.Contains(body, "/review?action=") {
		t.Fatalf("detail = %d", code)
	}
	for _, p := range []string{"/exceptions/c1/review?action=reject", "/users", "/audit"} {
		if code, _, _ := b.get(p); code != http.StatusForbidden {
			t.Fatalf("viewer %s = %d", p, code)
		}
	}
	if code, _, _ := b.post("/exceptions/c1/resolve", url.Values{"csrf": {b.csrf()}, "action": {"reject"}}); code != http.StatusForbidden {
		t.Fatalf("viewer resolve = %d", code)
	}
	if len(e.dcm.resolves) != 0 {
		t.Fatal("viewer resolved")
	}
	if code, _, _ := b.get("/exceptions/nope"); code != http.StatusNotFound {
		t.Fatalf("unknown = %d", code)
	}
}

// An edit happens only after the warning page: the resolve request needs the
// single-use confirmation issued with it (same session, conflict, action and
// plan), the approve box and a reason. The operator name sent to LibreDCM is
// the logged-in user, and the edit is audited.
// Verifies: WT-SRS-009, WT-SRS-010, WT-SRS-011
func TestReviewAndApproveEdit(t *testing.T) {
	e := newEnv(t)
	_, _ = e.acct.CreateFirstAdmin(context.Background(), "admin1", "admin one password")
	b := e.browser(t)
	b.login("admin1", "admin one password")

	code, body, _ := b.get("/exceptions/c1/review?action=correct_incoming")
	if code != 200 || !strings.Contains(body, "Warning: this changes patient data") || !strings.Contains(body, "Change the patient of 2 incoming") ||
		!strings.Contains(body, "I have checked the patient details and approve this edit") {
		t.Fatalf("review = %d", code)
	}
	csrf, confirm := find(t, csrfRE, body), find(t, confirmRE, body)
	form := func(c, action, approve, reason string) url.Values {
		return url.Values{"csrf": {csrf}, "confirm": {c}, "action": {action}, "approve": {approve}, "reason": {reason}}
	}
	// No confirmation token at all.
	if code, body, _ := b.post("/exceptions/c1/resolve", form("", "correct_incoming", "yes", "typo")); code != http.StatusConflict || !strings.Contains(body, "approval expired") {
		t.Fatalf("without confirm = %d", code)
	}
	// Token for another action is refused and consumed.
	if code, _, _ := b.post("/exceptions/c1/resolve", form(confirm, "correct_existing", "yes", "typo")); code != http.StatusConflict {
		t.Fatalf("other action = %d", code)
	}
	if code, body, _ := b.post("/exceptions/c1/resolve", form(confirm, "correct_incoming", "yes", "typo")); code != http.StatusConflict || !strings.Contains(body, "approval expired") {
		t.Fatalf("reused token = %d", code)
	}
	// Approve box missing, then reason missing.
	_, body, _ = b.get("/exceptions/c1/review?action=correct_incoming")
	if code, _, _ := b.post("/exceptions/c1/resolve", form(find(t, confirmRE, body), "correct_incoming", "", "typo")); code != 422 {
		t.Fatalf("no approve = %d", code)
	}
	_, body, _ = b.get("/exceptions/c1/review?action=correct_incoming")
	if code, _, _ := b.post("/exceptions/c1/resolve", form(find(t, confirmRE, body), "correct_incoming", "yes", "  ")); code != 422 {
		t.Fatalf("no reason = %d", code)
	}
	// The plan changed after review (new images arrived): refused.
	_, body, _ = b.get("/exceptions/c1/review?action=correct_incoming")
	tok := find(t, confirmRE, body)
	e.dcm.mu.Lock()
	e.dcm.plan.IncomingInstances = 3
	e.dcm.mu.Unlock()
	if code, body, _ := b.post("/exceptions/c1/resolve", form(tok, "correct_incoming", "yes", "typo")); code != http.StatusConflict || !strings.Contains(body, "no longer the one you approved") {
		t.Fatalf("changed plan = %d", code)
	}
	if len(e.dcm.resolves) != 0 {
		t.Fatalf("resolved without valid approval: %v", e.dcm.resolves)
	}
	// Proper approval.
	_, body, _ = b.get("/exceptions/c1/review?action=correct_incoming")
	code, body, _ = b.post("/exceptions/c1/resolve", form(find(t, confirmRE, body), "correct_incoming", "yes", "wrong worklist entry"))
	if code != 200 || !strings.Contains(body, "2 file(s) rewritten") {
		t.Fatalf("resolve = %d %s", code, body)
	}
	if len(e.dcm.resolves) != 1 || e.dcm.resolves[0] != "c1 correct_incoming admin1 wrong worklist entry" {
		t.Fatalf("resolves = %v", e.dcm.resolves)
	}
	// A confirmation from another session is refused.
	other := e.browser(t)
	other.login("admin1", "admin one password")
	_, body, _ = b.get("/exceptions/c1/review?action=reject")
	stolen := find(t, confirmRE, body)
	if code, _, _ := other.post("/exceptions/c1/resolve", url.Values{"csrf": {other.csrf()}, "confirm": {stolen}, "action": {"reject"}, "approve": {"yes"}, "reason": {"x"}}); code != http.StatusConflict {
		t.Fatalf("cross-session confirm = %d", code)
	}
	log, _ := e.acct.AuditLog(context.Background(), 0)
	actions := map[string]int{}
	for _, l := range log {
		actions[l.Action+"/"+l.Outcome]++
	}
	if actions["exception.resolve/success"] != 1 || actions["exception.resolve.approved/success"] != 1 || actions["exception.resolve/failure"] < 5 || actions["exception.review/success"] < 1 {
		t.Fatalf("audit = %v", actions)
	}
}

// Disabling a user or changing their role ends their session at the next
// request; idle sessions expire.
// Verifies: WT-SRS-005
func TestSessionEndsOnAccountChangeAndIdle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, _ = e.acct.CreateFirstAdmin(ctx, "admin1", "admin one password")
	v, _ := e.acct.CreateUser(ctx, "viewer1", "viewer one password", accounts.Viewer)
	b := e.browser(t)
	b.login("viewer1", "viewer one password")
	if code, _, _ := b.get("/exceptions"); code != 200 {
		t.Fatal("not logged in")
	}
	if err := e.acct.SetRole(ctx, v.ID, accounts.Operator); err != nil {
		t.Fatal(err)
	}
	if code, _, h := b.get("/exceptions"); code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/login") {
		t.Fatalf("after role change = %d", code)
	}
	b.login("viewer1", "viewer one password")
	_ = e.acct.SetDisabled(ctx, v.ID, true)
	if code, _, _ := b.get("/exceptions"); code != http.StatusSeeOther {
		t.Fatalf("after disable = %d", code)
	}
	_ = e.acct.SetDisabled(ctx, v.ID, false)
	b.login("viewer1", "viewer one password")
	now := time.Now()
	e.web.ss.mu.Lock()
	e.web.ss.now = func() time.Time { return now.Add(31 * time.Minute) }
	e.web.ss.mu.Unlock()
	if code, _, _ := b.get("/exceptions"); code != http.StatusSeeOther {
		t.Fatalf("after idle = %d", code)
	}
}

// Administrators manage users; every change is audited; the audit page
// shows it. An unreachable LibreDCM is reported, not hidden.
// Verifies: WT-SRS-006, WT-SRS-011, WT-SRS-012
func TestUsersAuditAndDCMDown(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, _ = e.acct.CreateFirstAdmin(ctx, "admin1", "admin one password")
	b := e.browser(t)
	b.login("admin1", "admin one password")
	if code, body, _ := b.post("/users", url.Values{"csrf": {b.csrf()}, "username": {"op1"}, "password": {"operator one password"}, "role": {"operator"}}); code != 200 || !strings.Contains(body, "User op1 created") {
		t.Fatalf("create = %d", code)
	}
	if code, body, _ := b.post("/users", url.Values{"csrf": {b.csrf()}, "username": {"op2"}, "password": {"short"}, "role": {"viewer"}}); code != 422 || !strings.Contains(body, "at least 12") {
		t.Fatalf("weak = %d", code)
	}
	users, _ := e.acct.List(ctx)
	var op1 int64
	for _, u := range users {
		if u.Username == "op1" {
			op1 = u.ID
		}
	}
	if code, _, _ := b.post("/users/"+itoa(op1), url.Values{"csrf": {b.csrf()}, "op": {"disable"}}); code != 200 {
		t.Fatalf("disable = %d", code)
	}
	code, body, _ := b.get("/audit")
	if code != 200 || !strings.Contains(body, "user.create") || !strings.Contains(body, "user.disable") || !strings.Contains(body, "login") {
		t.Fatalf("audit page = %d", code)
	}
	if !strings.Contains(body, "http://dcm.test:8081") {
		t.Fatal("LibreDCM URL not shown")
	}
	if strings.Contains(body, "Bearer") {
		t.Fatal("token leaked")
	}
	e.dcm.mu.Lock()
	e.dcm.down = true
	e.dcm.mu.Unlock()
	if code, body, _ := b.get("/exceptions"); code != 200 || !strings.Contains(body, "reach LibreDCM: LibreDCM unreachable") {
		t.Fatalf("dcm down page = %d", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
