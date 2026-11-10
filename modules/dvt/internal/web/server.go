package web

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/report"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/runner"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

//go:embed ui.html.tmpl
var uiTmpl string

// Validator validates files (engine.Config in production).
type Validator interface {
	Validate(ctx context.Context, files []string, names map[string]string) result.Validation
}

// Options configure the server.
type Options struct {
	Store         *store.Store
	Runner        *runner.Runner
	Validator     Validator
	Password      string
	SessionIdle   time.Duration
	SecureCookies bool
	PlansDir      string // bundled plans (read-only)
	Version       string
	Log           *slog.Logger
}

// Server is the web UI.
type Server struct {
	o    Options
	auth *auth
	tmpl *template.Template
	ctx  context.Context
	wg   sync.WaitGroup
}

// New builds the server. It refuses an empty password (DVT-SRS-061).
func New(ctx context.Context, o Options) (*Server, error) {
	if strings.TrimSpace(o.Password) == "" {
		return nil, errors.New("no admin password configured (set LIBRE_DVT_ADMIN_PASSWORD_FILE or LIBRE_DVT_ADMIN_PASSWORD)")
	}
	if len(o.Password) < 12 {
		return nil, errors.New("admin password must be at least 12 characters")
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	t, err := report.Template().Clone()
	if err != nil {
		return nil, err
	}
	if _, err := t.Parse(uiTmpl); err != nil {
		return nil, err
	}
	return &Server{o: o, auth: newAuth(o.Password, o.SessionIdle, o.SecureCookies), tmpl: t, ctx: ctx}, nil
}

// Wait blocks until background runs have finished.
func (s *Server) Wait() { s.wg.Wait() }

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = io.WriteString(w, report.CSS)
	})
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)

	p := http.NewServeMux()
	p.HandleFunc("POST /logout", s.logout)
	p.HandleFunc("GET /{$}", s.home)
	p.HandleFunc("GET /validate", s.validatePage)
	p.HandleFunc("POST /validate", s.validatePost)
	p.HandleFunc("GET /validations/{id}", s.validationPage)
	p.HandleFunc("GET /validations/{id}/report.html", s.validationReport)
	p.HandleFunc("POST /validations/{id}/delete", s.deleteResult)
	p.HandleFunc("GET /targets", s.targetsPage)
	p.HandleFunc("POST /targets", s.targetsPost)
	p.HandleFunc("POST /targets/{name}/delete", s.targetDelete)
	p.HandleFunc("GET /plans", s.plansPage)
	p.HandleFunc("POST /plans/run", s.plansRun)
	p.HandleFunc("GET /runs/{id}", s.runPage)
	p.HandleFunc("GET /runs/{id}/{file}", s.runFile)
	p.HandleFunc("POST /runs/{id}/delete", s.deleteResult)
	mux.Handle("/", s.auth.require(p))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, store.MaxUploadBytes+(64<<20))
		mux.ServeHTTP(w, r)
	})
}

type page struct {
	Title, Nav, CSRF, Version, Flash string
	Data                             any
	Refresh                          bool
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, pg page) {
	pg.CSRF = csrfOf(r)
	pg.Version = s.o.Version
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, name, pg); err != nil {
		s.o.Log.Error("render", "template", name, "err", err)
	}
}

// --- login ---

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "login", page{Title: "Log in"})
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.auth.limited(ip) {
		s.o.Log.Warn("login rate limited", "ip", ip)
		s.render(w, r, http.StatusTooManyRequests, "login", page{Title: "Log in", Flash: "Too many failed attempts. Wait a minute and try again."})
		return
	}
	t := s.auth.login(ip, r.FormValue("password"))
	if t == "" {
		s.o.Log.Warn("login failed", "ip", ip)
		s.render(w, r, http.StatusUnauthorized, "login", page{Title: "Log in", Flash: "Wrong password."})
		return
	}
	s.o.Log.Info("login", "ip", ip)
	s.auth.setCookie(w, t, 0)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if t, _ := s.auth.session(r); t != "" {
		s.auth.logout(t)
	}
	s.auth.setCookie(w, "", -1)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- home ---

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	runs, vals := s.o.Store.Runs(), s.o.Store.Validations()
	if len(runs) > 20 {
		runs = runs[:20]
	}
	if len(vals) > 20 {
		vals = vals[:20]
	}
	s.render(w, r, http.StatusOK, "home", page{Title: "Results", Nav: "home", Data: map[string]any{"Runs": runs, "Validations": vals}})
}

// --- validation ---

func (s *Server) validatePage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "validate", page{Title: "Validate objects", Nav: "validate"})
}

