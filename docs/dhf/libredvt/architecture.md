# LibreDVT architecture

| | |
|---|---|
| Document | DHF-DVT-ARCH |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Related | `SRS.md`, `risk-management.md`, `cybersecurity.md`; research: vault note *LibreDCMTK Research* |

## 1. Purpose

LibreDVT (DICOM Validation & Test) validates DICOM objects and tests DICOM
services through a web interface and a command line. It learns from DVTk
(emulators, message-level checks, scripted tests, results manager), dicom3tools
(IOD and cross-object validation) and Gazelle EVS (several engines behind one
web upload). It runs as its own container, separate from LibreDCM and Libre WT
(Patrick Mulcahy, 2026-11-10), and treats the system under test as a black box
reached only over DICOM.

## 2. Independence from the system under test

LibreDCM uses godicom and a fork of gonetdicom. LibreDVT must not (DVT-SRS-080):
a bug in a shared library could hide in both the product and its test. All
DICOM parsing and networking in LibreDVT is done by other implementations:

| Job | Engine | Language | Licence |
|---|---|---|---|
| IOD, encoding, VR/VM validation | dicom3tools `dciodvfy` | C++ | BSD (wheel: Apache-2.0 packaging) |
| Cross-file consistency | dicom3tools `dcentvfy` | C++ | BSD |
| IOD validation from the standard's DocBook source | pydicom `dicom-validator` | Python | MIT |
| File reading, pixel comparison | pydicom | Python | MIT |
| DIMSE (SCU and built-in Storage SCP) | pynetdicom | Python | MIT |

The Go code never decodes DICOM; it orchestrates, stores results and serves
the UI. An architecture test enforces that no `modules/dvt` package imports
godicom or gonetdicom.

## 3. Components

```
 browser ──HTTPS/HTTP──► libredvt serve (Go)  ◄── libredvt run plan.yaml (Go CLI, CI)
                            │  web UI, auth, targets, plans, runs, reports
                            │  internal/engine ── exec ──► dciodvfy, dcentvfy
                            │                  └─ exec ──► libredvt_worker.py (Python, JSON on stdout)
                            ▼                                  ├─ dicom-validator
                     /var/lib/libredvt                         ├─ pydicom (probe, compare)
                     (targets, uploads, results)               └─ pynetdicom (echo/store/find/move/reject, Storage SCP)
                                                                        │ DICOM
                                                                        ▼
                                                               system under test (e.g. LibreDCM)
```

Go packages under `modules/dvt/`:

| Package | Responsibility |
|---|---|
| `cmd/libredvt` | `serve`, `run`, `validate`, `version` subcommands |
| `internal/result` | Result model (findings, step and run verdicts, versions); verdict rules (DVT-SRS-009, -042) |
| `internal/engine` | Runs external engines with timeouts; parsers for `dciodvfy`/`dcentvfy` output; client for the Python worker; "engine error" on any failure (DVT-SRS-007) |
| `internal/plan` | Strict YAML test-plan parsing (DVT-SRS-040) |
| `internal/runner` | Executes plans and validations; production guard (DVT-SRS-028) |
| `internal/report` | JUnit XML and self-contained HTML (DVT-SRS-043, -044) |
| `internal/store` | Targets, uploads and results in the data directory (DVT-SRS-020, -045) |
| `internal/web` | HTTP handlers, sessions, CSRF, login rate limit, embedded templates and CSS (DVT-SRS-060…-062, -066) |

Python worker `modules/dvt/worker/libredvt_worker.py`: one process per
request, arguments on the command line, one JSON document on stdout, non-zero
exit only for worker failures (a failed DICOM check is a normal JSON result).
Commands: `versions`, `probe`, `iod`, `echo`, `store`, `find`, `move`,
`reject`. The worker never decides a verdict; Go applies the rules, so the
pass/fail logic lives in one tested place.

## 4. Test plans

```yaml
name: LibreDCM conformance
target: libredcm-lab          # name in the target registry
steps:
  - id: echo
    type: echo
    verifies: [DCM-SRS-001]
  - id: store
    type: store
    files: [fixtures/*.dcm]
    verifies: [DCM-SRS-002, DCM-SRS-003]
  - id: move
    type: move
    studies: from-store       # the studies sent by earlier store steps
    compare_pixels: true
    verifies: [DCM-SRS-010]
```

Unknown keys are errors (YAML decoder with known-fields checking). The plan's
SHA-256 goes into every report.

## 5. Deployment

- Image `libre/dvt`, built by `deploy/docker/Dockerfile.dvt`: Go build stage
  (pinned `golang` image), runtime on a pinned `python:3.12-slim-bookworm`
  with every Python package pinned, indirect ones included, installed with
  `pip install --no-deps` and checked with `pip check`
  (`modules/dvt/worker/requirements.txt`). The
  DICOM standard edition for `dicom-validator` is downloaded at build time
  and stored in the image (DVT-SRS-064). Runs as uid 10001 (DVT-SRS-065).
- A `test` build target runs the engine integration tests
  (`go test -tags engines ./modules/dvt/...`) inside an image with the real
  engines, so CI exercises the actual binaries.
- Compose file `deploy/compose/docker-compose.dvt.yml`: web port 8095 and
  Storage SCP port 11115 published on 127.0.0.1 (DVT-SRS-063), data volume
  `libredvt-data`, admin password from a Docker secret or env.

## 6. SOUP (tool configuration items)

LibreDVT is a QMS tool, so these are recorded as tool configuration items
(QMS-SOP-011, IEC 62304 §8.1.2), not as product SOUP.

| Item | Version | Purpose | Licence |
|---|---|---|---|
| python:3.12-slim-bookworm | pinned by digest in `Dockerfile.dvt` | runtime base | PSF / Debian |
| golang | as LibreDCM | build | BSD-3 |
| dicom3tools (PyPI wheel) | 20260927 (dicom3tools 1.00.snapshot.20260927113651) | `dciodvfy`, `dcentvfy` | BSD-style; wheel Apache-2.0 |
| dicom-validator | 0.9.0 | IOD validation | MIT |
| DICOM standard (DocBook, parts 3, 4, 6) | 2026d | rule source for dicom-validator | NEMA, free use |
| pydicom | 3.0.2 | read/compare DICOM | MIT |
| pynetdicom | 3.0.4 | DIMSE SCU/SCP | MIT |
| lxml | 6.1.3 | indirect, dicom-validator (DocBook parsing at build time) | BSD-3 |
| pyparsing | 3.3.3 | indirect, dicom-validator | MIT |
| go.yaml.in/yaml/v3 | already a libre-rws dependency | plan parsing | MIT/Apache-2.0 |

All are compatible with LibreDVT's Apache-2.0 licence. DVTk (LGPL) and
Wireshark (GPL) are not used in v0.1.
