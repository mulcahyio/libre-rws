package runner

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/fakepeer"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

const (
	verification = "1.2.840.10008.1.1"
	ctImage      = "1.2.840.10008.5.1.4.1.1.2"
	implicitLE   = "1.2.840.10008.1.2"
	explicitLE   = "1.2.840.10008.1.2.1"
)

const assocPlan = `
name: assoc
target: t
steps:
  - id: a
    type: associate
    contexts:
      - {abstract_syntax: "1.2.840.10008.1.1", transfer_syntaxes: ["1.2.840.10008.1.2"], expect: accepted}
      - {abstract_syntax: "1.2.840.10008.5.1.4.1.1.2", transfer_syntaxes: ["1.2.840.10008.1.2.1", "1.2.840.10008.1.2"], expect: accepted, expect_transfer_syntax: "1.2.840.10008.1.2.1", roles: {scu: true, scp: true}, expect_roles: {scu: true, scp: true}}
      - {abstract_syntax: "1.2.3.4.5", transfer_syntaxes: ["1.2.840.10008.1.2"], expect: rejected}
    expect: {max_pdu: 16384, impl_class_uid: "1.2.3.9", impl_version: "PEER_1"}
    async_ops: {invoked: 2, performed: 1}
`

func exch(seq int, proto, op, outcome string) result.Exchange {
	return result.Exchange{Seq: seq, Protocol: proto, Operation: op, Endpoint: "h:104", Outcome: outcome}
}

// goodAssoc is the worker's record of a peer that meets every expectation
// of assocPlan.
func goodAssoc() engine.AssociateResult {
	a := exch(1, "dimse", engine.OpAssociate, result.ExchangeCompleted)
	a.Association = &result.AssociationInfo{Result: "accepted", Released: true, MaxPDU: 16384, ImplClassUID: "1.2.3.9", ImplVersion: "PEER_1",
		AsyncOps: &result.AsyncOps{Invoked: 1, Performed: 1},
		Contexts: []result.ContextInfo{
			{ID: 1, AbstractSyntax: verification, ProposedTransferSyntaxes: []string{implicitLE}, Result: "accepted", AcceptedTransferSyntax: implicitLE},
			{ID: 3, AbstractSyntax: ctImage, ProposedTransferSyntaxes: []string{explicitLE, implicitLE}, Result: "accepted", AcceptedTransferSyntax: explicitLE, Roles: &result.Roles{SCU: true, SCP: true}},
			{ID: 5, AbstractSyntax: "1.2.3.4.5", ProposedTransferSyntaxes: []string{implicitLE}, Result: "rejected", Reason: "abstract-syntax-not-supported (3)"},
		}}
	return engine.AssociateResult{Exchanges: []result.Exchange{a, exch(2, "dimse", engine.OpRelease, result.ExchangeCompleted)}}
}

func runAssoc(t *testing.T, src string, mod func(*engine.AssociateResult)) (*result.Run, *fake) {
	t.Helper()
	f := &fake{assoc: func(engine.Peer, engine.AssociateRequest) engine.AssociateResult {
		r := goodAssoc()
		if mod != nil {
			mod(&r)
		}
		return r
	}}
	return run(t, f, setup(t, src), lab()), f
}

func failedIDs(ev *result.Evidence) string {
	var ids []string
	for _, a := range ev.Assertions {
		if a.Outcome != result.AssertPass {
			ids = append(ids, a.Outcome+":"+a.ID)
		}
	}
	for _, p := range ev.Errors {
		ids = append(ids, "problem:"+p.Kind)
	}
	return strings.Join(ids, ",")
}

