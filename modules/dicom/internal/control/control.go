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
}

// Register adds the endpoints to the admin listener.
func (a *API) Register(r Registrar) {
	r.HandleControl("GET /control/v1/conflicts", http.HandlerFunc(a.list))
	r.HandleControl("GET /control/v1/conflicts/{id}", http.HandlerFunc(a.get))
	r.HandleControl("POST /control/v1/conflicts/{id}/plan", http.HandlerFunc(a.plan))
	r.HandleControl("POST /control/v1/conflicts/{id}/resolve", http.HandlerFunc(a.resolve))
	a.registerReadOnly(r)
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
		Action string `json:"action"`
	}
	if err := decode(r, &req); err != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
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
