package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/redact"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Target protocols (DVT-SRS-110).
const (
	DIMSE    = "dimse"
	DICOMweb = "dicomweb"
)

// Auth types (decision D2: no other type in v0.2).
const (
	AuthNone   = "none"
	AuthBearer = "bearer"
)

// MinTokenLen is the shortest bearer token LibreDVT accepts: shorter
// values cannot be redacted reliably (they would match ordinary text).
const MinTokenLen = 16

// Auth is a DICOMweb target's authentication. The token is a reference to
// a file, never the value (DVT-SRS-141).
type Auth struct {
	Type      string `json:"type,omitempty" yaml:"type"`
	TokenFile string `json:"token_file,omitempty" yaml:"token_file"`
}

// TLS is a target's TLS configuration (plan-schema.md §2.4). There is no
// field that turns verification off or allows TLS below 1.2 (DVT-SRS-103).
type TLS struct {
	MinVersion     string `json:"min_version,omitempty" yaml:"min_version"`
	CAFile         string `json:"ca_file,omitempty" yaml:"ca_file"`
	ServerName     string `json:"server_name,omitempty" yaml:"server_name"`
	ClientCertFile string `json:"client_cert_file,omitempty" yaml:"client_cert_file"`
	ClientKeyFile  string `json:"client_key_file,omitempty" yaml:"client_key_file"`
	MoveClientCert string `json:"move_client_cert,omitempty" yaml:"move_client_cert"`
}

// Target is a system under test (DVT-SRS-020, DVT-SRS-110, DVT-SRS-140).
type Target struct {
	Name       string `json:"name" yaml:"name"`
	Class      string `json:"class" yaml:"class"`
	Protocol   string `json:"protocol,omitempty" yaml:"protocol"`
	Host       string `json:"host,omitempty" yaml:"host"`
	Port       int    `json:"port,omitempty" yaml:"port"`
	CalledAET  string `json:"called_aet,omitempty" yaml:"called_aet"`
	CallingAET string `json:"calling_aet,omitempty" yaml:"calling_aet"`
	BaseURL    string `json:"base_url,omitempty" yaml:"base_url"`
	Auth       *Auth  `json:"auth,omitempty" yaml:"auth"`
	TLS        *TLS   `json:"tls,omitempty" yaml:"tls"`
}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	hostRe = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
)

func validHost(h string) bool {
	if h == "" || len(h) > 253 || strings.HasPrefix(h, "-") {
		return false
	}
	return net.ParseIP(h) != nil || hostRe.MatchString(h)
}

// IsLoopback reports whether host is localhost or a loopback address.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Normalize fills defaults and checks the target against the schema of
// plan-schema.md §2. A missing class means production (DVT-SRS-020); a
// missing protocol means dimse, so v0.1 records load unchanged
// (DVT-SRS-110). Fields of the other protocol are errors (DVT-SRS-140).
func (t *Target) Normalize() error {
	t.Name = strings.TrimSpace(t.Name)
	t.Host = strings.TrimSpace(t.Host)
	t.BaseURL = strings.TrimSpace(t.BaseURL)
	t.Class = strings.ToLower(strings.TrimSpace(t.Class))
	if t.Class == "" {
		t.Class = Production
	}
	t.Protocol = strings.ToLower(strings.TrimSpace(t.Protocol))
	if t.Protocol == "" {
		t.Protocol = DIMSE
	}
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !nameRe.MatchString(t.Name) {
		add("name must be 1-64 letters, digits, '.', '_' or '-'")
	}
	if t.Class != Lab && t.Class != Production {
		add("class must be lab or production")
	}
	switch t.Protocol {
	case DIMSE:
		t.normalizeDIMSE(add)
	case DICOMweb:
		t.normalizeDICOMweb(add)
	default:
		add("protocol must be dimse or dicomweb")
	}
	if t.TLS != nil {
		t.TLS.normalize(t.Protocol, add)
	}
	return errors.Join(errs...)
}

func (t *Target) normalizeDIMSE(add func(string, ...any)) {
	if t.Host == "" || len(t.Host) > 253 || strings.ContainsAny(t.Host, " \t/\\@") {
		add("host must be a host name or IP address")
	} else if strings.HasPrefix(t.Host, "-") {
		add("host must not start with '-'")
	} else if !validHost(t.Host) {
		add("host must be a host name or IP address")
	}
	if t.Port < 1 || t.Port > 65535 {
		add("port must be 1-65535")
	}
	if !plan.ValidAET(t.CalledAET) {
		add("called AE title must be 1-16 printable characters")
	}
	if !plan.ValidAET(t.CallingAET) {
		add("calling AE title must be 1-16 printable characters")
	}
	if t.BaseURL != "" {
		add("base_url is not allowed on a dimse target")
	}
	if t.Auth != nil {
		add("auth is not allowed on a dimse target")
	}
}

