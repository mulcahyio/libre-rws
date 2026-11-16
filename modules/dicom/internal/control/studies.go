package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

// Study query (control-api-v1.md §5.1, DCM-SRS-091). The query itself is
// the gateway's: one function decides how a key matches and which conflict
// instances are visible, for C-FIND and for this endpoint. This file bounds
// and pages the result, requires the operator's name and writes the audit
// record.

// Data is the part of the gateway that queries the index and sends
// instances for an operator. It is implemented by *gateway.Gateway.
type Data interface {
	QueryIndex(ctx context.Context, q gateway.IndexQuery) (gateway.IndexQueryResult, error)
	PlanSend(ctx context.Context, req gateway.SendRequest) (gateway.SendPlan, error)
	Send(ctx context.Context, req gateway.SendRequest, check func(gateway.SendPlan) error) (gateway.SendResult, error)
	AuditOmitsPatientID() bool
}

// Limits of the study query (DCM-SRS-096).
const (
	studiesDefaultLimit = 50
	studiesMaxLimit     = 200
	// studiesMaxOffset bounds how deep a client can page: a deeper page
	// makes the index walk all rows before it. Narrow the query instead.
	studiesMaxOffset = 10000
	studiesMaxKeys   = 20
	studiesMaxValue  = 256
	studiesTimeout   = 30 * time.Second
	// studiesSlots: queries of the control API that may run at once.
	studiesSlots = 4

	// OperatorHeader carries the name of the person on a GET that is audited.
	OperatorHeader = "X-Libre-Operator"

	auditQuery = "Query"
)

// patientKeys are the matching keys whose values identify a patient. Their
// values are left out of the audit record when audit_omit_patient_id is set.
var patientKeys = map[string]bool{"PatientID": true, "PatientName": true, "PatientBirthDate": true}

// registerData adds the study query and the send. A node without an index
// to query (Data nil) does not serve them and does not list them.
func (a *API) registerData(r Registrar) {
	if a.Data == nil {
		return
	}
	slots := make(chan struct{}, studiesSlots)
	r.HandleControl("GET /control/v1/studies", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		a.studies(w, req, slots)
	}))
	r.HandleControl("POST /control/v1/send/plan", preview(a, a.sendPreview))
	r.HandleControl("POST /control/v1/send", action(a, actionSpec{name: "send", event: auditExported,
		timeout: SendTimeout, slots: make(chan struct{}, 1)}, a.send))
}

// cursorFor makes the opaque cursor of the page that starts at offset. It
// is bound to the query: a cursor of another query is refused.
func cursorFor(offset int, binding string) string {
	sum := sha256.Sum256([]byte(binding))
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset) + "." + hex.EncodeToString(sum[:8])))
}

func parseCursor(cursor, binding string) (int, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, false
	}
	off, tag, ok := strings.Cut(string(raw), ".")
	n, err := strconv.Atoi(off)
	sum := sha256.Sum256([]byte(binding))
	if !ok || err != nil || n < 1 || tag != hex.EncodeToString(sum[:8]) {
		return 0, false
	}
	return n, true
}

var reKeyword = regexp.MustCompile(`^[A-Za-z]{1,64}$`)