func (s *Server) validatePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader in Handler; 32 MiB in memory, rest spills to disk
		s.render(w, r, http.StatusBadRequest, "validate", page{Title: "Validate objects", Nav: "validate", Flash: "Upload failed: " + err.Error()})
		return
	}
	id := store.NewID()
	dir := s.o.Store.UploadDir(id)
	names, err := s.saveUploads(r, "files", dir)
	if err == nil && len(names) == 0 {
		err = errors.New("choose at least one file or zip archive")
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		s.render(w, r, http.StatusBadRequest, "validate", page{Title: "Validate objects", Nav: "validate", Flash: err.Error()})
		return
	}
	files := make([]string, 0, len(names))
	for f := range names {
		files = append(files, f)
	}
	sort.Strings(files)
	v := &result.Validation{ID: id, Label: strings.TrimSpace(r.FormValue("label")), Created: time.Now().UTC(), Running: true, Versions: map[string]string{}}
	if err := s.o.Store.SaveValidation(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		res := s.o.Validator.Validate(s.ctx, files, names)
		res.ID, res.Label, res.Created = id, v.Label, v.Created
		res.Versions["libredvt"] = s.o.Version
		if err := s.o.Store.SaveValidation(&res); err != nil {
			s.o.Log.Error("save validation", "id", id, "err", err)
		}
		s.o.Log.Info("validation finished", "id", id, "files", len(files), "verdict", res.Verdict)
	}()
	http.Redirect(w, r, "/validations/"+id, http.StatusSeeOther)
}

func (s *Server) saveUploads(r *http.Request, field, dir string) (map[string]string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	budget := int64(store.MaxUploadBytes)
	names := map[string]string{}
	for _, fh := range r.MultipartForm.File[field] {
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		got, err := store.SaveUpload(dir, filepath.Base(strings.ReplaceAll(fh.Filename, "\\", "/")), f, &budget)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		for k, v := range got {
			names[k] = v
		}
	}
	return names, nil
}

func (s *Server) validationPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.o.Store.Validation(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, http.StatusOK, "validation", page{Title: "Validation " + v.ID, Nav: "validate", Data: v, Refresh: v.Running})
}

