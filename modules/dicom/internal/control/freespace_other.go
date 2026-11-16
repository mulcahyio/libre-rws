//go:build !unix

package control

import "errors"

// freeBytes is not available on this platform. The backup endpoint then
// refuses to start (it never writes without a free-space check).
func freeBytes(string) (uint64, error) {
	return 0, errors.New("free space check is not supported on this platform")
}
