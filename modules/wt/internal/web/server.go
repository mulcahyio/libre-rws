// Package web is Libre WT's web interface: server-rendered pages, no
// JavaScript, strict Content Security Policy (WT-SRS-001…012).
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

//go:embed templates/*.html static/*
var assets embed.FS

// DCM is the part of the LibreDCM client the web interface uses.
type DCM interface {
	URL() string
	Conflicts(ctx context.Context, state string) ([]dcmclient.Conflict, error)
	Conflict(ctx context.Context, id string) (dcmclient.Detail, error)
	Plan(ctx context.Context, id, action string) (dcmclient.Plan, error)
	Resolve(ctx context.Context, id, action, operator, reason string) (dcmclient.Result, error)
	Version(ctx context.Context) (map[string]any, error)
}

// Config configures the web interface.
type Config struct {
	Version     string
	Secure      bool // served over HTTPS: cookies get the Secure flag
	IdleTimeout time.Duration
	MaxSession  time.Duration
}

// Server serves Libre WT.
type Server struct {
	cfg  Config
	acct *accounts.Store
	dcm  DCM
	log  *slog.Logger
	ss   *sessions
	tmpl map[string]*template.Template
}

// New builds the server and parses the templates.
func New(cfg Config, acct *accounts.Store, dcm DCM, log *slog.Logger) (*Server, error) {
	s := &Server{cfg: cfg, acct: acct, dcm: dcm, log: log, ss: newSessions(cfg.IdleTimeout, cfg.MaxSession, cfg.Secure),
		tmpl: map[string]*template.Template{}}
	funcs := template.FuncMap{
		"fmtTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("2006-01-02 15:04")
		},
		"fmtTimeP": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Local().Format("2006-01-02 15:04")
		},
		"kindLabel":   kindLabel,
		"actionLabel": actionLabel,
		"stateLabel":  stateLabel,
		"orDash": func(s string) string {
			if strings.TrimSpace(s) == "" {
				return "—"
			}
			return s
		},
		"differs": func(a, b string) bool { return a != b },
	}
	pages, err := fs.Glob(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, p := range pages {
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		if name == "layout" {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(assets, "templates/layout.html", p)
		if err != nil {
			return nil, fmt.Errorf("web: template %s: %w", name, err)
		}
		s.tmpl[name] = t
	}
	return s, nil
}

func kindLabel(k string) string {
	switch k {
	case "patient_id":
		return "Patient ID differs"
	case "series_study":
		return "Series under another study"
	case "instance_relation":
		return "Instance under another study/series"
	}
	return k
}

func actionLabel(a string) string {
	switch a {
	case "correct_incoming":
		return "Correct the incoming images to the archived patient"
	case "correct_existing":
		return "Correct the archived study to the incoming patient"
	case "reject":
		return "Reject the incoming images"
	}
	return a
}

func stateLabel(s string) string {
	switch s {
	case "open":
		return "Held"
	case "accepted":
		return "Accepted as separate study"
	case "corrected":
		return "Corrected"
	case "rejected":
		return "Rejected"
	}
	return s
}

// Handler returns the HTTP handler with security headers.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /setup", s.setupPage)
	mux.HandleFunc("POST /setup", s.setupPost)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("POST /logout", s.authed(accounts.Viewer, s.logout))
	mux.HandleFunc("GET /{$}", s.authed(accounts.Viewer, func(w http.ResponseWriter, r *http.Request, _ *reqCtx) {
		http.Redirect(w, r, "/exceptions", http.StatusSeeOther)
	}))
	mux.HandleFunc("GET /exceptions", s.authed(accounts.Viewer, s.exceptions))
	mux.HandleFunc("GET /exceptions/{id}", s.authed(accounts.Viewer, s.exception))
	mux.HandleFunc("GET /exceptions/{id}/review", s.authed(accounts.Admin, s.review))
	mux.HandleFunc("POST /exceptions/{id}/resolve", s.authed(accounts.Admin, s.resolve))
	mux.HandleFunc("GET /users", s.authed(accounts.Admin, s.users))
	mux.HandleFunc("POST /users", s.authed(accounts.Admin, s.userCreate))
	mux.HandleFunc("POST /users/{id}", s.authed(accounts.Admin, s.userUpdate))
	mux.HandleFunc("GET /account", s.authed(accounts.Viewer, s.account))
	mux.HandleFunc("POST /account/password", s.authed(accounts.Viewer, s.accountPassword))
	mux.HandleFunc("GET /audit", s.authed(accounts.Admin, s.auditPage))
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// reqCtx is the authenticated request context.
type reqCtx struct {
	token string
	sess  *session
	user  accounts.User
}

func rank(r accounts.Role) int {
	switch r {
	case accounts.Admin:
		return 3
	case accounts.Operator:
		return 2
	case accounts.Viewer:
		return 1
	}
	return 0
}

// authed requires a live session whose user is enabled, unchanged since
// login (epoch) and at least role min. State-changing requests also need the
// session's CSRF token (WT-SRS-004, -005).
func (s *Server) authed(min accounts.Role, h func(http.ResponseWriter, *http.Request, *reqCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if has, err := s.acct.HasUsers(r.Context()); err == nil && !has {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		token, ss := s.ss.get(r)
		if ss == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		u, err := s.acct.Get(r.Context(), ss.userID)
		if err != nil || u.Disabled || u.Epoch != ss.epoch {
			s.ss.end(w, token)
			http.Redirect(w, r, "/login?ended=1", http.StatusSeeOther)
			return
		}
		if rank(u.Role) < rank(min) {
			s.render(w, r, http.StatusForbidden, "error", &reqCtx{token, ss, u}, map[string]any{
				"Title": "Not allowed", "Message": "Your role (" + string(u.Role) + ") can't open this page."})
			return
		}
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil || !csrfOK(ss, r.PostFormValue("csrf")) {
				s.render(w, r, http.StatusForbidden, "error", &reqCtx{token, ss, u}, map[string]any{
					"Title": "Request refused", "Message": "The form expired or didn't come from this page. Go back, reload and try again."})
				return
			}
		}
		h(w, r, &reqCtx{token: token, sess: ss, user: u})
	}
}

func (s *Server) audit(r *http.Request, user, action, target, outcome, detail string) error {
	err := s.acct.Audit(r.Context(), accounts.Entry{User: user, IP: clientIP(r), Action: action, Target: target, Outcome: outcome, Detail: detail})
	if err != nil {
		s.log.Error("audit write failed", "action", action, "err", err)
	}
	return err
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, page string, rc *reqCtx, data map[string]any) {
	t, ok := s.tmpl[page]
	if !ok {
		http.Error(w, "page not found", http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["Version"] = s.cfg.Version
	data["DCMURL"] = s.dcm.URL()
	if rc != nil {
		data["User"] = rc.user
		data["IsAdmin"] = rc.user.Role == accounts.Admin
		data["CSRF"] = rc.sess.csrf
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := t.Execute(w, data); err != nil {
		s.log.Error("render", "page", page, "err", err)
	}
}

// ---------------------------------------------------------------- setup / login

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	if has, _ := s.acct.HasUsers(r.Context()); has {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "setup", nil, nil)
}

func (s *Server) setupPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	user, pw := r.PostFormValue("username"), r.PostFormValue("password")
	if pw != r.PostFormValue("password2") {
		s.render(w, r, http.StatusUnprocessableEntity, "setup", nil, map[string]any{"Error": "The passwords don't match.", "Username": user})
		return
	}
	u, err := s.acct.CreateFirstAdmin(r.Context(), user, pw)
	if errors.Is(err, accounts.ErrSetupDone) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.render(w, r, http.StatusUnprocessableEntity, "setup", nil, map[string]any{"Error": err.Error(), "Username": user})
		return
	}
	_ = s.audit(r, u.Username, "setup.admin-created", u.Username, "success", "first administrator created")
	s.ss.start(w, u.ID, u.Epoch, u.Username)
	http.Redirect(w, r, "/exceptions", http.StatusSeeOther)
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if has, _ := s.acct.HasUsers(r.Context()); !has {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	data := map[string]any{}
	if r.URL.Query().Get("ended") != "" {
		data["Notice"] = "Your session ended (timeout, logout elsewhere, or your account changed). Please log in again."
	}
	s.render(w, r, http.StatusOK, "login", nil, data)
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	user := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	ip := clientIP(r)
	if s.ss.limited(ip, user) {
		_ = s.audit(r, user, "login", user, "failure", "rate limited")
		s.render(w, r, http.StatusTooManyRequests, "login", nil, map[string]any{"Error": "Too many failed attempts. Wait 15 minutes and try again.", "Username": user})
		return
	}
	u, err := s.acct.Authenticate(r.Context(), user, r.PostFormValue("password"))
	if err != nil {
		s.ss.fail(ip, user)
		_ = s.audit(r, user, "login", user, "failure", "invalid credentials")
		s.render(w, r, http.StatusUnauthorized, "login", nil, map[string]any{"Error": "Invalid username or password.", "Username": user})
		return
	}
	_ = s.audit(r, u.Username, "login", u.Username, "success", "")
	s.ss.start(w, u.ID, u.Epoch, u.Username)
	http.Redirect(w, r, "/exceptions", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	_ = s.audit(r, rc.user.Username, "logout", rc.user.Username, "success", "")
	s.ss.end(w, rc.token)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---------------------------------------------------------------- exceptions

var stateFilters = []string{"active", "open", "accepted", "resolved", "all"}

func (s *Server) exceptions(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	state := r.URL.Query().Get("state")
	valid := false
	for _, f := range stateFilters {
		valid = valid || f == state
	}
	if !valid {
		state = "active"
	}
	data := map[string]any{"State": state, "Filters": stateFilters}
	list, err := s.dcm.Conflicts(r.Context(), state)
	if err != nil {
		data["Error"] = err.Error()
	}
	data["Conflicts"] = list
	s.render(w, r, http.StatusOK, "exceptions", rc, data)
}

func (s *Server) exception(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	d, err := s.dcm.Conflict(r.Context(), r.PathValue("id"))
	if err != nil {
		code := http.StatusBadGateway
		if dcmclient.NotFound(err) {
			code = http.StatusNotFound
		}
		s.render(w, r, code, "error", rc, map[string]any{"Title": "Exception not available", "Message": err.Error()})
		return
	}
	actions := []string{"reject"}
	if d.Conflict.Kind == "patient_id" {
		actions = []string{"correct_incoming", "correct_existing", "reject"}
	}
	s.render(w, r, http.StatusOK, "exception", rc, map[string]any{"D": d, "C": d.Conflict, "Actions": actions})
}

func digestOf(p dcmclient.Plan) string {
	parts := []string{p.ConflictID, p.Action, p.Summary, strconv.Itoa(p.ExistingInstances), strconv.Itoa(p.IncomingInstances)}
	for _, c := range append(append([]dcmclient.Change(nil), p.ExistingChanges...), p.IncomingChanges...) {
		parts = append(parts, c.Attribute, c.From, c.To)
	}
	return planDigest(parts...)
}

// review shows the warning page: exactly what will change, a reason field and
// the approve-edit confirmation (WT-SRS-009).
func (s *Server) review(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, action := r.PathValue("id"), r.URL.Query().Get("action")
	d, err := s.dcm.Conflict(r.Context(), id)
	if err != nil {
		s.render(w, r, http.StatusBadGateway, "error", rc, map[string]any{"Title": "Exception not available", "Message": err.Error()})
		return
	}
	p, err := s.dcm.Plan(r.Context(), id, action)
	if err != nil {
		s.render(w, r, http.StatusUnprocessableEntity, "error", rc, map[string]any{"Title": "This action isn't possible", "Message": err.Error()})
		return
	}
	_ = s.audit(r, rc.user.Username, "exception.review", id, "success", action)
	tok := s.ss.issueConfirm(rc.token, id, action, digestOf(p))
	s.render(w, r, http.StatusOK, "review", rc, map[string]any{"C": d.Conflict, "P": p, "Confirm": tok, "Action": action})
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, action := r.PathValue("id"), r.PostFormValue("action")
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	fail := func(code int, msg string) {
		_ = s.audit(r, rc.user.Username, "exception.resolve", id, "failure", action+": "+msg)
		s.render(w, r, code, "error", rc, map[string]any{"Title": "Edit not made", "Message": msg, "Back": "/exceptions/" + id})
	}
	digest, ok := s.ss.useConfirm(r.PostFormValue("confirm"), rc.token, id, action)
	if !ok {
		fail(http.StatusConflict, "The approval expired or was already used. Open the exception and review the change again.")
		return
	}
	if r.PostFormValue("approve") != "yes" {
		fail(http.StatusUnprocessableEntity, "Tick \"I approve this edit\" to confirm the change.")
		return
	}
	if reason == "" {
		fail(http.StatusUnprocessableEntity, "A reason is required; it is recorded in the audit trail.")
		return
	}
	// The plan must still be the one the administrator approved (new images
	// may have arrived since the warning page was shown).
	p, err := s.dcm.Plan(r.Context(), id, action)
	if err != nil {
		fail(http.StatusUnprocessableEntity, err.Error())
		return
	}
	if digestOf(p) != digest {
		fail(http.StatusConflict, "The change is no longer the one you approved (the exception was updated). Review it again.")
		return
	}
	// Record the intent first: if the audit log can't be written, don't edit.
	if err := s.audit(r, rc.user.Username, "exception.resolve.approved", id, "success", action+": "+reason); err != nil {
		fail(http.StatusInternalServerError, "The audit log can't be written, so the edit was not made.")
		return
	}
	res, err := s.dcm.Resolve(r.Context(), id, action, rc.user.Username, reason)
	if err != nil {
		fail(http.StatusBadGateway, err.Error())
		return
	}
	_ = s.audit(r, rc.user.Username, "exception.resolve", id, "success",
		fmt.Sprintf("%s: state=%s files_rewritten=%d", action, res.State, res.Rewritten))
	s.render(w, r, http.StatusOK, "resolved", rc, map[string]any{"R": res, "ID": id})
}

// ---------------------------------------------------------------- users / account / audit

func (s *Server) users(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.usersWith(w, r, rc, http.StatusOK, nil)
}

func (s *Server) usersWith(w http.ResponseWriter, r *http.Request, rc *reqCtx, code int, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	list, err := s.acct.List(r.Context())
	if err != nil {
		data["Error"] = err.Error()
	}
	data["Users"] = list
	data["Roles"] = []accounts.Role{accounts.Viewer, accounts.Operator, accounts.Admin}
	s.render(w, r, code, "users", rc, data)
}

func (s *Server) userCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	name, role := r.PostFormValue("username"), accounts.Role(r.PostFormValue("role"))
	u, err := s.acct.CreateUser(r.Context(), name, r.PostFormValue("password"), role)
	if err != nil {
		_ = s.audit(r, rc.user.Username, "user.create", name, "failure", err.Error())
		s.usersWith(w, r, rc, http.StatusUnprocessableEntity, map[string]any{"Error": err.Error()})
		return
	}
	_ = s.audit(r, rc.user.Username, "user.create", u.Username, "success", "role="+string(role))
	s.usersWith(w, r, rc, http.StatusOK, map[string]any{"Notice": "User " + u.Username + " created."})
}

func (s *Server) userUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	target, err := s.acct.Get(r.Context(), id)
	if err != nil {
		s.usersWith(w, r, rc, http.StatusNotFound, map[string]any{"Error": err.Error()})
		return
	}
	op := r.PostFormValue("op")
	var detail string
	switch op {
	case "role":
		role := accounts.Role(r.PostFormValue("role"))
		err, detail = s.acct.SetRole(r.Context(), id, role), "role="+string(role)
	case "disable":
		err = s.acct.SetDisabled(r.Context(), id, true)
	case "enable":
		err = s.acct.SetDisabled(r.Context(), id, false)
	case "password":
		err = s.acct.SetPassword(r.Context(), id, r.PostFormValue("password"))
	default:
		err = errors.New("unknown operation")
	}
	if err != nil {
		_ = s.audit(r, rc.user.Username, "user."+op, target.Username, "failure", err.Error())
		s.usersWith(w, r, rc, http.StatusUnprocessableEntity, map[string]any{"Error": err.Error()})
		return
	}
	_ = s.audit(r, rc.user.Username, "user."+op, target.Username, "success", detail)
	if id == rc.user.ID { // own account changed: sign in again
		s.ss.end(w, rc.token)
		http.Redirect(w, r, "/login?ended=1", http.StatusSeeOther)
		return
	}
	s.usersWith(w, r, rc, http.StatusOK, map[string]any{"Notice": "User " + target.Username + " updated; their sessions have ended."})
}

func (s *Server) account(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.render(w, r, http.StatusOK, "account", rc, nil)
}

func (s *Server) accountPassword(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if _, err := s.acct.Authenticate(r.Context(), rc.user.Username, r.PostFormValue("current")); err != nil {
		_ = s.audit(r, rc.user.Username, "password.change", rc.user.Username, "failure", "wrong current password")
		s.render(w, r, http.StatusUnprocessableEntity, "account", rc, map[string]any{"Error": "The current password is wrong."})
		return
	}
	if r.PostFormValue("password") != r.PostFormValue("password2") {
		s.render(w, r, http.StatusUnprocessableEntity, "account", rc, map[string]any{"Error": "The new passwords don't match."})
		return
	}
	if err := s.acct.SetPassword(r.Context(), rc.user.ID, r.PostFormValue("password")); err != nil {
		s.render(w, r, http.StatusUnprocessableEntity, "account", rc, map[string]any{"Error": err.Error()})
		return
	}
	_ = s.audit(r, rc.user.Username, "password.change", rc.user.Username, "success", "")
	u, _ := s.acct.Get(r.Context(), rc.user.ID)
	s.ss.endUser(rc.user.ID, rc.token)
	s.ss.setEpoch(rc.token, u.Epoch)
	s.render(w, r, http.StatusOK, "account", rc, map[string]any{"Notice": "Password changed. Other sessions have ended."})
}

func (s *Server) auditPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	data := map[string]any{}
	list, err := s.acct.AuditLog(r.Context(), 500)
	if err != nil {
		data["Error"] = err.Error()
	}
	data["Entries"] = list
	s.render(w, r, http.StatusOK, "audit", rc, data)
}
