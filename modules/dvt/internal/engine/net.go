package engine

import (
	"context"
	"strconv"
	"time"
)

// Peer is the DICOM side of a test target as the worker needs it.
type Peer struct {
	Host       string
	Port       int
	CalledAET  string
	CallingAET string
	Timeout    time.Duration // DIMSE/association timeout (DVT-SRS-029)
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
	Associated bool   `json:"associated"`
	Rejected   bool   `json:"rejected"`
	Status     *int   `json:"status"`
	Error      string `json:"error"`
}

// Echo sends C-ECHO.
func (c Config) Echo(ctx context.Context, p Peer) (EchoResult, string, error) {
	var r EchoResult
	raw, err := c.WorkerCall(ctx, p.processTimeout(1), &r, p.args("echo")...)
	return r, raw, err
}

// RejectResult from the worker.
type RejectResult struct {
	Associated bool   `json:"associated"`
	Rejected   bool   `json:"rejected"`
	Aborted    bool   `json:"aborted"`
	Error      string `json:"error"`
}

// Reject tries an association that the target is expected to reject.
func (c Config) Reject(ctx context.Context, p Peer) (RejectResult, string, error) {
	var r RejectResult
	raw, err := c.WorkerCall(ctx, p.processTimeout(1), &r, p.args("reject")...)
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
	Associated bool        `json:"associated"`
	Rejected   bool        `json:"rejected"`
	Error      string      `json:"error"`
	Files      []StoreFile `json:"files"`
}

// Store sends files with C-STORE, each in its own transfer syntax.
func (c Config) Store(ctx context.Context, p Peer, files []string) (StoreResult, string, error) {
	var r StoreResult
	raw, err := c.WorkerCall(ctx, p.processTimeout(len(files)), &r, append(p.args("store"), files...)...)
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
	Associated bool        `json:"associated"`
	Rejected   bool        `json:"rejected"`
	Error      string      `json:"error"`
	Queries    []FindQuery `json:"queries"`
}

// Find queries studies by Study Instance UID (Study Root, study level).
func (c Config) Find(ctx context.Context, p Peer, studies []string) (FindResult, string, error) {
	var r FindResult
	args := p.args("find")
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
	Associated bool        `json:"associated"`
	Rejected   bool        `json:"rejected"`
	Error      string      `json:"error"`
	Moves      []MoveStudy `json:"moves"`
	Received   []Received  `json:"received"`
}

// SCP is the built-in Storage SCP (DVT-SRS-026).
type SCP struct {
	AETitle string
	Port    int
}

// Move retrieves studies with C-MOVE to the built-in Storage SCP, which runs
// only for the duration of the call and writes to outDir.
func (c Config) Move(ctx context.Context, p Peer, scp SCP, outDir string, studies []string, expectedInstances int) (MoveResult, string, error) {
	var r MoveResult
	args := append(p.args("move"), "--dest-aet", scp.AETitle, "--scp-port", strconv.Itoa(scp.Port), "--out", outDir)
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
