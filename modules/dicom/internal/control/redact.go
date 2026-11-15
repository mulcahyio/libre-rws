package control

import (
	"bytes"
	"encoding/json"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/mulcahyio/libre-rws/platform/config"
)

// Redaction (DCM-SRS-082, HAZ-009). Two independent controls keep secrets
// out of control responses:
//
//  1. Settings are shown from an allow-list (settingClass). A setting that is
//     not on the list is returned as redacted, so a secret setting added
//     later is hidden until someone decides it is safe.
//  2. Every response body passes scrub before it is written: known secret
//     values and credentials inside URLs are replaced, wherever they appear
//     (a readiness error, a log line, an error text).

// How a listed setting may be shown.
const (
	classPlain = "plain" // value as it is
	classURL   = "url"   // URL or comma-separated URLs: user, password and query removed
	classDSN   = "dsn"   // database DSN: reduced to scheme, host and path
)

// settingClass is the allow-list of settings that may be shown. Everything
// else (today: admin.token, storage.s3.access_key, storage.s3.secret_key) is
// redacted. File paths are shown; file content never is.
var settingClass = map[string]string{
	"admin.addr":             classPlain,
	"admin.log_level":        classPlain,
	"admin.log_format":       classPlain,
	"admin.shutdown_timeout": classPlain,
	"admin.node_id":          classPlain,
	"admin.token_file":       classPlain,
	"admin.tls_cert_file":    classPlain,
	"admin.tls_key_file":     classPlain,

	"dicom.ae_title":                           classPlain,
	"dicom.listen":                             classPlain,
	"dicom.allow_any_called_ae_title":          classPlain,
	"dicom.accept_any_sop_class":               classPlain,
	"dicom.max_associations":                   classPlain,
	"dicom.max_pdu_length":                     classPlain,
	"dicom.handshake_timeout":                  classPlain,
	"dicom.idle_timeout":                       classPlain,
	"dicom.find_limit":                         classPlain,
	"dicom.get_max_bytes":                      classPlain,
	"dicom.tls_cert_file":                      classPlain,
	"dicom.tls_key_file":                       classPlain,
	"dicom.remote_aes":                         classPlain,
	"dicom.allowed_aes":                        classPlain,
	"dicom.find_case_sensitive_names":          classPlain,
	"dicom.audit_log_off":                      classPlain,
	"dicom.events_omit_patient_id":             classPlain,
	"dicom.audit_omit_patient_id":              classPlain,
	"dicom.patient_conflict_policy":            classPlain,
	"dicom.patient_conflict_visibility":        classPlain,
	"dicom.patient_conflict_coerce_patient_id": classPlain,

	"storage.driver":           classPlain,
	"storage.path":             classPlain,
	"storage.s3.endpoint":      classPlain,
	"storage.s3.bucket":        classPlain,
	"storage.s3.region":        classPlain,
	"storage.s3.prefix":        classPlain,
	"storage.s3.insecure":      classPlain,
	"storage.s3.create_bucket": classPlain,

	"index.driver": classPlain,
	"index.dsn":    classDSN,

	"events.driver":          classPlain,
	"events.url":             classURL,
	"events.stream":          classPlain,
	"events.replicas":        classPlain,
	"events.max_age_days":    classPlain,
	"events.legacy_subjects": classPlain,

	"license.file":     classPlain,
	"license.server":   classURL,
	"license.data_dir": classPlain,
}

// SettingClass reports how a setting is shown: "plain", "url", "dsn", or ""
// when it is not on the allow-list and is therefore redacted.
func SettingClass(key string) string { return settingClass[key] }

// Settings that take effect without a restart (DCM-SRS-016).
var reloadable = map[string]bool{"dicom.remote_aes": true, "dicom.allowed_aes": true}

// SettingView is one setting in GET /control/v1/config.
type SettingView struct {
	Key           string   `json:"key"`
	Value         any      `json:"value,omitempty"`
	Redacted      bool     `json:"redacted,omitempty"`
	Set           *bool    `json:"set,omitempty"`            // redacted settings: whether a value is configured
	RedactedParts []string `json:"redacted_parts,omitempty"` // what was removed from the value
	Source        string   `json:"source"`
	Env           string   `json:"env,omitempty"` // the environment variable, when source is "env"
	Reloadable    bool     `json:"reloadable"`
}

func isSet(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map, reflect.Slice:
		return rv.Len() > 0
	}
	return !rv.IsZero()
}

// viewSetting applies the allow-list to one setting.
func viewSetting(s config.Setting) SettingView {
	v := SettingView{Key: s.Key, Source: s.Source, Env: s.Env, Reloadable: reloadable[s.Key]}
	redact := func() SettingView {
		set := isSet(s.Value)
		v.Value, v.RedactedParts, v.Redacted, v.Set = nil, nil, true, &set
		return v
	}
	switch settingClass[s.Key] {
	case classPlain:
		v.Value = s.Value
	case classURL, classDSN:
		str, isString := s.Value.(string)
		if !isString {
			return redact()
		}
		if str == "" {
			v.Value = ""
			return v
		}
		var ok bool
		if settingClass[s.Key] == classDSN {
			v.Value, v.RedactedParts, ok = redactDSN(str)
		} else {
			v.Value, v.RedactedParts, ok = redactURLs(str)
		}
		if !ok {
			return redact()
		}
	default:
		return redact()
	}
	return v
}

