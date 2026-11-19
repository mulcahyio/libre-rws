// Package plan parses LibreDVT test plans strictly (DVT-SRS-040,
// DVT-SRS-142): unknown keys, unknown step types, fields of another step
// type and missing fields are errors before any step runs, so a typo can
// never silently skip a check.
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

// Step types (v0.1).
const (
	Validate = "validate"
	Echo     = "echo"
	Store    = "store"
	Find     = "find"
	Move     = "move"
	Reject   = "reject"
)

// Step types (v0.2, plan-schema.md §3.2).
const (
	Associate = "associate"
	TLSReject = "tls_reject"
	Qido      = "qido"
	Wado      = "wado"
	Stow      = "stow"
)

// Target protocols a step type needs (plan-schema.md §3.2).
const (
	ProtocolNone     = ""
	ProtocolDIMSE    = "dimse"
	ProtocolDICOMweb = "dicomweb"
	ProtocolAny      = "any" // either protocol, but the target must use TLS
)

// StepProtocol returns the target protocol a step type runs against.
func StepProtocol(typ string) string {
	switch typ {
	case Echo, Store, Find, Move, Reject, Associate:
		return ProtocolDIMSE
	case Qido, Wado, Stow:
		return ProtocolDICOMweb
	case TLSReject:
		return ProtocolAny
	}
	return ProtocolNone
}

// LabOnly reports whether a step type may only run against a lab target:
// store operations (C-STORE, STOW-RS) and the steps that pull or push
// image data (DVT-SRS-028, DVT-SRS-134).
func LabOnly(typ string) bool {
	switch typ {
	case Store, Move, Qido, Wado, Stow:
		return true
	}
	return false
}

// Plan is a parsed test plan.
type Plan struct {
	Name      string        `yaml:"name"`
	Target    string        `yaml:"target"`
	Timeout   time.Duration `yaml:"timeout"` // network operation timeout (default 30s)
	RawLimits *rawLimits    `yaml:"limits"`
	Steps     []Step        `yaml:"steps"`

	Limits Limits `yaml:"-"` // resolved limits (DVT-SRS-154)
	SHA256 string `yaml:"-"` // of the plan bytes (DVT-SRS-044)
	Dir    string `yaml:"-"` // directory that relative file patterns resolve against
}

// Step is one plan step.
type Step struct {
	ID       string   `yaml:"id"`
	Type     string   `yaml:"type"`
	Verifies []string `yaml:"verifies"`

	Files     []string `yaml:"files"`      // validate, store, move/wado (expected instances), stow: glob patterns
	Studies   []string `yaml:"studies"`    // find, move: Study Instance UIDs
	FromStore bool     `yaml:"from_store"` // find, move, qido, wado: use what earlier store/stow steps sent

	ComparePixels bool `yaml:"compare_pixels"` // move, wado

	CallingAET string `yaml:"calling_aet"` // reject: override the target's calling AE title
	CalledAET  string `yaml:"called_aet"`  // reject: override the target's called AE title

	// associate (DVT-SRS-090…-096)
	Contexts []Context   `yaml:"contexts"`
	Expect   *Expect     `yaml:"expect"`
	AsyncOps *AsyncOpsIn `yaml:"async_ops"`

	// tls_reject (DVT-SRS-105, -106)
	Mode       string `yaml:"mode"`
	TLSVersion string `yaml:"tls_version"`

	// qido, wado, stow (DVT-SRS-113…-136)
	Level             string            `yaml:"level"`
	Study             string            `yaml:"study"`
	Series            string            `yaml:"series"`
	Instance          string            `yaml:"instance"`
	Query             map[string]string `yaml:"query"`
	IncludeField      []string          `yaml:"includefield"`
	FuzzyMatching     bool              `yaml:"fuzzymatching"`
	Limit             *int              `yaml:"limit"`
	Offset            *int              `yaml:"offset"`
	ExpectUIDs        []string          `yaml:"expect_uids"`
	ExpectNone        bool              `yaml:"expect_none"`
	RequireAttributes []string          `yaml:"require_attributes"`
	TransferSyntax    string            `yaml:"transfer_syntax"`
	Metadata          bool              `yaml:"metadata"`
	CompareAttributes []string          `yaml:"compare_attributes"`
	ValidateObjects   bool              `yaml:"validate"`
	BatchSize         *int              `yaml:"batch_size"`
	AllowWarnings     bool              `yaml:"allow_warnings"`
	VerifyRetrieve    bool              `yaml:"verify_retrieve"`

	keys map[string]bool // keys present in the YAML step mapping
}

