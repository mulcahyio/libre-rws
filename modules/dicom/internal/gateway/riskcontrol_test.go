package gateway

// Effectiveness tests for the HAZ-001 and HAZ-007 risk controls, added with
// the risk file review (2026-11-10): a failed write never produces a
// success status, and a wrong called AE title is refused.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/blob"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

// failingBlobs fails Put; everything else is delegated.
type failingBlobs struct{ blob.Store }

func (failingBlobs) Put(context.Context, string, io.Reader) (int64, error) {
	return 0, errors.New("disk full")
}

// failingIndex fails every write; reads are delegated.
type failingIndex struct{ index.Index }

func (failingIndex) Upsert(context.Context, index.Record) error {
	return errors.New("database is locked")
}
func (failingIndex) UpsertWithEvent(context.Context, index.Record, *index.OutboxEvent) error {
	return errors.New("database is locked")
}

// TestStoreFailureNeverAcknowledged: when the storage write or the index
// commit fails, the sender receives a failure status (so the modality keeps
// its copy and retries), nothing becomes queryable, and no event is sent.
// Verifies: DCM-SRS-002
func TestStoreFailureNeverAcknowledged(t *testing.T) {
	for _, tc := range []struct {
		name       string
		breakBlob  bool
		breakIndex bool
		outbox     bool
	}{
		{"storage write fails", true, false, false},
		{"index commit fails (direct events)", false, true, false},
		{"index commit fails (outbox)", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			dir := t.TempDir()
			fs, err := blob.NewFS(filepath.Join(dir, "blobs"))
			if err != nil {
				t.Fatal(err)
			}
			realIdx, err := index.Open(ctx, "sqlite", "file:"+filepath.Join(dir, "index.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer realIdx.Close()
			var blobs blob.Store = fs
			var idx index.Index = realIdx
			if tc.breakBlob {
				blobs = failingBlobs{fs}
			}
			if tc.breakIndex {
				idx = failingIndex{realIdx}
			}
			pub := &recPub{}
			cfg := DefaultConfig()
			cfg.AETitle = "RWS_TEST"
			gw := New(cfg, "n", slog.New(slog.NewTextHandler(testWriter(), nil)), prometheus.NewRegistry(), blobs, idx, pub)
			if tc.outbox {
				gw.UseOutbox(nudgeNop{})
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			go func() { _ = gw.Serve(ctx, ln) }()

			scu, err := ae.Dial(ctx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts()}, ln.Addr().String(), "RWS_TEST")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = scu.Release(ctx) }()
			study, series := "1.2.826.0.1.3680043.10.999.21", "1.2.826.0.1.3680043.10.999.21.1"
			sop := series + ".1"
			res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage),
				AffectedSOPInstanceUID: sop, Data: ctImage(study, series, sop, 1)})
			if err != nil {
				t.Fatalf("C-STORE transport error: %v", err)
			}
			if res.Status == status.Success || res.Status>>12 == 0xB {
				t.Fatalf("status %#04x: a failed write was acknowledged", res.Status)
			}
			rows, err := realIdx.Find(ctx, index.Image, nil, 10)
			if err != nil || len(rows) != 0 {
				t.Fatalf("index has %d rows after a failed store (err %v)", len(rows), err)
			}
			pub.mu.Lock()
			n := len(pub.msgs)
			pub.mu.Unlock()
			if n != 0 {
				t.Fatalf("%d events published for a failed store", n)
			}
			pending, _ := realIdx.PendingEvents(ctx)
			if pending != 0 {
				t.Fatalf("%d outbox events left for a failed store", pending)
			}
		})
	}
}

type nudgeNop struct{}

func (nudgeNop) Nudge() {}

// TestWrongCalledAERejected: an association addressed to another AE title is
// refused before any DIMSE service runs, unless allow_any_called_ae_title is
// set on purpose.
// Verifies: DCM-SRS-014
func TestWrongCalledAERejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	n := startNode(t, ctx, nil, nil)
	for _, called := range []string{"OTHER_PACS", "rws_test", ""} {
		dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
		a, err := ae.Dial(dctx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts()}, n.addr, called)
		if err == nil {
			_ = a.Release(dctx)
			dcancel()
			t.Fatalf("association with called AE %q accepted", called)
		}
		dcancel()
	}
	a, err := ae.Dial(ctx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts()}, n.addr, "RWS_TEST")
	if err != nil {
		t.Fatalf("correct called AE rejected: %v", err)
	}
	_ = a.Release(ctx)

	// Opt-in override accepts any called AE title.
	m := startNode(t, ctx, nil, func(c *Config) { c.AllowAnyCalledAETitle = true })
	b, err := ae.Dial(ctx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts()}, m.addr, "OTHER_PACS")
	if err != nil {
		t.Fatal(fmt.Errorf("allow_any_called_ae_title set, still rejected: %w", err))
	}
	_ = b.Release(ctx)
}
