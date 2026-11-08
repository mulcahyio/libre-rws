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
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/events"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/licensing"
	"github.com/mulcahyio/libre-rws/platform/config"
	"github.com/mulcahyio/libre-rws/platform/runtime"
)

// version and buildDate (YYYY-MM-DD, the release date checked against a
// license's update period) are set at build time with -ldflags -X.
var (
	version   = "dev"
	buildDate = ""
)

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
	licStatus := flag.Bool("license-status", false, "show the license status and exit (0 receiving, 2 not receiving, 3 no license needed)")
	licRequest := flag.Bool("license-request", false, "print an activation request for this machine (offline activation) and exit")
	licActivate := flag.String("license-activate", "", "activate this machine online with a license KEY and exit")
	licInstall := flag.String("license-install", "", "install a license FILE (offline activation) and exit")
	licKey := flag.String("license-key", "", "license key to include in -license-request")
	licSite := flag.String("license-site", "", "site name for -license-request / -license-activate")
	backupTo := flag.String("backup", "", "write a consistent snapshot of the SQLite index to PATH and exit (safe while the module runs)")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
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
	if *licStatus || *licRequest || *licActivate != "" || *licInstall != "" {
		os.Exit(licenseCommand(*cfgPath, *licStatus, *licRequest, *licActivate, *licInstall, *licKey, *licSite))
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

// loadConfig reads the YAML file, applies environment overrides and merges
// LIBRE_DCM_REMOTE_AES (or the old RWS_DICOM_REMOTE_AES) into the destination table (environment wins per entry).
func loadConfig(cfgPath string) (Config, error) {
	cfg := defaults()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return cfg, err
	}
	if v := config.Getenv("LIBRE_DCM_REMOTE_AES", "RWS_DICOM_REMOTE_AES"); v != "" {
		aes, err := parseRemoteAEs(v)
		if err != nil {
			return cfg, err
		}
		if cfg.DICOM.RemoteAEs == nil {
			cfg.DICOM.RemoteAEs = map[string]gateway.RemoteAE{}
		}
		for k, a := range aes {
			cfg.DICOM.RemoteAEs[k] = a
		}
	}
	return cfg, nil
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

func run(cfgPath string) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	rt := runtime.New("LibreDCM", version, cfg.Admin)
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
		return err
	}

	if cfg.Index.Driver == "sqlite" {
		if p := sqlitePath(cfg.Index.DSN); p != "" {
			_ = os.MkdirAll(filepath.Dir(p), 0o750)
		}
	}
	idx, err := index.Open(ctx, cfg.Index.Driver, cfg.Index.DSN)
	if err != nil {
		return err
	}
	defer idx.Close()

	pub, err := events.New(ctx, cfg.Events, log)
	if err != nil {
		return err
	}
	defer pub.Close()

	rt.AddReadinessCheck("storage", blobs.Check)
	rt.AddReadinessCheck("index", idx.Ping)
	rt.AddReadinessCheck("events", pub.Check)

	gw := gateway.New(cfg.DICOM, rt.NodeID, log, rt.Registry, blobs, idx, pub)
	components := []runtime.Component{gw, &reloader{path: cfgPath, gw: gw, log: log}}
	lic, err := licensing.New(cfg.License, version, parseBuildDate(), log, rt.Registry)
	if err != nil {
		return fmt.Errorf("license: %w", err)
	}
	if lic != nil {
		gw.UseReceiveGate(lic.CanReceive)
		components = append(components, lic)
	}
	if cfg.Events.Driver != "" && cfg.Events.Driver != "none" {
		// Events go through the outbox so a bus outage can't lose them.
		relay := events.NewRelay(idx, pub, log, rt.Registry, gw.CountEventFailure)
		gw.UseOutbox(relay)
		components = append(components, relay)
	}
	for old, repl := range config.DeprecatedInUse() {
		log.Warn("deprecated setting; rename it", "old", old, "new", repl)
	}
	log.Info("LibreDCM starting", "version", version,
		"storage", cfg.Storage.Driver, "index", cfg.Index.Driver, "events", cfg.Events.Driver)
	return rt.Run(ctx, components...)
}

// probe lets distroless images (no curl/wget) run a container health check.
// When the admin endpoint serves HTTPS it probes over HTTPS; certificate checks
// are skipped because this only ever talks to its own loopback port.
func probe(addr string) int {
	scheme, tr := "http", http.DefaultTransport
	if config.Getenv("LIBRE_DCM_ADMIN_TLS_CERT") != "" {
		scheme = "https"
		tr = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // loopback self-probe
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

func envOr(def string, names ...string) string {
	if v := config.Getenv(names...); v != "" {
		return v
	}
	return def
}
