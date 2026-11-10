// Package control is LibreDCM's REST control API, served on the admin
// listener behind the admin token (DCM-SRS-077). Libre WT is its client. v1
// covers patient-association conflicts:
//
//	GET  /control/v1/conflicts?state=active|open|accepted|resolved|all&limit=N
//	GET  /control/v1/conflicts/{id}             conflict, indexed patient, held instances
//	POST /control/v1/conflicts/{id}/plan        {"action"}               -> what would change
//	POST /control/v1/conflicts/{id}/resolve     {"action","operator","reason","confirm":true}
//
// Every request body and response is JSON. Errors are {"error": "..."}.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
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

// API serves the control endpoints.
type API struct {
	Index    index.Conflicts
	Resolver Resolver
}

// Register adds the endpoints to the admin listener.
func (a *API) Register(r Registrar) {
	r.HandleControl("GET /control/v1/conflicts", http.HandlerFunc(a.list))
	r.HandleControl("GET /control/v1/conflicts/{id}", http.HandlerFunc(a.get))
	r.HandleControl("POST /control/v1/conflicts/{id}/plan", http.HandlerFunc(a.plan))
	r.HandleControl("POST /control/v1/conflicts/{id}/resolve", http.HandlerFunc(a.resolve))
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, index.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "conflict not found"})
	case errors.Is(err, gateway.ErrInvalidRequest):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := a.Index.ListConflicts(r.Context(), r.URL.Query().Get("state"), limit)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if out == nil {
		out = []index.ConflictInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": out})
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := a.Index.GetConflict(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	insts, err := a.Index.ConflictInstances(r.Context(), id)
	if err != nil {
		fail(w, err)
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
	writeJSON(w, http.StatusOK, d)
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	p, err := a.Resolver.PlanResolve(r.Context(), r.PathValue("id"), req.Action)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) resolve(w http.ResponseWriter, r *http.Request) {
	var req ResolveRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if !req.Confirm {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "edit not confirmed: show the plan and set confirm=true after the administrator approves"})
		return
	}
	res, err := a.Resolver.Resolve(r.Context(), r.PathValue("id"), req.Action, req.Operator, req.Reason)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
