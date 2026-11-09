// Package licensing connects LibreDCM to LibreKey. Official builds need a
// valid, machine-locked license, but licensing NEVER stops DICOM services
// (DCM-SRS-058, risk control for HAZ-010): images keep flowing in every
// license state. An unlicensed node nags its administrators instead:
//
//   - grace period (expired, up to 30 days): a daily warning;
//   - unlicensed (grace over, missing, wrong machine, …): an hourly error and
//     blocked configuration reloads until an administrator acknowledges with
//     `LibreDCM -license-ack NAME`. An acknowledgement lasts 24 hours, then the
//     reminders start again.
//
// Upgrades and official support stay tied to the license (state not_covered).
package licensing

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/mulcahyio/librekey/client"
	"github.com/mulcahyio/librekey/license"
	"github.com/prometheus/client_golang/prometheus"
)

// Product code of LibreDCM in LibreKey.
const Product = "libredcm"

const (
	// NagInterval is how often an unacknowledged, unlicensed node logs an error.
	NagInterval = time.Hour
	// GraceWarnInterval is how often a node in its grace period logs a warning.
	GraceWarnInterval = 24 * time.Hour
	// AckValidity is how long an administrator's acknowledgement silences the reminders.
	AckValidity = 24 * time.Hour
	// tickInterval is how often the running node re-evaluates (and picks up new acknowledgements).
	tickInterval = time.Minute
)

// Config for licensing.
type Config struct {
	File    string `yaml:"file" env:"LIBRE_DCM_LICENSE_FILE"`     // license file path
	Server  string `yaml:"server" env:"LIBRE_DCM_LICENSE_SERVER"` // LibreKey server for activation / renewal
	DataDir string `yaml:"data_dir" env:"LIBRE_DCM_LICENSE_DATA"` // install id location (containers)
}

// AckFile is where acknowledgements are stored: next to the license file.
func (c Config) AckFile() string { return filepath.Join(filepath.Dir(c.File), "license-ack.json") }

// Enforced reports whether this build requires a license.
func Enforced() bool { return len(officialKeys) > 0 }

// Licensed reports whether a status counts as licensed (valid or in grace).
func Licensed(st client.Status) bool {
	return st.State == client.StateValid || st.State == client.StateGrace
}

// Licensing is the running license check.
type Licensing struct {
	mgr     *client.Manager
	status  func() client.Status
	ackFile string
	now     func() time.Time
	log     *slog.Logger

	state, daysLeft, ackRequired prometheus.Gauge

	mu            sync.Mutex
	started       bool
	lastState     client.State
	lastNag       time.Time // last hourly error (unlicensed, unacknowledged)
	lastGraceWarn time.Time // last daily warning (grace)
	lastAckAt     time.Time // newest acknowledgement already reported
}

// New loads the license. In source builds it returns nil (no checks).
func New(cfg Config, version string, buildDate time.Time, log *slog.Logger, reg prometheus.Registerer) (*Licensing, error) {
	if !Enforced() {
		return nil, nil
	}
	keys, err := license.ParsePublicKeys(officialKeys)
	if err != nil {
		return nil, fmt.Errorf("embedded LibreKey key: %w", err)
	}
	mgr, err := client.New(client.Config{Product: Product, AppVersion: version, BuildDate: buildDate,
		Keyring: license.NewKeyring(keys...), LicenseFile: cfg.File, DataDir: cfg.DataDir, Server: cfg.Server})
	if err != nil {
		return nil, err
	}
	l := newLicensing(mgr.Status, cfg.AckFile(), time.Now, log, reg)
	l.mgr = mgr
	return l, nil
}

// newLicensing builds the reminder logic around a status source (tests use a fake).
func newLicensing(status func() client.Status, ackFile string, now func() time.Time, log *slog.Logger, reg prometheus.Registerer) *Licensing {
	l := &Licensing{status: status, ackFile: ackFile, now: now, log: log.With("component", "license"),
		state: prometheus.NewGauge(prometheus.GaugeOpts{Name: "libre_dcm_license_state",
			Help: "License: 0 valid, 1 grace period, 2 unlicensed (DICOM services keep running)"}),
		daysLeft: prometheus.NewGauge(prometheus.GaugeOpts{Name: "libre_dcm_license_days_left",
			Help: "Days until the license expires (valid) or until the grace period ends (grace); -1 perpetual"}),
		ackRequired: prometheus.NewGauge(prometheus.GaugeOpts{Name: "libre_dcm_license_ack_required",
			Help: "1 while the node is unlicensed and no administrator has acknowledged it in the last 24 hours"}),
	}
	if reg != nil {
		reg.MustRegister(l.state, l.daysLeft, l.ackRequired)
	}
	l.tick()
	return l
}

