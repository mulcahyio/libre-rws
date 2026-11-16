package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

// Pages for the configuration of one LibreDCM instance: running settings,
// C-MOVE destinations, calling-AE allow-list, edit of the two live
// settings, reload, rollback and the C-ECHO test (WT-SRS-019…022, -024).

// ---------------------------------------------------------------- views

// settingRow is one running setting as the configuration page shows it.
type settingRow struct {
	Key      string
	Lines    []string // the value; empty for a redacted setting
	Redacted bool
	IsSet    bool     // redacted setting: a value is configured
	SetKnown bool     // redacted setting: LibreDCM said whether one is
	Parts    []string // what LibreDCM removed from the value
	Source   string
	Env      string
	Live     bool // changes without a restart
}

// valueLines prints a setting value as text lines. A list gives one line
// per entry.
func valueLines(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case bool:
		return []string{strconv.FormatBool(t)}
	case float64:
		return []string{strconv.FormatFloat(t, 'f', -1, 64)}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, valueLines(e)...)
		}
		return out
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []string{fmt.Sprint(v)}
	}
	return []string{string(b)}
}

func settingRows(in []dcmclient.Setting) []settingRow {
	out := make([]settingRow, 0, len(in))
	for _, s := range in {
		row := settingRow{Key: s.Key, Redacted: s.Redacted, Parts: s.RedactedParts, Source: s.Source, Env: s.Env, Live: s.Reloadable}
		if s.Redacted {
			// Only "set" or "not set" is ever shown (WT-SRS-019, -032),
			// whatever else the answer holds.
			if s.Set != nil {
				row.SetKnown, row.IsSet = true, *s.Set
			}
		} else {
			row.Lines = valueLines(s.Value)
		}
		out = append(out, row)
	}
	return out
}

// configPage shows the settings the instance runs with (WT-SRS-019).
func (s *Server) configPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "config", "Configuration") {
		return
	}
	v, err := ic.client.Config(r.Context())
	if err != nil {
		s.readFailed(w, r, rc, ic, "config.view", "Configuration", err)
		return
	}
	if !s.sameNode(w, r, rc, ic, v.Instance) {
		return
	}
	allowed, why := ic.snap.ActionsAllowed()
	data := map[string]any{"Settings": settingRows(v.Settings), "RestartNote": v.RestartNote, "Digest": v.Instance.ConfigDigest,
		"ReadAt": time.Now(), "Allowed": allowed, "Blocked": why,
		"CanEdit": isAdmin(rc) && ic.snap.Supports("config-edit"), "CanReload": isAdmin(rc) && ic.snap.Supports("reload"),
		"EditOff": !ic.snap.Supports("config-edit")}
	if ic.snap.Supports("config-edit") {
		h, err := ic.client.ConfigHistory(r.Context())
		switch {
		case err != nil:
			data["HistoryErr"] = err.Error()
		case h.Instance.Identity() != ic.snap.Reported:
			data["HistoryErr"] = "another node answered"
		default:
			data["History"] = h
		}
	}
	if !s.viewed(w, r, rc, ic, "config.view", "configuration") {
		return
	}
	s.renderI(w, r, http.StatusOK, "config", rc, ic, data)
}

// remoteAEsPage shows the C-MOVE destinations in force (WT-SRS-020).
func (s *Server) remoteAEsPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "remote-aes", "Destinations") {
		return
	}
	v, err := ic.client.RemoteAEs(r.Context())
	if err != nil {
		s.readFailed(w, r, rc, ic, "remote-aes.view", "Destinations", err)
		return
	}
	if !s.sameNode(w, r, rc, ic, v.Instance) {
		return
	}
	if !s.viewed(w, r, rc, ic, "remote-aes.view", "destinations") {
		return
	}
	s.renderI(w, r, http.StatusOK, "remoteaes", rc, ic, map[string]any{"List": v.RemoteAEs, "ReadAt": time.Now(),
		"CanEdit": isAdmin(rc) && ic.snap.Supports("config-edit"), "CanEcho": ic.snap.Supports("echo")})
}

