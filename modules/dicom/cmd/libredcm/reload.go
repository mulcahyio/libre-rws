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
	gw   reloadTarget
	log  *slog.Logger
	gate func() (bool, string) // nil: always allowed; licensing blocks reloads while unacknowledged

	mu sync.Mutex // one reload at a time
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

// load checks the licence gate, reads the configuration and validates the
// live settings. It changes nothing.
func (r *reloader) load() (control.LiveSettings, error) {
	var next control.LiveSettings
	if r.gate != nil {
		if ok, why := r.gate(); !ok {
			r.log.Error("config reload blocked; keeping current settings", "reason", why)
			return next, fmt.Errorf("%w: %s", errReloadBlocked, why)
		}
	}
	cfg, err := loadConfig(r.path)
	if err != nil {
		r.log.Error("config reload failed; keeping current settings", "err", err)
		return next, err
	}
	rules, err := gateway.NormalizeAllowedAEs(cfg.DICOM.AllowedAEs)
	if err != nil {
		r.log.Error("config reload: invalid allowed_aes; keeping current settings", "err", err)
		return next, err
	}
	next.AllowedAEs, next.RemoteAEs = rules, cfg.DICOM.RemoteAEs
	return next, nil
}

// Preview reports what a reload would put in force, without changing
// anything (control.Reloader).
func (r *reloader) Preview() (control.LiveSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.load()
}

// Apply loads the configuration, lets check refuse it, and puts it in force
// (control.Reloader). On any error the running settings stay.
func (r *reloader) Apply(check func(control.LiveSettings) error) (control.LiveSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next, err := r.load()
	if err != nil {
		return next, err
	}
	if check != nil {
		if err := check(next); err != nil {
			r.log.Warn("config reload refused; keeping current settings", "err", err)
			return next, err
		}
	}
	if err := r.gw.SetAllowedAEs(next.AllowedAEs); err != nil {
		r.log.Error("config reload: invalid allowed_aes; keeping current settings", "err", err)
		return next, err
	}
	r.gw.SetRemoteAEs(next.RemoteAEs)
	r.log.Info("configuration reloaded", "remote_aes", len(next.RemoteAEs), "allowed_aes", len(next.AllowedAEs))
	return next, nil
}

// reload is the SIGHUP path.
func (r *reloader) reload() error {
	_, err := r.Apply(nil)
	return err
}
