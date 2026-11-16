package control

// Tests of the study query and the send of the control API
// (DCM-SRS-091, -092, -096) with a stand-in for the gateway, plus the study
// query over HTTP against a real gateway and index. The gateway's own
// behaviour (matching rules, conflict visibility, what a send sends) is
// tested in the gateway package and, end to end, in cmd/libredcm.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/blob"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

// fakeData stands for the gateway.
type fakeData struct {
	mu      sync.Mutex
	queries []gateway.IndexQuery
	total   int   // rows the query has in all
	qErr    error // returned by QueryIndex
	omit    bool  // audit_omit_patient_id
	hold    chan struct{}
	started chan struct{}

	state     string // part of the plan: changes when "the study changes"
	planErr   error
	sent      []gateway.SendRequest
	completed func(n int) (completed, failed, warning int)
}

func (f *fakeData) AuditOmitsPatientID() bool { return f.omit }

func (f *fakeData) QueryIndex(_ context.Context, q gateway.IndexQuery) (gateway.IndexQueryResult, error) {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	hold, started := f.hold, f.started
	f.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if hold != nil {
		<-hold
	}
	res := gateway.IndexQueryResult{Level: strings.ToLower(q.Level), Rows: []map[string]string{}, Held: gateway.VisibilityHidden, Filters: len(q.Keys)}
	if f.qErr != nil {
		return res, f.qErr
	}
	for i := q.Offset; i < f.total && len(res.Rows) < q.Limit; i++ {
		res.Rows = append(res.Rows, map[string]string{"StudyInstanceUID": fmt.Sprintf("1.2.3.%d", i), "PatientID": "QP1", "PatientName": "DOE^JANE"})
	}
	res.More = q.Offset+len(res.Rows) < f.total
	return res, nil
}

func (f *fakeData) queryCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.queries) }

func (f *fakeData) plan(req gateway.SendRequest) (gateway.SendPlan, error) {
	if f.planErr != nil {
		return gateway.SendPlan{}, f.planErr
	}
	return gateway.SendPlan{
		Destination: gateway.SendDestination{Name: req.Destination, Host: "10.0.0.20", Port: 104, AETitle: req.Destination},
		PatientID:   req.PatientID, PatientName: "DOE^JANE", StudyInstanceUID: req.StudyUID, StudyDate: "20260110",
		Series:      []gateway.SendSeries{{SeriesInstanceUID: req.StudyUID + ".1", Modality: "CT", Instances: 3}},
		SeriesCount: 1, Instances: 3, InstancesDigest: "sha256:" + f.state, Note: "note"}, nil
}

func (f *fakeData) PlanSend(_ context.Context, req gateway.SendRequest) (gateway.SendPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.plan(req)
}

func (f *fakeData) Send(_ context.Context, req gateway.SendRequest, check func(gateway.SendPlan) error) (gateway.SendResult, error) {
	f.mu.Lock()
	p, err := f.plan(req)
	hold, started := f.hold, f.started
	f.mu.Unlock()
	res := gateway.SendResult{Plan: p}
	if err != nil {
		return res, err
	}
	if check != nil {
		if err := check(p); err != nil {
			return res, err
		}
	}
	if started != nil {
		started <- struct{}{}
	}
	if hold != nil {
		<-hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, req)
	res.Completed = p.Instances
	if f.completed != nil {
		res.Completed, res.Failed, res.Warning = f.completed(p.Instances)
		for i := 0; i < res.Failed; i++ {
			res.FailedUIDs = append(res.FailedUIDs, fmt.Sprintf("%s.1.%d", req.StudyUID, i+1))
		}
		for i := 0; i < res.Warning; i++ {
			res.WarningUIDs = append(res.WarningUIDs, fmt.Sprintf("%s.1.%d", req.StudyUID, res.Failed+i+1))
		}
	}
	res.Duration = 1500 * time.Millisecond
	return res, nil
}

func (f *fakeData) sentCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }

// dataEnv is newActEnv plus the study query, the send and the
// configuration edit, served by stand-ins.
type dataEnv struct {
	*actEnv
	data *fakeData
	ed   *fakeEditor
}

func newDataEnv(t *testing.T) *dataEnv {
	t.Helper()
	a := newActEnv(t)
	d := &dataEnv{actEnv: a, data: &fakeData{total: 3, state: "s1"}}
	d.ed = newFakeEditor(a.live)
	a.api.Data, a.api.Editor = d.data, d.ed
	a.api.registerData(a.rt)
	a.api.registerConfigEdit(a.rt)
	a.h = a.rt.AdminHandler()
	return d
}

// query sends GET /control/v1/studies with the operator header.
func (e *env) query(t *testing.T, operator, rawQuery string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return e.queryAs(t, testToken, operator, rawQuery)
}

