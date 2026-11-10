// Package dcmclient talks to one LibreDCM instance through its REST control
// API (WT-SRS-006). Libre WT never imports LibreDCM code: the JSON contract
// is the only coupling, so the two can be released separately.
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
}

// Client is a LibreDCM control-API client.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
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
	return &Client{base: u, token: token, http: &http.Client{Transport: tr, Timeout: cfg.Timeout}}, nil
}

// URL returns the configured LibreDCM URL (never the token).
func (c *Client) URL() string { return c.base.String() }

// APIError is a non-2xx answer from LibreDCM.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("LibreDCM: %s (HTTP %d)", e.Message, e.Status) }

// NotFound reports whether err is a 404.
func NotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
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
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error}
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

// Plan asks what an action would change.
func (c *Client) Plan(ctx context.Context, id, action string) (Plan, error) {
	var p Plan
	err := c.do(ctx, http.MethodPost, "/control/v1/conflicts/"+url.PathEscape(id)+"/plan", map[string]string{"action": action}, &p)
	return p, err
}

// Resolve executes an action that the administrator approved.
func (c *Client) Resolve(ctx context.Context, id, action, operator, reason string) (Result, error) {
	var r Result
	err := c.do(ctx, http.MethodPost, "/control/v1/conflicts/"+url.PathEscape(id)+"/resolve", map[string]any{
		"action": action, "operator": operator, "reason": reason, "confirm": true,
	}, &r)
	return r, err
}

// Version returns LibreDCM's /version (module, version, node, uptime).
func (c *Client) Version(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	err := c.do(ctx, http.MethodGet, "/version", nil, &out)
	return out, err
}
