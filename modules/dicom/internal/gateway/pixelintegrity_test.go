package gateway

// Pixel-data integrity through the archive (class-C depth for the store and
// retrieve units, detailed design DHF-DCM-DD §4; HAZ-003). An instance must
// leave LibreDCM with exactly the pixel bytes it arrived with:
//   - stored and sent in the same transfer syntax: whole dataset byte for byte;
//   - native syntaxes re-encoded (Implicit <-> Explicit VR LE): pixel values
//     identical, and Pixel Data written as OW for 16-bit data;
//   - compressed data is never decompressed or transcoded: a destination that
//     does not accept the stored syntax gets a failed sub-operation.

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
)

const jpegLossless = "1.2.840.10008.1.2.4.70"

// pattern16 is 16-bit pixel data whose bytes differ in every position and
// use the high byte, so byte swapping, truncation or an OB/OW mix-up shows.
func pattern16(rows, cols int) []byte {
	b := make([]byte, rows*cols*2)
	for i := 0; i < rows*cols; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(0x0F00+i*37)) // #nosec G115 -- small test values
	}
	return b
}

func imageWith(study, series, sop string, n int, pixels []byte) *godicom.Dataset {
	ds := ctImage(study, series, sop, n)
	_ = ds.SetInt(godicom.MustTag("Rows"), 16)
	_ = ds.SetInt(godicom.MustTag("Columns"), 16)
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROW, pixels))
	return ds
}

// encapsulated returns raw Explicit VR LE dataset bytes with encapsulated
// (compressed) Pixel Data: empty basic offset table and one fragment.
func encapsulated(t *testing.T, study, series, sop string, fragment []byte) []byte {
	t.Helper()
	ds := ctImage(study, series, sop, 9)
	ds.Delete(godicom.MustTag("PixelData"))
	hdr, err := ds.Encode(godicom.UID(dcm.ExplicitVRLittleEndian))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	b.Write(hdr)
	b.Write([]byte{0xE0, 0x7F, 0x10, 0x00, 'O', 'B', 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}) // (7FE0,0010) OB undefined length
	b.Write([]byte{0xFE, 0xFF, 0x00, 0xE0, 0, 0, 0, 0})                             // item: empty basic offset table
	item := make([]byte, 8)
	copy(item, []byte{0xFE, 0xFF, 0x00, 0xE0})
	binary.LittleEndian.PutUint32(item[4:], uint32(len(fragment))) // #nosec G115 -- test fragment
	b.Write(item)
	b.Write(fragment)
	b.Write([]byte{0xFE, 0xFF, 0xDD, 0xE0, 0, 0, 0, 0}) // sequence delimiter
	return b.Bytes()
}

type receivedInstance struct {
	ts  string
	raw []byte
}

// destination starts a storage SCP that accepts only the transfer syntaxes
// in accept (in that order of preference) and records what it receives.
func destination(t *testing.T, ctx context.Context, name string, accept ...string) (RemoteAE, func() map[string]receivedInstance) {
	t.Helper()
	var mu sync.Mutex
	got := map[string]receivedInstance{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: name, AcceptedAbstractSyntaxes: ae.AllStorageSOPClasses,
			SelectTransferSyntax: func(_ string, proposed []string) (string, bool) {
				for _, a := range accept {
					for _, p := range proposed {
						if p == a {
							return a, true
						}
					}
				}
				return "", false
			},
			OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
				mu.Lock()
				defer mu.Unlock()
				got[req.AffectedSOPInstanceUID] = receivedInstance{req.TransferSyntax, append([]byte(nil), req.Dataset...)}
				return status.Success
			},
		})
	}()
	host, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	return RemoteAE{Host: host, Port: port, AETitle: name}, func() map[string]receivedInstance {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]receivedInstance{}
		for k, v := range got {
			out[k] = v
		}
		return out
	}
}

func pixelsOf(t *testing.T, r receivedInstance) ([]byte, godicom.VR) {
	t.Helper()
	ds, err := godicom.DecodeDataset(r.raw, godicom.UID(r.ts))
	if err != nil {
		t.Fatalf("destination could not decode what it received (%s): %v", r.ts, err)
	}
	el, ok := ds.Get(godicom.MustTag("PixelData"))
	if !ok {
		t.Fatal("no Pixel Data received")
	}
	px, ok := ds.GetBytes(godicom.MustTag("PixelData"))
	if !ok {
		t.Fatal("Pixel Data not readable as bytes")
	}
	return px, el.VR
}

