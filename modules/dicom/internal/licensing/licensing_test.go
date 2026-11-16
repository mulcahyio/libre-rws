package licensing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mulcahyio/librekey/client"
	"github.com/mulcahyio/librekey/license"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func gauge(g prometheus.Gauge) float64 {
	var m dto.Metric
	_ = g.Write(&m)
	return m.GetGauge().GetValue()
}

type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logBuf) count(s string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.b.String(), s)
}

func setup(t *testing.T, state client.State) (*Licensing, *fakeClock, *logBuf, *client.State) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2027, 3, 1, 9, 0, 0, 0, time.UTC)}
	logs := &logBuf{}
	st := state
	l := newLicensing(func() client.Status { return client.Status{State: st, DaysLeft: 5} },
		filepath.Join(t.TempDir(), "license-ack.json"), clk.now,
		slog.New(slog.NewTextHandler(logs, nil)), prometheus.NewRegistry())
	return l, clk, logs, &st
}

// Unlicensed: hourly error, reloads blocked, metric set; an acknowledgement
// silences it (with an audit record) for 24 hours, then it starts again.
// Verifies: DCM-SRS-059, DCM-SRS-060, DCM-SRS-061
func TestUnlicensedNagAndAcknowledgement(t *testing.T) {
	l, clk, logs, _ := setup(t, client.StateExpired)
	const nag = "LibreDCM is unlicensed"

	if logs.count(nag) != 1 {
		t.Fatalf("expected an immediate error on start, got %d", logs.count(nag))
	}
	if ok, _ := l.ConfigChangesAllowed(); ok {
		t.Fatal("config reload allowed while acknowledgement is required")
	}
	if gauge(l.ackRequired) != 1 || gauge(l.state) != 2 {
		t.Fatal("metrics not set for unlicensed state")
	}
	clk.add(30 * time.Minute)
	l.tick()
	if logs.count(nag) != 1 {
		t.Fatal("nagged again before an hour passed")
	}
	clk.add(31 * time.Minute)
	l.tick()
	if logs.count(nag) != 2 {
		t.Fatal("no hourly reminder")
	}

	if _, err := l.Acknowledge("  "); err == nil {
		t.Fatal("acknowledgement without a name accepted")
	}
	if _, err := l.Acknowledge("Pat Admin"); err != nil {
		t.Fatal(err)
	}
	l.tick()
	if logs.count("event=license_acknowledged") != 1 || logs.count("by=\"Pat Admin\"") != 1 {
		t.Fatal("acknowledgement not recorded as an audit record")
	}
	if ok, _ := l.ConfigChangesAllowed(); !ok || gauge(l.ackRequired) != 0 {
		t.Fatal("acknowledgement did not lift the block")
	}
	clk.add(2 * time.Hour)
	l.tick()
	if logs.count(nag) != 2 {
		t.Fatal("nagged while acknowledged")
	}
	clk.add(22 * time.Hour) // 24 h after the acknowledgement
	l.tick()
	if logs.count(nag) != 3 || l.AckRequired() == false {
		t.Fatal("reminders did not resume after 24 hours")
	}
	if logs.count("event=license_acknowledged") != 1 {
		t.Fatal("old acknowledgement reported twice")
	}
}

// Grace period: daily warning only, no block, no acknowledgement needed.
// Verifies: DCM-SRS-059
func TestGraceWarnsDaily(t *testing.T) {
	l, clk, logs, _ := setup(t, client.StateGrace)
	const warn = "license expired: grace period"
	for i := 0; i < 23; i++ {
		clk.add(time.Hour)
		l.tick()
	}
	if logs.count(warn) != 1 {
		t.Fatalf("grace warnings in the first day: %d, want 1", logs.count(warn))
	}
	clk.add(time.Hour)
	l.tick()
	if logs.count(warn) != 2 {
		t.Fatal("no daily grace warning")
	}
	if ok, _ := l.ConfigChangesAllowed(); !ok || l.AckRequired() {
		t.Fatal("grace period must not block configuration")
	}
}

// Renewal clears the block, and /version information reflects the state.
// Verifies: DCM-SRS-060
func TestRenewalClearsBlock(t *testing.T) {
	l, _, _, st := setup(t, client.StateMissing)
	if info := l.Info(); info["licensed"] != false || info["license_ack_required"] != true {
		t.Fatalf("info = %v", info)
	}
	*st = client.StateValid
	l.tick()
	if ok, _ := l.ConfigChangesAllowed(); !ok || gauge(l.state) != 0 {
		t.Fatal("valid license still blocked")
	}
	if info := l.Info(); info["licensed"] != true || info["license_ack_required"] != false {
		t.Fatalf("info = %v", info)
	}
	var none *Licensing // source builds
	if ok, _ := none.ConfigChangesAllowed(); !ok || none.Info() != nil {
		t.Fatal("source builds must never be gated")
	}
}

