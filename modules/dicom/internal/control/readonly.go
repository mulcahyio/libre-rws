package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/platform/config"
	"github.com/mulcahyio/libre-rws/platform/runtime"
)

// Read-only endpoints (control-api-v1.md §2.1 and §3). None of them changes
// state, writes a log line or starts work that outlives the request
// (DCM-SRS-080). Results are bounded (DCM-SRS-096).

// Features lists what every node's control API offers (GET /control/v1). A
// feature is added here in the change that implements it. "studies" and
// "send" are added when the node has an index to query, "config-edit" when
// it has an overlay file to write to: a node lists only what it serves.
var Features = []string{"conflicts", "status", "config", "remote-aes", "allow-list", "audit", "logs",
	"echo", "reload", "license", "backup"}

// features is the list this node answers with.
func (a *API) features() []string {
	out := append([]string{}, Features...)
	if a.Data != nil {
		out = append(out, "studies", "send")
	}
	if a.Editor != nil && a.Editor.Enabled() {
		out = append(out, "config-edit")
	}
	return out
}

// managed says which live settings the overlay file supplies now.
func (a *API) managed() (remoteAEs, allowedAEs bool) {
	if a.Editor == nil || !a.Editor.Enabled() {
		return false, false
	}
	return a.Editor.Managed()
}

// Limits of the two ring endpoints.
const (
	auditDefaultLimit, auditMaxLimit = 100, 500
	logsDefaultLimit, logsMaxLimit   = 200, 1000
	readyTimeout                     = 3 * time.Second
)

func (a *API) registerReadOnly(r Registrar) {
	r.HandleControl("GET /control/v1", http.HandlerFunc(a.discovery))
	r.HandleControl("GET /control/v1/status", http.HandlerFunc(a.status))
	r.HandleControl("GET /control/v1/config", http.HandlerFunc(a.config))
	r.HandleControl("GET /control/v1/remote-aes", http.HandlerFunc(a.remoteAEs))
	r.HandleControl("GET /control/v1/allow-list", http.HandlerFunc(a.allowList))
	r.HandleControl("GET /control/v1/audit", http.HandlerFunc(a.audit))
	r.HandleControl("GET /control/v1/logs", http.HandlerFunc(a.logs))
}

// Instance identifies the node in every response (DCM-SRS-097), so a client
// that manages several nodes can see which one answered.
type Instance struct {
	NodeID       string `json:"node_id"`
	AETitle      string `json:"ae_title"`
	Version      string `json:"version"`
	ConfigDigest string `json:"config_digest"`
}

// RemoteAEView is one C-MOVE destination in force.
type RemoteAEView struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	AETitle string `json:"ae_title"`
	Source  string `json:"source"`
}

// AllowRuleView is one calling-AE allow-list rule in force.
type AllowRuleView struct {
	Rule   string `json:"rule"`
	Source string `json:"source"`
}