// Verifies: DCM-SRS-004, DCM-SRS-010
func TestPixelDataIntegrity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	implDest, implGot := destination(t, ctx, "IMPL", dcm.ImplicitVRLittleEndian)
	explDest, explGot := destination(t, ctx, "EXPL", dcm.ExplicitVRLittleEndian)
	anyDest, anyGot := destination(t, ctx, "ANY", jpegLossless, dcm.ExplicitVRLittleEndian, dcm.ImplicitVRLittleEndian)
	addr, _, _ := startGateway(t, ctx, map[string]RemoteAE{"IMPL": implDest, "EXPL": explDest, "ANY": anyDest})

	study, series := "1.2.826.0.1.3680043.10.999.7", "1.2.826.0.1.3680043.10.999.7.1"
	sopExpl, sopImpl, sopJPEG := series+".1", series+".2", series+".3"
	pxExpl, pxImpl := pattern16(16, 16), pattern16(16, 16)
	pxImpl[0], pxImpl[1] = 0xAB, 0xCD // the two native instances differ
	fragment := []byte{0xFF, 0xD8, 0xFF, 0xC3, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0xFF, 0xD9}
	rawJPEG := encapsulated(t, study, series, sopJPEG, fragment)

	// Store: one instance per transfer syntax, each on its own association.
	store := func(ts string, req ae.StoreRequest) {
		t.Helper()
		scu, err := ae.Dial(ctx, ae.Config{AETitle: "MODALITY", PresentationContexts: []ae.PresentationContext{
			{AbstractSyntax: string(uid.CTImageStorage), TransferSyntaxes: []string{ts}},
		}}, addr, "RWS_TEST")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = scu.Release(ctx) }()
		res, err := scu.CStore(ctx, req)
		if err != nil || res.Status != status.Success {
			t.Fatalf("store %s in %s: %v %+v", req.AffectedSOPInstanceUID, ts, err, res)
		}
	}
	store(dcm.ExplicitVRLittleEndian, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage), AffectedSOPInstanceUID: sopExpl,
		Data: imageWith(study, series, sopExpl, 1, pxExpl)})
	store(dcm.ImplicitVRLittleEndian, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage), AffectedSOPInstanceUID: sopImpl,
		Data: imageWith(study, series, sopImpl, 2, pxImpl)})
	store(jpegLossless, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage), AffectedSOPInstanceUID: sopJPEG,
		Dataset: rawJPEG, TransferSyntax: jpegLossless})

	move := func(dest string) ae.RetrieveMatch {
		t.Helper()
		q := godicom.NewDataset()
		_ = q.SetString(godicom.MustTag("QueryRetrieveLevel"), "STUDY")
		_ = q.SetString(godicom.MustTag("StudyInstanceUID"), study)
		mv, err := ae.Dial(ctx, ae.Config{AETitle: "VIEWER", PresentationContexts: []ae.PresentationContext{{
			AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelMove, TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian},
		}}}, addr, "RWS_TEST")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = mv.Release(ctx) }()
		res, err := mv.CMove(ctx, ae.MoveRequest{QueryModel: ae.StudyRootQueryRetrieveInformationModelMove, MoveDestination: dest, IdentifierData: q})
		if err != nil || len(res) == 0 {
			t.Fatalf("move to %s: %v", dest, err)
		}
		return res[len(res)-1]
	}
	checkNative := func(dest string, got map[string]receivedInstance, wantTS string) {
		t.Helper()
		for sop, want := range map[string][]byte{sopExpl: pxExpl, sopImpl: pxImpl} {
			r, ok := got[sop]
			if !ok {
				t.Fatalf("%s: %s not received", dest, sop)
			}
			if r.ts != wantTS {
				t.Fatalf("%s: %s arrived as %s, want %s", dest, sop, r.ts, wantTS)
			}
			px, vr := pixelsOf(t, r)
			if !bytes.Equal(px, want) {
				t.Fatalf("%s: pixel data of %s changed (%d bytes, first %x, want %x)", dest, sop, len(px), px[:4], want[:4])
			}
			if wantTS == dcm.ExplicitVRLittleEndian && vr != godicom.VROW {
				t.Fatalf("%s: 16-bit Pixel Data of %s written as %s, want OW", dest, sop, vr)
			}
		}
	}

	// Native-only destinations: both native instances arrive with identical
	// pixels (one byte for byte, one re-encoded); the compressed one is
	// refused, never decompressed.
	for _, d := range []struct {
		name string
		got  func() map[string]receivedInstance
		ts   string
	}{{"IMPL", implGot, dcm.ImplicitVRLittleEndian}, {"EXPL", explGot, dcm.ExplicitVRLittleEndian}} {
		final := move(d.name)
		if final.Status != status.OneOrMoreFailures || final.SubOperations.Completed != 2 || final.SubOperations.Failed != 1 {
			t.Fatalf("move to %s: final %+v, want B000 with 2 completed and 1 failed", d.name, final)
		}
		got := d.got()
		if _, ok := got[sopJPEG]; ok {
			t.Fatalf("%s received the compressed instance in a syntax it does not support", d.name)
		}
		checkNative(d.name, got, d.ts)
	}

	// A destination that accepts the stored syntaxes gets every dataset
	// exactly as it was received.
	if final := move("ANY"); final.Status != status.Success || final.SubOperations.Completed != 3 {
		t.Fatalf("move to ANY: final %+v", final)
	}
	got := anyGot()
	if r := got[sopJPEG]; r.ts != jpegLossless || !bytes.Equal(r.raw, rawJPEG) {
		t.Fatalf("compressed instance not byte-identical (ts %s, %d bytes, want %d)", r.ts, len(r.raw), len(rawJPEG))
	}
	if r := got[sopExpl]; r.ts != dcm.ExplicitVRLittleEndian {
		t.Fatalf("explicit instance arrived as %s", r.ts)
	}
	px, _ := pixelsOf(t, got[sopExpl])
	if !bytes.Equal(px, pxExpl) {
		t.Fatal("explicit instance pixel data changed")
	}
}
