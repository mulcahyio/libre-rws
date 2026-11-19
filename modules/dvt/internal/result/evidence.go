package result

import (
	"errors"
	"fmt"
	"time"
)

// EvidenceSchema is the version of the evidence model
// (docs/dhf/libredvt/evidence-model.md). A reader rejects other major
// versions.
const EvidenceSchema = "libredvt.evidence/1"

// Problem kinds (evidence-model.md §3.5). Every problem fails the step
// (DVT-SRS-150).
const (
	ProblemEngine           = "engine"
	ProblemProtocol         = "protocol"
	ProblemTimeout          = "timeout"
	ProblemConnectionClosed = "connection_closed"
	ProblemTLS              = "tls"
	ProblemLimit            = "limit"
	ProblemConfig           = "config"
	ProblemGuard            = "guard"
)

// Assertion outcomes.
const (
	AssertPass = "pass"
	AssertWarn = "warn"
	AssertFail = "fail"
)

// Exchange outcomes.
const (
	ExchangeCompleted = "completed"
	ExchangeRefused   = "refused"
	ExchangeError     = "error"
)

// Redacted replaces every secret value in evidence and artefacts
// (DVT-SRS-152).
const Redacted = "[REDACTED]"

// Evidence is the protocol-neutral record of one v0.2 step
// (DVT-SRS-143): what was sent, what came back, what was checked.
type Evidence struct {
	Schema     string      `json:"schema"`
	Target     TargetInfo  `json:"target"`
	Exchanges  []Exchange  `json:"exchanges"`
	Instances  []Instance  `json:"instances,omitempty"`
	Assertions []Assertion `json:"assertions"`
	Errors     []Problem   `json:"errors,omitempty"`
	Redactions int         `json:"redactions"`
}

// TargetInfo identifies the system under test in the evidence.
type TargetInfo struct {
	Name     string `json:"name"`
	Class    string `json:"class"`
	Protocol string `json:"protocol"`
	Endpoint string `json:"endpoint"` // host:port or base URL, never credentials
}

// Exchange is one network interaction.
type Exchange struct {
	Seq         int              `json:"seq"`
	Protocol    string           `json:"protocol"`  // tls, dimse or http
	Operation   string           `json:"operation"` // TLS-HANDSHAKE, A-ASSOCIATE, C-ECHO, GET, POST ...
	Endpoint    string           `json:"endpoint"`
	Started     time.Time        `json:"started"`
	DurationMS  int64            `json:"duration_ms"`
	Outcome     string           `json:"outcome"` // completed, refused or error
	Error       *Problem         `json:"error,omitempty"`
	TLS         *TLSInfo         `json:"tls,omitempty"`
	Association *AssociationInfo `json:"association,omitempty"`
	HTTP        *HTTPInfo        `json:"http,omitempty"`
	DIMSEStatus string           `json:"dimse_status,omitempty"`
}

// TLSInfo describes a TLS handshake. It never holds key material
// (DVT-SRS-107).
type TLSInfo struct {
	Version        string    `json:"version"`
	Cipher         string    `json:"cipher"`
	Verified       bool      `json:"verified"`
	VerifyError    string    `json:"verify_error,omitempty"`
	Peer           *PeerCert `json:"peer,omitempty"`
	ClientCertSent bool      `json:"client_cert_sent"`
}

