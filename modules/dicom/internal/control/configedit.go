package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

// Configuration edit (control-api-v1.md §5.3, DCM-SRS-093, -094, -095). Only
// the two settings that change without a restart can be edited: the C-MOVE
// destinations and the calling-AE allow-list. The control API does not
// write files or apply settings itself: it asks the node's ConfigEditor,
// which validates the change with the normal loader, writes it to the
// overlay file LibreDCM owns and puts it in force through the normal
// reload. The administrator's config.yaml is never rewritten.

// The settings that can be edited.
const (
	keyRemoteAEs  = "dicom.remote_aes"
	keyAllowedAEs = "dicom.allowed_aes"
)

// ConfigEdit is a change of the live settings. A nil setting is not changed.
type ConfigEdit struct {
	RemoteAEs  *map[string]gateway.RemoteAE
	AllowedAEs *[]string
}

// OverlayMeta says who asked for an overlay version, when and why.
type OverlayMeta struct {
	Time      time.Time
	Operator  string
	Reason    string
	RequestID string
}

// OverlayVersion describes one version of the overlay file.
type OverlayVersion struct {
	Time      string `json:"time"`
	Operator  string `json:"operator"`
	Reason    string `json:"reason"`
	RequestID string `json:"request_id"`
	// Kind: "edit", "rollback", or "initial" for the state before the
	// first change.
	Kind string `json:"kind"`
	// Digest covers the settings the version holds.
	Digest string `json:"digest"`
	// Settings names the settings the version holds.
	Settings []string `json:"settings"`
}

// ErrNoPreviousVersion: there is no kept overlay version to go back to.
var ErrNoPreviousVersion = errors.New("there is no previous configuration version to go back to")

// ErrConfigWrite: the overlay file could not be written or did not read
// back as written. The editor wraps it; the running settings are unchanged.
var ErrConfigWrite = errors.New("the configuration overlay could not be written")

// errNoChange stops an apply whose plan changes nothing: no file is
// written, so the kept previous version stays the one before the last real
// change.
var errNoChange = errors.New("nothing to change")

// ConfigEditor is the node's reloader, seen as the writer of the overlay.
// Every method that changes something runs as one step with the reload: no
// other reload (SIGHUP included) can come in between.
type ConfigEditor interface {
	// Enabled reports whether this node has an overlay file.
	Enabled() bool
	// Managed reports which live settings the overlay supplies now.
	Managed() (remoteAEs, allowedAEs bool)
	// PreviewEdit validates an edit with the normal loader and returns
	// what would be in force. Nothing changes.
	PreviewEdit(ConfigEdit) (LiveSettings, error)
	// ApplyEdit validates an edit, calls check with what would be in
	// force and, when check returns nil, keeps the current overlay as the
	// previous version, writes the new one and reloads. On any error the
	// overlay and the running settings stay as they were.
	ApplyEdit(edit ConfigEdit, meta OverlayMeta, check func(LiveSettings) error) (LiveSettings, error)
	// PreviewRollback and ApplyRollback do the same for putting the
	// previous overlay version back.
	PreviewRollback() (LiveSettings, error)
	ApplyRollback(meta OverlayMeta, check func(LiveSettings) error) (LiveSettings, error)
	// History returns the current and the previous overlay version; nil
	// where there is none.
	History() (current, previous *OverlayVersion, err error)
}

// ConfigRequest is the body of POST /control/v1/config and .../config/plan.
// Settings holds the whole new value of one or both editable settings:
//
//	{"settings": {"dicom.remote_aes": [{"name": "VIEWER1", "host": "10.0.0.20", "port": 104, "ae_title": "VIEWER1"}],
//	              "dicom.allowed_aes": ["CT1@10.0.0.5", "MR1"]}}
type ConfigRequest struct {
	Envelope
	Settings            map[string]json.RawMessage `json:"settings,omitempty"`
	PlanDigest          string                     `json:"plan_digest,omitempty"`
	Confirm             bool                       `json:"confirm,omitempty"`
	AcknowledgeWarnings []string                   `json:"acknowledge_warnings,omitempty"`
}

// remoteAEInput is one destination in a request.
type remoteAEInput struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	AETitle string `json:"ae_title"`
}

