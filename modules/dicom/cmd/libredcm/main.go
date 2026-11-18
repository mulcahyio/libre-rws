// Command LibreDCM runs the Libre DCM module of Libre RWS: a DIMSE gateway (C-ECHO,
// C-STORE, C-FIND, C-MOVE, C-GET) backed by pluggable storage, index and
// event bus. It runs identically as a single process, a single container, or
// N replicas behind a TCP load balancer.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/blob"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/buildinfo"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/control"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/events"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/licensing"
	"github.com/mulcahyio/libre-rws/platform/config"
	"github.com/mulcahyio/libre-rws/platform/runtime"
)

// The version and build date are set at build time with -ldflags -X in
// package buildinfo, the one source of truth (DCM-SRS-057, DCM-SRS-098).

type Config struct {
	Admin   runtime.AdminConfig `yaml:"admin"`
	DICOM   gateway.Config      `yaml:"dicom"`
	Storage struct {
		Driver string        `yaml:"driver" env:"LIBRE_DCM_STORAGE_DRIVER,RWS_STORAGE_DRIVER"` // fs | s3
		Path   string        `yaml:"path" env:"LIBRE_DCM_STORAGE_PATH,RWS_STORAGE_PATH"`       // fs
		S3     blob.S3Config `yaml:"s3"`
	} `yaml:"storage"`
	Index struct {
		Driver string `yaml:"driver" env:"LIBRE_DCM_INDEX_DRIVER,RWS_INDEX_DRIVER"` // sqlite | postgres
		DSN    string `yaml:"dsn" env:"LIBRE_DCM_INDEX_DSN,RWS_INDEX_DSN"`
	} `yaml:"index"`
	Events  events.Config    `yaml:"events"`
	License licensing.Config `yaml:"license"`
	Control struct {
		// BackupDir is the only place POST /control/v1/backup writes index
		// snapshots to (DCM-SRS-090). Empty: backups through the control
		// API are off. A client can never choose or change this path.
		BackupDir string `yaml:"backup_dir" env:"LIBRE_DCM_CONTROL_BACKUP_DIR"`
		// OverlayFile is the file LibreDCM writes configuration changes
		// from the control API to (DCM-SRS-093). It can hold only the two
		// settings that change without a restart. Empty: configuration
		// edit through the control API is off.
		OverlayFile string `yaml:"overlay_file" env:"LIBRE_DCM_CONTROL_OVERLAY_FILE"`
	} `yaml:"control"`
}

func defaults() Config {
	var c Config
	c.Admin = runtime.DefaultAdmin()
	c.DICOM = gateway.DefaultConfig()
	c.Storage.Driver, c.Storage.Path = "fs", "./data/blobs"
	c.Index.Driver, c.Index.DSN = "sqlite", "file:./data/index.db"
	c.Events.Driver = "log"
	c.Events.LegacySubjects = true
	c.License.File = "./data/license.lic"
	c.Control.OverlayFile = "./data/config-overlay.yaml"
	return c
}

func main() {
	config.Prefix = "LIBRE_DCM_" // shared settings: LIBRE_DCM_ADMIN_TOKEN, LIBRE_DCM_LOG_LEVEL, ...
	cfgPath := flag.String("config", envOr("config.yaml", "LIBRE_DCM_CONFIG", "RWS_CONFIG"), "path to YAML config")
	showVersion := flag.Bool("version", false, "print version and exit")
	healthcheck := flag.String("healthcheck", "", "probe http://ADDR/readyz and exit 0/1 (for container HEALTHCHECK)")
	echoAddr := flag.String("echo", "", "send a DICOM C-ECHO to HOST:PORT and exit 0/1 (install verification)")
	echoAE := flag.String("echo-ae", envOr("LIBREDCM", "LIBRE_DCM_AE_TITLE", "RWS_DICOM_AE_TITLE"), "called AE title for -echo")
	genCertDir := flag.String("gen-cert", "", "write a self-signed TLS certificate (tls.crt, tls.key) to DIR and exit")
	genCertHosts := flag.String("gen-cert-hosts", "", "comma-separated DNS names / IPs for -gen-cert")
	genCertForce := flag.Bool("gen-cert-force", false, "replace an existing certificate with -gen-cert")
	echoTLS := flag.Bool("echo-tls", config.Getenv("LIBRE_DCM_TLS_CERT", "RWS_DICOM_TLS_CERT") != "", "use DICOM TLS for -echo (default: on when this node has a TLS certificate)")
	licStatus := flag.Bool("license-status", false, "show the license status and exit (0 licensed, 2 unlicensed, 3 no license needed)")
	licRequest := flag.Bool("license-request", false, "print an activation request for this machine (offline activation) and exit")
	licActivate := flag.String("license-activate", "", "activate this machine online with a license KEY and exit")
	licInstall := flag.String("license-install", "", "install a license FILE (offline activation) and exit")
	licKey := flag.String("license-key", "", "license key to include in -license-request")
	licSite := flag.String("license-site", "", "site name for -license-request / -license-activate")
	licAck := flag.String("license-ack", "", "acknowledge the unlicensed notice as administrator NAME (lifts the configuration block for 24 hours) and exit")
	backupTo := flag.String("backup", "", "write a consistent snapshot of the SQLite index to PATH and exit (safe while the module runs)")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.Version)
		return
	}
	if *healthcheck != "" {
		os.Exit(probe(*healthcheck))
	}
	if *echoAddr != "" {
		os.Exit(echo(*echoAddr, *echoAE, *echoTLS))
	}
	if *genCertDir != "" {
		if err := genCert(*genCertDir, *genCertHosts, *genCertForce); err != nil {
			fmt.Fprintln(os.Stderr, "LibreDCM: gen-cert:", err)
			os.Exit(1)
		}
		return
	}
	if *licStatus || *licRequest || *licActivate != "" || *licInstall != "" || *licAck != "" {
		os.Exit(licenseCommand(*cfgPath, *licStatus, *licRequest, *licActivate, *licInstall, *licKey, *licSite, *licAck))
	}
	if *backupTo != "" {
		if err := backup(*cfgPath, *backupTo); err != nil {
			fmt.Fprintln(os.Stderr, "LibreDCM: backup:", err)
			os.Exit(1)
		}
		fmt.Println("index snapshot written to", *backupTo)
		return
	}
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "LibreDCM:", err)
		os.Exit(1)
	}
}

