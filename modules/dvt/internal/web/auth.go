// Package web is LibreDVT's web interface (DVT-SRS-060…063, DVT-SRS-066).
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	cookieName  = "libredvt_session"
	loginWindow = time.Minute
	loginMax    = 5 // failed logins per IP per window (DVT-SRS-062)
)

type session struct {
	csrf     string
	lastSeen time.Time
}

// auth holds sessions and the login rate limiter (DVT-SRS-061, -062).
type auth struct {
	password []byte
	idle     time.Duration
	secure   bool

	mu       sync.Mutex
	sessions map[string]*session
	failures map[string][]time.Time
	now      func() time.Time
}

func newAuth(password string, idle time.Duration, secure bool) *auth {
	if idle <= 0 {
		idle = 30 * time.Minute
	}
	return &auth{password: []byte(password), idle: idle, secure: secure,
		sessions: map[string]*session{}, failures: map[string][]time.Time{}, now: time.Now}
}

func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limited reports whether ip has used up its failed login attempts.
func (a *auth) limited(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cut := a.now().Add(-loginWindow)
	var keep []time.Time
	for _, t := range a.failures[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	a.failures[ip] = keep
	return len(keep) >= loginMax
}

// login checks the password and returns a new session token, or "".
func (a *auth) login(ip, password string) string {
	if subtle.ConstantTimeCompare([]byte(password), a.password) != 1 {
		a.mu.Lock()
		a.failures[ip] = append(a.failures[ip], a.now())
		a.mu.Unlock()
		return ""
	}
	t := token()
	a.mu.Lock()
	a.sessions[t] = &session{csrf: token(), lastSeen: a.now()}
	delete(a.failures, ip)
	a.mu.Unlock()
	return t
}

// session returns the live session for the request, refreshing its idle
// timer, or nil.
func (a *auth) session(r *http.Request) (string, *session) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[c.Value]
	if s == nil {
		return "", nil
	}
	if a.now().Sub(s.lastSeen) > a.idle {
		delete(a.sessions, c.Value)
		return "", nil
	}
	s.lastSeen = a.now()
	return c.Value, s
}

func (a *auth) logout(t string) {
	a.mu.Lock()
	delete(a.sessions, t)
	a.mu.Unlock()
}

func (a *auth) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true,
		Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

type ctxKey struct{}

// require wraps a handler: no session → login page (or 401 for non-GET);
// POST without the session's CSRF token → 403.
func (a *auth) require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, s := a.session(r)
		if s == nil {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			got := r.Header.Get("X-CSRF-Token")
			if got == "" {
				got = csrfFromForm(r)
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.csrf)) != 1 {
				http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, s)))
	})
}

func csrfFromForm(r *http.Request) string {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return ""
		}
	}
	return r.FormValue("csrf")
}

func csrfOf(r *http.Request) string {
	if s, ok := r.Context().Value(ctxKey{}).(*session); ok {
		return s.csrf
	}
	return ""
}
