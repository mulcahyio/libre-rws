package runner

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/plan"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

// engineTLS turns a target's TLS block into the worker's TLS options:
// paths and names only (DVT-SRS-153).
func engineTLS(t *store.Target) *engine.TLS {
	if t == nil || t.TLS == nil {
		return nil
	}
	c := t.TLS
	return &engine.TLS{MinVersion: c.MinVersion, CAFile: c.CAFile, ServerName: c.ServerName,
		ClientCert: c.ClientCertFile, ClientKey: c.ClientKeyFile, MoveClientCert: c.MoveClientCert}
}

func findExchange(xs []result.Exchange, op string) *result.Exchange {
	for i := range xs {
		if xs[i].Operation == op {
			return &xs[i]
		}
	}
	return nil
}

func rolesText(r result.Roles) string {
	return fmt.Sprintf("scu=%t scp=%t", r.SCU, r.SCP)
}

// associate runs an associate step (DVT-SRS-090…-096): the worker records
// what the target answered; the assertions and the verdict are decided
// here and in result.DecideStep, never by the worker.
func (rn *Runner) associate(ctx context.Context, p *plan.Plan, ps plan.Step, t *store.Target, s *result.Step) {
	ev := result.NewEvidence(t.Info())
	req := engine.AssociateRequest{}
	for _, c := range ps.Contexts {
		ac := engine.AssocContext{AbstractSyntax: c.AbstractSyntax, TransferSyntaxes: c.TransferSyntaxes}
		if c.Roles != nil {
			ac.Roles = &result.Roles{SCU: c.Roles.SCU, SCP: c.Roles.SCP}
		}
		req.Contexts = append(req.Contexts, ac)
	}
	if ps.AsyncOps != nil {
		req.AsyncOps = &result.AsyncOps{Invoked: ps.AsyncOps.Invoked, Performed: ps.AsyncOps.Performed}
	}
	res, _, err := rn.Engines.Associate(ctx, rn.peer(p, t), req)
	if err != nil {
		ev.Problemf(result.ProblemEngine, "engine error: %v", err)
		s.ApplyEvidence(ev)
		return
	}
	ev.Exchanges = res.Exchanges
	// Every failed exchange is a problem (DVT-SRS-095), except the release:
	// a failed release is a warning (DVT-SRS-096).
	for _, x := range ev.Exchanges {
		if x.Error != nil && x.Operation != engine.OpRelease {
			ev.Problemf(x.Error.Kind, "%s: %s", x.Operation, x.Error.Message)
		}
	}
	ax := findExchange(ev.Exchanges, engine.OpAssociate)
	if ax == nil || ax.Association == nil {
		if len(ev.Errors) == 0 {
			ev.Problemf(result.ProblemEngine, "no association attempt was recorded")
		}
		s.ApplyEvidence(ev)
		addTLSNote(s, ev.Exchanges)
		return
	}
	a := ax.Association
	endpoint := t.Endpoint()
	expect := "accepted"
	if ps.Expect != nil && ps.Expect.Association != "" {
		expect = ps.Expect.Association
	}
	ev.Assert("assoc.result", endpoint, expect, a.Result, "")
	if expect == "rejected" {
		// DVT-SRS-092: exactly the expected Result, Source and Reason.
		want := ps.Expect.Reject
		got := "no A-ASSOCIATE-RJ"
		if a.Reject != nil {
			got = rejectText(a.Reject.Result, a.Reject.Source, a.Reject.Reason)
		}
		ev.Assert("assoc.reject", endpoint, rejectText(want.Result, want.Source, want.Reason), got, "PS3.8 Table 9-21 values")
	} else if a.Result == "accepted" {
		checkContexts(ev, ps, a)
		checkNegotiated(ev, ps, a, endpoint)
		// DVT-SRS-096: a failed release is a warning, never a pass of a
		// failed check (DecideStep fails on any failed assertion first).
		if a.Released {
			ev.Assert("assoc.release", endpoint, "released", "released", "")
		} else {
			why := "no A-RELEASE-RP"
			if rx := findExchange(ev.Exchanges, engine.OpRelease); rx != nil && rx.Error != nil {
				why = rx.Error.Message
			}
			ev.Warn("assoc.release", endpoint, "released", "not released", why)
		}
	}
	s.ApplyEvidence(ev)
	addTLSNote(s, ev.Exchanges)
	s.Messages = append(s.Messages, fmt.Sprintf("association %s; max PDU %d, Implementation Class UID %q, Version Name %q",
		a.Result, a.MaxPDU, a.ImplClassUID, a.ImplVersion))
}