// Manager exposes the key manager (activation commands).
func (l *Licensing) Manager() *client.Manager { return l.mgr }

// Acknowledge records that an administrator has seen the license notice.
// The running node reports it as an audit record within a minute.
func (l *Licensing) Acknowledge(by string) (Ack, error) {
	a := Ack{By: by, At: l.now().UTC(), State: string(l.status().State)}
	return a, writeAck(l.ackFile, a)
}

// AckRequired reports whether the node is unlicensed with no acknowledgement
// in the last 24 hours.
func (l *Licensing) AckRequired() bool {
	if l == nil {
		return false
	}
	return l.ackRequiredAt(l.status(), l.now())
}

func (l *Licensing) ackRequiredAt(st client.Status, now time.Time) bool {
	if Licensed(st) {
		return false
	}
	a, err := readAck(l.ackFile)
	return err != nil || !a.ValidAt(now)
}

// ConfigChangesAllowed is the configuration-reload gate: reloads are blocked
// while an acknowledgement is required. DICOM traffic is never affected.
func (l *Licensing) ConfigChangesAllowed() (bool, string) {
	if l.AckRequired() {
		return false, "LibreDCM is unlicensed; an administrator must acknowledge first: " +
			"LibreDCM -license-ack NAME (or LibreDCM.sh / LibreDCM.ps1 license-ack NAME)"
	}
	return true, ""
}

// Info is added to the admin /version response.
func (l *Licensing) Info() map[string]any {
	if l == nil {
		return nil
	}
	st := l.status()
	return map[string]any{"license_state": string(st.State), "licensed": Licensed(st),
		"license_ack_required": l.AckRequired(), "license_message": st.Message}
}

// Check is a readiness check that never fails: licensing never takes a node
// out of service.
func (l *Licensing) Check(context.Context) error { return nil }

// tick re-evaluates the license and emits the reminders that are due.
func (l *Licensing) tick() {
	st, now := l.status(), l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	changed := !l.started || st.State != l.lastState
	l.started, l.lastState = true, st.State
	l.daysLeft.Set(float64(st.DaysLeft))

	if a, err := readAck(l.ackFile); err == nil && a.At.After(l.lastAckAt) {
		l.lastAckAt = a.At
		l.log.Warn("license notice acknowledged", "audit", true, "event", "license_acknowledged",
			"by", a.By, "at", a.At, "license_state", a.State, "silenced_until", a.At.Add(AckValidity))
	}

	switch st.State {
	case client.StateValid:
		l.state.Set(0)
		l.ackRequired.Set(0)
		if changed {
			l.log.Info("license valid", "customer", customer(st), "message", st.Message)
		}
	case client.StateGrace:
		l.state.Set(1)
		l.ackRequired.Set(0)
		if changed || now.Sub(l.lastGraceWarn) >= GraceWarnInterval {
			l.lastGraceWarn = now
			l.log.Warn("license expired: grace period", "customer", customer(st), "days_left", st.DaysLeft,
				"message", st.Message)
		}
	default:
		l.state.Set(2)
		if !l.ackRequiredAt(st, now) {
			l.ackRequired.Set(0)
			return
		}
		l.ackRequired.Set(1)
		if changed || now.Sub(l.lastNag) >= NagInterval {
			l.lastNag = now
			l.log.Error("LibreDCM is unlicensed. DICOM services continue; configuration reloads are blocked "+
				"until an administrator acknowledges with: LibreDCM -license-ack NAME",
				"state", st.State, "message", st.Message, "fingerprint", l.fingerprint())
		}
	}
}

func (l *Licensing) fingerprint() string {
	if l.mgr == nil {
		return ""
	}
	return l.mgr.Fingerprint()
}

func customer(st client.Status) string {
	if st.License == nil {
		return ""
	}
	return st.License.Customer
}

// Name implements runtime.Component.
func (l *Licensing) Name() string { return "license" }

// Run re-checks the license hourly (renewing online in the last 30 days and
// during grace) and evaluates reminders every minute.
func (l *Licensing) Run(ctx context.Context) error {
	if l.mgr != nil {
		go l.mgr.Watch(ctx, time.Hour, 30*24*time.Hour, func(client.Status) { l.tick() })
	}
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			l.tick()
		}
	}
}
