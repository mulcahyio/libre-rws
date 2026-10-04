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
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "rws-dicom:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg := defaults()
	if err := config.Load(cfgPath, &cfg); err != nil {
		return err
	}
	if v := os.Getenv("RWS_DICOM_REMOTE_AES"); v != "" {
		aes, err := parseRemoteAEs(v)
		if err != nil {
			return err
		}
		if cfg.DICOM.RemoteAEs == nil {
			cfg.DICOM.RemoteAEs = map[string]gateway.RemoteAE{}
		}
		for k, a := range aes { // environment wins over the file, per entry
			cfg.DICOM.RemoteAEs[k] = a
		}
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
	log.Info("rws-dicom starting", "version", version,
		"storage", cfg.Storage.Driver, "index", cfg.Index.Driver, "events", cfg.Events.Driver)
	return rt.Run(ctx, gw)
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
