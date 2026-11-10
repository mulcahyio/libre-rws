package dcm

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
)

// testDataset builds a dataset with a referenced-image sequence of undefined
// length (so the parser must walk nested items) and native pixel data.
func testDataset(t *testing.T, ts string, extra func(*godicom.Dataset)) []byte {
	t.Helper()
	ds := godicom.NewDataset()
	_ = ds.SetString(godicom.MustTag("SOPClassUID"), "1.2.840.10008.5.1.4.1.1.7")
	_ = ds.SetString(godicom.MustTag("SOPInstanceUID"), "1.2.3.4.5")
	_ = ds.SetString(godicom.MustTag("PatientName"), "DOE^JANE")
	_ = ds.SetString(godicom.MustTag("PatientID"), "PAT-1")
	_ = ds.SetString(godicom.MustTag("PatientBirthDate"), "19700101")
	_ = ds.SetString(godicom.MustTag("StudyInstanceUID"), "1.2.3")
	_ = ds.SetString(godicom.MustTag("SeriesInstanceUID"), "1.2.3.1")
	ref := godicom.NewDataset()
	_ = ref.SetString(godicom.MustTag("ReferencedSOPClassUID"), "1.2.840.10008.5.1.4.1.1.7")
	_ = ref.SetString(godicom.MustTag("ReferencedSOPInstanceUID"), "1.2.3.4.4")
	_ = ds.SetSequence(godicom.MustTag("ReferencedImageSequence"), godicom.NewSequence([]*godicom.Dataset{ref}))
	_ = ds.SetInt(godicom.MustTag("Rows"), 2)
	_ = ds.SetInt(godicom.MustTag("Columns"), 2)
	_ = ds.SetInt(godicom.MustTag("BitsAllocated"), 8)
	_ = ds.SetInt(godicom.MustTag("BitsStored"), 8)
	_ = ds.SetInt(godicom.MustTag("HighBit"), 7)
	_ = ds.SetInt(godicom.MustTag("PixelRepresentation"), 0)
	_ = ds.SetInt(godicom.MustTag("SamplesPerPixel"), 1)
	_ = ds.SetString(godicom.MustTag("PhotometricInterpretation"), "MONOCHROME2")
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROB, []byte{1, 2, 3, 4}))
	if extra != nil {
		extra(ds)
	}
	raw, err := ds.Encode(godicom.UID(ts))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func el(ds *godicom.Dataset, kw string) *godicom.DataElement {
	e, _ := ds.Get(godicom.MustTag(kw))
	return e
}

func items(ds *godicom.Dataset, kw string) []*godicom.Dataset {
	e := el(ds, kw)
	if e == nil {
		return nil
	}
	sq, _ := e.Value.(*godicom.Sequence)
	if sq == nil {
		return nil
	}
	return sq.Items()
}

func decode(t *testing.T, raw []byte, ts string) *godicom.Dataset {
	t.Helper()
	file, err := Part10("1.2.840.10008.5.1.4.1.1.7", "1.2.3.4.5", ts, "", raw)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := godicom.ReadBytes(file, nil)
	if err != nil {
		t.Fatalf("coerced dataset unreadable: %v", err)
	}
	return fd.Dataset
}

// tail returns the bytes from the first element after the Original
// Attributes Sequence group (0400) to the end: everything Coerce must not touch.
func tailFrom(t *testing.T, raw []byte, explicit bool, tag uint32) []byte {
	t.Helper()
	r := reader{b: raw, explicit: explicit}
	_, rest, err := r.topLevel(tag - 1)
	if err != nil {
		t.Fatal(err)
	}
	return raw[rest:]
}

