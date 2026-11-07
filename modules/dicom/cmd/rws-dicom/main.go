// Command rws-dicom runs the RWS DICOM module: a DIMSE gateway (C-ECHO,
// C-STORE, C-FIND, C-MOVE, C-GET) backed by pluggable storage, index and
// event bus. It runs identically as a single process, a single container, or
// N replicas behind a TCP load balancer.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"rws/modules/dicom/internal/blob"
	"rws/modules/dicom/internal/events"
	"rws/modules/dicom/internal/gateway"
	"rws/modules/dicom/internal/index"
	"rws/platform/config"
	"rws/platform/runtime"
)

// version is set at build time: -ldflags "-X main.version=1.2.3"
var version = "dev"

type Config struct {
	Admin   runtime.AdminConfig `yaml:"admin"`
	DICOM   gateway.Config      `yaml:"dicom"`
	Storage struct {
		Driver string `yaml:"driver" env:"RWS_STORAGE_DRIVER"` // fs (s3 planned)
		Path   string `yaml:"path" env:"RWS_STORAGE_PATH"`
	} `yaml:"storage"`
	Index struct {
		Driver string `yaml:"driver" env:"RWS_INDEX_DRIVER"` // sqlite | postgres
		DSN    string `yaml:"dsn" env:"RWS_INDEX_DSN"`
	} `yaml:"index"`
	Events events.Config `yaml:"events"`
}

func defaults() Config {
	var c Config
	c.Admin = runtime.DefaultAdmin()
	c.DICOM = gateway.DefaultConfig()
	c.Storage.Driver, c.Storage.Path = "fs", "./data/blobs"
	c.Index.Driver, c.Index.DSN = "sqlite", "file:./data/index.db"
	c.Events.Driver = "log"
	return c
}

func main() {
	cfgPath := flag.String("config", envOr("RWS_CONFIG", "config.yaml"), "path to YAML config")
	showVersion := flag.Bool("version", false, "print version and exit")
	healthcheck := flag.String("healthcheck", "", "probe http://ADDR/readyz and exit 0/1 (for container HEALTHCHECK)")
	echoAddr := flag.String("echo", "", "send a DICOM C-ECHO to HOST:PORT and exit 0/1 (install verification)")
	echoAE := flag.String("echo-ae", envOr("RWS_DICOM_AE_TITLE", "RWS_DICOM"), "called AE title for -echo")
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
		os.Exit(echo(*echoAddr, *echoAE))
	}
	if *backupTo != "" {
		if err := backup(*cfgPath, *backupTo); err != nil {
			fmt.Fprintln(os.Stderr, "rws-dicom: backup:", err)
			os.Exit(1)
		}
		fmt.Println("index snapshot written to", *backupTo)
		return
	}
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "rws-dicom:", err)
		os.Exit(1)
	}
}

// loadConfig reads the YAML file, applies environment overrides and merges
// RWS_DICOM_REMOTE_AES into the destination table (environment wins per entry).
func loadConfig(cfgPath string) (Config, error) {
	cfg := defaults()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return cfg, err
	}
	if v := os.Getenv("RWS_DICOM_REMOTE_AES"); v != "" {
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
	rt := runtime.New("rws-dicom", version, cfg.Admin)
	log := rt.Logger
	ctx := context.Background()

	if cfg.Storage.Driver != "fs" {
		return fmt.Errorf("storage driver %q not supported yet (fs)", cfg.Storage.Driver)
	}
	blobs, err := blob.NewFS(cfg.Storage.Path)
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
	if cfg.Events.Driver != "" && cfg.Events.Driver != "none" {
		// Events go through the outbox so a bus outage can't lose them.
		relay := events.NewRelay(idx, pub, log, rt.Registry, gw.CountEventFailure)
		gw.UseOutbox(relay)
		components = append(components, relay)
	}
	log.Info("rws-dicom starting", "version", version,
		"storage", cfg.Storage.Driver, "index", cfg.Index.Driver, "events", cfg.Events.Driver)
	return rt.Run(ctx, components...)
}

// probe lets distroless images (no curl/wget) run a container health check.
func probe(addr string) int {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/readyz")
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

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
