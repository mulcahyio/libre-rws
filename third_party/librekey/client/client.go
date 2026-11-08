// Package client is the LibreKey key manager embedded in Libre products.
// It loads and verifies the machine's license file offline, works out
// whether the product may keep receiving new work (valid, in grace, expired),
// creates activation requests, and activates or renews online when a
// LibreKey server is reachable.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mulcahyio/librekey/fingerprint"
	"github.com/mulcahyio/librekey/license"
	"github.com/mulcahyio/librekey/request"
)

// State of a license on this machine.
type State string

const (
	StateMissing      State = "missing"       // no license file
	StateInvalid      State = "invalid"       // damaged, unknown key or bad signature
	StateWrongProduct State = "wrong_product" // license is for another product
	StateWrongMachine State = "wrong_machine" // locked to another machine
	StateNotCovered   State = "not_covered"   // build released after updates_until
	StateValid        State = "valid"
	StateGrace        State = "grace"   // expired, inside the grace period
	StateExpired      State = "expired" // expired and grace period over
)

// Status is the result of evaluating a license.
type Status struct {
	State     State
	License   *license.License
	Expires   *time.Time // license expiry (nil = perpetual)
	GraceEnds *time.Time // when receiving stops (nil = never)
	DaysLeft  int        // days until expiry (valid) or until grace ends (grace); -1 = perpetual
	Message   string
}

// CanReceive reports whether the product may accept new work (for LibreDCM:
// new studies). Access to existing data is never blocked by the license.
func (s Status) CanReceive() bool { return s.State == StateValid || s.State == StateGrace }

// Evaluate checks a verified license against this machine and time.
// buildDate is the release date of the running build (zero = skip the
// updates_until check).
func Evaluate(l *license.License, product, fp string, now, buildDate time.Time) Status {
	st := Status{License: l, Expires: l.Expires, DaysLeft: -1}
	switch {
	case l.Product != product:
		st.State, st.Message = StateWrongProduct, fmt.Sprintf("license is for %q, not %q", l.Product, product)
		return st
	case !fingerprint.Match(l.Fingerprint, fp):
		st.State, st.Message = StateWrongMachine, "license is locked to another machine; reactivate in the LibreKey portal"
		return st
	case l.UpdatesUntil != nil && !buildDate.IsZero() && buildDate.After(*l.UpdatesUntil):
		st.State = StateNotCovered
		st.Message = fmt.Sprintf("this version was released after the license's update period ended (%s); renew, or install a version released before then", l.UpdatesUntil.Format("2006-01-02"))
		return st
	}
	if l.Expires == nil {
		st.State, st.Message = StateValid, "perpetual license"
		return st
	}
	exp := *l.Expires
	graceEnd := exp.Add(time.Duration(l.GraceDays) * 24 * time.Hour)
	st.GraceEnds = &graceEnd
	switch {
	case now.Before(exp):
		st.State = StateValid
		st.DaysLeft = daysUntil(now, exp)
		st.Message = fmt.Sprintf("license valid until %s (%d days)", exp.Format("2006-01-02"), st.DaysLeft)
	case now.Before(graceEnd):
		st.State = StateGrace
		st.DaysLeft = daysUntil(now, graceEnd)
		st.Message = fmt.Sprintf("license expired on %s; receiving stops on %s (%d days) unless renewed", exp.Format("2006-01-02"), graceEnd.Format("2006-01-02"), st.DaysLeft)
	default:
		st.State = StateExpired
		st.DaysLeft = 0
		st.Message = fmt.Sprintf("license expired on %s and the %d-day grace period has ended: not receiving new work; existing data stays available", exp.Format("2006-01-02"), l.GraceDays)
	}
	return st
}

func daysUntil(now, t time.Time) int {
	d := t.Sub(now)
	return int((d + 24*time.Hour - time.Nanosecond) / (24 * time.Hour))
}

// Config for a Manager.
type Config struct {
	Product     string          // product code, e.g. "libredcm"
	AppVersion  string          // running version
	BuildDate   time.Time       // release date of this build (for updates_until)
	Keyring     license.Keyring // public keys embedded in the official build
	LicenseFile string          // where the license file is stored
	DataDir     string          // container: install id location ("" = use hardware ids)
	HostID      string          // container: host machine id ("" = LIBREKEY_HOST_ID env)
	Server      string          // LibreKey server URL ("" = offline only)
	HTTPClient  *http.Client
	Now         func() time.Time // for tests
}

// Manager keeps the current license status of the running product.
type Manager struct {
	cfg Config
	fp  string

	mu     sync.RWMutex
	status Status
}