// PeerCert summarises the peer's leaf certificate.
type PeerCert struct {
	Subject   string   `json:"subject"`
	Issuer    string   `json:"issuer"`
	SANDNS    []string `json:"san_dns,omitempty"`
	SANIP     []string `json:"san_ip,omitempty"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
	SHA256    string   `json:"sha256"`
}

// AssociationInfo describes an A-ASSOCIATE exchange (DVT-SRS-090…-096).
type AssociationInfo struct {
	Result       string        `json:"result"` // accepted, rejected, aborted, no_response
	Reject       *RejectInfo   `json:"reject,omitempty"`
	Abort        *AbortInfo    `json:"abort,omitempty"`
	MaxPDU       int           `json:"max_pdu,omitempty"`
	ImplClassUID string        `json:"impl_class_uid,omitempty"`
	ImplVersion  string        `json:"impl_version,omitempty"`
	AsyncOps     *AsyncOps     `json:"async_ops,omitempty"`
	Contexts     []ContextInfo `json:"contexts,omitempty"`
	Released     bool          `json:"released"`
}

// RejectInfo holds A-ASSOCIATE-RJ values (PS3.8 Table 9-21).
type RejectInfo struct {
	Result int `json:"result"`
	Source int `json:"source"`
	Reason int `json:"reason"`
}

// AbortInfo holds A-ABORT values.
type AbortInfo struct {
	Source int `json:"source"`
	Reason int `json:"reason"`
}

// AsyncOps is the Asynchronous Operations Window.
type AsyncOps struct {
	Invoked   int `json:"invoked"`
	Performed int `json:"performed"`
}

// ContextInfo is one presentation context's negotiation result.
type ContextInfo struct {
	ID                       int      `json:"id"`
	AbstractSyntax           string   `json:"abstract_syntax"`
	ProposedTransferSyntaxes []string `json:"proposed_transfer_syntaxes"`
	Result                   string   `json:"result"` // accepted, rejected, missing
	AcceptedTransferSyntax   string   `json:"accepted_transfer_syntax,omitempty"`
	Reason                   string   `json:"reason,omitempty"`
	Roles                    *Roles   `json:"roles,omitempty"`
}

// Roles is an SCP/SCU role selection.
type Roles struct {
	SCU bool `json:"scu"`
	SCP bool `json:"scp"`
}

// HTTPInfo summarises one HTTP request and response (DVT-SRS-118, -126).
// The Authorization header value is always Redacted.
type HTTPInfo struct {
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	RequestHeaders  map[string][]string `json:"request_headers,omitempty"`
	Status          int                 `json:"status"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	Body            *BodyInfo           `json:"body,omitempty"`
	Parts           []PartInfo          `json:"parts,omitempty"`
}

// BodyInfo summarises a response body; the excerpt is bounded by
// limits.max_body_excerpt_bytes.
type BodyInfo struct {
	ContentType      string `json:"content_type"`
	Size             int64  `json:"size"`
	SHA256           string `json:"sha256"`
	Excerpt          string `json:"excerpt,omitempty"`
	ExcerptTruncated bool   `json:"excerpt_truncated,omitempty"`
}

// PartInfo holds the facts of one multipart part (never its bytes).
type PartInfo struct {
	Index          int    `json:"index"`
	ContentType    string `json:"content_type"`
	TransferSyntax string `json:"transfer_syntax,omitempty"`
	SOPInstanceUID string `json:"sop_instance_uid,omitempty"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
}

// Instance holds per-object facts (stow, wado, compare).
type Instance struct {
	SOPInstanceUID      string `json:"sop_instance_uid"`
	SOPClassUID         string `json:"sop_class_uid,omitempty"`
	TransferSyntax      string `json:"transfer_syntax,omitempty"`
	File                string `json:"file,omitempty"`
	Size                int64  `json:"size,omitempty"`
	SHA256              string `json:"sha256,omitempty"`
	RemoteResult        string `json:"remote_result,omitempty"` // success, warning, failure, missing, extra, duplicate
	Reason              string `json:"reason,omitempty"`
	RetrieveURL         string `json:"retrieve_url,omitempty"`
	PixelSHA256Sent     string `json:"pixel_sha256_sent,omitempty"`
	PixelSHA256Received string `json:"pixel_sha256_received,omitempty"`
}

// Assertion is one expectation checked by Go (never by the worker).
type Assertion struct {
	ID       string `json:"id"`
	Subject  string `json:"subject,omitempty"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Outcome  string `json:"outcome"` // pass, warn or fail
	Message  string `json:"message,omitempty"`
}

// Problem is a protocol, engine, limit, configuration or guard error.
type Problem struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// NewEvidence starts the evidence of a step.
func NewEvidence(t TargetInfo) *Evidence {
	return &Evidence{Schema: EvidenceSchema, Target: t, Exchanges: []Exchange{}, Assertions: []Assertion{}}
}

// Problemf records a problem; every problem fails the step.
func (e *Evidence) Problemf(kind, format string, a ...any) {
	e.Errors = append(e.Errors, Problem{Kind: kind, Message: fmt.Sprintf(format, a...)})
}

// Assert records an assertion and returns whether it passed.
func (e *Evidence) Assert(id, subject, expected, actual, message string) bool {
	out := AssertFail
	if expected == actual {
		out = AssertPass
	}
	e.Assertions = append(e.Assertions, Assertion{ID: id, Subject: subject, Expected: expected, Actual: actual, Outcome: out, Message: message})
	return out == AssertPass
}

