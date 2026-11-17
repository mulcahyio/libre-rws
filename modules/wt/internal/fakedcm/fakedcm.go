// Package fakedcm is a stand-in for LibreDCM's control API, for Libre WT's
// tests only. It speaks the JSON contract of docs/dhf/libredcm/control-api-v1.md
// over HTTP (httptest), so the tests run the real client against it. It is
// not part of the Libre WT program: nothing outside test files imports it
// (checked by TestFakeIsTestOnly).
package fakedcm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

// Server is one fake LibreDCM node. Change its fields through Set.
type Server struct {
	mu sync.Mutex
	ts *httptest.Server

	Token    string
	NodeID   string
	AETitle  string
	Version  string
	Digest   string
	Features []string
	// Legacy makes the node answer like LibreDCM before the control API
	// extension: no discovery, no status, unknown request fields refused.
	Legacy bool
	// Down makes the node drop every connection.
	Down bool
	// StatusFails makes the status endpoint answer with an error while the
	// discovery endpoint still works.
	StatusFails bool
	// MismatchOnPost makes every POST answer "instance_mismatch", as a
	// node does that is not the one the client expects.
	MismatchOnPost bool
	// Status is returned by /control/v1/status (the instance block is
	// filled in from the fields above).
	Status dcmclient.Status

	Conflict dcmclient.Conflict
	Plan     dcmclient.Plan

	// Cfg holds the answers of the configuration endpoints (config.go).
	Cfg Config
	// Data holds the archive, audit records and log lines of the data
	// endpoints (data.go).
	Data Data

	plans    []map[string]any
	resolves []map[string]any
	hits     map[string]int
	posts    map[string][]map[string]any
	headers  map[string]http.Header
}

// New starts a fake node with one open patient-ID conflict (synthetic
// data).
func New() *Server {
	open := uint64(1)
	s := &Server{Token: "tok", NodeID: "node-1", AETitle: "FAKEDCM", Version: "0.3.0-test", Digest: "sha256:aaaa",
		Features: []string{"conflicts", "status", "config", "remote-aes", "allow-list", "audit", "logs", "echo", "reload", "license", "backup", "studies", "send", "config-edit"},
		hits:     map[string]int{}, posts: map[string][]map[string]any{}, headers: map[string]http.Header{}, Cfg: defaultConfig(), Data: defaultData()}
	s.Status.UptimeSeconds = 93784 // 1d 2h
	s.Status.Backends.Storage, s.Status.Backends.Index, s.Status.Backends.Events = "fs", "sqlite", "log"
	s.Status.DICOM.Listen, s.Status.DICOM.AllowListEntries, s.Status.DICOM.RemoteAEs = ":11112", 2, 1
	s.Status.Ready = true
	s.Status.Checks = []dcmclient.Check{{Name: "index", OK: true}, {Name: "storage", OK: true}}
	s.Status.License = dcmclient.License{Enforced: false}
	s.Status.Counters = dcmclient.Counters{AssociationsActive: 3, AssociationsTotal: 120, AssociationsRejected: 7,
		CStore: map[string]uint64{"success": 2040, "failure": 2, "conflict-open": 3}, CFindTotal: 310,
		RetrieveSubOps: map[string]uint64{"completed": 800, "failed": 1}, OutboxPending: 5, ConflictsOpen: &open}
	s.Conflict = dcmclient.Conflict{ID: "c1", Kind: "patient_id", State: "open", StudyUID: "1.2.3", ExistingPatientID: "P1",
		ExistingStudyUID: "1.2.3", IncomingPatientID: "P2", DisplayPatientID: "P2", PatientName: "ROE^RICHARD",
		SourceAE: "CT1", Instances: 2, FirstSeen: time.Now(), LastSeen: time.Now()}
	s.Plan = dcmclient.Plan{ConflictID: "c1", Action: "correct_incoming", Summary: "Change the patient of 2 incoming instance(s)",
		IncomingInstances: 2, IncomingChanges: []dcmclient.Change{{Attribute: "PatientID", From: "P2", To: "P1"}}}
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// URL is the address of the fake admin listener.
func (s *Server) URL() string { return s.ts.URL }

// Close stops the node.
func (s *Server) Close() { s.ts.Close() }

// Set changes the node under its lock.
func (s *Server) Set(f func(*Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// Hits returns how many requests the path received.
func (s *Server) Hits(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// Resolves returns the bodies of the resolve requests received.
func (s *Server) Resolves() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.resolves...)
}

// Plans returns the bodies of the plan requests received.
func (s *Server) Plans() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.plans...)
}

