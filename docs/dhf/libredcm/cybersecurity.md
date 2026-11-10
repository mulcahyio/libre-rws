# LibreDCM cybersecurity file

| | |
|---|---|
| Document | DHF-DCM-SEC |
| Status | **RETROSPECTIVE DRAFT** (2026-11-09). Seed for the threat model and security risk assessment |
| Basis | FD&C Act §524B (cyber device); FDA *Cybersecurity in Medical Devices: QMS Considerations and Content of Premarket Submissions* (Feb 2026); IEC 81001-5-1 (13-122); ANSI/AAMI SW96 (13-131); IHE ITI ATNA |

## 1. Why this applies

As part of the Libre RWS PACS, LibreDCM is software that connects to
networks, can reach the internet (S3, LibreKey activation, remote peers) and
could be vulnerable, so it meets the §524B "cyber device" definition. A 510(k)
must include a postmarket vulnerability plan with coordinated disclosure
(`SECURITY.md`), secure development and patching processes (`docs/qms/SDP.md`)
and an SBOM (`generated/sbom.cdx.json`).

## 2. System context and trust boundaries

| Interface | Peer | Protocol | Current protection | Gap |
|---|---|---|---|---|
| DICOM DIMSE :11112 | Modalities, viewers, other PACS | DICOM UL, optional TLS ≥1.2 (server cert) | Called-AE check, calling-AE allow-list (AE/IP/CIDR), audit log | No mutual TLS / peer certificate auth; TLS off by default in some installs |
| Admin :8081 | Operators, Prometheus, orchestrator | HTTP or HTTPS | Bearer token for /metrics, /version; HTTPS option | No user accounts/RBAC; token is shared; no rate limiting |
| Event bus | NATS JetStream | NATS (TLS capable) | Patient ID omission option | NATS TLS/auth not configured by installer |
| Index | SQLite file / PostgreSQL | local / PostgreSQL wire | — | PostgreSQL TLS and credentials guidance missing |
| Object store | S3 / compatible | HTTPS | Content-MD5 integrity | Encryption at rest is the store's job; document it |
| Licensing | LibreKey server | HTTPS (online) or files (offline) | Ed25519-signed licences; licensing cannot affect DICOM services (DCM-SRS-058) | Acknowledgement file is writable by anyone with container/data-volume access (audit-logged, DCM-SRS-061) |
| Install/update | Docker images, scripts | HTTPS pulls | — | Images and releases not signed; base image not pinned by digest |

## 3. Threat model (STRIDE, initial)

| ID | Threat | Asset | Current control | Planned control |
|---|---|---|---|---|
| TM-01 | Spoofing a modality AE to inject wrong-patient images | Archive integrity | Allow-list by AE@IP/CIDR | Mutual TLS (DCM-SRS-044) |
| TM-02 | Spoofing a viewer AE to pull studies | PHI | Allow-list, audit | Mutual TLS, per-peer permissions (store/query/retrieve) |
| TM-03 | Eavesdropping on DICOM traffic | PHI | Optional TLS | TLS on by default, installer enforces |
| TM-04 | Tampering with stored objects | Image integrity | S3 MD5 on upload | Stored checksum verified on retrieve; immutable storage option |
| TM-05 | Repudiation: accesses not traceable | Accountability | Structured audit log | ATNA syslog over TLS, NTP (DCM-SRS-045) |
| TM-06 | Info disclosure via admin API or logs | PHI, config | Token, patient-ID omission | RBAC, log redaction review |
| TM-07 | DoS: association floods, huge objects, slow-loris | Availability | — | Limits on associations, PDU/object size, timeouts; fuzzing of parsers |
| TM-08 | Malformed DICOM triggers parser crash or memory exhaustion | Availability, integrity | — | Go native fuzzing of Part 10 and C-FIND identifier parsing |
| TM-09 | Vulnerable dependency exploited | All | govulncheck (manual) | govulncheck in CI, SBOM per release, quarterly review |
| TM-10 | Supply-chain tampering of images/releases | All | — | Signed images (cosign), signed tags, SLSA provenance, pinned digests |
| TM-11 | Ransomware on the host encrypts the archive | Availability | Backup command | Documented backup/restore strategy, immutable/offline copies |

## 4. Baseline scan results (2026-11-09, branch `qms/foundation`)

`govulncheck` v1.9.0 against `./...` with Go 1.26.9:

