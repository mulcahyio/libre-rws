package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/events"
)

// Verifies: DCM-SRS-015
func TestParseAllowRule(t *testing.T) {
	for _, bad := range []string{"", "@1.2.3.4", "WAY_TOO_LONG_AE_TITLE", "CT@not-an-ip", "CT@10.0.0.0/99"} {
		if _, err := parseAllowRule(bad); err == nil {
			t.Errorf("parseAllowRule(%q) accepted", bad)
		}
	}
	g := &Gateway{m: newMetrics(prometheus.NewRegistry()), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
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
// Verifies: DCM-SRS-015, DCM-SRS-017
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

// A preview of an allow-list uses the same parser as the list in force: it
// accepts and refuses the same entries and gives the form AllowedAEs reports.
// Verifies: DCM-SRS-088, DCM-SRS-015
func TestNormalizeAllowedAEsMatchesSetAllowedAEs(t *testing.T) {
	g := &Gateway{}
	for _, list := range [][]string{
		nil, {}, {""}, {"CT1"}, {" CT1 ", "MR1@192.168.1.20", "US1@10.0.0.0/8", "", "X@::1", "Y@fd00::/8"},
		{"CT1@not-an-ip"}, {"@10.0.0.1"}, {"ABCDEFGHIJKLMNOPQ"}, {"CT1", "MR1@300.1.1.1"}, {"A@10.0.0.1/40"},
	} {
		norm, errN := NormalizeAllowedAEs(list)
		errS := g.SetAllowedAEs(list)
		if (errN == nil) != (errS == nil) {
			t.Fatalf("%q: preview error %v, apply error %v", list, errN, errS)
		}
		if errN != nil {
			continue
		}
		live, _ := g.AllowedAEs()
		if len(norm) != len(live) {
			t.Fatalf("%q: preview %v, in force %v", list, norm, live)
		}
		for i := range norm {
			if norm[i] != live[i] {
				t.Fatalf("%q: preview %v, in force %v", list, norm, live)
			}
		}
		// The normal form is stable: applying it gives itself.
		again, err := NormalizeAllowedAEs(norm)
		if err != nil || len(again) != len(norm) {
			t.Fatalf("%q: normal form %v does not round-trip: %v %v", list, norm, again, err)
		}
	}
}
