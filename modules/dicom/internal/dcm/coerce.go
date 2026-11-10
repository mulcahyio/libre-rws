package dcm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Attribute coercion and correction (DCM-SRS-075, DCM-SRS-078).
//
// Coerce edits top-level elements of a Little Endian dataset by splicing
// bytes: the elements it changes are re-encoded, everything else (in
// particular Pixel Data and any element after the last changed tag) is copied
// unchanged. The previous values go into a new item of the Original
// Attributes Sequence (0400,0561) as required by PS3.3 C.12.1.1.9, and
// Instance Coercion DateTime (0008,0015) is set. Existing Original Attributes
// Sequence items are kept.
//
// Decoding and re-encoding the whole dataset would also work, but it would
// put pixel-data integrity (HAZ-003) in the hands of a full encoder; splicing
// keeps the bytes the modality sent.

// Element tags used here.
const (
	TagSpecificCharacterSet     uint32 = 0x00080005
	TagInstanceCoercionDateTime uint32 = 0x00080015
	TagPatientName              uint32 = 0x00100010
	TagPatientID                uint32 = 0x00100020
	TagPatientBirthDate         uint32 = 0x00100030
	TagPatientSex               uint32 = 0x00100040
	tagModifiedAttributesSeq    uint32 = 0x04000550
	tagOriginalAttributesSeq    uint32 = 0x04000561
	tagAttributeModDateTime     uint32 = 0x04000562
	tagModifyingSystem          uint32 = 0x04000563
	tagSourceOfPreviousValues   uint32 = 0x04000564
	tagReasonForModification    uint32 = 0x04000565
	tagItem                     uint32 = 0xFFFEE000
	tagItemDelim                uint32 = 0xFFFEE00D
	tagSeqDelim                 uint32 = 0xFFFEE0DD
	undefinedLength             uint32 = 0xFFFFFFFF
)

// Reasons for the attribute modification (PS3.3 C.12.1.1.9, defined terms).
const (
	ReasonCoerce  = "COERCE"
	ReasonCorrect = "CORRECT"
)

// MaxLO is the maximum length of an LO value such as Patient ID.
const MaxLO = 64

// Change sets one top-level element. Value is the unpadded value.
type Change struct {
	Tag   uint32
	VR    string // LO, PN, DA, CS
	Value string
}

// ErrUnsupportedSyntax is returned for transfer syntaxes Coerce can't splice.
var ErrUnsupportedSyntax = errors.New("dcm: coercion supports Little Endian transfer syntaxes only")

// IsExplicitLE reports whether ts encodes the dataset as Explicit VR Little
// Endian (every stored syntax except Implicit VR Little Endian). ok is false
// for syntaxes LibreDCM doesn't store.
func IsExplicitLE(ts string) (explicit, ok bool) {
	if ts == ImplicitVRLittleEndian {
		return false, true
	}
	for _, s := range StorageSyntaxes {
		if s == ts {
			return true, true
		}
	}
	return false, false
}

// span is one parsed element: bytes [start,end), value [vstart,vend).
type span struct {
	tag                      uint32
	vr                       string
	start, vstart, vend, end int
	undefined                bool
}

type reader struct {
	b        []byte
	explicit bool
}

func (r reader) u16(off int) uint16 { return binary.LittleEndian.Uint16(r.b[off:]) }
func (r reader) u32(off int) uint32 { return binary.LittleEndian.Uint32(r.b[off:]) }

func readTag(b []byte, off int) uint32 {
	return uint32(binary.LittleEndian.Uint16(b[off:]))<<16 | uint32(binary.LittleEndian.Uint16(b[off+2:]))
}

func longVR(vr string) bool {
	switch vr {
	case "OB", "OD", "OF", "OL", "OV", "OW", "SQ", "SV", "UC", "UN", "UR", "UT", "UV":
		return true
	}
	return false
}

// element parses the element at off.
func (r reader) element(off int) (span, error) {
	if off+8 > len(r.b) {
		return span{}, fmt.Errorf("dcm: truncated element header at %d", off)
	}
	s := span{tag: readTag(r.b, off), start: off}
	var length uint32
	if s.tag>>16 == 0xFFFE { // item/delimiter tags have no VR in any syntax
		length = r.u32(off + 4)
		s.vstart = off + 8
	} else if r.explicit {
		s.vr = string(r.b[off+4 : off+6])
		if longVR(s.vr) {
			if off+12 > len(r.b) {
				return span{}, fmt.Errorf("dcm: truncated element header at %d", off)
			}
			length = r.u32(off + 8)
			s.vstart = off + 12
		} else {
			length = uint32(r.u16(off + 6))
			s.vstart = off + 8
		}
	} else {
		length = r.u32(off + 4)
		s.vstart = off + 8
	}
	if length == undefinedLength {
		s.undefined = true
		// Explicit VR "UN" with undefined length holds an implicit VR sequence
		// (PS3.5 6.2.2); anything else with undefined length is SQ or
		// encapsulated pixel data.
		inner := r
		if r.explicit && s.vr == "UN" {
			inner.explicit = false
		}
		end, err := inner.skipUndefined(s.vstart)
		if err != nil {
			return span{}, err
		}
		s.vend = end - 8 // before the sequence delimitation item
		s.end = end
		return s, nil
	}
	s.vend = s.vstart + int(length)
	if s.vend > len(r.b) || s.vend < s.vstart {
		return span{}, fmt.Errorf("dcm: element %08X length %d overruns dataset", s.tag, length)
	}
	s.end = s.vend
	return s, nil
}

