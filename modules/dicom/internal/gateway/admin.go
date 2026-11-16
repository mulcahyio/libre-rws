package gateway

// Functions the control API calls for an operator (DCM-SRS-091, -092): a
// study query and a send to a configured destination. Both reuse the code
// of the DIMSE services: the query goes through keyFilter and FindWith like
// C-FIND, the send through sendInstances like C-MOVE.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

// RequestError is a request the gateway refuses. Code is a machine code the
// control API passes on; the text holds no patient data.
type RequestError struct {
	Code string
	Msg  string
}

func (e *RequestError) Error() string { return e.Msg }

func refuse(code, format string, args ...any) error {
	return &RequestError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// IndexQuery is a study query from the control API.
type IndexQuery struct {
	Level  string            // patient, study, series or image (any case)
	Keys   map[string]string // DICOM keyword -> value, in DICOM matching syntax
	Limit  int               // rows wanted, at least 1
	Offset int               // rows to skip
}

// IndexQueryResult is one page of a study query.
type IndexQueryResult struct {
	Level   string
	Rows    []map[string]string
	More    bool   // rows exist after this page
	Filters int    // matching keys that restricted the result
	Held    string // visibility of held conflict instances: hidden or flagged
}

// multiValued says whether a key takes a list separated by backslashes.
func multiValued(kw string) bool {
	k := queryKeys[kw].kind
	return k == kUID || k == kModality
}

// QueryIndex runs a query with the matching rules and the conflict
// visibility rules of C-FIND (DCM-SRS-091). It refuses a query that no key
// restricts, a key this node does not match on, and a key that does not
// apply at the level: a wider answer than the person asked for is never
// returned silently. It writes no query value to the log.
func (g *Gateway) QueryIndex(ctx context.Context, q IndexQuery) (IndexQueryResult, error) {
	res := IndexQueryResult{Rows: []map[string]string{}, Held: VisibilityHidden}
	if g.flagged() {
		res.Held = VisibilityFlagged
	}
	level := index.Level(strings.ToUpper(strings.TrimSpace(q.Level)))
	switch level {
	case index.Patient, index.Study, index.Series, index.Image:
	default:
		return res, refuse("invalid_level", "level must be one of patient, study, series, image")
	}
	res.Level = strings.ToLower(string(level))
	if q.Limit < 1 || q.Offset < 0 {
		return res, refuse("invalid_limit", "limit must be 1 or more")
	}

	names := make([]string, 0, len(q.Keys))
	for kw := range q.Keys {
		names = append(names, kw)
	}
	sort.Strings(names)
	var filters []index.Filter
	for _, kw := range names {
		vals := []string{q.Keys[kw]}
		if multiValued(kw) {
			vals = strings.Split(q.Keys[kw], `\`)
		}
		f, use := keyFilter(level, kw, vals, g.queryOptions())
		switch use {
		case keyUnknown:
			return res, refuse("unknown_key", "%s is not a matching key of this node", kw)
		case keyNotAtLevel:
			return res, refuse("key_not_at_level", "%s cannot be matched at the %s level", kw, res.Level)
		case keyMatches:
			filters = append(filters, f)
		}
	}
	if len(filters) == 0 {
		return res, refuse("no_matching_key", "give at least one matching key with a value; the whole archive is not listed")
	}
	res.Filters = len(filters)

	opts := g.findOptions()
	opts.Offset = q.Offset
	rows, err := g.idx.FindWith(ctx, level, filters, q.Limit+1, opts)
	if err != nil {
		g.log.Error("control query failed", "level", res.Level, "err", err)
		return res, fmt.Errorf("index query failed")
	}
	if len(rows) > q.Limit {
		rows, res.More = rows[:q.Limit], true
	}
	var mods map[string][]string
	if level == index.Study {
		uids := make([]string, len(rows))
		for i, r := range rows {
			uids[i] = r["StudyInstanceUID"]
		}
		mods, _ = g.idx.ModalitiesForStudies(ctx, uids)
	}
	for _, r := range rows {
		out := make(map[string]string, len(r)+1)
		for k, v := range r {
			if !strings.HasPrefix(k, "_") { // internal columns (archive key) stay inside
				out[k] = v
			}
		}
		if level == index.Image {
			out["TransferSyntaxUID"] = r["_TransferSyntaxUID"]
		}
		if id := r["_ConflictID"]; id != "" {
			out["ConflictID"] = id
		}
		if mods != nil {
			out["ModalitiesInStudy"] = strings.Join(mods[r["StudyInstanceUID"]], `\`)
		}
		res.Rows = append(res.Rows, out)
	}
	return res, nil
}

// MaxSendInstances bounds one send from the control API (DCM-SRS-096).
const MaxSendInstances = 5000

// sendLimit is MaxSendInstances; a test lowers it.
var sendLimit = MaxSendInstances

// SendRequest asks to send a study, some of its series or some of its
// instances to a configured destination.
type SendRequest struct {
	Destination     string   // name of a configured remote AE
	StudyUID        string   // required
	SeriesUIDs      []string // empty: every series of the study
	SOPInstanceUIDs []string // empty: every instance of the chosen series
	PatientID       string   // required; must be the study's indexed Patient ID
}

// SendDestination is where a send goes.
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

// SendPlan says exactly what a send would do (DCM-SRS-092).
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
	// InstancesDigest covers the SOP Instance UIDs and archive keys that
	// would be sent: it changes when an instance is added or replaced.
	InstancesDigest string `json:"instances_digest"`
	Note            string `json:"note"`
}

// SendResult is the outcome of a send, per sub-operation.
type SendResult struct {
	Plan        SendPlan
	Completed   int
	Failed      int
	Warning     int
	FailedUIDs  []string
	WarningUIDs []string
	Duration    time.Duration
}

const sendNote = "Instances held as a patient conflict are never sent. Each instance is sent in the encoding it is stored in."

// planSend validates a send and returns its plan and the rows to send. It
// changes nothing.
func (g *Gateway) planSend(ctx context.Context, req SendRequest) (SendPlan, []index.Row, RemoteAE, error) {
	var plan SendPlan
	dest, ok := g.remoteAE(req.Destination)
	if !ok || req.Destination == "" {
		return plan, nil, dest, refuse("unknown_destination", "the destination is not a configured remote AE; nothing was sent")
	}
	plan.Destination = SendDestination{Name: req.Destination, Host: dest.Host, Port: dest.Port, AETitle: dest.AETitle}
	if req.StudyUID == "" {
		return plan, nil, dest, refuse("study_required", "study_instance_uid is required")
	}
	if req.PatientID == "" {
		return plan, nil, dest, refuse("patient_id_required", "patient_id is required: name the patient as well as the study")
	}

	// The zero FindOptions return indexed instances only: an instance held
	// as a patient conflict, or accepted as a separate study under another
	// patient, is never part of a send (DCM-SRS-092, HAZ-013).
	var none index.FindOptions
	study := []index.Filter{{Column: "st.study_uid", Op: index.OpEq, Values: []string{req.StudyUID}}}
	studies, err := g.idx.FindWith(ctx, index.Study, study, 2, none)
	if err != nil {
		g.log.Error("control send: study lookup failed", "err", err)
		return plan, nil, dest, fmt.Errorf("index lookup failed")
	}
	if len(studies) != 1 {
		return plan, nil, dest, refuse("study_not_found", "the study is not in the index; nothing was sent")
	}
	st := studies[0]
	if st["PatientID"] != req.PatientID {
		return plan, nil, dest, refuse("patient_mismatch",
			"patient_id is not the Patient ID this study is indexed under; nothing was sent")
	}
	plan.PatientID, plan.PatientName, plan.PatientBirthDate = st["PatientID"], st["PatientName"], st["PatientBirthDate"]
	plan.StudyInstanceUID, plan.StudyDate = st["StudyInstanceUID"], st["StudyDate"]
	plan.StudyDescription, plan.AccessionNumber = st["StudyDescription"], st["AccessionNumber"]

	filters := study
	if len(req.SeriesUIDs) > 0 {
		filters = append(filters, index.Filter{Column: "se.series_uid", Op: index.OpIn, Values: req.SeriesUIDs})
	}
	if len(req.SOPInstanceUIDs) > 0 {
		filters = append(filters, index.Filter{Column: "i.sop_instance_uid", Op: index.OpIn, Values: req.SOPInstanceUIDs})
	}
	rows, err := g.idx.FindWith(ctx, index.Image, filters, sendLimit+1, none)
	if err != nil {
		g.log.Error("control send: instance lookup failed", "err", err)
		return plan, nil, dest, fmt.Errorf("index lookup failed")
	}
	if len(rows) > sendLimit {
		return plan, nil, dest, refuse("too_many_instances",
			"the send holds more than %d instances; send it series by series", sendLimit)
	}
	if len(rows) == 0 {
		return plan, nil, dest, refuse("nothing_to_send", "no indexed instance matches; nothing was sent")
	}

	bySeries, haveSOP := map[string]*SendSeries{}, map[string]bool{}
	var order []string
	sum := sha256.New()
	for _, r := range rows {
		// Defence in depth: every row must belong to the named study and patient.
		if r["StudyInstanceUID"] != req.StudyUID || r["PatientID"] != req.PatientID {
			g.log.Error("control send: index returned a row of another study or patient; refusing")
			return plan, nil, dest, fmt.Errorf("index returned an instance of another study or patient")
		}
		se := r["SeriesInstanceUID"]
		if bySeries[se] == nil {
			bySeries[se] = &SendSeries{SeriesInstanceUID: se, Modality: r["Modality"]}
			order = append(order, se)
		}
		bySeries[se].Instances++
		haveSOP[r["SOPInstanceUID"]] = true
		fmt.Fprintf(sum, "%s\x00%s\x00%s\n", r["SOPInstanceUID"], r["_BlobKey"], r["_TransferSyntaxUID"])
	}
	for _, se := range req.SeriesUIDs {
		if bySeries[se] == nil {
			return plan, nil, dest, refuse("series_not_in_study", "a chosen series is not an indexed series of this study; nothing was sent")
		}
	}
	for _, sop := range req.SOPInstanceUIDs {
		if !haveSOP[sop] {
			return plan, nil, dest, refuse("instance_not_in_study", "a chosen instance is not an indexed instance of this study and series; nothing was sent")
		}
	}
	for _, se := range order {
		plan.Series = append(plan.Series, *bySeries[se])
	}
	plan.SeriesCount, plan.Instances = len(order), len(rows)
	plan.InstancesDigest = "sha256:" + hex.EncodeToString(sum.Sum(nil))
	plan.Note = sendNote
	return plan, rows, dest, nil
}

// PlanSend describes what Send would do. Nothing is sent.
func (g *Gateway) PlanSend(ctx context.Context, req SendRequest) (SendPlan, error) {
	plan, _, _, err := g.planSend(ctx, req)
	return plan, err
}

// Send sends the instances of req to its destination over the C-MOVE send
// path. It builds the plan again, hands it to check, and sends exactly the
// instances of that plan when check returns nil: what the person approved
// is what goes out.
func (g *Gateway) Send(ctx context.Context, req SendRequest, check func(SendPlan) error) (SendResult, error) {
	plan, rows, dest, err := g.planSend(ctx, req)
	res := SendResult{Plan: plan}
	if err != nil {
		return res, err
	}
	if check != nil {
		if err := check(plan); err != nil {
			return res, err
		}
	}
	start := time.Now()
	tally := g.sendInstances(ctx, dest, rows)
	res.Completed, res.Failed, res.Warning = tally.completed, tally.failed, tally.warning
	res.FailedUIDs, res.WarningUIDs = tally.failedUIDs, tally.warningUIDs
	res.Duration = time.Since(start)
	g.log.Info("control send finished", "destination", req.Destination, "instances", len(rows),
		"completed", tally.completed, "failed", tally.failed, "warning", tally.warning, "seconds", res.Duration.Seconds())
	return res, nil
}

// ValidateRemoteAE checks one destination entry: the name and the called AE
// title are DICOM AE titles, the host is a host name or IP address and the
// port is a TCP port. The control API and the overlay loader both use it.
func ValidateRemoteAE(name string, r RemoteAE) error {
	if !validAE(name) {
		return fmt.Errorf("remote AE name %q: must be 1 to 16 printable characters", name)
	}
	if r.AETitle != "" && !validAE(r.AETitle) {
		return fmt.Errorf("remote AE %s: ae_title must be 1 to 16 printable characters", name)
	}
	if !validHostName(r.Host) {
		return fmt.Errorf("remote AE %s: host must be a host name or an IP address", name)
	}
	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("remote AE %s: port must be 1 to 65535", name)
	}
	return nil
}

func validAE(s string) bool {
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

func validHostName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_', c == ':':
		default:
			return false
		}
	}
	// A colon is only part of an IPv6 address.
	if strings.Contains(s, ":") {
		_, err := netip.ParseAddr(s)
		return err == nil
	}
	return true
}
