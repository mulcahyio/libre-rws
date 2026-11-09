// Package runtime is the common chassis every RWS module runs on. It gives each
// module the same operational surface, whether it runs alone or as part of the
// full system:
//
//	GET /healthz   liveness  - the process is up and not wedged
//	GET /readyz    readiness - every registered dependency check passes
//	GET /metrics   Prometheus metrics
//	GET /version   module name, version, node id
//
// It also owns structured logging and graceful shutdown on SIGINT/SIGTERM, so
// orchestrators (Docker, systemd, Kubernetes) can stop and replace instances
// safely during upgrades.
package runtime

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

// AdminConfig controls the shared operational endpoints and logging.
type AdminConfig struct {
	Addr            string        `yaml:"addr" env:"{P}ADMIN_ADDR,RWS_ADMIN_ADDR"`
	LogLevel        string        `yaml:"log_level" env:"{P}LOG_LEVEL,RWS_LOG_LEVEL"`    // debug|info|warn|error
	LogFormat       string        `yaml:"log_format" env:"{P}LOG_FORMAT,RWS_LOG_FORMAT"` // json|text
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" env:"{P}SHUTDOWN_TIMEOUT,RWS_SHUTDOWN_TIMEOUT"`
	NodeID          string        `yaml:"node_id" env:"{P}NODE_ID,RWS_NODE_ID"` // defaults to hostname
	// Token, when set (directly or via TokenFile), is required as
	// "Authorization: Bearer <token>" on /metrics, /version and any other
	// admin endpoint. /healthz and /readyz stay open for container and load
	// balancer probes, but /readyz omits check details without the token.
	Token     string `yaml:"token" env:"{P}ADMIN_TOKEN"`
	TokenFile string `yaml:"token_file" env:"{P}ADMIN_TOKEN_FILE"`
	// TLSCertFile/TLSKeyFile serve the admin endpoint over HTTPS.
	TLSCertFile string `yaml:"tls_cert_file" env:"{P}ADMIN_TLS_CERT"`
	TLSKeyFile  string `yaml:"tls_key_file" env:"{P}ADMIN_TLS_KEY"`
}

// DefaultAdmin returns sensible defaults for AdminConfig.
func DefaultAdmin() AdminConfig {
	return AdminConfig{Addr: ":8081", LogLevel: "info", LogFormat: "json", ShutdownTimeout: 30 * time.Second}
}

// Component is a long-running part of a module (a DICOM listener, a consumer,
// an HTTP API). Run must block until ctx is cancelled and then return promptly.
type Component interface {
	Name() string
	Run(ctx context.Context) error
}

// CheckFunc reports whether a dependency is usable. Return nil when healthy.
type CheckFunc func(ctx context.Context) error

// Runtime is the per-process chassis.
type Runtime struct {
	Module   string
	Version  string
	NodeID   string
	Logger   *slog.Logger
	Registry *prometheus.Registry

	cfg     AdminConfig
	mu      sync.RWMutex
	checks  map[string]CheckFunc
	info    []func() map[string]any
	started time.Time
}

// AddVersionInfo adds module-specific fields (e.g. license state) to the
// /version response. Call before Run.
func (r *Runtime) AddVersionInfo(f func() map[string]any) {
	r.mu.Lock()
	r.info = append(r.info, f)
	r.mu.Unlock()
}

// New builds a Runtime and its logger.
func New(module, version string, cfg AdminConfig) *Runtime {
	if cfg.NodeID == "" {
		cfg.NodeID, _ = os.Hostname()
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name:        "libre_module_info",
		Help:        "Static module information.",
		ConstLabels: prometheus.Labels{"module": module, "version": version, "node": cfg.NodeID},
	}, func() float64 { return 1 }))

	return &Runtime{
		Module:   module,
		Version:  version,
		NodeID:   cfg.NodeID,
		Logger:   newLogger(cfg).With("module", module, "node", cfg.NodeID),
		Registry: reg,
		cfg:      cfg,
		checks:   map[string]CheckFunc{},
		started:  time.Now(),
	}
}

