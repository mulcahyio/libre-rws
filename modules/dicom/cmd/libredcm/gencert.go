package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// genCert writes a self-signed certificate and key (tls.crt, tls.key) to dir,
// for installs that choose "self-signed" TLS. hosts lists DNS names and IP
// addresses for the certificate; the machine's hostname and localhost are
// always included. Existing files are kept unless force is set, so re-running
// the installer doesn't change a certificate peers may have pinned.
func genCert(dir, hosts string, force bool) error {
	crt, key := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if !force {
		if _, err := os.Stat(crt); err == nil {
			fmt.Println("certificate exists, kept:", crt)
			return nil
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	hn, _ := os.Hostname()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "LibreDCM " + hn, Organization: []string{"Libre RWS"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(0, 0, 825),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range append(strings.Split(hosts, ","), hn, "localhost", "127.0.0.1") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	fmt.Println("self-signed certificate written:", crt)
	return nil
}