// allowListPage shows the calling-AE allow-list in force and says clearly
// when it is empty (WT-SRS-020).
func (s *Server) allowListPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "allow-list", "Allow-list") {
		return
	}
	v, err := ic.client.AllowList(r.Context())
	if err != nil {
		s.readFailed(w, r, rc, ic, "allow-list.view", "Allow-list", err)
		return
	}
	if !s.sameNode(w, r, rc, ic, v.Instance) {
		return
	}
	if !s.viewed(w, r, rc, ic, "allow-list.view", "allow-list") {
		return
	}
	s.renderI(w, r, http.StatusOK, "allowlist", rc, ic, map[string]any{"L": v, "ReadAt": time.Now(),
		"CanEdit": isAdmin(rc) && ic.snap.Supports("config-edit")})
}

// ---------------------------------------------------------------- edit form

const (
	maxDestinations = 200 // LibreDCM's limits (control-api-v1.md §5.3)
	maxAllowRules   = 500
	blankRows       = 3
)

// destRow is one row of the destinations form.
type destRow struct {
	Name, Host, Port, AETitle string
	Existing                  bool // in force now: the name is fixed
	Env                       bool // set by an environment variable: read-only
	Remove                    bool
}

// editForm is what the edit page shows.
type editForm struct {
	Rows       []destRow
	Rules      string // one rule per line
	AllowEnv   bool   // the allow-list is set by an environment variable
	AllowEmpty bool   // no rule in force: every calling AE may connect
}

// loadEditForm reads the two live settings in force and builds the form.
func (s *Server) loadEditForm(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) (editForm, bool) {
	var f editForm
	ra, err := ic.client.RemoteAEs(r.Context())
	if err != nil {
		s.readFailed(w, r, rc, ic, "config.edit.view", "Configuration", err)
		return f, false
	}
	if !s.sameNode(w, r, rc, ic, ra.Instance) {
		return f, false
	}
	al, err := ic.client.AllowList(r.Context())
	if err != nil {
		s.readFailed(w, r, rc, ic, "config.edit.view", "Configuration", err)
		return f, false
	}
	if !s.sameNode(w, r, rc, ic, al.Instance) {
		return f, false
	}
	for _, d := range ra.RemoteAEs {
		f.Rows = append(f.Rows, destRow{Name: d.Name, Host: d.Host, Port: strconv.Itoa(d.Port), AETitle: d.AETitle,
			Existing: true, Env: d.Source == "env"})
	}
	for i := 0; i < blankRows; i++ {
		f.Rows = append(f.Rows, destRow{})
	}
	var rules []string
	for _, e := range al.Entries {
		rules = append(rules, e.Rule)
		f.AllowEnv = f.AllowEnv || e.Source == "env"
	}
	f.Rules, f.AllowEmpty = strings.Join(rules, "\n"), len(rules) == 0
	return f, true
}

// configEditPage shows the forms for the two settings that change without
// a restart (WT-SRS-021).
func (s *Server) configEditPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "config-edit", "Configuration edit") {
		return
	}
	f, ok := s.loadEditForm(w, r, rc, ic)
	if !ok {
		return
	}
	if !s.viewed(w, r, rc, ic, "config.edit.view", "configuration") {
		return
	}
	allowed, why := ic.snap.ActionsAllowed()
	s.renderI(w, r, http.StatusOK, "configedit", rc, ic, map[string]any{"F": f, "Allowed": allowed, "Blocked": why, "Back": ic.base() + "/config"})
}

