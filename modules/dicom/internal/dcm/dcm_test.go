package dcm

import (
	"bytes"
	"errors"
	"strings"
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

// Identifiers that cannot be encoded in the File Meta group are refused
// rather than written as an unreadable file (gosec G115 finding, 2026-11-10).
// Over-long but encodable values (non-conformant senders) are still kept.
// Verifies: DCM-SRS-018
func TestPart10RefusesUnencodableMeta(t *testing.T) {
	huge := strings.Repeat("9", 70000)
	const ct = "1.2.840.10008.5.1.4.1.1.2"
	cases := []struct{ class, inst, ts, ae string }{
		{"", "1.2.3", ExplicitVRLittleEndian, ""},
		{ct, "", ExplicitVRLittleEndian, ""},
		{ct, "1.2.3", "", ""},
		{ct, huge, ExplicitVRLittleEndian, ""},
		{huge, "1.2.3", ExplicitVRLittleEndian, ""},
		{ct, "1.2.3", huge, ""},
		{ct, "1.2.3", ExplicitVRLittleEndian, huge},
	}
	for i, c := range cases {
		if f, err := Part10(c.class, c.inst, c.ts, c.ae, []byte{0x08, 0x00, 0x05, 0x00, 0, 0, 0, 0}); !errors.Is(err, ErrInvalidMeta) || f != nil {
			t.Errorf("case %d: err = %v, file %d bytes; want ErrInvalidMeta", i, err, len(f))
		}
	}
	// The longest encodable value, and a non-conformant 84-byte UID, are
	// written and read back unchanged.
	for _, inst := range []string{"1." + strings.Repeat("2", maxMetaValueLen-2), "1.2." + strings.Repeat("3", 80)} {
		f, err := Part10(ct, inst, ExplicitVRLittleEndian, "SIXTEEN_CHARS_AE", nil)
		if err != nil {
			t.Fatalf("%d-byte UID: %v", len(inst), err)
		}
		fd, err := godicom.ReadBytes(f, nil)
		if err != nil || String(fd.FileMeta.Dataset, "MediaStorageSOPInstanceUID") != inst {
			t.Fatalf("%d-byte UID not round-tripped: %v", len(inst), err)
		}
	}
}