func newLogger(cfg AdminConfig) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

// AddReadinessCheck registers a dependency check used by /readyz.
func (r *Runtime) AddReadinessCheck(name string, fn CheckFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = fn
}

// Run serves the admin endpoints and all components until a signal arrives or
// a component fails, then shuts everything down within ShutdownTimeout.
func (r *Runtime) Run(parent context.Context, components ...Component) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: r.cfg.Addr, Handler: r.adminMux(), ReadHeaderTimeout: 5 * time.Second}
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		var err error
		if r.cfg.TLSCertFile != "" {
			r.Logger.Info("admin endpoint listening (https)", "addr", r.cfg.Addr)
			srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			err = srv.ListenAndServeTLS(r.cfg.TLSCertFile, r.cfg.TLSKeyFile)
		} else {
			r.Logger.Info("admin endpoint listening", "addr", r.cfg.Addr)
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("admin http: %w", err)
		}
		return nil
	})
	for _, c := range components {
		c := c
		g.Go(func() error {
			r.Logger.Info("component starting", "component", c.Name())
			err := c.Run(gctx)
			r.Logger.Info("component stopped", "component", c.Name(), "err", err)
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("%s: %w", c.Name(), err)
			}
			return nil
		})
	}
	g.Go(func() error {
		<-gctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), r.cfg.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(sctx)
	})

	err := g.Wait()
	if err != nil {
		r.Logger.Error("module exited with error", "err", err)
	} else {
		r.Logger.Info("module stopped cleanly")
	}
	return err
}

// adminToken resolves the configured token. ok is false when a token file is
// configured but unreadable or empty; the caller then refuses protected
// requests instead of serving them unauthenticated.
func (r *Runtime) adminToken() (token string, ok bool) {
	if r.cfg.TokenFile == "" {
		return strings.TrimSpace(r.cfg.Token), true
	}
	b, err := os.ReadFile(r.cfg.TokenFile)
	if err != nil || strings.TrimSpace(string(b)) == "" {
		r.Logger.Error("admin token file unreadable or empty; protected admin endpoints will refuse requests",
			"file", r.cfg.TokenFile, "err", err)
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

func (r *Runtime) adminMux() *http.ServeMux {
	token, tokenOK := r.adminToken()
	authorized := func(req *http.Request) bool {
		if !tokenOK {
			return false
		}
		if token == "" {
			return true
		}
		got, found := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
		return found && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
	}
	protect := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !authorized(req) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="libre-admin"`)
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			h.ServeHTTP(w, req)
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		results, ok := r.runChecks(ctx)
		code := http.StatusOK
		if !ok {
			code = http.StatusServiceUnavailable
		}
		body := map[string]any{"ready": ok}
		if authorized(req) { // check errors can reveal hosts and paths
			body["checks"] = results
		}
		writeJSON(w, code, body)
	})
	mux.Handle("GET /version", protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := map[string]any{
			"module": r.Module, "version": r.Version, "node": r.NodeID,
			"uptime_seconds": int(time.Since(r.started).Seconds()),
		}
		r.mu.RLock()
		for _, f := range r.info {
			for k, v := range f() {
				body[k] = v
			}
		}
		r.mu.RUnlock()
		writeJSON(w, http.StatusOK, body)
	})))
	mux.Handle("GET /metrics", protect(promhttp.HandlerFor(r.Registry, promhttp.HandlerOpts{})))
	return mux
}

func (r *Runtime) runChecks(ctx context.Context) (map[string]string, bool) {
	r.mu.RLock()
	names := make([]string, 0, len(r.checks))
	for n := range r.checks {
		names = append(names, n)
	}
	r.mu.RUnlock()
	sort.Strings(names)

	out := make(map[string]string, len(names))
	ok := true
	for _, n := range names {
		r.mu.RLock()
		fn := r.checks[n]
		r.mu.RUnlock()
		if err := fn(ctx); err != nil {
			out[n] = err.Error()
			ok = false
		} else {
			out[n] = "ok"
		}
	}
	return out, ok
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