func (e *env) queryAs(t *testing.T, token, operator, rawQuery string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/control/v1/studies?"+rawQuery, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if operator != "" {
		req.Header.Set(OperatorHeader, operator)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// ---- DCM-SRS-091: study query ----

// The study query needs the admin token and the name of the person; without
// either no query runs. It is a GET and nothing else.
// Verifies: DCM-SRS-091, DCM-SRS-080
func TestStudiesNeedTokenAndOperator(t *testing.T) {
	d := newDataEnv(t)
	for _, tok := range []string{"", "wrong", testToken + "x"} {
		if rec, _ := d.queryAs(t, tok, "Pat Admin", "PatientID=QP1"); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q = %d, want 401", tok, rec.Code)
		}
	}
	for name, op := range map[string]string{"none": "", "blank": "  \t ", "too long": strings.Repeat("x", 129)} {
		rec, body := d.query(t, op, "PatientID=QP1")
		if rec.Code != http.StatusUnprocessableEntity || body["code"] != "operator_required" {
			t.Errorf("operator %s = %d %v", name, rec.Code, body)
		}
	}
	if n := d.data.queryCount(); n != 0 {
		t.Fatalf("%d queries ran without token or operator", n)
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if rec, _ := d.do(t, m, "/control/v1/studies?PatientID=QP1", testToken); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", m, rec.Code)
		}
	}
	rec, body := d.query(t, "Pat Admin", "PatientID=QP1")
	if rec.Code != 200 || body["count"] != float64(3) || body["more"] != false || body["level"] != "study" ||
		body["held_instances"] != "hidden" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("query = %d %v", rec.Code, body)
	}
	if _, has := body["cursor"]; has {
		t.Fatal("a complete result carries a cursor")
	}
	if inst, _ := body["instance"].(map[string]any); inst["node_id"] != "dcm-1" {
		t.Fatalf("instance = %v", inst)
	}
	if q := d.data.queries[0]; q.Level != "study" || q.Limit != studiesDefaultLimit || q.Offset != 0 || q.Keys["PatientID"] != "QP1" || len(q.Keys) != 1 {
		t.Fatalf("query passed on = %+v", q)
	}
}

// The result is bounded and paged: at most 200 rows per page, a cursor when
// more rows exist, never a silent cut; a cursor belongs to one query; a
// malformed query is refused.
// Verifies: DCM-SRS-091, DCM-SRS-096
func TestStudiesPagingAndBounds(t *testing.T) {
	d := newDataEnv(t)
	d.data.total = 450
	// The limit is clamped to 200.
	rec, body := d.query(t, "Pat Admin", "level=image&PatientID=QP1&limit=100000")
	if rec.Code != 200 || body["count"] != float64(200) || body["limit"] != float64(200) || body["more"] != true {
		t.Fatalf("clamped page = %d count %v limit %v more %v", rec.Code, body["count"], body["limit"], body["more"])
	}
	// Walk the pages with the cursor.
	seen, pages, cursor := map[string]bool{}, 0, ""
	for {
		q := "level=image&PatientID=QP1&limit=100"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		rec, body := d.query(t, "Pat Admin", q)
		if rec.Code != 200 {
			t.Fatalf("page %d = %d %v", pages, rec.Code, body)
		}
		rows := body["rows"].([]any)
		if len(rows) > 100 {
			t.Fatalf("page of %d rows", len(rows))
		}
		for _, r := range rows {
			uid := r.(map[string]any)["StudyInstanceUID"].(string)
			if seen[uid] {
				t.Fatalf("row %s returned twice", uid)
			}
			seen[uid] = true
		}
		pages++
		if body["more"] != true {
			if _, has := body["cursor"]; has {
				t.Fatal("last page carries a cursor")
			}
			break
		}
		cursor, _ = body["cursor"].(string)
		if cursor == "" {
			t.Fatal("more rows exist but there is no cursor")
		}
	}
	if len(seen) != 450 || pages != 5 {
		t.Fatalf("%d rows in %d pages, want 450 in 5", len(seen), pages)
	}

	// A cursor belongs to its query: other keys, level or limit refuse it.
	_, first := d.query(t, "Pat Admin", "level=image&PatientID=QP1&limit=100")
	cur := url.QueryEscape(first["cursor"].(string))
	before := d.data.queryCount()
	for name, q := range map[string]string{
		"other value": "level=image&PatientID=QP2&limit=100&cursor=" + cur,
		"other key":   "level=image&PatientID=QP1&StudyDate=20260110&limit=100&cursor=" + cur,
		"other level": "level=series&PatientID=QP1&limit=100&cursor=" + cur,
		"other limit": "level=image&PatientID=QP1&limit=50&cursor=" + cur,
		"garbage":     "level=image&PatientID=QP1&limit=100&cursor=abc",
		"offset 0":    "level=image&PatientID=QP1&limit=100&cursor=" + url.QueryEscape(cursorFor(0, "image\n100\nPatientID=QP1\n")),
		"negative":    "level=image&PatientID=QP1&limit=100&cursor=" + url.QueryEscape(cursorFor(-5, "image\n100\nPatientID=QP1\n")),
	} {
		if rec, body := d.query(t, "Pat Admin", q); rec.Code != http.StatusBadRequest || body["code"] != "invalid_cursor" {
			t.Errorf("cursor, %s = %d %v", name, rec.Code, body)
		}
	}
	// Paging stops at 10 000 rows: narrow the query.
	deep := url.QueryEscape(cursorFor(studiesMaxOffset+1, "image\n100\nPatientID=QP1\n"))
	if rec, body := d.query(t, "Pat Admin", "level=image&PatientID=QP1&limit=100&cursor="+deep); rec.Code != 422 || body["code"] != "too_many_rows" {
		t.Errorf("deep page = %d %v", rec.Code, body)
	}
	// Malformed queries.
	many := ""
	for i := 0; i < 21; i++ {
		many += fmt.Sprintf("&Key%c=x", 'A'+i)
	}
	for name, c := range map[string]struct{ q, code string }{
		"limit 0":        {"PatientID=QP1&limit=0", "invalid_limit"},
		"limit text":     {"PatientID=QP1&limit=ten", "invalid_limit"},
		"limit negative": {"PatientID=QP1&limit=-1", "invalid_limit"},
		"key twice":      {"PatientID=QP1&PatientID=QP2", "invalid_query"},
		"level twice":    {"PatientID=QP1&level=study&level=image", "invalid_query"},
		"not a keyword":  {"PatientID=QP1&st.patient_id=x", "invalid_query"},
		"sql in key":     {"PatientID=QP1&" + url.QueryEscape("a OR 1=1") + "=x", "invalid_query"},
		"long value":     {"PatientID=" + strings.Repeat("x", 257), "invalid_query"},
		"21 keys":        {"PatientID=QP1" + many, "invalid_query"},
	} {
		if rec, body := d.query(t, "Pat Admin", c.q); rec.Code != http.StatusBadRequest || body["code"] != c.code {
			t.Errorf("%s = %d %v, want 400 %s", name, rec.Code, body, c.code)
		}
	}
	if n := d.data.queryCount(); n != before {
		t.Fatalf("%d refused requests reached the index", n-before)
	}
	// What the gateway refuses is passed on with its code.
	for code, status := range map[string]int{"no_matching_key": 422, "unknown_key": 422, "key_not_at_level": 422, "invalid_level": 400} {
		d.data.qErr = &gateway.RequestError{Code: code, Msg: "refused"}
		if rec, body := d.query(t, "Pat Admin", "PatientID=QP1"); rec.Code != status || body["code"] != code {
			t.Errorf("gateway refusal %s = %d %v", code, rec.Code, body)
		}
	}
}