// Context is one proposed presentation context of an associate step.
type Context struct {
	AbstractSyntax       string   `yaml:"abstract_syntax"`
	TransferSyntaxes     []string `yaml:"transfer_syntaxes"`
	Expect               string   `yaml:"expect"` // accepted or rejected
	ExpectTransferSyntax string   `yaml:"expect_transfer_syntax"`
	Roles                *RolesIn `yaml:"roles"`
	ExpectRoles          *RolesIn `yaml:"expect_roles"`
}

// RolesIn is an SCP/SCU role selection.
type RolesIn struct {
	SCU bool `yaml:"scu"`
	SCP bool `yaml:"scp"`
}

// Expect holds the association-level expectations of an associate step.
type Expect struct {
	Association  string    `yaml:"association"` // accepted (default) or rejected
	Reject       *RejectIn `yaml:"reject"`
	MaxPDU       *int      `yaml:"max_pdu"`
	ImplClassUID string    `yaml:"impl_class_uid"`
	ImplVersion  string    `yaml:"impl_version"`
}

// RejectIn holds expected A-ASSOCIATE-RJ values (PS3.8 Table 9-21).
type RejectIn struct {
	Result int `yaml:"result"`
	Source int `yaml:"source"`
	Reason int `yaml:"reason"`
}

// AsyncOpsIn proposes an Asynchronous Operations Window.
type AsyncOpsIn struct {
	Invoked   int `yaml:"invoked"`
	Performed int `yaml:"performed"`
}

// Limits bound what a DICOMweb step reads (DVT-SRS-154, plan-schema.md
// §3.3). Reaching one fails the step.
type Limits struct {
	MaxResponseBytes    int64
	MaxParts            int64
	MaxPartBytes        int64
	MaxJSONBytes        int64
	MaxPages            int64
	MaxBodyExcerptBytes int64
}

type rawLimits struct {
	MaxResponseBytes    *int64 `yaml:"max_response_bytes"`
	MaxParts            *int64 `yaml:"max_parts"`
	MaxPartBytes        *int64 `yaml:"max_part_bytes"`
	MaxJSONBytes        *int64 `yaml:"max_json_bytes"`
	MaxPages            *int64 `yaml:"max_pages"`
	MaxBodyExcerptBytes *int64 `yaml:"max_body_excerpt_bytes"`
}

const (
	kib = int64(1) << 10
	mib = int64(1) << 20
	gib = int64(1) << 30
)

// DefaultLimits are the defaults of plan-schema.md §3.3.
func DefaultLimits() Limits {
	return Limits{MaxResponseBytes: 512 * mib, MaxParts: 1000, MaxPartBytes: 256 * mib, MaxJSONBytes: 32 * mib, MaxPages: 100, MaxBodyExcerptBytes: 64 * kib}
}

func (r *rawLimits) resolve(add func(string, ...any)) Limits {
	l := DefaultLimits()
	if r == nil {
		return l
	}
	set := func(name string, v *int64, dst *int64, lo, hi int64) {
		if v == nil {
			return
		}
		if *v < lo || *v > hi {
			add("plan: limits.%s must be between %d and %d", name, lo, hi)
			return
		}
		*dst = *v
	}
	set("max_response_bytes", r.MaxResponseBytes, &l.MaxResponseBytes, mib, 4*gib)
	set("max_parts", r.MaxParts, &l.MaxParts, 1, 10000)
	set("max_part_bytes", r.MaxPartBytes, &l.MaxPartBytes, kib, 2*gib)
	set("max_json_bytes", r.MaxJSONBytes, &l.MaxJSONBytes, 64*kib, 256*mib)
	set("max_pages", r.MaxPages, &l.MaxPages, 1, 1000)
	set("max_body_excerpt_bytes", r.MaxBodyExcerptBytes, &l.MaxBodyExcerptBytes, 0, mib)
	return l
}