// Verifies: DVT-SRS-090, DVT-SRS-091, DVT-SRS-093, DVT-SRS-094
func TestAssociatePositiveControls(t *testing.T) {
	r, f := runAssoc(t, assocPlan, nil)
	s := r.Steps[0]
	if r.Verdict != result.Pass || s.Verdict != result.Pass || s.Evidence == nil || len(s.Evidence.Assertions) != 10 {
		t.Fatalf("matching peer: %s %+v", r.Verdict, s)
	}
	// What LibreDVT proposed reached the engine.
	if len(f.calls) != 1 || f.calls[0] != "associate" {
		t.Fatalf("calls %v", f.calls)
	}
	if !strings.Contains(strings.Join(s.Messages, ";"), `Implementation Class UID "1.2.3.9", Version Name "PEER_1"`) {
		t.Fatalf("negotiated values not reported: %v", s.Messages)
	}
	cases := map[string]struct {
		mod  func(*engine.AssociateResult)
		want string // failed assertion ids / problems, in order
	}{
		"accepted context rejected": {func(r *engine.AssociateResult) {
			c := &r.Exchanges[0].Association.Contexts[0]
			c.Result, c.AcceptedTransferSyntax, c.Reason = "rejected", "", "abstract-syntax-not-supported (3)"
		}, "fail:assoc.context.result"},
		"rejected context accepted": {func(r *engine.AssociateResult) {
			c := &r.Exchanges[0].Association.Contexts[2]
			c.Result, c.AcceptedTransferSyntax, c.Reason = "accepted", implicitLE, ""
		}, "fail:assoc.context.result"},
		"other transfer syntax": {func(r *engine.AssociateResult) {
			r.Exchanges[0].Association.Contexts[1].AcceptedTransferSyntax = implicitLE
		}, "fail:assoc.context.transfer_syntax"},
		"context missing from AC": {func(r *engine.AssociateResult) {
			c := &r.Exchanges[0].Association.Contexts[0]
			c.Result, c.AcceptedTransferSyntax = "missing", ""
		}, "fail:assoc.context.result,problem:protocol"},
		"transfer syntax not proposed": {func(r *engine.AssociateResult) {
			r.Exchanges[0].Association.Contexts[0].AcceptedTransferSyntax = "1.2.840.10008.1.2.4.50"
		}, "problem:protocol"},
		"max pdu": {func(r *engine.AssociateResult) { r.Exchanges[0].Association.MaxPDU = 4096 }, "fail:assoc.max_pdu"},
		"impl class uid": {func(r *engine.AssociateResult) {
			r.Exchanges[0].Association.ImplClassUID = "1.2.3.10"
		}, "fail:assoc.impl_class_uid"},
		"impl version": {func(r *engine.AssociateResult) { r.Exchanges[0].Association.ImplVersion = "OTHER" }, "fail:assoc.impl_version"},
		"SCP role refused": {func(r *engine.AssociateResult) {
			r.Exchanges[0].Association.Contexts[1].Roles = &result.Roles{SCU: true, SCP: false}
		}, "fail:assoc.context.roles"},
		"no role reply": {func(r *engine.AssociateResult) {
			r.Exchanges[0].Association.Contexts[1].Roles = nil
		}, "fail:assoc.context.roles"},
		"association rejected": {func(r *engine.AssociateResult) {
			a := r.Exchanges[0].Association
			a.Result, a.Reject, a.Released = "rejected", &result.RejectInfo{Result: 1, Source: 1, Reason: 3}, false
			r.Exchanges[0].Outcome = result.ExchangeRefused
			r.Exchanges = r.Exchanges[:1]
		}, "fail:assoc.result"},
	}
	for name, c := range cases {
		r, _ := runAssoc(t, assocPlan, c.mod)
		s := r.Steps[0]
		if r.Verdict != result.Fail || s.Status != result.StepFailed {
			t.Errorf("%s: verdict %s", name, r.Verdict)
			continue
		}
		if got := failedIDs(s.Evidence); got != c.want {
			t.Errorf("%s: failed %q, want %q", name, got, c.want)
		}
	}
	// No role reply against an expectation of the default roles passes.
	src := strings.Replace(assocPlan, "roles: {scu: true, scp: true}, expect_roles: {scu: true, scp: true}", "roles: {scu: true, scp: true}, expect_roles: {scu: true, scp: false}", 1)
	r, _ = runAssoc(t, src, func(r *engine.AssociateResult) { r.Exchanges[0].Association.Contexts[1].Roles = nil })
	if r.Verdict != result.Pass {
		t.Fatalf("default roles: %s %v", r.Verdict, r.Steps[0].Messages)
	}
}