// Coercing Patient ID changes only the Patient ID, records the old value in
// the Original Attributes Sequence (reason COERCE) and leaves every element
// after group 0400, including Pixel Data, byte-identical. Both encodings.
// Verifies: DCM-SRS-075
func TestCoercePatientID(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, ts := range []string{ExplicitVRLittleEndian, ImplicitVRLittleEndian} {
		explicit, ok := IsExplicitLE(ts)
		if !ok {
			t.Fatal("syntax not stored")
		}
		raw := testDataset(t, ts, nil)
		out, changed, err := Coerce(raw, explicit, []Change{{Tag: TagPatientID, VR: "LO", Value: "B000_PAT-1"}}, ReasonCoerce, "LibreDCM", now)
		if err != nil || !changed {
			t.Fatalf("%s: changed=%v err=%v", ts, changed, err)
		}
		if !bytes.Equal(tailFrom(t, raw, explicit, 0x04000562), tailFrom(t, out, explicit, 0x04000562)) {
			t.Fatalf("%s: bytes after the Original Attributes Sequence changed", ts)
		}
		ds := decode(t, out, ts)
		if got := String(ds, "PatientID"); got != "B000_PAT-1" {
			t.Fatalf("%s: PatientID = %q", ts, got)
		}
		if got := String(ds, "PatientName"); got != "DOE^JANE" {
			t.Fatalf("%s: PatientName = %q", ts, got)
		}
		if got := String(ds, "InstanceCoercionDateTime"); got != "20261110120000.000000+0000" {
			t.Fatalf("%s: InstanceCoercionDateTime = %q", ts, got)
		}
		pd := el(ds, "PixelData")
		if pd == nil {
			t.Fatalf("%s: pixel data lost", ts)
		}
		if b, ok := pd.Value.([]byte); !ok || !bytes.Equal(b, []byte{1, 2, 3, 4}) {
			t.Fatalf("%s: pixel data = %v", ts, pd.Value)
		}
		oas := el(ds, "OriginalAttributesSequence")
		if oas == nil {
			t.Fatalf("%s: no Original Attributes Sequence", ts)
		}
		its := items(ds, "OriginalAttributesSequence")
		if len(its) != 1 {
			t.Fatalf("%s: %d items", ts, len(its))
		}
		it := its[0]
		if got := String(it, "ReasonForTheAttributeModification"); got != "COERCE" {
			t.Fatalf("%s: reason = %q", ts, got)
		}
		if got := String(it, "ModifyingSystem"); got != "LibreDCM" {
			t.Fatalf("%s: modifying system = %q", ts, got)
		}
		mod := items(it, "ModifiedAttributesSequence")[0]
		if got := String(mod, "PatientID"); got != "PAT-1" {
			t.Fatalf("%s: previous PatientID = %q", ts, got)
		}
		// The referenced image sequence (undefined length) survived.
		if len(items(ds, "ReferencedImageSequence")) != 1 {
			t.Fatalf("%s: referenced image sequence lost", ts)
		}
	}
}

// A second change appends an item and keeps the first (PS3.3 C.12.1.1.9:
// existing items shall be preserved); an unchanged value is a no-op.
// Verifies: DCM-SRS-075, DCM-SRS-078
func TestCoerceKeepsHistoryAndSkipsNoop(t *testing.T) {
	now := time.Now()
	raw := testDataset(t, ExplicitVRLittleEndian, nil)
	first, _, err := Coerce(raw, true, []Change{{Tag: TagPatientID, VR: "LO", Value: "B000_PAT-1"}}, ReasonCoerce, "LibreDCM", now)
	if err != nil {
		t.Fatal(err)
	}
	second, changed, err := Coerce(first, true, []Change{
		{Tag: TagPatientID, VR: "LO", Value: "PAT-2"},
		{Tag: TagPatientName, VR: "PN", Value: "ROE^RICHARD"},
		{Tag: TagPatientSex, VR: "CS", Value: "M"}, // absent before: added
	}, ReasonCorrect, "LibreWT via LibreDCM", now)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	ds := decode(t, second, ExplicitVRLittleEndian)
	if String(ds, "PatientID") != "PAT-2" || String(ds, "PatientName") != "ROE^RICHARD" || String(ds, "PatientSex") != "M" {
		t.Fatalf("values: %q %q %q", String(ds, "PatientID"), String(ds, "PatientName"), String(ds, "PatientSex"))
	}
	its := items(ds, "OriginalAttributesSequence")
	if len(its) != 2 || String(its[0], "ReasonForTheAttributeModification") != "COERCE" ||
		String(its[1], "ReasonForTheAttributeModification") != "CORRECT" {
		t.Fatalf("history not kept: %d items", len(its))
	}
	mod := items(its[1], "ModifiedAttributesSequence")[0]
	if String(mod, "PatientID") != "B000_PAT-1" || String(mod, "PatientName") != "DOE^JANE" {
		t.Fatalf("previous values: %q %q", String(mod, "PatientID"), String(mod, "PatientName"))
	}
	if same, changed, err := Coerce(second, true, []Change{{Tag: TagPatientID, VR: "LO", Value: "PAT-2"}}, ReasonCorrect, "x", now); err != nil || changed || !bytes.Equal(same, second) {
		t.Fatalf("no-op changed the dataset: changed=%v err=%v", changed, err)
	}
}

