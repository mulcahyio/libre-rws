package engine

import (
	"fmt"
	"regexp"
	"strconv"
)

// LibreDVT's DICOM identification under the Libre Systems UID root
// (docs/qms/uid-registry.md): product arc 1.3.6.1.4.1.67686.1.3, Implementation
// Class UID 1.3.6.1.4.1.67686.1.3.1.<major>.<minor>, Implementation Version
// Name LIBREDVT_<major>_<minor> (DVT-SRS-082). This is LibreDVT's own copy of
// the rule: it must not share code with LibreDCM (DVT-SRS-080).
const implementationArc = "1.3.6.1.4.1.67686.1.3.1"

var dvtVersionRE = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:[.+-].*)?$`)

// Implementation returns the Implementation Class UID and Version Name for a
// LibreDVT build version. A version that is not MAJOR.MINOR[...] ("dev",
// "ci", "test") or has a part above 999 identifies as 0.0, so the Version
// Name always fits VR SH (16 characters).
func Implementation(version string) (classUID, versionName string) {
	major, minor := 0, 0
	if m := dvtVersionRE.FindStringSubmatch(version); m != nil {
		a, err1 := strconv.Atoi(m[1])
		b, err2 := strconv.Atoi(m[2])
		if err1 == nil && err2 == nil && a <= 999 && b <= 999 {
			major, minor = a, b
		}
	}
	return fmt.Sprintf("%s.%d.%d", implementationArc, major, minor), fmt.Sprintf("LIBREDVT_%d_%d", major, minor)
}

// peerArgs is the worker argument vector for a network command: the peer
// and LibreDVT's own identification, which the worker sends in every
// association and writes in every Part 10 file it receives.
func (c Config) peerArgs(p Peer, cmd string) []string {
	uid, name := Implementation(c.Version)
	return append(append(p.args(cmd), "--impl-uid", uid, "--impl-version", name), p.TLS.args()...)
}