// registerConfigEdit adds the configuration edit endpoints. A node without
// an overlay file does not serve them and does not list them.
func (a *API) registerConfigEdit(r Registrar) {
	if a.Editor == nil || !a.Editor.Enabled() {
		return
	}
	r.HandleControl("POST /control/v1/config/plan", preview(a, a.configPreview))
	// One change at a time is kept by the editor itself (it is the reloader).
	r.HandleControl("POST /control/v1/config", action(a, actionSpec{name: "config", event: auditConfigChange,
		timeout: time.Minute}, a.configApply))
	r.HandleControl("GET /control/v1/config/history", http.HandlerFunc(a.configHistory))
	r.HandleControl("POST /control/v1/config/rollback/plan", preview(a, a.rollbackPreview))
	r.HandleControl("POST /control/v1/config/rollback", action(a, actionSpec{name: "config/rollback", event: auditConfigChange,
		timeout: time.Minute}, a.rollbackApply))
}

// strictJSON decodes raw into v and refuses unknown fields and trailing data.
func strictJSON(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the value")
	}
	return nil
}

// editRefusal is a request for a change that is not allowed.
type editRefusal struct {
	status    int
	code, msg string
}

// editorOff answers when this node cannot take configuration changes.
func (a *API) editorOff() *editRefusal {
	if a.Editor == nil || !a.Editor.Enabled() {
		return &editRefusal{http.StatusUnprocessableEntity, "config_edit_off",
			"configuration edit is off on this node: control.overlay_file is empty. Edit config.yaml on the host and reload."}
	}
	return nil
}

// parseEdit turns the settings of a request into an edit. It refuses every
// key other than the two live settings (DCM-SRS-093): a setting that needs
// a restart can never be changed through the API, whatever its value.
func (a *API) parseEdit(settings map[string]json.RawMessage) (ConfigEdit, *editRefusal) {
	var edit ConfigEdit
	unprocessable := func(code, msg string) (ConfigEdit, *editRefusal) {
		return ConfigEdit{}, &editRefusal{http.StatusUnprocessableEntity, code, msg}
	}
	if len(settings) == 0 {
		return unprocessable("nothing_to_change", "settings is empty: give the new value of "+keyRemoteAEs+" or "+keyAllowedAEs)
	}
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		raw := settings[key]
		switch key {
		case keyRemoteAEs:
			var list []remoteAEInput
			if err := strictJSON(raw, &list); err != nil || list == nil {
				return unprocessable("invalid_config", keyRemoteAEs+" must be a list of {name, host, port, ae_title}")
			}
			live := map[string]gateway.RemoteAE{}
			if a.Live != nil {
				live = a.Live.RemoteAEs()
			}
			m := map[string]gateway.RemoteAE{}
			for _, in := range list {
				r := gateway.RemoteAE{Host: in.Host, Port: in.Port, AETitle: in.AETitle}
				if err := gateway.ValidateRemoteAE(in.Name, r); err != nil {
					return unprocessable("invalid_config", err.Error())
				}
				if _, dup := m[in.Name]; dup {
					return unprocessable("invalid_config", "remote AE "+in.Name+" is listed twice")
				}
				if a.RemoteAEEnv[in.Name] {
					// The environment fixes this destination and wins over
					// every file. It may be sent back as it is (a client
					// sends the whole table); it cannot be changed.
					want := r
					if want.AETitle == "" {
						want.AETitle = in.Name
					}
					if cur, ok := live[in.Name]; !ok || cur != want {
						return unprocessable("set_by_environment", "remote AE "+in.Name+
							" is set by the environment variable LIBRE_DCM_REMOTE_AES and cannot be changed here")
					}
					continue // it stays in the environment only
				}
				m[in.Name] = r
			}
			edit.RemoteAEs = &m
		case keyAllowedAEs:
			if env := a.allowListEnv(); env != "" {
				return unprocessable("set_by_environment",
					keyAllowedAEs+" is set by the environment variable "+env+" and cannot be changed here")
			}
			var list []string
			if err := strictJSON(raw, &list); err != nil || list == nil {
				return unprocessable("invalid_config", keyAllowedAEs+` must be a list of "AE", "AE@IP" or "AE@CIDR"`)
			}
			for _, entry := range list {
				if strings.TrimSpace(entry) == "" {
					return unprocessable("invalid_config", keyAllowedAEs+" holds an empty entry")
				}
			}
			rules, err := gateway.NormalizeAllowedAEs(list)
			if err != nil {
				return unprocessable("invalid_config", err.Error())
			}
			if rules == nil {
				rules = []string{}
			}
			edit.AllowedAEs = &rules
		default:
			for _, s := range a.Settings {
				if s.Key == key {
					return unprocessable("restart_required", key+" needs a restart and cannot be changed through the control API. "+
						"Change it in config.yaml or the environment on the host and restart the node.")
				}
			}
			return unprocessable("unknown_setting", "unknown setting. Only "+keyRemoteAEs+" and "+keyAllowedAEs+" can be changed here.")
		}
	}
	return edit, nil
}