func (s *Server) validationReport(w http.ResponseWriter, r *http.Request) {
	v, err := s.o.Store.Validation(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := report.ValidationHTML(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=libredvt-validation-%s.html", v.ID))
	_, _ = w.Write(b) // #nosec G705 -- html/template output (auto-escaped), served as an attachment
}

func (s *Server) deleteResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.o.Store.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.o.Log.Info("deleted", "id", id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- targets ---

func (s *Server) targetsPage(w http.ResponseWriter, r *http.Request) {
	s.renderTargets(w, r, http.StatusOK, "")
}

func (s *Server) renderTargets(w http.ResponseWriter, r *http.Request, status int, flash string) {
	ts, err := s.o.Store.Targets()
	if err != nil {
		flash = err.Error()
	}
	s.render(w, r, status, "targets", page{Title: "Targets", Nav: "targets", Data: ts, Flash: flash})
}

func (s *Server) targetsPost(w http.ResponseWriter, r *http.Request) {
	port, _ := strconv.Atoi(r.FormValue("port"))
	t := store.Target{Name: r.FormValue("name"), Host: r.FormValue("host"), Port: port,
		CalledAET: r.FormValue("called_aet"), CallingAET: r.FormValue("calling_aet"), Class: r.FormValue("class")}
	if err := s.o.Store.PutTarget(t); err != nil {
		s.renderTargets(w, r, http.StatusBadRequest, err.Error())
		return
	}
	s.o.Log.Info("target saved", "name", t.Name, "class", t.Class)
	http.Redirect(w, r, "/targets", http.StatusSeeOther)
}

func (s *Server) targetDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.o.Store.DeleteTarget(r.PathValue("name")); err != nil {
		s.renderTargets(w, r, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, "/targets", http.StatusSeeOther)
}

// --- plans and runs ---

type bundled struct {
	File, Name, Target string
	Steps              int
	Err                string
}

func (s *Server) bundledPlans() []bundled {
	if s.o.PlansDir == "" {
		return nil
	}
	m, _ := filepath.Glob(filepath.Join(s.o.PlansDir, "*.yaml"))
	sort.Strings(m)
	var out []bundled
	for _, f := range m {
		b := bundled{File: filepath.Base(f)}
		data, err := os.ReadFile(f) // #nosec G304 -- file from the bundled plans directory listing
		if err == nil {
			var p *plan.Plan
			p, err = plan.Parse(data, s.o.PlansDir)
			if p != nil {
				b.Name, b.Target, b.Steps = p.Name, p.Target, len(p.Steps)
			}
		}
		if err != nil {
			b.Err = err.Error()
		}
		out = append(out, b)
	}
	return out
}

func (s *Server) plansPage(w http.ResponseWriter, r *http.Request) {
	s.renderPlans(w, r, http.StatusOK, "")
}

func (s *Server) renderPlans(w http.ResponseWriter, r *http.Request, status int, flash string) {
	ts, _ := s.o.Store.Targets()
	runs := s.o.Store.Runs()
	s.render(w, r, status, "plans", page{Title: "Test plans", Nav: "plans", Flash: flash,
		Data: map[string]any{"Bundled": s.bundledPlans(), "Targets": ts, "Runs": runs}})
}

func (s *Server) plansRun(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader in Handler; 32 MiB in memory, rest spills to disk
		s.renderPlans(w, r, http.StatusBadRequest, "Upload failed: "+err.Error())
		return
	}
	id := store.NewID()
	var data []byte
	var dir string
	if name := r.FormValue("bundled"); name != "" {
		if filepath.Base(name) != name || !strings.HasSuffix(name, ".yaml") || s.o.PlansDir == "" {
			s.renderPlans(w, r, http.StatusBadRequest, "unknown bundled plan")
			return
		}
		var err error
		data, err = os.ReadFile(filepath.Join(s.o.PlansDir, name)) // #nosec G304 G703 -- name is a base name checked above
		if err != nil {
			s.renderPlans(w, r, http.StatusBadRequest, "unknown bundled plan")
			return
		}
		dir = s.o.PlansDir
	} else {
		fhs := r.MultipartForm.File["plan"]
		if len(fhs) != 1 {
			s.renderPlans(w, r, http.StatusBadRequest, "choose a bundled plan or upload one plan file")
			return
		}
		f, err := fhs[0].Open()
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(f, 1<<20))
			_ = f.Close()
		}
		if err != nil {
			s.renderPlans(w, r, http.StatusBadRequest, err.Error())
			return
		}
		dir = s.o.Store.UploadDir(id)
		if _, err := s.saveUploads(r, "fixtures", dir); err != nil {
			_ = os.RemoveAll(dir)
			s.renderPlans(w, r, http.StatusBadRequest, err.Error())
			return
		}
	}
	p, err := plan.Parse(data, dir)
	if err != nil {
		_ = os.RemoveAll(s.o.Store.UploadDir(id))
		s.renderPlans(w, r, http.StatusBadRequest, "Plan rejected: "+err.Error())
		return
	}
	if t := r.FormValue("target"); t != "" {
		p.Target = t
	}
	var tgt *store.Target
	if p.Target != "" {
		t, ok, err := s.o.Store.Target(p.Target)
		if err != nil {
			s.renderPlans(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		if ok {
			tgt = &t
		}
	}
	runDir := s.o.Store.RunDir(id)
	if err := os.MkdirAll(runDir, 0o750); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = os.WriteFile(filepath.Join(runDir, "plan.yaml"), data, 0o600) // #nosec G703 -- runDir built from a fresh NewID
	initial := &result.Run{ID: id, Plan: p.Name, PlanSHA256: p.SHA256, Target: p.Target, Started: time.Now().UTC(), Running: true}
	if err := s.o.Store.SaveRun(initial); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		res := s.o.Runner.Run(s.ctx, id, p, tgt, func(partial *result.Run) {
			cp := *partial
			cp.Running = true
			_ = s.o.Store.SaveRun(&cp)
		})
		if err := WriteRunFiles(s.o.Store, res); err != nil {
			s.o.Log.Error("save run", "id", id, "err", err)
		}
		s.o.Log.Info("run finished", "id", id, "plan", p.Name, "verdict", res.Verdict)
	}()
	http.Redirect(w, r, "/runs/"+id, http.StatusSeeOther)
}

// WriteRunFiles saves run.json, report.html and junit.xml.
func WriteRunFiles(st *store.Store, r *result.Run) error {
	if err := st.SaveRun(r); err != nil {
		return err
	}
	h, err := report.HTML(r)
	if err != nil {
		return err
	}
	j, err := report.JUnit(r)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(st.RunDir(r.ID), "report.html"), h, 0o600); err != nil { // #nosec G703 -- run ID generated by NewID
		return err
	}
	return os.WriteFile(filepath.Join(st.RunDir(r.ID), "junit.xml"), j, 0o600) // #nosec G703 -- run ID generated by NewID
}

func (s *Server) runPage(w http.ResponseWriter, r *http.Request) {
	run, err := s.o.Store.Run(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, http.StatusOK, "run", page{Title: "Run " + run.ID, Nav: "plans", Data: run, Refresh: run.Running})
}

func (s *Server) runFile(w http.ResponseWriter, r *http.Request) {
	id, file := r.PathValue("id"), r.PathValue("file")
	types := map[string]string{"report.html": "text/html; charset=utf-8", "junit.xml": "application/xml", "plan.yaml": "text/plain; charset=utf-8", "run.json": "application/json"}
	ct, ok := types[file]
	if !ok || !store.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(filepath.Join(s.o.Store.RunDir(id), file)) // #nosec G304 G703 -- id validated by ValidID, file from a fixed list
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=libredvt-%s-%s", id, file))
	_, _ = w.Write(b) // #nosec G705 -- files LibreDVT wrote itself (escaped HTML, XML, JSON, plan), served as attachments with nosniff
}