const rejectPlan = `
name: rj
target: t
steps:
  - id: rj
    type: associate
    contexts:
      - {abstract_syntax: "1.2.840.10008.1.1", transfer_syntaxes: ["1.2.840.10008.1.2"]}
    expect: {association: rejected, reject: {result: 1, source: 1, reason: 7}}
`

func rejected(res, src, reason int) func(*engine.AssociateResult) {
	return func(r *engine.AssociateResult) {
		a := r.Exchanges[0].Association
		a.Result, a.Released, a.Reject = "rejected", false, &result.RejectInfo{Result: res, Source: src, Reason: reason}
		a.Contexts = []result.ContextInfo{{ID: 1, AbstractSyntax: verification, ProposedTransferSyntaxes: []string{implicitLE}, Result: "rejected", Reason: "association rejected"}}
		r.Exchanges[0].Outcome = result.ExchangeRefused
		r.Exchanges = r.Exchanges[:1]
	}
}

// Verifies: DVT-SRS-092
func TestAssociateExpectedRejection(t *testing.T) {
	r, _ := runAssoc(t, rejectPlan, rejected(1, 1, 7))
	if r.Verdict != result.Pass {
		t.Fatalf("matching rejection: %s %v", r.Verdict, r.Steps[0].Messages)
	}
	for name, mod := range map[string]func(*engine.AssociateResult){
		"other reason":     rejected(1, 1, 3),
		"other source":     rejected(1, 3, 1),
		"transient result": rejected(2, 1, 7),
		"accepted": func(r *engine.AssociateResult) {
			a := r.Exchanges[0].Association
			a.Contexts = a.Contexts[:1]
		},
	} {
		r, _ := runAssoc(t, rejectPlan, mod)
		if r.Verdict != result.Fail {
			t.Errorf("%s: verdict %s", name, r.Verdict)
		}
	}
}

