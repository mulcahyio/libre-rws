# Libre WT 0.2.0-rc.1: verification record

**DRAFT for approval.** Written by Hermes (Claude), 2026-11-17. Release
candidate only: no tag, no images published. Synthetic test data only; no
real patient data. Release notes: [`../librewt-v0.2.0-rc.1.md`](../librewt-v0.2.0-rc.1.md).

## What was verified

| Item | Value |
|---|---|
| Commit | `a493f976d8d73e17bd174d5546e6d32cf96f3d35` (branch `feat/librewt-0.2-release`). Later commits on the branch change documents only. |
| Go | go1.26.11 linux/amd64 (`golang:1.26-bookworm` through `rws-go`) |
| Images | `libre/dcm:wt-system` (source build), `libre/dcm:wt-system-official` (official build, unlicensed), `libre/wt:wt-system`, `rws-e2e-dcmtk` (DCMTK `storescu`/`storescp`). Digests in [`system-test/environment.txt`](system-test/environment.txt). |
| Stack | `deploy/compose/docker-compose.wt-system.yml`: two LibreDCM nodes (`dcm-1` source, `dcm-2` official), Libre WT, DCMTK, on one Docker network |
| Test | `scripts/e2e/wt-system-test.sh OUTDIR` (driver `wt-system-test.py`, data `wt_make_data.py`, comparison `wt_compare.py`); tool validation in `docs/qms/tool-validation/README.md` |

## Results

| Check | Result | Record |
|---|---|---|
| System test | **87 checks, 87 passed, 0 failed** | [`system-test/system-test.log`](system-test/system-test.log) |
| Byte-identical send | Study (4 instances) and one series (2 instances) received by DCMTK `storescp`; every data set SHA-256 equals the stored one. Control: one flipped pixel byte is reported as DIFFERENT (CTL-1) | [`system-test/compare-study.jsonl`](system-test/compare-study.jsonl), [`system-test/manifest.json`](system-test/manifest.json) |
| Admin token | Not in any of 97 fetched pages nor in the Libre WT log (SYS-99) | [`system-test/pages/`](system-test/pages/) |
| Screenshots | 70 (35 pages, light and dark), 0 failed, 0 wider than 1280 px; checked by eye on contact sheets and at full size (send warning page, configuration page) | [`screenshots.log`](screenshots.log), `docs/librewt/images/` |
| `fmt`, `vet`, `vet -tags official` | rc=0, no output | [`gate/`](gate/summary.txt) |
| `test -race -count=1 ./...` | 23 packages ok, 0 FAIL | [`gate/test-race.log`](gate/test-race.log) |
| `test -count=1 -tags official ./modules/...` | 21 packages ok, 0 FAIL | [`gate/test-official.log`](gate/test-official.log) |
| gosec v2.30.0 | 75 files, 21246 lines, 0 issues | [`gate/gosec.log`](gate/gosec.log) |
| govulncheck v1.9.0 | 0 vulnerabilities called | [`gate/govulncheck.log`](gate/govulncheck.log) |
| Trace / needs / QMS tool tests | LibreDCM 71 (67 active, 67 verified); Libre WT 37/37/37; LibreDVT 34/34/34; 0 untraced requirements; 13 tool tests OK | [`gate/qms-scripts.log`](gate/qms-scripts.log) |

## Libre WT functions exercised against real LibreDCM

| Function | Checks |
|---|---|
| First run, login, roles, CSRF, password change, logout | SYS-01…08, SYS-97, SYS-98 |
| Fleet, dashboard, status, about, plain-HTTP warning | SYS-10…16 |
| Store (DCMTK `storescu`), Study Exceptions with warning page and single-use approval | DATA-1…3, SYS-20…26 |
| Configuration (redacted), destinations edit, rollback, reload | SYS-30…42 |
| C-ECHO test | SYS-45…47 |
| Study search, study and series pages, send with wrong-patient guard | SYS-50…63, CTL-1 |
| Licence status, reload blocked until acknowledgement, acknowledgement, activation request download; DICOM keeps running unlicensed | SYS-70…76 (SYS-71b: C-STORE to the unlicensed official node) |
| Allow-list change on the unlicensed node: calling AE INTRUDER refused, MODALITY1 still stores | SYS-77a, SYS-77b, SYS-77 |
| Index backup with SHA-256 checked on the node | SYS-80…83 |
| LibreDCM audit records, logs, CSV export, Libre WT audit log | SYS-90…96 |

## Not covered here

- HTTPS between Libre WT and LibreDCM, and for the console.
- Installer scripts (PowerShell not run); INSTALL.md steps checked on Linux only.
- A partial or failed send against a real destination (covered by unit tests only).
- The system test does not run in GitHub CI (D59).
