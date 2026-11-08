// Package licensing connects LibreDCM to LibreKey. Official builds need a
// valid, machine-locked license: when it has expired and its grace period is
// over, LibreDCM stops receiving new studies (C-STORE is refused with
// "out of resources", so modalities keep the images and retry) while
// C-ECHO, C-FIND, C-MOVE and C-GET keep serving the existing archive.
package licensing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mulcahyio/librekey/client"
	"github.com/mulcahyio/librekey/license"
	"github.com/prometheus/client_golang/prometheus"
)

// Product code of LibreDCM in LibreKey.
const Product = "libredcm"

// Config for licensing.
type Config struct {
	File    string `yaml:"file" env:"LIBRE_DCM_LICENSE_FILE"`     // license file path
	Server  string `yaml:"server" env:"LIBRE_DCM_LICENSE_SERVER"` // LibreKey server for activation / renewal
	DataDir string `yaml:"data_dir" env:"LIBRE_DCM_LICENSE_DATA"` // install id location (containers)
}

// Enforced reports whether this build requires a license.
func Enforced() bool { return len(officialKeys) > 0 }

// Licensing is the running license check.
type Licensing struct {
	mgr *client.Manager
	log *slog.Logger

	state, daysLeft prometheus.Gauge
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
	l := &Licensing{mgr: mgr, log: log.With("component", "license"),
		state: prometheus.NewGauge(prometheus.GaugeOpts{Name: "libre_dcm_license_state",
			Help: "License: 0 valid, 1 grace period, 2 not receiving (expired, missing, wrong machine)"}),
		daysLeft: prometheus.NewGauge(prometheus.GaugeOpts{Name: "libre_dcm_license_days_left",
			Help: "Days until the license expires (valid) or until receiving stops (grace); -1 perpetual"}),
	}
	if reg != nil {
		reg.MustRegister(l.state, l.daysLeft)
	}
	l.report(mgr.Status())
	return l, nil
}

// Manager exposes the key manager (activation commands).
func (l *Licensing) Manager() *client.Manager { return l.mgr }

// CanReceive is the C-STORE gate.
func (l *Licensing) CanReceive() (bool, string) {
	if l == nil {
		return true, ""
	}
	st := l.mgr.Status()
	return st.CanReceive(), st.Message
}

// Check is a readiness check that never fails: an expired license still
// serves existing studies, so the node stays in the load balancer.
func (l *Licensing) Check(context.Context) error { return nil }

func (l *Licensing) report(st client.Status) {
	switch st.State {
	case client.StateValid:
		l.state.Set(0)
		l.log.Info("license valid", "customer", customer(st), "message", st.Message)
	case client.StateGrace:
		l.state.Set(1)
		l.log.Warn("license in grace period", "customer", customer(st), "message", st.Message)
	default:
		l.state.Set(2)
		l.log.Error("not receiving new studies: no valid license", "state", st.State, "message", st.Message,
			"fingerprint", l.mgr.Fingerprint())
	}
	l.daysLeft.Set(float64(st.DaysLeft))
}

func customer(st client.Status) string {
	if st.License == nil {
		return ""
	}
	return st.License.Customer
}

// Name implements runtime.Component.
func (l *Licensing) Name() string { return "license" }

// Run re-checks the license hourly, renews online in the last 30 days and
// during grace, and logs every change of state.
func (l *Licensing) Run(ctx context.Context) error {
	l.mgr.Watch(ctx, time.Hour, 30*24*time.Hour, func(st client.Status) { l.report(st) })
	return nil
}