// The licence state is exposed through the registry as
// libre_dcm_license_state (0 valid, 1 grace, 2 unlicensed),
// libre_dcm_license_days_left and libre_dcm_license_ack_required.
// Verifies: DCM-SRS-054
func TestLicenseStateMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	st := client.StateValid
	clk := &fakeClock{t: time.Date(2027, 3, 1, 9, 0, 0, 0, time.UTC)}
	l := newLicensing(func() client.Status { return client.Status{State: st, DaysLeft: 12} },
		filepath.Join(t.TempDir(), "license-ack.json"), clk.now,
		slog.New(slog.NewTextHandler(&logBuf{}, nil)), reg)
	read := func() map[string]float64 {
		mfs, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, mf := range mfs {
			out[mf.GetName()] = mf.GetMetric()[0].GetGauge().GetValue()
		}
		return out
	}
	for _, c := range []struct {
		state      client.State
		want, ackd float64
	}{
		{client.StateValid, 0, 0}, {client.StateGrace, 1, 0}, {client.StateExpired, 2, 1},
		{client.StateMissing, 2, 1}, {client.StateWrongMachine, 2, 1}, {client.StateValid, 0, 0},
	} {
		st = c.state
		l.tick()
		m := read()
		if m["libre_dcm_license_state"] != c.want || m["libre_dcm_license_ack_required"] != c.ackd ||
			m["libre_dcm_license_days_left"] != 12 {
			t.Fatalf("%s: metrics %v, want state %v ack_required %v days_left 12", c.state, m, c.want, c.ackd)
		}
	}
}

// The licence status for the control API shows the key as its last four
// characters only, carries the acknowledgement on file, and repeats that
// DICOM services never stop. A source build (nil) needs no licence. Without
// a licence manager the install, request and activation functions refuse.
// Verifies: DCM-SRS-089
func TestDetailForControlAPI(t *testing.T) {
	const key = "LK-SYNTH-ABCD-EFGH-7788" // gitleaks:allow (synthetic test value)
	l, clk, _, st := setup(t, client.StateExpired)
	exp := clk.now().Add(-40 * 24 * time.Hour)
	l.status = func() client.Status {
		return client.Status{State: *st, Message: "license expired", Expires: &exp,
			License: &license.License{Key: key, Customer: "Synthetic Clinic", Site: "Main", Term: "annual"}}
	}
	d := l.Detail()
	if !d.Enforced || d.State != "expired" || d.Licensed || !d.AckRequired || d.KeyLast4 != "7788" || d.Customer != "Synthetic Clinic" ||
		d.Term != "annual" || d.Ack != nil || !strings.Contains(d.Note, "never stops") {
		t.Fatalf("detail = %+v", d)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), key) || strings.Contains(string(raw), "ABCD") {
		t.Fatalf("the status holds the licence key: %s", raw)
	}
	for in, want := range map[string]string{key: "7788", "  LK-1234  ": "1234", "1234": "", "abc": "", "": ""} {
		if got := KeyLast4(in); got != want {
			t.Errorf("KeyLast4(%q) = %q, want %q", in, got, want)
		}
	}

	d, err := l.AcknowledgeBy("Dana Admin")
	if err != nil || d.AckRequired || d.Ack == nil || d.Ack.By != "Dana Admin" || !d.Ack.Valid || !d.Ack.ValidUntil.Equal(d.Ack.At.Add(AckValidity)) {
		t.Fatalf("after acknowledgement: %+v %v", d, err)
	}
	if gauge(l.ackRequired) != 0 {
		t.Fatal("the acknowledgement was not picked up at once")
	}
	if _, err := l.AcknowledgeBy("   "); err == nil {
		t.Fatal("acknowledgement without a name accepted")
	}
	clk.add(25 * time.Hour)
	if d := l.Detail(); !d.AckRequired || d.Ack == nil || d.Ack.Valid {
		t.Fatalf("a day later: %+v", d)
	}

	if _, err := l.RequestText("k", "s"); !errors.Is(err, ErrNoManager) {
		t.Fatalf("RequestText without a manager: %v", err)
	}
	if _, err := l.InstallText("x"); !errors.Is(err, ErrNoManager) {
		t.Fatalf("InstallText without a manager: %v", err)
	}
	if _, err := l.ActivateOnline(context.Background(), "k", "s"); !errors.Is(err, ErrNoManager) {
		t.Fatalf("ActivateOnline without a manager: %v", err)
	}

	var none *Licensing
	if d := none.Detail(); d.Enforced || d.State != "" || d.KeyLast4 != "" {
		t.Fatalf("source build detail = %+v", d)
	}
}
