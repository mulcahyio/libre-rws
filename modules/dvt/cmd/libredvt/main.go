// Command libredvt is LibreDVT (DICOM Validation & Test): a web UI and a
// command line that validate DICOM objects and test DICOM services, using
// engines independent of LibreDCM (dicom3tools, dicom-validator, pydicom,
// pynetdicom).
//
//	libredvt serve                 web UI (settings from LIBRE_DVT_* / -config)
//	libredvt run [flags] PLAN      run a test plan; exit 0 only if it passed
//	libredvt validate FILE|DIR...  validate objects; exit 0 only if nothing failed
//	libredvt version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/report"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/runner"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/web"
	"github.com/mulcahyio/libre-rws/platform/config"
)

var version = "dev"

// Config is LibreDVT's configuration (YAML file and LIBRE_DVT_* variables).
type Config struct {
	Listen            string        `yaml:"listen" env:"LIBRE_DVT_LISTEN"`
	DataDir           string        `yaml:"data_dir" env:"LIBRE_DVT_DATA"`
	PlansDir          string        `yaml:"plans_dir" env:"LIBRE_DVT_PLANS"`
	AdminPassword     string        `yaml:"-" env:"LIBRE_DVT_ADMIN_PASSWORD"`
	AdminPasswordFile string        `yaml:"admin_password_file" env:"LIBRE_DVT_ADMIN_PASSWORD_FILE"`
	SessionIdle       time.Duration `yaml:"session_idle" env:"LIBRE_DVT_SESSION_IDLE"`
	SecureCookies     bool          `yaml:"secure_cookies" env:"LIBRE_DVT_SECURE_COOKIES"`
	TLSCert           string        `yaml:"tls_cert" env:"LIBRE_DVT_TLS_CERT"`
	TLSKey            string        `yaml:"tls_key" env:"LIBRE_DVT_TLS_KEY"`
	SCPAETitle        string        `yaml:"scp_ae_title" env:"LIBRE_DVT_SCP_AE"`
	SCPPort           int           `yaml:"scp_port" env:"LIBRE_DVT_SCP_PORT"`
	Engines           EngineConfig  `yaml:"engines"`
}

// EngineConfig locates the engines.
type EngineConfig struct {
	Dciodvfy     string        `yaml:"dciodvfy" env:"LIBRE_DVT_DCIODVFY"`
	Dcentvfy     string        `yaml:"dcentvfy" env:"LIBRE_DVT_DCENTVFY"`
	Python       string        `yaml:"python" env:"LIBRE_DVT_PYTHON"`
	Worker       string        `yaml:"worker" env:"LIBRE_DVT_WORKER"`
	StandardPath string        `yaml:"standard_path" env:"LIBRE_DVT_STANDARD_PATH"`
	Edition      string        `yaml:"edition" env:"LIBRE_DVT_EDITION"`
	Timeout      time.Duration `yaml:"timeout" env:"LIBRE_DVT_ENGINE_TIMEOUT"`
}

func defaults() Config {
	return Config{
		Listen:      ":8095",
		DataDir:     "/var/lib/libredvt",
		PlansDir:    "/opt/libredvt/plans",
		SessionIdle: 30 * time.Minute,
		SCPAETitle:  "LIBREDVT",
		SCPPort:     11115,
		Engines: EngineConfig{
			Dciodvfy: "dciodvfy", Dcentvfy: "dcentvfy", Python: "python3",
			Worker:       "/opt/libredvt/worker/libredvt_worker.py",
			StandardPath: "/opt/libredvt/standard", Edition: "2026d",
			Timeout: 2 * time.Minute,
		},
	}
}

