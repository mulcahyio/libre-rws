// Package dcm holds the DICOM-specific helpers the gateway needs. It wraps the
// raw dataset bytes received over the network into a Part 10 file *without*
// re-encoding them, so what is archived is byte-for-byte what the modality
// sent, and it reads the dataset bytes back out the same way for retrieval.
package dcm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/godicom-dev/godicom"
)

const (
	ImplicitVRLittleEndian = "1.2.840.10008.1.2"
	ExplicitVRLittleEndian = "1.2.840.10008.1.2.1"
	ExplicitVRBigEndian    = "1.2.840.10008.1.2.2" // retired, still seen in the wild

	ImplementationClassUID    = "1.2.826.0.1.3680043.10.541.1" // gonetdicom's root; replace with your own org root
	ImplementationVersionName = "LIBREDCM_020"
)

// IsUncompressed reports whether ts is a native (non-encapsulated) syntax the
// library can safely re-encode between.
func IsUncompressed(ts string) bool {
	return ts == ImplicitVRLittleEndian || ts == ExplicitVRLittleEndian
}

// Part10 builds a DICOM Part 10 file: 128-byte preamble, "DICM", an Explicit VR
// Little Endian File Meta group with a correct group length, then the dataset
// bytes exactly as received.
func Part10(sopClass, sopInstance, transferSyntax, sourceAE string, dataset []byte) ([]byte, error) {
	// File Meta is always Explicit VR Little Endian (PS3.10 7.1). It is a
	// handful of elements, so it is encoded directly rather than through the
	// general encoder.
	var meta bytes.Buffer
	writeMeta(&meta, 0x0001, "OB", []byte{0x00, 0x01})
	writeMeta(&meta, 0x0002, "UI", padUI(sopClass))
	writeMeta(&meta, 0x0003, "UI", padUI(sopInstance))
	writeMeta(&meta, 0x0010, "UI", padUI(transferSyntax))
	writeMeta(&meta, 0x0012, "UI", padUI(ImplementationClassUID))
	writeMeta(&meta, 0x0013, "SH", padText(ImplementationVersionName))
	if sourceAE != "" {
		writeMeta(&meta, 0x0016, "AE", padText(sourceAE))
	}
	body := meta.Bytes()

	var buf bytes.Buffer
	buf.Grow(132 + 12 + len(body) + len(dataset))
	buf.Write(make([]byte, 128))
	buf.WriteString("DICM")
	// (0002,0000) UL FileMetaInformationGroupLength, explicit VR LE.
	gl := []byte{0x02, 0x00, 0x00, 0x00, 'U', 'L', 0x04, 0x00, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(gl[8:], uint32(len(body)))
	buf.Write(gl)
	buf.Write(body)
	buf.Write(dataset)
	return buf.Bytes(), nil
}

func writeMeta(b *bytes.Buffer, elem uint16, vr string, val []byte) {
	var hdr [12]byte
	binary.LittleEndian.PutUint16(hdr[0:], 0x0002)
	binary.LittleEndian.PutUint16(hdr[2:], elem)
	copy(hdr[4:6], vr)
	if vr == "OB" { // OB: 2 reserved bytes + 32-bit length
		binary.LittleEndian.PutUint32(hdr[8:], uint32(len(val)))
		b.Write(hdr[:12])
	} else {
		binary.LittleEndian.PutUint16(hdr[6:], uint16(len(val)))
		b.Write(hdr[:8])
	}
	b.Write(val)
}

func padUI(s string) []byte {
	b := []byte(s)
	if len(b)%2 == 1 {
		b = append(b, 0x00)
	}
	return b
}

func padText(s string) []byte {
	b := []byte(s)
	if len(b)%2 == 1 {
		b = append(b, ' ')
	}
	return b
}

// ErrNotPart10 is returned when a stored object is not a file written by Part10.
var ErrNotPart10 = errors.New("dcm: not a Part 10 file with group length")

// DatasetOffset returns where the dataset begins in a Part 10 file written by
// Part10 (or any conformant file that starts its meta with the group length).
func DatasetOffset(file []byte) (int, error) {
	if len(file) < 144 || string(file[128:132]) != "DICM" {
		return 0, ErrNotPart10
	}
	if !bytes.Equal(file[132:138], []byte{0x02, 0x00, 0x00, 0x00, 'U', 'L'}) {
		return 0, ErrNotPart10
	}
	gl := int(binary.LittleEndian.Uint32(file[140:144]))
	off := 144 + gl
	if off > len(file) {
		return 0, ErrNotPart10
	}
	return off, nil
}

// Strings returns the string values of a data element, whatever concrete type
// godicom decoded it into (string, UID, PersonName, MultiValue[...]).
func Strings(ds *godicom.Dataset, keyword string) []string {
	if ds == nil {
		return nil
	}
	el, ok := ds.Get(godicom.MustTag(keyword))
	if !ok || el == nil || el.Value == nil {
		return nil
	}
	return valueStrings(el.Value)
}

func valueStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return splitVM(x)
	case []string:
		return x
	case []byte: // e.g. DA ranges ("20260101-20260131") are kept raw by godicom
		return splitVM(string(x))
	}
	rv := reflect.ValueOf(v)
	if m := rv.MethodByName("Values"); m.IsValid() && m.Type().NumIn() == 0 {
		out := m.Call(nil)[0]
		if out.Kind() == reflect.Slice {
			res := make([]string, 0, out.Len())
			for i := 0; i < out.Len(); i++ {
				res = append(res, strings.TrimSpace(fmt.Sprint(out.Index(i).Interface())))
			}
			return res
		}
	}
	return splitVM(fmt.Sprint(v))
}

