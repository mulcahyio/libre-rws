// Package gateway is the DIMSE front door of the RWS DICOM module: it accepts
// associations and serves Verification (C-ECHO), Storage (C-STORE) and
// Query/Retrieve (C-FIND, C-MOVE, C-GET).
//
// The gateway is stateless. Everything durable lives behind three interfaces
// (blob.Store, index.Index, events.Publisher), which is what allows any number
// of gateway nodes to sit behind a TCP load balancer and be replaced at will.
package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/prometheus/client_golang/prometheus"

	"rws/modules/dicom/internal/blob"
	"rws/modules/dicom/internal/dcm"
	"rws/modules/dicom/internal/events"
	"rws/modules/dicom/internal/index"
)

// RemoteAE is a peer this node may send to (C-MOVE destinations).
type RemoteAE struct {
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	AETitle string `yaml:"ae_title"` // called AE title; defaults to the map key
}

// Config is the DIMSE listener configuration.
type Config struct {
	AETitle               string              `yaml:"ae_title" env:"RWS_DICOM_AE_TITLE"`
	Listen                string              `yaml:"listen" env:"RWS_DICOM_LISTEN"`
	AllowAnyCalledAETitle bool                `yaml:"allow_any_called_ae_title" env:"RWS_DICOM_ALLOW_ANY_CALLED_AE"`
	AcceptAnySOPClass     bool                `yaml:"accept_any_sop_class" env:"RWS_DICOM_ACCEPT_ANY_SOP_CLASS"`
	MaxAssociations       int                 `yaml:"max_associations" env:"RWS_DICOM_MAX_ASSOCIATIONS"`
	MaxPDULength          uint32              `yaml:"max_pdu_length" env:"RWS_DICOM_MAX_PDU"`
	HandshakeTimeout      time.Duration       `yaml:"handshake_timeout" env:"RWS_DICOM_HANDSHAKE_TIMEOUT"`
	IdleTimeout           time.Duration       `yaml:"idle_timeout" env:"RWS_DICOM_IDLE_TIMEOUT"`
	FindLimit             int                 `yaml:"find_limit" env:"RWS_DICOM_FIND_LIMIT"`
	GetMaxBytes           int64               `yaml:"get_max_bytes" env:"RWS_DICOM_GET_MAX_BYTES"`
	TLSCertFile           string              `yaml:"tls_cert_file" env:"RWS_DICOM_TLS_CERT"`
	TLSKeyFile            string              `yaml:"tls_key_file" env:"RWS_DICOM_TLS_KEY"`
	RemoteAEs             map[string]RemoteAE `yaml:"remote_aes"`
}

// DefaultConfig returns production-sane defaults.
func DefaultConfig() Config {
	return Config{
		AETitle:          "RWS_DICOM",
		Listen:           ":11112",
		MaxAssociations:  64,
		MaxPDULength:     131072,
		HandshakeTimeout: 30 * time.Second,
		IdleTimeout:      5 * time.Minute,
		FindLimit:        1000,
		GetMaxBytes:      1 << 30, // C-GET buffers the response set in memory; see retrieve.go
		RemoteAEs:        map[string]RemoteAE{},
	}
}

// Gateway serves DIMSE.
type Gateway struct {
	cfg    Config
	node   string
	log    *slog.Logger
	blobs  blob.Store
	idx    index.Index
	events events.Publisher
	m      *metrics

	mu        sync.RWMutex
	remoteAEs map[string]RemoteAE

	outbox interface{ Nudge() } // nil: events are published directly (no outbox)
}

// UseOutbox makes C-STORE write its event to the index outbox in the same
// transaction as the instance; relay publishes it. Call before Run.
func (g *Gateway) UseOutbox(relay interface{ Nudge() }) { g.outbox = relay }

// CountEventFailure records a failed event publish (used by the outbox relay).
func (g *Gateway) CountEventFailure() { g.m.eventFailures.Inc() }

// New builds a Gateway and registers its metrics.
func New(cfg Config, node string, log *slog.Logger, reg prometheus.Registerer,
	blobs blob.Store, idx index.Index, pub events.Publisher) *Gateway {
	g := &Gateway{
		cfg: cfg, node: node, log: log.With("component", "dimse"),
		blobs: blobs, idx: idx, events: pub, m: newMetrics(reg),
		remoteAEs: map[string]RemoteAE{},
	}
	g.SetRemoteAEs(cfg.RemoteAEs)
	return g
}

