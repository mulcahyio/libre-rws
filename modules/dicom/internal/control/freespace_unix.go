//go:build unix

package control

import "syscall"

// freeBytes returns the space in dir that an unprivileged process may use.
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	if st.Bsize <= 0 {
		return 0, syscall.EINVAL
	}
	return st.Bavail * uint64(st.Bsize), nil // #nosec G115 -- Bsize checked positive above
}
