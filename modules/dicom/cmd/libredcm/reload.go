package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

// errReloadBlocked: an unlicensed node needs an administrator acknowledgement first.
var errReloadBlocked = errors.New("config reload blocked until the license notice is acknowledged")

// reloadTarget is the part of the gateway that can change without a restart.
type reloadTarget interface {
	SetRemoteAEs(map[string]gateway.RemoteAE)
	SetAllowedAEs([]string) error
}

// reloader re-reads the configuration on SIGHUP and applies the settings that
// are safe to change live: C-MOVE destinations (remote_aes) and the calling-AE
// allow-list (allowed_aes). Everything else still needs a restart. A config
// that fails to load or validate leaves the running settings untouched.
type reloader struct {
	path string
	gw   reloadTarget
	log  *slog.Logger
	gate func() (bool, string) // nil: always allowed; licensing blocks reloads while unacknowledged
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

func (r *reloader) reload() error {
	if r.gate != nil {
		if ok, why := r.gate(); !ok {
			r.log.Error("config reload blocked; keeping current settings", "reason", why)
			return errReloadBlocked
		}
	}
	cfg, err := loadConfig(r.path)
	if err != nil {
		r.log.Error("config reload failed; keeping current settings", "err", err)
		return err
	}
	if err := r.gw.SetAllowedAEs(cfg.DICOM.AllowedAEs); err != nil {
		r.log.Error("config reload: invalid allowed_aes; keeping current settings", "err", err)
		return err
	}
	r.gw.SetRemoteAEs(cfg.DICOM.RemoteAEs)
	r.log.Info("configuration reloaded", "remote_aes", len(cfg.DICOM.RemoteAEs), "allowed_aes", len(cfg.DICOM.AllowedAEs))
	return nil
}
