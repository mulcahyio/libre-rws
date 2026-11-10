# LibreDCM Software Requirements Specification (SRS)

| | |
|---|---|
| Document | DHF-DCM-SRS |
| Status | **RETROSPECTIVE DRAFT** (2026-11-09). Written from the implemented and documented behaviour of branch `dev/0.2.0` (CHANGELOG, DEVELOPMENT.md, INSTALL.md, tests). Must be reviewed against the code and approved at the QMS baseline |
| Software item | LibreDCM (DICOM archive component of the Libre RWS PACS) |
| Safety class | B (proposed; see risk-management.md) |

Rules: IDs are permanent. Never renumber or reuse an ID; set Status to
`Retired` instead. Status is `Active` (must be verified), `Draft` (not yet
agreed) or `Retired`. Risk-control requirements name the hazard they control.
Tests reference IDs with `// Verifies: DCM-SRS-###`; `scripts/qms/trace.py`
builds the trace matrix.

## 1. DICOM network services

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| DCM-SRS-001 | LibreDCM shall act as a Verification SCP (C-ECHO). | Active | |
| DCM-SRS-002 | LibreDCM shall act as a Storage SCP for the standard storage SOP classes, and shall return a success status only after the instance is durably stored and indexed. | Active | HAZ-001 |
| DCM-SRS-003 | When a sender offers compressed transfer syntaxes (JPEG Baseline, JPEG-LS, JPEG 2000, RLE), LibreDCM shall store the instance in the sender's encoding, without transcoding the pixel data. | Active | HAZ-003 |
| DCM-SRS-004 | LibreDCM shall store instances as DICOM Part 10 files and preserve all received attributes, including multi-valued and raw-encoded values. | Active | HAZ-003 |
| DCM-SRS-005 | Every storage backend (filesystem, S3) shall satisfy the same store contract (put, get, exists, delete, overwrite rules), and S3 uploads shall carry an integrity checksum so a corrupted upload is rejected. | Active | HAZ-001, HAZ-003 |
| DCM-SRS-006 | LibreDCM shall act as a Query SCP (C-FIND, Study Root) at patient, study, series and image level, supporting universal, single-value, wildcard, date-range and UID-list matching. | Active | HAZ-002 |
| DCM-SRS-007 | PatientName matching shall be case-insensitive, including non-ASCII letters, unless configured to be case-sensitive. | Active | HAZ-002 |
| DCM-SRS-008 | StudyTime shall match single times and ranges (open or closed), where a partial bound covers its whole period. | Active | HAZ-002 |
| DCM-SRS-009 | C-FIND responses shall declare a Specific Character Set that matches the encoding of the returned text (ISO_IR 192 when non-ASCII text is returned). | Active | HAZ-002 |
| DCM-SRS-010 | LibreDCM shall act as a Move SCP (C-MOVE) to configured destinations, sending each instance individually and in its stored encoding when the destination accepts it. | Active | HAZ-003, HAZ-004 |
| DCM-SRS-011 | A C-MOVE to a destination that is not configured shall fail with status "Move Destination Unknown" and send nothing. | Active | HAZ-005 |
| DCM-SRS-012 | LibreDCM shall act as a Get SCP (C-GET), returning matching instances on the same association. | Active | HAZ-004 |
| DCM-SRS-013 | Remote destinations shall be configured as `NAME=HOST:PORT[:AE]` entries; invalid entries shall be reported and not used. | Active | HAZ-005 |
| DCM-SRS-014 | Associations whose called AE title does not match LibreDCM's AE title shall be rejected. | Active | HAZ-007 |
| DCM-SRS-015 | When a calling-AE allow-list (`AE`, `AE@IP`, `AE@CIDR`) is configured, associations from other peers shall be rejected; an invalid list shall reject all peers rather than allow all. | Active | HAZ-007 |
| DCM-SRS-016 | On SIGHUP, LibreDCM shall reload remote destinations and the allow-list without dropping open associations; a configuration that fails to load shall leave the running settings unchanged. | Active | HAZ-005, HAZ-007 |
| DCM-SRS-017 | LibreDCM shall record the calling AE title of the sender with each stored instance. | Active | HAZ-008 |
| DCM-SRS-018 | LibreDCM shall refuse to archive an instance (failure status, nothing written) when its SOP Class UID, SOP Instance UID or transfer syntax UID is empty, or when any File Meta value is too long to encode (over 65,534 bytes), instead of writing a Part 10 file that cannot be read back. Values longer than PS3.5 allows but still encodable are archived as received. Added 2026-11-10 (gosec G115 finding, ANOM-001). | Active | HAZ-003 |

