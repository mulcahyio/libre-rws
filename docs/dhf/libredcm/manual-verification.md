# LibreDCM manual and system-level verification

Requirements verified outside `go test`. Format: `- <ID>: <how>`. Read by
`scripts/qms/trace.py`.

**Status:** the runs listed below were done **before the QMS existed**
(2026-10-11 to 2026-11-07, recorded in the vault note *Libre DCM* and in
CHANGELOG). They are pre-QMS evidence only and must be re-run against the
baseline build and written up in a signed system test report.

- DCM-SRS-003: scripts/conformance.sh (DCMTK 3.6.7) C-STORE/C-MOVE of JPEG Baseline, JPEG-LS, RLE, JPEG 2000; compressed pixel data byte-identical (pre-QMS run 2026-11-07)
- DCM-SRS-014: DCMTK echoscu with a wrong called AE title, rejected "Called AE Title Not Recognized" (pre-QMS run 2026-10-11)
- DCM-SRS-055: 3-node cluster (PostgreSQL + NATS + HAProxy) conformance and node-failure test (pre-QMS run 2026-11-07)
- DCM-SRS-056: workstation installer end-to-end on Linux (2026-10-11, 2026-11-07) and Windows upgrade on Surface Pro 7 (2026-11-07); macOS not tested
- DCM-SRS-043: Windows (Surface Pro 7) self-signed and own-file TLS: DICOM TLS store/find worked and plain DICOM was refused (pre-QMS run 2026-11-07, CHANGELOG "Tested (2026-11-07)")