func (s *Server) instance() dcmclient.InstanceInfo {
	return dcmclient.InstanceInfo{NodeID: s.NodeID, AETitle: s.AETitle, Version: s.Version, ConfigDigest: s.Digest}
}

func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, mcode, msg string) {
	body := map[string]string{"error": msg}
	if mcode != "" {
		body["code"] = mcode
	}
	write(w, code, body)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Down {
		panic(http.ErrAbortHandler) // drops the connection
	}
	s.hits[r.URL.Path]++
	s.headers[r.URL.Path] = r.Header.Clone()
	if r.Header.Get("Authorization") != "Bearer "+s.Token {
		fail(w, http.StatusUnauthorized, "", "unauthorized")
		return
	}
	path := r.URL.Path
	switch {
	case path == "/version" && r.Method == http.MethodGet:
		write(w, 200, map[string]any{"module": "libredcm", "version": s.Version, "node": s.NodeID})
	case path == "/control/v1" && r.Method == http.MethodGet && !s.Legacy:
		write(w, 200, map[string]any{"api": "v1", "features": s.Features, "instance": s.instance()})
	case path == "/control/v1/status" && r.Method == http.MethodGet && !s.Legacy && s.StatusFails:
		fail(w, http.StatusInternalServerError, "internal", "status is not available (test)")
	case path == "/control/v1/status" && r.Method == http.MethodGet && !s.Legacy:
		st := s.Status
		st.Instance, st.Time = s.instance(), time.Now().UTC()
		write(w, 200, st)
	case path == "/control/v1/conflicts" && r.Method == http.MethodGet:
		write(w, 200, map[string]any{"conflicts": []dcmclient.Conflict{s.Conflict}})
	case strings.HasPrefix(path, "/control/v1/conflicts/"):
		s.conflict(w, r, strings.TrimPrefix(path, "/control/v1/conflicts/"))
	case s.configPaths(w, r):
	default:
		http.NotFound(w, r) // plain 404 without a machine code, as the real mux
	}
}

func (s *Server) conflict(w http.ResponseWriter, r *http.Request, rest string) {
	id, op, _ := strings.Cut(rest, "/")
	if r.Method == http.MethodGet && op == "" {
		if id != s.Conflict.ID {
			fail(w, http.StatusNotFound, "", "conflict not found")
			return
		}
		write(w, 200, dcmclient.Detail{Conflict: s.Conflict, IndexedPatient: &dcmclient.Demographics{PatientID: "P1", PatientName: "DOE^JANE"},
			IndexedInstances: 3, IncomingInstances: []dcmclient.Instance{{SOPInstanceUID: "1.2.3.4", Modality: "CT"}}})
		return
	}
	if r.Method != http.MethodPost || (op != "plan" && op != "resolve") {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	_, hasNode := body["expect_node_id"]
	_, hasAE := body["expect_ae_title"]
	if s.Legacy && (hasNode || hasAE) {
		fail(w, http.StatusBadRequest, "", "invalid request: json: unknown field")
		return
	}
	if s.MismatchOnPost || (hasNode && body["expect_node_id"] != s.NodeID) || (hasAE && body["expect_ae_title"] != s.AETitle) {
		fail(w, http.StatusConflict, "instance_mismatch", "this request names another LibreDCM node")
		return
	}
	action, _ := body["action"].(string)
	p := s.Plan
	p.Action = action
	if op == "plan" {
		s.plans = append(s.plans, body)
		if id != s.Conflict.ID {
			fail(w, http.StatusNotFound, "", "conflict not found")
			return
		}
		write(w, 200, p)
		return
	}
	s.resolves = append(s.resolves, body)
	operator, _ := body["operator"].(string)
	s.Conflict.State, s.Conflict.ResolvedBy = "corrected", operator
	write(w, 200, dcmclient.Result{Plan: p, State: "corrected", Rewritten: 2})
}
