package engine

import (
	"context"
	"strconv"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// Peer is the DICOM side of a test target as the worker needs it.
type Peer struct {
	Host       string
	Port       int
	CalledAET  string
	CallingAET string
	Timeout    time.Duration // DIMSE/association timeout (DVT-SRS-029)
	TLS        *TLS          // nil: plain TCP; otherwise TLS 1.2+ with verification (DVT-SRS-100)
}

func (p Peer) args(cmd string) []string {
	t := p.Timeout
	if t <= 0 {
		t = 30 * time.Second
	}
	return []string{cmd, "--host", p.Host, "--port", strconv.Itoa(p.Port),
		"--called-aet", p.CalledAET, "--calling-aet", p.CallingAET,
		"--timeout", strconv.FormatFloat(t.Seconds(), 'f', -1, 64)}
}

// processTimeout bounds the whole worker process: the per-operation timeout
// plus headroom per operation, so a hung worker cannot hang a run.
func (p Peer) processTimeout(ops int) time.Duration {
	t := p.Timeout
	if t <= 0 {
		t = 30 * time.Second
	}
	return time.Duration(ops+2)*t + 15*time.Second
}

// EchoResult from the worker.
type EchoResult struct {
	Associated bool            `json:"associated"`
	Rejected   bool            `json:"rejected"`
	Status     *int            `json:"status"`
	Error      string          `json:"error"`
	TLS        *result.TLSInfo `json:"tls"`       // TLS facts when the target uses TLS (DVT-SRS-107)
	TLSError   string          `json:"tls_error"` // why no TLS connection was made
}

// Echo sends C-ECHO.
func (c Config) Echo(ctx context.Context, p Peer) (EchoResult, string, error) {
	var r EchoResult
	raw, err := c.WorkerCall(ctx, p.processTimeout(1), &r, c.peerArgs(p, "echo")...)
	return r, raw, err
}

// RejectResult from the worker.
type RejectResult struct {
	Associated bool            `json:"associated"`
	Rejected   bool            `json:"rejected"`
	Aborted    bool            `json:"aborted"`
	Error      string          `json:"error"`
	TLS        *result.TLSInfo `json:"tls"`       // TLS facts when the target uses TLS (DVT-SRS-107)
	TLSError   string          `json:"tls_error"` // why no TLS connection was made
}

// Reject tries an association that the target is expected to reject.
func (c Config) Reject(ctx context.Context, p Peer) (RejectResult, string, error) {
	var r RejectResult
	raw, err := c.WorkerCall(ctx, p.processTimeout(1), &r, c.peerArgs(p, "reject")...)
	return r, raw, err
}

// StoreFile is the outcome for one sent file.
type StoreFile struct {
	File              string `json:"file"`
	SOPInstanceUID    string `json:"sop_instance_uid"`
	StudyInstanceUID  string `json:"study_instance_uid"`
	TransferSyntaxUID string `json:"transfer_syntax_uid"`
	Status            *int   `json:"status"`
	Error             string `json:"error"`
}

// StoreResult from the worker.
type StoreResult struct {
	Associated bool            `json:"associated"`
	Rejected   bool            `json:"rejected"`
	Error      string          `json:"error"`
	Files      []StoreFile     `json:"files"`
	TLS        *result.TLSInfo `json:"tls"`       // TLS facts when the target uses TLS (DVT-SRS-107)
	TLSError   string          `json:"tls_error"` // why no TLS connection was made
}

// Store sends files with C-STORE, each in its own transfer syntax.
func (c Config) Store(ctx context.Context, p Peer, files []string) (StoreResult, string, error) {
	var r StoreResult
	raw, err := c.WorkerCall(ctx, p.processTimeout(len(files)), &r, append(c.peerArgs(p, "store"), files...)...)
	return r, raw, err
}

// FindQuery is the outcome for one study query.
type FindQuery struct {
	Study       string   `json:"study"`
	FinalStatus *int     `json:"final_status"`
	Matches     []string `json:"matches"`
	Pending     int      `json:"pending"`
}

// FindResult from the worker.
type FindResult struct {
	Associated bool            `json:"associated"`
	Rejected   bool            `json:"rejected"`
	Error      string          `json:"error"`
	Queries    []FindQuery     `json:"queries"`
	TLS        *result.TLSInfo `json:"tls"`       // TLS facts when the target uses TLS (DVT-SRS-107)
	TLSError   string          `json:"tls_error"` // why no TLS connection was made
}

// Find queries studies by Study Instance UID (Study Root, study level).
func (c Config) Find(ctx context.Context, p Peer, studies []string) (FindResult, string, error) {
	var r FindResult
	args := c.peerArgs(p, "find")
	for _, s := range studies {
		args = append(args, "--study", s)
	}
	raw, err := c.WorkerCall(ctx, p.processTimeout(len(studies)), &r, args...)
	return r, raw, err
}

// MoveStudy is the outcome for one study retrieve.
type MoveStudy struct {
	Study       string `json:"study"`
	FinalStatus *int   `json:"final_status"`
	Completed   *int   `json:"NumberOfCompletedSuboperations"`
	Failed      *int   `json:"NumberOfFailedSuboperations"`
	Warning     *int   `json:"NumberOfWarningSuboperations"`
}

// Received is one instance received by the built-in Storage SCP.
type Received struct {
	SOPInstanceUID    string `json:"sop_instance_uid"`
	File              string `json:"file"`
	TransferSyntaxUID string `json:"transfer_syntax_uid"`
	CallingAET        string `json:"calling_aet"`
}

// MoveResult from the worker.
type MoveResult struct {
	Associated bool            `json:"associated"`
	Rejected   bool            `json:"rejected"`
	Error      string          `json:"error"`
	Moves      []MoveStudy     `json:"moves"`
	Received   []Received      `json:"received"`
	TLS        *result.TLSInfo `json:"tls"`       // TLS facts when the target uses TLS (DVT-SRS-107)
	TLSError   string          `json:"tls_error"` // why no TLS connection was made
	// SCPConnections lists every TLS connection to the built-in Storage SCP
	// during a TLS move (DVT-SRS-108); nil for a move without TLS.
	SCPConnections []SCPConnection `json:"scp_connections"`
}

// SCPConnection is one incoming TLS connection to the built-in Storage SCP.
type SCPConnection struct {
	Peer    string  `json:"peer"`
	Started string  `json:"started"`
	Outcome string  `json:"outcome"` // completed or refused
	Error   string  `json:"error"`
	TLS     *SCPTLS `json:"tls"`
}

// SCPTLS holds the facts of an accepted Storage SCP connection.
type SCPTLS struct {
	Version       string `json:"version"`
	Cipher        string `json:"cipher"`
	ClientSubject string `json:"client_subject"`
	ClientIssuer  string `json:"client_issuer"`
	ClientSHA256  string `json:"client_sha256"`
}

// SCP is the built-in Storage SCP (DVT-SRS-026). With a TLS target it
// listens with TLS using TLSCert and TLSKey (DVT-SRS-108).
type SCP struct {
	AETitle string
	Port    int
	TLSCert string // settings scp_tls_cert (path)
	TLSKey  string // settings scp_tls_key (path; secret)
}

// Move retrieves studies with C-MOVE to the built-in Storage SCP, which runs
// only for the duration of the call and writes to outDir. With a TLS target
// the SCP uses TLS as well, never clear text (DVT-SRS-108).
func (c Config) Move(ctx context.Context, p Peer, scp SCP, outDir string, studies []string, expectedInstances int) (MoveResult, string, error) {
	var r MoveResult
	args := append(c.peerArgs(p, "move"), "--dest-aet", scp.AETitle, "--scp-port", strconv.Itoa(scp.Port), "--out", outDir)
	if p.TLS != nil {
		mode := "required"
		if p.TLS.MoveClientCert == "optional" {
			mode = "optional"
		}
		if scp.TLSCert != "" {
			args = append(args, "--scp-tls-cert", scp.TLSCert, "--scp-tls-key", scp.TLSKey)
		}
		args = append(args, "--scp-client-cert", mode)
	}
	for _, s := range studies {
		args = append(args, "--study", s)
	}
	raw, err := c.WorkerCall(ctx, p.processTimeout(len(studies)+expectedInstances), &r, args...)
	return r, raw, err
}

// CompareFile is the pixel comparison for one sent file (DVT-SRS-025).
type CompareFile struct {
	File                   string `json:"file"`
	SOPInstanceUID         string `json:"sop_instance_uid"`
	Received               string `json:"received"`
	SentPixelSHA256        string `json:"sent_pixel_sha256"`
	ReceivedPixelSHA256    string `json:"received_pixel_sha256"`
	SentTransferSyntax     string `json:"sent_transfer_syntax"`
	ReceivedTransferSyntax string `json:"received_transfer_syntax"`
	Match                  bool   `json:"match"`
	Error                  string `json:"error"`
}

// Compare compares sent files with the instances received in dir.
func (c Config) Compare(ctx context.Context, sent []string, dir string) ([]CompareFile, string, error) {
	var r struct {
		Files []CompareFile `json:"files"`
	}
	args := append([]string{"compare", "--received", dir, "--sent"}, sent...)
	raw, err := c.WorkerCall(ctx, c.Timeout+time.Duration(len(sent))*2*time.Second, &r, args...)
	return r.Files, raw, err
}
