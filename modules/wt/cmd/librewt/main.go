// Command LibreWT is Libre Watch Tower, the management console for LibreDCM
// (v0.1: accounts, one LibreDCM connection, Study Exceptions queue, audit
// log). Configuration comes from a YAML file and LIBRE_WT_* environment
// variables (environment wins).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/web"
	"github.com/mulcahyio/libre-rws/platform/config"
)

var version = "dev"

// Config is Libre WT's configuration.
type Config struct {
	Listen      string        `yaml:"listen" env:"LIBRE_WT_LISTEN"`
	DataDir     string        `yaml:"data_dir" env:"LIBRE_WT_DATA_DIR"`
	TLSCertFile string        `yaml:"tls_cert_file" env:"LIBRE_WT_TLS_CERT"`
	TLSKeyFile  string        `yaml:"tls_key_file" env:"LIBRE_WT_TLS_KEY"`
	IdleTimeout time.Duration `yaml:"session_idle_timeout" env:"LIBRE_WT_SESSION_IDLE_TIMEOUT"`
	MaxSession  time.Duration `yaml:"session_max_age" env:"LIBRE_WT_SESSION_MAX_AGE"`
	LogLevel    string        `yaml:"log_level" env:"LIBRE_WT_LOG_LEVEL"`
	DCM         struct {
		URL       string `yaml:"url" env:"LIBRE_WT_DCM_URL"`
		Token     string `yaml:"token" env:"LIBRE_WT_DCM_TOKEN"`
		TokenFile string `yaml:"token_file" env:"LIBRE_WT_DCM_TOKEN_FILE"`
		CAFile    string `yaml:"ca_file" env:"LIBRE_WT_DCM_CA_FILE"`
	} `yaml:"dcm"`
}

func defaults() Config {
	var c Config
	c.Listen = "127.0.0.1:8090"
	c.DataDir = "./data-wt"
	c.IdleTimeout = 30 * time.Minute
	c.MaxSession = 12 * time.Hour
	c.LogLevel = "info"
	c.DCM.URL = "http://127.0.0.1:8081"
	return c
}

func main() {
	cfgPath := flag.String("config", "", "YAML configuration file (optional; LIBRE_WT_* environment variables override it)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("LibreWT", version)
		return
	}
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "LibreWT:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg := defaults()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return err
	}
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(cfg.LogLevel))
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})).With("module", "librewt")

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	acct, err := accounts.Open(ctx, filepath.Join(cfg.DataDir, "librewt.db"))
	if err != nil {
		return err
	}
	defer acct.Close()
	dcm, err := dcmclient.New(dcmclient.Config{URL: cfg.DCM.URL, Token: cfg.DCM.Token, TokenFile: cfg.DCM.TokenFile, CAFile: cfg.DCM.CAFile})
	if err != nil {
		return err
	}
	secure := cfg.TLSCertFile != "" && cfg.TLSKeyFile != ""
	srv, err := web.New(web.Config{Version: version, Secure: secure, IdleTimeout: cfg.IdleTimeout, MaxSession: cfg.MaxSession}, acct, dcm, log)
	if err != nil {
		return err
	}
	if has, _ := acct.HasUsers(ctx); !has {
		log.Warn("no accounts yet: open Libre WT in a browser to create the first administrator", "listen", cfg.Listen)
	}
	if !secure {
		log.Warn("serving plain HTTP: set tls_cert_file/tls_key_file unless Libre WT is only reachable from this machine", "listen", cfg.Listen)
	}
	hs := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 3 * time.Minute, IdleTimeout: 2 * time.Minute,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	errc := make(chan error, 1)
	go func() {
		log.Info("LibreWT starting", "version", version, "listen", cfg.Listen, "https", secure, "dcm", dcm.URL())
		if secure {
			errc <- hs.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			errc <- hs.ListenAndServe()
		}
	}()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}