// redactURLs reduces a URL, or a comma-separated list of URLs, to scheme,
// host and path. ok is false when a part is not a URL with a host: the
// caller then hides the whole value.
func redactURLs(s string) (out string, removed []string, ok bool) {
	seen := map[string]bool{}
	var parts []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		u, err := url.Parse(p)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "", nil, false
		}
		if u.User != nil {
			seen["user"] = true
			if _, has := u.User.Password(); has {
				seen["password"] = true
			}
		}
		if u.RawQuery != "" {
			seen["query"] = true
		}
		if u.Fragment != "" {
			seen["fragment"] = true
		}
		parts = append(parts, (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String())
	}
	if len(parts) == 0 {
		return "", nil, false
	}
	for _, k := range []string{"user", "password", "query", "fragment"} {
		if seen[k] {
			removed = append(removed, k)
		}
	}
	return strings.Join(parts, ","), removed, true
}

// redactDSN shows a database DSN without credentials: a URL form keeps
// scheme, host and path; a SQLite "file:" form keeps the path. Any other
// form (for example "host=… password=…") is hidden whole.
func redactDSN(s string) (out string, removed []string, ok bool) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "://") {
		return redactURLs(s)
	}
	if rest, isFile := strings.CutPrefix(s, "file:"); isFile {
		path, query, hasQuery := strings.Cut(rest, "?")
		if hasQuery && query != "" {
			removed = []string{"query"}
		}
		return "file:" + path, removed, true
	}
	return "", nil, false
}

var (
	// Credentials in a URL: scheme://user:password@host -> scheme://[redacted]@host
	reUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^/\s@]+@`)
	// key=value credentials (keyword DSNs, query strings).
	reKeyValue = regexp.MustCompile(`(?i)\b(password|passwd|pwd|pass|secret|secret_key|access_key|token|sslpassword)=("[^"]*"|'[^']*'|[^\s&;,"']+)`)
)

// Replacement shown where something was removed.
const redactedMark = "[redacted]"

// minSecretLen is the shortest secret value that scrub replaces by value.
// Shorter values would turn ordinary words into the marker; they are covered
// by the setting allow-list and the URL rules only.
const minSecretLen = 4

// SecretValues returns the secret values held in settings: the value of
// every setting that is not on the allow-list, and the passwords inside URL
// and DSN settings. Pass them (with the admin token) as API.Secrets.
func SecretValues(settings []config.Setting) []string {
	var out []string
	for _, s := range settings {
		str, ok := s.Value.(string)
		if !ok || str == "" {
			continue
		}
		switch settingClass[s.Key] {
		case classPlain:
		case classURL, classDSN:
			found := false
			for _, p := range strings.Split(str, ",") {
				u, err := url.Parse(strings.TrimSpace(p))
				if err != nil {
					continue
				}
				if u.User != nil {
					if pw, has := u.User.Password(); has && pw != "" {
						out, found = append(out, pw), true
					}
				}
				for k, vals := range u.Query() {
					if reSecretKey.MatchString(k) {
						out, found = append(out, vals...), true
					}
				}
			}
			for _, m := range reKeyValue.FindAllStringSubmatch(str, -1) {
				out, found = append(out, strings.Trim(m[2], `"'`)), true
			}
			if _, _, shown := redactDSN(str); !found && !shown {
				out = append(out, str) // unreadable form: treat the whole value as secret
			}
		default:
			out = append(out, str)
		}
	}
	return out
}

var reSecretKey = regexp.MustCompile(`(?i)pass|secret|token|key|pwd`)

// scrub removes secrets from a JSON response body: every string value has
// the known secret values, credentials in URLs and key=value credentials
// replaced by "[redacted]". Object keys and numbers are left alone, so the
// structure of the response does not change. ok is false when the body is
// not valid JSON; the caller then sends nothing of it.
func scrub(body []byte, secrets []string) ([]byte, bool) {
	var use []string
	for _, s := range secrets {
		if len(s) >= minSecretLen {
			use = append(use, s)
		}
	}
	// Longest first, so a secret that contains another is replaced whole.
	sort.Slice(use, func(i, j int) bool { return len(use[i]) > len(use[j]) })

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(scrubValue(v, use)); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

func scrubValue(v any, secrets []string) any {
	switch t := v.(type) {
	case string:
		return scrubString(t, secrets)
	case []any:
		for i := range t {
			t[i] = scrubValue(t[i], secrets)
		}
	case map[string]any:
		for k := range t {
			t[k] = scrubValue(t[k], secrets)
		}
	}
	return v
}

func scrubString(s string, secrets []string) string {
	for _, sec := range secrets {
		if strings.Contains(s, sec) {
			s = strings.ReplaceAll(s, sec, redactedMark)
		}
	}
	if strings.Contains(s, "://") {
		s = reUserinfo.ReplaceAllString(s, "${1}"+redactedMark+"@")
	}
	if strings.Contains(s, "=") {
		s = reKeyValue.ReplaceAllString(s, "${1}="+redactedMark)
	}
	return s
}