// parseDestinations reads the destinations form: the whole new table. Rows
// ticked "remove" and empty rows are left out. Destinations set by the
// environment are not in the form and are not sent: LibreDCM keeps them.
func parseDestinations(r *http.Request) ([]dcmclient.RemoteAE, []destRow, error) {
	names, hosts, ports, aes := r.PostForm["name"], r.PostForm["host"], r.PostForm["port"], r.PostForm["ae_title"]
	if len(hosts) != len(names) || len(ports) != len(names) || len(aes) != len(names) {
		return nil, nil, fmt.Errorf("the form is incomplete; open the edit page again")
	}
	if len(names) > maxDestinations+blankRows {
		return nil, nil, fmt.Errorf("too many destinations (%d at most)", maxDestinations)
	}
	existing, remove := map[string]bool{}, map[string]bool{}
	for _, n := range r.PostForm["existing"] {
		existing[n] = true
	}
	for _, n := range r.PostForm["remove"] {
		remove[n] = true
	}
	var list []dcmclient.RemoteAE
	var rows []destRow
	var firstErr error
	for i := range names {
		row := destRow{Name: strings.TrimSpace(names[i]), Host: strings.TrimSpace(hosts[i]), Port: strings.TrimSpace(ports[i]),
			AETitle: strings.TrimSpace(aes[i])}
		row.Existing, row.Remove = existing[row.Name], remove[row.Name]
		if row.Name == "" && row.Host == "" && row.Port == "" && row.AETitle == "" {
			continue
		}
		rows = append(rows, row)
		if row.Remove {
			continue
		}
		port, err := strconv.Atoi(row.Port)
		if err != nil || port < 1 || port > 65535 {
			if firstErr == nil {
				firstErr = fmt.Errorf("destination %q: the port must be a number from 1 to 65535", row.Name)
			}
			continue
		}
		list = append(list, dcmclient.RemoteAE{Name: row.Name, Host: row.Host, Port: port, AETitle: row.AETitle})
	}
	for i := 0; i < blankRows; i++ {
		rows = append(rows, destRow{})
	}
	if list == nil {
		list = []dcmclient.RemoteAE{}
	}
	return list, rows, firstErr
}

// parseRules reads the allow-list form: one rule per line.
func parseRules(text string) ([]string, error) {
	rules := []string{}
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			rules = append(rules, line)
		}
	}
	if len(rules) > maxAllowRules {
		return nil, fmt.Errorf("too many allow-list rules (%d at most)", maxAllowRules)
	}
	return rules, nil
}

// ---------------------------------------------------------------- plans

func destText(d dcmclient.RemoteAE) string {
	return fmt.Sprintf("%s=%s (AE %s)", d.Name, net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), d.AETitle)
}

// planSummary says in one line what a plan changes, for the audit records.
func planSummary(kind string, p dcmclient.ChangePlan) string {
	var parts []string
	for _, d := range p.RemoteAEs.Added {
		parts = append(parts, "+destination "+destText(d))
	}
	for _, d := range p.RemoteAEs.Removed {
		parts = append(parts, "-destination "+destText(d))
	}
	for _, c := range p.RemoteAEs.Changed {
		parts = append(parts, "destination "+destText(c.From)+" -> "+destText(c.To))
	}
	for _, rule := range p.AllowList.Added {
		parts = append(parts, "+rule "+rule)
	}
	for _, rule := range p.AllowList.Removed {
		parts = append(parts, "-rule "+rule)
	}
	if p.AllowList.EnforcedBefore != p.AllowList.EnforcedAfter {
		parts = append(parts, fmt.Sprintf("allow-list enforced %t -> %t", p.AllowList.EnforcedBefore, p.AllowList.EnforcedAfter))
	}
	if len(parts) == 0 {
		parts = []string{"no change"}
	}
	var codes []string
	for _, w := range p.Warnings {
		codes = append(codes, w.Code)
	}
	sort.Strings(codes)
	out := kind + ": " + strings.Join(parts, "; ")
	if len(codes) > 0 {
		out += "; warnings: " + strings.Join(codes, ", ")
	}
	return out
}

// planFailed shows why LibreDCM gave no plan. Nothing was changed: a plan
// request changes nothing.
func (s *Server) planFailed(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, act, target, title, back string, err error) {
	code, msg, _, data := s.refusalPage(r, ic, err, "")
	_ = s.auditI(r, rc.user.Username, ic.name, act+".review", target, "failure", clip(msg, 600))
	data["Title"], data["Message"], data["Back"] = title+": not possible", msg, back
	s.renderI(w, r, code, "error", rc, ic, data)
}