var (
	idRe      = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	reqRe     = regexp.MustCompile(`^[A-Z][A-Z0-9]*-SRS-\d{3}$`)
	uidRe     = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
	keywordRe = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9]{0,63}|[0-9A-Fa-f]{8})$`)
)

// ValidUID reports whether s is a syntactically valid UID (PS3.5 §9).
func ValidUID(s string) bool { return len(s) <= 64 && uidRe.MatchString(s) }

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
	if err := p.recordKeys(data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	p.SHA256 = hex.EncodeToString(sum[:])
	p.Dir = dir
	if p.Timeout == 0 {
		p.Timeout = 30 * time.Second
	}
	return &p, p.check()
}

// recordKeys notes which keys each step mapping holds, so a field of
// another step type is rejected even when its value is the zero value.
func (p *Plan) recordKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("plan: not a mapping")
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "steps" {
			continue
		}
		seq := root.Content[i+1]
		if seq.Kind == yaml.AliasNode {
			seq = seq.Alias
		}
		if seq.Kind != yaml.SequenceNode {
			continue // empty steps: reported by check
		}
		if len(seq.Content) != len(p.Steps) {
			return fmt.Errorf("plan: steps could not be read")
		}
		for j, n := range seq.Content {
			if n.Kind == yaml.AliasNode {
				n = n.Alias
			}
			p.Steps[j].keys = map[string]bool{}
			for k := 0; k+1 < len(n.Content); k += 2 {
				p.Steps[j].keys[n.Content[k].Value] = true
			}
		}
	}
	return nil
}

// Fields allowed per step type besides id, type and verifies
// (plan-schema.md §3.2). v0.1 step types keep their v0.1 rules: they may
// hold any v0.1 field key, and a field with a value is checked by type.
var (
	v01Fields  = []string{"files", "studies", "from_store", "compare_pixels", "calling_aet", "called_aet"}
	stepFields = map[string][]string{
		Validate:  v01Fields,
		Echo:      v01Fields,
		Store:     v01Fields,
		Find:      v01Fields,
		Move:      v01Fields,
		Reject:    v01Fields,
		Associate: {"contexts", "expect", "async_ops"},
		TLSReject: {"mode", "tls_version"},
		Qido:      {"level", "study", "series", "query", "includefield", "fuzzymatching", "limit", "offset", "expect_uids", "from_store", "expect_none", "require_attributes"},
		Wado:      {"level", "study", "series", "instance", "from_store", "transfer_syntax", "compare_pixels", "metadata", "compare_attributes", "validate", "files"},
		Stow:      {"files", "study", "batch_size", "allow_warnings", "verify_retrieve"},
	}
)

func isV01(typ string) bool {
	switch typ {
	case Validate, Echo, Store, Find, Move, Reject:
		return true
	}
	return false
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
	p.Limits = p.RawLimits.resolve(add)
	if len(p.Steps) == 0 {
		add("plan: at least one step is required")
	}
	needsTarget := false
	seen := map[string]bool{}
	stored := false // an earlier store or stow step exists
	for i := range p.Steps {
		s := &p.Steps[i]
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
			if !ValidUID(u) {
				add("%s: %q is not a valid UID", where, u)
			}
		}
		for _, f := range s.Files {
			if _, err := filepath.Match(f, ""); err != nil {
				add("%s: bad file pattern %q", where, f)
			}
		}
		s.checkKeys(where, add)
		if isV01(s.Type) {
			s.checkV01Values(where, add)
		}
		if StepProtocol(s.Type) != ProtocolNone {
			needsTarget = true
		}
		switch s.Type {
		case Validate:
			if len(s.Files) == 0 {
				add("%s: validate needs files", where)
			}
		case Echo:
		case Store:
			stored = true
			if len(s.Files) == 0 {
				add("%s: store needs files", where)
			}
		case Find:
			if len(s.Studies) == 0 && !s.FromStore {
				add("%s: find needs studies or from_store: true", where)
			}
			if s.FromStore && !stored {
				add("%s: from_store needs an earlier store step", where)
			}
		case Move:
			switch {
			case s.FromStore && !stored:
				add("%s: from_store needs an earlier store step", where)
			case s.FromStore && (len(s.Files) > 0 || len(s.Studies) > 0):
				add("%s: use either from_store or files/studies", where)
			case !s.FromStore && len(s.Files) == 0:
				add("%s: move needs from_store: true or files (the instances expected back)", where)
			}
		case Reject:
			if s.CallingAET == "" && s.CalledAET == "" {
				add("%s: reject needs calling_aet or called_aet", where)
			}
			for _, a := range []string{s.CallingAET, s.CalledAET} {
				if a != "" && !ValidAET(a) {
					add("%s: %q is not a valid AE title", where, a)
				}
			}
		case Associate:
			s.checkAssociate(where, add)
		case TLSReject:
			s.checkTLSReject(where, add)
		case Qido:
			s.checkQido(where, stored, add)
		case Wado:
			s.checkWado(where, stored, add)
		case Stow:
			stored = true
			s.checkStow(where, add)
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

// checkKeys rejects every key that does not belong to the step's type,
// whatever its value (DVT-SRS-140, -142).
func (s *Step) checkKeys(where string, add func(string, ...any)) {
	allowed, known := stepFields[s.Type]
	if !known {
		return // unknown type: reported by check
	}
	ok := map[string]bool{"id": true, "type": true, "verifies": true}
	for _, f := range allowed {
		ok[f] = true
	}
	keys := make([]string, 0, len(s.keys))
	for k := range s.keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "<<" && isV01(s.Type) {
			continue // YAML merge key: v0.1 accepted it; its fields are checked by value
		}
		if !ok[k] {
			add("%s: %s is not allowed for a %s step", where, k, s.Type)
		}
	}
}

// checkV01Values keeps the v0.1 rule: a v0.1 field with a value is only
// allowed on the step types that use it.
func (s *Step) checkV01Values(where string, add func(string, ...any)) {
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
}

// checkAssociate applies plan-schema.md §3.2 "associate".
func (s *Step) checkAssociate(where string, add func(string, ...any)) {
	assoc := "accepted"
	if s.Expect != nil && s.Expect.Association != "" {
		assoc = s.Expect.Association
	}
	if assoc != "accepted" && assoc != "rejected" {
		add("%s: expect.association must be accepted or rejected", where)
	}
	if len(s.Contexts) < 1 || len(s.Contexts) > 128 {
		add("%s: associate needs 1-128 contexts", where)
	}
	for i, c := range s.Contexts {
		cw := fmt.Sprintf("%s: context %d", where, i+1)
		if !ValidUID(c.AbstractSyntax) {
			add("%s: abstract_syntax %q is not a valid UID", cw, c.AbstractSyntax)
		}
		if len(c.TransferSyntaxes) < 1 || len(c.TransferSyntaxes) > 16 {
			add("%s: needs 1-16 transfer_syntaxes", cw)
		}
		for _, ts := range c.TransferSyntaxes {
			if !ValidUID(ts) {
				add("%s: transfer syntax %q is not a valid UID", cw, ts)
			}
		}
		switch {
		case assoc == "rejected" && c.Expect != "":
			add("%s: expect is not allowed when the association is expected to be rejected", cw)
		case assoc == "accepted" && c.Expect != "accepted" && c.Expect != "rejected":
			add("%s: expect must be accepted or rejected", cw)
		}
		if c.ExpectTransferSyntax != "" {
			if c.Expect != "accepted" {
				add("%s: expect_transfer_syntax is only allowed with expect: accepted", cw)
			}
			if !contains(c.TransferSyntaxes, c.ExpectTransferSyntax) {
				add("%s: expect_transfer_syntax must be one of the proposed transfer_syntaxes", cw)
			}
		}
		if c.ExpectRoles != nil && c.Roles == nil {
			add("%s: expect_roles needs roles", cw)
		}
	}
	if assoc == "rejected" && (s.Expect == nil || s.Expect.Reject == nil) {
		add("%s: expect.reject is required when the association is expected to be rejected", where)
	}
	if e := s.Expect; e != nil {
		if assoc == "accepted" && e.Reject != nil {
			add("%s: expect.reject is only allowed with expect.association: rejected", where)
		}
		if e.Reject != nil && !validReject(*e.Reject) {
			add("%s: expect.reject result %d, source %d, reason %d is not a PS3.8 Table 9-21 combination", where, e.Reject.Result, e.Reject.Source, e.Reject.Reason)
		}
		if e.MaxPDU != nil && *e.MaxPDU < 0 {
			add("%s: expect.max_pdu must be 0 or more", where)
		}
		if e.ImplClassUID != "" && !ValidUID(e.ImplClassUID) {
			add("%s: expect.impl_class_uid is not a valid UID", where)
		}
		if len(e.ImplVersion) > 16 {
			add("%s: expect.impl_version must be at most 16 characters", where)
		}
	}
	if a := s.AsyncOps; a != nil && (a.Invoked < 0 || a.Invoked > 65535 || a.Performed < 0 || a.Performed > 65535) {
		add("%s: async_ops values must be 0-65535", where)
	}
}

// validReject checks A-ASSOCIATE-RJ result/source/reason (PS3.8 Table
// 9-21).
func validReject(r RejectIn) bool {
	if r.Result != 1 && r.Result != 2 {
		return false
	}
	switch r.Source {
	case 1: // service-user
		return r.Reason == 1 || r.Reason == 2 || r.Reason == 3 || r.Reason == 7
	case 2, 3: // service-provider (ACSE / presentation)
		return r.Reason == 1 || r.Reason == 2
	}
	return false
}

func (s *Step) checkTLSReject(where string, add func(string, ...any)) {
	switch s.Mode {
	case "plaintext", "no_client_cert":
		if s.keys["tls_version"] {
			add("%s: tls_version is only allowed with mode: tls_version", where)
		}
	case "tls_version":
		if s.TLSVersion != "1.0" && s.TLSVersion != "1.1" {
			add("%s: tls_version must be \"1.0\" or \"1.1\"", where)
		}
	case "":
		add("%s: tls_reject needs mode", where)
	default:
		add("%s: mode must be plaintext, tls_version or no_client_cert", where)
	}
}

// checkKeywords checks keyword syntax. Whether a keyword exists in the
// DICOM dictionary is checked by the worker before any request (card
// DVT-0.2/4); Go has no DICOM dictionary of its own (DVT-SRS-080).
func checkKeywords(where, field string, list []string, add func(string, ...any)) {
	for _, k := range list {
		if !keywordRe.MatchString(k) {
			add("%s: %s %q is not a DICOM keyword or 8-hex-digit tag", where, field, k)
		}
	}
}

func checkUIDField(where, field, v string, add func(string, ...any)) {
	if v != "" && !ValidUID(v) {
		add("%s: %s %q is not a valid UID", where, field, v)
	}
}

func (s *Step) checkQido(where string, stored bool, add func(string, ...any)) {
	checkUIDField(where, "study", s.Study, add)
	checkUIDField(where, "series", s.Series, add)
	switch s.Level {
	case "studies":
		if s.Study != "" || s.Series != "" {
			add("%s: study and series are not allowed with level: studies", where)
		}
	case "series":
		if s.Series != "" {
			add("%s: series is not allowed with level: series", where)
		}
	case "instances":
		if s.Series != "" && s.Study == "" {
			add("%s: series needs study", where)
		}
	case "":
		add("%s: qido needs level", where)
	default:
		add("%s: level must be studies, series or instances", where)
	}
	keys := make([]string, 0, len(s.Query))
	for k := range s.Query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	checkKeywords(where, "query key", keys, add)
	for _, k := range keys {
		v := s.Query[k]
		if len(v) > 1024 || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			add("%s: query value for %s must be at most 1024 printable characters", where, k)
		}
	}
	if contains(s.IncludeField, "all") {
		if len(s.IncludeField) != 1 {
			add("%s: includefield all must stand alone", where)
		}
	} else {
		checkKeywords(where, "includefield", s.IncludeField, add)
	}
	checkKeywords(where, "require_attributes", s.RequireAttributes, add)
	if s.Limit != nil && (*s.Limit < 1 || *s.Limit > 1000) {
		add("%s: limit must be 1-1000", where)
	}
	if s.Offset != nil {
		if s.Limit == nil {
			add("%s: offset needs limit", where)
		}
		if *s.Offset < 0 {
			add("%s: offset must be 0 or more", where)
		}
	}
	for _, u := range s.ExpectUIDs {
		checkUIDField(where, "expect_uids", u, add)
	}
	n := 0
	for _, set := range []bool{len(s.ExpectUIDs) > 0, s.FromStore, s.ExpectNone} {
		if set {
			n++
		}
	}
	if n != 1 {
		add("%s: qido needs exactly one of expect_uids, from_store or expect_none", where)
	}
	if s.FromStore && !stored {
		add("%s: from_store needs an earlier store or stow step", where)
	}
}

// lossyTS are transfer syntaxes that may lose pixel data; pixel comparison
// against them is meaningless (DVT-SRS-123).
var lossyTS = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range []string{"50", "51", "52", "53", "54", "55", "56", "59", "60", "61", "62", "63", "64", "81", "91", "93",
		"100", "101", "102", "103", "104", "105", "106", "107", "108", "112", "203"} {
		m["1.2.840.10008.1.2.4."+n] = true
	}
	return m
}()

func (s *Step) checkWado(where string, stored bool, add func(string, ...any)) {
	checkUIDField(where, "study", s.Study, add)
	checkUIDField(where, "series", s.Series, add)
	checkUIDField(where, "instance", s.Instance, add)
	if s.FromStore {
		if !stored {
			add("%s: from_store needs an earlier store or stow step", where)
		}
		if s.Level != "" || s.Study != "" || s.Series != "" || s.Instance != "" || len(s.Files) > 0 {
			add("%s: use either from_store or level/study/series/instance/files", where)
		}
	} else {
		switch s.Level {
		case "study":
			if s.Study == "" || s.Series != "" || s.Instance != "" {
				add("%s: level study needs study only", where)
			}
		case "series":
			if s.Study == "" || s.Series == "" || s.Instance != "" {
				add("%s: level series needs study and series", where)
			}
		case "instance":
			if s.Study == "" || s.Series == "" || s.Instance == "" {
				add("%s: level instance needs study, series and instance", where)
			}
		case "":
			add("%s: wado needs level or from_store: true", where)
		default:
			add("%s: level must be study, series or instance", where)
		}
		if len(s.Files) == 0 {
			add("%s: wado needs files (the instances expected back) or from_store: true", where)
		}
	}
	if s.TransferSyntax != "" && s.TransferSyntax != "*" && !ValidUID(s.TransferSyntax) {
		add("%s: transfer_syntax must be a UID or *", where)
	}
	if s.ComparePixels && lossyTS[s.TransferSyntax] {
		add("%s: compare_pixels is not allowed with the lossy transfer syntax %s", where, s.TransferSyntax)
	}
	if s.Metadata {
		if s.ComparePixels || s.TransferSyntax != "" || s.ValidateObjects {
			add("%s: metadata excludes compare_pixels, transfer_syntax and validate", where)
		}
		checkKeywords(where, "compare_attributes", s.CompareAttributes, add)
	} else if len(s.CompareAttributes) > 0 {
		add("%s: compare_attributes needs metadata: true", where)
	}
}

func (s *Step) checkStow(where string, add func(string, ...any)) {
	if len(s.Files) == 0 {
		add("%s: stow needs files", where)
	}
	checkUIDField(where, "study", s.Study, add)
	if s.BatchSize != nil && (*s.BatchSize < 1 || *s.BatchSize > 100) {
		add("%s: batch_size must be 1-100", where)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// CheckTarget checks every step against the target's protocol before any
// step runs (DVT-SRS-142): a DICOMweb step on a dimse target, a DIMSE step
// on a dicomweb target, or a tls_reject step on a target without TLS
// rejects the plan.
func (p *Plan) CheckTarget(name, protocol string, hasTLS bool) error {
	var errs []error
	for i, s := range p.Steps {
		want := StepProtocol(s.Type)
		where := fmt.Sprintf("step %d (%s)", i+1, s.ID)
		switch {
		case want == ProtocolNone:
		case want == ProtocolAny:
			if !hasTLS {
				errs = append(errs, fmt.Errorf("%s: a %s step needs a target with TLS; %s has none", where, s.Type, name))
			}
		case want != protocol:
			errs = append(errs, fmt.Errorf("%s: a %s step needs a %s target; %s is %s", where, s.Type, want, name, protocol))
		}
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
