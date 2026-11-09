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
| Licensing | LibreKey server | HTTPS (online) or files (offline) | Ed25519-signed licences | Lock-out behaviour is a safety hazard (HAZ-010) |
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
