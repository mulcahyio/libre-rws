package redact

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/url"
	"strings"
	"testing"
)

// Synthetic test values only.
const token = "dvt-test-token_ABCdef0123456789+/=" // gitleaks:allow (synthetic test value)

// newKeyPEM generates a throwaway private key for the test, so no key
// material is committed.
func newKeyPEM(t *testing.T) []byte {
	t.Helper()
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// Verifies: DVT-SRS-152
func TestTokenAndEncodingsAreRedacted(t *testing.T) {
	var s Set
	if !s.Empty() {
		t.Fatal("zero set not empty")
	}
	s.AddToken(token)
	b := []byte(token)
	forms := []string{
		token,
		base64.StdEncoding.EncodeToString(b),
		base64.RawURLEncoding.EncodeToString(b),
		url.QueryEscape(token),
		url.PathEscape(token),
	}
	for _, f := range forms {
		in := "Authorization: Bearer " + f + " and again " + f
		out, n := s.String(in)
		if n != 2 || strings.Contains(out, f) || strings.Count(out, Placeholder) != 2 {
			t.Errorf("form %q: n=%d out=%q", f, n, out)
		}
		if !s.Contains(in) {
			t.Errorf("Contains missed %q", f)
		}
	}
	if s.Contains("nothing secret here") {
		t.Fatal("false positive")
	}
	// The input is not modified.
	in := []byte("x" + token)
	_, _ = s.Bytes(in)
	if string(in) != "x"+token {
		t.Fatal("input modified")
	}
}

// Verifies: DVT-SRS-152
func TestPEMKeyIsRedacted(t *testing.T) {
	keyPEM := newKeyPEM(t)
	var s Set
	s.AddPEM(keyPEM)
	body := strings.Split(string(keyPEM), "\n")[1]
	blk, _ := pem.Decode(keyPEM)
	der := base64.StdEncoding.EncodeToString(blk.Bytes)
	for _, in := range []string{string(keyPEM), "error near " + body, strings.TrimSpace(string(keyPEM)), "der " + der} {
		out, n := s.String(in)
		if n == 0 || strings.Contains(out, body) {
			t.Errorf("key material left in %q", out)
		}
	}
	// PEM armour lines alone are not secret.
	if s.Contains("-----BEGIN PRIVATE KEY-----") {
		t.Fatal("armour line treated as secret")
	}
}

// Verifies: DVT-SRS-152
func TestShortFormsAndNilSet(t *testing.T) {
	var s Set
	s.AddToken("abc")
	if !s.Empty() {
		t.Fatal("a form shorter than MinLen must not be scanned for")
	}
	var nilSet *Set
	if !nilSet.Empty() || nilSet.Contains(token) {
		t.Fatal("nil set must be empty")
	}
	if out, n := nilSet.String(token); n != 0 || out != token {
		t.Fatal("nil set changed input")
	}
}
