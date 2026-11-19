// Package redact keeps configured secrets (bearer tokens, client private
// keys) out of everything LibreDVT writes (DVT-SRS-152): it scans bytes for
// every configured secret value, including its common encodings, and
// replaces each hit with "[REDACTED]".
package redact

import (
	"bytes"
	"encoding/base64"
	"encoding/pem"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Placeholder replaces every secret value.
const Placeholder = "[REDACTED]"

// MinLen is the shortest form the scanner looks for. Shorter fragments
// (for example a short PEM line) would redact ordinary text; secrets
// themselves are checked for a minimum length when they are loaded.
const MinLen = 8

// Set is a set of secret values to keep out of artefacts. The zero value
// is an empty set; a nil *Set is also empty. It is safe for concurrent use.
type Set struct {
	mu    sync.RWMutex
	forms map[string]bool
}

// Empty reports whether the set holds no secret.
func (s *Set) Empty() bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.forms) == 0
}

func (s *Set) add(v string) {
	if len(v) < MinLen {
		return
	}
	if s.forms == nil {
		s.forms = map[string]bool{}
	}
	s.forms[v] = true
}

// AddToken adds a token and its base64 and URL-encoded forms
// (evidence-model.md §5 step 4).
func (s *Set) AddToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := []byte(token)
	for _, v := range []string{
		token,
		base64.StdEncoding.EncodeToString(b),
		base64.RawStdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b),
		base64.RawURLEncoding.EncodeToString(b),
		url.QueryEscape(token),
		url.PathEscape(token),
	} {
		s.add(v)
	}
}

// AddPEM adds a PEM private key: the whole trimmed text, each block's
// base64 body (joined and line by line) and the raw DER in base64.
func (s *Set) AddPEM(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(strings.TrimSpace(string(data)))
	rest := data
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		der := base64.StdEncoding.EncodeToString(blk.Bytes)
		s.add(der)
		for i := 0; i < len(der); i += 64 {
			s.add(der[i:min(i+64, len(der))])
		}
	}
	// Body lines also when the file is not well-formed PEM.
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "-----") {
			s.add(line)
		}
	}
}

func (s *Set) sorted() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.forms))
	for v := range s.forms {
		out = append(out, v)
	}
	// Longest first, so a form that contains another is replaced whole.
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// Bytes returns b with every secret form replaced, and the number of
// replacements. b itself is not modified.
func (s *Set) Bytes(b []byte) ([]byte, int) {
	if s.Empty() {
		return b, 0
	}
	n := 0
	for _, v := range s.sorted() {
		c := bytes.Count(b, []byte(v))
		if c == 0 {
			continue
		}
		n += c
		b = bytes.ReplaceAll(b, []byte(v), []byte(Placeholder))
	}
	return b, n
}

// String is Bytes for strings.
func (s *Set) String(v string) (string, int) {
	b, n := s.Bytes([]byte(v))
	return string(b), n
}

// Contains reports whether v holds any secret form.
func (s *Set) Contains(v string) bool {
	_, n := s.String(v)
	return n > 0
}
