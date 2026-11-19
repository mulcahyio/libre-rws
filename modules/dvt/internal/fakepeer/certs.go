// Package fakepeer holds LibreDVT's test peers: a synthetic PKI and a
// scripted DICOM upper-layer peer written in Go (net, crypto/tls), so the
// tests of LibreDVT's association and TLS checks do not share the worker's
// libraries (pynetdicom, Python ssl/OpenSSL) (architecture.md §7, decision
// 5). Only tests import this package (architecture test); it is never part
// of a LibreDVT code path.
package fakepeer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Authority is a synthetic certificate authority.
type Authority struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	DER  []byte
	File string // PEM file of the certificate
}

// PKI is a synthetic test PKI: a root CA, an intermediate CA under it and an
// unrelated CA. Files are written to Dir.
type PKI struct {
	Dir     string
	Root    *Authority
	Inter   *Authority
	OtherCA *Authority
}

// Leaf is an issued end-entity certificate with its key.
type Leaf struct {
	CertFile string // PEM: leaf certificate only
	KeyFile  string // PEM: PKCS#8 private key (synthetic, test only)
	TLS      tls.Certificate
	DER      []byte
}

// LeafOpts describe a leaf certificate.
type LeafOpts struct {
	Issuer     *Authority // nil = self-signed
	CN         string
	DNS        []string
	IPs        []net.IP
	NotBefore  time.Time // zero = an hour ago
	NotAfter   time.Time // zero = a day from now
	Client     bool      // client-auth usage instead of server-auth
	WithIssuer bool      // serve the issuing (intermediate) certificate in the chain
}

var serial atomic.Int64

func nextSerial() *big.Int {
	return big.NewInt(1000 + serial.Add(1))
}

func writePEM(path, typ string, der []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600)
}

func newAuthority(dir, name string, parent *Authority) (*Authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: name, Organization: []string{"LibreDVT test PKI"}},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.Cert, parent.Key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	a := &Authority{Cert: cert, Key: key, DER: der, File: filepath.Join(dir, name+".pem")}
	return a, writePEM(a.File, "CERTIFICATE", der)
}

// NewPKI creates the authorities in dir.
func NewPKI(dir string) (*PKI, error) {
	p := &PKI{Dir: dir}
	var err error
	if p.Root, err = newAuthority(dir, "test-root-ca", nil); err != nil {
		return nil, err
	}
	if p.Inter, err = newAuthority(dir, "test-intermediate-ca", p.Root); err != nil {
		return nil, err
	}
	if p.OtherCA, err = newAuthority(dir, "unrelated-ca", nil); err != nil {
		return nil, err
	}
	return p, nil
}

// Issue creates a leaf certificate and writes name.pem and name.key.
func (p *PKI) Issue(name string, o LeafOpts) (*Leaf, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	nb, na := o.NotBefore, o.NotAfter
	if nb.IsZero() {
		nb = time.Now().Add(-time.Hour)
	}
	if na.IsZero() {
		na = time.Now().Add(24 * time.Hour)
	}
	usage := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	if o.Client {
		usage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	tmpl := &x509.Certificate{
		SerialNumber: nextSerial(),
		Subject:      pkix.Name{CommonName: o.CN, Organization: []string{"LibreDVT test PKI"}},
		DNSNames:     o.DNS,
		IPAddresses:  o.IPs,
		NotBefore:    nb,
		NotAfter:     na,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usage,
	}
	var der []byte
	if o.Issuer == nil {
		der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	} else {
		der, err = x509.CreateCertificate(rand.Reader, tmpl, o.Issuer.Cert, &key.PublicKey, o.Issuer.Key)
	}
	if err != nil {
		return nil, fmt.Errorf("issue %s: %w", name, err)
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	l := &Leaf{CertFile: filepath.Join(p.Dir, name+".pem"), KeyFile: filepath.Join(p.Dir, name+".key"), DER: der}
	if err := writePEM(l.CertFile, "CERTIFICATE", der); err != nil {
		return nil, err
	}
	if err := writePEM(l.KeyFile, "PRIVATE KEY", kb); err != nil {
		return nil, err
	}
	chain := [][]byte{der}
	if o.WithIssuer && o.Issuer != nil {
		chain = append(chain, o.Issuer.DER)
	}
	l.TLS = tls.Certificate{Certificate: chain, PrivateKey: key}
	return l, nil
}

// Pool returns a certificate pool holding the given authorities.
func Pool(as ...*Authority) *x509.CertPool {
	p := x509.NewCertPool()
	for _, a := range as {
		p.AddCert(a.Cert)
	}
	return p
}
