package dcmclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	if _, err := c.Plan(ctx, "c1", "x", Identity{}); !Refused(err) {
		t.Fatalf("422 = %v", err)
	}
	r, err := c.Resolve(ctx, "c1", "reject", "admin1", "dup", Identity{})
	if err != nil || r.State != "rejected" || got["confirm"] != true || got["operator"] != "admin1" || got["reason"] != "dup" {
		t.Fatalf("resolve = %+v %v body=%v", r, err, got)
	}
	// Without an expected identity the body has no such fields: a LibreDCM
	// that doesn't know them refuses unknown fields.
	if _, has := got["expect_node_id"]; has || len(got) != 4 {
		t.Fatalf("body without identity = %v", got)
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

// The client reads discovery and status, sends the expected node ID and AE
// title with plan and resolve, recognises LibreDCM's "not the node you
// expect" refusal, and bounds status reads with a short timeout.
// Verifies: WT-SRS-015, WT-SRS-017, WT-SRS-018
func TestClientStatusDiscoveryAndExpectedIdentity(t *testing.T) {
	var bodies []map[string]any
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control/v1":
			_, _ = w.Write([]byte(`{"api":"v1","features":["conflicts","status"],"instance":{"node_id":"dcm-1","ae_title":"LIBREDCM","version":"0.3.0","config_digest":"sha256:9f"}}`))
		case "/control/v1/status":
			_, _ = w.Write([]byte(`{"instance":{"node_id":"dcm-1","ae_title":"LIBREDCM","version":"0.3.0","config_digest":"sha256:9f"},
				"time":"2026-11-14T15:04:05Z","uptime_seconds":86400,"backends":{"storage":"fs","index":"sqlite","events":"nats"},
				"dicom":{"listen":":11112","tls":true,"allow_list_entries":3,"remote_aes":2},"admin":{"tls":true},"ready":false,
				"checks":[{"name":"storage","ok":true},{"name":"events","ok":false,"error":"nats: no servers available"}],
				"license":{"enforced":true,"state":"valid","licensed":true,"ack_required":false,"message":"licensed until 2027-11-01","days_left":352},
				"counters":{"associations_active":1,"associations_total":120,"associations_rejected_total":4,
				"cstore":{"success":2040,"failure":2,"conflict-open":3},"cfind_total":310,"retrieve_subops":{"completed":800,"failed":0},
				"outbox_pending":7,"event_publish_failures_total":1,"conflicts_open":1},"counters_incomplete":true}`))
		case "/slow/control/v1/status":
			<-block
		case "/control/v1/conflicts/c1/plan", "/control/v1/conflicts/c1/resolve":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			bodies = append(bodies, b)
			if b["expect_node_id"] != "dcm-1" {
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`{"code":"instance_mismatch","error":"this node is not the one the request names; nothing was changed"}`))
				return
			}
			_, _ = w.Write([]byte(`{"state":"rejected","summary":"Reject"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	defer close(block)
	ctx := context.Background()
	c, err := New(Config{URL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Discovery(ctx)
	if err != nil || d.API != "v1" || len(d.Features) != 2 || d.Instance.Identity() != (Identity{NodeID: "dcm-1", AETitle: "LIBREDCM"}) {
		t.Fatalf("discovery = %+v, %v", d, err)
	}
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Instance.Version != "0.3.0" || st.Instance.ConfigDigest != "sha256:9f" || st.UptimeSeconds != 86400 || st.Ready ||
		st.Backends.Events != "nats" || !st.DICOM.TLS || st.DICOM.AllowListEntries != 3 || st.DICOM.RemoteAEs != 2 || !st.Admin.TLS ||
		len(st.Checks) != 2 || st.Checks[1].OK || st.Checks[1].Error != "nats: no servers available" ||
		!st.License.Enforced || st.License.State != "valid" || st.License.DaysLeft != 352 ||
		st.Counters.AssociationsActive != 1 || st.Counters.AssociationsTotal != 120 || st.Counters.AssociationsRejected != 4 ||
		st.Counters.CStore["success"] != 2040 || st.Counters.CStore["conflict-open"] != 3 || st.Counters.CFindTotal != 310 ||
		st.Counters.RetrieveSubOps["completed"] != 800 || st.Counters.OutboxPending != 7 || st.Counters.PublishFailures != 1 ||
		st.Counters.ConflictsOpen == nil || *st.Counters.ConflictsOpen != 1 || !st.CountersIncomplete || st.Time.Year() != 2026 {
		t.Fatalf("status = %+v", st)
	}
	// The expected identity goes into the body of plan and resolve.
	me := Identity{NodeID: "dcm-1", AETitle: "LIBREDCM"}
	if _, err := c.Plan(ctx, "c1", "reject", me); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resolve(ctx, "c1", "reject", "admin1", "dup", me); err != nil {
		t.Fatal(err)
	}
	for _, b := range bodies {
		if b["expect_node_id"] != "dcm-1" || b["expect_ae_title"] != "LIBREDCM" {
			t.Fatalf("body = %v", b)
		}
	}
	// Another node answers: the refusal is recognised.
	_, err = c.Resolve(ctx, "c1", "reject", "admin1", "dup", Identity{NodeID: "dcm-2", AETitle: "LIBREDCM"})
	if !InstanceMismatch(err) {
		t.Fatalf("mismatch = %v", err)
	}
	if InstanceMismatch(nil) || InstanceMismatch(&APIError{Status: 409, Code: "plan_changed"}) || InstanceMismatch(&APIError{Status: 404}) {
		t.Fatal("other errors read as instance mismatch")
	}
	// A discovery 404 (older LibreDCM) is told apart from other failures.
	old, _ := New(Config{URL: srv.URL + "/old", Token: "tok"})
	if _, err := old.Discovery(ctx); !NotFound(err) {
		t.Fatalf("old discovery = %v", err)
	}
	// A node that does not answer costs the read timeout, not the long
	// action timeout.
	slow, _ := New(Config{URL: srv.URL + "/slow", Token: "tok", ReadTimeout: 50 * time.Millisecond})
	start := time.Now()
	if _, err := slow.Status(ctx); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("slow status: err=%v after %v", err, time.Since(start))
	}
}

// Plain HTTP is flagged when the address is not a loopback address.
// Verifies: WT-SRS-036
func TestPlainHTTPRemote(t *testing.T) {
	for url, want := range map[string]bool{
		"http://127.0.0.1:8081":       false,
		"http://127.8.9.10:8081":      false,
		"http://localhost:8081":       false,
		"http://LOCALHOST:8081":       false,
		"http://[::1]:8081":           false,
		"https://dcm-1.example:8081":  false,
		"https://10.0.0.5:8081":       false,
		"http://10.0.0.5:8081":        true,
		"http://dcm-1.example:8081":   true,
		"http://libredcm:8081":        true,
		"http://[2001:db8::1]:8081":   true,
		"http://0.0.0.0:8081":         true,
		"http://localhost.evil.test/": true,
	} {
		c, err := New(Config{URL: url, Token: "t"})
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		if got := c.PlainHTTPRemote(); got != want {
			t.Errorf("%s: plain HTTP remote = %v, want %v", url, got, want)
		}
	}
}