// An existing Original Attributes Sequence of undefined length gets the new
// item before its delimiter; encapsulated pixel data after it is untouched.
// Verifies: DCM-SRS-075
func TestCoerceUndefinedLengthHistoryAndEncapsulated(t *testing.T) {
	raw := testDataset(t, ExplicitVRLittleEndian, func(ds *godicom.Dataset) { ds.Delete(godicom.MustTag("PixelData")) })
	first, _, err := Coerce(raw, true, []Change{{Tag: TagPatientID, VR: "LO", Value: "X1"}}, ReasonCoerce, "A", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the defined-length OAS as undefined length by hand.
	r := reader{b: first, explicit: true}
	spans, _, err := r.topLevel(tagOriginalAttributesSeq)
	if err != nil {
		t.Fatal(err)
	}
	s := spans[len(spans)-1]
	if s.tag != tagOriginalAttributesSeq {
		t.Fatalf("last span %08X", s.tag)
	}
	var undef bytes.Buffer
	undef.Write(first[:s.start])
	undef.Write(encodeElement(true, tagOriginalAttributesSeq, "SQ", nil, true)[:12])
	undef.Write(first[s.vstart:s.vend])
	undef.Write([]byte{0xFE, 0xFF, 0xDD, 0xE0, 0, 0, 0, 0})
	// encapsulated pixel data: basic offset table + one fragment
	pix := encodeElement(true, 0x7FE00010, "OB", nil, true)[:12]
	undef.Write(pix)
	frag := []byte{0xFF, 0xD8, 0xFF, 0xD9}
	undef.Write([]byte{0xFE, 0xFF, 0x00, 0xE0, 0, 0, 0, 0})
	undef.Write([]byte{0xFE, 0xFF, 0x00, 0xE0})
	_ = binary.Write(&undef, binary.LittleEndian, uint32(len(frag)))
	undef.Write(frag)
	undef.Write([]byte{0xFE, 0xFF, 0xDD, 0xE0, 0, 0, 0, 0})
	in := undef.Bytes()

	out, changed, err := Coerce(in, true, []Change{{Tag: TagPatientID, VR: "LO", Value: "X2"}}, ReasonCorrect, "B", time.Now())
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !bytes.HasSuffix(out, in[len(in)-len(pix)-8-8-len(frag)-8:]) {
		t.Fatal("encapsulated pixel data changed")
	}
	ds := decode(t, out, "1.2.840.10008.1.2.4.50")
	its := items(ds, "OriginalAttributesSequence")
	if len(its) != 2 || String(ds, "PatientID") != "X2" {
		t.Fatalf("items=%d pid=%q", len(its), String(ds, "PatientID"))
	}
}

// Values too long for LO and tags outside the editable range are refused.
// Verifies: DCM-SRS-075
func TestCoerceRefusesInvalid(t *testing.T) {
	raw := testDataset(t, ExplicitVRLittleEndian, nil)
	long := "B000_" + string(bytes.Repeat([]byte("9"), 60))
	if _, _, err := Coerce(raw, true, []Change{{Tag: TagPatientID, VR: "LO", Value: long}}, ReasonCoerce, "x", time.Now()); err == nil {
		t.Fatal("over-long Patient ID accepted")
	}
	if _, _, err := Coerce(raw, true, []Change{{Tag: 0x7FE00010, VR: "OB", Value: "x"}}, ReasonCoerce, "x", time.Now()); err == nil {
		t.Fatal("pixel data edit accepted")
	}
	if _, _, err := Coerce(raw[:len(raw)-3], true, []Change{{Tag: TagPatientID, VR: "LO", Value: "Y"}}, ReasonCoerce, "x", time.Now()); err != nil {
		t.Log("truncated tail is outside the parsed range; ok either way:", err)
	}
	if got, err := TopLevelString(raw, true, TagPatientID); err != nil || got != "PAT-1" {
		t.Fatalf("TopLevelString = %q, %v", got, err)
	}
}
