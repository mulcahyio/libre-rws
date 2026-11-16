package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/control"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

// errReloadBlocked: an unlicensed node needs an administrator acknowledgement first.
var errReloadBlocked = control.ErrReloadBlocked

// reloadTarget is the part of the gateway that can change without a restart.
type reloadTarget interface {
	SetRemoteAEs(map[string]gateway.RemoteAE)
	SetAllowedAEs([]string) error
}

// reloader re-reads the configuration and applies the settings that are safe
// to change live: C-MOVE destinations (remote_aes) and the calling-AE
// allow-list (allowed_aes). Everything else still needs a restart. A config
// that fails to load or validate leaves the running settings untouched.
//
// There is one reload path. SIGHUP and the control API (POST
// /control/v1/reload, DCM-SRS-088) both go through Apply, one at a time.
type reloader struct {
	path string
	// overlayPath is the overlay file the control API writes changes to
	// (DCM-SRS-093); "" means this node has none. It is fixed at start.
	overlayPath string
	gw          reloadTarget
	log         *slog.Logger
	gate        func() (bool, string) // nil: always allowed; licensing blocks reloads while unacknowledged

	mu sync.Mutex // one reload or configuration change at a time

	useMu sync.Mutex
	use   overlayUse // which live settings the overlay supplied at the last load put in force

	// writeFn replaces writeFileAtomic for the overlay file (tests).
	writeFn func(path string, data []byte) error
}

// writeOverlay writes the overlay file.
func (r *reloader) writeOverlay(path string, data []byte) error {
	if r.writeFn != nil {
		return r.writeFn(path, data)
	}
	return writeFileAtomic(path, data)
}

func (r *reloader) Name() string { return "config-reload" }

func (r *reloader) Run(ctx context.Context) error {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
			_ = r.reload()
		}
	}
}

// load checks the licence gate, reads the configuration (config.yaml, then
// the overlay file, then the environment) and validates the live settings.
// candidate, when not nil, stands in for the content of the overlay file:
// a change is validated this way before it is written. load changes nothing.
func (r *reloader) load(candidate *overlayDoc) (control.LiveSettings, overlayUse, error) {
	var next control.LiveSettings
	if r.gate != nil {
		if ok, why := r.gate(); !ok {
			r.log.Error("config reload blocked; keeping current settings", "reason", why)
			return next, overlayUse{}, fmt.Errorf("%w: %s", errReloadBlocked, why)
		}
	}
	cfg, use, err := loadConfigOverlay(r.path, &r.overlayPath, candidate)
	if err != nil {
		r.log.Error("config reload failed; keeping current settings", "err", err)
		return next, use, err
	}
	rules, err := gateway.NormalizeAllowedAEs(cfg.DICOM.AllowedAEs)
	if err != nil {
		r.log.Error("config reload: invalid allowed_aes; keeping current settings", "err", err)
		return next, use, err
	}
	next.AllowedAEs, next.RemoteAEs = rules, cfg.DICOM.RemoteAEs
	return next, use, nil
}

// put puts loaded settings in force and remembers what the overlay supplied.
func (r *reloader) put(next control.LiveSettings, use overlayUse) error {
	if err := r.gw.SetAllowedAEs(next.AllowedAEs); err != nil {
		r.log.Error("config reload: invalid allowed_aes; keeping current settings", "err", err)
		return err
	}
	r.gw.SetRemoteAEs(next.RemoteAEs)
	r.useMu.Lock()
	r.use = use
	r.useMu.Unlock()
	return nil
}

// Preview reports what a reload would put in force, without changing
// anything (control.Reloader).
func (r *reloader) Preview() (control.LiveSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next, _, err := r.load(nil)
	return next, err
}

// Apply loads the configuration, lets check refuse it, and puts it in force
// (control.Reloader). On any error the running settings stay.
func (r *reloader) Apply(check func(control.LiveSettings) error) (control.LiveSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next, use, err := r.load(nil)
	if err != nil {
		return next, err
	}
	if check != nil {
		if err := check(next); err != nil {
			r.log.Warn("config reload refused; keeping current settings", "err", err)
			return next, err
		}
	}
	if err := r.put(next, use); err != nil {
		return next, err
	}
	r.log.Info("configuration reloaded", "remote_aes", len(next.RemoteAEs), "allowed_aes", len(next.AllowedAEs))
	return next, nil
}

// reload is the SIGHUP path.
func (r *reloader) reload() error {
	_, err := r.Apply(nil)
	return err
}