// skipUndefined walks the items of an undefined-length value starting at off
// and returns the offset just past its sequence delimitation item.
func (r reader) skipUndefined(off int) (int, error) {
	for off+8 <= len(r.b) {
		tag := readTag(r.b, off)
		length := r.u32(off + 4)
		switch tag {
		case tagSeqDelim:
			return off + 8, nil
		case tagItem:
			if length != undefinedLength {
				off += 8 + int(length)
				if off > len(r.b) {
					return 0, errors.New("dcm: item overruns dataset")
				}
				continue
			}
			// undefined-length item: nested dataset until the item delimiter
			p := off + 8
			for {
				if p+8 > len(r.b) {
					return 0, errors.New("dcm: unterminated item")
				}
				if readTag(r.b, p) == tagItemDelim {
					p += 8
					break
				}
				e, err := r.element(p)
				if err != nil {
					return 0, err
				}
				p = e.end
			}
			off = p
		default:
			return 0, fmt.Errorf("dcm: unexpected tag %08X in undefined-length value", tag)
		}
	}
	return 0, errors.New("dcm: missing sequence delimiter")
}

// topLevel parses top-level elements until the first tag greater than stop.
func (r reader) topLevel(stop uint32) ([]span, int, error) {
	var out []span
	off := 0
	for off < len(r.b) {
		if off+4 > len(r.b) {
			return nil, 0, fmt.Errorf("dcm: trailing bytes at %d", off)
		}
		if readTag(r.b, off) > stop {
			break
		}
		s, err := r.element(off)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, s)
		off = s.end
	}
	return out, off, nil
}

func pad(vr, v string) []byte {
	b := []byte(v)
	if len(b)%2 == 1 {
		if vr == "UI" {
			b = append(b, 0)
		} else {
			b = append(b, ' ')
		}
	}
	return b
}

func encodeElement(explicit bool, tag uint32, vr string, value []byte, undefined bool) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint16(tag>>16))    // #nosec G115 -- upper 16 bits of a 32-bit tag
	_ = binary.Write(&b, binary.LittleEndian, uint16(tag&0xFFFF)) // #nosec G115 -- lower 16 bits, masked
	length := uint32(len(value))                                  // #nosec G115 -- element values are far below 4 GiB
	if undefined {
		length = undefinedLength
	}
	if explicit {
		b.WriteString(vr)
		if longVR(vr) {
			b.Write([]byte{0, 0})
			_ = binary.Write(&b, binary.LittleEndian, length)
		} else {
			_ = binary.Write(&b, binary.LittleEndian, uint16(length)) // #nosec G115 -- short-VR values are < 64 KiB
		}
	} else {
		_ = binary.Write(&b, binary.LittleEndian, length)
	}
	b.Write(value)
	return b.Bytes()
}

func encodeItem(content []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint16(0xFFFE))
	_ = binary.Write(&b, binary.LittleEndian, uint16(0xE000))
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(content))) // #nosec G115 -- small
	b.Write(content)
	return b.Bytes()
}

// TopLevelString returns the raw (unpadded) text of a top-level element, or
// "" when absent. Only meaningful for text VRs.
func TopLevelString(dataset []byte, explicit bool, tag uint32) (string, error) {
	r := reader{b: dataset, explicit: explicit}
	spans, _, err := r.topLevel(tag)
	if err != nil {
		return "", err
	}
	for _, s := range spans {
		if s.tag == tag {
			return strings.TrimRight(string(dataset[s.vstart:s.vend]), " \x00"), nil
		}
	}
	return "", nil
}