// New creates a manager and computes this machine's fingerprint.
func New(cfg Config) (*Manager, error) {
	if cfg.Product == "" || len(cfg.Keyring) == 0 || cfg.LicenseFile == "" {
		return nil, errors.New("librekey: product, keyring and license file are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	fp, _, err := fingerprint.Collect(fingerprint.Options{DataDir: cfg.DataDir, HostID: cfg.HostID})
	if err != nil {
		return nil, err
	}
	m := &Manager{cfg: cfg, fp: fp}
	m.Reload()
	return m, nil
}

// Fingerprint of this machine.
func (m *Manager) Fingerprint() string { return m.fp }

// Status returns the last evaluated status (re-evaluated against the clock).
func (m *Manager) Status() Status {
	m.mu.RLock()
	st := m.status
	m.mu.RUnlock()
	if st.License != nil && (st.State == StateValid || st.State == StateGrace || st.State == StateExpired) {
		return Evaluate(st.License, m.cfg.Product, m.fp, m.cfg.Now(), m.cfg.BuildDate)
	}
	return st
}

// Reload reads and verifies the license file again.
func (m *Manager) Reload() Status {
	st := m.load()
	m.mu.Lock()
	m.status = st
	m.mu.Unlock()
	return st
}

func (m *Manager) load() Status {
	b, err := os.ReadFile(m.cfg.LicenseFile)
	if errors.Is(err, os.ErrNotExist) {
		return Status{State: StateMissing, Message: "no license installed; activate with a license key or a license file"}
	}
	if err != nil {
		return Status{State: StateInvalid, Message: err.Error()}
	}
	return m.check(string(b))
}

func (m *Manager) check(text string) Status {
	l, err := m.cfg.Keyring.Verify(text)
	if err != nil {
		return Status{State: StateInvalid, Message: err.Error()}
	}
	return Evaluate(l, m.cfg.Product, m.fp, m.cfg.Now(), m.cfg.BuildDate)
}

// Install verifies a license file and, when it is usable on this machine,
// saves it as the active license.
func (m *Manager) Install(text string) (Status, error) {
	st := m.check(text)
	switch st.State {
	case StateValid, StateGrace:
	default:
		return st, fmt.Errorf("license not installed: %s", st.Message)
	}
	if err := writeAtomic(m.cfg.LicenseFile, []byte(text)); err != nil {
		return st, err
	}
	m.mu.Lock()
	m.status = st
	m.mu.Unlock()
	return st, nil
}

// Request returns an activation request for this machine.
func (m *Manager) Request(key, site string) request.Request {
	r := request.New(m.cfg.Product, m.cfg.AppVersion, key, m.fp)
	r.Site = site
	return r
}

// Activate sends an activation request with a license key to the server and
// installs the license file it returns.
func (m *Manager) Activate(ctx context.Context, key, site string) (Status, error) {
	r := m.Request(license.NormalizeKey(key), site)
	var out struct {
		License string `json:"license"`
	}
	if err := m.post(ctx, "/api/v1/activate", map[string]any{"request": r}, &out); err != nil {
		return Status{}, err
	}
	return m.Install(out.License)
}

// Renew asks the server for the newest license file for this machine (after
// an annual renewal) and installs it when it is newer.
func (m *Manager) Renew(ctx context.Context) (Status, error) {
	cur := m.Status()
	if cur.License == nil {
		return cur, errors.New("no license to renew; activate first")
	}
	var out struct {
		License string `json:"license"`
	}
	if err := m.post(ctx, "/api/v1/renew", map[string]any{"license_id": cur.License.ID, "key": cur.License.Key, "fingerprint": m.fp}, &out); err != nil {
		return cur, err
	}
	next := m.check(out.License)
	if next.License == nil || !next.License.Issued.After(cur.License.Issued) {
		return cur, nil
	}
	return m.Install(out.License)
}

// Watch re-evaluates the license periodically, renews online when the
// license is within renewBefore of expiry (or in grace), and calls onChange
// when the state changes. It returns when ctx ends.
func (m *Manager) Watch(ctx context.Context, every, renewBefore time.Duration, onChange func(Status)) {
	last := m.Status().State
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st := m.Status()
		if m.cfg.Server != "" && st.License != nil && st.Expires != nil &&
			(st.State == StateGrace || st.State == StateExpired || st.Expires.Sub(m.cfg.Now()) < renewBefore) {
			if s, err := m.Renew(ctx); err == nil {
				st = s
			}
		}
		if st.State != last && onChange != nil {
			onChange(st)
		}
		last = st.State
	}
}

func (m *Manager) post(ctx context.Context, path string, body, out any) error {
	server := m.cfg.Server
	if server == "" {
		return errors.New("no LibreKey server configured; use an offline license file")
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(server, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("LibreKey server not reachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("LibreKey server: %s", resp.Status)
	}
	return json.Unmarshal(data, out)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
