package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mulcahyio/librekey/license"
	"github.com/mulcahyio/librekey/request"
)

var day = 24 * time.Hour

func lic(fp string, exp *time.Time) *license.License {
	term := license.TermAnnual
	if exp == nil {
		term = license.TermPerpetual
	}
	return &license.License{ID: "1", Key: "LK-X", Product: "libredcm", Customer: "C", Term: term, Expires: exp, GraceDays: 30, Fingerprint: fp}
}

func TestEvaluate(t *testing.T) {
	fp := "fp1-aaaaaaaaaaaa-bbbbbbbbbbbb-cccccccccccc"
	now := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	exp := now.Add(10 * day)
	past := now.Add(-10 * day)
	longPast := now.Add(-31 * day)
	cases := []struct {
		name    string
		l       *license.License
		product string
		fp      string
		want    State
		receive bool
		days    int
	}{
		{"valid", lic(fp, &exp), "libredcm", fp, StateValid, true, 10},
		{"perpetual", lic(fp, nil), "libredcm", fp, StateValid, true, -1},
		{"grace", lic(fp, &past), "libredcm", fp, StateGrace, true, 20},
		{"expired", lic(fp, &longPast), "libredcm", fp, StateExpired, false, 0},
		{"one part changed", lic(fp, &exp), "libredcm", "fp1-aaaaaaaaaaaa-bbbbbbbbbbbb-dddddddddddd", StateValid, true, 10},
		{"other machine", lic(fp, &exp), "libredcm", "fp1-111111111111-222222222222-cccccccccccc", StateWrongMachine, false, -1},
		{"other product", lic(fp, &exp), "libremwl", fp, StateWrongProduct, false, -1},
	}
	for _, c := range cases {
		st := Evaluate(c.l, c.product, c.fp, now, time.Time{})
		if st.State != c.want || st.CanReceive() != c.receive || st.DaysLeft != c.days {
			t.Errorf("%s: got %s receive=%v days=%d (%s)", c.name, st.State, st.CanReceive(), st.DaysLeft, st.Message)
		}
	}
	// Grace ends exactly 30 days after expiry.
	edge := now.Add(-30 * day)
	if st := Evaluate(lic(fp, &edge), "libredcm", fp, now, time.Time{}); st.State != StateExpired {
		t.Errorf("at grace end: %s", st.State)
	}
	// updates_until: a perpetual license does not cover later builds.
	l := lic(fp, nil)
	uu := now.Add(-day)
	l.UpdatesUntil = &uu
	if st := Evaluate(l, "libredcm", fp, now, now); st.State != StateNotCovered || st.CanReceive() {
		t.Errorf("updates_until: %s", st.State)
	}
	if st := Evaluate(l, "libredcm", fp, now, now.Add(-2*day)); st.State != StateValid {
		t.Errorf("older build: %s", st.State)
	}
}

// fakeServer signs whatever the request asks for; enough to test the client flow.
func fakeServer(t *testing.T, s *license.Signer, expiry *time.Time) *httptest.Server {
	issued := time.Now().UTC()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Request     request.Request `json:"request"`
			Fingerprint string          `json:"fingerprint"`
			Key         string          `json:"key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fp, key := body.Request.Fingerprint, body.Request.Key
		if r.URL.Path == "/api/v1/renew" {
			fp, key = body.Fingerprint, body.Key
			issued = issued.Add(time.Second)
		}
		if key == "LK-BAD" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "license key not found"})
			return
		}
		l := license.License{ID: "1", Key: key, Product: "libredcm", Customer: "C", Term: license.TermAnnual,
			Issued: issued, Expires: expiry, GraceDays: 30, Fingerprint: fp}
		text, err := s.Sign(l)
		if err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"license": text})
	}))
}

func TestManagerActivateRenew(t *testing.T) {
	pub, priv, _ := license.GenerateKey()
	signer := license.NewSigner(priv)
	exp := time.Now().Add(365 * day)
	srv := fakeServer(t, signer, &exp)
	defer srv.Close()
	dir := t.TempDir()
	m, err := New(Config{Product: "libredcm", Keyring: license.NewKeyring(pub), LicenseFile: filepath.Join(dir, "license.lic"),
		DataDir: dir, HostID: "host-1", Server: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.State != StateMissing || st.CanReceive() {
		t.Fatalf("before activation: %s", st.State)
	}
	if _, err := m.Activate(context.Background(), "LK-BAD", ""); err == nil || err.Error() != "license key not found" {
		t.Fatalf("bad key: %v", err)
	}
	st, err := m.Activate(context.Background(), "lk-abcde-fghjk-mnpqr-stvwx", "Room 1")
	if err != nil || st.State != StateValid {
		t.Fatalf("activate: %v %s", err, st.State)
	}
	if _, err := os.Stat(filepath.Join(dir, "license.lic")); err != nil {
		t.Fatal("license not saved")
	}
	// A fresh manager (restart) reads the saved file.
	m2, _ := New(Config{Product: "libredcm", Keyring: license.NewKeyring(pub), LicenseFile: filepath.Join(dir, "license.lic"), DataDir: dir, HostID: "host-1"})
	if m2.Status().State != StateValid {
		t.Fatal("not valid after restart")
	}
	first := m.Status().License.Issued
	st, err = m.Renew(context.Background())
	if err != nil || !st.License.Issued.After(first) {
		t.Fatalf("renew: %v", err)
	}
	// A license for another machine is refused and not saved over the good one.
	other, _ := signer.Sign(license.License{ID: "2", Key: "LK-Y", Product: "libredcm", Customer: "C", Term: license.TermPerpetual,
		GraceDays: 30, Fingerprint: "fp1-111111111111-222222222222-333333333333"})
	if _, err := m.Install(other); err == nil {
		t.Fatal("foreign license installed")
	}
	if m.Reload().State != StateValid {
		t.Fatal("good license overwritten")
	}
	// Same data volume on another PC: wrong machine.
	m3, _ := New(Config{Product: "libredcm", Keyring: license.NewKeyring(pub), LicenseFile: filepath.Join(dir, "license.lic"), DataDir: dir, HostID: "host-2"})
	_ = m3 // host differs but install id and disk are the same -> still one part changed
	if st := m3.Status(); st.State != StateValid && st.State != StateWrongMachine {
		t.Fatalf("moved volume: %s", st.State)
	}
}

func TestManagerClockExpiry(t *testing.T) {
	pub, priv, _ := license.GenerateKey()
	dir := t.TempDir()
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := now
	m, _ := New(Config{Product: "libredcm", Keyring: license.NewKeyring(pub), LicenseFile: filepath.Join(dir, "l.lic"), DataDir: dir, HostID: "h", Now: func() time.Time { return clock }})
	exp := now.Add(5 * day)
	text, _ := license.NewSigner(priv).Sign(license.License{ID: "1", Key: "LK-1", Product: "libredcm", Customer: "C", Term: license.TermAnnual, Issued: now, Expires: &exp, GraceDays: 30, Fingerprint: m.Fingerprint()})
	if _, err := m.Install(text); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		at   time.Duration
		want State
	}{{0, StateValid}, {6 * day, StateGrace}, {34 * day, StateGrace}, {36 * day, StateExpired}} {
		clock = now.Add(c.at)
		if st := m.Status(); st.State != c.want {
			t.Errorf("at +%v: %s", c.at, st.State)
		}
	}
}