// Verifies: DVT-SRS-095, DVT-SRS-096, DVT-SRS-150
func TestAssociateAbortCloseGarbageTimeoutAndRelease(t *testing.T) {
	events := map[string]struct {
		kind, msg, result string
	}{
		"abort":   {result.ProblemProtocol, "the target aborted the association (A-ABORT source 2, reason 0)", "aborted"},
		"close":   {result.ProblemConnectionClosed, "the target closed the connection without answering", "no_response"},
		"garbage": {result.ProblemProtocol, "malformed PDU: unknown PDU type 0x47", "no_response"},
		"timeout": {result.ProblemTimeout, "no answer within the timeout", "no_response"},
	}
	for name, e := range events {
		r, _ := runAssoc(t, assocPlan, func(r *engine.AssociateResult) {
			x := &r.Exchanges[0]
			x.Outcome, x.Error = result.ExchangeError, &result.Problem{Kind: e.kind, Message: e.msg}
			x.Association.Result, x.Association.Released = e.result, false
			for i := range x.Association.Contexts {
				x.Association.Contexts[i].Result, x.Association.Contexts[i].AcceptedTransferSyntax = "missing", ""
			}
			r.Exchanges = r.Exchanges[:1]
		})
		s := r.Steps[0]
		if r.Verdict != result.Fail || !strings.Contains(strings.Join(s.Messages, ";"), e.msg) || s.Evidence.Errors[0].Kind != e.kind {
			t.Errorf("%s: %s %v", name, r.Verdict, s.Messages)
		}
	}
	// A peer that aborts on release: pass with warnings when every check
	// passed, fail when one failed.
	abortOnRelease := func(r *engine.AssociateResult) {
		r.Exchanges[0].Association.Released = false
		r.Exchanges[1].Outcome = result.ExchangeError
		r.Exchanges[1].Error = &result.Problem{Kind: result.ProblemProtocol, Message: "the target aborted instead of releasing (A-ABORT source 2, reason 0)"}
	}
	r, _ := runAssoc(t, assocPlan, abortOnRelease)
	if r.Verdict != result.PassWithWarnings || r.Steps[0].Verdict != result.PassWithWarnings || r.Steps[0].Status != result.StepPassed {
		t.Fatalf("abort on release, checks passed: %s %v", r.Verdict, r.Steps[0].Messages)
	}
	r, _ = runAssoc(t, assocPlan, func(r *engine.AssociateResult) {
		abortOnRelease(r)
		r.Exchanges[0].Association.MaxPDU = 1
	})
	if r.Verdict != result.Fail {
		t.Fatalf("abort on release, check failed: %s", r.Verdict)
	}
	// Engine failure and a TLS failure fail the step.
	f := &fake{err: os.ErrNotExist}
	if r := run(t, f, setup(t, assocPlan), lab()); r.Verdict != result.Fail || r.Steps[0].Evidence.Errors[0].Kind != result.ProblemEngine {
		t.Fatalf("engine error: %s", r.Verdict)
	}
	r, _ = runAssoc(t, assocPlan, func(r *engine.AssociateResult) {
		x := exch(1, "tls", engine.OpTLS, result.ExchangeError)
		x.Error = &result.Problem{Kind: result.ProblemTLS, Message: "certificate verification failed: Hostname mismatch"}
		x.TLS = &result.TLSInfo{VerifyError: "certificate verification failed: Hostname mismatch"}
		r.Exchanges = []result.Exchange{x}
	})
	if r.Verdict != result.Fail || r.Steps[0].Evidence.Errors[0].Kind != result.ProblemTLS || !strings.Contains(strings.Join(r.Steps[0].Messages, ";"), "Hostname mismatch") {
		t.Fatalf("TLS failure: %s %v", r.Verdict, r.Steps[0].Messages)
	}
}

// Verifies: DVT-SRS-105
func TestProbeVerdictTable(t *testing.T) {
	want := map[string]map[string]string{ // mode -> observation -> verdict or problem kind
		"plaintext": {
			engine.ObsNonDICOMReply: "refused", engine.ObsConnectionClosed: "refused", engine.ObsConnectionReset: "refused",
			engine.ObsDICOMReply: "accepted", engine.ObsTimeout: "timeout", engine.ObsConnectError: "connection_closed",
			engine.ObsLocalError: "engine", engine.ObsHandshakeCompleted: "engine", engine.ObsHandshakeAlert: "engine",
		},
		"tls_version": {
			engine.ObsHandshakeAlert: "refused", engine.ObsConnectionClosed: "refused", engine.ObsConnectionReset: "refused",
			engine.ObsHandshakeCompleted: "accepted", engine.ObsCertVerifyFailed: "accepted",
			engine.ObsHandshakeError: "tls", engine.ObsLocalError: "engine", engine.ObsTimeout: "timeout",
			engine.ObsDICOMReply: "engine", engine.ObsNonDICOMReply: "engine",
		},
		"no_client_cert": {
			engine.ObsHandshakeAlert: "refused", engine.ObsConnectionClosed: "refused", engine.ObsConnectionReset: "refused",
			engine.ObsDICOMReply: "accepted", engine.ObsCertVerifyFailed: "tls", engine.ObsHandshakeError: "tls",
			engine.ObsLocalError: "engine", engine.ObsTimeout: "timeout", engine.ObsHandshakeCompleted: "engine",
		},
	}
	for mode, m := range want {
		for obs, w := range m {
			v, k := probeVerdict(mode, obs)
			got := v
			if got == "" {
				got = k
			}
			if got != w {
				t.Errorf("%s/%s: %s, want %s", mode, obs, got, w)
			}
		}
	}
}