## 2. Index and data management

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| DCM-SRS-020 | The SQLite and PostgreSQL index implementations shall return identical results for the same queries and operations. | Active | HAZ-002 |
| DCM-SRS-021 | The index schema shall upgrade in place from every earlier released schema version without data loss, and cluster nodes shall not run migrations concurrently. | Active | HAZ-001 |
| DCM-SRS-022 | `-backup PATH` shall write a consistent snapshot of the index while LibreDCM keeps running, and shall refuse an empty destination. | Active | HAZ-001 |
| DCM-SRS-023 | Receiving the same SOP Instance UID again shall update the stored instance idempotently, without creating duplicate index entries. | Draft | HAZ-002 |

## 3. Events and integration

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| DCM-SRS-030 | Each stored instance shall produce exactly one `instance.stored` event, committed with the instance (outbox) and published with retries, so an event bus outage delays events but never loses them. | Active | HAZ-006 |
| DCM-SRS-031 | Events shall also be published under the legacy `rws.dicom.*` subjects unless this is turned off. | Active | |
| DCM-SRS-032 | Patient IDs shall be left out of events and audit records when configured. | Active | HAZ-009 |

## 4. Security, audit and administration

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| DCM-SRS-040 | LibreDCM shall write structured audit records for received instances, queries, exports (C-MOVE/C-GET, with destination, studies and patient IDs) and rejected associations, named after the DICOM PS3.15 A.5 audit message types. | Active | HAZ-008, HAZ-009 |
| DCM-SRS-041 | The admin endpoint shall serve `/healthz`, `/readyz`, `/metrics` and `/version`. When an admin token is configured, `/metrics` and `/version` shall require it, `/readyz` shall hide check details without it, and an unreadable token file shall cause requests to be refused. | Active | HAZ-009 |
| DCM-SRS-042 | The admin endpoint shall use HTTPS when a certificate is configured; `-gen-cert` shall create a self-signed certificate and not overwrite an existing one unless forced. | Active | HAZ-009 |
| DCM-SRS-043 | When a certificate is configured, the DICOM listener shall accept only TLS connections, with TLS 1.2 or later. | Active | HAZ-009 |
| DCM-SRS-044 | The DICOM listener shall support mutual TLS (client certificate required, configurable trusted CAs) per IHE ATNA ITI-19. | Draft | HAZ-007, HAZ-009 |
| DCM-SRS-045 | Audit records shall be sent as RFC 5424 syslog over TLS (RFC 5425) in DICOM PS3.15 A.5 format (IHE ATNA ITI-20). | Draft | HAZ-008 |