// Each query leaves one audit record with the operator, the level, the keys
// and the number of rows. The query values appear in that record only: in
// no other log line. With audit_omit_patient_id the values that identify a
// patient are left out. When the record cannot be written, no row leaves
// the node.
// Verifies: DCM-SRS-091, DCM-SRS-086
func TestStudiesAuditAndLogSecrecy(t *testing.T) {
	d := newDataEnv(t)
	const pid, name, acc = "QP-SECRET-771", "ZZTOP^SYNTHETIC", "ACC-551-X"
	q := "level=study&PatientID=" + pid + "&PatientName=" + url.QueryEscape(name) + "&AccessionNumber=" + acc
	rec, body := d.query(t, "  Pat\nAdmin ", q)
	if rec.Code != 200 {
		t.Fatalf("query = %d %v", rec.Code, body)
	}
	recs := findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": "Query"})
	if len(recs) != 1 {
		t.Fatalf("%d Query audit records, want 1: %v", len(recs), recs)
	}
	r := recs[0]
	if r["operator"] != "Pat Admin" || r["outcome"] != "success" || r["level"] != "study" || r["rows"] != float64(3) ||
		r["component"] != "control" || r["endpoint"] != "/control/v1/studies" || r["more"] != false ||
		fmt.Sprint(r["keys"]) != "[AccessionNumber="+acc+" PatientID="+pid+" PatientName="+name+"]" {
		t.Fatalf("audit record = %v", r)
	}
	// No log line holds a query value.
	lines, _ := d.rt.Logs.Select(10000, nil)
	for _, l := range lines {
		for _, v := range []string{pid, name, acc} {
			if strings.Contains(string(l.Line), v) {
				t.Errorf("log line holds the query value %s: %s", v, l.Line)
			}
		}
	}

	// Patient-ID omission.
	d.data.omit = true
	if rec, _ := d.query(t, "Pat Admin", q+"&PatientBirthDate=19700101"); rec.Code != 200 {
		t.Fatalf("query = %d", rec.Code)
	}
	recs = findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": "Query"})
	last := fmt.Sprint(recs[len(recs)-1]["keys"])
	if len(recs) != 2 || strings.Contains(last, pid) || strings.Contains(last, name) || strings.Contains(last, "19700101") ||
		last != "[AccessionNumber="+acc+" PatientBirthDate=(omitted) PatientID=(omitted) PatientName=(omitted)]" {
		t.Fatalf("audit keys with omission = %v", last)
	}
	d.data.omit = false

	// A failed index query is recorded as a failure.
	d.data.qErr = errors.New("database is locked")
	if rec, body := d.query(t, "Pat Admin", q); rec.Code != 500 || body["code"] != "query_failed" || strings.Contains(rec.Body.String(), "locked") {
		t.Fatalf("failed query = %d %s", rec.Code, rec.Body)
	}
	if recs := findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": "Query", "outcome": "serious-failure"}); len(recs) != 1 {
		t.Fatalf("failure records = %v", recs)
	}
	d.data.qErr = nil

	// No audit record, no rows.
	working := d.api.AuditLog
	for name, log := range map[string]*slog.Logger{
		"broken sink": slog.New(failingHandler{slog.NewJSONHandler(io.Discard, nil)}),
		"no logger":   nil,
	} {
		d.api.AuditLog = log
		rec, body := d.query(t, "Pat Admin", q)
		if rec.Code != 500 || body["code"] != "audit_unavailable" || body["rows"] != nil || strings.Contains(rec.Body.String(), "DOE^JANE") {
			t.Errorf("%s: query = %d %s", name, rec.Code, rec.Body)
		}
	}
	d.api.AuditLog = working
}

