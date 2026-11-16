// Package control is LibreDCM's REST control API, served on the admin
// listener behind the admin token (DCM-SRS-077, DCM-SRS-080). Libre WT is its
// client. The contract is docs/dhf/libredcm/control-api-v1.md. Read-only
// endpoints (readonly.go; they never change state):
//
//	GET  /control/v1                 features this node supports
//	GET  /control/v1/status          identity, readiness, licence state, counters
//	GET  /control/v1/config          running configuration, secrets redacted
//	GET  /control/v1/remote-aes      C-MOVE destinations in force
//	GET  /control/v1/allow-list      calling-AE allow-list in force
//	GET  /control/v1/audit           recent audit records (in-memory ring)
//	GET  /control/v1/logs            recent log lines (in-memory ring)
//	GET  /control/v1/license         licence status (key: last four characters)
//	GET  /control/v1/backups         index snapshots in the configured directory
//
// Actions (actions.go; each needs operator, reason and request_id, checks
// the expected node, is remembered by request ID and is audited):
//
//	POST /control/v1/echo               C-ECHO test of a destination
//	POST /control/v1/reload/plan        what a reload would change
//	POST /control/v1/reload             reload (the SIGHUP path), with plan digest
//	POST /control/v1/license/ack        acknowledge the unlicensed notice
//	POST /control/v1/license/request    activation request text (offline)
//	POST /control/v1/license/install    install a licence file sent as text
//	POST /control/v1/license/activate   online activation
//	POST /control/v1/backup             index snapshot into the configured directory
//
// Data and configuration (studies.go, send.go, configedit.go):
//
//	GET  /control/v1/studies                study query with the C-FIND rules, paged
//	POST /control/v1/send/plan              what a send would send, and where to
//	POST /control/v1/send                   send to a configured destination (C-MOVE path)
//	POST /control/v1/config/plan            what an edit of the live settings would change
//	POST /control/v1/config                 write the edit to the overlay file and reload
//	GET  /control/v1/config/history         current and previous overlay version
//	POST /control/v1/config/rollback/plan   what putting the previous version back would change
//	POST /control/v1/config/rollback        put the previous overlay version back
//
// Patient-association conflicts:
//
//	GET  /control/v1/conflicts?state=active|open|accepted|resolved|all&limit=N
//	GET  /control/v1/conflicts/{id}             conflict, indexed patient, held instances
//	POST /control/v1/conflicts/{id}/plan        {"action"}               -> what would change
//	POST /control/v1/conflicts/{id}/resolve     {"action","operator","reason","confirm":true}
//
// Every request body and response is JSON. Errors are {"error": "..."}, with
// a machine "code" on the newer endpoints. Every response passes the secret
// filter in redact.go before it is written (DCM-SRS-082).
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
	"github.com/mulcahyio/libre-rws/platform/config"
	"github.com/mulcahyio/libre-rws/platform/runtime"
)

// Resolver is implemented by *gateway.Gateway.
type Resolver interface {
	PlanResolve(ctx context.Context, id, action string) (gateway.ResolvePlan, error)
	Resolve(ctx context.Context, id, action, operator, reason string) (gateway.ResolveResult, error)
}

// Registrar is implemented by *runtime.Runtime.
type Registrar interface {
	HandleControl(pattern string, h http.Handler)
}

// Live is the part of the gateway that reports the settings in force now.
// It is implemented by *gateway.Gateway.
type Live interface {
	RemoteAEs() map[string]gateway.RemoteAE
	AllowedAEs() (rules []string, enforced bool)
}

// Node holds the facts about this node that are fixed while it runs.
type Node struct {
	ID          string
	AETitle     string
	Version     string
	Started     time.Time
	DICOMListen string
	DICOMTLS    bool
	AdminTLS    bool
	Storage     string // storage driver
	Index       string // index driver
	Events      string // event bus driver
	AuditLogOff bool   // audit_log_off: the gateway writes no audit records
}