// changeView is what the warning page of an edit, reload or rollback needs.
type changeView struct {
	Kind     string // "edit", "reload", "rollback"
	Title    string
	Apply    string // address of the apply step
	Back     string
	Plan     dcmclient.ChangePlan
	Acks     []dcmclient.PlanWarning // each gets its own box
	Notes    []dcmclient.PlanWarning // warnings without a box
	Previous *dcmclient.OverlayVersion
	Confirm  string
}

// showChange issues the confirmation for a plan and renders the warning
// page (WT-SRS-021, -022, -031).
func (s *Server) showChange(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, act string, v changeView, p *pending) {
	if v.Plan.PlanDigest == "" {
		// Without LibreDCM's digest the apply step could not be tied to
		// this plan.
		_ = s.auditI(r, rc.user.Username, ic.name, act+".review", p.target, "failure", "LibreDCM sent a plan without a digest")
		s.renderI(w, r, http.StatusBadGateway, "error", rc, ic, map[string]any{"Title": v.Title + ": not possible",
			"Message": "LibreDCM sent a plan without a digest, so it can't be approved. Nothing was changed.", "Back": v.Back})
		return
	}
	p.planDigest, p.acks, p.summary = v.Plan.PlanDigest, mustAck(v.Plan), planSummary(v.Kind, v.Plan)
	v.Acks = p.acks
	for _, wn := range v.Plan.Warnings {
		if !wn.MustAcknowledge {
			v.Notes = append(v.Notes, wn)
		}
	}
	v.Confirm = s.issueAction(r, rc, ic, act, p)
	s.renderI(w, r, http.StatusOK, "change", rc, ic, map[string]any{"V": v, "Back": v.Back})
}

// blocked refuses a review while Libre WT sends nothing to the instance.
func (s *Server) blocked(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, act, target, back string) bool {
	ok, why := ic.snap.ActionsAllowed()
	if ok {
		return false
	}
	_ = s.auditI(r, rc.user.Username, ic.name, act+".review", target, "failure", why)
	s.renderI(w, r, http.StatusConflict, "error", rc, ic, map[string]any{"Title": "Actions are blocked for this instance", "Message": why, "Back": back})
	return true
}

// configReview takes the edit form, asks LibreDCM for the plan and shows
// the warning page with the exact difference (WT-SRS-021).
func (s *Server) configReview(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	back := ic.base() + "/config/edit"
	if !s.need(w, r, rc, ic, "config-edit", "Configuration edit") {
		return
	}
	var edit dcmclient.ConfigEdit
	var target string
	var rows []destRow
	var ferr error
	rulesText := r.PostFormValue("rules")
	switch r.PostFormValue("setting") {
	case "remote_aes":
		var list []dcmclient.RemoteAE
		list, rows, ferr = parseDestinations(r)
		edit.RemoteAEs, target = &list, dcmclient.KeyRemoteAEs
	case "allow_list":
		var rules []string
		rules, ferr = parseRules(rulesText)
		edit.AllowedAEs, target = &rules, dcmclient.KeyAllowedAEs
	default:
		s.renderI(w, r, http.StatusBadRequest, "error", rc, ic, map[string]any{"Title": "Unknown setting",
			"Message": "Only the destinations and the allow-list can be changed here.", "Back": back})
		return
	}
	if s.blocked(w, r, rc, ic, actConfigEdit, target, back) {
		return
	}
	// again shows the form with what the person typed and the reason it
	// was not accepted.
	again := func(msg string) {
		_ = s.auditI(r, rc.user.Username, ic.name, actConfigEdit+".review", target, "failure", clip(msg, 600))
		f, ok := s.loadEditForm(w, r, rc, ic)
		if !ok {
			return
		}
		if edit.RemoteAEs != nil {
			var env []destRow
			for _, row := range f.Rows {
				if row.Env {
					env = append(env, row)
				}
			}
			f.Rows = append(env, rows...)
		} else {
			f.Rules = rulesText
		}
		s.renderI(w, r, http.StatusUnprocessableEntity, "configedit", rc, ic, map[string]any{"F": f, "Allowed": true,
			"Error": msg, "ErrorFor": r.PostFormValue("setting"), "Back": ic.base() + "/config"})
	}
	if ferr != nil {
		again(ferr.Error() + ". Nothing was changed.")
		return
	}
	plan, err := ic.client.ConfigPlan(r.Context(), edit, ic.snap.Expect())
	if err != nil {
		if dcmclient.Refused(err) {
			// Invalid entry, set by the environment, …: LibreDCM's reason.
			again(err.Error() + ". Nothing was changed.")
			return
		}
		s.planFailed(w, r, rc, ic, actConfigEdit, target, "Configuration change", back, err)
		return
	}
	title := "Change the C-MOVE destinations"
	if edit.AllowedAEs != nil {
		title = "Change the calling-AE allow-list"
	}
	s.showChange(w, r, rc, ic, actConfigEdit, changeView{Kind: "edit", Title: title, Apply: ic.base() + "/config/apply", Back: back, Plan: plan},
		&pending{target: target, edit: &edit})
}

