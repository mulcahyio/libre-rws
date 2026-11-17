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

**2026-11-17, Hermes (pre-baseline, release-candidate evidence, card
WT-0.2/9):** the control API v1 driven end to end by Libre WT in the Libre
WT system test (`scripts/e2e/wt-system-test.sh`; record
[`docs/releases/librewt-v0.2.0-rc.1-verification/`](../../releases/librewt-v0.2.0-rc.1-verification/README.md)),
against two LibreDCM containers built from branch
`feat/librewt-0.2-release`: `dcm-1` (source build) and `dcm-2` (official
build, no licence). DCMTK `storescu` stored synthetic CT instances;
`storescp` was the C-MOVE destination. Finding on DICOM data: the data set
LibreDCM sends is byte-identical to the data set it received (SHA-256 of
everything after the File Meta Information, 6 of 6 instances in Explicit VR
Little Endian). DCMTK `storescu` drops Data Set Trailing Padding
(FFFC,FFFC) from a file it sends, as PS3.10 allows; the test data is
written without it.

- DCM-SRS-081: status of both nodes read by Libre WT (identity, readiness, licence state) (Libre WT system test SYS-10…15, 2026-11-17)
- DCM-SRS-082: running configuration with source, live/restart flag and the admin token redacted (SYS-31, -32, 2026-11-17)
- DCM-SRS-083: remote AEs with source `overlay` and the empty allow-list (SYS-33, -37, 2026-11-17)
- DCM-SRS-084: audit records naming the Libre WT user and reason of a send (SYS-91, 2026-11-17)
- DCM-SRS-085: recent log lines (SYS-93, 2026-11-17)
- DCM-SRS-087: C-ECHO to configured destination `STORESCP` and to the free address of `dcm-2` (SYS-45, -47, 2026-11-17)
- DCM-SRS-088: reload of `dcm-1`; reload of the unlicensed `dcm-2` refused until the notice was acknowledged, then done (SYS-41, -72, -75, 2026-11-17)
- DCM-SRS-089: licence status of both builds; acknowledgement by the named operator; activation request file (SYS-70…76, 2026-11-17)
- DCM-SRS-090: online backup; the SHA-256 reported equals the SHA-256 of the file in the backup directory (SYS-81, -82, 2026-11-17)
- DCM-SRS-091: study, series and instance queries; a held conflict instance (rejected) not returned (SYS-52…55, 2026-11-17)
- DCM-SRS-092: send of a study (4 instances) and of one series (2 instances) to DCMTK `storescp`; plan refused for another Patient ID; received data sets byte-identical (SYS-57…63, 2026-11-17)
- DCM-SRS-093: destination added and allow-list restricted through plan and apply; the restricted allow-list refuses calling AE `INTRUDER` (SYS-34…37, -77a, -77b, 2026-11-17)
- DCM-SRS-094: rollback to the previous overlay version (SYS-38…40, 2026-11-17)
