package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	godicom "github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
)

// When the license gate says no, new studies are refused with "out of
// resources" (the modality keeps them and retries) while existing studies
// stay searchable; when it says yes again, receiving resumes.
// Verifies: DCM-SRS-052
func TestReceiveGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr, gw, pub := startGateway(t, ctx, nil)
	var open atomic.Bool
	open.Store(true)
	gw.UseReceiveGate(func() (bool, string) {
		if open.Load() {
			return true, ""
		}
		return false, "license expired"
	})
	study, series := "1.2.826.0.1.3680043.10.999.7", "1.2.826.0.1.3680043.10.999.7.1"
	store := func(sop string) uint16 {
		scu, err := ae.Dial(ctx, ae.Config{AETitle: "MODALITY", PresentationContexts: []ae.PresentationContext{
			{AbstractSyntax: "1.2.840.10008.1.1", TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}},
			{AbstractSyntax: string(uid.CTImageStorage), TransferSyntaxes: []string{dcm.ExplicitVRLittleEndian}},
		}}, addr, "RWS_TEST")
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer scu.Release(ctx)
		if err := scu.CEcho(ctx); err != nil {
			t.Fatalf("echo must keep working: %v", err)
		}
		res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage), AffectedSOPInstanceUID: sop, Data: ctImage(study, series, sop, 1)})
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		return res.Status
	}
	if s := store(series + ".1"); s != status.Success {
		t.Fatalf("licensed store: %#x", s)
	}
	open.Store(false)
	if s := store(series + ".2"); s != status.OutOfResources {
		t.Fatalf("store after expiry: %#x, want out of resources", s)
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("refused instance produced an event: %d events", len(pub.msgs))
	}
	// Existing study is still found.
	q := godicom.NewDataset()
	_ = q.SetString(godicom.MustTag("QueryRetrieveLevel"), "IMAGE")
	_ = q.SetString(godicom.MustTag("StudyInstanceUID"), study)
	_ = q.SetString(godicom.MustTag("SeriesInstanceUID"), series)
	_ = q.SetString(godicom.MustTag("SOPInstanceUID"), "")
	find, err := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
		AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelFind, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian},
	}}}, addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := find.CFind(ctx, ae.FindRequest{QueryModel: ae.StudyRootQueryRetrieveInformationModelFind, IdentifierData: q})
	_ = find.Release(ctx)
	n := 0
	for _, m := range matches {
		if m.Status == status.Pending && m.Identifier != nil {
			n++
		}
	}
	if err != nil || n != 1 {
		t.Fatalf("find after expiry: %v, %d matches (want 1)", err, n)
	}
	open.Store(true)
	if s := store(series + ".2"); s != status.Success {
		t.Fatalf("store after renewal: %#x", s)
	}
}
