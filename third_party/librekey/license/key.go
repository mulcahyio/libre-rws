package license

import (
	"crypto/rand"
	"strings"
)

// Crockford base32 without I, L, O, U: easy to read and type.
const keyAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewKey returns a random human license key, e.g. LK-7Q2MF-K9XWD-3HTR8-VN4CP
// (100 random bits). The key identifies a license; it is not the proof of
// a valid license - the signed license file is.
func NewKey() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	var s strings.Builder
	s.WriteString("LK")
	for i, v := range b {
		if i%5 == 0 {
			s.WriteByte('-')
		}
		s.WriteByte(keyAlphabet[int(v)&31])
	}
	return s.String()
}

// NormalizeKey upper-cases a typed key and maps look-alike characters.
func NormalizeKey(k string) string {
	k = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(k), " ", ""))
	k = strings.TrimPrefix(strings.TrimPrefix(k, "LK"), "-")
	k = strings.NewReplacer("O", "0", "I", "1", "L", "1", "U", "V").Replace(k)
	return "LK-" + k
}

// NewID returns a random UUID (version 4) string.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	const hx = "0123456789abcdef"
	var s strings.Builder
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			s.WriteByte('-')
		}
		s.WriteByte(hx[v>>4])
		s.WriteByte(hx[v&15])
	}
	return s.String()
}