func rejectText(result, source, reason int) string {
	return fmt.Sprintf("result %d, source %d, reason %d", result, source, reason)
}

// checkContexts applies DVT-SRS-090, -091 and -094 to every proposed
// context.
func checkContexts(ev *result.Evidence, ps plan.Step, a *result.AssociationInfo) {
	for i, c := range ps.Contexts {
		ci := a.Contexts[i]
		subj := fmt.Sprintf("context %d (%s)", ci.ID, c.AbstractSyntax)
		if ci.Result == "missing" {
			ev.Problemf(result.ProblemProtocol, "%s: not answered in the A-ASSOCIATE-AC", subj)
		}
		ev.Assert("assoc.context.result", subj, c.Expect, ci.Result, ci.Reason)
		if ci.Result == "accepted" && !contains(ci.ProposedTransferSyntaxes, ci.AcceptedTransferSyntax) {
			ev.Problemf(result.ProblemProtocol, "%s: accepted transfer syntax %q was not proposed", subj, ci.AcceptedTransferSyntax)
		}
		if c.ExpectTransferSyntax != "" {
			got := ci.AcceptedTransferSyntax
			if ci.Result != "accepted" || got == "" {
				got = "none"
			}
			ev.Assert("assoc.context.transfer_syntax", subj, c.ExpectTransferSyntax, got, "")
		}
		if c.ExpectRoles != nil {
			want := rolesText(result.Roles{SCU: c.ExpectRoles.SCU, SCP: c.ExpectRoles.SCP})
			got, msg := "", ""
			if ci.Roles != nil {
				got = rolesText(*ci.Roles)
			} else {
				// No reply: the default roles apply (PS3.7 D.3.3.4).
				got, msg = rolesText(result.Roles{SCU: true, SCP: false}), "no role selection reply; default roles"
			}
			ev.Assert("assoc.context.roles", subj, want, got, msg)
		}
	}
}

// checkNegotiated applies DVT-SRS-093: each named expectation is checked;
// the values are recorded in any case.
func checkNegotiated(ev *result.Evidence, ps plan.Step, a *result.AssociationInfo, endpoint string) {
	e := ps.Expect
	if e == nil {
		return
	}
	if e.MaxPDU != nil {
		ev.Assert("assoc.max_pdu", endpoint, strconv.Itoa(*e.MaxPDU), strconv.Itoa(a.MaxPDU), "")
	}
	if e.ImplClassUID != "" {
		ev.Assert("assoc.impl_class_uid", endpoint, e.ImplClassUID, a.ImplClassUID, "")
	}
	if e.ImplVersion != "" {
		ev.Assert("assoc.impl_version", endpoint, e.ImplVersion, a.ImplVersion, "")
	}
}

