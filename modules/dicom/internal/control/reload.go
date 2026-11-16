package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

// Reload (control-api-v1.md §4.2, DCM-SRS-088). The control API does not
// load or apply configuration itself: it asks the node's reloader, the same
// one SIGHUP uses, so there is one reload path (DCM-SRS-016, -060).

// ErrReloadBlocked is returned by a Reloader while an unlicensed node waits
// for an administrator's acknowledgement (DCM-SRS-060). The reloader wraps
// it; the text of the wrapping error says what to do.
var ErrReloadBlocked = errors.New("configuration reload blocked until the licence notice is acknowledged")

// LiveSettings are the settings a reload puts in force, validated and in
// the form the gateway reports them.
type LiveSettings struct {
	RemoteAEs  map[string]gateway.RemoteAE
	AllowedAEs []string
}

// Reloader is the node's configuration reloader.
type Reloader interface {
	// Preview loads and validates the configuration a reload would apply.
	// Nothing changes.
	Preview() (LiveSettings, error)
	// Apply loads and validates the configuration, calls check with it
	// and, when check returns nil, puts it in force. Load, check and apply
	// happen as one step: no other reload can run in between. On any error
	// the running settings stay as they are.
	Apply(check func(LiveSettings) error) (LiveSettings, error)
}

// Warning codes of a reload plan. Each marks a change that weakens a risk
// control and must be acknowledged by its code before apply (DCM-SRS-095).
const (
	WarnAllowListEmptied   = "allow_list_emptied"
	WarnRemoteAERemoved    = "remote_ae_removed"
	WarnRemoteAERetargeted = "remote_ae_retargeted"
)

// PlanWarning is one warning of a plan.
type PlanWarning struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	MustAcknowledge bool   `json:"must_acknowledge"`
}

// RemoteAEChange is a destination whose address or AE title changes.
type RemoteAEChange struct {
	Name string       `json:"name"`
	From RemoteAEView `json:"from"`
	To   RemoteAEView `json:"to"`
}

// ReloadPlan is the difference between the running live settings and the
// settings a reload would load.
type ReloadPlan struct {
	Changed   bool `json:"changed"`
	RemoteAEs struct {
		Added   []RemoteAEView   `json:"added"`
		Removed []RemoteAEView   `json:"removed"`
		Changed []RemoteAEChange `json:"changed"`
	} `json:"remote_aes"`
	AllowList struct {
		Added          []string `json:"added"`
		Removed        []string `json:"removed"`
		EnforcedBefore bool     `json:"enforced_before"`
		EnforcedAfter  bool     `json:"enforced_after"`
	} `json:"allow_list"`
	Warnings   []PlanWarning `json:"warnings"`
	PlanDigest string        `json:"plan_digest"`
}

// ReloadRequest is the body of POST /control/v1/reload and .../reload/plan.
// The plan needs none of the fields.
type ReloadRequest struct {
	Envelope
	PlanDigest          string   `json:"plan_digest,omitempty"`
	Confirm             bool     `json:"confirm,omitempty"`
	AcknowledgeWarnings []string `json:"acknowledge_warnings,omitempty"`
}

