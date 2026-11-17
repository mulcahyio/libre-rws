package dcmclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// The study query carries the person's name in X-Libre-Operator and the
// keys as query fields; a send of which nothing arrived (502 send_failed)
// comes back as a result with outcome "failed" and its instance lists, any
// other refusal as an error; the ring queries carry only their own
// filters.
// Verifies: WT-SRS-025, WT-SRS-026, WT-SRS-029, WT-SRS-030
func TestDataCalls(t *testing.T) {
	var got *http.Request
	var gotBody map[string]any
	answer, status := `{}`, 200
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		gotBody = nil
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &gotBody)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	defer ts.Close()
	c, err := New(Config{URL: ts.URL, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	answer = `{"level":"study","rows":[{"PatientID":"P1"}],"count":1,"more":true,"cursor":"abc","held_instances":"hidden"}`
	r, err := c.Studies(ctx, StudyQuery{Level: "study", Keys: map[string]string{"PatientID": "P*", "StudyDate": "20260101-"}, Limit: 50, Cursor: "x1"}, "op1")
	if err != nil || len(r.Rows) != 1 || !r.More || r.Cursor != "abc" || r.Held != "hidden" {
		t.Fatalf("studies = %+v, %v", r, err)
	}
	q, _ := url.ParseQuery(got.URL.RawQuery)
	if got.URL.Path != "/control/v1/studies" || got.Header.Get(OperatorHeader) != "op1" || got.Header.Get("Authorization") != "Bearer t" ||
		q.Get("PatientID") != "P*" || q.Get("StudyDate") != "20260101-" || q.Get("level") != "study" || q.Get("limit") != "50" || q.Get("cursor") != "x1" {
		t.Fatalf("request = %s %v", got.URL, got.Header)
	}

	req := SendRequest{Destination: "V1", PatientID: "P1", StudyInstanceUID: "1.2", SeriesUIDs: []string{"1.2.1"}}
	env := Envelope{Operator: "a", Reason: "r", RequestID: NewRequestID(), Expect: Identity{NodeID: "n", AETitle: "AE"}}
	answer = `{"outcome":"partial","instances":3,"completed":2,"failed":1,"failed_sop_instance_uids":["1.2.1.3"]}`
	res, err := c.Send(ctx, req, "sha256:d", env)
	if err != nil || res.Outcome != "partial" || res.Failed != 1 || res.FailedUIDs[0] != "1.2.1.3" {
		t.Fatalf("send = %+v, %v", res, err)
	}
	if gotBody["destination"] != "V1" || gotBody["patient_id"] != "P1" || gotBody["study_instance_uid"] != "1.2" || gotBody["plan_digest"] != "sha256:d" ||
		gotBody["confirm"] != true || gotBody["operator"] != "a" || gotBody["expect_node_id"] != "n" {
		t.Fatalf("send body = %v", gotBody)
	}
	status, answer = http.StatusBadGateway, `{"outcome":"failed","code":"send_failed","error":"no instance was accepted","instances":2,"failed":2,"failed_sop_instance_uids":["a","b"]}`
	res, err = c.Send(ctx, req, "sha256:d", env)
	if err != nil || res.Outcome != "failed" || len(res.FailedUIDs) != 2 {
		t.Fatalf("failed send = %+v, %v", res, err)
	}
	status, answer = http.StatusConflict, `{"code":"plan_changed","error":"changed"}`
	if _, err := c.Send(ctx, req, "sha256:d", env); Code(err) != "plan_changed" {
		t.Fatalf("refused send = %v", err)
	}
	status, answer = http.StatusBadGateway, `{"code":"other","error":"proxy"}`
	if _, err := c.Send(ctx, req, "sha256:d", env); HTTPStatus(err) != http.StatusBadGateway {
		t.Fatalf("502 without send_failed = %v", err)
	}

	status, answer = 200, `{"records":[{"event":"Query"}],"count":1}`
	since := time.Date(2026, 11, 16, 10, 0, 0, 0, time.FixedZone("x", 3600))
	ring, err := c.AuditRecords(ctx, RingQuery{Type: "Query", Outcome: "success", Level: "debug", Since: since, Limit: 250})
	if err != nil || len(ring.Records) != 1 {
		t.Fatalf("audit = %+v, %v", ring, err)
	}
	q, _ = url.ParseQuery(got.URL.RawQuery)
	if got.URL.Path != "/control/v1/audit" || q.Get("type") != "Query" || q.Get("outcome") != "success" || q.Has("level") ||
		q.Get("since") != "2026-11-16T09:00:00Z" || q.Get("limit") != "250" {
		t.Fatalf("audit request = %s", got.URL)
	}
	answer = `{"lines":[{"msg":"x"}],"count":1}`
	ring, err = c.LogLines(ctx, RingQuery{Type: "Query", Outcome: "success", Level: "warn", Limit: 200})
	q, _ = url.ParseQuery(got.URL.RawQuery)
	if err != nil || len(ring.Lines) != 1 || got.URL.Path != "/control/v1/logs" || q.Get("level") != "warn" || q.Has("type") || q.Has("outcome") || q.Has("since") {
		t.Fatalf("logs request = %s, %+v, %v", got.URL, ring, err)
	}
}