// reloadReview asks LibreDCM what a reload would change and shows the
// warning page (WT-SRS-022).
func (s *Server) reloadReview(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	back := ic.base() + "/config"
	if !s.need(w, r, rc, ic, "reload", "Reload") || s.blocked(w, r, rc, ic, actReload, "reload", back) {
		return
	}
	plan, err := ic.client.ReloadPlan(r.Context(), ic.snap.Expect())
	if err != nil {
		s.planFailed(w, r, rc, ic, actReload, "reload", "Reload", back, err)
		return
	}
	s.showChange(w, r, rc, ic, actReload, changeView{Kind: "reload", Title: "Reload the configuration", Apply: ic.base() + "/reload/apply", Back: back, Plan: plan},
		&pending{target: "reload"})
}

// rollbackReview asks LibreDCM what putting the previous console-made
// version back would change and shows the warning page (WT-SRS-022).
func (s *Server) rollbackReview(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	back := ic.base() + "/config"
	if !s.need(w, r, rc, ic, "config-edit", "Rollback") || s.blocked(w, r, rc, ic, actRollback, "rollback", back) {
		return
	}
	plan, prev, err := ic.client.RollbackPlan(r.Context(), ic.snap.Expect())
	if err != nil {
		s.planFailed(w, r, rc, ic, actRollback, "rollback", "Rollback", back, err)
		return
	}
	s.showChange(w, r, rc, ic, actRollback, changeView{Kind: "rollback", Title: "Roll back to the previous version", Apply: ic.base() + "/rollback/apply",
		Back: back, Plan: plan, Previous: prev}, &pending{target: "rollback"})
}

// ---------------------------------------------------------------- apply

// changeApply is the apply step of an edit, a reload and a rollback. send
// makes the one request to LibreDCM.
func (s *Server) changeApply(act, title string, send func(*http.Request, *instCtx, *pending, []string, dcmclient.Envelope) (dcmclient.ChangeResult, error)) func(http.ResponseWriter, *http.Request, *reqCtx, *instCtx) {
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
		back := ic.base() + "/config"
		p, env, acks, ok := s.approveAction(w, r, rc, ic, act, title, back, nil)
		if !ok {
			return
		}
		res, err := send(r, ic, p, acks, env)
		if err != nil {
			s.actionFailed(w, r, rc, ic, act, title, back, p, env, err)
			return
		}
		_ = s.auditI(r, rc.user.Username, ic.name, act, p.target, "success",
			fmt.Sprintf("applied=%t changed=%t digest %s -> %s request_id=%s", res.Applied, res.Changed, res.DigestBefore, res.DigestAfter, env.RequestID))
		// The settings digest of the node changed: read it again, so the
		// fleet page compares the new one.
		ic.snap, _ = s.fleet.Fresh(r.Context(), ic.name)
		s.renderI(w, r, http.StatusOK, "changed", rc, ic, map[string]any{"Title": title, "R": res, "RequestID": env.RequestID, "Back": back})
	}
}

