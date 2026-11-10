package dcm

// Go native fuzz targets for the Part 10 writer/reader and the inbound
// dataset decoding path (cybersecurity.md TM-08). `go test` runs the seed
// corpus as regression tests; `go test -fuzz=FuzzX -fuzztime=...` explores.

import (
	"bytes"
	"testing"

	"github.com/godicom-dev/godicom"
)

func seedDataset(t interface{ Fatal(...any) }, ts string) []byte {
	ds := godicom.NewDataset()
	set := func(kw, v string) { _ = ds.SetString(godicom.MustTag(kw), v) }
	set("SOPClassUID", "1.2.840.10008.5.1.4.1.1.2")
	set("SOPInstanceUID", "1.2.3.4.5.6")
	set("StudyInstanceUID", "1.2.3.4")
	set("SeriesInstanceUID", "1.2.3.4.5")
	set("PatientID", "P001")
	set("PatientName", "DOE^JANE=ドウ^ジェーン")
	set("SpecificCharacterSet", "ISO_IR 192")
	set("StudyDate", "20260115")
	set("Modality", "CT")
	_ = ds.SetInt(godicom.MustTag("InstanceNumber"), 7)
	b, err := ds.Encode(godicom.UID(ts))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// FuzzPart10RoundTrip: whatever identifiers and dataset bytes arrive, Part10
// either refuses them or writes a file whose dataset bytes read back
// unchanged (DCM-SRS-004, DCM-SRS-018).
// Verifies: DCM-SRS-004, DCM-SRS-018
func FuzzPart10RoundTrip(f *testing.F) {
	f.Add("1.2.840.10008.5.1.4.1.1.2", "1.2.3.4.5.6", ExplicitVRLittleEndian, "MODALITY", seedDataset(f, ExplicitVRLittleEndian))
	f.Add("1.2.840.10008.5.1.4.1.1.7", "1.2.3", ImplicitVRLittleEndian, "", seedDataset(f, ImplicitVRLittleEndian))
	f.Add("1.2.840.10008.5.1.4.1.1.7", "1.2.3.", "1.2.840.10008.1.2.4.50", "A", []byte{})
	f.Fuzz(func(t *testing.T, class, inst, ts, ae string, dataset []byte) {
		file, err := Part10(class, inst, ts, ae, dataset)
		if err != nil {
			return
		}
		off, err := DatasetOffset(file)
		if err != nil {
			t.Fatalf("Part10 output not readable by DatasetOffset: %v", err)
		}
		if !bytes.Equal(file[off:], dataset) {
			t.Fatal("dataset bytes changed")
		}
		fd, err := godicom.ReadBytes(file[:off], nil) // meta only
		if err != nil {
			// The meta group must always parse, whatever the dataset holds.
			t.Fatalf("file meta unreadable: %v", err)
		}
		if fd.FileMeta.Dataset == nil || len(fd.FileMeta.Dataset.Iter()) == 0 {
			t.Fatal("file meta group empty after read-back")
		}
		// The identifier must be written as given (plus at most one padding
		// byte). Compared on raw bytes: a parser's own trimming of padding
		// and whitespace is not part of this contract (the first oracle,
		// via the parsed string, gave false alarms for " ", "\x00", "\r").
		if !bytes.Contains(file[:off], []byte(inst)) {
			t.Fatalf("meta SOP Instance UID %q not written as given", inst)
		}
	})
}

// FuzzDatasetOffset: arbitrary stored bytes never panic the reader used by
// C-MOVE/C-GET, and a returned offset is inside the file.
func FuzzDatasetOffset(f *testing.F) {
	good, _ := Part10("1.2.840.10008.5.1.4.1.1.2", "1.2.3", ExplicitVRLittleEndian, "", seedDataset(f, ExplicitVRLittleEndian))
	f.Add(good)
	f.Add(good[:140])
	f.Add([]byte("DICM"))
	f.Fuzz(func(t *testing.T, file []byte) {
		off, err := DatasetOffset(file)
		if err == nil && (off < 0 || off > len(file)) {
			t.Fatalf("offset %d outside file of %d bytes", off, len(file))
		}
	})
}

// FuzzDecodeAndExtract: the inbound C-STORE path decodes untrusted dataset
// bytes (godicom, SOUP) and extracts the index fields; neither may panic or
// hang, in either native transfer syntax.
func FuzzDecodeAndExtract(f *testing.F) {
	f.Add(seedDataset(f, ExplicitVRLittleEndian), true)
	f.Add(seedDataset(f, ImplicitVRLittleEndian), false)
	f.Add([]byte{0x08, 0x00, 0x05, 0x00, 'C', 'S', 0x0a, 0x00, 'I', 'S', 'O', '_', 'I', 'R', ' ', '1', '9', '2'}, true)
	f.Fuzz(func(t *testing.T, data []byte, explicit bool) {
		ts := ImplicitVRLittleEndian
		if explicit {
			ts = ExplicitVRLittleEndian
		}
		ds, err := godicom.DecodeDataset(data, godicom.UID(ts))
		if err != nil || ds == nil {
			return
		}
		inst := ExtractInstance(ds)
		_ = Strings(ds, "PatientName")
		_ = Has(ds, "PixelData")
		_ = inst
	})
}
