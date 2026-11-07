package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"rws/modules/dicom/internal/gateway"
)

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
