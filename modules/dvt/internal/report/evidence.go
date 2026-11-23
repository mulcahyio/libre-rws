package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Identity is what a run learned about the system under test: the TLS
// versions and certificates it saw and the implementation it announced
// (DVT-SRS-145). Reports show it once at the top, so an auditor sees the
// endpoint's identity without reading every exchange.
type Identity struct {
	TLSVersions []string   // distinct, in order of appearance
	Ciphers     []string   // distinct, in order of appearance
	Certs       []CertSeen // distinct leaf certificates (by SHA-256)
	Impl        []ImplSeen // distinct Implementation Class UID / Version Name pairs
}

// CertSeen is a peer certificate with what LibreDVT concluded about it.
type CertSeen struct {
	result.PeerCert
	Verified    bool   // verified in at least one handshake
	VerifyError string // the first verification error, when never verified
	AtRun       string // validity at the start of the run
}

// ImplSeen is an implementation identification from an A-ASSOCIATE-AC.
type ImplSeen struct {
	UID, Version string
}

// EndpointIdentity collects the identity facts of every v0.2 step. It
// returns nil for a run without any (v0.1 runs), so those render as before.
func EndpointIdentity(r *result.Run) *Identity {
	id := &Identity{}
	certs := map[string]int{}
	impls := map[ImplSeen]bool{}
	for _, s := range r.Steps {
		if s.Evidence == nil {
			continue
		}
		for _, x := range s.Evidence.Exchanges {
			if t := x.TLS; t != nil && t.Version != "" {
				id.TLSVersions = appendNew(id.TLSVersions, t.Version)
				id.Ciphers = appendNew(id.Ciphers, t.Cipher)
				if t.Peer != nil && t.Peer.SHA256 != "" {
					i, ok := certs[t.Peer.SHA256]
					if !ok {
						i = len(id.Certs)
						certs[t.Peer.SHA256] = i
						id.Certs = append(id.Certs, CertSeen{PeerCert: *t.Peer, AtRun: validity(*t.Peer, r.Started)})
					}
					c := &id.Certs[i]
					if t.Verified {
						c.Verified, c.VerifyError = true, ""
					} else if !c.Verified && c.VerifyError == "" {
						c.VerifyError = firstNonEmpty(t.VerifyError, "not verified")
					}
				}
			}
			if a := x.Association; a != nil && (a.ImplClassUID != "" || a.ImplVersion != "") {
				k := ImplSeen{UID: a.ImplClassUID, Version: a.ImplVersion}
				if !impls[k] {
					impls[k] = true
					id.Impl = append(id.Impl, k)
				}
			}
		}
	}
	if len(id.TLSVersions) == 0 && len(id.Certs) == 0 && len(id.Impl) == 0 {
		return nil
	}
	return id
}

