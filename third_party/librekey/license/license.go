// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Patrick Mulcahy, Mulcahy.io (LibreKey client)

// Package license defines the LibreKey license file: a JSON document signed
// with Ed25519, wrapped in a text armor so it can be emailed, pasted or saved.
//
// LibreKey - licensing for Libre Systems modules
// Developed by Patrick Mulcahy, Mulcahy.io - dev@mulcahy.io
package license

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Version of the license document format.
const Version = 1

// Terms.
const (
	TermAnnual    = "annual"
	TermPerpetual = "perpetual"
)

const (
	armorBegin = "-----BEGIN LIBREKEY LICENSE-----"
	armorEnd   = "-----END LIBREKEY LICENSE-----"
)

// License is the signed content of a license file.
type License struct {
	Version      int        `json:"v"`
	ID           string     `json:"id"`          // license id (UUID)
	Key          string     `json:"key"`         // human license key, e.g. LK-ABCD-...
	Product      string     `json:"product"`     // product code, e.g. "libredcm"
	Customer     string     `json:"customer"`    // organisation name
	CustomerID   string     `json:"customer_id"` // server customer id
	Site         string     `json:"site,omitempty"`
	Plan         string     `json:"plan"` // plan code
	Term         string     `json:"term"` // annual | perpetual
	SupportTier  string     `json:"support,omitempty"`
	Issued       time.Time  `json:"issued"`
	Expires      *time.Time `json:"expires,omitempty"`       // nil = never (perpetual)
	UpdatesUntil *time.Time `json:"updates_until,omitempty"` // builds released after this date are not covered
	GraceDays    int        `json:"grace_days"`
	MachineID    string     `json:"machine_id,omitempty"` // server machine id
	Fingerprint  string     `json:"fingerprint"`          // machine fingerprint the file is locked to
	Server       string     `json:"server,omitempty"`     // LibreKey server URL for renewals
}

// envelope is the outer, armored structure.
type envelope struct {
	Version int    `json:"v"`
	KeyID   string `json:"kid"`
	Payload string `json:"payload"` // base64url(JSON(License))
	Sig     string `json:"sig"`     // base64url(Ed25519(payload bytes))
}

// Errors returned by Verify.
var (
	ErrFormat       = errors.New("license: not a LibreKey license file")
	ErrUnknownKey   = errors.New("license: signed with an unknown key")
	ErrBadSignature = errors.New("license: signature is not valid")
)

// Validate checks the fields a signer must fill in.
func (l *License) Validate() error {
	switch {
	case l.ID == "" || l.Key == "" || l.Product == "" || l.Customer == "":
		return errors.New("license: id, key, product and customer are required")
	case l.Fingerprint == "":
		return errors.New("license: fingerprint is required (licenses are machine-locked)")
	case l.Term != TermAnnual && l.Term != TermPerpetual:
		return fmt.Errorf("license: term must be %q or %q", TermAnnual, TermPerpetual)
	case l.Term == TermAnnual && l.Expires == nil:
		return errors.New("license: an annual license needs an expiry date")
	case l.Term == TermPerpetual && l.Expires != nil:
		return errors.New("license: a perpetual license has no expiry date")
	case l.GraceDays < 0:
		return errors.New("license: grace_days cannot be negative")
	}
	return nil
}

// Signer holds a private signing key and its id.
type Signer struct {
	KeyID string
	key   ed25519.PrivateKey
}

// NewSigner wraps an Ed25519 private key.
func NewSigner(priv ed25519.PrivateKey) *Signer {
	return &Signer{KeyID: KeyID(priv.Public().(ed25519.PublicKey)), key: priv}
}

// Public returns the signer's public key.
func (s *Signer) Public() ed25519.PublicKey { return s.key.Public().(ed25519.PublicKey) }

// Sign validates and signs l, returning the armored license file.
func (s *Signer) Sign(l License) (string, error) {
	l.Version = Version
	if err := l.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	env := envelope{Version: Version, KeyID: s.KeyID,
		Payload: base64.RawURLEncoding.EncodeToString(payload),
		Sig:     base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.key, payload))}
	raw, _ := json.Marshal(env)
	return armor(raw), nil
}

// Keyring maps key ids to public keys. Builds embed the current and next keys
// so the signing key can be rotated without breaking installed licenses.
type Keyring map[string]ed25519.PublicKey

// NewKeyring builds a keyring from public keys.
func NewKeyring(keys ...ed25519.PublicKey) Keyring {
	k := Keyring{}
	for _, p := range keys {
		k[KeyID(p)] = p
	}
	return k
}

// Verify checks the signature of an armored license file and returns its content.
// It does not check dates or the machine; see client.Evaluate.
func (k Keyring) Verify(text string) (*License, error) {
	raw, err := dearmor(text)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Payload == "" || env.Sig == "" {
		return nil, ErrFormat
	}
	pub, ok := k[env.KeyID]
	if !ok {
		return nil, ErrUnknownKey
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(env.Payload)
	sig, err2 := base64.RawURLEncoding.DecodeString(env.Sig)
	if err1 != nil || err2 != nil {
		return nil, ErrFormat
	}
	if !ed25519.Verify(pub, payload, sig) {
		return nil, ErrBadSignature
	}
	var l License
	dec := json.NewDecoder(bytes.NewReader(payload))
	if err := dec.Decode(&l); err != nil {
		return nil, ErrFormat
	}
	if l.Version > Version {
		return nil, fmt.Errorf("license: format version %d is newer than this program supports (%d)", l.Version, Version)
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return &l, nil
}

// KeyID is a short, stable id for a public key (first 8 bytes of SHA-256, hex).
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:8])
}

// GenerateKey creates a new Ed25519 signing key pair.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// MarshalPrivateKey encodes a private key as PKCS#8 PEM.
func MarshalPrivateKey(priv ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// MarshalPublicKey encodes a public key as PKIX PEM.
func MarshalPublicKey(pub ed25519.PublicKey) []byte {
	der, _ := x509.MarshalPKIXPublicKey(pub)
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// ParsePrivateKey decodes a PKCS#8 PEM Ed25519 private key.
func ParsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	b, _ := pem.Decode(data)
	if b == nil {
		return nil, errors.New("license: no PEM block in private key")
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("license: private key is not Ed25519")
	}
	return priv, nil
}

// ParsePublicKeys decodes one or more PKIX PEM Ed25519 public keys.
func ParsePublicKeys(data []byte) ([]ed25519.PublicKey, error) {
	var out []ed25519.PublicKey
	for {
		var b *pem.Block
		b, data = pem.Decode(data)
		if b == nil {
			break
		}
		k, err := x509.ParsePKIXPublicKey(b.Bytes)
		if err != nil {
			return nil, err
		}
		pub, ok := k.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("license: public key is not Ed25519")
		}
		out = append(out, pub)
	}
	if len(out) == 0 {
		return nil, errors.New("license: no public keys found")
	}
	return out, nil
}

func armor(raw []byte) string {
	enc := base64.StdEncoding.EncodeToString(raw)
	var b strings.Builder
	b.WriteString(armorBegin + "\n")
	for len(enc) > 64 {
		b.WriteString(enc[:64] + "\n")
		enc = enc[64:]
	}
	b.WriteString(enc + "\n" + armorEnd + "\n")
	return b.String()
}

func dearmor(text string) ([]byte, error) {
	i := strings.Index(text, armorBegin)
	j := strings.Index(text, armorEnd)
	if i < 0 || j < i {
		return nil, ErrFormat
	}
	body := strings.Join(strings.Fields(text[i+len(armorBegin):j]), "")
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, ErrFormat
	}
	return raw, nil
}
