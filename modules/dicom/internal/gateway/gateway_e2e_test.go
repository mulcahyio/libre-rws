package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gonetdicom/status"
	"github.com/prometheus/client_golang/prometheus"

	"rws/modules/dicom/internal/blob"
	"rws/modules/dicom/internal/dcm"
	"rws/modules/dicom/internal/events"
	"rws/modules/dicom/internal/index"
)

type recPub struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recPub) Publish(_ context.Context, subject, id string, _ any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, subject+" "+id)
	return nil
}
func (r *recPub) Check(context.Context) error { return nil }
func (r *recPub) Close() error                { return nil }

var _ events.Publisher = (*recPub)(nil)

func ctImage(study, series, sop string, n int) *godicom.Dataset {
	ds := godicom.NewDataset()
	set := func(kw, v string) { _ = ds.SetString(godicom.MustTag(kw), v) }
	set("SOPClassUID", string(uid.CTImageStorage))
	set("SOPInstanceUID", sop)
	set("StudyInstanceUID", study)
	set("SeriesInstanceUID", series)
	set("PatientID", "P001")
	set("PatientName", "DOE^JANE")
	set("StudyDate", "20260115")
	set("AccessionNumber", "ACC123")
	set("Modality", "CT")
	set("SeriesNumber", "1")
	_ = ds.SetInt(godicom.MustTag("InstanceNumber"), n)
	_ = ds.SetInt(godicom.MustTag("Rows"), 4)
	_ = ds.SetInt(godicom.MustTag("Columns"), 4)
	_ = ds.SetInt(godicom.MustTag("BitsAllocated"), 16)
	_ = ds.SetInt(godicom.MustTag("BitsStored"), 12)
	_ = ds.SetInt(godicom.MustTag("HighBit"), 11)
	_ = ds.SetInt(godicom.MustTag("PixelRepresentation"), 0)
	_ = ds.SetInt(godicom.MustTag("SamplesPerPixel"), 1)
	set("PhotometricInterpretation", "MONOCHROME2")
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROW, make([]byte, 32)))
	return ds
}

func startGateway(t *testing.T, ctx context.Context, remotes map[string]RemoteAE) (string, *Gateway, *recPub) {
	t.Helper()
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
	pub := &recPub{}
	cfg := DefaultConfig()
	cfg.AETitle = "RWS_TEST"
	cfg.RemoteAEs = remotes
	log := slog.New(slog.NewTextHandler(testWriter(), nil))
	gw := New(cfg, "test-node", log, prometheus.NewRegistry(), blobs, idx, pub)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gw.Serve(ctx, ln) }()
	return ln.Addr().String(), gw, pub
}

func TestEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A destination SCP for C-MOVE.
	var mu sync.Mutex
	received := map[string]string{} // sop -> transfer syntax
	destLn, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		_ = ae.Serve(ctx, destLn, ae.ServerConfig{
			AETitle: "DEST", AcceptedAbstractSyntaxes: ae.AllStorageSOPClasses,
			OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
				mu.Lock()
				received[req.AffectedSOPInstanceUID] = req.TransferSyntax
				mu.Unlock()
				return status.Success
			},
		})
	}()
	dh, dp, _ := net.SplitHostPort(destLn.Addr().String())
	var port int
	for _, c := range dp {
		port = port*10 + int(c-'0')
	}

	addr, _, pub := startGateway(t, ctx, map[string]RemoteAE{"DEST": {Host: dh, Port: port}})

	study, series := "1.2.826.0.1.3680043.10.999.1", "1.2.826.0.1.3680043.10.999.1.1"
	sops := []string{series + ".1", series + ".2", series + ".3"}

	// ---- C-ECHO + C-STORE (Explicit VR LE)
	scu, err := ae.Dial(ctx, ae.Config{
		AETitle: "MODALITY",
		PresentationContexts: []ae.PresentationContext{
			{AbstractSyntax: "1.2.840.10008.1.1", TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}},
			{AbstractSyntax: string(uid.CTImageStorage), TransferSyntaxes: []string{dcm.ExplicitVRLittleEndian}},
		},
	}, addr, "RWS_TEST")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := scu.CEcho(ctx); err != nil {
		t.Fatalf("echo: %v", err)
	}
	for i, sop := range sops {
		res, err := scu.CStore(ctx, ae.StoreRequest{
			AffectedSOPClassUID: string(uid.CTImageStorage), AffectedSOPInstanceUID: sop,
			Data: ctImage(study, series, sop, i+1),
		})
		if err != nil || res.Status != status.Success {
			t.Fatalf("store %s: %v status=%v", sop, err, res)
		}
	}
	_ = scu.Release(ctx)
	if len(pub.msgs) != 3 {
		t.Fatalf("expected 3 events, got %d", len(pub.msgs))
	}

	// ---- C-FIND (study level, wildcard on name, date range)
	q := godicom.NewDataset()
	_ = q.SetString(godicom.MustTag("QueryRetrieveLevel"), "STUDY")
	_ = q.SetString(godicom.MustTag("PatientName"), "DOE*")
	_ = q.SetString(godicom.MustTag("StudyDate"), "20260101-20260131")
	_ = q.SetString(godicom.MustTag("StudyInstanceUID"), "")
	_ = q.SetString(godicom.MustTag("ModalitiesInStudy"), "")
	_ = q.SetString(godicom.MustTag("NumberOfStudyRelatedInstances"), "")
	find, err := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
		AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelFind, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian},
	}}}, addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := find.CFind(ctx, ae.FindRequest{QueryModel: ae.StudyRootQueryRetrieveInformationModelFind, IdentifierData: q})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	_ = find.Release(ctx)
	var pending []*godicom.Dataset
	for _, m := range matches {
		if m.Status == status.Pending && m.Identifier != nil {
			pending = append(pending, m.Identifier)
		}
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 study match, got %d (%+v)", len(pending), matches)
	}
	if got := dcm.String(pending[0], "StudyInstanceUID"); got != study {
		t.Fatalf("study uid = %q", got)
	}
	if got := dcm.String(pending[0], "ModalitiesInStudy"); got != "CT" {
		t.Fatalf("modalities = %q", got)
	}
	if got := dcm.String(pending[0], "NumberOfStudyRelatedInstances"); got != "3" {
		t.Fatalf("instances = %q", got)
	}

	// ---- C-MOVE study to DEST
	mq := godicom.NewDataset()
	_ = mq.SetString(godicom.MustTag("QueryRetrieveLevel"), "STUDY")
	_ = mq.SetString(godicom.MustTag("StudyInstanceUID"), study)
	mv, err := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
		AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelMove, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian},
	}}}, addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	mres, err := mv.CMove(ctx, ae.MoveRequest{QueryModel: ae.StudyRootQueryRetrieveInformationModelMove, MoveDestination: "DEST", IdentifierData: mq})
	_ = mv.Release(ctx)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	last := mres[len(mres)-1]
	if last.Status != status.Success || last.SubOperations.Completed != 3 {
		t.Fatalf("move final = %+v", last)
	}
	mu.Lock()
	if len(received) != 3 {
		t.Fatalf("destination received %d", len(received))
	}
	for sop, ts := range received {
		if !dcm.IsUncompressed(ts) { // destination picks between the native syntaxes we offer
			t.Fatalf("%s arrived as %s", sop, ts)
		}
	}
	mu.Unlock()

	// ---- C-MOVE to unknown destination
	mv2, _ := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
		AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelMove, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian},
	}}}, addr, "RWS_TEST")
	mres2, _ := mv2.CMove(ctx, ae.MoveRequest{QueryModel: ae.StudyRootQueryRetrieveInformationModelMove, MoveDestination: "NOPE", IdentifierData: mq})
	_ = mv2.Release(ctx)
	if len(mres2) == 0 || mres2[len(mres2)-1].Status != status.MoveDestinationUnknown {
		t.Fatalf("expected A801, got %+v", mres2)
	}

	// ---- C-GET series
	var gotMu sync.Mutex
	got := 0
	gq := godicom.NewDataset()
	_ = gq.SetString(godicom.MustTag("QueryRetrieveLevel"), "SERIES")
	_ = gq.SetString(godicom.MustTag("StudyInstanceUID"), study)
	_ = gq.SetString(godicom.MustTag("SeriesInstanceUID"), series)
	gc, err := ae.Dial(ctx, ae.Config{
		AETitle: "VIEWER",
		PresentationContexts: []ae.PresentationContext{
			{AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelGet, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}},
			{AbstractSyntax: string(uid.CTImageStorage), TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}},
		},
		RoleSelections: []pdu.RoleSelection{ae.BuildRole(string(uid.CTImageStorage), false, true)},
	}, addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	gres, err := gc.CGet(ctx, ae.GetRequest{
		QueryModel: ae.StudyRootQueryRetrieveInformationModelGet, IdentifierData: gq,
		OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
			gotMu.Lock()
			got++
			gotMu.Unlock()
			if dcm.String(req.Data, "PatientID") != "P001" {
				return status.ProcessingFailure
			}
			return status.Success
		},
	})
	_ = gc.Release(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != 3 || gres[len(gres)-1].Status != status.Success {
		t.Fatalf("get received %d, final %+v", got, gres[len(gres)-1])
	}
}

func testWriter() io.Writer {
	if testing.Verbose() {
		return os.Stderr
	}
	return io.Discard
}
