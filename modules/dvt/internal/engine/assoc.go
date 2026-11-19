package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// TLS is what the worker needs to open a TLS connection to a target: file
// paths and names only, never secret values (DVT-SRS-153). There is no
// field that weakens verification (DVT-SRS-103).
type TLS struct {
	MinVersion     string // "1.2" (default) or "1.3"
	CAFile         string // PEM CA certificates; empty = the image's trust store
	ServerName     string // name checked against the subjectAltName; empty = host
	ClientCert     string // PEM client certificate (mutual TLS)
	ClientKey      string // PEM private key of ClientCert (secret: path only)
	MoveClientCert string // "required" (default) or "optional": Storage SCP during a TLS move
}

func (t *TLS) minVersion() string {
	if t.MinVersion == "1.3" {
		return "1.3"
	}
	return "1.2"
}

// trustArgs are the options every TLS connection uses: minimum version,
// trust anchors and the name to verify.
func (t *TLS) trustArgs() []string {
	a := []string{"--tls-min", t.minVersion()}
	if t.CAFile != "" {
		a = append(a, "--ca-file", t.CAFile)
	}
	if t.ServerName != "" {
		a = append(a, "--server-name", t.ServerName)
	}
	return a
}

// args turns TLS on for a worker network command.
func (t *TLS) args() []string {
	if t == nil {
		return nil
	}
	a := append([]string{"--tls"}, t.trustArgs()...)
	if t.ClientCert != "" {
		a = append(a, "--client-cert", t.ClientCert, "--client-key", t.ClientKey)
	}
	return a
}

// workerCallStrict is WorkerCall with unknown JSON fields rejected: the
// v0.2 commands have a fixed output schema (DVT-SRS-150).
func (c Config) workerCallStrict(ctx context.Context, timeout time.Duration, v any, args ...string) (string, error) {
	var raw json.RawMessage
	out, err := c.WorkerCall(ctx, timeout, &raw, args...)
	if err != nil {
		return out, err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return out, fmt.Errorf("worker %s: output does not match its schema: %w", args[0], err)
	}
	return out, nil
}

// AssocContext is one presentation context an associate step proposes.
type AssocContext struct {
	AbstractSyntax   string
	TransferSyntaxes []string
	Roles            *result.Roles // proposed SCP/SCU role selection, or nil
}

// AssociateRequest is what an associate step proposes (DVT-SRS-090, -093,
// -094).
type AssociateRequest struct {
	Contexts []AssocContext
	AsyncOps *result.AsyncOps
}

// AssociateResult is the worker's record of an association attempt: the
// exchanges in the evidence-model shape (TLS-HANDSHAKE, A-ASSOCIATE,
// A-RELEASE). The worker reports facts; the runner decides.
type AssociateResult struct {
	Exchanges []result.Exchange `json:"exchanges"`
}

// Operations the worker may record.
const (
	OpConnect   = "TCP-CONNECT"
	OpTLS       = "TLS-HANDSHAKE"
	OpAssociate = "A-ASSOCIATE"
	OpRelease   = "A-RELEASE"
)

func contextArg(c AssocContext) string {
	s := c.AbstractSyntax + ";" + strings.Join(c.TransferSyntaxes, ",")
	if c.Roles != nil {
		s += ";" + bit(c.Roles.SCU) + bit(c.Roles.SCP)
	}
	return s
}

func bit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Associate requests an association, records the target's answer and
// releases it (DVT-SRS-090…-096). Output that does not match the schema is
// an error (DVT-SRS-150).
func (c Config) Associate(ctx context.Context, p Peer, req AssociateRequest) (AssociateResult, string, error) {
	var r AssociateResult
	args := c.peerArgs(p, "associate")
	for _, x := range req.Contexts {
		args = append(args, "--context", contextArg(x))
	}
	if req.AsyncOps != nil {
		args = append(args, "--async-ops", fmt.Sprintf("%d,%d", req.AsyncOps.Invoked, req.AsyncOps.Performed))
	}
	raw, err := c.workerCallStrict(ctx, p.processTimeout(3), &r, args...)
	if err != nil {
		return r, raw, err
	}
	return r, raw, checkAssociate(r, req, p.TLS != nil)
}