// Warn records an assertion with outcome warn: the step can pass at most
// with warnings (DVT-SRS-096, evidence-model.md §4).
func (e *Evidence) Warn(id, subject, expected, actual, message string) {
	e.Assertions = append(e.Assertions, Assertion{ID: id, Subject: subject, Expected: expected, Actual: actual, Outcome: AssertWarn, Message: message})
}

// nothingChecked is the problem DecideStep reports for a step without
// assertions.
const nothingChecked = "nothing was checked"

// DecideStep applies the v0.2 verdict rule (DVT-SRS-144,
// evidence-model.md §4): any problem fails; no assertion fails as
// "nothing was checked"; any failed (or unknown) assertion fails; any
// warning gives pass with warnings; otherwise pass. Missing evidence fails.
func DecideStep(e *Evidence) Verdict {
	if e == nil || len(e.Errors) > 0 || len(e.Assertions) == 0 {
		return Fail
	}
	warn := false
	for _, a := range e.Assertions {
		switch a.Outcome {
		case AssertPass:
		case AssertWarn:
			warn = true
		default: // fail and anything unexpected fail safe
			return Fail
		}
	}
	if warn {
		return PassWithWarnings
	}
	return Pass
}

// Check validates evidence against the model's closed value sets
// (DVT-SRS-143): a reader must be able to rely on every field.
func (e *Evidence) Check() error {
	if e == nil {
		return errors.New("evidence missing")
	}
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if e.Schema != EvidenceSchema {
		add("evidence schema %q is not %s", e.Schema, EvidenceSchema)
	}
	for i, x := range e.Exchanges {
		if x.Seq != i+1 {
			add("exchange %d has seq %d", i+1, x.Seq)
		}
		if !oneOf(x.Protocol, "tls", "dimse", "http") {
			add("exchange %d: protocol %q", i+1, x.Protocol)
		}
		if !oneOf(x.Outcome, ExchangeCompleted, ExchangeRefused, ExchangeError) {
			add("exchange %d: outcome %q", i+1, x.Outcome)
		}
		if x.Outcome == ExchangeError && x.Error == nil {
			add("exchange %d: outcome error without a problem", i+1)
		}
		if x.Operation == "" || x.Endpoint == "" {
			add("exchange %d: operation and endpoint are required", i+1)
		}
	}
	for i, a := range e.Assertions {
		if a.ID == "" {
			add("assertion %d: id is required", i+1)
		}
		if !oneOf(a.Outcome, AssertPass, AssertWarn, AssertFail) {
			add("assertion %d (%s): outcome %q", i+1, a.ID, a.Outcome)
		}
	}
	for i, p := range e.Errors {
		if !oneOf(p.Kind, ProblemEngine, ProblemProtocol, ProblemTimeout, ProblemConnectionClosed, ProblemTLS, ProblemLimit, ProblemConfig, ProblemGuard) {
			add("problem %d: kind %q", i+1, p.Kind)
		}
	}
	if e.Redactions < 0 {
		add("redactions is negative")
	}
	return errors.Join(errs...)
}

func oneOf(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

// ApplyEvidence attaches e to the step and sets the step's verdict and
// v0.1 status from DecideStep alone (DVT-SRS-144). Evidence that breaks
// the model fails the step as an engine error. Problems and failed
// assertions are copied into Messages so every report shows the reason.
func (s *Step) ApplyEvidence(e *Evidence) {
	if e == nil {
		e = NewEvidence(TargetInfo{})
		e.Problemf(ProblemEngine, "no evidence recorded")
	}
	if err := e.Check(); err != nil {
		e.Problemf(ProblemEngine, "evidence does not match the model: %v", err)
	}
	if len(e.Assertions) == 0 && len(e.Errors) == 0 {
		e.Problemf(ProblemEngine, nothingChecked)
	}
	s.Evidence = e
	s.Verdict = DecideStep(e)
	for _, p := range e.Errors {
		s.Messages = append(s.Messages, fmt.Sprintf("%s error: %s", p.Kind, p.Message))
	}
	for _, a := range e.Assertions {
		if a.Outcome == AssertPass {
			continue
		}
		s.Messages = append(s.Messages, fmt.Sprintf("%s %s %s: expected %s, got %s %s", a.Outcome, a.ID, a.Subject, a.Expected, a.Actual, a.Message))
	}
	if s.Verdict.Passed() {
		s.Status = StepPassed
		s.Messages = append(s.Messages, fmt.Sprintf("%d assertion(s) checked; verdict %s", len(e.Assertions), s.Verdict))
	} else {
		s.Status = StepFailed
	}
}
