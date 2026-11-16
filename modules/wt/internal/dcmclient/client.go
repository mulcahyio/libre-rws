// Package dcmclient talks to one LibreDCM instance through its REST control
// API (WT-SRS-006, WT-SRS-013). Libre WT never imports LibreDCM code: the JSON
// contract is the only coupling, so the two can be released separately.
package dcmclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config locates LibreDCM.
type Config struct {
	URL       string // e.g. https://dcm-host:8081
	Token     string // LibreDCM admin token
	TokenFile string
	CAFile    string // PEM to trust (LibreDCM's self-signed certificate)
	Timeout   time.Duration
	// ReadTimeout bounds the status reads (discovery, status, version), so
	// an unreachable node can't hold a page for long. Default 5 seconds.
	ReadTimeout time.Duration
	// ActionTimeout bounds the wait for the answer to an action (C-ECHO
	// test, reload, configuration edit, rollback). Default 30 seconds.
	ActionTimeout time.Duration
	// BackupTimeout bounds the wait for an index backup. Default
	// DefaultBackupTimeout (110 seconds).
	BackupTimeout time.Duration
}

// Client is a LibreDCM control-API client.
type Client struct {
	base   *url.URL
	token  string
	http   *http.Client
	read   time.Duration
	action time.Duration
	backup time.Duration
}

// New validates the configuration and builds a client.
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(cfg.URL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("dcmclient: invalid LibreDCM URL %q", cfg.URL)
	}
	token := strings.TrimSpace(cfg.Token)
	if cfg.TokenFile != "" {
		b, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("dcmclient: token file: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return nil, errors.New("dcmclient: a LibreDCM admin token is required (the control API refuses requests without one)")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("dcmclient: CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("dcmclient: CA file holds no PEM certificate")
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute // a correction rewrites every file of a study
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 5 * time.Second
	}
	if cfg.ActionTimeout <= 0 {
		cfg.ActionTimeout = DefaultActionTimeout
	}
	if cfg.BackupTimeout <= 0 {
		cfg.BackupTimeout = DefaultBackupTimeout
	}
	return &Client{base: u, token: token, http: &http.Client{Transport: tr, Timeout: cfg.Timeout}, read: cfg.ReadTimeout, action: cfg.ActionTimeout,
		backup: cfg.BackupTimeout}, nil
}

// URL returns the configured LibreDCM URL (never the token).
func (c *Client) URL() string { return c.base.String() }

