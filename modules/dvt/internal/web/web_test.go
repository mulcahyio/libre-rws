package web

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/runner"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

const pw = "correct horse battery"

type passValidator struct{}

func (passValidator) Validate(_ context.Context, files []string, names map[string]string) result.Validation {
	v := result.Validation{Versions: map[string]string{}}
	for _, f := range files {
		v.Files = append(v.Files, result.FileResult{File: f, Name: names[f]})
	}
	v.Finish()
	return v
}

func newTest(t *testing.T) (*Server, *store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), Options{Store: st, Runner: &runner.Runner{}, Validator: passValidator{}, Password: pw, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, st, ts
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func login(t *testing.T, ts *httptest.Server) (*http.Cookie, string) {
	t.Helper()
	resp, err := noRedirect().PostForm(ts.URL+"/login", url.Values{"password": {pw}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status %d", resp.StatusCode)
	}
	var c *http.Cookie
	for _, x := range resp.Cookies() {
		if x.Name == cookieName {
			c = x
		}
	}
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie flags: %+v", c)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/targets", nil)
	req.AddCookie(c)
	r2, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	m := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf token in page")
	}
	return c, string(m[1])
}

func post(t *testing.T, ts *httptest.Server, c *http.Cookie, path string, form url.Values) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// Verifies: DVT-SRS-061
func TestRefusesToStartWithoutPassword(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	for _, p := range []string{"", "   ", "short"} {
		if _, err := New(context.Background(), Options{Store: st, Password: p}); err == nil {
			t.Errorf("started with password %q", p)
		}
	}
}

// Verifies: DVT-SRS-061
func TestEverythingButLoginAndHealthNeedsSession(t *testing.T) {
	_, _, ts := newTest(t)
	c := noRedirect()
	for _, p := range []string{"/healthz", "/login", "/style.css"} {
		resp, _ := c.Get(ts.URL + p)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	for _, p := range []string{"/", "/validate", "/targets", "/plans", "/runs/20261110-120000-abcdef01", "/runs/20261110-120000-abcdef01/junit.xml"} {
		resp, _ := c.Get(ts.URL + p)
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
			t.Errorf("GET %s without session: %d", p, resp.StatusCode)
		}
	}
	if r := post(t, ts, nil, "/targets", url.Values{"name": {"x"}}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST without session: %d", r.StatusCode)
	}
	if r := post(t, ts, &http.Cookie{Name: cookieName, Value: "forged"}, "/targets", url.Values{}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("forged session: %d", r.StatusCode)
	}
}

// Verifies: DVT-SRS-061
func TestSessionExpiresAfterIdle(t *testing.T) {
	s, _, ts := newTest(t)
	c, _ := login(t, ts)
	now := time.Now()
	s.auth.now = func() time.Time { return now.Add(31 * time.Minute) }
	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.AddCookie(c)
	resp, _ := noRedirect().Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("idle session still valid: %d", resp.StatusCode)
	}
}

// Verifies: DVT-SRS-062
func TestCSRFAndLoginRateLimit(t *testing.T) {
	_, st, ts := newTest(t)
	c, csrf := login(t, ts)
	form := url.Values{"name": {"lab"}, "host": {"libredcm"}, "port": {"11112"}, "called_aet": {"LIBREDCM"}, "calling_aet": {"LIBREDVT"}, "class": {"lab"}}
	if r := post(t, ts, c, "/targets", form); r.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without CSRF token: %d", r.StatusCode)
	}
	form.Set("csrf", "0000")
	if r := post(t, ts, c, "/targets", form); r.StatusCode != http.StatusForbidden {
		t.Fatalf("POST with wrong CSRF token: %d", r.StatusCode)
	}
	form.Set("csrf", csrf)
	if r := post(t, ts, c, "/targets", form); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST with CSRF token: %d", r.StatusCode)
	}
	if _, ok, _ := st.Target("lab"); !ok {
		t.Fatal("target not saved")
	}
	// Five wrong passwords, then even the right one is refused for a while.
	for i := 0; i < loginMax; i++ {
		if r := post(t, ts, nil, "/login", url.Values{"password": {"wrong"}}); r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, r.StatusCode)
		}
	}
	if r := post(t, ts, nil, "/login", url.Values{"password": {pw}}); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate limit not applied: %d", r.StatusCode)
	}
}

// Verifies: DVT-SRS-060, DVT-SRS-066, DVT-SRS-045
func TestUploadValidateAndDelete(t *testing.T) {
	s, st, ts := newTest(t)
	c, csrf := login(t, ts)
	for _, p := range []string{"/", "/validate", "/targets", "/plans"} {
		req, _ := http.NewRequest("GET", ts.URL+p, nil)
		req.AddCookie(c)
		resp, _ := noRedirect().Do(req)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), "Do not upload real patient data") {
			t.Fatalf("%s: status %d or PHI warning missing", p, resp.StatusCode)
		}
		if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s: security headers missing", p)
		}
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("csrf", csrf)
	_ = mw.WriteField("label", "test")
	fw, _ := mw.CreateFormFile("files", "a.dcm")
	_, _ = fw.Write([]byte("DICM"))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/validate", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(c)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/validations/") {
		t.Fatalf("upload: %d %s", resp.StatusCode, loc)
	}
	s.Wait()
	id := strings.TrimPrefix(loc, "/validations/")
	v, err := st.Validation(id)
	if err != nil || v.Running || v.Verdict != result.Pass || len(v.Files) != 1 || v.Files[0].Name != "a.dcm" {
		t.Fatalf("validation %+v %v", v, err)
	}
	if _, err := os.Stat(st.UploadDir(id)); err != nil {
		t.Fatal("upload not stored")
	}
	if r := post(t, ts, c, "/validations/"+id+"/delete", url.Values{"csrf": {csrf}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", r.StatusCode)
	}
	if _, err := os.Stat(st.UploadDir(id)); !os.IsNotExist(err) {
		t.Fatal("uploaded files not deleted")
	}
}