// At most four study queries run at once; a fifth is refused at once and
// does not wait.
// Verifies: DCM-SRS-096
func TestStudiesConcurrencyLimit(t *testing.T) {
	d := newDataEnv(t)
	d.data.hold, d.data.started = make(chan struct{}), make(chan struct{}, studiesSlots)
	var wg sync.WaitGroup
	for i := 0; i < studiesSlots; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rec, _ := d.query(t, "Pat Admin", "PatientID=QP1"); rec.Code != 200 {
				t.Errorf("held query = %d", rec.Code)
			}
		}()
	}
	for i := 0; i < studiesSlots; i++ {
		select {
		case <-d.data.started:
		case <-time.After(10 * time.Second):
			t.Fatal("the queries did not start")
		}
	}
	done := make(chan int, 1)
	go func() {
		rec, _ := d.query(t, "Pat Admin", "PatientID=QP1")
		done <- rec.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusTooManyRequests {
			t.Errorf("fifth query = %d, want 429", code)
		}
	case <-time.After(5 * time.Second):
		t.Error("the fifth query waited instead of being refused: the limit is not enforced")
	}
	if n := d.data.queryCount(); n != studiesSlots {
		t.Errorf("%d queries reached the index, want %d", n, studiesSlots)
	}
	close(d.data.hold)
	wg.Wait()
	d.data.mu.Lock()
	d.data.hold, d.data.started = nil, nil
	d.data.mu.Unlock()
	if rec, _ := d.query(t, "Pat Admin", "PatientID=QP1"); rec.Code != 200 {
		t.Fatalf("query after the others finished = %d", rec.Code)
	}
}

// realGatewayEnv serves the study query from a real gateway on the index
// of newEnv: P1 has one indexed instance of study S1; one instance of the
// same study sent as P2 is held as an open patient conflict.
func realGatewayEnv(t *testing.T, visibility string) *env {
	t.Helper()
	e := newEnv(t, testToken)
	blobs, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := gateway.DefaultConfig()
	cfg.PatientConflictVisibility = visibility
	gw := gateway.New(cfg, "dcm-1", e.rt.Logger, prometheus.NewRegistry(), blobs, e.idx, nopPub{})
	e.api.Data, e.api.AuditLog = gw, e.rt.Logger
	e.api.registerData(e.rt)
	e.h = e.rt.AdminHandler()
	return e
}