var exchangeKinds = map[string]bool{
	result.ProblemEngine: true, result.ProblemProtocol: true, result.ProblemTimeout: true,
	result.ProblemConnectionClosed: true, result.ProblemTLS: true, result.ProblemConfig: true,
}

// checkExchanges applies the common rules: seq 1, 2, 3 …; known protocol,
// operation and outcome; an error carries a known problem kind.
func checkExchanges(xs []result.Exchange, ops map[string]string) error {
	for i, x := range xs {
		where := fmt.Sprintf("exchange %d", i+1)
		if x.Seq != i+1 {
			return fmt.Errorf("%s has seq %d", where, x.Seq)
		}
		proto, ok := ops[x.Operation]
		if !ok {
			return fmt.Errorf("%s: unknown operation %q", where, x.Operation)
		}
		if x.Protocol != proto {
			return fmt.Errorf("%s: operation %s with protocol %q", where, x.Operation, x.Protocol)
		}
		switch x.Outcome {
		case result.ExchangeCompleted, result.ExchangeRefused, result.ExchangeError:
		default:
			return fmt.Errorf("%s: outcome %q", where, x.Outcome)
		}
		if x.Outcome == result.ExchangeError && x.Error == nil {
			return fmt.Errorf("%s: outcome error without a problem", where)
		}
		if x.Error != nil && (!exchangeKinds[x.Error.Kind] || x.Error.Message == "") {
			return fmt.Errorf("%s: problem kind %q or empty message", where, x.Error.Kind)
		}
		if x.Endpoint == "" {
			return fmt.Errorf("%s: no endpoint", where)
		}
		if x.Operation == OpTLS && x.TLS == nil {
			return fmt.Errorf("%s: TLS handshake without TLS details", where)
		}
		if x.HTTP != nil {
			return fmt.Errorf("%s: HTTP details in a DIMSE exchange", where)
		}
	}
	return nil
}

var assocOps = map[string]string{OpConnect: "dimse", OpTLS: "tls", OpAssociate: "dimse", OpRelease: "dimse"}

// checkAssociate holds the worker's associate output to its schema: order
// of exchanges, one context record per proposed context with the proposed
// values, closed value sets. Anything else is an engine error, never a pass.
func checkAssociate(r AssociateResult, req AssociateRequest, tls bool) error {
	xs := r.Exchanges
	if len(xs) == 0 {
		return errors.New("worker associate: no exchange recorded")
	}
	if err := checkExchanges(xs, assocOps); err != nil {
		return fmt.Errorf("worker associate: %w", err)
	}
	// Allowed sequences: [TLS] A-ASSOCIATE [A-RELEASE], or a failed
	// TCP-CONNECT / TLS-HANDSHAKE as the last exchange.
	i := 0
	if xs[0].Operation == OpConnect {
		if len(xs) != 1 || xs[0].Outcome != result.ExchangeError {
			return errors.New("worker associate: a TCP-CONNECT exchange must be a single failed exchange")
		}
		return nil
	}
	if xs[0].Operation == OpTLS {
		if !tls {
			return errors.New("worker associate: TLS handshake on a target without TLS")
		}
		if xs[0].Error != nil {
			if len(xs) != 1 {
				return errors.New("worker associate: exchanges after a failed TLS handshake")
			}
			return nil
		}
		i = 1
	} else if tls {
		return errors.New("worker associate: no TLS handshake recorded for a TLS target")
	}
	if i >= len(xs) || xs[i].Operation != OpAssociate {
		return errors.New("worker associate: no A-ASSOCIATE exchange")
	}
	a := xs[i].Association
	if a == nil {
		return errors.New("worker associate: A-ASSOCIATE without association details")
	}
	switch a.Result {
	case "accepted", "rejected", "aborted", "no_response":
	default:
		return fmt.Errorf("worker associate: association result %q", a.Result)
	}
	if a.Result == "rejected" && a.Reject == nil {
		return errors.New("worker associate: rejection without A-ASSOCIATE-RJ values")
	}
	if len(a.Contexts) != len(req.Contexts) {
		return fmt.Errorf("worker associate: %d context records for %d proposed contexts", len(a.Contexts), len(req.Contexts))
	}
	for k, ci := range a.Contexts {
		want := req.Contexts[k]
		if ci.ID != 2*k+1 || ci.AbstractSyntax != want.AbstractSyntax || strings.Join(ci.ProposedTransferSyntaxes, ",") != strings.Join(want.TransferSyntaxes, ",") {
			return fmt.Errorf("worker associate: context record %d does not match proposed context %d", k+1, 2*k+1)
		}
		switch ci.Result {
		case "accepted", "rejected", "missing":
		default:
			return fmt.Errorf("worker associate: context %d result %q", ci.ID, ci.Result)
		}
		if a.Result != "accepted" && ci.Result == "accepted" {
			return fmt.Errorf("worker associate: context %d accepted in a %s association", ci.ID, a.Result)
		}
	}
	rest := xs[i+1:]
	switch {
	case len(rest) == 0:
	case len(rest) == 1 && rest[0].Operation == OpRelease && a.Result == "accepted":
	default:
		return errors.New("worker associate: unexpected exchanges after A-ASSOCIATE")
	}
	if a.Released && (len(rest) != 1 || rest[0].Outcome != result.ExchangeCompleted) {
		return errors.New("worker associate: released without a completed A-RELEASE exchange")
	}
	return nil
}