| Finding | Component | Reachable from LibreDCM code? | Fix |
|---|---|---|---|
| GO-2026-6809 (crypto/x509: distrust compromised intermediate certificates) | Go standard library 1.26.9 | **Yes** (trace via blob storage I/O) | Build with Go **1.26.10** or later |
| GO-2026-6866, GO-2026-6860 | golang.org/x/net v0.60.0 | No (module required, vulnerable code not called) | Upgrade to v0.62.0 |
| GO-2026-6865 | github.com/klauspost/compress v1.20.0 | No | Upgrade to v1.20.2 |
| GO-2026-5932 | golang.org/x/crypto v0.57.0 | No | Upgrade when a tagged release containing the fix exists |

SBOM: CycloneDX 1.x, 49 components, `generated/sbom.cdx.json` (cyclonedx-gomod v1.12.0). One component without a detected licence: `github.com/tinylib/msgp` v1.6.4 (check by hand).

## 5. Required before a 510(k)

Security architecture views (global system, multi-patient harm, updatability,
security use cases); security risk assessment with scoring and transfer into
`risk-management.md`; traceability threat → control → test; SAST (gosec or
Semgrep); fuzzing; independent penetration test; customer security
documentation and MDS2 form; patch and end-of-support policy; postmarket
metrics (time to patch).

## 6. Static analysis (SAST), gosec v2.30.0 (added 2026-11-10)

CI runs gosec on every pull request. Libre RWS code (`modules/`, `platform/`)
is **blocking**: the build fails on any finding that is neither fixed nor
annotated `#nosec <rule> -- <reason>`. `third_party/` is scanned
report-only and the report is kept as a CI artifact.

Baseline run on branch `qms/retro-dcm` (2026-11-10), Libre RWS code, 24
files: 15 findings, all triaged below; result after triage 0 open, 13
annotated.

| Rule | Where | Verdict | Action |
|---|---|---|---|
| G115 int→uint16/uint32 | `dcm.Part10` / `writeMeta` | **Real defect (latent).** A File Meta value over 65,535 bytes would wrap the 16-bit length and produce an unreadable file. Not reachable through the filesystem store today (the file name would exceed the OS limit first, so the store fails), but reachable in principle with S3 keys or future callers | Fixed: Part10 refuses values that cannot be encoded (DCM-SRS-018, ANOM-001); remaining conversions annotated as bounded |
| G115 int→uint16 | `retrieve.go` `clamp16` | False positive: value clamped to 65,535 the line before | Annotated |
| G402 InsecureSkipVerify | `-echo` (TLS) and `-healthcheck` | Accepted: reachability check of an often self-signed install, and the container probe of its own loopback port. Neither carries data or makes a trust decision | Annotated |
| G304 file path from variable | config loader, licence ack, licence install, `blob.FS.Get` | Accepted: paths come from the administrator (flags, configuration). Blob keys pass `FS.path`, which rejects `..` and anchors under the root | Annotated |
| G301/G306 0755/0644 | licence acknowledgement file, `-gen-cert` certificate | Accepted: the acknowledgement holds a name, time and state; the certificate is public (its key is written 0600) | Annotated |
| G104 unhandled error | `index.backfillFold` `rows.Close()` | Cosmetic | Fixed (`_ =`) |

`third_party/gonetdicom` (fork, a LibreDCM software item) 22 findings:

| Rule | Count | Verdict |
|---|---|---|
| G115 decode side (`pdu/pdata.go`, `dimse/command.go`, uint64→int) | 4 | False positive: lengths are compared in uint64 against the buffer length before conversion (comments in the code explain why); fuzz targets cover these paths (see §7 once added) |
| G115 encode side (PDU/command/associate lengths, int→uint16/uint32) | 14 | Bounded: values are built locally and limited by the negotiated PDU size or by configuration (UIDs, AE titles, user identity). Track: add explicit length checks when the fork is next changed |
| G115 message ID (`ae/assoc.go`, uint32→uint16) | 1 | Intended wrap-around of the DIMSE message ID counter (zero skipped) |
| G118 goroutine with context.Background | 1 | N-EVENT-REPORT on a new association (`scp.go`); not used by LibreDCM |
| G705 XSS taint (`dicomweb/server.go`) | 3 | Not compiled into LibreDCM (the DICOMweb package is not imported) |

`third_party/librekey` (client copy) 7 findings: G204 subprocess with
variable arguments in `fingerprint` (fixed command names, arguments are not
user input), G304/G301/G306 for the licence and activation files (paths
from configuration; licence files are signed and not secret). Accepted; to
be annotated in the LibreKey repository and re-synced.
