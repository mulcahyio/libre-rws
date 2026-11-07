package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"
	"github.com/prometheus/client_golang/prometheus"

	"rws/modules/dicom/internal/dcm"
	"rws/modules/dicom/internal/events"
)

func TestParseAllowRule(t *testing.T) {
	for _, bad := range []string{"", "@1.2.3.4", "WAY_TOO_LONG_AE_TITLE", "CT@not-an-ip", "CT@10.0.0.0/99"} {
		if _, err := parseAllowRule(bad); err == nil {
			t.Errorf("parseAllowRule(%q) accepted", bad)
		}
	}
	g := &Gateway{m: newMetrics(prometheus.NewRegistry())}
	if err := g.SetAllowedAEs([]string{"CT1", " MR1@192.168.1.20 ", "US@10.1.0.0/16", ""}); err != nil {
		t.Fatal(err)
	}
	addr := func(s string) net.Addr { a, _ := net.ResolveTCPAddr("tcp", s); return a }
	cases := []struct {
		ae, remote string
		want       bool
	}{
		{"CT1", "8.8.8.8:4000", true},
		{"MR1", "192.168.1.20:4000", true},
		{"MR1", "192.168.1.21:4000", false},
		{"US", "10.1.200.3:104", true},
		{"US", "10.2.0.1:104", false},
		{"ct1", "8.8.8.8:4000", false}, // AE titles are case sensitive
		{"OTHER", "127.0.0.1:1", false},
	}
	for _, c := range cases {
		if got := g.callingAEAllowed(c.ae, addr(c.remote)); got != c.want {
			t.Errorf("%s from %s: got %v want %v", c.ae, c.remote, got, c.want)
		}
	}
	if err := g.SetAllowedAEs([]string{"bad@x"}); err == nil {
		t.Fatal("invalid entry accepted")
	}
	if !g.callingAEAllowed("CT1", addr("1.1.1.1:1")) {
		t.Fatal("a failed update must keep the previous list")
	}
}

func dialAs(ctx context.Context, addr, calling string) (*ae.Association, error) {
	return ae.Dial(ctx, ae.Config{
		AETitle: calling,
		PresentationContexts: []ae.PresentationContext{
			{AbstractSyntax: "1.2.840.10008.1.1", TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}},
			{AbstractSyntax: string(uid.CTImageStorage), TransferSyntaxes: []string{dcm.ExplicitVRLittleEndian}},
		},
	}, addr, "RWS_TEST")
}

// TestAllowListEndToEnd: allowed peers associate, others get A-ASSOCIATE-RJ,
// list changes apply to new associations without a restart, and the calling
// AE is recorded as the source of stored instances.
func TestAllowListEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr, gw, pub := startGateway(t, ctx, nil)
	if err := gw.SetAllowedAEs([]string{"MODALITY@127.0.0.1"}); err != nil {
		t.Fatal(err)
	}

	if a, err := dialAs(ctx, addr, "INTRUDER"); err == nil {
		_ = a.Release(ctx)
		t.Fatal("INTRUDER was allowed to associate")
	}

	scu, err := dialAs(ctx, addr, "MODALITY")
	if err != nil {
		t.Fatalf("allowed AE rejected: %v", err)
	}
	study, series := "1.2.826.0.1.3680043.10.999.7", "1.2.826.0.1.3680043.10.999.7.1"
	res, err := scu.CStore(ctx, ae.StoreRequest{
		AffectedSOPClassUID: string(uid.CTImageStorage), AffectedSOPInstanceUID: series + ".1",
		Data: ctImage(study, series, series+".1", 1),
	})
	if err != nil || res.Status != status.Success {
		t.Fatalf("store: %v %v", err, res)
	}
	_ = scu.Release(ctx)
	pub.mu.Lock()
	ev, _ := pub.evs[len(pub.evs)-1].(events.InstanceStored)
	pub.mu.Unlock()
	if ev.SourceAE != "MODALITY" {
		t.Fatalf("event source_ae = %q, want MODALITY", ev.SourceAE)
	}

	// Narrow the list at runtime: the same peer is now refused.
	if err := gw.SetAllowedAEs([]string{"MODALITY@10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if a, err := dialAs(ctx, addr, "MODALITY"); err == nil {
		_ = a.Release(ctx)
		t.Fatal("MODALITY from 127.0.0.1 allowed after list changed")
	}
}
