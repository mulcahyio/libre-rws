package dcmclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The client sends the bearer token, decodes the contract, maps LibreDCM's
// errors, always sends confirm=true with the operator and reason, and refuses
// to start without a token or with an invalid URL.
// Verifies: WT-SRS-006
func TestClientContract(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/control/v1/conflicts":
			if r.URL.Query().Get("state") != "open" {
				t.Errorf("state = %q", r.URL.Query().Get("state"))
			}
			_, _ = w.Write([]byte(`{"conflicts":[{"id":"c1","kind":"patient_id","state":"open","incoming_patient_id":"P2","instances":2}]}`))
		case "/control/v1/conflicts/c1":
			_, _ = w.Write([]byte(`{"conflict":{"id":"c1","state":"open"},"indexed_patient":{"PatientID":"P1"},"indexed_instances":3,"incoming_instances":[{"sop_instance_uid":"1.2"}]}`))
		case "/control/v1/conflicts/c1/resolve":
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = w.Write([]byte(`{"state":"rejected","files_rewritten":0,"summary":"Reject"}`))
		case "/control/v1/conflicts/c1/plan":
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":"invalid request: unknown action"}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"conflict not found"}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c, err := New(Config{URL: srv.URL + "/", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.Conflicts(ctx, "open")
	if err != nil || len(list) != 1 || list[0].IncomingPatientID != "P2" || !list[0].Active() {
		t.Fatalf("list = %+v, %v", list, err)
	}
	d, err := c.Conflict(ctx, "c1")
	if err != nil || d.IndexedPatient.PatientID != "P1" || d.IndexedInstances != 3 || len(d.IncomingInstances) != 1 {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	if _, err := c.Conflict(ctx, "nope"); !NotFound(err) {
		t.Fatalf("404 = %v", err)
	}
	if _, err := c.Plan(ctx, "c1", "x"); !Refused(err) {
		t.Fatalf("422 = %v", err)
	}
	r, err := c.Resolve(ctx, "c1", "reject", "admin1", "dup")
	if err != nil || r.State != "rejected" || got["confirm"] != true || got["operator"] != "admin1" || got["reason"] != "dup" {
		t.Fatalf("resolve = %+v %v body=%v", r, err, got)
	}
	bad, _ := New(Config{URL: srv.URL, Token: "wrong"})
	if _, err := bad.Conflicts(ctx, "open"); err == nil {
		t.Fatal("wrong token accepted")
	}
	if _, err := New(Config{URL: srv.URL}); err == nil {
		t.Fatal("client without token created")
	}
	if _, err := New(Config{URL: "ftp://x", Token: "t"}); err == nil {
		t.Fatal("invalid URL accepted")
	}
	f := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(f, []byte("tok\n"), 0o600)
	if c2, err := New(Config{URL: srv.URL, TokenFile: f}); err != nil {
		t.Fatal(err)
	} else if _, err := c2.Conflicts(ctx, "open"); err != nil {
		t.Fatalf("token file: %v", err)
	}
	if c.URL() != srv.URL {
		t.Fatalf("URL = %s", c.URL())
	}
}