// SetRemoteAEs replaces the destination table at runtime (config reload hook).
func (g *Gateway) SetRemoteAEs(m map[string]RemoteAE) {
	cp := make(map[string]RemoteAE, len(m))
	for k, v := range m {
		if v.AETitle == "" {
			v.AETitle = k
		}
		cp[k] = v
	}
	g.mu.Lock()
	g.remoteAEs = cp
	g.mu.Unlock()
}

func (g *Gateway) remoteAE(name string) (RemoteAE, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	r, ok := g.remoteAEs[name]
	return r, ok
}

// Name implements runtime.Component.
func (g *Gateway) Name() string { return "dimse-gateway" }

// Run listens until ctx is cancelled.
func (g *Gateway) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", g.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", g.cfg.Listen, err)
	}
	return g.Serve(ctx, ln)
}

// Serve runs on an existing listener (used by tests).
func (g *Gateway) Serve(ctx context.Context, ln net.Listener) error {
	sc := g.serverConfig()
	if g.cfg.TLSCertFile != "" {
		cert, err := tls.LoadX509KeyPair(g.cfg.TLSCertFile, g.cfg.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("load TLS keypair: %w", err)
		}
		sc.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		ln = tls.NewListener(ln, sc.TLS)
	}
	g.log.Info("DICOM listener started", "addr", ln.Addr().String(), "ae_title", g.cfg.AETitle, "tls", sc.TLS != nil)
	return ae.Serve(ctx, &countingListener{Listener: ln, m: g.m}, sc)
}

func (g *Gateway) serverConfig() ae.ServerConfig {
	syntaxes := append([]string{}, ae.AllStorageSOPClasses...)
	syntaxes = append(syntaxes,
		ae.PatientRootQueryRetrieveInformationModelFind,
		ae.StudyRootQueryRetrieveInformationModelFind,
		ae.PatientRootQueryRetrieveInformationModelMove,
		ae.StudyRootQueryRetrieveInformationModelMove,
		ae.PatientRootQueryRetrieveInformationModelGet,
		ae.StudyRootQueryRetrieveInformationModelGet,
	)
	if g.cfg.AcceptAnySOPClass {
		syntaxes = append(syntaxes, "*")
	}
	// C-GET sends C-STORE back over the requestor's association, so the
	// acceptor must agree to act as SCU for the storage classes (role selection).
	roles := make([]pdu.RoleSelection, 0, len(ae.AllStorageSOPClasses))
	for _, uid := range ae.AllStorageSOPClasses {
		roles = append(roles, ae.BuildRole(uid, true, true))
	}

	storageSet := make(map[string]bool, len(ae.AllStorageSOPClasses))
	for _, u := range ae.AllStorageSOPClasses {
		storageSet[u] = true
	}

	return ae.ServerConfig{
		AETitle:                   g.cfg.AETitle,
		AllowAnyCalledAETitle:     g.cfg.AllowAnyCalledAETitle,
		MaxPDULength:              g.cfg.MaxPDULength,
		ImplementationVersionName: "RWS_DICOM_01",
		AcceptedAbstractSyntaxes:  syntaxes,
		MaxConcurrentAssociations: g.cfg.MaxAssociations,
		HandshakeTimeout:          g.cfg.HandshakeTimeout,
		IdleTimeout:               g.cfg.IdleTimeout,
		RoleSelections:            roles,
		SelectTransferSyntax:      selectSyntax(storageSet),
		OnCStore:                  g.onCStore,
		OnCFind:                   g.onCFind,
		OnCMove:                   g.onCMove,
		OnCGet:                    g.onCGet,
		Logger:                    g.log,
	}
}

// countingListener tracks association (TCP connection) counts for metrics.
type countingListener struct {
	net.Listener
	m *metrics
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.m.connsTotal.Inc()
	l.m.connsActive.Inc()
	return &countingConn{Conn: c, m: l.m}, nil
}

type countingConn struct {
	net.Conn
	m    *metrics
	once sync.Once
}

func (c *countingConn) Close() error {
	c.once.Do(func() { c.m.connsActive.Dec() })
	return c.Conn.Close()
}

// selectSyntax archives storage objects in whatever (supported) syntax the
// sender prefers, and uses a native syntax for everything else.
func selectSyntax(storage map[string]bool) func(string, []string) (string, bool) {
	return func(abstract string, proposed []string) (string, bool) {
		if storage[abstract] {
			return dcm.SelectStorageSyntax(proposed)
		}
		if ts, ok := dcm.SelectNativeSyntax(proposed); ok {
			return ts, true
		}
		return dcm.SelectStorageSyntax(proposed) // e.g. private classes via accept_any_sop_class
	}
}