func tlsTarget(t *testing.T) *store.Target {
	t.Helper()
	pki, err := fakepeer.NewPKI(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tg := lab()
	tg.TLS = &store.TLS{MinVersion: "1.2", CAFile: pki.Root.File, MoveClientCert: "required"}
	return tg
}

// Verifies: DVT-SRS-105, DVT-SRS-106
func TestTLSRejectStep(t *testing.T) {
	src := "name: x\ntarget: t\nsteps:\n  - {id: p, type: tls_reject, mode: tls_version, tls_version: \"1.1\"}\n"
	for obs, want := range map[string]result.Verdict{
		engine.ObsHandshakeAlert:     result.Pass,
		engine.ObsHandshakeCompleted: result.Fail,
		engine.ObsCertVerifyFailed:   result.Fail,
		engine.ObsTimeout:            result.Fail,
		engine.ObsLocalError:         result.Fail,
	} {
		f := &fake{probe: func(_ engine.Peer, mode, version string) engine.ProbeResult {
			return engine.ProbeResult{Mode: mode, Observation: obs, Detail: "detail " + version}
		}}
		r := run(t, f, setup(t, src), tlsTarget(t))
		if r.Verdict != want {
			t.Errorf("%s: %s, want %s", obs, r.Verdict, want)
		}
		// The probe gets the TLS block (trust only is passed by the engine).
		if len(f.peers) != 1 || f.peers[0].TLS == nil {
			t.Errorf("%s: probe peer %+v", obs, f.peers)
		}
	}
	// Against a DICOMweb target the probe is one bare HTTP request
	// (DVT-SRS-105); HTTP 400 is a refusal, any other status shows the
	// request was served.
	tg := webTarget(store.Lab, "")
	httpGet := func(status int) []result.Exchange {
		return []result.Exchange{{Seq: 1, Protocol: "http", Operation: "GET", Endpoint: "x", Outcome: "completed",
			HTTP: &result.HTTPInfo{Method: "GET", URL: "x", Status: status}}}
	}
	for _, c := range []struct {
		mode, obs string
		status    int
		want      result.Verdict
	}{
		{"plaintext", engine.ObsHTTPReply, 400, result.Pass},
		{"plaintext", engine.ObsHTTPReply, 200, result.Fail},
		{"plaintext", engine.ObsHTTPReply, 401, result.Fail},
		{"plaintext", engine.ObsNonHTTPReply, 0, result.Pass},
		{"plaintext", engine.ObsConnectionClosed, 0, result.Pass},
		{"plaintext", engine.ObsTimeout, 0, result.Fail},
		{"no_client_cert", engine.ObsHTTPReply, 204, result.Fail},
		{"no_client_cert", engine.ObsHandshakeAlert, 0, result.Pass},
		{"no_client_cert", engine.ObsNonHTTPReply, 0, result.Fail},
		{"tls_version", engine.ObsHTTPReply, 400, result.Fail},
		{"tls_version", engine.ObsHandshakeCompleted, 0, result.Fail},
		{"tls_version", engine.ObsHandshakeAlert, 0, result.Pass},
	} {
		f := &fake{probeW: func(_ engine.Web, mode, _ string) engine.ProbeResult {
			r := engine.ProbeResult{Mode: mode, Observation: c.obs, Detail: "d"}
			if c.status != 0 {
				r.Exchanges = httpGet(c.status)
			}
			return r
		}}
		src := "name: x\ntarget: web\nsteps:\n  - {id: p, type: tls_reject, mode: " + c.mode + "}\n"
		if c.mode == "tls_version" {
			src = "name: x\ntarget: web\nsteps:\n  - {id: p, type: tls_reject, mode: tls_version, tls_version: \"1.1\"}\n"
		}
		r := run(t, f, setup(t, src), tg)
		if r.Verdict != c.want || len(f.calls) != 1 || f.calls[0] != "tls-probe-web" {
			t.Errorf("dicomweb %s %s %d: %s %v", c.mode, c.obs, c.status, r.Verdict, f.calls)
		}
	}
}

// Verifies: DVT-SRS-108, DVT-SRS-152
func TestTLSMoveNeedsSCPCertificate(t *testing.T) {
	src := "name: x\ntarget: t\nsteps:\n  - {id: m, type: move, files: [\"fx/*.dcm\"]}\n"
	tg := tlsTarget(t)
	f := &fake{}
	r := run(t, f, setup(t, src), tg)
	if r.Verdict != result.Fail || len(f.calls) != 0 || !strings.Contains(strings.Join(r.Steps[0].Messages, ";"), "does not fall back to clear text") {
		t.Fatalf("no SCP certificate: %s calls %v %v", r.Verdict, f.calls, r.Steps[0].Messages)
	}
	dir := t.TempDir()
	pki, _ := fakepeer.NewPKI(dir)
	a, _ := pki.Issue("scp-a", fakepeer.LeafOpts{Issuer: pki.Root, CN: "libredvt"})
	b, _ := pki.Issue("scp-b", fakepeer.LeafOpts{Issuer: pki.Root, CN: "other"})
	rn := &Runner{Engines: f, SCP: engine.SCP{AETitle: "LIBREDVT", Port: 11115, TLSCert: a.CertFile, TLSKey: b.KeyFile}, WorkDir: t.TempDir(), Version: "test"}
	r = rn.Run(context.Background(), "id", setup(t, src), tg, nil)
	if r.Verdict != result.Fail || len(f.calls) != 0 || !strings.Contains(strings.Join(r.Steps[0].Messages, ";"), "do not form a usable pair") {
		t.Fatalf("mismatched SCP key: %s calls %v %v", r.Verdict, f.calls, r.Steps[0].Messages)
	}
	// With a usable pair the move runs; the SCP's connections are reported
	// and a key leaked into the output is redacted.
	key, _ := os.ReadFile(a.KeyFile)
	f = &fake{move: func(studies []string) engine.MoveResult {
		m := engine.MoveResult{Associated: true, TLS: &result.TLSInfo{Version: "TLSv1.3", Cipher: "c", Verified: true},
			SCPConnections: []engine.SCPConnection{
				{Peer: "127.0.0.1:5000", Outcome: "completed", TLS: &engine.SCPTLS{Version: "TLSv1.3", Cipher: "c", ClientSubject: "CN=pacs"}},
				{Peer: "127.0.0.1:5001", Outcome: "refused", Error: "the target sent a TLS alert " + string(key)},
			}}
		for _, s := range studies {
			m.Moves = append(m.Moves, engine.MoveStudy{Study: s, FinalStatus: ip(0), Failed: ip(0)})
		}
		m.Received = []engine.Received{{SOPInstanceUID: "1.2.a.dcm"}, {SOPInstanceUID: "1.2.b.dcm"}}
		return m
	}}
	rn = &Runner{Engines: f, SCP: engine.SCP{AETitle: "LIBREDVT", Port: 11115, TLSCert: a.CertFile, TLSKey: a.KeyFile}, WorkDir: t.TempDir(), Version: "test"}
	r = rn.Run(context.Background(), "id", setup(t, src), tg, nil)
	msgs := strings.Join(r.Steps[0].Messages, ";")
	if len(f.calls) != 1 || !strings.Contains(msgs, `TLS connection from 127.0.0.1:5000 accepted (TLSv1.3 c, client certificate "CN=pacs"`) || !strings.Contains(msgs, "127.0.0.1:5001 refused") {
		t.Fatalf("TLS move: %s calls %v %v", r.Verdict, f.calls, msgs)
	}
	for _, line := range strings.Split(string(key), "\n") {
		if line != "" && !strings.HasPrefix(line, "-----") && strings.Contains(msgs, line) {
			t.Fatalf("SCP key material not redacted")
		}
	}
	if !strings.Contains(msgs, "[REDACTED") {
		t.Fatalf("no redaction marker: %q", msgs)
	}
	if !strings.Contains(msgs, "secret value(s) found") || r.Verdict != result.Fail {
		t.Fatalf("leaked SCP key did not fail the run: %s %q", r.Verdict, msgs)
	}
}
