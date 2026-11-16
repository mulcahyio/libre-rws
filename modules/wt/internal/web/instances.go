package web

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fleet"
)

// Pages for one or more LibreDCM instances: fleet overview, dashboard,
// status, about, Study Exceptions per instance and acceptance of a changed
// instance identity (WT-SRS-013…018, -023, -035, -036).

// instCtx is the LibreDCM instance a request is for. It comes from the
// address of the request (/i/{inst}/…) and from nowhere else: Libre WT keeps
// no "selected instance" in the session (WT-SRS-014).
type instCtx struct {
	name   string
	client fleet.Client
	snap   fleet.Snapshot
}

func (ic *instCtx) base() string { return "/i/" + ic.name }

// inst resolves the instance named in the address and reads its state (from
// the short cache). An unknown name is a 404 page.
func (s *Server) inst(h func(http.ResponseWriter, *http.Request, *reqCtx, *instCtx)) func(http.ResponseWriter, *http.Request, *reqCtx) {
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
		name := r.PathValue("inst")
		client, ok := s.fleet.Client(name)
		if !ok {
			s.render(w, r, http.StatusNotFound, "error", rc, map[string]any{
				"Title": "Unknown instance", "Message": "No LibreDCM instance with this name is configured in Libre WT.", "Back": "/"})
			return
		}
		snap, _ := s.fleet.Snapshot(r.Context(), name)
		h(w, r, rc, &instCtx{name: name, client: client, snap: snap})
	}
}

// renderI renders a page of one instance: the layout then shows the instance
// banner (name, AE title, node ID, cluster label) and its warnings.
func (s *Server) renderI(w http.ResponseWriter, r *http.Request, code int, page string, rc *reqCtx, ic *instCtx, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["I"] = ic.snap
	data["Base"] = ic.base()
	if _, ok := data["Back"]; !ok {
		data["Back"] = ic.base() + "/"
	}
	s.render(w, r, code, page, rc, data)
}

// ---------------------------------------------------------------- view helpers

func fmtStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05 MST")
}

// uptime prints seconds as days, hours and minutes.
func uptime(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	d, h, m := sec/86400, sec%86400/3600, sec%3600/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", sec)
}

type count struct {
	Name string
	N    uint64
}

