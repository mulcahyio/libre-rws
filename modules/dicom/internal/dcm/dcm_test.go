package dcm

import (
	"bytes"
	"testing"

	"github.com/godicom-dev/godicom"
)

// The archived file must be a valid Part 10 file whose dataset bytes are
// exactly what was received on the wire.
// Verifies: DCM-SRS-004
func TestPart10RoundTrip(t *testing.T) {
	ds := godicom.NewDataset()
	_ = ds.SetString(godicom.MustTag("SOPClassUID"), "1.2.840.10008.5.1.4.1.1.7")
	_ = ds.SetString(godicom.MustTag("SOPInstanceUID"), "1.2.3.4.5")
	_ = ds.SetString(godicom.MustTag("PatientName"), "DOE^JANE")
	raw, err := ds.Encode(godicom.UID(ExplicitVRLittleEndian))
	if err != nil {
		t.Fatal(err)
	}
	file, err := Part10("1.2.840.10008.5.1.4.1.1.7", "1.2.3.4.5", ExplicitVRLittleEndian, "MODALITY", raw)
	if err != nil {
		t.Fatal(err)
	}
	off, err := DatasetOffset(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(file[off:], raw) {
		t.Fatal("dataset bytes changed")
	}
	fd, err := godicom.ReadBytes(file, nil)
	if err != nil {
		t.Fatalf("godicom cannot read our Part 10 file: %v", err)
	}
	if got := String(fd.FileMeta.Dataset, "TransferSyntaxUID"); got != ExplicitVRLittleEndian {
		t.Fatalf("meta transfer syntax = %q", got)
	}
	if got := String(fd.Dataset, "PatientName"); got != "DOE^JANE" {
		t.Fatalf("patient name = %q", got)
	}
}

// Verifies: DCM-SRS-004
func TestStringsHandlesRawAndMultiValues(t *testing.T) {
	ds := godicom.NewDataset()
	_ = ds.SetStrings(godicom.MustTag("StudyInstanceUID"), "1.2", "1.3")
	ds.Set(godicom.NewDataElement(godicom.MustTag("StudyDate"), godicom.VRDA, []byte("20260101-20260131 ")))
	if got := Strings(ds, "StudyInstanceUID"); len(got) != 2 || got[1] != "1.3" {
		t.Fatalf("multi = %q", got)
	}
	if got := String(ds, "StudyDate"); got != "20260101-20260131" {
		t.Fatalf("raw = %q", got)
	}
}

// Verifies: DCM-SRS-003
func TestSelectStorageSyntaxKeepsSenderPreference(t *testing.T) {
	// A modality offering JPEG 2000 first, with native fallbacks in the same
	// context, must be allowed to send compressed (upstream picked Implicit).
	got, ok := SelectStorageSyntax([]string{"1.2.840.10008.1.2.4.91", ExplicitVRLittleEndian, ImplicitVRLittleEndian})
	if !ok || got != "1.2.840.10008.1.2.4.91" {
		t.Fatalf("got %q", got)
	}
	if _, ok := SelectStorageSyntax([]string{"1.2.840.10008.1.2.1.99"}); ok { // deflate: not supported
		t.Fatal("unsupported syntax accepted")
	}
	if got, _ := SelectNativeSyntax([]string{ImplicitVRLittleEndian, ExplicitVRLittleEndian}); got != ExplicitVRLittleEndian {
		t.Fatalf("native = %q", got)
	}
}
