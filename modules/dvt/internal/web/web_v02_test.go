package web

import (
	"context"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/fakepeer"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/runner"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

// Synthetic bearer token for the tests (never a real credential).
const webToken = "dvt-synthetic-ui-token-0123456789abcdef" // gitleaks:allow (synthetic test value)

// fieldNameRe finds the names of a page's form fields.
var fieldNameRe = regexp.MustCompile(`<(?:input|select|textarea)[^>]*\bname="([^"]+)"`)

var escT = template.Must(template.New("esc").Parse("{{.}}"))

// esc escapes text as html/template does in element content.
func esc(s string) string {
	var b strings.Builder
	_ = escT.Execute(&b, s)
	return b.String()
}

func newTestWith(t *testing.T, o Options) (*Server, *store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o.Store, o.Validator, o.Password, o.Version = st, passValidator{}, pw, "test"
	if o.Runner == nil {
		o.Runner = &runner.Runner{}
	}
	s, err := New(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, st, ts
}

func get(t *testing.T, ts *httptest.Server, c *http.Cookie, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.AddCookie(c)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func postBody(t *testing.T, ts *httptest.Server, c *http.Cookie, path string, form url.Values) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func vals(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

// Verifies: DVT-SRS-146, DVT-SRS-140, DVT-SRS-141, DVT-SRS-103
func TestTargetFormsUseTheTargetFileSchema(t *testing.T) {
	// Each form gives exactly the target the same record in a target file
	// gives (libredvt run -targets FILE).
	cases := []struct {
		name, yaml string
		form       url.Values
	}{
		{"v0.1 DIMSE", "{name: a, class: lab, host: libredcm, port: 11112, called_aet: LIBREDCM, calling_aet: LIBREDVT}",
			vals("name", "a", "class", "lab", "host", "libredcm", "port", "11112", "called_aet", "LIBREDCM", "calling_aet", "LIBREDVT")},
		{"DIMSE form without TLS fields", "{name: a, protocol: dimse, host: h, port: 104, called_aet: PACS, calling_aet: LIBREDVT}",
			vals("protocol", "dimse", "name", "a", "class", "", "host", "h", "port", "104", "called_aet", "PACS", "calling_aet", "LIBREDVT",
				"tls_min_version", "", "tls_ca_file", "", "tls_server_name", "", "tls_client_cert_file", "", "tls_client_key_file", "", "tls_move_client_cert", "")},
		{"DIMSE TLS checkbox only", "{name: a, class: lab, protocol: dimse, host: h, port: 2762, called_aet: PACS, calling_aet: LIBREDVT, tls: {}}",
			vals("protocol", "dimse", "name", "a", "class", "lab", "host", "h", "port", "2762", "called_aet", "PACS", "calling_aet", "LIBREDVT", "tls", "on")},
		{"DIMSE mutual TLS", "{name: a, class: lab, protocol: dimse, host: pacs.lab.example, port: 2762, called_aet: PACS, calling_aet: DVT, tls: {min_version: \"1.3\", ca_file: /run/secrets/ca.pem, server_name: pacs.example, client_cert_file: /run/secrets/c.pem, client_key_file: /run/secrets/c.key, move_client_cert: optional}}",
			vals("protocol", "dimse", "name", "a", "class", "lab", "host", "pacs.lab.example", "port", "2762", "called_aet", "PACS", "calling_aet", "DVT",
				"tls_min_version", "1.3", "tls_ca_file", "/run/secrets/ca.pem", "tls_server_name", "pacs.example", "tls_client_cert_file", "/run/secrets/c.pem",
				"tls_client_key_file", "/run/secrets/c.key", "tls_move_client_cert", "optional")},
		{"DIMSE TLS by a field, checkbox forgotten", "{name: a, protocol: dimse, host: h, port: 1, called_aet: P, calling_aet: L, tls: {ca_file: /run/secrets/ca.pem}}",
			vals("protocol", "dimse", "name", "a", "host", "h", "port", "1", "called_aet", "P", "calling_aet", "L", "tls_ca_file", "/run/secrets/ca.pem")},
		{"DICOMweb https bearer", "{name: w, class: lab, protocol: dicomweb, base_url: \"https://pacs.lab.example/dicom-web/\", auth: {type: bearer, token_file: /run/secrets/tok}, tls: {ca_file: /run/secrets/ca.pem}}",
			vals("protocol", "dicomweb", "name", "w", "class", "lab", "base_url", "https://pacs.lab.example/dicom-web/", "auth_type", "bearer", "token_file", "/run/secrets/tok",
				"tls_min_version", "", "tls_ca_file", "/run/secrets/ca.pem", "tls_server_name", "", "tls_client_cert_file", "", "tls_client_key_file", "")},
		{"DICOMweb no auth", "{name: w, protocol: dicomweb, base_url: \"https://pacs.lab.example/dicom-web\", auth: {type: none}}",
			vals("protocol", "dicomweb", "name", "w", "class", "", "base_url", "https://pacs.lab.example/dicom-web", "auth_type", "none", "token_file", "")},
		{"DICOMweb loopback http bearer", "{name: w, class: lab, protocol: dicomweb, base_url: \"http://127.0.0.1:8042/dicom-web\", auth: {type: bearer, token_file: /run/secrets/tok}}",
			vals("protocol", "dicomweb", "name", "w", "class", "lab", "base_url", "http://127.0.0.1:8042/dicom-web", "auth_type", "bearer", "token_file", "/run/secrets/tok")},
	}
	for _, c := range cases {
		ts, err := store.ParseTargetsFile([]byte("targets:\n  - " + c.yaml + "\n"))
		if err != nil {
			t.Fatalf("%s: target file rejected: %v", c.name, err)
		}
		got, err := TargetFromForm(c.form)
		if err != nil {
			t.Fatalf("%s: form rejected: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, ts[0]) {
			t.Errorf("%s: form gives %+v (tls %+v auth %+v), file gives %+v (tls %+v auth %+v)", c.name, got, got.TLS, got.Auth, ts[0], ts[0].TLS, ts[0].Auth)
		}
	}
	// What the schema rejects, the form rejects: inline secrets and
	// switches that would weaken TLS are unknown fields; fields of the other
	// protocol, TLS below 1.2, TLS with http and repeated fields fail.
	dimse := func(kv ...string) url.Values {
		v := vals("protocol", "dimse", "name", "a", "class", "lab", "host", "h", "port", "104", "called_aet", "P", "calling_aet", "L")
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	web := func(kv ...string) url.Values {
		v := vals("protocol", "dicomweb", "name", "w", "class", "lab", "base_url", "https://pacs.lab.example/dicom-web", "auth_type", "none")
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	twice := web()
	twice["base_url"] = []string{"https://a.example/dw", "https://b.example/dw"}
	bad := map[string]struct {
		form url.Values
		want string
	}{
		"inline token":         {web("auth_type", "bearer", "token", webToken), `unknown field "token"`},
		"inline password":      {web("password", "x"), `unknown field "password"`},
		"inline private key":   {dimse("private_key", "x"), `unknown field "private_key"`},
		"insecure_skip_verify": {dimse("tls", "on", "insecure_skip_verify", "true"), `unknown field "insecure_skip_verify"`},
		"verify false":         {web("verify", "false"), `unknown field "verify"`},
		"check_hostname":       {dimse("check_hostname", "false"), `unknown field "check_hostname"`},
		"base_url on dimse":    {dimse("base_url", "https://x.example/dw"), "base_url is not allowed on a dimse target"},
		"host on dicomweb":     {web("host", "h"), "host is not allowed on a dicomweb target"},
		"auth on dimse":        {dimse("auth_type", "bearer", "token_file", "/run/secrets/t"), "auth is not allowed on a dimse target"},
		"TLS 1.1":              {dimse("tls_min_version", "1.1"), `tls.min_version must be "1.2" or "1.3"`},
		"tls not on":           {dimse("tls", "off"), "tls must be on or absent"},
		"tls with http":        {web("base_url", "http://pacs.example/dw", "tls_ca_file", "/run/secrets/ca.pem"), "tls is only allowed with an https base_url"},
		"bearer over http":     {web("base_url", "http://pacs.example/dw", "auth_type", "bearer", "token_file", "/run/secrets/t"), "bearer token over http"},
		"relative token file":  {web("auth_type", "bearer", "token_file", "tok"), "auth.token_file must be an absolute path"},
		"repeated field":       {twice, `field "base_url" given more than once`},
		"bad port":             {dimse("port", "x"), "port must be 1-65535"},
	}
	for name, c := range bad {
		_, err := TargetFromForm(c.form)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), webToken) {
			t.Errorf("%s: error message quotes the token", name)
		}
	}
}

// Verifies: DVT-SRS-146, DVT-SRS-141, DVT-SRS-152, DVT-SRS-062
func TestTargetsPageKeepsSecretsByReference(t *testing.T) {
	secrets := t.TempDir()
	pki, err := fakepeer.NewPKI(secrets)
	if err != nil {
		t.Fatal(err)
	}
	client, err := pki.Issue("libredvt-client", fakepeer.LeafOpts{Issuer: pki.Root, CN: "libredvt", Client: true})
	if err != nil {
		t.Fatal(err)
	}
	tok := filepath.Join(secrets, "web-token")
	if err := os.WriteFile(tok, []byte(webToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPEM, _ := os.ReadFile(client.KeyFile)
	keyBody := strings.Split(strings.TrimSpace(string(keyPEM)), "\n")[1] // first base64 line of the key

	_, st, ts := newTestWith(t, Options{Runner: &runner.Runner{SecretsDir: secrets}})
	c, csrf := login(t, ts)

	// CSRF is still required on the new forms.
	form := vals("protocol", "dicomweb", "name", "vendor-web-lab", "class", "lab", "base_url", "https://pacs.lab.example/dicom-web",
		"auth_type", "bearer", "token_file", tok, "tls_ca_file", pki.Root.File, "tls_client_cert_file", client.CertFile, "tls_client_key_file", client.KeyFile)
	if code, _ := postBody(t, ts, c, "/targets", form); code != http.StatusForbidden {
		t.Fatalf("DICOMweb form without CSRF token: %d", code)
	}
	form.Set("csrf", csrf)
	if code, body := postBody(t, ts, c, "/targets", form); code != http.StatusSeeOther {
		t.Fatalf("save DICOMweb target: %d %s", code, body)
	}
	dimse := vals("csrf", csrf, "protocol", "dimse", "name", "pacs-tls", "class", "production", "host", "pacs.lab.example", "port", "2762",
		"called_aet", "PACS", "calling_aet", "LIBREDVT", "tls", "on", "tls_ca_file", pki.Root.File)
	if code, body := postBody(t, ts, c, "/targets", dimse); code != http.StatusSeeOther {
		t.Fatalf("save DIMSE TLS target: %d %s", code, body)
	}
	w, ok, _ := st.Target("vendor-web-lab")
	if !ok || w.Proto() != store.DICOMweb || w.Auth.TokenFile != tok || w.TLS.ClientKeyFile != client.KeyFile {
		t.Fatalf("stored DICOMweb target: %+v", w)
	}
	if d, ok, _ := st.Target("pacs-tls"); !ok || !d.HasTLS() || d.TLS.MinVersion != "1.2" || d.TLS.MoveClientCert != "required" {
		t.Fatalf("stored DIMSE target: %+v", d)
	}

	// The page shows the paths and the file check, never the values, and
	// has no field that could take a secret value.
	code, page := get(t, ts, c, "/targets")
	if code != 200 {
		t.Fatalf("targets page: %d", code)
	}
	for _, want := range []string{esc(tok), esc(client.KeyFile), esc("https://pacs.lab.example/dicom-web"), "pacs.lab.example:2762", "TLS 1.2+", "auth bearer", `<span class="badge pass">ok</span>`, "Secrets by reference"} {
		if !strings.Contains(page, want) {
			t.Errorf("targets page lacks %q", want)
		}
	}
	if c := strings.Count(page, `<span class="badge pass">ok</span>`); c != 2 {
		t.Errorf("file check ok shown %d times, want 2 (both targets)", c)
	}
	for _, leak := range []string{webToken, keyBody, "PRIVATE KEY"} {
		if strings.Contains(page, leak) {
			t.Fatalf("targets page shows secret material %q", leak[:10])
		}
	}
	if strings.Contains(page, `type="password"`) || strings.Contains(page, `name="token"`) || strings.Contains(page, `name="private_key"`) || strings.Contains(strings.ToLower(page), "<script") {
		t.Error("targets page has a secret-value field or script")
	}
	for _, f := range []string{"tls_ca_file", "tls_client_key_file", "token_file", "base_url", "tls_move_client_cert", `name="protocol" value="dimse"`, `name="protocol" value="dicomweb"`} {
		if !strings.Contains(page, f) {
			t.Errorf("targets page lacks form field %s", f)
		}
	}
	// Every form field the page offers is one TargetFromForm accepts.
	for _, m := range fieldNameRe.FindAllStringSubmatch(page, -1) {
		if !targetFields[m[1]] {
			t.Errorf("form offers field %q that the server refuses", m[1])
		}
	}

	// A file outside the secrets directory is refused when saved; a missing
	// file under it is saved and reported by field name.
	out := vals("csrf", csrf, "protocol", "dicomweb", "name", "outside", "class", "lab", "base_url", "https://pacs.lab.example/dw", "auth_type", "bearer", "token_file", "/opt/elsewhere/token")
	if code, body := postBody(t, ts, c, "/targets", out); code != http.StatusBadRequest || !strings.Contains(body, "auth.token_file must lie under the secrets directory") {
		t.Fatalf("token file outside secrets dir: %d", code)
	}
	if _, ok, _ := st.Target("outside"); ok {
		t.Fatal("target with a file outside the secrets directory was saved")
	}
	missing := vals("csrf", csrf, "protocol", "dicomweb", "name", "missing", "class", "lab", "base_url", "https://pacs.lab.example/dw", "auth_type", "bearer", "token_file", filepath.Join(secrets, "nope"))
	if code, _ := postBody(t, ts, c, "/targets", missing); code != http.StatusSeeOther {
		t.Fatalf("missing token file: %d", code)
	}
	if _, page = get(t, ts, c, "/targets"); !strings.Contains(page, "auth.token_file: file not found or not readable") {
		t.Error("missing token file not reported on the page")
	}
	// An inline token is refused, and the answer does not echo it.
	inline := vals("csrf", csrf, "protocol", "dicomweb", "name", "inline", "base_url", "https://pacs.lab.example/dw", "auth_type", "bearer", "token", webToken)
	code, body := postBody(t, ts, c, "/targets", inline)
	if code != http.StatusBadRequest || !strings.Contains(body, `unknown field &#34;token&#34;`) || strings.Contains(body, webToken) {
		t.Fatalf("inline token: %d, echoed %v", code, strings.Contains(body, webToken))
	}
	// Nothing in the data directory holds a secret value.
	_ = filepath.Walk(st.Dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			if strings.Contains(string(b), webToken) || strings.Contains(string(b), keyBody) {
				t.Errorf("secret value in %s", p)
			}
		}
		return nil
	})
}

// Verifies: DVT-SRS-156, DVT-SRS-146
func TestPlansPageListsBundledExamplePlans(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	plans := filepath.Join(filepath.Dir(here), "..", "..", "plans")
	_, st, ts := newTestWith(t, Options{PlansDir: plans})
	_ = st.PutTarget(store.Target{Name: "web", Class: "lab", Protocol: "dicomweb", BaseURL: "https://pacs.lab.example/dw"})
	c, _ := login(t, ts)
	code, page := get(t, ts, c, "/plans")
	if code != 200 {
		t.Fatalf("plans page: %d", code)
	}
	for _, want := range []string{"LibreDCM smoke (DIMSE + TLS)", "Generic DICOM TLS audit", "Generic DICOMweb audit", "LibreDCM conformance",
		"associate, echo, store, tls_reject", "tls_reject, stow, qido, wado", "web (dicomweb, TLS, lab)"} {
		if !strings.Contains(page, esc(want)) {
			t.Errorf("plans page lacks %q", want)
		}
	}
	if strings.Contains(page, "invalid") || strings.Contains(page, "sev-error") {
		t.Error("a bundled plan is shown as invalid")
	}
}