func (t *Target) normalizeDICOMweb(add func(string, ...any)) {
	for f, set := range map[string]bool{"host": t.Host != "", "port": t.Port != 0, "called_aet": t.CalledAET != "", "calling_aet": t.CallingAET != ""} {
		if set {
			add("%s is not allowed on a dicomweb target", f)
		}
	}
	u, err := checkBaseURL(t.BaseURL)
	if err != nil {
		add("base_url: %v", err)
	}
	if t.Auth == nil {
		t.Auth = &Auth{}
	}
	t.Auth.Type = strings.ToLower(strings.TrimSpace(t.Auth.Type))
	if t.Auth.Type == "" {
		t.Auth.Type = AuthNone
	}
	switch t.Auth.Type {
	case AuthNone:
		if t.Auth.TokenFile != "" {
			add("auth.token_file is only allowed with auth.type bearer")
		}
	case AuthBearer:
		if !filepath.IsAbs(t.Auth.TokenFile) {
			add("auth.token_file must be an absolute path")
		}
		// DVT-SRS-112: never send a token in clear text off the host.
		if u != nil && u.Scheme == "http" && !IsLoopback(u.Hostname()) {
			add("a bearer token over http is only allowed to a loopback host; use https")
		}
	default:
		add("auth.type must be none or bearer")
	}
	if t.TLS != nil && u != nil && u.Scheme != "https" {
		add("tls is only allowed with an https base_url")
	}
	if u != nil {
		t.BaseURL = strings.TrimRight(u.String(), "/")
	}
}

// checkBaseURL applies DVT-SRS-110: http or https, a host, an optional
// port and path, no user-info, query or fragment.
func checkBaseURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("required")
	}
	if strings.ContainsAny(raw, "#?") {
		return nil, errors.New("must not have a query or fragment")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("scheme must be http or https")
	}
	if u.Opaque != "" || u.Host == "" {
		return nil, errors.New("must have a host")
	}
	if u.User != nil {
		return nil, errors.New("must not hold user information")
	}
	if !validHost(u.Hostname()) {
		return nil, errors.New("host must be a host name or IP address")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("port must be 1-65535")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("port must be 1-65535")
	}
	return u, nil
}

func (c *TLS) normalize(protocol string, add func(string, ...any)) {
	if c.MinVersion == "" {
		c.MinVersion = "1.2"
	}
	if c.MinVersion != "1.2" && c.MinVersion != "1.3" {
		add("tls.min_version must be \"1.2\" or \"1.3\"")
	}
	for f, p := range map[string]string{"tls.ca_file": c.CAFile, "tls.client_cert_file": c.ClientCertFile, "tls.client_key_file": c.ClientKeyFile} {
		if p != "" && !filepath.IsAbs(p) {
			add("%s must be an absolute path", f)
		}
	}
	if (c.ClientCertFile == "") != (c.ClientKeyFile == "") {
		add("tls.client_cert_file and tls.client_key_file must be given together")
	}
	if c.ServerName != "" && !validHost(c.ServerName) {
		add("tls.server_name must be a host name or IP address")
	}
	switch {
	case protocol != DIMSE && c.MoveClientCert != "":
		add("tls.move_client_cert is only allowed on a dimse target")
	case protocol == DIMSE && c.MoveClientCert == "":
		c.MoveClientCert = "required"
	case protocol == DIMSE && c.MoveClientCert != "required" && c.MoveClientCert != "optional":
		add("tls.move_client_cert must be required or optional")
	}
}

// Proto is the target's protocol; a record without one is dimse
// (DVT-SRS-110).
func (t Target) Proto() string {
	if t.Protocol == "" {
		return DIMSE
	}
	return t.Protocol
}

// HasTLS reports whether connections to the target use TLS: a tls block
// on a DIMSE target, or an https base URL.
func (t Target) HasTLS() bool {
	if t.Proto() == DICOMweb {
		return strings.HasPrefix(t.BaseURL, "https://")
	}
	return t.TLS != nil
}

