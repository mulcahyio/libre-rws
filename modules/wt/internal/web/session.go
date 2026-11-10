package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"sync"
	"time"
)

// Sessions, CSRF tokens, login rate limiting and edit confirmations
// (WT-SRS-004, WT-SRS-005, WT-SRS-009).

const (
	cookieName      = "librewt_session"
	loginWindow     = 15 * time.Minute
	loginMaxPerIP   = 10 // failed logins per IP per window
	loginMaxPerUser = 5  // failed logins per username per window
	confirmTTL      = 10 * time.Minute
)

type session struct {
	userID   int64
	epoch    int64
	csrf     string
	created  time.Time
	lastSeen time.Time
}

// confirmation is a single-use token issued with the warning page of an edit.
// The resolve request must present it, from the same session, for the same
// conflict, action and plan, before it expires.
type confirmation struct {
	session  string
	conflict string
	action   string
	digest   string
	expires  time.Time
}

type sessions struct {
	idle, absolute time.Duration
	secure         bool

	mu       sync.Mutex
	byToken  map[string]*session
	failures map[string][]time.Time // "ip:<addr>" / "user:<name>"
	confirms map[string]confirmation
	now      func() time.Time
}

func newSessions(idle, absolute time.Duration, secure bool) *sessions {
	if idle <= 0 {
		idle = 30 * time.Minute
	}
	if absolute <= 0 {
		absolute = 12 * time.Hour
	}
	return &sessions{idle: idle, absolute: absolute, secure: secure, byToken: map[string]*session{},
		failures: map[string][]time.Time{}, confirms: map[string]confirmation{}, now: time.Now}
}

func randToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
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

func (s *sessions) prune(key string) []time.Time {
	cut := s.now().Add(-loginWindow)
	var keep []time.Time
	for _, t := range s.failures[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(s.failures, key)
	} else {
		s.failures[key] = keep
	}
	return keep
}

// limited reports whether more login attempts from ip or for user must wait.
func (s *sessions) limited(ip, user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.prune("ip:"+ip)) >= loginMaxPerIP || len(s.prune("user:"+user)) >= loginMaxPerUser
}

func (s *sessions) fail(ip, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures["ip:"+ip] = append(s.failures["ip:"+ip], s.now())
	s.failures["user:"+user] = append(s.failures["user:"+user], s.now())
}

// start creates a session and sets its cookie.
func (s *sessions) start(w http.ResponseWriter, userID, epoch int64, user string) {
	t := randToken()
	now := s.now()
	s.mu.Lock()
	s.byToken[t] = &session{userID: userID, epoch: epoch, csrf: randToken(), created: now, lastSeen: now}
	delete(s.failures, "user:"+user)
	s.mu.Unlock()
	// #nosec G124 -- Secure is set whenever Libre WT serves HTTPS; plain HTTP is for localhost only and logs a warning at startup
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: t, Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(s.absolute.Seconds())})
}

// get returns the live session for r, refreshing its idle timer.
func (s *sessions) get(r *http.Request) (string, *session) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.byToken[c.Value]
	if ss == nil {
		return "", nil
	}
	now := s.now()
	if now.Sub(ss.lastSeen) > s.idle || now.Sub(ss.created) > s.absolute {
		delete(s.byToken, c.Value)
		return "", nil
	}
	ss.lastSeen = now
	cp := *ss
	return c.Value, &cp
}

func (s *sessions) end(w http.ResponseWriter, token string) {
	s.mu.Lock()
	delete(s.byToken, token)
	for k, c := range s.confirms {
		if c.session == token {
			delete(s.confirms, k)
		}
	}
	s.mu.Unlock()
	// #nosec G124 -- see start(): Secure follows the HTTPS setting
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

// endUser ends every session of a user (used after their own password change
// so other browsers are logged out; epoch checks cover the rest).
func (s *sessions) endUser(userID int64, except string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for t, ss := range s.byToken {
		if ss.userID == userID && t != except {
			delete(s.byToken, t)
		}
	}
}

func (s *sessions) setEpoch(token string, epoch int64) {
	s.mu.Lock()
	if ss := s.byToken[token]; ss != nil {
		ss.epoch = epoch
	}
	s.mu.Unlock()
}

func csrfOK(ss *session, got string) bool {
	return ss != nil && got != "" && subtle.ConstantTimeCompare([]byte(ss.csrf), []byte(got)) == 1
}

// planDigest fingerprints a plan so an approval only applies to the changes
// the administrator saw.
func planDigest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *sessions) issueConfirm(session, conflict, action, digest string) string {
	t := randToken()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, c := range s.confirms {
		if now.After(c.expires) {
			delete(s.confirms, k)
		}
	}
	s.confirms[t] = confirmation{session: session, conflict: conflict, action: action, digest: digest, expires: now.Add(confirmTTL)}
	return t
}

// useConfirm consumes a confirmation token; it returns the plan digest it
// was issued for, and false when the token is unknown, expired, already used
// or belongs to another session, conflict or action.
func (s *sessions) useConfirm(token, session, conflict, action string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.confirms[token]
	delete(s.confirms, token)
	if !ok || s.now().After(c.expires) || c.session != session || c.conflict != conflict || c.action != action {
		return "", false
	}
	return c.digest, true
}