// Over HTTP, against the real gateway and index: an instance held as a
// patient conflict is not returned at any level while visibility is hidden,
// whichever key asks for it; with visibility flagged it is returned under
// its own Patient ID and marked with its conflict.
// Verifies: DCM-SRS-091, DCM-SRS-073
func TestStudiesHideHeldConflictInstances(t *testing.T) {
	rows := func(e *env, q string) []map[string]any {
		t.Helper()
		rec, body := e.query(t, "Pat Admin", q)
		if rec.Code != 200 {
			t.Fatalf("GET studies?%s = %d %s", q, rec.Code, rec.Body)
		}
		var out []map[string]any
		for _, r := range body["rows"].([]any) {
			out = append(out, r.(map[string]any))
		}
		return out
	}
	hidden := realGatewayEnv(t, gateway.VisibilityHidden)
	for q, want := range map[string]int{
		"level=study&PatientID=P2":          0,
		"level=study&PatientID=P*":          1,
		"level=study&PatientName=DOE*":      1,
		"level=study&StudyInstanceUID=S1":   1,
		"level=patient&PatientID=P*":        1,
		"level=patient&PatientID=P2":        0,
		"level=series&StudyInstanceUID=S1":  1,
		"level=series&PatientID=P2":         0,
		"level=image&StudyInstanceUID=S1":   1,
		"level=image&SOPInstanceUID=I2":     0,
		"level=image&PatientID=P2":          0,
		"level=image&SeriesInstanceUID=SE1": 1,
	} {
		got := rows(hidden, q)
		if len(got) != want {
			t.Errorf("hidden: %s returned %d rows, want %d: %v", q, len(got), want, got)
		}
		for _, r := range got {
			if r["PatientID"] == "P2" || r["SOPInstanceUID"] == "I2" || r["ConflictID"] != nil {
				t.Errorf("hidden: %s returned the held instance: %v", q, r)
			}
		}
	}
	if rec, body := hidden.query(t, "Pat Admin", "level=study&PatientID=P1"); rec.Code != 200 || body["held_instances"] != "hidden" {
		t.Fatalf("held_instances = %v", body["held_instances"])
	}

	flagged := realGatewayEnv(t, gateway.VisibilityFlagged)
	if got := rows(flagged, "level=study&PatientID=P2"); len(got) != 1 || got[0]["ConflictID"] == nil || got[0]["ConflictID"] == "" {
		t.Fatalf("flagged: P2 study = %v", got)
	}
	if got := rows(flagged, "level=image&StudyInstanceUID=S1"); len(got) != 2 {
		t.Fatalf("flagged: images of S1 = %v", got)
	}
	if rec, body := flagged.query(t, "Pat Admin", "level=study&PatientID=P1"); rec.Code != 200 || body["held_instances"] != "flagged" {
		t.Fatalf("held_instances = %v", body["held_instances"])
	}
	// The whole archive is not listed, and the refusal comes from the gateway.
	for q, code := range map[string]string{"level=study": "no_matching_key", "level=study&PatientID=*": "no_matching_key",
		"level=study&PatientID=": "no_matching_key", "level=study&Rows=4": "unknown_key", "level=archive&PatientID=P1": "invalid_level",
		"level=study&SOPInstanceUID=I1": "key_not_at_level"} {
		if rec, body := hidden.query(t, "Pat Admin", q); rec.Code/100 != 4 || body["code"] != code || body["rows"] != nil {
			t.Errorf("%s = %d %v, want refusal %s", q, rec.Code, body, code)
		}
	}
}

// ---- DCM-SRS-092: send ----