func splitVM(s string) []string {
	s = strings.TrimRight(s, " \x00")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, `\`)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// String returns the first value of a data element or "".
func String(ds *godicom.Dataset, keyword string) string {
	if v := Strings(ds, keyword); len(v) > 0 {
		return v[0]
	}
	return ""
}

// Has reports whether the dataset contains keyword (even with an empty value).
func Has(ds *godicom.Dataset, keyword string) bool {
	return ds != nil && ds.Has(godicom.MustTag(keyword))
}

// Instance is the metadata the index keeps for every stored instance.
type Instance struct {
	PatientID, PatientName, PatientBirthDate, PatientSex           string
	StudyInstanceUID, StudyDate, StudyTime, AccessionNumber        string
	StudyID, StudyDescription, ReferringPhysicianName              string
	SeriesInstanceUID, Modality, SeriesNumber, SeriesDescription   string
	SOPClassUID, SOPInstanceUID, InstanceNumber, TransferSyntaxUID string
}

// ExtractInstance pulls index metadata out of a decoded dataset.
func ExtractInstance(ds *godicom.Dataset) Instance {
	return Instance{
		PatientID:              String(ds, "PatientID"),
		PatientName:            String(ds, "PatientName"),
		PatientBirthDate:       String(ds, "PatientBirthDate"),
		PatientSex:             String(ds, "PatientSex"),
		StudyInstanceUID:       String(ds, "StudyInstanceUID"),
		StudyDate:              String(ds, "StudyDate"),
		StudyTime:              String(ds, "StudyTime"),
		AccessionNumber:        String(ds, "AccessionNumber"),
		StudyID:                String(ds, "StudyID"),
		StudyDescription:       String(ds, "StudyDescription"),
		ReferringPhysicianName: String(ds, "ReferringPhysicianName"),
		SeriesInstanceUID:      String(ds, "SeriesInstanceUID"),
		Modality:               String(ds, "Modality"),
		SeriesNumber:           String(ds, "SeriesNumber"),
		SeriesDescription:      String(ds, "SeriesDescription"),
		SOPClassUID:            String(ds, "SOPClassUID"),
		SOPInstanceUID:         String(ds, "SOPInstanceUID"),
		InstanceNumber:         String(ds, "InstanceNumber"),
	}
}

// StorageSyntaxes are the transfer syntaxes the archive accepts and stores
// unchanged. Native syntaxes first, then encapsulated (compressed) ones.
var StorageSyntaxes = []string{
	ExplicitVRLittleEndian,
	ImplicitVRLittleEndian,
	"1.2.840.10008.1.2.4.50",  // JPEG Baseline (8-bit lossy)
	"1.2.840.10008.1.2.4.51",  // JPEG Extended (12-bit lossy)
	"1.2.840.10008.1.2.4.57",  // JPEG Lossless
	"1.2.840.10008.1.2.4.70",  // JPEG Lossless SV1
	"1.2.840.10008.1.2.4.80",  // JPEG-LS Lossless
	"1.2.840.10008.1.2.4.81",  // JPEG-LS Near-Lossless
	"1.2.840.10008.1.2.4.90",  // JPEG 2000 Lossless
	"1.2.840.10008.1.2.4.91",  // JPEG 2000
	"1.2.840.10008.1.2.4.201", // HTJ2K Lossless
	"1.2.840.10008.1.2.4.202", // HTJ2K Lossless RPCL
	"1.2.840.10008.1.2.4.203", // HTJ2K
	"1.2.840.10008.1.2.5",     // RLE Lossless
}

// SelectStorageSyntax keeps the sender's preference: the first proposed
// syntax we can store as-is wins, so compressed data is archived compressed.
func SelectStorageSyntax(proposed []string) (string, bool) {
	for _, p := range proposed {
		for _, s := range StorageSyntaxes {
			if p == s {
				return p, true
			}
		}
	}
	return "", false
}

// SelectNativeSyntax is used for non-storage services (verification, query/
// retrieve identifiers), which only need a native encoding.
func SelectNativeSyntax(proposed []string) (string, bool) {
	for _, want := range []string{ExplicitVRLittleEndian, ImplicitVRLittleEndian} {
		for _, p := range proposed {
			if p == want {
				return p, true
			}
		}
	}
	return "", false
}