// PlainHTTPRemote reports whether the connection uses plain HTTP to an
// address that is not a loopback address: the admin token and patient data
// then cross a network unencrypted (WT-SRS-036).
func (c *Client) PlainHTTPRemote() bool {
	if c.base.Scheme != "http" {
		return false
	}
	host := c.base.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

// APIError is a non-2xx answer from LibreDCM. Code is LibreDCM's machine
// code, when it sent one.
type APIError struct {
	Status  int
	Message string
	Code    string
}

func (e *APIError) Error() string { return fmt.Sprintf("LibreDCM: %s (HTTP %d)", e.Message, e.Status) }

// NotFound reports whether err is a 404.
func NotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// InstanceMismatch reports whether LibreDCM refused the request because the
// node that answered is not the node Libre WT expected (DCM-SRS-097).
func InstanceMismatch(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusConflict && ae.Code == "instance_mismatch"
}

// Refused reports whether LibreDCM refused the request as invalid (422).
func Refused(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnprocessableEntity
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("LibreDCM unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error, Code: e.Code}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// Conflict mirrors LibreDCM's conflict summary.
type Conflict struct {
	ID                string     `json:"id"`
	Kind              string     `json:"kind"`
	State             string     `json:"state"`
	StudyUID          string     `json:"study_uid"`
	SeriesUID         string     `json:"series_uid"`
	ExistingPatientID string     `json:"existing_patient_id"`
	ExistingStudyUID  string     `json:"existing_study_uid"`
	ExistingSeriesUID string     `json:"existing_series_uid"`
	IncomingPatientID string     `json:"incoming_patient_id"`
	DisplayPatientID  string     `json:"display_patient_id"`
	PatientName       string     `json:"incoming_patient_name"`
	PatientBirthDate  string     `json:"incoming_patient_birth_date"`
	PatientSex        string     `json:"incoming_patient_sex"`
	StudyDate         string     `json:"study_date"`
	AccessionNumber   string     `json:"accession_number"`
	StudyDescription  string     `json:"study_description"`
	SourceAE          string     `json:"source_ae"`
	Coerced           bool       `json:"coerced"`
	Instances         int        `json:"instances"`
	FirstSeen         time.Time  `json:"first_seen"`
	LastSeen          time.Time  `json:"last_seen"`
	ResolvedAt        *time.Time `json:"resolved_at"`
	ResolvedBy        string     `json:"resolved_by"`
	ResolutionReason  string     `json:"resolution_reason"`
	ResolutionAction  string     `json:"resolution_action"`
}

// Active reports whether the conflict can still be resolved.
func (c Conflict) Active() bool { return c.State == "open" || c.State == "accepted" }

// Demographics are the patient attributes of the indexed study.
type Demographics struct {
	PatientID        string `json:"PatientID"`
	PatientName      string `json:"PatientName"`
	PatientBirthDate string `json:"PatientBirthDate"`
	PatientSex       string `json:"PatientSex"`
}

// Instance is a held instance.
type Instance struct {
	SOPInstanceUID    string    `json:"sop_instance_uid"`
	SeriesUID         string    `json:"series_uid"`
	Modality          string    `json:"modality"`
	SeriesNumber      string    `json:"series_number"`
	SeriesDescription string    `json:"series_description"`
	InstanceNumber    string    `json:"instance_number"`
	StoredPatientID   string    `json:"stored_patient_id"`
	ReceivedAt        time.Time `json:"received_at"`
	SourceAE          string    `json:"source_ae"`
}

// Detail is one conflict with context.
type Detail struct {
	Conflict          Conflict      `json:"conflict"`
	IndexedPatient    *Demographics `json:"indexed_patient"`
	IndexedInstances  int           `json:"indexed_instances"`
	IncomingInstances []Instance    `json:"incoming_instances"`
}

// Change is one planned attribute change.
type Change struct {
	Attribute string `json:"attribute"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// Plan describes what an action would change.
type Plan struct {
	ConflictID        string   `json:"conflict_id"`
	Action            string   `json:"action"`
	Summary           string   `json:"summary"`
	ExistingInstances int      `json:"existing_instances_rewritten"`
	IncomingInstances int      `json:"incoming_instances_rewritten"`
	ExistingChanges   []Change `json:"existing_changes"`
	IncomingChanges   []Change `json:"incoming_changes"`
	Warnings          []string `json:"warnings"`
}

// Result is what a resolve did.
type Result struct {
	Plan
	Rewritten int    `json:"files_rewritten"`
	State     string `json:"state"`
}

// Conflicts lists conflicts; state is active, open, accepted, resolved or all.
func (c *Client) Conflicts(ctx context.Context, state string) ([]Conflict, error) {
	var out struct {
		Conflicts []Conflict `json:"conflicts"`
	}
	err := c.do(ctx, http.MethodGet, "/control/v1/conflicts?state="+url.QueryEscape(state), nil, &out)
	return out.Conflicts, err
}

// Conflict returns one conflict with context.
func (c *Client) Conflict(ctx context.Context, id string) (Detail, error) {
	var d Detail
	err := c.do(ctx, http.MethodGet, "/control/v1/conflicts/"+url.PathEscape(id), nil, &d)
	return d, err
}

// Identity names a LibreDCM node (DCM-SRS-097).
type Identity struct {
	NodeID  string `json:"node_id"`
	AETitle string `json:"ae_title"`
}

// Zero reports whether no identity is given.
func (i Identity) Zero() bool { return i.NodeID == "" && i.AETitle == "" }

// expect adds the expected identity to a request body. LibreDCM then refuses
// the request when another node answers (WT-SRS-015). A zero identity adds
// nothing: a LibreDCM without identity support refuses unknown fields.
func expect(body map[string]any, exp Identity) map[string]any {
	if exp.NodeID != "" {
		body["expect_node_id"] = exp.NodeID
	}
	if exp.AETitle != "" {
		body["expect_ae_title"] = exp.AETitle
	}
	return body
}

// Plan asks what an action would change. exp is the node Libre WT expects.
func (c *Client) Plan(ctx context.Context, id, action string, exp Identity) (Plan, error) {
	var p Plan
	err := c.do(ctx, http.MethodPost, "/control/v1/conflicts/"+url.PathEscape(id)+"/plan", expect(map[string]any{"action": action}, exp), &p)
	return p, err
}

// Resolve executes an action that the administrator approved, on the node
// Libre WT expects (exp).
func (c *Client) Resolve(ctx context.Context, id, action, operator, reason string, exp Identity) (Result, error) {
	var r Result
	err := c.do(ctx, http.MethodPost, "/control/v1/conflicts/"+url.PathEscape(id)+"/resolve", expect(map[string]any{
		"action": action, "operator": operator, "reason": reason, "confirm": true,
	}, exp), &r)
	return r, err
}

// Version returns LibreDCM's /version (module, version, node, uptime).
func (c *Client) Version(ctx context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, c.read)
	defer cancel()
	out := map[string]any{}
	err := c.do(ctx, http.MethodGet, "/version", nil, &out)
	return out, err
}

// InstanceInfo is the identity block LibreDCM puts in every control
// response (DCM-SRS-097).
type InstanceInfo struct {
	NodeID       string `json:"node_id"`
	AETitle      string `json:"ae_title"`
	Version      string `json:"version"`
	ConfigDigest string `json:"config_digest"`
}

// Identity returns the part that names the node.
func (i InstanceInfo) Identity() Identity { return Identity{NodeID: i.NodeID, AETitle: i.AETitle} }

// Discovery is the answer of GET /control/v1: the control functions this
// LibreDCM serves (WT-SRS-018).
type Discovery struct {
	API      string       `json:"api"`
	Features []string     `json:"features"`
	Instance InstanceInfo `json:"instance"`
}

// Discovery asks the node which control functions it supports.
func (c *Client) Discovery(ctx context.Context) (Discovery, error) {
	ctx, cancel := context.WithTimeout(ctx, c.read)
	defer cancel()
	var d Discovery
	err := c.do(ctx, http.MethodGet, "/control/v1", nil, &d)
	return d, err
}

// Check is one readiness check of a node.
type Check struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// License is the licence part of the status. Enforced is false for source
// builds. The licence state never affects DICOM services.
type License struct {
	Enforced    bool   `json:"enforced"`
	State       string `json:"state"`
	Licensed    bool   `json:"licensed"`
	AckRequired bool   `json:"ack_required"`
	Message     string `json:"message"`
	DaysLeft    int    `json:"days_left"`
}

// Counters are the node's live counters, read by LibreDCM at request time.
type Counters struct {
	AssociationsActive   uint64            `json:"associations_active"`
	AssociationsTotal    uint64            `json:"associations_total"`
	AssociationsRejected uint64            `json:"associations_rejected_total"`
	CStore               map[string]uint64 `json:"cstore"`
	CFindTotal           uint64            `json:"cfind_total"`
	RetrieveSubOps       map[string]uint64 `json:"retrieve_subops"`
	OutboxPending        uint64            `json:"outbox_pending"`
	PublishFailures      uint64            `json:"event_publish_failures_total"`
	// ConflictsOpen is nil when the node could not count them.
	ConflictsOpen *uint64 `json:"conflicts_open"`
}

// Status is the answer of GET /control/v1/status (DCM-SRS-081).
type Status struct {
	Instance      InstanceInfo `json:"instance"`
	Time          time.Time    `json:"time"`
	UptimeSeconds int64        `json:"uptime_seconds"`
	Backends      struct {
		Storage string `json:"storage"`
		Index   string `json:"index"`
		Events  string `json:"events"`
	} `json:"backends"`
	DICOM struct {
		Listen           string `json:"listen"`
		TLS              bool   `json:"tls"`
		AllowListEntries int    `json:"allow_list_entries"`
		RemoteAEs        int    `json:"remote_aes"`
	} `json:"dicom"`
	Admin struct {
		TLS bool `json:"tls"`
	} `json:"admin"`
	Ready              bool     `json:"ready"`
	Checks             []Check  `json:"checks"`
	License            License  `json:"license"`
	Counters           Counters `json:"counters"`
	CountersIncomplete bool     `json:"counters_incomplete"`
}

// Status reads the node's status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, c.read)
	defer cancel()
	var s Status
	err := c.do(ctx, http.MethodGet, "/control/v1/status", nil, &s)
	return s, err
}