func sendBody(id string, kv ...any) map[string]any {
	m := act(id, "destination", "VIEWER1", "patient_id", "QP1", "study_instance_uid", "1.2.3")
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func (d *dataEnv) sendPlan(t *testing.T) (map[string]any, string) {
	t.Helper()
	rec, body := d.post(t, "/control/v1/send/plan", map[string]any{"destination": "VIEWER1", "patient_id": "QP1", "study_instance_uid": "1.2.3"})
	if rec.Code != 200 {
		t.Fatalf("send plan = %d %s", rec.Code, rec.Body)
	}
	return body["plan"].(map[string]any), body["plan_digest"].(string)
}

// The plan says what would be sent and where to, and sends nothing. The
// send needs the confirmation and the digest of that plan; a plan that
// changed is refused and nothing is sent. The outcome is reported and
// audited with operator, destination, study and patient.
// Verifies: DCM-SRS-092, DCM-SRS-086
func TestSendPlanThenApply(t *testing.T) {
	d := newDataEnv(t)
	plan, digest := d.sendPlan(t)
	dest := plan["destination"].(map[string]any)
	if dest["name"] != "VIEWER1" || dest["host"] != "10.0.0.20" || dest["port"] != float64(104) || dest["ae_title"] != "VIEWER1" ||
		plan["patient_id"] != "QP1" || plan["patient_name"] != "DOE^JANE" || plan["study_instance_uid"] != "1.2.3" ||
		plan["instances"] != float64(3) || plan["series_count"] != float64(1) || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("plan = %v digest %s", plan, digest)
	}
	if _, again := d.sendPlan(t); again != digest {
		t.Fatal("the digest of an unchanged plan is not stable")
	}
	if d.data.sentCount() != 0 || len(auditRecs(t, d.rt.Audit)) != 0 {
		t.Fatal("a plan sent something or wrote an audit record")
	}

	// The guard.
	for name, c := range map[string]struct {
		fields []any
		status int
		code   string
	}{
		"not confirmed":         {[]any{"plan_digest", digest}, 422, "not_confirmed"},
		"confirm false":         {[]any{"plan_digest", digest, "confirm", false}, 422, "not_confirmed"},
		"no digest":             {[]any{"confirm", true}, 422, "plan_digest_required"},
		"wrong digest":          {[]any{"confirm", true, "plan_digest", "sha256:0000"}, 409, "plan_changed"},
		"digest of other dest":  {[]any{"confirm", true, "plan_digest", digest, "destination", "ENVDEST"}, 409, "plan_changed"},
		"digest, other study":   {[]any{"confirm", true, "plan_digest", digest, "study_instance_uid", "1.2.4"}, 409, "plan_changed"},
		"digest, other patient": {[]any{"confirm", true, "plan_digest", digest, "patient_id", "QP2"}, 409, "plan_changed"},
	} {
		rec, body := d.post(t, "/control/v1/send", sendBody(rid(), c.fields...))
		if rec.Code != c.status || body["code"] != c.code {
			t.Errorf("%s = %d %v, want %d %s", name, rec.Code, body, c.status, c.code)
		}
		if len(findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": auditRefused, "code": c.code})) == 0 {
			t.Errorf("%s: no refusal audit record", name)
		}
	}
	// The study changes after the plan was shown.
	d.data.state = "s2"
	if rec, body := d.post(t, "/control/v1/send", sendBody(rid(), "confirm", true, "plan_digest", digest)); rec.Code != 409 || body["code"] != "plan_changed" {
		t.Fatalf("send with a stale plan = %d %v", rec.Code, body)
	}
	if n := d.data.sentCount(); n != 0 {
		t.Fatalf("%d refused sends went out", n)
	}

	// The approved plan goes out, once.
	_, digest = d.sendPlan(t)
	id := rid()
	rec, body := d.post(t, "/control/v1/send", sendBody(id, "confirm", true, "plan_digest", digest))
	if rec.Code != 200 || body["outcome"] != "complete" || body["completed"] != float64(3) || body["failed"] != float64(0) ||
		body["instances"] != float64(3) || body["duration_ms"] != float64(1500) ||
		len(body["failed_sop_instance_uids"].([]any)) != 0 || len(body["warning_sop_instance_uids"].([]any)) != 0 {
		t.Fatalf("send = %d %v", rec.Code, body)
	}
	if d.data.sentCount() != 1 || fmt.Sprintf("%+v", d.data.sent[0]) != fmt.Sprintf("%+v", gateway.SendRequest{Destination: "VIEWER1", StudyUID: "1.2.3", PatientID: "QP1"}) {
		t.Fatalf("sent = %+v", d.data.sent)
	}
	rec, again := d.post(t, "/control/v1/send", sendBody(id, "confirm", true, "plan_digest", digest))
	if rec.Code != 200 || rec.Header().Get("Idempotent-Replay") != "true" || again["outcome"] != "complete" || d.data.sentCount() != 1 {
		t.Fatalf("repeated request = %d %v, sent %d times", rec.Code, again, d.data.sentCount())
	}
	recs := findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": auditExported, "request_id": id})
	if len(recs) != 2 || recs[0]["phase"] != "start" || recs[0]["outcome"] != "pending" || recs[1]["phase"] != "result" {
		t.Fatalf("audit records = %v", recs)
	}
	r := recs[1]
	if r["outcome"] != "success" || r["operator"] != "Pat Admin" || r["reason"] != "routine test" || r["destination"] != "VIEWER1" ||
		r["destination_host"] != "10.0.0.20" || fmt.Sprint(r["studies"]) != "[1.2.3]" || fmt.Sprint(r["patient_ids"]) != "[QP1]" ||
		r["instances"] != float64(3) || r["completed"] != float64(3) || r["failed"] != float64(0) {
		t.Fatalf("result record = %v", r)
	}
	// Patient-ID omission in the audit record.
	d.data.omit = true
	id = rid()
	if rec, _ := d.post(t, "/control/v1/send", sendBody(id, "confirm", true, "plan_digest", digest)); rec.Code != 200 {
		t.Fatalf("send = %d", rec.Code)
	}
	for _, r := range findAudit(auditRecs(t, d.rt.Audit), map[string]any{"request_id": id}) {
		if strings.Contains(fmt.Sprint(r), "QP1") {
			t.Errorf("audit record holds the patient ID with omission on: %v", r)
		}
	}
}

