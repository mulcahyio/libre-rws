package gateway

// Go native fuzz targets for C-FIND identifier parsing and the query path it
// drives (cybersecurity.md TM-08): untrusted identifiers go through
// parseIdentifier, the SQL index and buildResponse. `go test` runs the seeds.

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/blob"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

var (
	fuzzOnce sync.Once
	fuzzGW   *Gateway
	fuzzErr  error
)

// fuzzGateway is one gateway with a seeded SQLite index, shared by all fuzz
// iterations (C-FIND does not write).
func fuzzGateway(f *testing.F) *Gateway {
	fuzzOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), "libredcm-fuzz-"+time.Now().Format("20060102150405.000000"))
		blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
		if err != nil {
			fuzzErr = err
			return
		}
		idx, err := index.Open(context.Background(), "sqlite", "file:"+filepath.Join(dir, "index.db"))
		if err != nil {
			fuzzErr = err
			return
		}
		for i, r := range []index.Record{
			{PatientID: "P001", PatientName: "DOE^JANE", StudyUID: "1.2.3", StudyDate: "20260115", StudyTime: "083000",
				SeriesUID: "1.2.3.1", Modality: "CT", SOPInstanceUID: "1.2.3.1.1", SOPClassUID: "1.2.840.10008.5.1.4.1.1.2", BlobKey: "a"},
			{PatientID: "P002", PatientName: "MÜLLER^JÖRG", StudyUID: "1.2.4", StudyDate: "20251231", StudyTime: "235959.5",
				SeriesUID: "1.2.4.1", Modality: "MR", SOPInstanceUID: "1.2.4.1.1", SOPClassUID: "1.2.840.10008.5.1.4.1.1.4", BlobKey: "b"},
			{PatientID: "P_%\\", PatientName: "ドウ^ジェーン", StudyUID: "1.2.5", SeriesUID: "1.2.5.1", Modality: "US",
				SOPInstanceUID: "1.2.5.1.1", BlobKey: "c"},
		} {
			r.InstanceNumber = strconv.Itoa(i + 1)
			if err := idx.Upsert(context.Background(), r); err != nil {
				fuzzErr = err
				return
			}
		}
		cfg := DefaultConfig()
		cfg.FindLimit = 50
		fuzzGW = New(cfg, "fuzz", slog.New(slog.NewTextHandler(io.Discard, nil)), prometheus.NewRegistry(), blobs, idx, &recPub{})
	})
	if fuzzErr != nil {
		f.Fatal(fuzzErr)
	}
	return fuzzGW
}

var findModels = []string{ae.StudyRootQueryRetrieveInformationModelFind, ae.PatientRootQueryRetrieveInformationModelFind}

// checkFind runs one C-FIND and checks the invariants: no panic, no query
// error (a well-formed identifier must never make the SQL fail), a final
// Success, and every response encodable on the wire in the transfer syntax
// the request arrived in (the SCP answers in the same presentation context).
func checkFind(t *testing.T, g *Gateway, ident *godicom.Dataset, model, ts string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	matches := g.onCFind(ctx, ae.FindRequest{QueryModel: model, IdentifierData: ident})
	if len(matches) == 0 {
		t.Fatal("no C-FIND response")
	}
	last := matches[len(matches)-1].Status
	switch last {
	case status.Success, status.IdentifierDoesNotMatchSOPClass:
	case status.UnableToProcess:
		t.Fatalf("query failed in the index for a parsed identifier (model %s)", model)
	default:
		t.Fatalf("unexpected final status %#04x", last)
	}
	for _, m := range matches[:len(matches)-1] {
		if m.Status != status.Pending || m.Identifier == nil {
			t.Fatalf("bad pending response %+v", m)
		}
		if _, err := m.Identifier.Encode(godicom.UID(ts)); err != nil {
			t.Fatalf("response not encodable: %v", err)
		}
	}
}

// FuzzCFindKeys drives the matching rules with structured values.
// Verifies: DCM-SRS-006
func FuzzCFindKeys(f *testing.F) {
	g := fuzzGateway(f)
	f.Add("STUDY", "DOE*", "20260101-20260131", "0800-0900", "", "", "P001", true)
	f.Add("PATIENT", "müller^jörg", "", "", "", "", "", false)
	f.Add("SERIES", "*", "-20251231", "08", "1.2.3\\1.2.4", "CT", "", true)
	f.Add("IMAGE", "?OE^*", "20260115", "235959.5-", "1.2.5", "US", "P_%\\", true)
	f.Add("STUDY", "%_\\", "2026", "12:30", "", "MR\\CT", "*_*", false)
	f.Fuzz(func(t *testing.T, level, name, date, tm, uids, modality, pid string, studyRoot bool) {
		ds := godicom.NewDataset()
		set := func(kw, v string) {
			if err := ds.SetString(godicom.MustTag(kw), v); err != nil {
				t.Skip() // value the encoder itself refuses (e.g. too long for its VR)
			}
		}
		set("QueryRetrieveLevel", level)
		set("PatientName", name)
		set("PatientID", pid)
		set("StudyDate", date)
		set("StudyTime", tm)
		set("StudyInstanceUID", uids)
		set("ModalitiesInStudy", modality)
		set("Modality", modality)
		set("SeriesInstanceUID", "")
		set("SOPInstanceUID", "")
		set("NumberOfStudyRelatedInstances", "")
		model := findModels[0]
		if !studyRoot {
			model = findModels[1]
		}
		for _, ci := range []bool{false, true} {
			g.cfg.FindCaseSensitiveNames = ci
			checkFind(t, g, ds, model, dcm.ExplicitVRLittleEndian)
		}
	})
}

// FuzzCFindIdentifierBytes drives the same path from raw identifier bytes as
// they arrive on the wire (Implicit VR Little Endian, decoded by godicom).
// Verifies: DCM-SRS-006
func FuzzCFindIdentifierBytes(f *testing.F) {
	g := fuzzGateway(f)
	for _, lvl := range []string{"PATIENT", "STUDY", "SERIES", "IMAGE"} {
		ds := godicom.NewDataset()
		_ = ds.SetString(godicom.MustTag("QueryRetrieveLevel"), lvl)
		_ = ds.SetString(godicom.MustTag("PatientName"), "D*")
		_ = ds.SetString(godicom.MustTag("StudyDate"), "20250101-")
		_ = ds.SetString(godicom.MustTag("SpecificCharacterSet"), "ISO_IR 100")
		_ = ds.SetString(godicom.MustTag("StudyInstanceUID"), "")
		b, err := ds.Encode(godicom.UID(dcm.ImplicitVRLittleEndian))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, true)
	}
	f.Fuzz(func(t *testing.T, raw []byte, studyRoot bool) {
		ds, err := godicom.DecodeDataset(raw, godicom.UID(dcm.ImplicitVRLittleEndian))
		if err != nil || ds == nil {
			return
		}
		model := findModels[0]
		if !studyRoot {
			model = findModels[1]
		}
		checkFind(t, g, ds, model, dcm.ImplicitVRLittleEndian)
	})
}