// loadConfig reads the YAML file, then the overlay file the control API
// writes (overlay.go), applies environment overrides and merges
// LIBRE_DCM_REMOTE_AES (or the old RWS_DICOM_REMOTE_AES) into the destination table (environment wins per entry).
func loadConfig(cfgPath string) (Config, error) {
	cfg, _, err := loadConfigOverlay(cfgPath, nil, nil)
	return cfg, err
}

// backup snapshots the index without stopping a running module. Images are
// write-once files, so copying the blob folder after the snapshot gives a
// backup in which every indexed image is present.
func backup(cfgPath, dest string) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	if cfg.Index.Driver != "sqlite" {
		return fmt.Errorf("index driver %q: use the database's own backup tools (pg_dump)", cfg.Index.Driver)
	}
	ctx := context.Background()
	idx, err := index.Open(ctx, cfg.Index.Driver, cfg.Index.DSN)
	if err != nil {
		return err
	}
	defer idx.Close()
	return idx.Backup(ctx, dest)
}

// app is a built, not yet running, node: the chassis, its components and
// what must be closed when it stops.
type app struct {
	rt         *runtime.Runtime
	gw         *gateway.Gateway
	components []runtime.Component
	closers    []func()
}

func (a *app) close() {
	for i := len(a.closers) - 1; i >= 0; i-- {
		a.closers[i]()
	}
}

func run(cfgPath string) error {
	a, err := build(cfgPath)
	if a != nil {
		defer a.close()
	}
	if err != nil {
		return err
	}
	return a.rt.Run(context.Background(), a.components...)
}