// API serves the control endpoints. Index and Resolver serve the conflict
// endpoints; the other fields serve the read-only endpoints and may be left
// empty in tests that do not use them.
type API struct {
	Index    index.Conflicts
	Resolver Resolver

	Node Node
	Live Live
	// Ready runs the readiness checks (name -> "ok" or error text).
	Ready func(context.Context) (map[string]string, bool)
	// Metrics is the node's Prometheus registry; counters are read from it.
	Metrics prometheus.Gatherer
	// License reports the licence state; nil means no licence is needed.
	License func() map[string]any
	// Settings is the configuration the node started with, as loaded. It
	// holds real values; they are redacted before they are returned.
	Settings []config.Setting
	// RemoteAEEnv names the destinations that come from the environment.
	RemoteAEEnv map[string]bool
	// Secrets are values that must never appear in any response (admin
	// token, storage keys, passwords). SecretValues finds those in Settings.
	Secrets []string
	// Logs and Audit are the in-memory rings fed by the logger.
	Logs, Audit *runtime.LogBuffer

	// The fields below serve the action endpoints.

	// AuditLog is the node's logger. Every action writes its audit records
	// through it. Without it every action is refused.
	AuditLog *slog.Logger
	// Echo runs a C-ECHO test (DICOMEcho on a real node).
	Echo EchoFunc
	// Reloader is the node's configuration reloader, the one SIGHUP uses.
	Reloader Reloader
	// Licence is the node's licensing; nil in a source build.
	Licence Licence
	// Backup describes where and how index snapshots are written.
	Backup *Backup
	// Data runs the study query and the send (the gateway on a real node).
	Data Data
	// Editor writes configuration changes to the node's overlay file (the
	// reloader on a real node). Nil: configuration edit is not offered.
	Editor ConfigEditor

	requests idempotency // results of recent actions, by request ID
}

// Register adds the endpoints to the admin listener.
func (a *API) Register(r Registrar) {
	r.HandleControl("GET /control/v1/conflicts", http.HandlerFunc(a.list))
	r.HandleControl("GET /control/v1/conflicts/{id}", http.HandlerFunc(a.get))
	r.HandleControl("POST /control/v1/conflicts/{id}/plan", http.HandlerFunc(a.plan))
	r.HandleControl("POST /control/v1/conflicts/{id}/resolve", http.HandlerFunc(a.resolve))
	a.registerReadOnly(r)
	a.registerActions(r)
}

// registerActions adds the action endpoints (control-api-v1.md §4).
func (a *API) registerActions(r Registrar) {
	r.HandleControl("POST /control/v1/echo", action(a, actionSpec{name: "echo", event: auditVerification,
		timeout: EchoTimeout, slots: make(chan struct{}, echoSlots)}, a.echo))
	// One reload at a time is kept by the reloader itself (SIGHUP included).
	r.HandleControl("POST /control/v1/reload/plan", preview(a, a.reloadPreview))
	r.HandleControl("POST /control/v1/reload", action(a, actionSpec{name: "reload", event: auditConfigChange,
		timeout: time.Minute}, a.reload))
	r.HandleControl("GET /control/v1/license", http.HandlerFunc(a.licenseStatus))
	r.HandleControl("POST /control/v1/license/ack", action(a, actionSpec{name: "license/ack", event: auditLicense,
		timeout: time.Minute}, a.licenseAck))
	// The request text holds the licence key: it is not kept for replay.
	r.HandleControl("POST /control/v1/license/request", action(a, actionSpec{name: "license/request", event: auditLicense,
		timeout: time.Minute, forget: true}, a.licenseRequest))
	r.HandleControl("POST /control/v1/license/install", action(a, actionSpec{name: "license/install", event: auditLicense,
		timeout: time.Minute, bodyLimit: licenseBodyLimit}, a.licenseInstall))
	r.HandleControl("POST /control/v1/license/activate", action(a, actionSpec{name: "license/activate", event: auditLicense,
		timeout: LicenseActivateTimeout, slots: make(chan struct{}, 1)}, a.licenseActivate))
	r.HandleControl("GET /control/v1/backups", http.HandlerFunc(a.backups))
	r.HandleControl("POST /control/v1/backup", action(a, actionSpec{name: "backup", event: auditBackup,
		timeout: BackupTimeout, slots: make(chan struct{}, 1)}, a.backup))
	a.registerData(r)
	a.registerConfigEdit(r)
}