// A request the gateway refuses is refused with its code and sends
// nothing; so is a request that names something that is not a DICOM UID.
// Verifies: DCM-SRS-092
func TestSendRefusals(t *testing.T) {
	d := newDataEnv(t)
	_, digest := d.sendPlan(t)
	for code, status := range map[string]int{"unknown_destination": 404, "study_not_found": 404, "patient_mismatch": 422,
		"patient_id_required": 422, "series_not_in_study": 422, "instance_not_in_study": 422, "nothing_to_send": 422, "too_many_instances": 422} {
		d.data.planErr = &gateway.RequestError{Code: code, Msg: "refused; nothing was sent"}
		if rec, body := d.post(t, "/control/v1/send/plan", sendBody("")); rec.Code != status || body["code"] != code {
			t.Errorf("plan refused with %s = %d %v", code, rec.Code, body)
		}
		rec, body := d.post(t, "/control/v1/send", sendBody(rid(), "confirm", true, "plan_digest", digest))
		if rec.Code != status || body["code"] != code {
			t.Errorf("send refused with %s = %d %v", code, rec.Code, body)
		}
		if len(findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": auditRefused, "code": code, "destination": "VIEWER1", "study": "1.2.3"})) != 1 {
			t.Errorf("no refusal record for %s", code)
		}
	}
	d.data.planErr = errors.New("database is locked")
	if rec, body := d.post(t, "/control/v1/send", sendBody(rid(), "confirm", true, "plan_digest", digest)); rec.Code != 500 || strings.Contains(rec.Body.String(), "locked") {
		t.Errorf("index failure = %d %v", rec.Code, body)
	}
	d.data.planErr = nil
	long := make([]string, maxSendSeries+1)
	for i := range long {
		long[i] = "1.2"
	}
	for name, fields := range map[string][]any{
		"study not a UID":    {"study_instance_uid", "1.2.3; DROP"},
		"study with letters": {"study_instance_uid", "S1"},
		"study too long":     {"study_instance_uid", "1." + strings.Repeat("2", 64)},
		"series not a UID":   {"series_instance_uids", []string{"1.2.3.1", "*"}},
		"instance not a UID": {"sop_instance_uids", []string{"1.2.3.1.1", ""}},
		"too many series":    {"series_instance_uids", long},
	} {
		for _, path := range []string{"/control/v1/send/plan", "/control/v1/send"} {
			rec, body := d.post(t, path, sendBody(rid(), append([]any{"confirm", true, "plan_digest", digest}, fields...)...))
			if rec.Code != 422 || (body["code"] != "invalid_uid" && body["code"] != "too_many_instances") {
				t.Errorf("%s, %s = %d %v", name, path, rec.Code, body)
			}
		}
	}
	// A field the API does not know, such as a free host, is refused.
	for _, extra := range []string{"host", "port", "destination_host", "path"} {
		if rec, body := d.post(t, "/control/v1/send", sendBody(rid(), "confirm", true, "plan_digest", digest, extra, "10.9.9.9")); rec.Code != 400 || body["code"] != "invalid_body" {
			t.Errorf("unknown field %s = %d %v", extra, rec.Code, body)
		}
	}
	if n := d.data.sentCount(); n != 0 {
		t.Fatalf("%d refused sends went out", n)
	}
}

// The answer gives the outcome per sub-operation: which instances failed
// or got a warning. A send in which nothing arrived does not read as a
// success.
// Verifies: DCM-SRS-092
func TestSendOutcomes(t *testing.T) {
	d := newDataEnv(t)
	_, digest := d.sendPlan(t)
	for name, c := range map[string]struct {
		done, failed, warning int
		status                int
		outcome, audit        string
	}{
		"partial":       {2, 1, 0, 200, "partial", "minor-failure"},
		"with warnings": {2, 0, 1, 200, "partial", "minor-failure"},
		"only warnings": {0, 0, 3, 200, "partial", "minor-failure"},
		"all failed":    {0, 3, 0, 502, "failed", "serious-failure"},
	} {
		d.data.completed = func(int) (int, int, int) { return c.done, c.failed, c.warning }
		id := rid()
		rec, body := d.post(t, "/control/v1/send", sendBody(id, "confirm", true, "plan_digest", digest))
		if rec.Code != c.status || body["outcome"] != c.outcome || body["completed"] != float64(c.done) || body["failed"] != float64(c.failed) ||
			body["warning"] != float64(c.warning) || len(body["failed_sop_instance_uids"].([]any)) != c.failed ||
			len(body["warning_sop_instance_uids"].([]any)) != c.warning {
			t.Errorf("%s = %d %v", name, rec.Code, body)
		}
		if c.status == 502 && body["code"] != "send_failed" {
			t.Errorf("%s: code = %v", name, body["code"])
		}
		recs := findAudit(auditRecs(t, d.rt.Audit), map[string]any{"event": auditExported, "phase": "result", "request_id": id})
		if len(recs) != 1 || recs[0]["outcome"] != c.audit || recs[0]["failed"] != float64(c.failed) {
			t.Errorf("%s: audit = %v", name, recs)
		}
	}
}