// build loads the configuration and wires the node together without
// starting any listener (tests use it to exercise the real wiring).
func build(cfgPath string) (*app, error) {
	cfg, use, err := loadConfigOverlay(cfgPath, nil, nil)
	if err != nil {
		return nil, err
	}
	fileKeys, err := config.FileKeys(cfgPath)
	if err != nil {
		return nil, err
	}
	a := &app{}
	rt := runtime.New("LibreDCM", buildinfo.Version, cfg.Admin)
	a.rt = rt
	log := rt.Logger
	ctx := context.Background()

	var blobs blob.Store
	switch cfg.Storage.Driver {
	case "fs":
		blobs, err = blob.NewFS(cfg.Storage.Path)
	case "s3":
		blobs, err = blob.NewS3(ctx, cfg.Storage.S3)
	default:
		err = fmt.Errorf("storage driver %q not supported (fs, s3)", cfg.Storage.Driver)
	}
	if err != nil {
		return a, err
	}

	if cfg.Index.Driver == "sqlite" {
		if p := sqlitePath(cfg.Index.DSN); p != "" {
			_ = os.MkdirAll(filepath.Dir(p), 0o750)
		}
	}
	idx, err := index.Open(ctx, cfg.Index.Driver, cfg.Index.DSN)
	if err != nil {
		return a, err
	}
	a.closers = append(a.closers, func() { _ = idx.Close() })

	pub, err := events.New(ctx, cfg.Events, log)
	if err != nil {
		return a, err
	}
	a.closers = append(a.closers, func() { _ = pub.Close() })

	rt.AddReadinessCheck("storage", blobs.Check)
	rt.AddReadinessCheck("index", idx.Ping)
	rt.AddReadinessCheck("events", pub.Check)

	gw := gateway.New(cfg.DICOM, rt.NodeID, log, rt.Registry, blobs, idx, pub)
	a.gw = gw
	lic, err := licensing.New(cfg.License, buildinfo.Version, parseBuildDate(), log, rt.Registry)
	if err != nil {
		return a, fmt.Errorf("license: %w", err)
	}
	// Licensing never touches the DICOM gateway (DCM-SRS-058). An unlicensed,
	// unacknowledged node only blocks configuration reloads (DCM-SRS-060).
	rl := &reloader{path: cfgPath, overlayPath: cfg.Control.OverlayFile, gw: gw, log: log, use: use}
	if use.RemoteAEs || use.AllowedAEs {
		log.Info("live settings taken from the configuration overlay", "file", cfg.Control.OverlayFile,
			"remote_aes", use.RemoteAEs, "allowed_aes", use.AllowedAEs)
	}
	// Control API for Libre WT (DCM-SRS-077, -080…097); refused unless an
	// admin token is set. It reads the licence state and offers the licence
	// functions but lives outside the gateway: licensing never touches DICOM
	// services (DCM-SRS-058).
	settings := config.Describe(&cfg, fileKeys)
	api := &control.API{
		Index: idx, Resolver: gw, Live: gw,
		Node: control.Node{ID: rt.NodeID, AETitle: cfg.DICOM.AETitle, Version: buildinfo.Version, Started: rt.Started(),
			DICOMListen: cfg.DICOM.Listen, DICOMTLS: cfg.DICOM.TLSCertFile != "", AdminTLS: rt.AdminTLS(),
			Storage: cfg.Storage.Driver, Index: cfg.Index.Driver, Events: cfg.Events.Driver,
			AuditLogOff: cfg.DICOM.AuditLogOff},
		Ready: rt.Readiness, Metrics: rt.Registry, License: lic.Summary,
		Settings: settings, RemoteAEEnv: remoteAEsFromEnv(),
		Secrets: append(control.SecretValues(settings), rt.AdminSecrets()...),
		Logs:    rt.Logs, Audit: rt.Audit,
		AuditLog: log, Echo: control.DICOMEcho, Reloader: rl, Data: gw, Editor: rl,
		Backup: &control.Backup{Dir: cfg.Control.BackupDir, Driver: cfg.Index.Driver, Snapshot: idx.Backup,
			IndexBytes: func() (int64, error) { return sqliteSize(cfg.Index.DSN) }},
	}
	components := []runtime.Component{gw, rl}
	if lic != nil {
		api.Licence = lic // a nil *Licensing must stay a nil interface (source build)
		rl.gate = lic.ConfigChangesAllowed
		rt.AddVersionInfo(lic.Info)
		components = append(components, lic)
	}
	if cfg.Events.Driver != "" && cfg.Events.Driver != "none" {
		// Events go through the outbox so a bus outage can't lose them.
		relay := events.NewRelay(idx, pub, log, rt.Registry, gw.CountEventFailure)
		gw.UseOutbox(relay)
		components = append(components, relay)
	}
	api.Register(rt)
	for old, repl := range config.DeprecatedInUse() {
		log.Warn("deprecated setting; rename it", "old", old, "new", repl)
	}
	log.Info("LibreDCM starting", "version", buildinfo.Version,
		"storage", cfg.Storage.Driver, "index", cfg.Index.Driver, "events", cfg.Events.Driver)
	a.components = components
	return a, nil
}

// remoteAEsFromEnv names the destinations that LIBRE_DCM_REMOTE_AES (or its
// old name) supplies. The environment is fixed while the process runs, so
// this also holds after a reload.
func remoteAEsFromEnv() map[string]bool {
	out := map[string]bool{}
	if v := config.Getenv("LIBRE_DCM_REMOTE_AES", "RWS_DICOM_REMOTE_AES"); v != "" {
		if aes, err := parseRemoteAEs(v); err == nil {
			for name := range aes {
				out[name] = true
			}
		}
	}
	return out
}

// probe lets distroless images (no curl/wget) run a container health check.
// When the admin endpoint serves HTTPS it probes over HTTPS; certificate checks
// are skipped because this only ever talks to its own loopback port.
func probe(addr string) int {
	scheme, tr := "http", http.DefaultTransport
	if config.Getenv("LIBRE_DCM_ADMIN_TLS_CERT") != "" {
		scheme = "https"
		tr = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- loopback self-probe of this node's own admin port
	}
	c := http.Client{Timeout: 3 * time.Second, Transport: tr}
	resp, err := c.Get(scheme + "://" + addr + "/readyz")
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func sqlitePath(dsn string) string {
	p := dsn
	if len(p) > 5 && p[:5] == "file:" {
		p = p[5:]
	}
	for i, c := range p {
		if c == '?' {
			return p[:i]
		}
	}
	return p
}

// sqliteSize is the size of a SQLite index on disk: the database file plus
// its write-ahead log.
func sqliteSize(dsn string) (int64, error) {
	p := sqlitePath(dsn)
	st, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	n := st.Size()
	if wal, err := os.Stat(p + "-wal"); err == nil {
		n += wal.Size()
	}
	return n, nil
}

func envOr(def string, names ...string) string {
	if v := config.Getenv(names...); v != "" {
		return v
	}
	return def
}
