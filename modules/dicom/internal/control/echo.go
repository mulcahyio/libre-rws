package control

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
)

// C-ECHO test (control-api-v1.md §4.1, DCM-SRS-087).

const (
	// EchoTimeout bounds one C-ECHO test from connect to release.
	EchoTimeout = 15 * time.Second
	// echoSlots is how many tests may run at once on a node.
	echoSlots = 2

	verificationSOPClass = "1.2.840.10008.1.1"
)

// Stages of a C-ECHO test, reported when it fails.
const (
	StageConnect     = "connect"     // TCP (or TLS) connection
	StageAssociation = "association" // DICOM association negotiation
	StageEcho        = "echo"        // the C-ECHO exchange itself
)

// EchoTarget is where a C-ECHO test goes.
type EchoTarget struct {
	Addr      string // host:port
	CalledAE  string
	CallingAE string
	TLS       bool
}

// EchoFunc runs one C-ECHO test. It returns the stage that failed and the
// error, or "" and nil on success.
type EchoFunc func(ctx context.Context, t EchoTarget) (stage string, err error)

// DICOMEcho opens an association for the Verification SOP class only, sends
// one C-ECHO and releases. No data set, and so no patient data, is sent.
// With TLS the peer certificate is not verified: this is a reachability
// test (often of a self-signed install), not a trust decision.
func DICOMEcho(ctx context.Context, t EchoTarget) (string, error) {
	var tlsCfg *tls.Config
	if t.TLS {
		tlsCfg = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // #nosec G402 -- reachability check of a (often self-signed) peer, not a trust decision; no patient data is sent
	}
	scu, err := ae.Dial(ctx, ae.Config{
		TLS:                       tlsCfg,
		ImplementationClassUID:    dcm.ImplementationClassUID,
		ImplementationVersionName: dcm.ImplementationVersionName,
		AETitle:                   t.CallingAE,
		DialTimeout:               10 * time.Second,
		PresentationContexts: []ae.PresentationContext{
			{AbstractSyntax: verificationSOPClass, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}},
		},
	}, t.Addr, t.CalledAE)
	if err != nil {
		// ae.Dial wraps a connection failure as "ae: dial <addr>: ...";
		// everything after the connection is the association negotiation.
		if strings.HasPrefix(err.Error(), "ae: dial ") {
			return StageConnect, err
		}
		return StageAssociation, err
	}
	defer func() { _ = scu.Release(ctx) }()
	if err := scu.CEcho(ctx); err != nil {
		return StageEcho, err
	}
	return "", nil
}

// EchoRequest is the body of POST /control/v1/echo: either remote_ae (the
// name of a configured destination) or host, port and ae_title.
type EchoRequest struct {
	Envelope
	RemoteAE string `json:"remote_ae,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	AETitle  string `json:"ae_title,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
}

// validAETitle: 1 to 16 printable ASCII characters, no backslash (PS3.5 AE).
func validAETitle(s string) bool {
	if len(s) < 1 || len(s) > 16 || strings.TrimSpace(s) != s {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x20 || c > 0x7e || c == '\\' {
			return false
		}
	}
	return true
}

// validHost: a host name or IP address, nothing that could be read as a
// URL, a path or a second address.
func validHost(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	if ip := net.ParseIP(s); ip != nil {
		return true
	}
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// echoErrorText reduces a failure to a fixed phrase. The peer's own bytes
// and the system's error text are never returned (TM-25): the answer says
// which stage failed and in which broad way, nothing more.
func echoErrorText(stage string, err error) string {
	var ne net.Error
	timedOut := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) ||
		(errors.As(err, &ne) && ne.Timeout())
	switch stage {
	case StageConnect:
		var dnsErr *net.DNSError
		switch {
		case errors.As(err, &dnsErr):
			return "host name not found"
		case timedOut:
			return "no answer (timed out)"
		case strings.Contains(err.Error(), "connection refused"):
			return "connection refused"
		case strings.Contains(err.Error(), "tls:"):
			return "TLS handshake failed"
		}
		return "connection failed"
	case StageAssociation:
		if timedOut {
			return "no answer to the association request (timed out)"
		}
		return "association rejected or aborted by the peer"
	default:
		if timedOut {
			return "no answer to the C-ECHO (timed out)"
		}
		return "C-ECHO failed"
	}
}

func (a *API) echo(ctx context.Context, req *EchoRequest) actionResult {
	if a.Echo == nil {
		return refusal(http.StatusInternalServerError, "internal", "C-ECHO is not available on this node", false)
	}
	t := EchoTarget{CallingAE: a.Node.AETitle, TLS: req.TLS}
	source := "ad-hoc"
	adHoc := req.Host != "" || req.Port != 0 || req.AETitle != "" || req.TLS
	switch {
	case req.RemoteAE != "" && adHoc:
		return refusal(http.StatusUnprocessableEntity, "invalid_target",
			"give either remote_ae or host, port and ae_title, not both", true)
	case req.RemoteAE != "":
		var dest *RemoteAEView
		for _, r := range a.liveRemoteAEs() {
			if r.Name == req.RemoteAE {
				dest = &r
				break
			}
		}
		if dest == nil {
			// The name is not echoed into the audit record beyond a safe length.
			return refusal(http.StatusNotFound, "unknown_remote_ae",
				"no remote AE with this name is configured on this node", true, "remote_ae", clip(req.RemoteAE, 32))
		}
		source = "configured"
		t.Addr, t.CalledAE = net.JoinHostPort(dest.Host, strconv.Itoa(dest.Port)), dest.AETitle
	default:
		if !validHost(req.Host) || req.Port < 1 || req.Port > 65535 || !validAETitle(req.AETitle) {
			return refusal(http.StatusUnprocessableEntity, "invalid_target",
				"host must be a host name or IP address, port 1 to 65535, ae_title 1 to 16 characters", true)
		}
		t.Addr, t.CalledAE = net.JoinHostPort(req.Host, strconv.Itoa(req.Port)), req.AETitle
	}

	start := time.Now()
	stage, err := a.Echo(ctx, t)
	ms := time.Since(start).Milliseconds()
	body := map[string]any{"ok": err == nil, "duration_ms": ms, "target": t.Addr, "called_ae": t.CalledAE,
		"calling_ae": t.CallingAE, "tls": t.TLS, "source": source,
		"note": "Verification SOP class only. No patient data was sent."}
	if t.TLS {
		// Said plainly: the test proves reachability, not the peer's identity.
		body["tls_verified"] = false
	}
	attrs := []any{"target", t.Addr, "called_ae", t.CalledAE, "tls", t.TLS, "source", source, "duration_ms", ms}
	if err != nil {
		text := echoErrorText(stage, err)
		body["stage"], body["error"] = stage, text
		// The test ran and failed: a result, not an API error.
		return actionResult{status: http.StatusOK, body: body, outcome: outcomeMinor,
			attrs: append(attrs, "stage", stage, "result", text)}
	}
	return actionResult{status: http.StatusOK, body: body, outcome: outcomeSuccess, attrs: append(attrs, "result", "success")}
}
