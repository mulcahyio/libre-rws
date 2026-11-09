package main

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

// Verifies: DCM-SRS-042
func TestGenCert(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if err := genCert(dir, "pacs.clinic.local, 192.168.1.50", false); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(pair.Certificate[0])
	if err := c.VerifyHostname("pacs.clinic.local"); err != nil {
		t.Error(err)
	}
	if err := c.VerifyHostname("192.168.1.50"); err != nil {
		t.Error(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "tls.key")); fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v, want 0600", fi.Mode().Perm())
	}
	before, _ := os.ReadFile(filepath.Join(dir, "tls.crt"))
	_ = genCert(dir, "", false) // kept
	after, _ := os.ReadFile(filepath.Join(dir, "tls.crt"))
	if string(before) != string(after) {
		t.Error("existing certificate replaced without force")
	}
	_ = genCert(dir, "", true)
	forced, _ := os.ReadFile(filepath.Join(dir, "tls.crt"))
	if string(forced) == string(before) {
		t.Error("force did not replace the certificate")
	}
}