// reloadPlan compares what is running with next.
func (a *API) reloadPlan(next LiveSettings) ReloadPlan {
	var p ReloadPlan
	p.RemoteAEs.Added, p.RemoteAEs.Removed, p.RemoteAEs.Changed = []RemoteAEView{}, []RemoteAEView{}, []RemoteAEChange{}
	p.AllowList.Added, p.AllowList.Removed, p.Warnings = []string{}, []string{}, []PlanWarning{}

	current := map[string]RemoteAEView{}
	for _, r := range a.liveRemoteAEs() {
		current[r.Name] = r
	}
	names := make([]string, 0, len(next.RemoteAEs))
	for name := range next.RemoteAEs {
		names = append(names, name)
	}
	sort.Strings(names)
	type dest struct {
		Name, Host string
		Port       int
		AETitle    string
	}
	var nextDests []dest
	for _, name := range names {
		r := next.RemoteAEs[name]
		if r.AETitle == "" {
			r.AETitle = name // as the gateway does
		}
		nextDests = append(nextDests, dest{name, r.Host, r.Port, r.AETitle})
		to := RemoteAEView{Name: name, Host: r.Host, Port: r.Port, AETitle: r.AETitle, Source: a.remoteAESource(name)}
		from, had := current[name]
		switch {
		case !had:
			p.RemoteAEs.Added = append(p.RemoteAEs.Added, to)
		case from.Host != to.Host || from.Port != to.Port || from.AETitle != to.AETitle:
			p.RemoteAEs.Changed = append(p.RemoteAEs.Changed, RemoteAEChange{Name: name, From: from, To: to})
			p.Warnings = append(p.Warnings, PlanWarning{Code: WarnRemoteAERetargeted, MustAcknowledge: true,
				Message: "The destination " + name + " gets a new address or AE title. Studies moved to " + name + " will go to the new address."})
		}
	}
	for _, r := range a.liveRemoteAEs() { // sorted by name
		if _, stays := next.RemoteAEs[r.Name]; !stays {
			p.RemoteAEs.Removed = append(p.RemoteAEs.Removed, r)
			p.Warnings = append(p.Warnings, PlanWarning{Code: WarnRemoteAERemoved, MustAcknowledge: true,
				Message: "The destination " + r.Name + " is removed. A C-MOVE to " + r.Name + " will be refused."})
		}
	}

	rules, enforced := a.liveAllowList()
	have, want := map[string]bool{}, map[string]bool{}
	for _, r := range rules {
		have[r.Rule] = true
	}
	for _, r := range next.AllowedAEs {
		want[r] = true
		if !have[r] {
			p.AllowList.Added = append(p.AllowList.Added, r)
		}
	}
	for _, r := range rules {
		if !want[r.Rule] {
			p.AllowList.Removed = append(p.AllowList.Removed, r.Rule)
		}
	}
	sort.Strings(p.AllowList.Added)
	sort.Strings(p.AllowList.Removed)
	p.AllowList.EnforcedBefore, p.AllowList.EnforcedAfter = enforced, len(next.AllowedAEs) > 0
	if enforced && len(next.AllowedAEs) == 0 {
		p.Warnings = append(p.Warnings, PlanWarning{Code: WarnAllowListEmptied, MustAcknowledge: true,
			Message: "The allow-list becomes empty: every calling AE may then connect, store and retrieve."})
	}
	p.Changed = len(p.RemoteAEs.Added)+len(p.RemoteAEs.Removed)+len(p.RemoteAEs.Changed)+
		len(p.AllowList.Added)+len(p.AllowList.Removed) > 0 || p.AllowList.EnforcedBefore != p.AllowList.EnforcedAfter

	// The digest covers what is running and what would be loaded: when
	// either changes after the person saw the plan, the digest changes.
	allow := append([]string(nil), next.AllowedAEs...)
	sort.Strings(allow)
	raw, _ := json.Marshal(struct {
		Running   string
		RemoteAEs []dest
		AllowList []string
	}{a.instance().ConfigDigest, nextDests, allow})
	sum := sha256.Sum256(raw)
	p.PlanDigest = "sha256:" + hex.EncodeToString(sum[:])
	return p
}

func (a *API) remoteAESource(name string) string {
	if a.RemoteAEEnv[name] {
		return "env"
	}
	return "file"
}

// reloadFailure maps a reloader error to a response.
func reloadFailure(err error) (status int, code, msg string) {
	if errors.Is(err, ErrReloadBlocked) {
		return http.StatusLocked, "license_ack_required", err.Error()
	}
	return http.StatusUnprocessableEntity, "invalid_config",
		"the configuration could not be loaded; the running settings are unchanged: " + err.Error()
}

