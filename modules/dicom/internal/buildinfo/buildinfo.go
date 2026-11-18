// Package buildinfo holds LibreDCM's build identification. It is the one
// source of truth for the version: `-version`, `/version`, the licence check,
// the control API and the DICOM Implementation Class UID and Version Name
// (DCM-SRS-057, DCM-SRS-098) all read it.
//
// Both values are set at build time:
//
//	go build -ldflags "-X github.com/mulcahyio/libre-rws/modules/dicom/internal/buildinfo.Version=0.2.0 \
//	  -X github.com/mulcahyio/libre-rws/modules/dicom/internal/buildinfo.BuildDate=2026-11-18" ./modules/dicom/cmd/libredcm
package buildinfo

// Version is the release version without the leading "v" (e.g.
// "0.2.0-beta.1"). Builds without one report "dev".
var Version = "dev"

// BuildDate is the release date, YYYY-MM-DD, checked against a licence's
// update period. Empty for development builds.
var BuildDate = ""
