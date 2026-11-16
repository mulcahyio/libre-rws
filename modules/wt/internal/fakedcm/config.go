package fakedcm

import (
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

// The configuration part of the fake: running settings, C-MOVE
// destinations, allow-list, reload, configuration edit, rollback, history
// and the C-ECHO test (control-api-v1.md §3.2, §3.3, §4.1, §4.2, §5.3).

// Failure is an error answer the fake gives for one path.
type Failure struct {
	Status int
	Code   string
	Msg    string
}

// Config holds what the configuration endpoints of the fake answer. Tests
// change it through Server.Set.
type Config struct {
	Settings  []dcmclient.Setting
	RemoteAEs []dcmclient.RemoteAE
	AllowList dcmclient.AllowList
	// Plan is the answer to every plan request (reload, edit, rollback).
	// An apply must carry its PlanDigest and acknowledge its warnings.
	Plan    dcmclient.ChangePlan
	History dcmclient.ConfigHistory
	// Echo is the answer of the C-ECHO test for a known target.
	Echo dcmclient.EchoResult
	// Fail makes a path answer with an error instead.
	Fail map[string]Failure
	// Delay makes a path wait before it answers.
	Delay map[string]time.Duration
	// OtherNode makes the answer of a path name another node in its
	// instance block, as when the address now reaches another LibreDCM.
	OtherNode map[string]bool
	// DigestAfter is the settings digest after an applied change.
	DigestAfter string

	// Lic is the licence status of the node (licence.go).
	Lic dcmclient.LicenseDetail
	// BackupDir is the node's backup directory; "" means backups are off.
	// BackupReason is the reason the node gives then.
	BackupDir, BackupReason string
	// Backups are the snapshots in the backup directory, newest first.
	Backups []dcmclient.BackupFile
}

func defaultConfig() Config {
	yes := true
	c := Config{
		Settings: []dcmclient.Setting{
			{Key: "dicom.ae_title", Value: "FAKEDCM", Source: "file"},
			{Key: "dicom.listen", Value: ":11112", Source: "default"},
			{Key: "dicom.remote_aes", Value: []any{map[string]any{"name": "VIEWER1", "host": "10.0.0.20", "port": 104, "ae_title": "VIEWER1", "source": "file"}}, Source: "file", Reloadable: true},
			{Key: "dicom.allowed_aes", Value: []any{"CT1@10.0.0.5", "MR1"}, Source: "overlay", Reloadable: true},
			{Key: "storage.s3.secret_key", Redacted: true, Set: &yes, Source: "env", Env: "LIBRE_DCM_S3_SECRET_KEY"},
			{Key: "index.dsn", Value: "postgres://db.example:5432/libredcm", RedactedParts: []string{"user", "password"}, Source: "env", Env: "LIBRE_DCM_INDEX_DSN"},
		},
		RemoteAEs: []dcmclient.RemoteAE{{Name: "VIEWER1", Host: "10.0.0.20", Port: 104, AETitle: "VIEWER1", Source: "file"}},
		AllowList: dcmclient.AllowList{Enforced: true, Note: "An empty list lets every calling AE connect.",
			Entries: []dcmclient.AllowRule{{Rule: "CT1@10.0.0.5", Source: "overlay"}, {Rule: "MR1", Source: "overlay"}}},
		History: dcmclient.ConfigHistory{Enabled: true, Note: "One previous version is kept for rollback.",
			Current:     &dcmclient.OverlayVersion{Time: "2026-11-01T10:00:00Z", Operator: "admin0", Reason: "new CT", Kind: "edit", Digest: "sha256:cur", Settings: []string{"dicom.allowed_aes"}},
			Previous:    &dcmclient.OverlayVersion{Time: "2026-10-01T09:00:00Z", Kind: "initial", Digest: "sha256:prev", Settings: []string{}},
			FromOverlay: []string{"dicom.allowed_aes"}},
		Echo:        dcmclient.EchoResult{OK: true, DurationMS: 42, Target: "10.0.0.20:104", CalledAE: "VIEWER1", CallingAE: "FAKEDCM", Source: "configured", Note: "Verification SOP class only. No patient data was sent."},
		Fail:        map[string]Failure{},
		Delay:       map[string]time.Duration{},
		OtherNode:   map[string]bool{},
		DigestAfter: "sha256:after",
		Lic: dcmclient.LicenseDetail{Enforced: true, State: "unlicensed", AckRequired: true,
			Message: "No licence is installed. DICOM services keep running.", Fingerprint: "fp-synthetic-0001",
			Note: "DICOM services run in every licence state. Licensing never stops them."},
		BackupDir: "/var/lib/libredcm/backups",
		Backups:   []dcmclient.BackupFile{{File: "index-node-1-20261101T020000Z.db", Bytes: 4096, Time: time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)}},
	}
	c.Plan.Changed = true
	c.Plan.RemoteAEs.Added = []dcmclient.RemoteAE{{Name: "VIEWER2", Host: "10.1.2.3", Port: 11112, AETitle: "VIEWER2", Source: "overlay"}}
	c.Plan.RemoteAEs.Removed, c.Plan.RemoteAEs.Changed = []dcmclient.RemoteAE{}, []dcmclient.RemoteAEChange{}
	c.Plan.AllowList.Added, c.Plan.AllowList.Removed = []string{}, []string{}
	c.Plan.AllowList.EnforcedBefore, c.Plan.AllowList.EnforcedAfter = true, true
	c.Plan.Warnings = []dcmclient.PlanWarning{}
	c.Plan.PlanDigest = "sha256:plan-1"
	return c
}

