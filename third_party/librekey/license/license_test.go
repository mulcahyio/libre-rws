package license

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testLicense() License {
	exp := time.Date(2027, 11, 7, 0, 0, 0, 0, time.UTC)
	return License{ID: "a1", Key: NewKey(), Product: "libredcm", Customer: "Test Clinic", Plan: "standard",
		Term: TermAnnual, Issued: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC), Expires: &exp, GraceDays: 30,
		Fingerprint: "fp1-aaaaaaaaaaaa-bbbbbbbbbbbb-cccccccccccc"}
}

func TestSignVerify(t *testing.T) {
	pub, priv, _ := GenerateKey()
	s := NewSigner(priv)
	text, err := s.Sign(testLicense())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, armorBegin) {
		t.Fatal("no armor")
	}
	l, err := NewKeyring(pub).Verify(text)
	if err != nil {
		t.Fatal(err)
	}
	if l.Customer != "Test Clinic" || l.Expires == nil || l.GraceDays != 30 || l.Version != Version {
		t.Fatalf("round trip lost data: %+v", l)
	}
	// Survives re-wrapping (email clients, copy/paste with CRLF).
	if _, err := NewKeyring(pub).Verify(strings.ReplaceAll("note\r\n"+text, "\n", "\r\n  ")); err != nil {
		t.Fatalf("rewrapped: %v", err)
	}
}

func TestTamperAndUnknownKey(t *testing.T) {
	pub, priv, _ := GenerateKey()
	text, _ := NewSigner(priv).Sign(testLicense())
	raw, _ := dearmor(text)
	// Change the customer inside the signed payload: signature must fail.
	var env envelope
	_ = json.Unmarshal(raw, &env)
	p, _ := base64.RawURLEncoding.DecodeString(env.Payload)
	p = []byte(strings.Replace(string(p), "Test Clinic", "Free Clinic", 1))
	env.Payload = base64.RawURLEncoding.EncodeToString(p)
	b, _ := json.Marshal(env)
	if _, err := NewKeyring(pub).Verify(armor(b)); err != ErrBadSignature {
		t.Fatalf("tampered license: want ErrBadSignature, got %v", err)
	}
	pub2, _, _ := GenerateKey()
	if _, err := NewKeyring(pub2).Verify(text); err != ErrUnknownKey {
		t.Fatalf("want ErrUnknownKey, got %v", err)
	}
	// A signature from another key with this key's id must fail.
	_, priv3, _ := GenerateKey()
	other := NewSigner(priv3)
	other.KeyID = KeyID(pub)
	forged, _ := other.Sign(testLicense())
	if _, err := NewKeyring(pub).Verify(forged); err != ErrBadSignature {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
	if _, err := NewKeyring(pub).Verify("hello"); err != ErrFormat {
		t.Fatalf("want ErrFormat, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	_, priv, _ := GenerateKey()
	s := NewSigner(priv)
	l := testLicense()
	l.Term = TermPerpetual // with expiry: invalid
	if _, err := s.Sign(l); err == nil {
		t.Fatal("perpetual with expiry accepted")
	}
	l.Expires = nil
	if _, err := s.Sign(l); err != nil {
		t.Fatal(err)
	}
	l = testLicense()
	l.Expires = nil
	if _, err := s.Sign(l); err == nil {
		t.Fatal("annual without expiry accepted")
	}
	l = testLicense()
	l.Fingerprint = ""
	if _, err := s.Sign(l); err == nil {
		t.Fatal("license without machine accepted")
	}
}

func TestKeyRotation(t *testing.T) {
	pubOld, privOld, _ := GenerateKey()
	pubNew, privNew, _ := GenerateKey()
	ring := NewKeyring(pubOld, pubNew)
	for _, p := range []*Signer{NewSigner(privOld), NewSigner(privNew)} {
		text, _ := p.Sign(testLicense())
		if _, err := ring.Verify(text); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPEMRoundTrip(t *testing.T) {
	pub, priv, _ := GenerateKey()
	pp, _ := MarshalPrivateKey(priv)
	priv2, err := ParsePrivateKey(pp)
	if err != nil || !priv2.Equal(priv) {
		t.Fatal("private key round trip", err)
	}
	pubs, err := ParsePublicKeys(append(MarshalPublicKey(pub), MarshalPublicKey(pub)...))
	if err != nil || len(pubs) != 2 || !pubs[0].Equal(pub) {
		t.Fatal("public key round trip", err)
	}
}

func TestKeys(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		k := NewKey()
		if len(k) != 2+4+20 || seen[k] {
			t.Fatalf("bad or duplicate key %q", k)
		}
		seen[k] = true
		if NormalizeKey(strings.ToLower(k)) != k {
			t.Fatalf("normalize changed %q to %q", k, NormalizeKey(strings.ToLower(k)))
		}
	}
	if got := NormalizeKey(" lk-7q2mf-k9xwd-3htr8-vn4cO "); got != "LK-7Q2MF-K9XWD-3HTR8-VN4C0" {
		t.Fatalf("normalize: %q", got)
	}
}