// editFailure maps an editor error to a response.
func editFailure(err error) (status int, code, msg string) {
	switch {
	case errors.Is(err, ErrReloadBlocked):
		return http.StatusLocked, "license_ack_required", err.Error()
	case errors.Is(err, ErrNoPreviousVersion):
		return http.StatusUnprocessableEntity, "no_previous_version", ErrNoPreviousVersion.Error()
	case errors.Is(err, ErrConfigWrite):
		return http.StatusInternalServerError, "config_write_failed",
			"the configuration could not be written; the overlay file and the running settings are unchanged: " + err.Error()
	}
	return http.StatusUnprocessableEntity, "invalid_config",
		"the change does not load or validate; the running settings are unchanged: " + err.Error()
}

func (a *API) configPreview(_ context.Context, req *ConfigRequest) (int, map[string]any) {
	if off := a.editorOff(); off != nil {
		return off.status, map[string]any{"error": off.msg, "code": off.code}
	}
	edit, ref := a.parseEdit(req.Settings)
	if ref != nil {
		return ref.status, map[string]any{"error": ref.msg, "code": ref.code}
	}
	next, err := a.Editor.PreviewEdit(edit)
	if err != nil {
		status, code, msg := editFailure(err)
		return status, map[string]any{"error": msg, "code": code}
	}
	return http.StatusOK, map[string]any{"plan": a.diffPlan(next, planEdit)}
}

func (a *API) rollbackPreview(_ context.Context, _ *ReloadRequest) (int, map[string]any) {
	if off := a.editorOff(); off != nil {
		return off.status, map[string]any{"error": off.msg, "code": off.code}
	}
	next, err := a.Editor.PreviewRollback()
	if err != nil {
		status, code, msg := editFailure(err)
		return status, map[string]any{"error": msg, "code": code}
	}
	body := map[string]any{"plan": a.diffPlan(next, planRollback)}
	if _, prev, err := a.Editor.History(); err == nil && prev != nil {
		body["previous"] = prev
	}
	return http.StatusOK, body
}

func (a *API) configApply(_ context.Context, req *ConfigRequest) actionResult {
	if off := a.editorOff(); off != nil {
		return refusal(off.status, off.code, off.msg, true)
	}
	edit, ref := a.parseEdit(req.Settings)
	if ref != nil {
		return refusal(ref.status, ref.code, ref.msg, true)
	}
	return a.commitChange(planEdit, &req.Envelope, req.Confirm, req.PlanDigest, req.AcknowledgeWarnings,
		func(meta OverlayMeta, check func(LiveSettings) error) (LiveSettings, error) {
			return a.Editor.ApplyEdit(edit, meta, check)
		})
}

func (a *API) rollbackApply(_ context.Context, req *ReloadRequest) actionResult {
	if off := a.editorOff(); off != nil {
		return refusal(off.status, off.code, off.msg, true)
	}
	return a.commitChange(planRollback, &req.Envelope, req.Confirm, req.PlanDigest, req.AcknowledgeWarnings, a.Editor.ApplyRollback)
}