// Detail is the response of GET /control/v1/conflicts/{id}.
type Detail struct {
	Conflict          index.ConflictInfo       `json:"conflict"`
	IndexedPatient    *index.Demographics      `json:"indexed_patient,omitempty"`
	IndexedInstances  int                      `json:"indexed_instances"`
	IncomingInstances []index.ConflictInstance `json:"incoming_instances"`
}

// ResolveRequest is the body of POST .../resolve.
type ResolveRequest struct {
	Action   string `json:"action"`
	Operator string `json:"operator"`
	Reason   string `json:"reason"`
	// Confirm must be true: the client has shown the plan and the
	// administrator approved the edit.
	Confirm bool `json:"confirm"`
	// ExpectNodeID and ExpectAETitle, when set, must name this node: an
	// edit meant for another node is refused (DCM-SRS-097).
	ExpectNodeID  string `json:"expect_node_id,omitempty"`
	ExpectAETitle string `json:"expect_ae_title,omitempty"`
}

// wrongInstance answers 409 and reports true when the request names another
// node than this one. Nothing is changed (DCM-SRS-097).
func (a *API) wrongInstance(w http.ResponseWriter, nodeID, aeTitle string) bool {
	if (nodeID == "" || nodeID == a.Node.ID) && (aeTitle == "" || aeTitle == a.Node.AETitle) {
		return false
	}
	a.writeJSON(w, http.StatusConflict, map[string]any{"code": "instance_mismatch",
		"error": "this node is not the one the request names; nothing was changed", "instance": a.instance()})
	return true
}

// writeJSON is the only way a response leaves this package. The body goes
// through the secret filter first; if the filter cannot process it, nothing
// of it is sent (fail closed).
func (a *API) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error","code":"internal"}` + "\n"))
		return
	}
	body, ok := scrub(buf.Bytes(), a.Secrets)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error","code":"internal"}` + "\n"))
		return
	}
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func (a *API) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, index.ErrNotFound):
		a.writeJSON(w, http.StatusNotFound, map[string]string{"error": "conflict not found"})
	case errors.Is(err, gateway.ErrInvalidRequest):
		a.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	default:
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := a.Index.ListConflicts(r.Context(), r.URL.Query().Get("state"), limit)
	if err != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if out == nil {
		out = []index.ConflictInfo{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"conflicts": out})
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := a.Index.GetConflict(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	insts, err := a.Index.ConflictInstances(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if insts == nil {
		insts = []index.ConflictInstance{}
	}
	d := Detail{Conflict: c, IncomingInstances: insts}
	target := c.ExistingStudyUID
	if target == "" {
		target = c.StudyUID
	}
	if dem, ok, err := a.Index.StudyDemographics(r.Context(), target); err == nil && ok {
		d.IndexedPatient = &dem
		if refs, err := a.Index.StudyInstances(r.Context(), target); err == nil {
			d.IndexedInstances = len(refs)
		}
	}
	a.writeJSON(w, http.StatusOK, d)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (a *API) plan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action        string `json:"action"`
		ExpectNodeID  string `json:"expect_node_id,omitempty"`
		ExpectAETitle string `json:"expect_ae_title,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if a.wrongInstance(w, req.ExpectNodeID, req.ExpectAETitle) {
		return
	}
	p, err := a.Resolver.PlanResolve(r.Context(), r.PathValue("id"), req.Action)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, p)
}

func (a *API) resolve(w http.ResponseWriter, r *http.Request) {
	var req ResolveRequest
	if err := decode(r, &req); err != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if a.wrongInstance(w, req.ExpectNodeID, req.ExpectAETitle) {
		return
	}
	if !req.Confirm {
		a.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "edit not confirmed: show the plan and set confirm=true after the administrator approves"})
		return
	}
	res, err := a.Resolver.Resolve(r.Context(), r.PathValue("id"), req.Action, req.Operator, req.Reason)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, res)
}