## 5. Configuration, licensing and operation

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| DCM-SRS-050 | Configuration shall come from a YAML file with environment-variable overrides; an environment variable that is set but empty shall not override the file. | Active | HAZ-005 |
| DCM-SRS-051 | Legacy `RWS_*` setting names shall still be read, with a warning naming the replacement. | Active | |
| DCM-SRS-052 | ~~Official builds refuse C-STORE after licence expiry plus 30-day grace.~~ Replaced by DCM-SRS-058…061 (issue #5, 2026-11-09): refusing storage was itself a hazard (HAZ-010). | Retired | |
| DCM-SRS-053 | LibreDCM shall shut down gracefully, finishing or cleanly aborting open associations so that no success status is sent for an instance that was not stored. | Active | HAZ-001 |
| DCM-SRS-054 | LibreDCM shall expose Prometheus metrics for associations, rejected associations, stored instances, pending outbox events and licence state. | Active | HAZ-006 |
| DCM-SRS-055 | In a cluster, any node shall serve queries and retrievals for instances stored by any other node, and the load balancer configuration shall retry new associations on another node when one fails. | Active | HAZ-004 |
| DCM-SRS-056 | The workstation package shall install, verify (incl. a real C-ECHO), upgrade (backing up the index first, keeping data), back up, report status and uninstall LibreDCM on Linux, macOS and Windows. | Active | HAZ-001 |
| DCM-SRS-057 | `-version` and the admin `/version` endpoint shall report the exact released version (and, after clearance, the UDI). | Active | |
| DCM-SRS-058 | No licence state (valid, grace, expired, missing, invalid, wrong machine, not covered) shall refuse, delay or interrupt any DICOM association or service (C-ECHO, C-STORE, C-FIND, C-MOVE, C-GET). The DIMSE gateway shall not depend on licensing code. | Active | HAZ-010 |
| DCM-SRS-059 | During the grace period, LibreDCM shall log a warning when the state is entered and at least every 24 hours. | Active | HAZ-010 |
| DCM-SRS-060 | When unlicensed and not acknowledged within the last 24 hours, LibreDCM shall log an error when the state is entered and every hour, set `libre_dcm_license_ack_required` to 1, report the licence state in `/version`, and refuse configuration reloads while keeping the running settings. | Active | HAZ-010 |
| DCM-SRS-061 | `LibreDCM -license-ack NAME` (and the workstation scripts' `license-ack NAME`) shall record the administrator's name, time and licence state; the running node shall write an audit record for it, and the acknowledgement shall lift the requirements of DCM-SRS-060 for 24 hours. An acknowledgement without a name shall be rejected. | Active | HAZ-008, HAZ-010 |

## 6. Patient association integrity

Added 2026-11-10 (issue #14, decisions by Patrick Mulcahy recorded there).
Research: vault `Projects/Libre RWS/Patient ID Conflict Handling Research.md`.

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| DCM-SRS-070 | On C-STORE, LibreDCM shall detect a conflict when the Study Instance UID is indexed under a different Patient ID, the Series Instance UID is indexed under a different study, or the SOP Instance UID is indexed under a different study or series. A conflicting instance shall never change the patient, study or series association of indexed instances, also when several nodes store at the same time. | Active | HAZ-013 |
| DCM-SRS-071 | The conflict policy shall be configurable: `quarantine` (default) stores and holds the instance; `reject` refuses it with C-STORE status A778 and stores nothing; `accept_separate` stores it as a separate patient/study entry. An unknown value shall act as `quarantine`. | Active | HAZ-013 |
| DCM-SRS-072 | A held or accepted conflicting instance shall be stored under its own storage key (never overwriting an archived file) and recorded with its conflict: kind, study, indexed and incoming Patient IDs, source AE, times and instances. Instances of one study arriving with the same conflicting Patient ID shall be grouped into one conflict. | Active | HAZ-013, HAZ-001 |
| DCM-SRS-073 | With visibility `hidden` (default), held instances shall not be returned by C-FIND, C-MOVE or C-GET. With visibility `flagged`, and always for accepted conflicts, they shall be returned by C-FIND as a separate entry under their stored Patient ID, and by C-MOVE/C-GET only when the request names that Patient ID; a retrieve without it shall return only the indexed instances. | Active | HAZ-013, HAZ-002 |
| DCM-SRS-074 | Every conflict shall produce an audit record (`PatientRecord.conflict-detected`), a warning log entry and the metrics `libre_dcm_patient_conflicts_total` (by kind and policy) and `libre_dcm_patient_conflicts_open`. | Active | HAZ-013, HAZ-008 |
| DCM-SRS-075 | When `patient_conflict_coerce_patient_id` is on, a held or accepted instance of a Patient ID conflict shall be stored with Patient ID `B000_` + received Patient ID, with an Original Attributes Sequence item (reason COERCE, previous Patient ID, modifying system, date/time) and Instance Coercion DateTime, all other bytes unchanged, and C-STORE shall answer Warning B000. When the result would exceed 64 characters or the encoding is not supported, the instance shall be stored uncoerced with a warning. | Active | HAZ-013, HAZ-003 |
| DCM-SRS-076 | For an indexed study, instances with the same Patient ID but a different patient name, birth date or sex shall be stored; the first-registered values shall be kept in the index, and a warning and the metric `libre_dcm_patient_demographics_mismatch_total` shall record it. | Active | HAZ-013 |
| DCM-SRS-077 | LibreDCM shall offer a REST control API on the admin listener (`/control/v1/conflicts`: list, detail, plan, resolve) that refuses every request unless an admin token is configured and presented. Resolve shall require an action, an operator name, a reason and an explicit confirmation, shall act only on unresolved conflicts, and shall write an audit record (`PatientRecord.conflict-resolved`) with operator, action and reason. Plan shall describe the change without making it. | Active | HAZ-013, HAZ-008, HAZ-009 |
| DCM-SRS-078 | For Patient ID conflicts, `correct_incoming` shall set the held instances' patient attributes to the study's indexed values and index them in the study; `correct_existing` shall set every archived instance of the study and the study row to the incoming patient's values, then index the held instances. Each rewritten file shall get an Original Attributes Sequence item (reason CORRECT) with pixel data and other elements unchanged. A correction shall be resumable after a failure. | Active | HAZ-013, HAZ-014 |
| DCM-SRS-079 | `reject` shall close the conflict and keep its instances archived but never returned by query or retrieve. | Active | HAZ-013 |
