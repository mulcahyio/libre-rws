package dcmclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Study query, send, audit records and log lines (control-api-v1.md §3.4,
// §3.5, §5.1, §5.2; WT-SRS-025, -026, -029, -030).

const (
	// QueryTimeout bounds the wait for a study query. LibreDCM stops a
	// query after 30 seconds; Libre WT waits a little longer, so it sees
	// LibreDCM's answer rather than its own timeout.
	QueryTimeout = 35 * time.Second
	// DefaultSendTimeout bounds the wait for a send. LibreDCM lets a send
	// run for up to 30 minutes and finishes it when Libre WT stops
	// waiting; Libre WT stays below its own HTTP client timeout (2 minutes)
	// and the write timeout of its web server (3 minutes), as for a backup.
	DefaultSendTimeout = 110 * time.Second

	// OperatorHeader carries the name of the person on a study query,
	// which LibreDCM audits (DCM-SRS-091).
	OperatorHeader = "X-Libre-Operator"
)

// StudyQuery is one study query: the level, the matching keys by DICOM
// keyword with values in DICOM matching syntax, the page size and the
// cursor of the next page.
type StudyQuery struct {
	Level  string
	Keys   map[string]string
	Limit  int
	Cursor string
}

func (q StudyQuery) values() url.Values {
	v := url.Values{}
	for k, val := range q.Keys {
		v.Set(k, val)
	}
	if q.Level != "" {
		v.Set("level", q.Level)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	return v
}

// StudyResult is one page of a study query. Rows are keyed by DICOM
// keyword. More is true when LibreDCM has more rows; Cursor then asks for
// the next page. Held says how LibreDCM treats instances held as a patient
// conflict: "hidden" (not listed) or "flagged" (listed with ConflictID).
type StudyResult struct {
	Instance InstanceInfo        `json:"instance"`
	Level    string              `json:"level"`
	Rows     []map[string]string `json:"rows"`
	Count    int                 `json:"count"`
	Limit    int                 `json:"limit"`
	More     bool                `json:"more"`
	Cursor   string              `json:"cursor"`
	Held     string              `json:"held_instances"`
	Note     string              `json:"note"`
}

// Studies runs a study query for the named person. LibreDCM writes the
// query, with the person's name, to its audit records before it returns a
// row.
func (c *Client) Studies(ctx context.Context, q StudyQuery, operator string) (StudyResult, error) {
	var r StudyResult
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	err := c.doH(ctx, http.MethodGet, "/control/v1/studies?"+q.values().Encode(), http.Header{OperatorHeader: {operator}}, nil, &r)
	return r, err
}

// SendRequest names what is sent and where: a configured destination by
// name, the patient and the study, and optionally some of its series.
type SendRequest struct {
	Destination      string
	PatientID        string
	StudyInstanceUID string
	SeriesUIDs       []string
}

func (s SendRequest) body() map[string]any {
	body := map[string]any{"destination": s.Destination, "patient_id": s.PatientID, "study_instance_uid": s.StudyInstanceUID}
	if len(s.SeriesUIDs) > 0 {
		body["series_instance_uids"] = append([]string{}, s.SeriesUIDs...)
	}
	return body
}

// SendDestination is the destination of a send plan, as configured on the
// node.
type SendDestination struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	AETitle string `json:"ae_title"`
}

// SendSeries is one series of a send plan.
type SendSeries struct {
	SeriesInstanceUID string `json:"series_instance_uid"`
	Modality          string `json:"modality"`
	Instances         int    `json:"instances"`
}

// SendPlan is LibreDCM's plan of a send: exactly which patient, study,
// series and how many instances would go to which address.
type SendPlan struct {
	Destination      SendDestination `json:"destination"`
	PatientID        string          `json:"patient_id"`
	PatientName      string          `json:"patient_name"`
	PatientBirthDate string          `json:"patient_birth_date"`
	StudyInstanceUID string          `json:"study_instance_uid"`
	StudyDate        string          `json:"study_date"`
	StudyDescription string          `json:"study_description"`
	AccessionNumber  string          `json:"accession_number"`
	Series           []SendSeries    `json:"series"`
	SeriesCount      int             `json:"series_count"`
	Instances        int             `json:"instances"`
	InstancesDigest  string          `json:"instances_digest"`
	Note             string          `json:"note"`
}

// SendPlanAnswer is the answer of POST /control/v1/send/plan.
type SendPlanAnswer struct {
	Instance   InstanceInfo `json:"instance"`
	Plan       SendPlan     `json:"plan"`
	PlanDigest string       `json:"plan_digest"`
}

// SendPlan asks what a send would do. Nothing is sent.
func (c *Client) SendPlan(ctx context.Context, s SendRequest, exp Identity) (SendPlanAnswer, error) {
	var a SendPlanAnswer
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	err := c.do(ctx, http.MethodPost, "/control/v1/send/plan", expect(s.body(), exp), &a)
	return a, err
}

// SendResult is the answer of POST /control/v1/send. Outcome is
// "complete", "partial" or "failed". Every instance of the plan that is in
// neither list was accepted by the destination.
type SendResult struct {
	Instance    InstanceInfo `json:"instance"`
	Outcome     string       `json:"outcome"`
	Plan        SendPlan     `json:"plan"`
	Instances   int          `json:"instances"`
	Completed   int          `json:"completed"`
	Failed      int          `json:"failed"`
	Warning     int          `json:"warning"`
	FailedUIDs  []string     `json:"failed_sop_instance_uids"`
	WarningUIDs []string     `json:"warning_sop_instance_uids"`
	DurationMS  int64        `json:"duration_ms"`
	Note        string       `json:"note"`
}

// Send sends the plan the person approved. LibreDCM builds the plan again
// and sends only when its digest is digest. A send of which no instance
// arrived is answered by LibreDCM with 502 and the counts; Send returns it
// as a result with outcome "failed", so the page can list the instances.
func (c *Client) Send(ctx context.Context, s SendRequest, digest string, env Envelope) (SendResult, error) {
	var r SendResult
	ctx, cancel := context.WithTimeout(ctx, c.send)
	defer cancel()
	body := s.body()
	body["confirm"], body["plan_digest"] = true, digest
	err := c.do(ctx, http.MethodPost, "/control/v1/send", env.body(body), &r)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusBadGateway && ae.Code == "send_failed" {
		var failed SendResult
		if json.Unmarshal(ae.body, &failed) == nil && failed.Outcome == "failed" {
			return failed, nil
		}
	}
	return r, err
}

// RingQuery filters the recent audit records or log lines of a node. Type
// is an event name prefix (audit records), Outcome an audit outcome, Level
// the lowest log level (log lines). A zero Since gives every held entry.
type RingQuery struct {
	Type    string
	Outcome string
	Level   string
	Since   time.Time
	Limit   int
}

func (q RingQuery) values() url.Values {
	v := url.Values{}
	if q.Type != "" {
		v.Set("type", q.Type)
	}
	if q.Outcome != "" {
		v.Set("outcome", q.Outcome)
	}
	if q.Level != "" {
		v.Set("level", q.Level)
	}
	if !q.Since.IsZero() {
		v.Set("since", q.Since.UTC().Format(time.RFC3339))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	return v
}

// Ring is the answer of GET /control/v1/audit and GET /control/v1/logs:
// the most recent entries held in the node's memory, oldest first, each a
// JSON object as written to the log.
type Ring struct {
	Instance    InstanceInfo      `json:"instance"`
	Records     []json.RawMessage `json:"records"` // audit records
	Lines       []json.RawMessage `json:"lines"`   // log lines
	Count       int               `json:"count"`
	More        bool              `json:"more"`
	Limit       int               `json:"limit"`
	BufferSize  int               `json:"buffer_size"`
	AuditLogOff bool              `json:"audit_log_off"`
	Note        string            `json:"note"`
}

// AuditRecords reads the recent audit records of the node.
func (c *Client) AuditRecords(ctx context.Context, q RingQuery) (Ring, error) {
	var r Ring
	q.Level = ""
	err := c.get(ctx, "/control/v1/audit?"+q.values().Encode(), &r)
	return r, err
}

// LogLines reads the recent log lines of the node.
func (c *Client) LogLines(ctx context.Context, q RingQuery) (Ring, error) {
	var r Ring
	q.Type, q.Outcome = "", ""
	err := c.get(ctx, "/control/v1/logs?"+q.values().Encode(), &r)
	return r, err
}