func (s *Server) sendEdit(r *http.Request, ic *instCtx, p *pending, acks []string, env dcmclient.Envelope) (dcmclient.ChangeResult, error) {
	return ic.client.ConfigApply(r.Context(), *p.edit, p.planDigest, acks, env)
}

func (s *Server) sendReload(r *http.Request, ic *instCtx, p *pending, acks []string, env dcmclient.Envelope) (dcmclient.ChangeResult, error) {
	return ic.client.Reload(r.Context(), p.planDigest, acks, env)
}

func (s *Server) sendRollback(r *http.Request, ic *instCtx, p *pending, acks []string, env dcmclient.Envelope) (dcmclient.ChangeResult, error) {
	return ic.client.Rollback(r.Context(), p.planDigest, acks, env)
}

// ---------------------------------------------------------------- C-ECHO

// echoPage lists the destinations to test and, for an Admin, a form for a
// free address (WT-SRS-024).
func (s *Server) echoPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	if !s.need(w, r, rc, ic, "echo", "C-ECHO test") {
		return
	}
	data := map[string]any{}
	if ic.snap.Supports("remote-aes") {
		v, err := ic.client.RemoteAEs(r.Context())
		if err != nil {
			s.readFailed(w, r, rc, ic, "echo.view", "C-ECHO test", err)
			return
		}
		if !s.sameNode(w, r, rc, ic, v.Instance) {
			return
		}
		data["List"] = v.RemoteAEs
	}
	if !s.viewed(w, r, rc, ic, "echo.view", "destinations") {
		return
	}
	data["Allowed"], data["Blocked"] = ic.snap.ActionsAllowed()
	s.renderI(w, r, http.StatusOK, "echo", rc, ic, data)
}

func shownDest(d dcmclient.RemoteAE) string {
	return d.Name + "\x00" + d.Host + "\x00" + strconv.Itoa(d.Port) + "\x00" + d.AETitle
}

// findDest reads the destinations in force and returns the one named.
func (s *Server) findDest(r *http.Request, ic *instCtx, name string) (dcmclient.RemoteAE, bool, error) {
	v, err := ic.client.RemoteAEs(r.Context())
	if err != nil {
		return dcmclient.RemoteAE{}, false, err
	}
	if v.Instance.Identity() != ic.snap.Reported {
		return dcmclient.RemoteAE{}, false, fmt.Errorf("another node answered than the one this page is for")
	}
	for _, d := range v.RemoteAEs {
		if d.Name == name {
			return d, true, nil
		}
	}
	return dcmclient.RemoteAE{}, false, nil
}

// echoView is what the C-ECHO warning and result pages show of the target.
type echoView struct {
	Configured bool
	Name       string // configured destination
	Addr       string // host:port
	AETitle    string
	TLS        bool
}