// probeVerdict maps what the worker saw during a refusal probe to
// refused, accepted, or a problem kind when the outcome does not show
// either (DVT-SRS-105). Only a clear refusal passes.
func probeVerdict(mode, obs string) (verdict, kind string) {
	switch obs {
	case engine.ObsTimeout:
		return "", result.ProblemTimeout
	case engine.ObsConnectError:
		return "", result.ProblemConnectionClosed
	case engine.ObsLocalError:
		return "", result.ProblemEngine
	}
	switch mode {
	case "plaintext":
		switch obs {
		case engine.ObsNonDICOMReply, engine.ObsConnectionClosed, engine.ObsConnectionReset:
			return "refused", ""
		case engine.ObsDICOMReply:
			return "accepted", ""
		}
	case "tls_version":
		switch obs {
		case engine.ObsHandshakeAlert, engine.ObsConnectionClosed, engine.ObsConnectionReset:
			return "refused", ""
		case engine.ObsHandshakeCompleted, engine.ObsCertVerifyFailed:
			// The target negotiated the old version (it sent its certificate).
			return "accepted", ""
		case engine.ObsHandshakeError:
			return "", result.ProblemTLS
		}
	case "no_client_cert":
		switch obs {
		case engine.ObsHandshakeAlert, engine.ObsConnectionClosed, engine.ObsConnectionReset:
			return "refused", ""
		case engine.ObsDICOMReply:
			return "accepted", ""
		case engine.ObsCertVerifyFailed, engine.ObsHandshakeError:
			// LibreDVT could not complete its own side; nothing is shown.
			return "", result.ProblemTLS
		}
	}
	return "", result.ProblemEngine
}

// tlsReject runs a tls_reject step against a DIMSE target (DVT-SRS-105,
// -106).
func (rn *Runner) tlsReject(ctx context.Context, p *plan.Plan, ps plan.Step, t *store.Target, s *result.Step) {
	ev := result.NewEvidence(t.Info())
	res, _, err := rn.Engines.TLSProbe(ctx, rn.peer(p, t), ps.Mode, ps.TLSVersion)
	if err != nil {
		ev.Problemf(result.ProblemEngine, "engine error: %v", err)
		s.ApplyEvidence(ev)
		return
	}
	ev.Exchanges = res.Exchanges
	subj := ps.Mode
	if ps.Mode == "tls_version" {
		subj = "TLS " + ps.TLSVersion + " only"
	}
	verdict, kind := probeVerdict(ps.Mode, res.Observation)
	switch verdict {
	case "refused":
		ev.Assert("tls_reject.refused", subj, "refused", "refused", res.Observation+": "+res.Detail)
	case "accepted":
		ev.Assert("tls_reject.refused", subj, "refused", "accepted", res.Observation+": "+res.Detail)
	default:
		ev.Problemf(kind, "could not show a refusal (%s): %s", res.Observation, res.Detail)
	}
	s.ApplyEvidence(ev)
}

// addTLSNote puts the TLS facts of the step's handshake into the messages
// every report shows (DVT-SRS-107).
func addTLSNote(s *result.Step, xs []result.Exchange) {
	if x := findExchange(xs, engine.OpTLS); x != nil && x.TLS != nil {
		s.Messages = append(s.Messages, tlsText(x.TLS))
	}
}

func tlsText(i *result.TLSInfo) string {
	if !i.Verified {
		msg := "TLS: not established"
		if i.VerifyError != "" {
			msg += " (" + i.VerifyError + ")"
		}
		return msg
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "TLS: %s %s, certificate verified", i.Version, i.Cipher)
	if pc := i.Peer; pc != nil {
		fmt.Fprintf(b, "; peer subject %q, issuer %q, SAN DNS %v, SAN IP %v, valid %s to %s, SHA-256 %s",
			pc.Subject, pc.Issuer, pc.SANDNS, pc.SANIP, pc.NotBefore, pc.NotAfter, pc.SHA256)
	}
	fmt.Fprintf(b, "; client certificate offered: %t", i.ClientCertSent)
	return b.String()
}

// v01TLS checks the TLS facts of a v0.1 step against a TLS target: without
// them the step fails, so a clear-text fallback can never pass
// (DVT-SRS-100, -107).
func v01TLS(s *result.Step, t *store.Target, info *result.TLSInfo, tlsErr string) bool {
	if t == nil || t.TLS == nil {
		return true
	}
	if info == nil || !info.Verified || info.Version == "" {
		why := tlsErr
		if why == "" {
			why = "the engine reported no verified TLS connection"
		}
		s.Failf("TLS error: " + why)
		return false
	}
	s.Messages = append(s.Messages, tlsText(info))
	return true
}