// commitChange is the apply step of an edit and of a rollback. The plan is
// built again inside the editor, while it holds the configuration, and the
// change is written only when that plan has the digest the person approved
// and every warning that must be acknowledged is named.
func (a *API) commitChange(kind string, env *Envelope, confirm bool, digest string, acks []string,
	apply func(OverlayMeta, func(LiveSettings) error) (LiveSettings, error)) actionResult {
	planPath := "/control/v1/config/plan"
	if kind == planRollback {
		planPath = "/control/v1/config/rollback/plan"
	}
	if !confirm {
		return refusal(http.StatusUnprocessableEntity, "not_confirmed",
			"change not confirmed: show the plan and set confirm=true after the administrator approves", true)
	}
	if digest == "" {
		return refusal(http.StatusUnprocessableEntity, "plan_digest_required",
			"plan_digest is required: ask for the plan first (POST "+planPath+")", true)
	}
	before := a.instance().ConfigDigest
	var plan ReloadPlan
	meta := OverlayMeta{Time: time.Now(), Operator: env.Operator, Reason: env.Reason, RequestID: env.RequestID}
	_, err := apply(meta, func(next LiveSettings) error {
		plan = a.diffPlan(next, kind)
		if err := planGuard(plan, digest, acks); err != nil {
			return err
		}
		if !plan.Changed {
			return errNoChange
		}
		return nil
	})
	var pe *planError
	switch {
	case errors.Is(err, errNoChange):
		return actionResult{status: http.StatusOK, outcome: outcomeSuccess,
			body: map[string]any{"applied": false, "changed": false, "plan": plan,
				"config_digest_before": before, "config_digest_after": before,
				"note": "The settings in force already are what was asked for. Nothing was written."},
			attrs: []any{"change", kind, "changed", false, "digest_before", before, "digest_after", before}}
	case errors.As(err, &pe):
		return refusal(pe.status, pe.code, pe.msg, true, "change", kind, "config_digest", before)
	case err != nil:
		status, code, msg := editFailure(err)
		res := refusal(status, code, msg, false, "change", kind, "config_digest", before)
		res.attrs = append(res.attrs, "error", clip(err.Error(), 300))
		return res
	}
	after := a.instance().ConfigDigest
	acked := append([]string{}, acks...)
	sort.Strings(acked)
	body := map[string]any{"applied": true, "changed": true, "plan": plan,
		"config_digest_before": before, "config_digest_after": after}
	if cur, prev, err := a.Editor.History(); err == nil {
		body["current"], body["previous"] = cur, prev
	}
	return actionResult{status: http.StatusOK, outcome: outcomeSuccess, body: body,
		attrs: []any{"change", kind, "changed", true,
			"remote_aes_added", aeNames(plan.RemoteAEs.Added), "remote_aes_removed", aeNames(plan.RemoteAEs.Removed),
			"remote_aes_changed", changedNames(plan.RemoteAEs.Changed),
			"allow_list_added", plan.AllowList.Added, "allow_list_removed", plan.AllowList.Removed,
			"allow_list_enforced", plan.AllowList.EnforcedAfter,
			"digest_before", before, "digest_after", after, "acknowledged_warnings", acked}}
}

func (a *API) configHistory(w http.ResponseWriter, _ *http.Request) {
	body := map[string]any{"instance": a.instance(), "enabled": false, "current": nil, "previous": nil,
		"editable": []string{keyAllowedAEs, keyRemoteAEs},
		"note": "Changes made here are kept in an overlay file that LibreDCM owns. A setting the overlay holds " +
			"replaces the value of config.yaml as a whole; environment variables still win. " +
			"One previous version is kept for rollback."}
	if a.editorOff() != nil {
		body["note"] = "Configuration edit is off on this node (control.overlay_file is empty)."
		a.writeJSON(w, http.StatusOK, body)
		return
	}
	cur, prev, err := a.Editor.History()
	if err != nil {
		a.failCode(w, http.StatusInternalServerError, "overlay_unreadable",
			"the configuration overlay cannot be read: "+err.Error())
		return
	}
	remote, allowed := a.Editor.Managed()
	managed := []string{}
	if allowed {
		managed = append(managed, keyAllowedAEs)
	}
	if remote {
		managed = append(managed, keyRemoteAEs)
	}
	body["enabled"], body["current"], body["previous"], body["in_force_from_overlay"] = true, cur, prev, managed
	a.writeJSON(w, http.StatusOK, body)
}
