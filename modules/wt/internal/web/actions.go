package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fleet"
)

// The action flow of Libre WT 0.2 (architecture.md §5, WT-SRS-031): a
// review step that shows a warning page and issues a single-use
// confirmation, and an apply step that checks it and sends the action. The
// configuration edit, reload, rollback and C-ECHO test use it.

// Actions. The name is the audit action; ".review" and ".approved" are added
// for the two steps before the result.
const (
	actConfigEdit = "config.edit"
	actReload     = "config.reload"
	actRollback   = "config.rollback"
	actEcho       = "echo"
)

const maxReason = 500

// pending is what a warning page showed and what the apply step will send.
// It stays on the server with the confirmation: the apply request carries
// only the token, the reason and the ticked boxes.
type pending struct {
	target  string // what the action is for (audit target)
	summary string // what the page showed, for the audit records
	// planDigest is LibreDCM's digest of the plan shown. It is sent with
	// the action, so LibreDCM refuses when its state changed since.
	planDigest string
	// acks are the warnings that must be acknowledged, in page order: box
	// "ack_<i>" belongs to acks[i].
	acks []dcmclient.PlanWarning

	edit *dcmclient.ConfigEdit // configuration edit
	echo *dcmclient.EchoTarget // C-ECHO test
	// shown is the address a configured C-ECHO destination had on the
	// warning page.
	shown string
}

// bind fingerprints what an approval is for: the action, the instance, the
// node expected behind it, the target and LibreDCM's plan.
func bind(ic *instCtx, act string, p *pending) string {
	exp := ic.snap.Expect()
	return planDigest(act, ic.name, exp.NodeID, exp.AETitle, p.target, p.planDigest, p.shown)
}

// mustAck lists the warnings of a plan that need their own ticked box.
func mustAck(plan dcmclient.ChangePlan) []dcmclient.PlanWarning {
	var out []dcmclient.PlanWarning
	for _, w := range plan.Warnings {
		if w.MustAcknowledge {
			out = append(out, w)
		}
	}
	return out
}

// clip shortens text for an audit record.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// issueAction records the review and issues the confirmation for the
// warning page.
func (s *Server) issueAction(r *http.Request, rc *reqCtx, ic *instCtx, act string, p *pending) string {
	_ = s.auditI(r, rc.user.Username, ic.name, act+".review", p.target, "success", clip(p.summary, 1000))
	return s.ss.issuePending(rc.token, ic.name, p.target, act, bind(ic, act, p), p)
}

// approveAction is the apply step up to the point where the action may be
// sent: single-use confirmation of this session, instance and action;
// approval box; reason; every warning box; the instance still is the node
// that was reviewed; the approval is in the audit log. check, when given,
// runs before the approval is recorded and may refuse (status and message).
// On any refusal it records the failure, renders the page and returns false.
func (s *Server) approveAction(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, act, title, back string,
	check func(*pending) (int, string)) (*pending, dcmclient.Envelope, []string, bool) {
	target := ""
	fail := func(code int, msg string) (*pending, dcmclient.Envelope, []string, bool) {
		_ = s.auditI(r, rc.user.Username, ic.name, act, target, "failure", msg)
		s.renderI(w, r, code, "error", rc, ic, map[string]any{"Title": title + ": not done", "Message": msg, "Back": back})
		return nil, dcmclient.Envelope{}, nil, false
	}
	p, digest, ok := s.ss.usePending(r.PostFormValue("confirm"), rc.token, ic.name, act)
	if !ok {
		return fail(http.StatusConflict, "The approval expired or was already used. Nothing was sent. Review the action again.")
	}
	target = p.target
	if r.PostFormValue("approve") != "yes" {
		return fail(http.StatusUnprocessableEntity, "Tick the approval box to confirm. Nothing was sent.")
	}
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	if reason == "" {
		return fail(http.StatusUnprocessableEntity, "A reason is required; it is recorded in the audit trail. Nothing was sent.")
	}
	if len(reason) > maxReason {
		return fail(http.StatusUnprocessableEntity, "The reason is too long (500 characters at most). Nothing was sent.")
	}
	var acks []string
	seen := map[string]bool{}
	for i, wn := range p.acks {
		if r.PostFormValue("ack_"+strconv.Itoa(i)) != "yes" {
			return fail(http.StatusUnprocessableEntity, "Tick the box of every warning to acknowledge it. Nothing was sent.")
		}
		if !seen[wn.Code] {
			seen[wn.Code] = true
			acks = append(acks, wn.Code)
		}
	}
	if ok, why := ic.snap.ActionsAllowed(); !ok {
		return fail(http.StatusConflict, why)
	}
	if bind(ic, act, p) != digest {
		return fail(http.StatusConflict, "The instance is no longer the node you reviewed this for. Nothing was sent. Review the action again.")
	}
	if check != nil {
		if code, msg := check(p); code != 0 {
			return fail(code, msg)
		}
	}
	env := dcmclient.Envelope{Operator: rc.user.Username, Reason: reason, RequestID: dcmclient.NewRequestID(), Expect: ic.snap.Expect()}
	// Record the approval first: without the record, the action is not sent.
	detail := clip(p.summary, 600) + "; request_id=" + env.RequestID
	if len(acks) > 0 {
		detail += "; acknowledged: " + strings.Join(acks, ", ")
	}
	if err := s.auditI(r, rc.user.Username, ic.name, act+".approved", p.target, "success", detail+"; reason: "+reason); err != nil {
		return fail(http.StatusInternalServerError, "The audit log can't be written, so nothing was sent.")
	}
	return p, env, acks, true
}