// Endpoint is host:port or the base URL, never credentials.
func (t Target) Endpoint() string {
	if t.Proto() == DICOMweb {
		return t.BaseURL
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// Info is the target as recorded in step evidence.
func (t Target) Info() result.TargetInfo {
	return result.TargetInfo{Name: t.Name, Class: t.Class, Protocol: t.Proto(), Endpoint: t.Endpoint()}
}

// Describe returns a one-line description for reports. A v0.1 target
// (DIMSE without TLS) reads exactly as in v0.1.
func (t Target) Describe() string {
	if t.Proto() == DICOMweb {
		auth := AuthNone
		if t.Auth != nil && t.Auth.Type != "" {
			auth = t.Auth.Type
		}
		return fmt.Sprintf("%s (dicomweb %s, auth %s, class %s)", t.Name, t.BaseURL, auth, t.Class)
	}
	tls := ""
	if t.TLS != nil {
		tls = ", TLS " + t.TLS.MinVersion + "+"
	}
	return fmt.Sprintf("%s (%s:%d, called AE %s, calling AE %s%s, class %s)", t.Name, t.Host, t.Port, t.CalledAET, t.CallingAET, tls, t.Class)
}

// --- strict decoding (DVT-SRS-140) ---

// DecodeTargetsJSON decodes a registry file strictly: unknown fields,
// wrong types and invalid records are errors naming the target and field.
func DecodeTargetsJSON(data []byte) ([]Target, error) {
	var ts []Target
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ts); err != nil {
		return nil, fmt.Errorf("targets: %w", err)
	}
	if dec.More() {
		return nil, errors.New("targets: more than one JSON document")
	}
	return ts, checkAll(ts)
}

// ParseTargetsFile decodes a YAML target file (`targets:` list) with the
// same schema as the registry (plan-schema.md §2).
func ParseTargetsFile(data []byte) ([]Target, error) {
	var f struct {
		Targets []Target `yaml:"targets"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("target file is empty")
		}
		return nil, fmt.Errorf("target file: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("target file: more than one YAML document")
	}
	if len(f.Targets) == 0 {
		return nil, errors.New("target file: no targets")
	}
	for i := range f.Targets {
		t := &f.Targets[i]
		if t.Proto() == DIMSE && t.CallingAET == "" {
			t.CallingAET = "LIBREDVT" // plan-schema.md §2.2 default
		}
	}
	return f.Targets, checkAll(f.Targets)
}

func checkAll(ts []Target) error {
	var errs []error
	seen := map[string]bool{}
	for i := range ts {
		if err := ts[i].Normalize(); err != nil {
			errs = append(errs, fmt.Errorf("target %d (%s): %w", i+1, ts[i].Name, err))
		}
		if seen[ts[i].Name] {
			errs = append(errs, fmt.Errorf("target %d (%s): duplicate name", i+1, ts[i].Name))
		}
		seen[ts[i].Name] = true
	}
	return errors.Join(errs...)
}

// --- secret and certificate files (DVT-SRS-141) ---

// tokenRe is the RFC 6750 b64token syntax. It also guarantees that JSON,
// HTML and XML escaping never changes a token, so the artefact scan finds
// it in every format (DVT-SRS-152).
var tokenRe = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)

// LoadSecrets checks every file the target references and adds the secret
// values (bearer token, client private key) to into, which is used only to
// keep them out of artefacts (DVT-SRS-152); the worker reads the files
// itself (DVT-SRS-153). A missing, empty or non-regular file is an error,
// so the step fails before any connection (DVT-SRS-141). When secretsDir
// is set (web UI), every referenced file must lie under it
// (plan-schema.md §1). Errors name the field and never the contents.
func (t Target) LoadSecrets(secretsDir string, into *redact.Set) error {
	type ref struct {
		field, path string
		secret      int // 0 = not secret, 1 = token, 2 = private key
	}
	var refs []ref
	if t.Auth != nil && t.Auth.TokenFile != "" {
		refs = append(refs, ref{"auth.token_file", t.Auth.TokenFile, 1})
	}
	if t.TLS != nil {
		refs = append(refs, ref{"tls.ca_file", t.TLS.CAFile, 0}, ref{"tls.client_cert_file", t.TLS.ClientCertFile, 0}, ref{"tls.client_key_file", t.TLS.ClientKeyFile, 2})
	}
	for _, r := range refs {
		if r.path == "" {
			continue
		}
		b, err := readRef(r.path, secretsDir)
		if err != nil {
			return fmt.Errorf("%s: %w", r.field, err)
		}
		switch r.secret {
		case 1:
			tok := strings.TrimRight(string(b), "\r\n")
			if strings.ContainsAny(tok, "\r\n") {
				return fmt.Errorf("%s: the token must be on one line", r.field)
			}
			if len(tok) < MinTokenLen || !tokenRe.MatchString(tok) {
				return fmt.Errorf("%s: the token must be at least %d characters of A-Z a-z 0-9 - . _ ~ + / (and trailing =)", r.field, MinTokenLen)
			}
			into.AddToken(tok)
		case 2:
			into.AddPEM(b)
		}
	}
	return nil
}

// maxRefBytes bounds a referenced certificate, key or token file.
const maxRefBytes = 1 << 20

func readRef(path, secretsDir string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("must be an absolute path")
	}
	clean := filepath.Clean(path)
	if secretsDir != "" {
		rel, err := filepath.Rel(filepath.Clean(secretsDir), clean)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("must lie under the secrets directory %s", secretsDir)
		}
	}
	info, err := os.Stat(clean)
	if err != nil {
		return nil, errors.New("file not found or not readable")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Size() == 0 {
		return nil, errors.New("file is empty")
	}
	if info.Size() > maxRefBytes {
		return nil, errors.New("file is larger than 1 MiB")
	}
	b, err := os.ReadFile(clean) // #nosec G304 -- path from the target's configuration, checked above
	if err != nil {
		return nil, errors.New("file not readable")
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New("file is empty")
	}
	return b, nil
}
