// Package plan parses LibreDVT test plans strictly (DVT-SRS-040): unknown
// keys, unknown step types and missing fields are errors before any step
// runs, so a typo can never silently skip a check.
package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Step types.
const (
	Validate = "validate"
	Echo     = "echo"
	Store    = "store"
	Find     = "find"
	Move     = "move"
	Reject   = "reject"
)

// Plan is a parsed test plan.
type Plan struct {
	Name    string        `yaml:"name"`
	Target  string        `yaml:"target"`
	Timeout time.Duration `yaml:"timeout"` // network operation timeout (default 30s)
	Steps   []Step        `yaml:"steps"`

	SHA256 string `yaml:"-"` // of the plan bytes (DVT-SRS-044)
	Dir    string `yaml:"-"` // directory that relative file patterns resolve against
}

// Step is one plan step.
type Step struct {
	ID       string   `yaml:"id"`
	Type     string   `yaml:"type"`
	Verifies []string `yaml:"verifies"`

	Files     []string `yaml:"files"`      // validate, store, move (expected instances): glob patterns
	Studies   []string `yaml:"studies"`    // find, move: Study Instance UIDs
	FromStore bool     `yaml:"from_store"` // find, move: use the studies/instances sent by earlier store steps

	ComparePixels bool `yaml:"compare_pixels"` // move

	CallingAET string `yaml:"calling_aet"` // reject: override the target's calling AE title
	CalledAET  string `yaml:"called_aet"`  // reject: override the target's called AE title
}

var (
	idRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	reqRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-SRS-\d{3}$`)
	uidRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
)

// ValidAET reports whether s is a usable AE title (1–16 characters, no
// backslash or control characters, not only spaces).
func ValidAET(s string) bool {
	if len(s) > 16 || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r > 0x7e || r == '\\' {
			return false
		}
	}
	return true
}

// Parse decodes and checks a plan. dir is where relative file patterns
// resolve (usually the plan file's directory).
func Parse(data []byte, dir string) (*Plan, error) {
	var p Plan
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("plan is empty")
		}
		return nil, fmt.Errorf("plan: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("plan: more than one YAML document")
	}
	sum := sha256.Sum256(data)
	p.SHA256 = hex.EncodeToString(sum[:])
	p.Dir = dir
	if p.Timeout == 0 {
		p.Timeout = 30 * time.Second
	}
	return &p, p.check()
}

func (p *Plan) check() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if p.Name == "" {
		add("plan: name is required")
	}
	if p.Timeout < time.Second || p.Timeout > 30*time.Minute {
		add("plan: timeout must be between 1s and 30m")
	}
	if len(p.Steps) == 0 {
		add("plan: at least one step is required")
	}
	needsTarget := false
	seen := map[string]bool{}
	stored := false
	for i, s := range p.Steps {
		where := fmt.Sprintf("step %d (%s)", i+1, s.ID)
		if !idRe.MatchString(s.ID) {
			add("step %d: id must be 1-64 letters, digits, '.', '_' or '-'", i+1)
		}
		if seen[s.ID] {
			add("%s: duplicate id", where)
		}
		seen[s.ID] = true
		for _, r := range s.Verifies {
			if !reqRe.MatchString(r) {
				add("%s: verifies %q is not a requirement ID like DCM-SRS-001", where, r)
			}
		}
		for _, u := range s.Studies {
			if !uidRe.MatchString(u) || len(u) > 64 {
				add("%s: %q is not a valid UID", where, u)
			}
		}
		for _, f := range s.Files {
			if _, err := filepath.Match(f, ""); err != nil {
				add("%s: bad file pattern %q", where, f)
			}
		}
		only := func(field string, set bool, allowed ...string) {
			if !set {
				return
			}
			for _, a := range allowed {
				if s.Type == a {
					return
				}
			}
			add("%s: %s is not allowed for a %s step", where, field, s.Type)
		}
		only("files", len(s.Files) > 0, Validate, Store, Move)
		only("studies", len(s.Studies) > 0, Find, Move)
		only("from_store", s.FromStore, Find, Move)
		only("compare_pixels", s.ComparePixels, Move)
		only("calling_aet", s.CallingAET != "", Reject)
		only("called_aet", s.CalledAET != "", Reject)
		switch s.Type {
		case Validate:
			if len(s.Files) == 0 {
				add("%s: validate needs files", where)
			}
		case Echo:
			needsTarget = true
		case Store:
			needsTarget = true
			stored = true
			if len(s.Files) == 0 {
				add("%s: store needs files", where)
			}
		case Find:
			needsTarget = true
			if len(s.Studies) == 0 && !s.FromStore {
				add("%s: find needs studies or from_store: true", where)
			}
			if s.FromStore && !stored {
				add("%s: from_store needs an earlier store step", where)
			}
		case Move:
			needsTarget = true
			switch {
			case s.FromStore && !stored:
				add("%s: from_store needs an earlier store step", where)
			case s.FromStore && (len(s.Files) > 0 || len(s.Studies) > 0):
				add("%s: use either from_store or files/studies", where)
			case !s.FromStore && len(s.Files) == 0:
				add("%s: move needs from_store: true or files (the instances expected back)", where)
			}
		case Reject:
			needsTarget = true
			if s.CallingAET == "" && s.CalledAET == "" {
				add("%s: reject needs calling_aet or called_aet", where)
			}
			for _, a := range []string{s.CallingAET, s.CalledAET} {
				if a != "" && !ValidAET(a) {
					add("%s: %q is not a valid AE title", where, a)
				}
			}
		case "":
			add("%s: type is required", where)
		default:
			add("%s: unknown step type %q", where, s.Type)
		}
	}
	if needsTarget && p.Target == "" {
		add("plan: target is required for network steps")
	}
	return errors.Join(errs...)
}

// ResolveFiles expands a step's file patterns relative to the plan
// directory. Every pattern must match at least one file, so a wrong path
// fails the step instead of testing nothing.
func (p *Plan) ResolveFiles(patterns []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, pat := range patterns {
		full := pat
		if !filepath.IsAbs(pat) {
			full = filepath.Join(p.Dir, pat)
		}
		m, err := filepath.Glob(full)
		if err != nil {
			return nil, err
		}
		if len(m) == 0 {
			return nil, fmt.Errorf("no file matches %q", pat)
		}
		sort.Strings(m)
		for _, f := range m {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out, nil
}