func (a *API) reloadPreview(_ context.Context, _ *ReloadRequest) (int, map[string]any) {
	if a.Reloader == nil {
		return http.StatusInternalServerError, map[string]any{"error": "reload is not available on this node", "code": "internal"}
	}
	next, err := a.Reloader.Preview()
	if err != nil {
		status, code, msg := reloadFailure(err)
		return status, map[string]any{"error": msg, "code": code}
	}
	return http.StatusOK, map[string]any{"plan": a.reloadPlan(next)}
}

// planError is a refusal decided while the reloader holds the configuration.
type planError struct {
	status    int
	code, msg string
	plan      ReloadPlan
}

func (e *planError) Error() string { return e.msg }

func (a *API) reload(_ context.Context, req *ReloadRequest) actionResult {
	if a.Reloader == nil {
		return refusal(http.StatusInternalServerError, "internal", "reload is not available on this node", false)
	}
	if !req.Confirm {
		return refusal(http.StatusUnprocessableEntity, "not_confirmed",
			"reload not confirmed: show the plan and set confirm=true after the administrator approves", true)
	}
	if req.PlanDigest == "" {
		return refusal(http.StatusUnprocessableEntity, "plan_digest_required",
			"plan_digest is required: ask for the plan first (POST /control/v1/reload/plan)", true)
	}
	before := a.instance().ConfigDigest
	var plan ReloadPlan
	_, err := a.Reloader.Apply(func(next LiveSettings) error {
		plan = a.reloadPlan(next)
		if plan.PlanDigest != req.PlanDigest {
			return &planError{http.StatusConflict, "plan_changed",
				"the running settings or the configuration changed since the plan was made; nothing was changed. Ask for a new plan.", plan}
		}
		acked := map[string]bool{}
		for _, c := range req.AcknowledgeWarnings {
			acked[c] = true
		}
		for _, w := range plan.Warnings {
			if w.MustAcknowledge && !acked[w.Code] {
				return &planError{http.StatusUnprocessableEntity, "warning_not_acknowledged",
					"the plan has a warning that must be acknowledged in acknowledge_warnings: " + w.Code + ". Nothing was changed.", plan}
			}
		}
		return nil
	})
	var pe *planError
	switch {
	case errors.As(err, &pe):
		return refusal(pe.status, pe.code, pe.msg, true, "config_digest", before)
	case err != nil:
		status, code, msg := reloadFailure(err)
		res := refusal(status, code, msg, false, "config_digest", before)
		res.attrs = append(res.attrs, "error", clip(err.Error(), 300))
		return res
	}
	after := a.instance().ConfigDigest
	acked := append([]string{}, req.AcknowledgeWarnings...)
	sort.Strings(acked)
	return actionResult{status: http.StatusOK, outcome: outcomeSuccess,
		body: map[string]any{"applied": true, "changed": plan.Changed, "plan": plan,
			"config_digest_before": before, "config_digest_after": after},
		attrs: []any{"change", "reload", "changed", plan.Changed,
			"remote_aes_added", aeNames(plan.RemoteAEs.Added), "remote_aes_removed", aeNames(plan.RemoteAEs.Removed),
			"remote_aes_changed", changedNames(plan.RemoteAEs.Changed),
			"allow_list_added", plan.AllowList.Added, "allow_list_removed", plan.AllowList.Removed,
			"allow_list_enforced", plan.AllowList.EnforcedAfter,
			"digest_before", before, "digest_after", after, "acknowledged_warnings", acked}}
}

func aeNames(v []RemoteAEView) []string {
	out := make([]string, 0, len(v))
	for _, r := range v {
		out = append(out, r.Name)
	}
	return out
}

func changedNames(v []RemoteAEChange) []string {
	out := make([]string, 0, len(v))
	for _, r := range v {
		out = append(out, r.Name)
	}
	return out
}
