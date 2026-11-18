package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/buildinfo"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
)

// wantImplementation spells out what this build must send and write, so a
// change to the UID layout, the derivation or the wiring fails here rather
// than only in the dcm package. Test builds carry buildinfo.Version "dev".
func wantImplementation(t *testing.T) (classUID, versionName string) {
	t.Helper()
	if buildinfo.Version != "dev" {
		t.Skipf("expected values are spelled out for the dev build, not %q", buildinfo.Version)
	}
	return "1.3.6.1.4.1.67686.1.1.1.0.0", "LIBREDCM_0_0"
}

// metaElements reads the File Meta group of a Part 10 file with its own
// minimal Explicit VR Little Endian parser (independent of dcm.Part10 and of
// godicom), returning the raw values by element number.
func metaElements(t *testing.T, file []byte) map[uint16][]byte {
	t.Helper()
	if len(file) < 132 || string(file[128:132]) != "DICM" {
		t.Fatalf("not a Part 10 file (%d bytes)", len(file))
	}
	out := map[uint16][]byte{}
	for p := 132; p+8 <= len(file); {
		group := binary.LittleEndian.Uint16(file[p:])
		if group != 0x0002 {
			break
		}
		elem := binary.LittleEndian.Uint16(file[p+2:])
		vr := string(file[p+4 : p+6])
		var n, hdr int
		switch vr {
		case "OB", "OW", "OF", "SQ", "UT", "UN":
			n, hdr = int(binary.LittleEndian.Uint32(file[p+8:])), 12
		default:
			n, hdr = int(binary.LittleEndian.Uint16(file[p+6:])), 8
		}
		if p+hdr+n > len(file) {
			t.Fatalf("meta element (0002,%04X) overruns the file", elem)
		}
		out[elem] = file[p+hdr : p+hdr+n]
		p += hdr + n
	}
	return out
}

// TestImplementationIdentification reads the A-ASSOCIATE-AC LibreDCM sends,
// the A-ASSOCIATE-RQ it sends as a C-MOVE requestor, and a Part 10 file it
// stores, and checks the Implementation Class UID and Version Name in all
// three (ANOM-007: they used to be gonetdicom's).
// Verifies: DCM-SRS-098
func TestImplementationIdentification(t *testing.T) {
	wantUID, wantName := wantImplementation(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A destination that records the A-ASSOCIATE-RQ and rejects it.
	destLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = destLn.Close() }()
	gotRQ := make(chan *pdu.AAssociateRQ, 1)
	go func() {
		c, err := destLn.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		p, err := pdu.Read(c)
		if rq, ok := p.(*pdu.AAssociateRQ); err == nil && ok {
			gotRQ <- rq
		}
		_ = pdu.Write(c, &pdu.AAssociateRJ{Result: 1, Source: 1, ReasonDiagnostic: 1})
	}()
	_, dp, _ := net.SplitHostPort(destLn.Addr().String())
	port, _ := strconv.Atoi(dp)
	addr, gw, _ := startGateway(t, ctx, map[string]RemoteAE{"DEST": {Host: "127.0.0.1", Port: port}})

	// 1. A-ASSOCIATE-AC, read from the wire.
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	err = pdu.Write(c, &pdu.AAssociateRQ{
		CalledAETitle: "RWS_TEST", CallingAETitle: "PROBE", ApplicationContextName: pdu.ApplicationContextName,
		PresentationContexts: []pdu.PresentationContextRQ{{ID: 1, AbstractSyntax: "1.2.840.10008.1.1", TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}}},
		UserInformation: pdu.UserInformation{MaxLength: 16384,
			ImplementationClassUID: "1.3.6.1.4.1.67686.9.1.900", ImplementationVersionName: "PROBE_TEST"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := pdu.Read(c)
	if err != nil {
		t.Fatal(err)
	}
	acPDU, ok := p.(*pdu.AAssociateAC)
	if !ok {
		t.Fatalf("got %T, want A-ASSOCIATE-AC", p)
	}
	if ui := acPDU.UserInformation; ui.ImplementationClassUID != wantUID || ui.ImplementationVersionName != wantName {
		t.Errorf("A-ASSOCIATE-AC carries %q / %q, want %q / %q", ui.ImplementationClassUID, ui.ImplementationVersionName, wantUID, wantName)
	}
	_ = pdu.Write(c, &pdu.AReleaseRQ{})
	_, _ = pdu.Read(c)
	_ = c.Close()

	// 2. A stored Part 10 file.
	study := "1.3.6.1.4.1.67686.9.1.98"
	series, sop := study+".1", study+".1.1"
	storeOne(t, ctx, addr, study)
	rc, err := gw.blobs.Get(ctx, blobKey(study, series, sop))
	if err != nil {
		t.Fatal(err)
	}
	file, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	meta := metaElements(t, file)
	if got := string(bytes.TrimRight(meta[0x0012], "\x00")); got != wantUID {
		t.Errorf("(0002,0012) = %q, want %q", got, wantUID)
	}
	if got := strings.TrimRight(string(meta[0x0013]), " "); got != wantName {
		t.Errorf("(0002,0013) = %q, want %q", got, wantName)
	}

	// 3. A-ASSOCIATE-RQ of a C-MOVE sub-operation association.
	mq := godicom.NewDataset()
	_ = mq.SetString(godicom.MustTag("QueryRetrieveLevel"), "STUDY")
	_ = mq.SetString(godicom.MustTag("StudyInstanceUID"), study)
	mv, err := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
		AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelMove, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian},
	}}}, addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = mv.CMove(ctx, ae.MoveRequest{QueryModel: ae.StudyRootQueryRetrieveInformationModelMove, MoveDestination: "DEST", IdentifierData: mq})
	_ = mv.Release(ctx)
	select {
	case rq := <-gotRQ:
		if ui := rq.UserInformation; ui.ImplementationClassUID != wantUID || ui.ImplementationVersionName != wantName {
			t.Errorf("C-MOVE A-ASSOCIATE-RQ carries %q / %q, want %q / %q", ui.ImplementationClassUID, ui.ImplementationVersionName, wantUID, wantName)
		}
	case <-ctx.Done():
		t.Fatal("no A-ASSOCIATE-RQ reached the C-MOVE destination")
	}
}