// Posts returns the bodies of the POST requests the path received.
func (s *Server) Posts(path string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.posts[path]...)
}

// Headers returns the headers of the last request the path received.
func (s *Server) Headers(path string) http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headers[path].Clone()
}

var reRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{16,64}$`)

// configPaths serves the configuration endpoints. It returns false for a
// path it does not know. The caller holds the lock.
func (s *Server) configPaths(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	known := map[string]string{
		"/control/v1/config": "GET POST", "/control/v1/remote-aes": "GET", "/control/v1/allow-list": "GET",
		"/control/v1/config/history": "GET", "/control/v1/config/plan": "POST", "/control/v1/reload/plan": "POST",
		"/control/v1/reload": "POST", "/control/v1/config/rollback/plan": "POST", "/control/v1/config/rollback": "POST",
		"/control/v1/echo":    "POST",
		"/control/v1/license": "GET", "/control/v1/license/ack": "POST", "/control/v1/license/request": "POST",
		"/control/v1/license/install": "POST", "/control/v1/license/activate": "POST",
		"/control/v1/backups": "GET", "/control/v1/backup": "POST",
	}
	if s.Legacy || !strings.Contains(known[path], r.Method) {
		return false
	}
	if (path == "/control/v1/config" && r.Method == http.MethodPost) || strings.HasPrefix(path, "/control/v1/config/") {
		if !s.hasFeature("config-edit") {
			return false // a node without an overlay file does not serve them
		}
	}
	if d := s.Cfg.Delay[path]; d > 0 {
		s.mu.Unlock()
		time.Sleep(d)
		s.mu.Lock()
	}
	inst := s.instance()
	if s.Cfg.OtherNode[path] {
		inst.NodeID, inst.AETitle = "node-other", "OTHERAE"
	}
	var body map[string]any
	if r.Method == http.MethodPost {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, "invalid_body", "invalid request body")
			return true
		}
		s.posts[path] = append(s.posts[path], body)
		if s.MismatchOnPost || (body["expect_node_id"] != nil && body["expect_node_id"] != s.NodeID) ||
			(body["expect_ae_title"] != nil && body["expect_ae_title"] != s.AETitle) {
			fail(w, http.StatusConflict, "instance_mismatch", "this request names another LibreDCM node")
			return true
		}
	}
	if f, ok := s.Cfg.Fail[path]; ok {
		fail(w, f.Status, f.Code, f.Msg)
		return true
	}
	isPlan := strings.HasSuffix(path, "/plan")
	if r.Method == http.MethodPost && !isPlan {
		// The action envelope (control-api-v1.md §2.3).
		op, _ := body["operator"].(string)
		reason, _ := body["reason"].(string)
		id, _ := body["request_id"].(string)
		switch {
		case strings.TrimSpace(op) == "":
			fail(w, http.StatusUnprocessableEntity, "operator_required", "operator is required")
			return true
		case strings.TrimSpace(reason) == "":
			fail(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
			return true
		case !reRequestID.MatchString(id):
			fail(w, http.StatusUnprocessableEntity, "request_id_invalid", "request_id is invalid")
			return true
		}
	}
	switch {
	case path == "/control/v1/config" && r.Method == http.MethodGet:
		write(w, 200, map[string]any{"instance": inst, "settings": s.Cfg.Settings,
			"restart_required_note": "Settings with reloadable=false take effect after a restart. Restart with the installer script or Docker."})
	case path == "/control/v1/remote-aes":
		list := s.Cfg.RemoteAEs
		if list == nil {
			list = []dcmclient.RemoteAE{}
		}
		write(w, 200, map[string]any{"instance": inst, "remote_aes": list})
	case path == "/control/v1/allow-list":
		al := s.Cfg.AllowList
		al.Instance = inst
		if al.Entries == nil {
			al.Entries = []dcmclient.AllowRule{}
		}
		write(w, 200, al)
	case path == "/control/v1/config/history":
		h := s.Cfg.History
		h.Instance = inst
		write(w, 200, h)
	case isPlan:
		out := map[string]any{"instance": inst, "plan": s.Cfg.Plan}
		if path == "/control/v1/config/rollback/plan" {
			if s.Cfg.History.Previous == nil {
				fail(w, http.StatusUnprocessableEntity, "no_previous_version", "there is no previous configuration version to go back to")
				return true
			}
			out["previous"] = s.Cfg.History.Previous
		}
		write(w, 200, out)
	case path == "/control/v1/echo":
		s.echo(w, body, inst)
	case strings.HasPrefix(path, "/control/v1/license"):
		s.licence(w, path, body, inst)
	case strings.HasPrefix(path, "/control/v1/backup"):
		s.backup(w, path, inst)
	default: // reload, config, rollback
		s.applyChange(w, path, body, inst)
	}
	return true
}

func (s *Server) hasFeature(name string) bool {
	for _, f := range s.Features {
		if f == name {
			return true
		}
	}
	return false
}

// applyChange is the apply step of a reload, an edit and a rollback: it
// needs confirm, the digest of the current plan and every warning that must
// be acknowledged, as LibreDCM does (DCM-SRS-088, -093, -095).
func (s *Server) applyChange(w http.ResponseWriter, path string, body map[string]any, inst dcmclient.InstanceInfo) {
	plan := s.Cfg.Plan
	if body["confirm"] != true {
		fail(w, http.StatusUnprocessableEntity, "not_confirmed", "change not confirmed")
		return
	}
	digest, _ := body["plan_digest"].(string)
	if digest == "" {
		fail(w, http.StatusUnprocessableEntity, "plan_digest_required", "plan_digest is required")
		return
	}
	if digest != plan.PlanDigest {
		fail(w, http.StatusConflict, "plan_changed", "the running settings or the configuration changed since the plan was made; nothing was changed. Ask for a new plan.")
		return
	}
	acked := map[string]bool{}
	list, _ := body["acknowledge_warnings"].([]any)
	for _, c := range list {
		if code, ok := c.(string); ok {
			acked[code] = true
		}
	}
	for _, wn := range plan.Warnings {
		if wn.MustAcknowledge && !acked[wn.Code] {
			fail(w, http.StatusUnprocessableEntity, "warning_not_acknowledged", "the plan has a warning that must be acknowledged in acknowledge_warnings: "+wn.Code+". Nothing was changed.")
			return
		}
	}
	before := s.Digest
	if path == "/control/v1/config" {
		settings, _ := body["settings"].(map[string]any)
		if raw, ok := settings[dcmclient.KeyRemoteAEs]; ok {
			var list []dcmclient.RemoteAE
			b, _ := json.Marshal(raw)
			_ = json.Unmarshal(b, &list)
			for i := range list {
				list[i].Source = "overlay"
			}
			s.Cfg.RemoteAEs = list
		}
		if raw, ok := settings[dcmclient.KeyAllowedAEs]; ok {
			var rules []string
			b, _ := json.Marshal(raw)
			_ = json.Unmarshal(b, &rules)
			s.Cfg.AllowList.Entries = nil
			for _, rule := range rules {
				s.Cfg.AllowList.Entries = append(s.Cfg.AllowList.Entries, dcmclient.AllowRule{Rule: rule, Source: "overlay"})
			}
			s.Cfg.AllowList.Enforced = len(rules) > 0
		}
	}
	if plan.Changed {
		s.Digest = s.Cfg.DigestAfter
	}
	inst.ConfigDigest = s.Digest
	write(w, 200, map[string]any{"instance": inst, "applied": true, "changed": plan.Changed, "plan": plan,
		"config_digest_before": before, "config_digest_after": s.Digest})
}

func (s *Server) echo(w http.ResponseWriter, body map[string]any, inst dcmclient.InstanceInfo) {
	res := s.Cfg.Echo
	res.Instance = inst
	if name, ok := body["remote_ae"].(string); ok && name != "" {
		if body["host"] != nil || body["port"] != nil || body["ae_title"] != nil {
			fail(w, http.StatusUnprocessableEntity, "invalid_target", "give either remote_ae or host, port and ae_title, not both")
			return
		}
		found := false
		for _, d := range s.Cfg.RemoteAEs {
			found = found || d.Name == name
		}
		if !found {
			fail(w, http.StatusNotFound, "unknown_remote_ae", "no remote AE with this name is configured on this node")
			return
		}
		res.Source = "configured"
	} else {
		host, _ := body["host"].(string)
		port, _ := body["port"].(float64)
		ae, _ := body["ae_title"].(string)
		if host == "" || port < 1 || port > 65535 || ae == "" || len(ae) > 16 {
			fail(w, http.StatusUnprocessableEntity, "invalid_target", "host must be a host name or IP address, port 1 to 65535, ae_title 1 to 16 characters")
			return
		}
		res.Source, res.CalledAE, res.Target = "ad-hoc", ae, net.JoinHostPort(host, strconv.Itoa(int(port)))
		if tls, _ := body["tls"].(bool); tls {
			no := false
			res.TLS, res.TLSVerified = true, &no
		}
	}
	write(w, 200, res)
}