func (c Config) engines() engine.Config {
	e := c.Engines
	return engine.Config{DciodvfyBin: e.Dciodvfy, DcentvfyBin: e.Dcentvfy, Python: e.Python, Worker: e.Worker,
		StandardPath: e.StandardPath, Edition: e.Edition, Timeout: e.Timeout}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "serve":
		code = serve(os.Args[2:])
	case "run":
		code = runCmd(os.Args[2:])
	case "validate":
		code = validateCmd(os.Args[2:])
	case "version", "-version", "--version":
		fmt.Println("LibreDVT", version)
	default:
		usage()
		code = 2
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  libredvt serve [-config FILE]
  libredvt run [-config FILE] [-target NAME | -host H -port N -called-aet AE [-calling-aet AE] -lab] [-junit F] [-html F] [-json F] PLAN
  libredvt validate [-config FILE] [-json F] [-html F] FILE|DIR...
  libredvt version`)
}

func loadConfig(fs *flag.FlagSet, args []string) (Config, error) {
	cfgPath := fs.String("config", "", "YAML configuration file")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	config.Prefix = "LIBRE_DVT_"
	cfg := defaults()
	if err := config.Load(*cfgPath, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func serve(args []string) int {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfg, err := loadConfig(fs, args)
	if err != nil {
		log.Error("config", "err", err)
		return 2
	}
	pw := cfg.AdminPassword
	if cfg.AdminPasswordFile != "" {
		b, err := os.ReadFile(cfg.AdminPasswordFile)
		if err != nil {
			log.Error("admin password file", "err", err)
			return 2
		}
		pw = strings.TrimRight(string(b), "\r\n")
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Error("data dir", "err", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	eng := cfg.engines()
	rn := &runner.Runner{Engines: eng, SCP: engine.SCP{AETitle: cfg.SCPAETitle, Port: cfg.SCPPort},
		WorkDir: filepath.Join(cfg.DataDir, "work"), Version: version}
	srv, err := web.New(ctx, web.Options{Store: st, Runner: rn, Validator: eng, Password: pw,
		SessionIdle: cfg.SessionIdle, SecureCookies: cfg.SecureCookies || cfg.TLSCert != "",
		PlansDir: cfg.PlansDir, Version: version, Log: log})
	if err != nil {
		log.Error("refusing to start", "err", err)
		return 2
	}
	hs := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	vers := eng.Versions(ctx)
	log.Info("LibreDVT starting", "version", version, "listen", cfg.Listen, "data", cfg.DataDir, "engines", vers)
	if cfg.TLSCert != "" {
		err = hs.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
	} else {
		err = hs.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http", "err", err)
		return 1
	}
	srv.Wait()
	return 0
}

func runCmd(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	targetName := fs.String("target", "", "target name from the data directory's registry (default: the plan's target)")
	host := fs.String("host", "", "ad-hoc target host (instead of the registry)")
	port := fs.Int("port", 11112, "ad-hoc target port")
	called := fs.String("called-aet", "", "ad-hoc target called AE title")
	calling := fs.String("calling-aet", "LIBREDVT", "ad-hoc target calling AE title")
	lab := fs.Bool("lab", false, "ad-hoc target is a lab system (allows store and move steps)")
	junit := fs.String("junit", "", "write JUnit XML to this file")
	htmlOut := fs.String("html", "", "write the HTML report to this file")
	jsonOut := fs.String("json", "", "write the run as JSON to this file")
	cfg, err := loadConfig(fs, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if fs.NArg() != 1 {
		usage()
		return 2
	}
	planPath := fs.Arg(0)
	data, err := os.ReadFile(planPath) // #nosec G304 -- plan path given by the operator on the command line
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	abs, _ := filepath.Abs(planPath)
	p, err := plan.Parse(data, filepath.Dir(abs))
	if err != nil {
		fmt.Fprintln(os.Stderr, "plan rejected:", err)
		return 2
	}
	var tgt *store.Target
	if *host != "" {
		class := store.Production
		if *lab {
			class = store.Lab
		}
		t := store.Target{Name: "cli", Host: *host, Port: *port, CalledAET: *called, CallingAET: *calling, Class: class}
		if err := t.Normalize(); err != nil {
			fmt.Fprintln(os.Stderr, "target:", err)
			return 2
		}
		tgt = &t
		p.Target = t.Name
	} else if p.Target != "" || *targetName != "" {
		if *targetName != "" {
			p.Target = *targetName
		}
		st, err := store.Open(cfg.DataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		t, ok, err := st.Target(p.Target)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if ok {
			tgt = &t
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	work, err := os.MkdirTemp("", "libredvt-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer os.RemoveAll(work)
	rn := &runner.Runner{Engines: cfg.engines(), SCP: engine.SCP{AETitle: cfg.SCPAETitle, Port: cfg.SCPPort}, WorkDir: work, Version: version}
	r := rn.Run(ctx, store.NewID(), p, tgt, func(partial *result.Run) {
		s := partial.Steps[len(partial.Steps)-1]
		fmt.Printf("%-8s %-24s %-9s %s\n", s.Status, s.ID, s.Type, strings.Join(s.Messages, "; "))
	})
	if r.Error != "" {
		fmt.Println("run error:", r.Error)
	}
	fmt.Printf("verdict: %s (%s)\n", r.Verdict, p.Name)
	if err := writeOutputs(r, *junit, *htmlOut, *jsonOut); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if r.Verdict.Passed() {
		return 0
	}
	return 1
}

func writeOutputs(r *result.Run, junit, htmlOut, jsonOut string) error {
	if junit != "" {
		b, err := report.JUnit(r)
		if err != nil {
			return err
		}
		if err := os.WriteFile(junit, b, 0o644); err != nil { // #nosec G306 -- CI artifact, no patient data
			return err
		}
	}
	if htmlOut != "" {
		b, err := report.HTML(r)
		if err != nil {
			return err
		}
		if err := os.WriteFile(htmlOut, b, 0o644); err != nil { // #nosec G306 -- CI artifact, no patient data
			return err
		}
	}
	if jsonOut != "" {
		b, err := jsonMarshal(r)
		if err != nil {
			return err
		}
		if err := os.WriteFile(jsonOut, b, 0o644); err != nil { // #nosec G306 -- CI artifact, no patient data
			return err
		}
	}
	return nil
}

func validateCmd(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	jsonOut := fs.String("json", "", "write the validation as JSON to this file")
	htmlOut := fs.String("html", "", "write the HTML report to this file")
	cfg, err := loadConfig(fs, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if fs.NArg() == 0 {
		usage()
		return 2
	}
	var files []string
	for _, a := range fs.Args() {
		info, err := os.Stat(a)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if info.IsDir() {
			fsFiles, err := store.CollectFiles(a)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			files = append(files, fsFiles...)
		} else {
			files = append(files, a)
		}
	}
	v := cfg.engines().Validate(context.Background(), files, nil)
	v.ID = store.NewID()
	v.Versions["libredvt"] = version
	for _, f := range v.Files {
		fmt.Printf("%-18s %s\n", f.Verdict, f.File)
		for _, e := range f.EngineErrors {
			fmt.Printf("    engine error  %s: %s\n", e.Engine, e.Message)
		}
		for _, x := range f.Findings {
			fmt.Printf("    %-7s %-15s %s %s\n", x.Severity, x.Engine, x.Path, x.Message)
		}
	}
	for _, x := range v.SetFindings {
		fmt.Printf("set %-7s %-15s %s\n", x.Severity, x.Engine, x.Message)
	}
	for _, e := range v.SetErrors {
		fmt.Printf("set engine error %s: %s\n", e.Engine, e.Message)
	}
	e, w, ee := v.Counts()
	fmt.Printf("verdict: %s (%d files, %d errors, %d warnings, %d engine errors)\n", v.Verdict, len(v.Files), e, w, ee)
	if *jsonOut != "" {
		b, err := jsonMarshal(&v)
		if err == nil {
			err = os.WriteFile(*jsonOut, b, 0o644) // #nosec G306 -- CI artifact
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if *htmlOut != "" {
		b, err := report.ValidationHTML(&v)
		if err == nil {
			err = os.WriteFile(*htmlOut, b, 0o644) // #nosec G306 -- CI artifact
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if v.Verdict.Passed() {
		return 0
	}
	return 1
}

func jsonMarshal(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return append(b, '\n'), err
}
