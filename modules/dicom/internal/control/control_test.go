package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/blob"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/events"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

type mux struct{ *http.ServeMux }

func (m mux) HandleControl(p string, h http.Handler) { m.Handle(p, h) }

type nopPub struct{}

func (nopPub) Publish(context.Context, string, string, any) error { return nil }
func (nopPub) Check(context.Context) error                        { return nil }
func (nopPub) Close() error                                       { return nil }

var _ events.Publisher = nopPub{}

func setup(t *testing.T) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(ctx, "sqlite", "file:"+filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	gw := gateway.New(gateway.DefaultConfig(), "n", slog.New(slog.NewTextHandler(io.Discard, nil)), prometheus.NewRegistry(), blobs, idx, nopPub{})
	r := index.Record{PatientID: "P1", PatientName: "DOE^JANE", StudyUID: "S1", SeriesUID: "SE1", SOPInstanceUID: "I1",
		SOPClassUID: "1.2.840.10008.5.1.4.1.1.2", TransferSyntaxUID: "1.2.840.10008.1.2.1", BlobKey: "S1/SE1/I1.dcm"}
	if err := idx.Upsert(ctx, r); err != nil {
		t.Fatal(err)
	}
	in := r
	in.PatientID, in.SOPInstanceUID, in.BlobKey = "P2", "I2", "conflicts/S1/I2.dcm"
	chk, err := idx.CheckConflict(ctx, in)
	if err != nil || chk.Conflict == nil {
		t.Fatalf("check: %+v %v", chk, err)
	}
	id, err := idx.RecordConflict(ctx, *chk.Conflict, in, index.StateOpen, false)
	if err != nil {
		t.Fatal(err)
	}
	m := mux{http.NewServeMux()}
	(&API{Index: idx, Resolver: gw}).Register(m)
	return m, id
}

func call(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewBufferString(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, rd))
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// The control API lists and details conflicts, plans actions without
// changing anything, refuses unconfirmed or incomplete edits, and resolves.
// Verifies: DCM-SRS-077, DCM-SRS-079
func TestControlAPI(t *testing.T) {
	h, id := setup(t)

	code, body := call(t, h, "GET", "/control/v1/conflicts", nil)
	if code != 200 || len(body["conflicts"].([]any)) != 1 {
		t.Fatalf("list = %d %v", code, body)
	}
	if code, _ := call(t, h, "GET", "/control/v1/conflicts?state=bogus", nil); code != 400 {
		t.Fatalf("bad filter = %d", code)
	}
	code, body = call(t, h, "GET", "/control/v1/conflicts/"+id, nil)
	if code != 200 || body["indexed_patient"].(map[string]any)["PatientID"] != "P1" || len(body["incoming_instances"].([]any)) != 1 {
		t.Fatalf("detail = %d %v", code, body)
	}
	if code, _ := call(t, h, "GET", "/control/v1/conflicts/nope", nil); code != 404 {
		t.Fatalf("unknown = %d", code)
	}

	code, body = call(t, h, "POST", "/control/v1/conflicts/"+id+"/plan", map[string]string{"action": "correct_incoming"})
	if code != 200 || body["incoming_instances_rewritten"].(float64) != 1 {
		t.Fatalf("plan = %d %v", code, body)
	}
	if code, _ := call(t, h, "POST", "/control/v1/conflicts/"+id+"/plan", map[string]string{"action": "drop_table"}); code != 422 {
		t.Fatalf("bad action = %d", code)
	}
	if code, _ := call(t, h, "POST", "/control/v1/conflicts/"+id+"/plan", `{"action":"reject","extra":1}`); code != 400 {
		t.Fatalf("unknown field = %d", code)
	}

	req := ResolveRequest{Action: "reject", Operator: "admin", Reason: "test duplicate"}
	if code, body := call(t, h, "POST", "/control/v1/conflicts/"+id+"/resolve", req); code != 422 {
		t.Fatalf("unconfirmed = %d %v", code, body)
	}
	req.Confirm, req.Reason = true, ""
	if code, _ := call(t, h, "POST", "/control/v1/conflicts/"+id+"/resolve", req); code != 422 {
		t.Fatalf("no reason = %d", code)
	}
	req.Reason = "test duplicate"
	code, body = call(t, h, "POST", "/control/v1/conflicts/"+id+"/resolve", req)
	if code != 200 || body["state"] != "rejected" {
		t.Fatalf("resolve = %d %v", code, body)
	}
	code, body = call(t, h, "GET", "/control/v1/conflicts?state=resolved", nil)
	if code != 200 || len(body["conflicts"].([]any)) != 1 {
		t.Fatalf("resolved list = %d %v", code, body)
	}
}