// ProbeResult is the worker's record of a TLS refusal probe (DVT-SRS-105).
type ProbeResult struct {
	Mode        string            `json:"mode"`
	Observation string            `json:"observation"`
	Detail      string            `json:"detail"`
	Exchanges   []result.Exchange `json:"exchanges"`
}

// Probe observations (what the worker saw; the runner decides).
const (
	ObsDICOMReply         = "dicom_reply"
	ObsNonDICOMReply      = "non_dicom_reply"
	ObsConnectionClosed   = "connection_closed"
	ObsConnectionReset    = "connection_reset"
	ObsHandshakeAlert     = "handshake_alert"
	ObsHandshakeError     = "handshake_error"
	ObsHandshakeCompleted = "handshake_completed"
	ObsCertVerifyFailed   = "cert_verify_failed"
	ObsTimeout            = "timeout"
	ObsConnectError       = "connect_error"
	ObsLocalError         = "local_error"
)

var observations = map[string]bool{
	ObsDICOMReply: true, ObsNonDICOMReply: true, ObsConnectionClosed: true, ObsConnectionReset: true,
	ObsHandshakeAlert: true, ObsHandshakeError: true, ObsHandshakeCompleted: true,
	ObsCertVerifyFailed: true, ObsTimeout: true, ObsConnectError: true, ObsLocalError: true,
}

// TLSProbe breaks the TLS policy on purpose (plaintext, an old TLS version,
// no client certificate) and reports what the target did. The probe never
// gets the client certificate or key (DVT-SRS-106).
func (c Config) TLSProbe(ctx context.Context, p Peer, mode, version string) (ProbeResult, string, error) {
	var r ProbeResult
	uid, name := Implementation(c.Version)
	args := append(p.args("tls-probe"), "--impl-uid", uid, "--impl-version", name)
	if p.TLS != nil {
		args = append(args, p.TLS.trustArgs()...)
	}
	args = append(args, "--mode", mode)
	if mode == "tls_version" {
		args = append(args, "--tls-version", version)
	}
	raw, err := c.workerCallStrict(ctx, p.processTimeout(2), &r, args...)
	if err != nil {
		return r, raw, err
	}
	switch {
	case r.Mode != mode:
		return r, raw, fmt.Errorf("worker tls-probe: result for mode %q, expected %q", r.Mode, mode)
	case !observations[r.Observation]:
		return r, raw, fmt.Errorf("worker tls-probe: unknown observation %q", r.Observation)
	case r.Detail == "":
		return r, raw, errors.New("worker tls-probe: observation without detail")
	}
	if err := checkExchanges(r.Exchanges, assocOps); err != nil {
		return r, raw, fmt.Errorf("worker tls-probe: %w", err)
	}
	for _, x := range r.Exchanges {
		if x.Operation == OpRelease {
			return r, raw, errors.New("worker tls-probe: a probe must not release (no data beyond the A-ASSOCIATE-RQ)")
		}
		if x.TLS != nil && x.TLS.ClientCertSent {
			return r, raw, errors.New("worker tls-probe: a probe must not send a client certificate")
		}
	}
	return r, raw, nil
}
