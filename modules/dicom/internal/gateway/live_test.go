package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/platform/runtime"
)

// The gateway reports the destinations and the allow-list that are in force
// now, also after a live change, and reports the fail-closed state of an
// invalid list as "enforced, no rules".
// Verifies: DCM-SRS-083
func TestLiveSettingsReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, gw, _ := startGateway(t, ctx, map[string]RemoteAE{"VIEWER": {Host: "10.0.0.20", Port: 104}})

	if rules, enforced := gw.AllowedAEs(); len(rules) != 0 || enforced {
		t.Fatalf("empty list reported as %v enforced=%v", rules, enforced)
	}
	want := map[string]RemoteAE{"VIEWER": {Host: "10.0.0.20", Port: 104, AETitle: "VIEWER"}}
	if got := gw.RemoteAEs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("remote AEs = %v", got)
	}
	// The returned map is a copy: changing it changes nothing in the gateway.
	got := gw.RemoteAEs()
	got["EVIL"] = RemoteAE{Host: "203.0.113.9", Port: 104}
	delete(got, "VIEWER")
	if _, ok := gw.remoteAE("EVIL"); ok {
		t.Fatal("caller changed the gateway's destination table through RemoteAEs()")
	}
	if _, ok := gw.remoteAE("VIEWER"); !ok {
		t.Fatal("caller removed a destination through RemoteAEs()")
	}

	if err := gw.SetAllowedAEs([]string{"CT1", " MR1@192.168.1.20 ", "US@10.1.2.3/16", "X@2001:db8::1"}); err != nil {
		t.Fatal(err)
	}
	rules, enforced := gw.AllowedAEs()
	if !enforced || !reflect.DeepEqual(rules, []string{"CT1", "MR1@192.168.1.20", "US@10.1.0.0/16", "X@2001:db8::1"}) {
		t.Fatalf("rules = %v enforced=%v", rules, enforced)
	}
	// What is reported parses back to the same rules.
	if err := gw.SetAllowedAEs(rules); err != nil {
		t.Fatal(err)
	}
	if again, _ := gw.AllowedAEs(); !reflect.DeepEqual(again, rules) {
		t.Fatalf("round trip changed the rules: %v", again)
	}
	gw.SetRemoteAEs(map[string]RemoteAE{"PACS_B": {Host: "pacs-b", Port: 11112, AETitle: "PACSB"}})
	if got := gw.RemoteAEs(); len(got) != 1 || got["PACS_B"].AETitle != "PACSB" {
		t.Fatalf("after reload: %v", got)
	}

	// Invalid list at start: fail closed, reported as enforced without rules.
	testConfig = func(c *Config) { c.AllowedAEs = []string{"CT1@not-an-address"} }
	t.Cleanup(func() { testConfig = nil })
	_, bad, _ := startGateway(t, ctx, nil)
	if rules, enforced := bad.AllowedAEs(); len(rules) != 0 || !enforced {
		t.Fatalf("invalid list reported as %v enforced=%v, want no rules and enforced", rules, enforced)
	}
}

func storeOne(t *testing.T, ctx context.Context, addr, study string) {
	t.Helper()
	scu, err := dialAs(ctx, addr, "MODALITY")
	if err != nil {
		t.Fatal(err)
	}
	series := study + ".1"
	res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage),
		AffectedSOPInstanceUID: series + ".1", Data: ctImage(study, series, series+".1", 1)})
	if err != nil || res.Status != status.Success {
		t.Fatalf("store: %v %v", err, res)
	}
	_ = scu.Release(ctx)
}

// The in-memory audit ring holds the same audit records as the log: with
// patient-ID omission on, no Patient ID reaches the ring; with the audit log
// off, the gateway puts nothing in it; and patient names never appear.
// Verifies: DCM-SRS-084, DCM-SRS-032
func TestAuditRingHonoursPatientIDOmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Cleanup(func() { testLogger, testConfig = nil, nil })
	newRT := func() *runtime.Runtime {
		cfg := runtime.DefaultAdmin()
		cfg.LogLevel, cfg.NodeID = "info", "ring-test"
		rt := runtime.New("LibreDCM", "test", cfg)
		testLogger = rt.Logger
		return rt
	}
	ring := func(rt *runtime.Runtime) string {
		var b strings.Builder
		entries, _ := rt.Audit.Select(runtime.AuditRingSize, nil)
		for _, e := range entries {
			if !json.Valid(e.Line) {
				t.Fatalf("ring line is not JSON: %s", e.Line)
			}
			b.Write(e.Line)
			b.WriteByte('\n')
		}
		return b.String()
	}

	// Default: the record names the patient ID, as the audit log does.
	rt := newRT()
	testConfig = nil
	addr, _, _ := startGateway(t, ctx, nil)
	storeOne(t, ctx, addr, "1.2.826.0.1.3680043.10.999.71")
	got := ring(rt)
	for _, want := range []string{`"event":"DICOMInstancesTransferred.received"`, `"patient_id":"P001"`, `"audit":true`} {
		if !strings.Contains(got, want) {
			t.Fatalf("audit ring missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "DOE") {
		t.Fatalf("audit ring holds the patient name:\n%s", got)
	}
	if logs, _ := rt.Logs.Select(runtime.LogRingSize, nil); len(logs) == 0 {
		t.Fatal("log ring is empty")
	} else {
		for _, e := range logs {
			if strings.Contains(string(e.Line), `"audit":true`) {
				t.Fatalf("audit record in the log ring: %s", e.Line)
			}
		}
	}

	// Omission on: no Patient ID in the ring.
	rt = newRT()
	testConfig = func(c *Config) { c.AuditOmitPatientID = true }
	addr, _, _ = startGateway(t, ctx, nil)
	storeOne(t, ctx, addr, "1.2.826.0.1.3680043.10.999.72")
	got = ring(rt)
	if !strings.Contains(got, `"event":"DICOMInstancesTransferred.received"`) {
		t.Fatalf("no store record in the ring:\n%s", got)
	}
	if strings.Contains(got, "P001") || strings.Contains(got, "DOE") {
		t.Fatalf("audit ring holds patient data although omission is on:\n%s", got)
	}

	// Audit log off: nothing from the gateway in the ring.
	rt = newRT()
	testConfig = func(c *Config) { c.AuditLogOff = true }
	addr, _, _ = startGateway(t, ctx, nil)
	storeOne(t, ctx, addr, "1.2.826.0.1.3680043.10.999.73")
	if n := rt.Audit.Len(); n != 0 {
		t.Fatalf("audit ring holds %d records although audit_log_off is set:\n%s", n, ring(rt))
	}
}