// licencePath is the licence page of an instance, or "" while Libre WT has
// none.
func licencePath(ic *instCtx) string {
	for _, f := range functions {
		if f.Feature == "license" {
			return ic.base() + "/" + f.Path
		}
	}
	return ""
}

const licenceBlockMsg = "LibreDCM blocks configuration changes on this instance until the licence notice is acknowledged. " +
	"Nothing was changed. DICOM services keep running. Acknowledge the notice (on the host: LibreDCM -license-ack NAME), then try again."

// refusalPage turns an error of a plan or action request into a page:
// status, message and whether the outcome is known. Text from LibreDCM is
// shown as text.
func (s *Server) refusalPage(r *http.Request, ic *instCtx, err error, requestID string) (code int, msg, outcome string, data map[string]any) {
	data = map[string]any{}
	switch {
	case s.mismatch(r, ic, err):
		return http.StatusConflict, mismatchMsg, "failure", data
	case dcmclient.NoAnswer(err):
		msg = "LibreDCM gave no answer in time (" + err.Error() + ")."
		if requestID != "" {
			msg += " It is not known whether the action was done. Check the instance's pages and LibreDCM's audit records for request ID " +
				requestID + " before you try again."
		}
		return http.StatusGatewayTimeout, msg, "unknown", data
	case dcmclient.Code(err) == "plan_changed":
		return http.StatusConflict, "The settings of the instance changed after you reviewed this. Nothing was changed. Review the action again.", "failure", data
	case dcmclient.HTTPStatus(err) == http.StatusLocked:
		if p := licencePath(ic); p != "" {
			data["Link"], data["LinkText"] = p, "Open the licence page of this instance"
		}
		return http.StatusLocked, licenceBlockMsg, "failure", data
	}
	st := dcmclient.HTTPStatus(err)
	switch st {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests:
		// Refused before anything started.
		return st, err.Error() + ". Nothing was changed.", "failure", data
	}
	msg = err.Error() + "."
	if requestID != "" {
		msg += " Check LibreDCM's audit records for request ID " + requestID + "."
	}
	return http.StatusBadGateway, msg, "failure", data
}

// actionFailed records and shows an action that LibreDCM refused or did not
// answer.
func (s *Server) actionFailed(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, act, title, back string, p *pending, env dcmclient.Envelope, err error) {
	code, msg, outcome, data := s.refusalPage(r, ic, err, env.RequestID)
	_ = s.auditI(r, rc.user.Username, ic.name, act, p.target, outcome, clip(msg, 600)+" request_id="+env.RequestID)
	data["Title"], data["Message"], data["Back"] = title+": not done", msg, back
	if outcome == "unknown" {
		data["Title"] = title + ": outcome not known"
	}
	s.renderI(w, r, code, "error", rc, ic, data)
}

// need reports whether the instance serves a control function. When it
// does not, or can't be reached, it renders a plain page that says so
// (WT-SRS-018).
func (s *Server) need(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, feature, label string) bool {
	if ic.snap.State != fleet.Unreachable && ic.snap.Supports(feature) {
		return true
	}
	s.renderI(w, r, http.StatusOK, "unavailable", rc, ic, map[string]any{"Label": label,
		"Unreachable": ic.snap.State == fleet.Unreachable})
	return false
}

// sameNode checks that an answer came from the node the page banner names.
// When another node answered, nothing of the answer is shown.
func (s *Server) sameNode(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, got dcmclient.InstanceInfo) bool {
	if got.Identity() == ic.snap.Reported {
		return true
	}
	s.log.Warn("another node answered than the one just read", "instance", ic.name)
	ic.snap, _ = s.fleet.Fresh(r.Context(), ic.name)
	s.renderI(w, r, http.StatusConflict, "error", rc, ic, map[string]any{"Title": "The node changed",
		"Message": "Another node answered than the one this page is for. Nothing is shown. Open the page again."})
	return false
}

// readFailed shows that a page's data could not be read from LibreDCM.
func (s *Server) readFailed(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, action, label string, err error) {
	_ = s.auditI(r, rc.user.Username, ic.name, action, label, "failure", clip(err.Error(), 300))
	s.renderI(w, r, http.StatusBadGateway, "error", rc, ic, map[string]any{"Title": label + " not available", "Message": err.Error()})
}

// viewed records that a user opened a page of an instance. When the record
// can't be written the page is not shown (WT-SRS-033).
func (s *Server) viewed(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, action, target string) bool {
	if err := s.auditI(r, rc.user.Username, ic.name, action, target, "success", ""); err != nil {
		s.renderI(w, r, http.StatusInternalServerError, "error", rc, ic, map[string]any{"Title": "Page not shown",
			"Message": "The audit log can't be written, so the page is not shown."})
		return false
	}
	return true
}

// forbidden refuses a request the user's role does not allow, inside a
// handler that serves several roles.
func (s *Server) forbidden(w http.ResponseWriter, r *http.Request, rc *reqCtx, ic *instCtx, action, target, what string) {
	_ = s.auditI(r, rc.user.Username, ic.name, action, target, "failure", "role "+string(rc.user.Role)+" not allowed: "+what)
	s.renderI(w, r, http.StatusForbidden, "error", rc, ic, map[string]any{"Title": "Not allowed",
		"Message": fmt.Sprintf("Your role (%s) can't do this: %s.", rc.user.Role, what)})
}

func isAdmin(rc *reqCtx) bool { return rc.user.Role == accounts.Admin }