// Coerce applies changes (top-level elements only) and records the previous
// values. Changes whose value already equals the stored value are skipped;
// when nothing would change, the dataset is returned unchanged and changed is
// false.
func Coerce(dataset []byte, explicit bool, changes []Change, reason, modifyingSystem string, now time.Time) (out []byte, changed bool, err error) {
	for _, c := range changes {
		if c.Tag>>16 == 0x0002 || c.Tag>>16 == 0xFFFE || c.Tag >= tagOriginalAttributesSeq {
			return nil, false, fmt.Errorf("dcm: tag %08X can't be coerced", c.Tag)
		}
		if c.VR == "LO" && len(c.Value) > MaxLO {
			return nil, false, fmt.Errorf("dcm: value for %08X exceeds %d characters", c.Tag, MaxLO)
		}
	}
	r := reader{b: dataset, explicit: explicit}
	spans, rest, err := r.topLevel(tagOriginalAttributesSeq)
	if err != nil {
		return nil, false, err
	}
	find := func(tag uint32) *span {
		for i := range spans {
			if spans[i].tag == tag {
				return &spans[i]
			}
		}
		return nil
	}

	// Keep only real changes.
	var real []Change
	for _, c := range changes {
		if s := find(c.Tag); s != nil {
			if strings.TrimRight(string(dataset[s.vstart:s.vend]), " \x00") == c.Value {
				continue
			}
		} else if c.Value == "" {
			continue
		}
		real = append(real, c)
	}
	if len(real) == 0 {
		return dataset, false, nil
	}
	sort.Slice(real, func(i, j int) bool { return real[i].Tag < real[j].Tag })

	stamp := now.UTC().Format("20060102150405.000000") + "+0000"

	// Modified Attributes Sequence item: previous values, copied verbatim
	// (same encoding as the dataset); attributes that were absent are
	// included empty.
	var prev bytes.Buffer
	prevTags := append([]Change(nil), real...)
	if find(TagInstanceCoercionDateTime) != nil {
		prevTags = append(prevTags, Change{Tag: TagInstanceCoercionDateTime, VR: "DT"})
	}
	sort.Slice(prevTags, func(i, j int) bool { return prevTags[i].Tag < prevTags[j].Tag })
	for _, c := range prevTags {
		if s := find(c.Tag); s != nil {
			prev.Write(dataset[s.start:s.end])
		} else {
			prev.Write(encodeElement(explicit, c.Tag, c.VR, nil, false))
		}
	}
	var item bytes.Buffer
	item.Write(encodeElement(explicit, tagModifiedAttributesSeq, "SQ", encodeItem(prev.Bytes()), false))
	item.Write(encodeElement(explicit, tagAttributeModDateTime, "DT", pad("DT", stamp), false))
	item.Write(encodeElement(explicit, tagModifyingSystem, "LO", pad("LO", modifyingSystem), false))
	item.Write(encodeElement(explicit, tagSourceOfPreviousValues, "LO", nil, false))
	item.Write(encodeElement(explicit, tagReasonForModification, "CS", pad("CS", reason), false))
	newItem := encodeItem(item.Bytes())

	// Replacement elements, by tag.
	repl := map[uint32][]byte{}
	for _, c := range real {
		repl[c.Tag] = encodeElement(explicit, c.Tag, c.VR, pad(c.VR, c.Value), false)
	}
	repl[TagInstanceCoercionDateTime] = encodeElement(explicit, TagInstanceCoercionDateTime, "DT", pad("DT", stamp), false)
	if s := find(tagOriginalAttributesSeq); s != nil {
		if s.undefined {
			// insert the new item before the sequence delimitation item
			var v bytes.Buffer
			v.Write(dataset[s.start:s.vend])
			v.Write(newItem)
			v.Write(dataset[s.vend:s.end])
			repl[tagOriginalAttributesSeq] = v.Bytes()
		} else {
			val := append(append([]byte(nil), dataset[s.vstart:s.vend]...), newItem...)
			vr := s.vr
			if vr == "" {
				vr = "SQ"
			}
			repl[tagOriginalAttributesSeq] = encodeElement(explicit, tagOriginalAttributesSeq, vr, val, false)
		}
	} else {
		repl[tagOriginalAttributesSeq] = encodeElement(explicit, tagOriginalAttributesSeq, "SQ", newItem, false)
	}

	targets := make([]uint32, 0, len(repl))
	for t := range repl {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })

	var b bytes.Buffer
	b.Grow(len(dataset) + 512)
	ti := 0
	for _, s := range spans {
		for ti < len(targets) && targets[ti] < s.tag {
			b.Write(repl[targets[ti]]) // insert a missing element in order
			ti++
		}
		if ti < len(targets) && targets[ti] == s.tag {
			b.Write(repl[targets[ti]])
			ti++
			continue
		}
		b.Write(dataset[s.start:s.end])
	}
	for ; ti < len(targets); ti++ {
		b.Write(repl[targets[ti]])
	}
	b.Write(dataset[rest:])
	return b.Bytes(), true, nil
}
