//go:build !official

package licensing

// Source builds (no "official" build tag) carry no license key and run
// without a license, by design: LibreDCM is open source; the license covers
// the official builds and support.
var officialKeys []byte