// One send runs at a time on a node; a second one is refused at once.
// Verifies: DCM-SRS-096
func TestSendOneAtATime(t *testing.T) {
	d := newDataEnv(t)
	_, digest := d.sendPlan(t)
	d.data.hold, d.data.started = make(chan struct{}), make(chan struct{}, 2)
	first := make(chan int, 1)
	go func() {
		rec, _ := d.post(t, "/control/v1/send", sendBody(rid(), "confirm", true, "plan_digest", digest))
		first <- rec.Code
	}()
	select {
	case <-d.data.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first send did not start")
	}
	second := make(chan int, 1)
	go func() {
		rec, _ := d.post(t, "/control/v1/send", sendBody(rid(), "confirm", true, "plan_digest", digest))
		second <- rec.Code
	}()
	select {
	case code := <-second:
		if code != http.StatusTooManyRequests {
			t.Errorf("second send = %d, want 429", code)
		}
	case <-time.After(5 * time.Second):
		t.Error("the second send was not refused while the first runs: the limit is not enforced")
	}
	close(d.data.hold)
	if code := <-first; code != 200 {
		t.Fatalf("first send = %d", code)
	}
	if SendTimeout <= 0 || SendTimeout > time.Hour {
		t.Fatalf("SendTimeout = %v", SendTimeout)
	}
}

// The new actions follow the rules of every action: token, operator,
// reason, request ID and the expected node; the plan endpoints need the
// token and check the expected node.
// Verifies: DCM-SRS-086, DCM-SRS-097, DCM-SRS-080
func TestDataAndConfigActionsEnvelope(t *testing.T) {
	d := newDataEnv(t)
	_, digest := d.sendPlan(t)
	actions := map[string]map[string]any{
		"/control/v1/send":            {"destination": "VIEWER1", "patient_id": "QP1", "study_instance_uid": "1.2.3", "confirm": true, "plan_digest": digest},
		"/control/v1/config":          {"settings": map[string]any{"dicom.allowed_aes": []string{"MR1"}}, "confirm": true, "plan_digest": "sha256:x"},
		"/control/v1/config/rollback": {"confirm": true, "plan_digest": "sha256:x"},
	}
	plans := []string{"/control/v1/send/plan", "/control/v1/config/plan", "/control/v1/config/rollback/plan"}
	with := func(base map[string]any, id string, edit func(map[string]any)) map[string]any {
		m := act(id)
		for k, v := range base {
			m[k] = v
		}
		if edit != nil {
			edit(m)
		}
		return m
	}
	for path, fields := range actions {
		for _, tok := range []string{"", "wrong"} {
			if rec, _ := d.postAs(t, path, tok, with(fields, rid(), nil)); rec.Code != http.StatusUnauthorized {
				t.Errorf("POST %s with token %q = %d", path, tok, rec.Code)
			}
		}
		for name, c := range map[string]struct {
			edit   func(map[string]any)
			status int
			code   string
		}{
			"no operator":     {func(m map[string]any) { delete(m, "operator") }, 422, "operator_required"},
			"blank operator":  {func(m map[string]any) { m["operator"] = " \n " }, 422, "operator_required"},
			"no reason":       {func(m map[string]any) { delete(m, "reason") }, 422, "reason_required"},
			"no request id":   {func(m map[string]any) { delete(m, "request_id") }, 422, "request_id_invalid"},
			"short id":        {func(m map[string]any) { m["request_id"] = "short" }, 422, "request_id_invalid"},
			"other node":      {func(m map[string]any) { m["expect_node_id"] = "dcm-2" }, 409, "instance_mismatch"},
			"other AE title":  {func(m map[string]any) { m["expect_ae_title"] = "OTHERDCM" }, 409, "instance_mismatch"},
			"unknown field":   {func(m map[string]any) { m["force"] = true }, 400, "invalid_body"},
			"not JSON at all": {nil, 400, "invalid_body"},
		} {
			var body any = "{not json"
			if c.edit != nil {
				body = with(fields, rid(), c.edit)
			}
			rec, out := d.post(t, path, body)
			if rec.Code != c.status || out["code"] != c.code {
				t.Errorf("POST %s, %s = %d %v, want %d %s", path, name, rec.Code, out, c.status, c.code)
			}
		}
	}
	for _, path := range plans {
		for _, tok := range []string{"", "wrong"} {
			if rec, _ := d.postAs(t, path, tok, map[string]any{}); rec.Code != http.StatusUnauthorized {
				t.Errorf("POST %s with token %q = %d", path, tok, rec.Code)
			}
		}
		if rec, out := d.post(t, path, map[string]any{"expect_node_id": "dcm-2"}); rec.Code != 409 || out["code"] != "instance_mismatch" {
			t.Errorf("POST %s for another node = %d %v", path, rec.Code, out)
		}
		if rec, _ := d.do(t, "GET", path, testToken); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", path, rec.Code)
		}
	}
	if d.data.sentCount() != 0 || d.ed.writes != 0 {
		t.Fatalf("a refused request acted: %d sends, %d configuration writes", d.data.sentCount(), d.ed.writes)
	}
	// Discovery lists the three features, and each answers.
	var got []string
	for _, f := range d.get(t, "/control/v1")["features"].([]any) {
		got = append(got, f.(string))
	}
	if !strings.HasSuffix(strings.Join(got, " "), "backup studies send config-edit") {
		t.Fatalf("features = %v", got)
	}
}
