package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/fakepeer"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/redact"
)

const synthToken = "dvt-synthetic-bearer-0123456789abcdef" // gitleaks:allow (synthetic test value)

// Verifies: DVT-SRS-110
func TestV01TargetRecordLoadsAsDIMSE(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A registry written by v0.1: no protocol, one record with a class.
	v01 := `[{"name":"lab","host":"libredcm","port":11112,"called_aet":"LIBREDCM","calling_aet":"LIBREDVT","class":"lab"},{"name":"x","host":"h","port":1,"called_aet":"A","calling_aet":"B"}]`
	if err := os.WriteFile(filepath.Join(s.Dir, "targets.json"), []byte(v01), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, err := s.Targets()
	if err != nil {
		t.Fatal(err)
	}
	if ts[0].Protocol != DIMSE || ts[0].Class != Lab || ts[1].Protocol != DIMSE || ts[1].Class != Production {
		t.Fatalf("%+v", ts)
	}
	if got := ts[0].Describe(); got != "lab (libredcm:11112, called AE LIBREDCM, calling AE LIBREDVT, class lab)" {
		t.Fatalf("v0.1 description changed: %s", got)
	}
	if ts[0].HasTLS() || ts[0].Endpoint() != "libredcm:11112" {
		t.Fatalf("%+v", ts[0])
	}
	// A record without protocol keeps meaning dimse even unnormalised.
	if (Target{}).Proto() != DIMSE {
		t.Fatal("empty protocol must mean dimse")
	}
}

func web(base string) Target {
	return Target{Name: "w", Class: Lab, Protocol: DICOMweb, BaseURL: base}
}

// Verifies: DVT-SRS-110
func TestDICOMwebBaseURLRules(t *testing.T) {
	good := []string{"https://pacs.lab.example/dicom-web", "http://127.0.0.1:8042/dicom-web/", "https://[::1]:8443", "http://orthanc"}
	for _, u := range good {
		tg := web(u)
		if err := tg.Normalize(); err != nil {
			t.Errorf("%s rejected: %v", u, err)
		}
		if strings.HasSuffix(tg.BaseURL, "/") {
			t.Errorf("%s: trailing slash kept", tg.BaseURL)
		}
	}
	bad := []string{"", "ftp://h/x", "https://user:pw@h/x", "https://h/x?a=1", "https://h/x#frag", "https:///x", "https://h:0/x", "https://h:99999/x", "https://h:/x", "h/x", "https://-h/x", "mailto:a@b"}
	for _, u := range bad {
		tg := web(u)
		if err := tg.Normalize(); err == nil || !strings.Contains(err.Error(), "base_url") {
			t.Errorf("%q accepted (err %v)", u, err)
		}
	}
}

// Verifies: DVT-SRS-112
func TestBearerOverHTTPOnlyToLoopback(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:8042/dicom-web", "http://localhost/dw", "http://[::1]/dw", "https://pacs.example/dw"} {
		tg := web(u)
		tg.Auth = &Auth{Type: AuthBearer, TokenFile: "/run/secrets/t"}
		if err := tg.Normalize(); err != nil {
			t.Errorf("%s rejected: %v", u, err)
		}
	}
	for _, u := range []string{"http://pacs.example/dw", "http://10.0.0.5/dw", "http://127.0.0.1.nip.io/dw"} {
		tg := web(u)
		tg.Auth = &Auth{Type: AuthBearer, TokenFile: "/run/secrets/t"}
		if err := tg.Normalize(); err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Errorf("%s accepted (err %v)", u, err)
		}
	}
}