func (a *API) failCode(w http.ResponseWriter, status int, code, msg string) {
	a.writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// liveRemoteAEs returns the destinations in force, sorted by name.
func (a *API) liveRemoteAEs() []RemoteAEView {
	out := []RemoteAEView{}
	if a.Live == nil {
		return out
	}
	fileSrc := config.SourceFile
	if remote, _ := a.managed(); remote {
		fileSrc = config.SourceOverlay
	}
	for name, r := range a.Live.RemoteAEs() {
		src := fileSrc
		if a.RemoteAEEnv[name] {
			src = config.SourceEnv
		}
		out = append(out, RemoteAEView{Name: name, Host: r.Host, Port: r.Port, AETitle: r.AETitle, Source: src})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// liveAllowList returns the allow-list in force, in configuration order.
func (a *API) liveAllowList() (rules []AllowRuleView, enforced bool) {
	rules = []AllowRuleView{}
	if a.Live == nil {
		return rules, false
	}
	src := config.SourceFile
	if _, allowed := a.managed(); allowed {
		src = config.SourceOverlay
	}
	if a.allowListEnv() != "" {
		src = config.SourceEnv
	}
	list, enforced := a.Live.AllowedAEs()
	for _, r := range list {
		rules = append(rules, AllowRuleView{Rule: r, Source: src})
	}
	return rules, enforced
}

// allowListEnv names the environment variable that fixes the allow-list, or
// "" when the environment does not set it.
func (a *API) allowListEnv() string {
	for _, s := range a.Settings {
		if s.Key == "dicom.allowed_aes" && s.Source == config.SourceEnv {
			if s.Env != "" {
				return s.Env
			}
			return "LIBRE_DCM_ALLOWED_AES"
		}
	}
	return ""
}

// instance builds the identity block. The digest covers the live-reloadable
// settings in force (destinations by name, allow-list rules sorted, and
// whether the list is enforced): two nodes with the same digest run the same
// live settings.
func (a *API) instance() Instance {
	type dest struct {
		Name, Host string
		Port       int
		AETitle    string
	}
	var live struct {
		RemoteAEs []dest
		AllowList []string
		Enforced  bool
	}
	for _, r := range a.liveRemoteAEs() {
		live.RemoteAEs = append(live.RemoteAEs, dest{r.Name, r.Host, r.Port, r.AETitle})
	}
	rules, enforced := a.liveAllowList()
	for _, r := range rules {
		live.AllowList = append(live.AllowList, r.Rule)
	}
	sort.Strings(live.AllowList)
	live.Enforced = enforced
	b, _ := json.Marshal(live)
	sum := sha256.Sum256(b)
	return Instance{NodeID: a.Node.ID, AETitle: a.Node.AETitle, Version: a.Node.Version,
		ConfigDigest: "sha256:" + hex.EncodeToString(sum[:])}
}

func (a *API) discovery(w http.ResponseWriter, _ *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{"api": "v1", "features": a.features(), "instance": a.instance()})
}

// checkResult is one readiness check in the status response.
type checkResult struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	checks, ready := []checkResult{}, true
	if a.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		results, ok := a.Ready(ctx)
		cancel()
		ready = ok
		for name, res := range results {
			c := checkResult{Name: name, OK: res == "ok"}
			if !c.OK {
				c.Error = res
			}
			checks = append(checks, c)
		}
		sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	}
	license := map[string]any{"enforced": false}
	if a.License != nil {
		if l := a.License(); l != nil {
			license = l
		}
	}
	remotes := a.liveRemoteAEs()
	rules, _ := a.liveAllowList()
	now := time.Now().UTC()
	counters, complete := a.counters(r.Context())
	body := map[string]any{
		"instance":       a.instance(),
		"time":           now.Format(time.RFC3339),
		"uptime_seconds": int64(now.Sub(a.Node.Started).Seconds()),
		"backends":       map[string]string{"storage": a.Node.Storage, "index": a.Node.Index, "events": a.Node.Events},
		"dicom": map[string]any{"listen": a.Node.DICOMListen, "tls": a.Node.DICOMTLS,
			"allow_list_entries": len(rules), "remote_aes": len(remotes)},
		"admin":    map[string]any{"tls": a.Node.AdminTLS},
		"ready":    ready,
		"checks":   checks,
		"license":  license,
		"counters": counters,
	}
	if !complete {
		body["counters_incomplete"] = true
	}
	a.writeJSON(w, http.StatusOK, body)
}

// counters reads the live values of the node's Prometheus metrics. complete
// is false when a value could not be read; that value is then left out.
func (a *API) counters(ctx context.Context) (map[string]any, bool) {
	cstore, subops := map[string]uint64{}, map[string]uint64{}
	c := map[string]any{
		"associations_active": uint64(0), "associations_total": uint64(0), "associations_rejected_total": uint64(0),
		"cstore": cstore, "cfind_total": uint64(0), "retrieve_subops": subops,
		"outbox_pending": uint64(0), "event_publish_failures_total": uint64(0),
	}
	complete := true
	if a.Metrics != nil {
		families, err := a.Metrics.Gather()
		if err != nil {
			complete = false
		}
		simple := map[string]string{
			"libre_dcm_connections_active":           "associations_active",
			"libre_dcm_connections_total":            "associations_total",
			"libre_dcm_associations_rejected_total":  "associations_rejected_total",
			"libre_dcm_cfind_total":                  "cfind_total",
			"libre_dcm_outbox_pending":               "outbox_pending",
			"libre_dcm_event_publish_failures_total": "event_publish_failures_total",
		}
		for _, f := range families {
			name := f.GetName()
			for _, m := range f.GetMetric() {
				val := m.GetCounter().GetValue() + m.GetGauge().GetValue()
				if val < 0 {
					val = 0
				}
				n := uint64(val)
				if key, ok := simple[name]; ok {
					c[key] = c[key].(uint64) + n
					continue
				}
				if name != "libre_dcm_cstore_total" && name != "libre_dcm_retrieve_subops_total" {
					continue
				}
				for _, l := range m.GetLabel() {
					if l.GetName() != "result" {
						continue
					}
					if name == "libre_dcm_cstore_total" {
						cstore[l.GetValue()] += n
					} else {
						subops[l.GetValue()] += n
					}
				}
			}
		}
	}
	if a.Index != nil {
		if n, err := a.Index.CountOpenConflicts(ctx); err == nil {
			c["conflicts_open"] = n
		} else {
			complete = false
		}
	}
	return c, complete
}