func (a *API) studies(w http.ResponseWriter, r *http.Request, slots chan struct{}) {
	if a.Data == nil {
		a.failCode(w, http.StatusInternalServerError, "internal", "the study query is not available on this node")
		return
	}
	operator := cleanText(r.Header.Get(OperatorHeader))
	if operator == "" || utf8.RuneCountInString(operator) > maxOperatorLen {
		a.failCode(w, http.StatusUnprocessableEntity, "operator_required",
			"the header "+OperatorHeader+" is required: the name of the person who asked for this query (1 to 128 characters)")
		return
	}

	q := gateway.IndexQuery{Level: "study", Keys: map[string]string{}, Limit: studiesDefaultLimit}
	cursor := ""
	for name, vals := range r.URL.Query() {
		if len(vals) != 1 {
			a.failCode(w, http.StatusBadRequest, "invalid_query", "a query field is given more than once")
			return
		}
		v := vals[0]
		switch name {
		case "level":
			q.Level = v
		case "limit":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				a.failCode(w, http.StatusBadRequest, "invalid_limit", "limit must be a whole number of 1 or more")
				return
			}
			q.Limit = min(n, studiesMaxLimit)
		case "cursor":
			cursor = v
		default:
			if !reKeyword.MatchString(name) || len(v) > studiesMaxValue || len(q.Keys) >= studiesMaxKeys {
				a.failCode(w, http.StatusBadRequest, "invalid_query",
					"a matching key is not a DICOM keyword, its value is longer than 256 bytes, or there are more than 20 keys")
				return
			}
			q.Keys[name] = v
		}
	}
	// The cursor belongs to one query: level, limit and keys.
	names := make([]string, 0, len(q.Keys))
	for k := range q.Keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var bind strings.Builder
	bind.WriteString(strings.ToLower(q.Level) + "\n" + strconv.Itoa(q.Limit) + "\n")
	for _, k := range names {
		bind.WriteString(k + "=" + q.Keys[k] + "\n")
	}
	if cursor != "" {
		off, ok := parseCursor(cursor, bind.String())
		if !ok {
			a.failCode(w, http.StatusBadRequest, "invalid_cursor", "the cursor does not belong to this query; start again without it")
			return
		}
		q.Offset = off
	}
	if q.Offset > studiesMaxOffset {
		a.failCode(w, http.StatusUnprocessableEntity, "too_many_rows",
			"this query has more than "+strconv.Itoa(studiesMaxOffset)+" rows; narrow it (a date range, a patient, a study)")
		return
	}

	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		a.failCode(w, http.StatusTooManyRequests, "busy", "too many study queries are running; try again in a moment")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), studiesTimeout)
	defer cancel()
	res, err := a.Data.QueryIndex(ctx, q)
	var reqErr *gateway.RequestError
	switch {
	case errors.As(err, &reqErr):
		status := http.StatusUnprocessableEntity
		if reqErr.Code == "invalid_level" || reqErr.Code == "invalid_limit" {
			status = http.StatusBadRequest
		}
		a.failCode(w, status, reqErr.Code, reqErr.Msg)
		return
	case err != nil:
		_ = a.writeAudit(ctx, auditQuery, outcomeSerious, a.queryAudit(operator, q, names)...)
		a.failCode(w, http.StatusInternalServerError, "query_failed", "the index query failed; see the node's log")
		return
	}

	// The audit record is written before the rows leave the node. Without
	// it nothing is returned (DCM-SRS-091).
	attrs := append(a.queryAudit(operator, q, names), "rows", len(res.Rows), "more", res.More, "offset", q.Offset)
	if err := a.writeAudit(context.WithoutCancel(ctx), auditQuery, outcomeSuccess, attrs...); err != nil {
		a.failCode(w, http.StatusInternalServerError, "audit_unavailable",
			"the audit record could not be written; the result is not returned")
		return
	}
	body := map[string]any{"instance": a.instance(), "level": res.Level, "rows": res.Rows, "count": len(res.Rows),
		"limit": q.Limit, "more": res.More, "held_instances": res.Held,
		"note": "Same matching rules and patient-conflict visibility as C-FIND. " +
			"Instances held as a patient conflict are not listed while visibility is hidden."}
	if res.More {
		body["cursor"] = cursorFor(q.Offset+len(res.Rows), bind.String())
	}
	a.writeJSON(w, http.StatusOK, body)
}

// queryAudit builds the fields of a Query audit record. The values of the
// matching keys appear here and nowhere else in the log; the values that
// identify a patient are left out when audit_omit_patient_id is set.
func (a *API) queryAudit(operator string, q gateway.IndexQuery, names []string) []any {
	omit := a.Data.AuditOmitsPatientID()
	keys := make([]string, 0, len(names))
	for _, k := range names {
		v := cleanText(q.Keys[k])
		if omit && patientKeys[k] {
			v = "(omitted)"
		}
		keys = append(keys, k+"="+v)
	}
	return []any{"endpoint", "/control/v1/studies", "op", "control-query", "operator", operator,
		"level", strings.ToLower(q.Level), "keys", keys}
}
