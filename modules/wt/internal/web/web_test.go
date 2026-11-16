package web

import (
	"bytes"
	"context"
	"database/sql"
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
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fakedcm"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fleet"
)

func init() { accounts.ArgonMemory, accounts.ArgonTime, accounts.ArgonThreads = 1024, 1, 1 }

// syncBuf collects Libre WT's log, so tests can check what was logged.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type env struct {
	srv   *httptest.Server
	acct  *accounts.Store
	nodes map[string]*fakedcm.Server
	dcm   *fakedcm.Server // the first (or only) node
	web   *Server
	log   *syncBuf
	db    string // path of librewt.db
}

// breakAudit makes every write to the audit log fail (and nothing else),
// through a trigger set from a second connection to the database.
func (e *env) breakAudit(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+e.db+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER audit_broken BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT, 'audit log is broken (test)'); END`); err != nil {
		t.Fatal(err)
	}
}

// plainHTTP wraps a client so it reports a plain-HTTP network connection.
type plainHTTP struct{ fleet.Client }

func (plainHTTP) PlainHTTPRemote() bool { return true }

// newEnv starts Libre WT with one instance named "default", as an upgraded
// 0.1 installation has.
func newEnv(t *testing.T) *env { return newFleetEnv(t, "default") }

// newFleetEnv starts Libre WT with the given instances ("name" or
// "name@cluster"), each backed by its own fake LibreDCM with its own token,
// reached through the real client. Status reads are not cached, so a test
// sees every change of a node at once (the cache is tested in package fleet).
func newFleetEnv(t *testing.T, specs ...string) *env {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wt.db")
	acct, err := accounts.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = acct.Close() })
	e := &env{acct: acct, nodes: map[string]*fakedcm.Server{}, log: &syncBuf{}, db: path}
	var cfgs []fleet.InstanceConfig
	for i, spec := range specs {
		name, cluster, _ := strings.Cut(spec, "@")
		n := fakedcm.New()
		t.Cleanup(n.Close)
		n.Set(func(f *fakedcm.Server) {
			f.Token = "tok-SECRET-" + name // gitleaks:allow (synthetic test value)
			f.NodeID, f.AETitle = "node-"+name, "AE_"+strings.ToUpper(name)
		})
		e.nodes[name] = n
		if i == 0 {
			e.dcm = n
		}
		cfgs = append(cfgs, fleet.InstanceConfig{Name: name, Cluster: cluster, URL: n.URL(), Token: "tok-SECRET-" + name}) // gitleaks:allow (synthetic test value)
	}
	members, err := fleet.Connect(cfgs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range members {
		if strings.HasPrefix(members[i].Name, "plain") {
			members[i].Client = plainHTTP{members[i].Client}
		}
	}
	logger := slog.New(slog.NewTextHandler(e.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reg, err := fleet.New(members, acct, time.Nanosecond, logger)
	if err != nil {
		t.Fatal(err)
	}
	w, err := New(Config{Version: "wt-test-1.2.3"}, acct, reg, logger)
	if err != nil {
		t.Fatal(err)
	}
	e.web = w
	e.srv = httptest.NewServer(w.Handler())
	t.Cleanup(e.srv.Close)
	return e
}

// users creates admin1, op1 and viewer1.
func (e *env) users(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.acct.CreateFirstAdmin(ctx, "admin1", "admin one password"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.acct.CreateUser(ctx, "op1", "operator one password", accounts.Operator); err != nil {
		t.Fatal(err)
	}
	if _, err := e.acct.CreateUser(ctx, "viewer1", "viewer one password", accounts.Viewer); err != nil {
		t.Fatal(err)
	}
}

var passwords = map[string]string{"admin1": "admin one password", "op1": "operator one password", "viewer1": "viewer one password"}

func (e *env) as(t *testing.T, user string) *browser {
	t.Helper()
	b := e.browser(t)
	b.login(user, passwords[user])
	return b
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
	if code != http.StatusSeeOther || h.Get("Location") != "/" {
		b.t.Fatalf("login %s = %d %s", user, code, body)
	}
}

func (b *browser) csrf() string {
	b.t.Helper()
	_, body, _ := b.get("/account")
	return find(b.t, csrfRE, body)
}

// has fails the test when the page lacks one of the texts.
func has(t *testing.T, what, body string, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if !strings.Contains(body, s) {
			t.Fatalf("%s: %q not in page:\n%s", what, s, body)
		}
	}
}

// hasNot fails the test when the page holds one of the texts.
func hasNot(t *testing.T, what, body string, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if strings.Contains(body, s) {
			t.Fatalf("%s: %q is in the page", what, s)
		}
	}
}

// A fresh install redirects everything to first-run setup; setup works once
// and logs the administrator in; afterwards /setup goes to the login page.
// Verifies: WT-SRS-001, WT-SRS-002
func TestSetupFlow(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	for _, p := range []string{"/", "/exceptions", "/i/default/", "/i/default/exceptions", "/about", "/login"} {
		if code, _, h := b.get(p); code != http.StatusSeeOther || h.Get("Location") != "/setup" {
			t.Fatalf("%s = %d %s", p, code, h.Get("Location"))
		}
	}
	if code, body, _ := b.post("/setup", url.Values{"username": {"admin1"}, "password": {"admin one password"}, "password2": {"different!!!!"}}); code != 422 || !strings.Contains(body, "don&#39;t match") {
		t.Fatalf("mismatch = %d", code)
	}
	code, _, h := b.post("/setup", url.Values{"username": {"admin1"}, "password": {"admin one password"}, "password2": {"admin one password"}})
	if code != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("setup = %d", code)
	}
	if code, body, _ := b.get("/i/default/exceptions"); code != 200 || !strings.Contains(body, "Study Exceptions") {
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
// headers and no script; state-changing requests without the CSRF token are
// refused.
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
	for _, p := range []string{"/", "/about", "/i/default/", "/i/default/status", "/i/default/exceptions", "/i/default/exceptions/c1", "/i/nope/", "/audit"} {
		_, body, h := b.get(p)
		if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") ||
			!strings.Contains(csp, "form-action 'self'") || strings.Contains(csp, "script-src") || strings.Contains(csp, "unsafe") {
			t.Fatalf("%s: CSP = %q", p, csp)
		}
		if h.Get("X-Frame-Options") != "DENY" || h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s: headers = %v", p, h)
		}
		lower := strings.ToLower(body)
		for _, bad := range []string{"<script", "javascript:", " onclick=", " onload=", "http-equiv"} {
			if strings.Contains(lower, bad) {
				t.Fatalf("%s: page holds %q", p, bad)
			}
		}
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
	e.users(t)
	b := e.as(t, "viewer1")
	code, body, _ := b.get("/i/default/exceptions")
	if code != 200 || !strings.Contains(body, "Patient ID differs") || !strings.Contains(body, "ROE^RICHARD") {
		t.Fatalf("list = %d", code)
	}
	code, body, _ = b.get("/i/default/exceptions/c1")
	if code != 200 || !strings.Contains(body, "In the archive") || !strings.Contains(body, "DOE^JANE") || strings.Contains(body, "/review?action=") {
		t.Fatalf("detail = %d", code)
	}
	for _, p := range []string{"/i/default/exceptions/c1/review?action=reject", "/users", "/audit"} {
		if code, _, _ := b.get(p); code != http.StatusForbidden {
			t.Fatalf("viewer %s = %d", p, code)
		}
	}
	if code, _, _ := b.post("/i/default/exceptions/c1/resolve", url.Values{"csrf": {b.csrf()}, "action": {"reject"}}); code != http.StatusForbidden {
		t.Fatalf("viewer resolve = %d", code)
	}
	if len(e.dcm.Resolves()) != 0 {
		t.Fatal("viewer resolved")
	}
	if code, _, _ := b.get("/i/default/exceptions/nope"); code != http.StatusNotFound {
		t.Fatalf("unknown = %d", code)
	}
}

// An edit happens only after the warning page: the resolve request needs the
// single-use confirmation issued with it (same session, conflict, action and
// plan), the approve box and a reason. The operator name sent to LibreDCM is
// the logged-in user, and the edit is audited with the instance.
// Verifies: WT-SRS-009, WT-SRS-010, WT-SRS-011
func TestReviewAndApproveEdit(t *testing.T) {
	e := newEnv(t)
	_, _ = e.acct.CreateFirstAdmin(context.Background(), "admin1", "admin one password")
	b := e.browser(t)
	b.login("admin1", "admin one password")
	const base = "/i/default/exceptions/c1"

	code, body, _ := b.get(base + "/review?action=correct_incoming")
	if code != 200 || !strings.Contains(body, "Warning: this changes patient data") || !strings.Contains(body, "Change the patient of 2 incoming") ||
		!strings.Contains(body, "I have checked the patient details and approve this edit") {
		t.Fatalf("review = %d", code)
	}
	has(t, "review names the instance", body, "The edit is made on instance <b>default</b>", "node-default", "AE_DEFAULT")
	csrf, confirm := find(t, csrfRE, body), find(t, confirmRE, body)
	form := func(c, action, approve, reason string) url.Values {
		return url.Values{"csrf": {csrf}, "confirm": {c}, "action": {action}, "approve": {approve}, "reason": {reason}}
	}
	// No confirmation token at all.
	if code, body, _ := b.post(base+"/resolve", form("", "correct_incoming", "yes", "typo")); code != http.StatusConflict || !strings.Contains(body, "approval expired") {
		t.Fatalf("without confirm = %d", code)
	}
	// Token for another action is refused and consumed.
	if code, _, _ := b.post(base+"/resolve", form(confirm, "correct_existing", "yes", "typo")); code != http.StatusConflict {
		t.Fatalf("other action = %d", code)
	}
	if code, body, _ := b.post(base+"/resolve", form(confirm, "correct_incoming", "yes", "typo")); code != http.StatusConflict || !strings.Contains(body, "approval expired") {
		t.Fatalf("reused token = %d", code)
	}
	// Approve box missing, then reason missing.
	_, body, _ = b.get(base + "/review?action=correct_incoming")
	if code, _, _ := b.post(base+"/resolve", form(find(t, confirmRE, body), "correct_incoming", "", "typo")); code != 422 {
		t.Fatalf("no approve = %d", code)
	}
	_, body, _ = b.get(base + "/review?action=correct_incoming")
	if code, _, _ := b.post(base+"/resolve", form(find(t, confirmRE, body), "correct_incoming", "yes", "  ")); code != 422 {
		t.Fatalf("no reason = %d", code)
	}
	// The plan changed after review (new images arrived): refused.
	_, body, _ = b.get(base + "/review?action=correct_incoming")
	tok := find(t, confirmRE, body)
	e.dcm.Set(func(f *fakedcm.Server) { f.Plan.IncomingInstances = 3 })
	if code, body, _ := b.post(base+"/resolve", form(tok, "correct_incoming", "yes", "typo")); code != http.StatusConflict || !strings.Contains(body, "no longer the one you approved") {
		t.Fatalf("changed plan = %d", code)
	}
	if n := len(e.dcm.Resolves()); n != 0 {
		t.Fatalf("resolved without valid approval: %d", n)
	}
	// Proper approval.
	_, body, _ = b.get(base + "/review?action=correct_incoming")
	code, body, _ = b.post(base+"/resolve", form(find(t, confirmRE, body), "correct_incoming", "yes", "wrong worklist entry"))
	if code != 200 || !strings.Contains(body, "2 file(s) rewritten") {
		t.Fatalf("resolve = %d %s", code, body)
	}
	got := e.dcm.Resolves()
	if len(got) != 1 || got[0]["action"] != "correct_incoming" || got[0]["operator"] != "admin1" || got[0]["reason"] != "wrong worklist entry" || got[0]["confirm"] != true {
		t.Fatalf("resolves = %v", got)
	}
	// A confirmation from another session is refused.
	other := e.browser(t)
	other.login("admin1", "admin one password")
	_, body, _ = b.get(base + "/review?action=reject")
	stolen := find(t, confirmRE, body)
	if code, _, _ := other.post(base+"/resolve", url.Values{"csrf": {other.csrf()}, "confirm": {stolen}, "action": {"reject"}, "approve": {"yes"}, "reason": {"x"}}); code != http.StatusConflict {
		t.Fatalf("cross-session confirm = %d", code)
	}
	log, _ := e.acct.AuditLog(context.Background(), 0)
	actions := map[string]int{}
	for _, l := range log {
		if strings.HasPrefix(l.Action, "exception.") && l.Instance != "default" {
			t.Fatalf("audit record without the instance: %+v", l)
		}
		actions[l.Action+"/"+l.Outcome]++
	}
	if actions["exception.resolve/success"] != 1 || actions["exception.resolve.approved/success"] != 1 || actions["exception.resolve/failure"] < 5 || actions["exception.review/success"] < 1 {
		t.Fatalf("audit = %v", actions)
	}
}

// When the audit log can't be written, the approved edit is not sent.
// Verifies: WT-SRS-011
func TestNoAuditNoEdit(t *testing.T) {
	e := newEnv(t)
	e.users(t)
	b := e.as(t, "admin1")
	const base = "/i/default/exceptions/c1"
	_, body, _ := b.get(base + "/review?action=reject")
	form := url.Values{"csrf": {find(t, csrfRE, body)}, "confirm": {find(t, confirmRE, body)}, "action": {"reject"}, "approve": {"yes"}, "reason": {"duplicate"}}
	e.breakAudit(t)
	code, page, _ := b.post(base+"/resolve", form)
	if code != http.StatusInternalServerError || !strings.Contains(page, "audit log can&#39;t be written") {
		t.Fatalf("resolve without audit = %d %s", code, page)
	}
	if n := len(e.dcm.Resolves()); n != 0 {
		t.Fatalf("edit sent without an audit record: %d", n)
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
	if code, _, _ := b.get("/"); code != 200 {
		t.Fatal("not logged in")
	}
	if err := e.acct.SetRole(ctx, v.ID, accounts.Operator); err != nil {
		t.Fatal(err)
	}
	if code, _, h := b.get("/"); code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/login") {
		t.Fatalf("after role change = %d", code)
	}
	b.login("viewer1", "viewer one password")
	_ = e.acct.SetDisabled(ctx, v.ID, true)
	if code, _, _ := b.get("/i/default/"); code != http.StatusSeeOther {
		t.Fatalf("after disable = %d", code)
	}
	_ = e.acct.SetDisabled(ctx, v.ID, false)
	b.login("viewer1", "viewer one password")
	now := time.Now()
	e.web.ss.mu.Lock()
	e.web.ss.now = func() time.Time { return now.Add(31 * time.Minute) }
	e.web.ss.mu.Unlock()
	if code, _, _ := b.get("/i/default/status"); code != http.StatusSeeOther {
		t.Fatalf("after idle = %d", code)
	}
}

// Administrators manage users; every change is audited; the audit page
// shows it. An unreachable LibreDCM is reported, not hidden. The admin token
// never appears on a page or in the log.
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
	_, about, _ := b.get("/about")
	if !strings.Contains(about, e.dcm.URL()) {
		t.Fatal("LibreDCM URL not shown")
	}
	e.dcm.Set(func(f *fakedcm.Server) { f.Down = true })
	code, down, _ := b.get("/i/default/exceptions")
	if code != 200 || !strings.Contains(down, "reach LibreDCM: LibreDCM unreachable") {
		t.Fatalf("dcm down page = %d", code)
	}
	for name, page := range map[string]string{"audit": body, "about": about, "down": down, "log": e.log.String()} {
		hasNot(t, name, page, "Bearer", "tok-SECRET")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