// counts lists a counter map by name, so a page always shows it in the same
// order.
func counts(m map[string]uint64) []count {
	out := make([]count, 0, len(m))
	for k, v := range m {
		out = append(out, count{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func licenceLabel(l dcmclient.License) string {
	if !l.Enforced {
		return "not enforced (source build)"
	}
	label := l.State
	if label == "" {
		label = "unknown"
	}
	if l.Licensed && l.DaysLeft > 0 {
		label += fmt.Sprintf(", %d days left", l.DaysLeft)
	}
	if l.AckRequired {
		label += ", notice not acknowledged"
	}
	return label
}

// stored splits the C-STORE counter into stored and not stored.
func stored(c dcmclient.Counters) (ok, other uint64) {
	for k, v := range c.CStore {
		if k == "success" {
			ok += v
		} else {
			other += v
		}
	}
	return ok, other
}

// function is a control function with a page in Libre WT. A later version
// adds its pages here; the dashboard shows each as available or not for the
// instance (WT-SRS-018).
type function struct {
	Feature string // name in LibreDCM's discovery answer
	Label   string
	Path    string // under /i/{inst}/
}

var functions = []function{
	{"status", "Status", "status"},
	{"conflicts", "Study Exceptions", "exceptions"},
}

type functionView struct {
	function
	Available bool
}

func functionViews(s fleet.Snapshot) []functionView {
	out := make([]functionView, 0, len(functions))
	for _, f := range functions {
		out = append(out, functionView{f, s.Supports(f.Feature)})
	}
	return out
}

// ---------------------------------------------------------------- fleet, about

type fleetRow struct {
	S         fleet.Snapshot
	Flags     fleet.Flags
	Stored    uint64
	NotStored uint64
}

type fleetGroup struct {
	Label string // "" for instances without a cluster label
	Rows  []fleetRow
}

// fleetPage lists every configured instance, grouped by cluster label
// (WT-SRS-016, -023).
func (s *Server) fleetPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	snaps := s.fleet.All(r.Context())
	flags := fleet.Divergence(snaps)
	var groups []fleetGroup
	at := map[string]int{}
	for _, sn := range snaps {
		row := fleetRow{S: sn, Flags: flags[sn.Name]}
		if sn.Status != nil {
			row.Stored, row.NotStored = stored(sn.Status.Counters)
		}
		i, ok := at[sn.Cluster]
		if !ok {
			i = len(groups)
			at[sn.Cluster] = i
			groups = append(groups, fleetGroup{Label: sn.Cluster})
		}
		groups[i].Rows = append(groups[i].Rows, row)
	}
	// Instances without a label come last.
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Label != "" && groups[j].Label == "" })
	s.render(w, r, http.StatusOK, "fleet", rc, map[string]any{"Groups": groups, "Now": time.Now()})
}

// aboutPage shows the Libre WT version and, per instance, the LibreDCM
// version, node ID and AE title (WT-SRS-035).
func (s *Server) aboutPage(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.render(w, r, http.StatusOK, "about", rc, map[string]any{"Snaps": s.fleet.All(r.Context())})
}

// instRoot sends "/i/name" to the dashboard "/i/name/". The target is built
// from the configured name of a known instance, never from the request text,
// so the redirect can't leave Libre WT.
func (s *Server) instRoot(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	for _, name := range s.fleet.Names() {
		if name == r.PathValue("inst") {
			http.Redirect(w, r, "/i/"+name+"/", http.StatusSeeOther)
			return
		}
	}
	s.render(w, r, http.StatusNotFound, "error", rc, map[string]any{
		"Title": "Unknown instance", "Message": "No LibreDCM instance with this name is configured in Libre WT.", "Back": "/"})
}

// legacyPath serves the addresses of Libre WT 0.1.
func (s *Server) legacyPath(w http.ResponseWriter, r *http.Request, _ *reqCtx) {
	names := s.fleet.Names()
	if len(names) != 1 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	target := "/i/" + names[0] + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	// #nosec G710 -- the target always starts with "/i/<configured name>/exceptions": a path on this server, never another host
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// ---------------------------------------------------------------- dashboard, status

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	data := map[string]any{"Functions": functionViews(ic.snap), "Back": "/"}
	if st := ic.snap.Status; st != nil {
		data["Stored"], data["NotStored"] = stored(st.Counters)
	}
	s.renderI(w, r, http.StatusOK, "dashboard", rc, ic, data)
}

// statusPage shows everything LibreDCM's status endpoint returns, with the
// time of the reading (WT-SRS-017). An instance without the status function
// gets a plain page that says so (WT-SRS-018).
func (s *Server) statusPage(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	s.renderI(w, r, http.StatusOK, "status", rc, ic, nil)
}

// ---------------------------------------------------------------- identity

func identityDigest(name string, remembered, reported dcmclient.Identity) string {
	return planDigest("identity", name, remembered.NodeID, remembered.AETitle, reported.NodeID, reported.AETitle)
}

const actIdentity = "identity.accept"

// identityReview shows the warning page for accepting a changed identity:
// what Libre WT remembers, what the node reports now, a reason field and the
// approval box (WT-SRS-015, -031).
func (s *Server) identityReview(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	snap, _ := s.fleet.Fresh(r.Context(), ic.name)
	ic.snap = snap
	if snap.State != fleet.IdentityChanged {
		s.renderI(w, r, http.StatusConflict, "error", rc, ic, map[string]any{"Title": "Nothing to accept",
			"Message": "The instance reports the identity Libre WT remembers, or it can't be reached."})
		return
	}
	_ = s.auditI(r, rc.user.Username, ic.name, "instance.identity.review", ic.name, "success",
		fmt.Sprintf("remembered node=%s ae=%s; reported node=%s ae=%s", snap.Remembered.NodeID, snap.Remembered.AETitle, snap.Reported.NodeID, snap.Reported.AETitle))
	tok := s.ss.issueConfirm(rc.token, ic.name, ic.name, actIdentity, identityDigest(ic.name, snap.Remembered, snap.Reported))
	s.renderI(w, r, http.StatusOK, "identity", rc, ic, map[string]any{"Confirm": tok})
}

// identityApply makes the reported identity the remembered one. Nothing is
// sent to LibreDCM: this changes only what Libre WT expects.
func (s *Server) identityApply(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	fail := func(code int, msg string) {
		_ = s.auditI(r, rc.user.Username, ic.name, "instance.identity.accept", ic.name, "failure", msg)
		s.renderI(w, r, code, "error", rc, ic, map[string]any{"Title": "Identity not accepted", "Message": msg})
	}
	digest, ok := s.ss.useConfirm(r.PostFormValue("confirm"), rc.token, ic.name, ic.name, actIdentity)
	if !ok {
		fail(http.StatusConflict, "The approval expired or was already used. Open the instance and review the identity again.")
		return
	}
	if r.PostFormValue("approve") != "yes" {
		fail(http.StatusUnprocessableEntity, "Tick the approval box to accept the new identity.")
		return
	}
	if reason == "" {
		fail(http.StatusUnprocessableEntity, "A reason is required; it is recorded in the audit trail.")
		return
	}
	if len(reason) > 500 {
		fail(http.StatusUnprocessableEntity, "The reason is too long (500 characters at most).")
		return
	}
	// The node must still report what the administrator saw.
	snap, _ := s.fleet.Fresh(r.Context(), ic.name)
	ic.snap = snap
	if snap.State != fleet.IdentityChanged || identityDigest(ic.name, snap.Remembered, snap.Reported) != digest {
		fail(http.StatusConflict, "The instance no longer reports the identity you reviewed. Review it again.")
		return
	}
	detail := fmt.Sprintf("node=%s ae=%s (was node=%s ae=%s): %s", snap.Reported.NodeID, snap.Reported.AETitle, snap.Remembered.NodeID, snap.Remembered.AETitle, reason)
	if err := s.auditI(r, rc.user.Username, ic.name, "instance.identity.accept.approved", ic.name, "success", detail); err != nil {
		fail(http.StatusInternalServerError, "The audit log can't be written, so the identity was not accepted.")
		return
	}
	if err := s.fleet.Accept(r.Context(), ic.name, snap.Reported, rc.user.Username, reason); err != nil {
		fail(http.StatusInternalServerError, "The identity could not be stored: "+err.Error())
		return
	}
	_ = s.auditI(r, rc.user.Username, ic.name, "instance.identity.accept", ic.name, "success", detail)
	ic.snap, _ = s.fleet.Snapshot(r.Context(), ic.name)
	s.renderI(w, r, http.StatusOK, "done", rc, ic, map[string]any{"Title": "Identity accepted",
		"Message": "Libre WT now expects node ID " + snap.Reported.NodeID + " and AE title " + snap.Reported.AETitle + " for this instance."})
}

// ---------------------------------------------------------------- exceptions

var stateFilters = []string{"active", "open", "accepted", "resolved", "all"}

func (s *Server) exceptions(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	state := r.URL.Query().Get("state")
	valid := false
	for _, f := range stateFilters {
		valid = valid || f == state
	}
	if !valid {
		state = "active"
	}
	data := map[string]any{"State": state, "Filters": stateFilters}
	if ic.snap.State != fleet.Unreachable && !ic.snap.Supports("conflicts") {
		data["Unsupported"] = true
		s.renderI(w, r, http.StatusOK, "exceptions", rc, ic, data)
		return
	}
	list, err := ic.client.Conflicts(r.Context(), state)
	if err != nil {
		data["Error"] = err.Error()
	}
	data["Conflicts"] = list
	s.renderI(w, r, http.StatusOK, "exceptions", rc, ic, data)
}

func (s *Server) exception(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	back := ic.base() + "/exceptions"
	d, err := ic.client.Conflict(r.Context(), r.PathValue("id"))
	if err != nil {
		code := http.StatusBadGateway
		if dcmclient.NotFound(err) {
			code = http.StatusNotFound
		}
		s.renderI(w, r, code, "error", rc, ic, map[string]any{"Title": "Exception not available", "Message": err.Error(), "Back": back})
		return
	}
	actions := []string{"reject"}
	if d.Conflict.Kind == "patient_id" {
		actions = []string{"correct_incoming", "correct_existing", "reject"}
	}
	allowed, why := ic.snap.ActionsAllowed()
	s.renderI(w, r, http.StatusOK, "exception", rc, ic, map[string]any{"D": d, "C": d.Conflict, "Actions": actions,
		"Allowed": allowed, "Blocked": why})
}

// digestOf fingerprints what the administrator approves: the instance and
// the node expected behind it, and LibreDCM's plan.
func digestOf(instance string, exp dcmclient.Identity, p dcmclient.Plan) string {
	parts := []string{instance, exp.NodeID, exp.AETitle, p.ConflictID, p.Action, p.Summary, strconv.Itoa(p.ExistingInstances), strconv.Itoa(p.IncomingInstances)}
	for _, c := range append(append([]dcmclient.Change(nil), p.ExistingChanges...), p.IncomingChanges...) {
		parts = append(parts, c.Attribute, c.From, c.To)
	}
	return planDigest(parts...)
}

// mismatch handles LibreDCM's refusal "this is not the node you expect": the
// state of the instance is read again, so its pages show the warning.
func (s *Server) mismatch(r *http.Request, ic *instCtx, err error) bool {
	if !dcmclient.InstanceMismatch(err) {
		return false
	}
	s.log.Warn("LibreDCM refused a request: not the expected node", "instance", ic.name)
	ic.snap, _ = s.fleet.Fresh(r.Context(), ic.name)
	return true
}

const mismatchMsg = "LibreDCM refused the request: the node that answered is not the node Libre WT expects for this instance. Nothing was changed."

// review shows the warning page: exactly what will change, a reason field and
// the approve-edit confirmation (WT-SRS-009).
func (s *Server) review(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	id, action := r.PathValue("id"), r.URL.Query().Get("action")
	back := ic.base() + "/exceptions/" + url.PathEscape(id)
	if ok, why := ic.snap.ActionsAllowed(); !ok {
		s.renderI(w, r, http.StatusConflict, "error", rc, ic, map[string]any{"Title": "Edits are blocked for this instance", "Message": why, "Back": back})
		return
	}
	d, err := ic.client.Conflict(r.Context(), id)
	if err != nil {
		s.renderI(w, r, http.StatusBadGateway, "error", rc, ic, map[string]any{"Title": "Exception not available", "Message": err.Error(), "Back": back})
		return
	}
	exp := ic.snap.Expect()
	p, err := ic.client.Plan(r.Context(), id, action, exp)
	if err != nil {
		if s.mismatch(r, ic, err) {
			s.renderI(w, r, http.StatusConflict, "error", rc, ic, map[string]any{"Title": "Wrong node", "Message": mismatchMsg, "Back": back})
			return
		}
		s.renderI(w, r, http.StatusUnprocessableEntity, "error", rc, ic, map[string]any{"Title": "This action isn't possible", "Message": err.Error(), "Back": back})
		return
	}
	_ = s.auditI(r, rc.user.Username, ic.name, "exception.review", id, "success", action)
	tok := s.ss.issueConfirm(rc.token, ic.name, id, action, digestOf(ic.name, exp, p))
	s.renderI(w, r, http.StatusOK, "review", rc, ic, map[string]any{"C": d.Conflict, "P": p, "Confirm": tok, "Action": action})
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx) {
	id, action := r.PathValue("id"), r.PostFormValue("action")
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	fail := func(code int, msg string) {
		_ = s.auditI(r, rc.user.Username, ic.name, "exception.resolve", id, "failure", action+": "+msg)
		s.renderI(w, r, code, "error", rc, ic, map[string]any{"Title": "Edit not made", "Message": msg, "Back": ic.base() + "/exceptions/" + url.PathEscape(id)})
	}
	// The confirmation belongs to the instance in this request's address:
	// an approval given for another instance is refused (WT-SRS-031).
	digest, ok := s.ss.useConfirm(r.PostFormValue("confirm"), rc.token, ic.name, id, action)
	if !ok {
		fail(http.StatusConflict, "The approval expired or was already used. Open the exception and review the change again.")
		return
	}
	if r.PostFormValue("approve") != "yes" {
		fail(http.StatusUnprocessableEntity, "Tick \"I approve this edit\" to confirm the change.")
		return
	}
	if reason == "" {
		fail(http.StatusUnprocessableEntity, "A reason is required; it is recorded in the audit trail.")
		return
	}
	if ok, why := ic.snap.ActionsAllowed(); !ok {
		fail(http.StatusConflict, why)
		return
	}
	// The plan must still be the one the administrator approved (new images
	// may have arrived since the warning page was shown), on the same node.
	exp := ic.snap.Expect()
	p, err := ic.client.Plan(r.Context(), id, action, exp)
	if err != nil {
		if s.mismatch(r, ic, err) {
			fail(http.StatusConflict, mismatchMsg)
			return
		}
		fail(http.StatusUnprocessableEntity, err.Error())
		return
	}
	if digestOf(ic.name, exp, p) != digest {
		fail(http.StatusConflict, "The change is no longer the one you approved (the exception or the instance was updated). Review it again.")
		return
	}
	// Record the intent first: if the audit log can't be written, don't edit.
	if err := s.auditI(r, rc.user.Username, ic.name, "exception.resolve.approved", id, "success", action+": "+reason); err != nil {
		fail(http.StatusInternalServerError, "The audit log can't be written, so the edit was not made.")
		return
	}
	res, err := ic.client.Resolve(r.Context(), id, action, rc.user.Username, reason, exp)
	if err != nil {
		if s.mismatch(r, ic, err) {
			fail(http.StatusConflict, mismatchMsg)
			return
		}
		fail(http.StatusBadGateway, err.Error())
		return
	}
	_ = s.auditI(r, rc.user.Username, ic.name, "exception.resolve", id, "success",
		fmt.Sprintf("%s: state=%s files_rewritten=%d", action, res.State, res.Rewritten))
	s.renderI(w, r, http.StatusOK, "resolved", rc, ic, map[string]any{"R": res, "ID": id})
}