func appendNew(list []string, s string) []string {
	if s == "" {
		return list
	}
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// validity says whether the certificate was valid when the run started and
// how long it had left, so an audit report shows an expiring certificate.
func validity(c result.PeerCert, at time.Time) string {
	nb, err1 := time.Parse(time.RFC3339, c.NotBefore)
	na, err2 := time.Parse(time.RFC3339, c.NotAfter)
	switch {
	case err1 != nil || err2 != nil || at.IsZero():
		return "unknown"
	case at.Before(nb):
		return "not yet valid at the start of the run"
	case !at.Before(na):
		return "expired at the start of the run"
	}
	days := int(na.Sub(at).Hours() / 24)
	return fmt.Sprintf("valid at the start of the run, %d day(s) left", days)
}

// ExchangeResult is the short result of an exchange: the HTTP status, the
// association result, the DIMSE status or the TLS version.
func ExchangeResult(x result.Exchange) string {
	var out string
	switch {
	case x.HTTP != nil && x.HTTP.Status != 0:
		out = fmt.Sprintf("HTTP %d", x.HTTP.Status)
	case x.Association != nil:
		out = "association " + x.Association.Result
	case x.DIMSEStatus != "":
		out = "status " + x.DIMSEStatus
	case x.TLS != nil && x.TLS.Version != "":
		out = x.TLS.Version
	}
	if x.Error != nil {
		out = strings.TrimSpace(out + " " + x.Error.Kind + " error: " + x.Error.Message)
	}
	return out
}

// exchangeLine is one exchange on one line (JUnit).
func exchangeLine(x result.Exchange) string {
	s := fmt.Sprintf("%s %s %s: %s", x.Protocol, x.Operation, x.Endpoint, x.Outcome)
	if r := ExchangeResult(x); r != "" {
		s += " (" + r + ")"
	}
	return s
}

// AssertionLine is one assertion on one line (JUnit system-out).
func AssertionLine(a result.Assertion) string {
	s := a.Outcome + " " + a.ID
	if a.Subject != "" {
		s += " " + a.Subject
	}
	s += ": expected " + a.Expected + ", got " + a.Actual
	if a.Message != "" {
		s += " (" + a.Message + ")"
	}
	return s
}

// AssertionCounts is "N passed, N warned, N failed".
func AssertionCounts(e *result.Evidence) string {
	var p, w, f int
	for _, a := range e.Assertions {
		switch a.Outcome {
		case result.AssertPass:
			p++
		case result.AssertWarn:
			w++
		default:
			f++
		}
	}
	return fmt.Sprintf("%d passed, %d warned, %d failed", p, w, f)
}

// headerLines renders a header map as sorted "Name: value" lines. Values
// arrive already redacted (the Authorization value is [REDACTED]).
func headerLines(h map[string][]string) []string {
	out := make([]string, 0, len(h))
	for k, vs := range h {
		for _, v := range vs {
			out = append(out, k+": "+v)
		}
	}
	sort.Strings(out)
	return out
}

func pixels(sent, received string) string {
	switch {
	case sent == "" && received == "":
		return ""
	case sent == received:
		return "identical"
	case received == "":
		return "not received"
	default:
		return "different"
	}
}

func roles(r *result.Roles) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("SCU %s, SCP %s", yesno(r.SCU), yesno(r.SCP))
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func assertClass(outcome string) string {
	switch outcome {
	case result.AssertPass:
		return "pass"
	case result.AssertWarn:
		return "warn"
	default:
		return "fail"
	}
}

// junitIdentity adds the run's target and endpoint identity as suite
// properties.
func junitIdentity(r *result.Run) []junitProperty {
	var ps []junitProperty
	if t := r.TargetInfo; t != nil {
		ps = append(ps, junitProperty{"target.name", t.Name}, junitProperty{"target.protocol", t.Protocol},
			junitProperty{"target.endpoint", t.Endpoint}, junitProperty{"target.class", t.Class})
	}
	id := EndpointIdentity(r)
	if id == nil {
		return ps
	}
	if len(id.TLSVersions) > 0 {
		ps = append(ps, junitProperty{"identity.tls_versions", strings.Join(id.TLSVersions, ", ")},
			junitProperty{"identity.cipher_suites", strings.Join(id.Ciphers, ", ")})
	}
	for i, c := range id.Certs {
		p := fmt.Sprintf("identity.cert.%d.", i+1)
		ps = append(ps, junitProperty{p + "subject", c.Subject}, junitProperty{p + "issuer", c.Issuer},
			junitProperty{p + "san_dns", strings.Join(c.SANDNS, ", ")}, junitProperty{p + "san_ip", strings.Join(c.SANIP, ", ")},
			junitProperty{p + "not_before", c.NotBefore}, junitProperty{p + "not_after", c.NotAfter},
			junitProperty{p + "sha256", c.SHA256}, junitProperty{p + "verified", yesno(c.Verified)},
			junitProperty{p + "at_run", c.AtRun})
		if c.VerifyError != "" {
			ps = append(ps, junitProperty{p + "verify_error", c.VerifyError})
		}
	}
	for i, m := range id.Impl {
		p := fmt.Sprintf("identity.implementation.%d.", i+1)
		ps = append(ps, junitProperty{p + "class_uid", m.UID}, junitProperty{p + "version_name", m.Version})
	}
	return ps
}

// junitEvidence adds a v0.2 step's evidence as test case properties: the
// verdict, every exchange with its TLS and association facts, and every
// per-instance result (DVT-SRS-145).
func junitEvidence(s result.Step) []junitProperty {
	e := s.Evidence
	if e == nil {
		return nil
	}
	ps := []junitProperty{{"verdict", string(s.Verdict)}, {"assertions", AssertionCounts(e)},
		{"target.protocol", e.Target.Protocol}, {"target.endpoint", e.Target.Endpoint}}
	for _, x := range e.Exchanges {
		p := fmt.Sprintf("exchange.%d", x.Seq)
		ps = append(ps, junitProperty{p, exchangeLine(x)})
		if t := x.TLS; t != nil {
			ps = append(ps, junitProperty{p + ".tls.version", t.Version}, junitProperty{p + ".tls.cipher", t.Cipher},
				junitProperty{p + ".tls.verified", yesno(t.Verified)}, junitProperty{p + ".tls.client_cert_sent", yesno(t.ClientCertSent)})
			if t.VerifyError != "" {
				ps = append(ps, junitProperty{p + ".tls.verify_error", t.VerifyError})
			}
			if c := t.Peer; c != nil {
				ps = append(ps, junitProperty{p + ".tls.peer_subject", c.Subject}, junitProperty{p + ".tls.peer_issuer", c.Issuer},
					junitProperty{p + ".tls.peer_not_after", c.NotAfter}, junitProperty{p + ".tls.peer_sha256", c.SHA256})
			}
		}
		if a := x.Association; a != nil {
			ps = append(ps, junitProperty{p + ".association.result", a.Result})
			if a.ImplClassUID != "" || a.ImplVersion != "" {
				ps = append(ps, junitProperty{p + ".association.impl_class_uid", a.ImplClassUID}, junitProperty{p + ".association.impl_version", a.ImplVersion})
			}
			if a.Reject != nil {
				ps = append(ps, junitProperty{p + ".association.reject", fmt.Sprintf("result %d, source %d, reason %d", a.Reject.Result, a.Reject.Source, a.Reject.Reason)})
			}
			for _, c := range a.Contexts {
				v := c.AbstractSyntax + " " + c.Result
				if c.AcceptedTransferSyntax != "" {
					v += " " + c.AcceptedTransferSyntax
				}
				if c.Reason != "" {
					v += " (" + c.Reason + ")"
				}
				ps = append(ps, junitProperty{fmt.Sprintf("%s.association.context.%d", p, c.ID), v})
			}
		}
	}
	for i, in := range e.Instances {
		v := in.SOPInstanceUID
		if in.RemoteResult != "" {
			v += ": " + in.RemoteResult
		}
		if in.Reason != "" {
			v += " (" + in.Reason + ")"
		}
		if px := pixels(in.PixelSHA256Sent, in.PixelSHA256Received); px != "" {
			v += "; pixels " + px
		}
		ps = append(ps, junitProperty{fmt.Sprintf("instance.%d", i+1), v})
	}
	for i, p := range e.Errors {
		ps = append(ps, junitProperty{fmt.Sprintf("problem.%d", i+1), p.Kind + ": " + p.Message})
	}
	return ps
}