// echoReview shows what the test will do and on which instance, and issues
// the confirmation. An Operator can test a configured destination only; a
// free host, port and AE title is for an Admin (WT-SRS-024).
func (s *Server) echoReview(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	back := ic.base() + "/echo"
	if !s.need(w, r, rc, ic, "echo", "C-ECHO test") {
		return
	}
	bad := func(code int, target, msg string) {
		_ = s.auditI(r, rc.user.Username, ic.name, actEcho+".review", target, "failure", msg)
		s.renderI(w, r, code, "error", rc, ic, map[string]any{"Title": "C-ECHO test: not possible", "Message": msg, "Back": back})
	}
	var p *pending
	var v echoView
	if name := r.PostFormValue("remote_ae"); name != "" {
		if s.blocked(w, r, rc, ic, actEcho, clip(name, 64), back) {
			return
		}
		d, found, err := s.findDest(r, ic, name)
		if err != nil {
			bad(http.StatusBadGateway, clip(name, 64), err.Error())
			return
		}
		if !found {
			bad(http.StatusNotFound, clip(name, 64), "No destination with this name is configured on this instance. Nothing was sent.")
			return
		}
		v = echoView{Configured: true, Name: d.Name, Addr: net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), AETitle: d.AETitle}
		p = &pending{target: d.Name, echo: &dcmclient.EchoTarget{RemoteAE: d.Name}, shown: shownDest(d),
			summary: "C-ECHO to destination " + destText(d)}
	} else {
		host, ae := strings.TrimSpace(r.PostFormValue("host")), strings.TrimSpace(r.PostFormValue("ae_title"))
		target := clip(host, 64)
		if !isAdmin(rc) {
			s.forbidden(w, r, rc, ic, actEcho+".review", target, "a C-ECHO test to a free address is for administrators")
			return
		}
		if s.blocked(w, r, rc, ic, actEcho, target, back) {
			return
		}
		port, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("port")))
		if host == "" || len(host) > 253 || strings.ContainsAny(host, " /@:?#\\") && net.ParseIP(host) == nil ||
			err != nil || port < 1 || port > 65535 || ae == "" || len(ae) > 16 {
			bad(http.StatusUnprocessableEntity, target, "Give a host name or IP address, a port from 1 to 65535 and an AE title of 1 to 16 characters. Nothing was sent.")
			return
		}
		t := dcmclient.EchoTarget{Host: host, Port: port, AETitle: ae, TLS: r.PostFormValue("tls") == "yes"}
		v = echoView{Addr: net.JoinHostPort(host, strconv.Itoa(port)), AETitle: ae, TLS: t.TLS}
		p = &pending{target: v.Addr, echo: &t, shown: fmt.Sprintf("free\x00%s\x00%s\x00%t", v.Addr, ae, t.TLS),
			summary: fmt.Sprintf("C-ECHO to free address %s (AE %s, TLS %t)", v.Addr, ae, t.TLS)}
	}
	tok := s.issueAction(r, rc, ic, actEcho, p)
	s.renderI(w, r, http.StatusOK, "echoreview", rc, ic, map[string]any{"T": v, "Confirm": tok, "Back": back})
}

// echoApply runs the approved test and shows success, or the stage that
// failed, and the duration.
func (s *Server) echoApply(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	back := ic.base() + "/echo"
	const title = "C-ECHO test"
	p, env, _, ok := s.approveAction(w, r, rc, ic, actEcho, title, back, func(p *pending) (int, string) {
		if p.echo.RemoteAE == "" {
			if !isAdmin(rc) {
				return http.StatusForbidden, "A C-ECHO test to a free address is for administrators. Nothing was sent."
			}
			return 0, ""
		}
		// The destination must still be the one on the warning page.
		d, found, err := s.findDest(r, ic, p.echo.RemoteAE)
		switch {
		case err != nil:
			return http.StatusBadGateway, err.Error() + ". Nothing was sent."
		case !found || shownDest(d) != p.shown:
			return http.StatusConflict, "The destination changed after you reviewed the test. Nothing was sent. Review the test again."
		}
		return 0, ""
	})
	if !ok {
		return
	}
	res, err := ic.client.Echo(r.Context(), *p.echo, env)
	if err != nil {
		s.actionFailed(w, r, rc, ic, actEcho, title, back, p, env, err)
		return
	}
	outcome, detail := "success", fmt.Sprintf("ok in %d ms", res.DurationMS)
	if !res.OK {
		outcome, detail = "failure", fmt.Sprintf("failed at stage %s: %s (%d ms)", res.Stage, res.Error, res.DurationMS)
	}
	_ = s.auditI(r, rc.user.Username, ic.name, actEcho, p.target, outcome,
		clip(fmt.Sprintf("%s; target %s called AE %s; request_id=%s", detail, res.Target, res.CalledAE, env.RequestID), 600))
	s.renderI(w, r, http.StatusOK, "echoresult", rc, ic, map[string]any{"R": res, "RequestID": env.RequestID, "Back": back})
}