func (a *API) config(w http.ResponseWriter, _ *http.Request) {
	remotes := a.liveRemoteAEs()
	rules, _ := a.liveAllowList()
	views := make([]SettingView, 0, len(a.Settings))
	for _, s := range a.Settings {
		// The two live settings show what is in force now, not what the
		// node started with.
		switch s.Key {
		case "dicom.remote_aes":
			s.Value, s.Source = remotes, sourceOf(len(remotes), func(i int) string { return remotes[i].Source })
			if remote, _ := a.managed(); remote && len(remotes) == 0 {
				s.Source = config.SourceOverlay // the overlay holds an empty table
			}
		case "dicom.allowed_aes":
			list := make([]string, 0, len(rules))
			for _, r := range rules {
				list = append(list, r.Rule)
			}
			s.Value, s.Source = list, sourceOf(len(rules), func(i int) string { return rules[i].Source })
			if _, allowed := a.managed(); allowed && len(rules) == 0 && a.allowListEnv() == "" {
				s.Source = config.SourceOverlay // the overlay holds an empty list
			}
		}
		views = append(views, viewSetting(s))
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"instance": a.instance(),
		"settings": views,
		"restart_required_note": "Settings with reloadable=false take effect after a restart. " +
			"Restart with the installer script or Docker.",
	})
}

// sourceOf gives the source of a list setting from its entries: "default"
// when empty, "env" when every entry comes from the environment, else the
// source of the other entries ("file" or "overlay").
func sourceOf(n int, src func(int) string) string {
	if n == 0 {
		return config.SourceDefault
	}
	for i := 0; i < n; i++ {
		if s := src(i); s != config.SourceEnv {
			return s
		}
	}
	return config.SourceEnv
}

func (a *API) remoteAEs(w http.ResponseWriter, _ *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]any{"instance": a.instance(), "remote_aes": a.liveRemoteAEs()})
}

func (a *API) allowList(w http.ResponseWriter, _ *http.Request) {
	rules, enforced := a.liveAllowList()
	body := map[string]any{"instance": a.instance(), "enforced": enforced, "entries": rules,
		"note": "An empty list lets every calling AE connect."}
	if enforced && len(rules) == 0 {
		// The configured list was invalid at start: the node fails closed.
		body["invalid"] = true
		body["note"] = "The configured allow-list is invalid. Every association is refused until it is corrected."
	}
	a.writeJSON(w, http.StatusOK, body)
}

// ringQuery reads the common query fields of the two ring endpoints.
func ringQuery(r *http.Request, def, max int) (limit int, since time.Time, code, msg string) {
	q := r.URL.Query()
	limit = def
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return 0, since, "invalid_limit", "limit must be a whole number of 1 or more"
		}
		limit = min(n, max)
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return 0, since, "invalid_since", "since must be an RFC 3339 time, for example 2026-11-14T15:04:05Z"
		}
		since = t
	}
	return limit, since, "", ""
}

func lines(entries []runtime.LogEntry) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Line)
	}
	return out
}

func (a *API) audit(w http.ResponseWriter, r *http.Request) {
	limit, since, code, msg := ringQuery(r, auditDefaultLimit, auditMaxLimit)
	if code != "" {
		a.failCode(w, http.StatusBadRequest, code, msg)
		return
	}
	typ, outcome := r.URL.Query().Get("type"), r.URL.Query().Get("outcome")
	var entries []runtime.LogEntry
	var more bool
	size := 0
	if a.Audit != nil {
		size = a.Audit.Size()
		entries, more = a.Audit.Select(limit, func(e *runtime.LogEntry) bool {
			return (typ == "" || strings.HasPrefix(e.Event, typ)) &&
				(outcome == "" || e.Outcome == outcome) &&
				(since.IsZero() || !e.Time.Before(since))
		})
	}
	body := map[string]any{"instance": a.instance(), "records": lines(entries), "count": len(entries),
		"more": more, "limit": limit, "buffer_size": size, "audit_log_off": a.Node.AuditLogOff,
		"note": "Most recent audit records held in memory on this node. The buffer is lost at restart; " +
			"the log stream is the audit record of the site."}
	if a.Node.AuditLogOff {
		body["note"] = "audit_log_off is set: the DICOM gateway writes no audit records on this node."
	}
	a.writeJSON(w, http.StatusOK, body)
}

func (a *API) logs(w http.ResponseWriter, r *http.Request) {
	limit, since, code, msg := ringQuery(r, logsDefaultLimit, logsMaxLimit)
	if code != "" {
		a.failCode(w, http.StatusBadRequest, code, msg)
		return
	}
	minLevel := slog.LevelDebug
	if v := strings.ToLower(r.URL.Query().Get("level")); v != "" {
		switch v {
		case "debug", "info", "warn", "error":
			_ = minLevel.UnmarshalText([]byte(v))
		default:
			a.failCode(w, http.StatusBadRequest, "invalid_level", "level must be one of debug, info, warn, error")
			return
		}
	}
	var entries []runtime.LogEntry
	var more bool
	size := 0
	if a.Logs != nil {
		size = a.Logs.Size()
		entries, more = a.Logs.Select(limit, func(e *runtime.LogEntry) bool {
			return e.Level >= minLevel && (since.IsZero() || !e.Time.Before(since))
		})
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"instance": a.instance(), "lines": lines(entries),
		"count": len(entries), "more": more, "limit": limit, "buffer_size": size,
		"note": "Most recent log lines held in memory on this node, without audit records. " +
			"Lines below the node's configured log level are not written and are not held."})
}