// Verifies: DVT-SRS-140, DVT-SRS-141
func TestTargetSchemaIsStrict(t *testing.T) {
	dimse := `{"name":"d","class":"lab","protocol":"dimse","host":"h","port":104,"called_aet":"A","calling_aet":"B"`
	webT := `{"name":"w","class":"lab","protocol":"dicomweb","base_url":"https://h/dw"`
	good := []string{
		"[" + dimse + "}]",
		"[" + dimse + `,"tls":{"ca_file":"/s/ca.pem","client_cert_file":"/s/c.pem","client_key_file":"/s/c.key","server_name":"pacs.lab","min_version":"1.3","move_client_cert":"optional"}}]`,
		"[" + webT + "}]",
		"[" + webT + `,"auth":{"type":"bearer","token_file":"/s/tok"},"tls":{"ca_file":"/s/ca.pem"}}]`,
	}
	for _, g := range good {
		if _, err := DecodeTargetsJSON([]byte(g)); err != nil {
			t.Errorf("valid record rejected: %v\n%s", err, g)
		}
	}
	bad := map[string]string{
		"unknown field":           "[" + dimse + `,"colour":"red"}]`,
		"inline token":            "[" + webT + `,"auth":{"type":"bearer","token":"x"}}]`,
		"inline password":         "[" + webT + `,"password":"x"}]`,
		"inline private key":      "[" + dimse + `,"tls":{"private_key":"x"}}]`,
		"insecure switch":         "[" + dimse + `,"tls":{"insecure_skip_verify":true}}]`,
		"verify switch":           "[" + webT + `,"tls":{"verify":false}}]`,
		"check_hostname switch":   "[" + webT + `,"tls":{"check_hostname":false}}]`,
		"wrong type":              `[{"name":"d","host":"h","port":"104","called_aet":"A","calling_aet":"B"}]`,
		"called_aet on dicomweb":  "[" + webT + `,"called_aet":"A"}]`,
		"host on dicomweb":        "[" + webT + `,"host":"h"}]`,
		"base_url on dimse":       "[" + dimse + `,"base_url":"https://h"}]`,
		"auth on dimse":           "[" + dimse + `,"auth":{"type":"none"}}]`,
		"missing base_url":        `[{"name":"w","protocol":"dicomweb"}]`,
		"missing host":            `[{"name":"d","port":104,"called_aet":"A","calling_aet":"B"}]`,
		"unknown protocol":        `[{"name":"w","protocol":"hl7"}]`,
		"bearer without file":     "[" + webT + `,"auth":{"type":"bearer"}}]`,
		"relative token file":     "[" + webT + `,"auth":{"type":"bearer","token_file":"tok"}}]`,
		"token file without auth": "[" + webT + `,"auth":{"type":"none","token_file":"/s/t"}}]`,
		"basic auth":              "[" + webT + `,"auth":{"type":"basic"}}]`,
		"tls with http":           `[{"name":"w","protocol":"dicomweb","base_url":"http://127.0.0.1/dw","tls":{}}]`,
		"tls 1.1":                 "[" + dimse + `,"tls":{"min_version":"1.1"}}]`,
		"cert without key":        "[" + dimse + `,"tls":{"client_cert_file":"/s/c.pem"}}]`,
		"relative ca":             "[" + dimse + `,"tls":{"ca_file":"ca.pem"}}]`,
		"move_client_cert on web": "[" + webT + `,"tls":{"move_client_cert":"required"}}]`,
		"bad move_client_cert":    "[" + dimse + `,"tls":{"move_client_cert":"never"}}]`,
		"bad server name":         "[" + dimse + `,"tls":{"server_name":"a b"}}]`,
		"duplicate name":          "[" + dimse + "}," + dimse + "}]",
		"two documents":           "[" + dimse + "}] []",
		"bad class":               `[{"name":"d","class":"staging","host":"h","port":1,"called_aet":"A","calling_aet":"B"}]`,
	}
	for name, b := range bad {
		if _, err := DecodeTargetsJSON([]byte(b)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The error names the field.
	_, err := DecodeTargetsJSON([]byte("[" + webT + `,"auth":{"type":"bearer","token":"x"}}]`))
	if err == nil || !strings.Contains(err.Error(), `"token"`) {
		t.Fatalf("error does not name the field: %v", err)
	}
	// A registry with a bad record is not loaded at all.
	s, _ := Open(t.TempDir())
	_ = os.WriteFile(filepath.Join(s.Dir, "targets.json"), []byte(bad["insecure switch"]), 0o600)
	if _, err := s.Targets(); err == nil {
		t.Fatal("registry with an insecure field loaded")
	}
	if err := s.PutTarget(Target{Name: "n", Host: "h", Port: 1, CalledAET: "A", CallingAET: "B"}); err == nil {
		t.Fatal("PutTarget wrote over an invalid registry")
	}
}

// Verifies: DVT-SRS-140, DVT-SRS-141
func TestTargetFileYAMLSameSchema(t *testing.T) {
	good := `targets:
  - name: vendor-pacs-lab
    class: lab
    protocol: dimse
    host: pacs.lab.example
    port: 2762
    called_aet: VENDORPACS
    tls:
      ca_file: /run/secrets/client-lab-ca.pem
      client_cert_file: /run/secrets/libredvt-client.pem
      client_key_file: /run/secrets/libredvt-client.key
  - name: vendor-web-lab
    class: lab
    protocol: dicomweb
    base_url: https://pacs.lab.example/dicom-web
    auth:
      type: bearer
      token_file: /run/secrets/vendor-web-token
    tls:
      ca_file: /run/secrets/client-lab-ca.pem
`
	ts, err := ParseTargetsFile([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].CallingAET != "LIBREDVT" || ts[0].Protocol != DIMSE || ts[1].Auth.Type != AuthBearer || ts[0].TLS.MinVersion != "1.2" || ts[0].TLS.MoveClientCert != "required" {
		t.Fatalf("%+v", ts)
	}
	bad := map[string]string{
		"unknown key":    strings.Replace(good, "    class: lab\n    protocol: dimse", "    class: lab\n    colour: red\n    protocol: dimse", 1),
		"inline token":   strings.Replace(good, "token_file: /run/secrets/vendor-web-token", "token: abc", 1),
		"insecure":       strings.Replace(good, "      ca_file: /run/secrets/client-lab-ca.pem\n      client_cert_file", "      insecure_skip_verify: true\n      client_cert_file", 1),
		"other protocol": strings.Replace(good, "    base_url: https://pacs.lab.example/dicom-web\n", "    base_url: https://pacs.lab.example/dicom-web\n    called_aet: X\n", 1),
		"empty":          "",
		"no targets":     "targets: []\n",
		"two documents":  good + "---\ntargets: []\n",
		"wrong type":     strings.Replace(good, "port: 2762", "port: [1]", 1),
	}
	for name, b := range bad {
		if _, err := ParseTargetsFile([]byte(b)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Verifies: DVT-SRS-141
func TestLoadSecretsChecksEveryReferencedFile(t *testing.T) {
	dir := t.TempDir()
	tok := writeFile(t, dir, "tok", synthToken+"\n")
	pki, err := fakepeer.NewPKI(dir)
	if err != nil {
		t.Fatal(err)
	}
	ca := pki.Root.File
	empty := writeFile(t, dir, "empty", "")
	blank := writeFile(t, dir, "blank", " \n\n")
	short := writeFile(t, dir, "short", "abc\n")
	twoLines := writeFile(t, dir, "two", synthToken+"\n"+synthToken+"\n")
	spaces := writeFile(t, dir, "spaces", "has spaces in the token value\n")
	_ = os.Mkdir(filepath.Join(dir, "adir"), 0o750)

	tg := web("https://pacs.example/dw")
	tg.Auth = &Auth{Type: AuthBearer, TokenFile: tok}
	tg.TLS = &TLS{CAFile: ca}
	set := &redact.Set{}
	if err := tg.LoadSecrets("", set); err != nil {
		t.Fatal(err)
	}
	if !set.Contains("Bearer "+synthToken) || set.Contains(synthToken[:6]) {
		t.Fatal("token not registered for redaction")
	}
	// Under the secrets directory: allowed; outside: refused.
	if err := tg.LoadSecrets(dir, &redact.Set{}); err != nil {
		t.Fatalf("file under secrets dir refused: %v", err)
	}
	if err := tg.LoadSecrets(filepath.Join(dir, "adir"), &redact.Set{}); err == nil || !strings.Contains(err.Error(), "secrets directory") {
		t.Fatalf("file outside secrets dir accepted: %v", err)
	}
	cases := map[string]struct{ path, want string }{
		"missing":   {filepath.Join(dir, "nope"), "not found"},
		"empty":     {empty, "file is empty"},
		"blank":     {blank, "file is empty"},
		"directory": {filepath.Join(dir, "adir"), "not a regular file"},
		"short":     {short, "at least 16 characters"},
		"two lines": {twoLines, "on one line"},
		"spaces":    {spaces, "at least 16 characters"},
		"relative":  {"tok", "absolute path"},
		"climbing":  {dir + "/adir/../../" + filepath.Base(dir) + "/nope", "not found"},
	}
	for name, c := range cases {
		bad := tg
		bad.Auth = &Auth{Type: AuthBearer, TokenFile: c.path}
		err := bad.LoadSecrets("", &redact.Set{})
		if err == nil || !strings.Contains(err.Error(), "auth.token_file") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, err)
		}
		if err != nil && strings.Contains(err.Error(), synthToken) {
			t.Errorf("%s: error holds the token", name)
		}
	}
	// A missing CA or client key fails the same way.
	d := Target{Name: "d", Host: "h", Port: 1, CalledAET: "A", CallingAET: "B", TLS: &TLS{ClientCertFile: ca, ClientKeyFile: filepath.Join(dir, "nokey")}}
	if err := d.LoadSecrets("", &redact.Set{}); err == nil || !strings.Contains(err.Error(), "tls.client_key_file") {
		t.Fatalf("missing key: %v", err)
	}
	// A target without references loads nothing and passes.
	plain := Target{Name: "p", Host: "h", Port: 1, CalledAET: "A", CallingAET: "B"}
	s := &redact.Set{}
	if err := plain.LoadSecrets("/run/secrets", s); err != nil || !s.Empty() {
		t.Fatalf("plain target: %v", err)
	}
}

// Verifies: DVT-SRS-101, DVT-SRS-104
func TestLoadSecretsChecksCAAndClientKeyPair(t *testing.T) {
	dir := t.TempDir()
	pki, err := fakepeer.NewPKI(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := pki.Issue("client-a", fakepeer.LeafOpts{Issuer: pki.Root, CN: "a", Client: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := pki.Issue("client-b", fakepeer.LeafOpts{Issuer: pki.Root, CN: "b", Client: true})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(a.KeyFile)
	base := func() Target {
		return Target{Name: "d", Host: "h", Port: 1, CalledAET: "A", CallingAET: "B",
			TLS: &TLS{CAFile: pki.Root.File, ClientCertFile: a.CertFile, ClientKeyFile: a.KeyFile}}
	}
	good := base()
	set := &redact.Set{}
	if err := good.LoadSecrets("", set); err != nil {
		t.Fatalf("good pair refused: %v", err)
	}
	if !set.Contains(string(key)) {
		t.Fatal("client key not registered for redaction")
	}
	cases := map[string]struct {
		mod  func(*Target)
		want string
	}{
		"mismatched key": {func(x *Target) { x.TLS.ClientKeyFile = b.KeyFile }, "do not form a usable pair"},
		"key is a cert":  {func(x *Target) { x.TLS.ClientKeyFile = a.CertFile }, "do not form a usable pair"},
		"CA is not PEM":  {func(x *Target) { x.TLS.CAFile = writeFile(t, dir, "junk.pem", "not a certificate\n") }, "tls.ca_file: no usable PEM CA certificate"},
		"CA holds a key": {func(x *Target) { x.TLS.CAFile = a.KeyFile }, "tls.ca_file: no usable PEM CA certificate"},
		"CA garbage DER": {func(x *Target) {
			x.TLS.CAFile = writeFile(t, dir, "bad.pem", "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
		}, "tls.ca_file"},
		"CA trailing junk": {func(x *Target) {
			c, _ := os.ReadFile(pki.Root.File)
			x.TLS.CAFile = writeFile(t, dir, "tj.pem", string(c)+"junk\n")
		}, "tls.ca_file"},
	}
	for name, c := range cases {
		x := base()
		c.mod(&x)
		err := x.LoadSecrets("", &redact.Set{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, err)
		}
		if err != nil && strings.Contains(err.Error(), "PRIVATE KEY") {
			t.Errorf("%s: error quotes key material", name)
		}
	}
}
