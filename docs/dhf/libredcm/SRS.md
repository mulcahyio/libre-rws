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

## 7. Control API v1 extension for Libre WT 0.2

Added 2026-11-14 as **Draft** (issue #20; scope approved by Patrick Mulcahy
on 2026-11-14: "Build Libre WT to support all LibreDCM functions developed to
date"). The interface contract is `control-api-v1.md`. Each row becomes
`Active` in the pull request that implements and verifies it
(DCM-SRS-080…085: card WT-0.2/2; DCM-SRS-086…090 and -097: card WT-0.2/3;
both issue #20). Hazards HAZ-015 and HAZ-016 are in `risk-management.md` §2a.

Wording made exact when the rows became `Active` (card WT-0.2/3; the IDs and
their meaning are unchanged): -086 names the endpoints it covers and the two
audit records; -087 adds that the answer carries no error text of the peer;
-088 adds the confirmation and the plan digest; -089 adds the source build;
-090 adds the case that free space cannot be read.

Two rows are partly implemented and stay `Draft` until card WT-0.2/4
completes them: -095 (the warnings and their acknowledgement work for
reload; the configuration edit and the "recently active peer" warning are
not built yet) and -096 (the limits and timeouts of the actions built so
far are verified; the send limit is not built yet).

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| DCM-SRS-080 | Every `/control/v1` endpoint shall refuse requests unless an admin token is configured and presented. Request bodies shall be JSON with unknown fields refused and a size limit; responses shall carry `Cache-Control: no-store`; `GET` requests shall never change state; error texts shall contain no secret. `GET /control/v1` shall list the features this node supports. | Active | HAZ-009, HAZ-015 |
| DCM-SRS-081 | `GET /control/v1/status` shall report the node's identity (node ID, AE title, version), time, uptime, backends, readiness with the result of each check, licence state and the live counters for associations, rejected associations, stored instances by result, queries, retrieve sub-operations, pending outbox events, event publish failures and open patient conflicts. Check errors shall not contain credentials. | Active | HAZ-004, HAZ-006 |
| DCM-SRS-082 | `GET /control/v1/config` shall return the running configuration with, for each setting, its source (default, file, overlay, environment) and whether it can be changed without a restart. Secrets (admin token, S3 access and secret keys, user and password in the index DSN and the event bus URL, TLS private keys, licence keys and licence file content) shall never be returned by any control endpoint. A setting that is not on the list of safe settings shall be returned as redacted. | Active | HAZ-009 |
| DCM-SRS-083 | `GET /control/v1/remote-aes` and `GET /control/v1/allow-list` shall return the C-MOVE destinations and the calling-AE allow-list that are in force now, each entry with its source, and shall say whether the allow-list is enforced. | Active | HAZ-005, HAZ-007 |
| DCM-SRS-084 | `GET /control/v1/audit` shall return recent audit records from a fixed-size in-memory buffer, filtered by event type, outcome and time and limited in number. The records shall hold no more patient data than the audit log and shall honour the patient-ID omission setting. | Active | HAZ-008, HAZ-009 |
| DCM-SRS-085 | `GET /control/v1/logs` shall return recent log lines from a fixed-size in-memory buffer, filtered by minimum level and time and limited in number, without audit records. | Active | HAZ-004, HAZ-009 |
| DCM-SRS-086 | Every action endpoint of the control API v1 extension (`control-api-v1.md` §4 and §5; the conflict resolve endpoint keeps the rules of DCM-SRS-077) shall carry an operator name, a reason and a request ID, and shall be refused without them. Each such request shall write an audit record with operator, reason and request ID before the action starts and a second record with the outcome; a refused request shall write a refusal record. When the record before the action cannot be written, the action shall not be done. A request ID repeated within 10 minutes shall return the first result without acting again, and a request ID used for a different request shall be refused. | Active | HAZ-008, HAZ-015 |
| DCM-SRS-087 | `POST /control/v1/echo` shall send a DICOM C-ECHO to a configured remote AE given by name, or to a given host, port and AE title, within a timeout, and report success or the stage that failed and the duration. An unknown remote AE name shall be refused. No patient data shall be sent. The answer shall carry no error text from the peer or the operating system. | Active | HAZ-004, HAZ-005 |
| DCM-SRS-088 | `POST /control/v1/reload` shall reload the configuration through the same path as SIGHUP, including the licence acknowledgement block, and report what changed. Its plan shall show the difference between the running settings and the settings a reload would load. A configuration that fails to load or validate shall be reported and shall leave the running settings unchanged. The reload shall act only when the request carries an explicit confirmation and the digest of an unchanged plan. | Active | HAZ-005, HAZ-007, HAZ-015 |
| DCM-SRS-089 | The control API shall offer the licence functions: status, acknowledgement by the named operator, activation request, licence file installation and online activation, using the existing licensing code. An invalid licence shall be refused and the installed licence kept. Licence keys shall not be logged, audited or returned in full. No licence endpoint shall be able to refuse, delay or interrupt DICOM services. A build that needs no licence shall say so and refuse the licence actions. | Active | HAZ-010, HAZ-008 |
| DCM-SRS-090 | `POST /control/v1/backup` shall write a consistent snapshot of the SQLite index into the directory configured on the node, under a file name made by LibreDCM, and report file name, size and SHA-256. It shall accept no path from the client, never overwrite an existing file, refuse when the directory is not configured, when free space is too low or cannot be determined, or when another backup is running, and `GET /control/v1/backups` shall list the snapshots. No endpoint shall download or delete a snapshot. | Active | HAZ-001, HAZ-004, HAZ-009 |
| DCM-SRS-091 | `GET /control/v1/studies` shall query the index at patient, study, series and image level with the same matching rules and the same patient-conflict visibility rules as C-FIND. It shall refuse a query without a matching key, limit and page the result, say when more rows exist, write a query audit record naming the operator, and keep the query values out of the log outside that audit record. | Draft | HAZ-002, HAZ-009, HAZ-013 |
| DCM-SRS-092 | `POST /control/v1/send` shall send a study or chosen series to a configured remote AE given by name, over the same send path as C-MOVE. It shall require the Patient ID and refuse when it does not match the study's indexed Patient ID; refuse an unknown destination; never send held conflict instances; and act only when the request carries the digest of an unchanged plan and an explicit confirmation. The plan shall show destination address, patient, study and instance count. The result shall give the outcome per sub-operation and shall be audited with operator, destination, study and patient. | Draft | HAZ-016, HAZ-005, HAZ-008, HAZ-013 |
| DCM-SRS-093 | The control API shall allow editing only the settings that can change without a restart (remote AEs, calling-AE allow-list). The change shall be validated by the normal configuration loader, written atomically to an overlay file owned by LibreDCM (the administrator's configuration file is never rewritten), and applied through the normal reload. An invalid change or a failed reload shall leave the running settings and the overlay unchanged. A setting fixed by an environment variable shall be refused. Apply shall require the digest of an unchanged plan and an explicit confirmation. | Draft | HAZ-015, HAZ-005, HAZ-007 |
| DCM-SRS-094 | LibreDCM shall keep the previous overlay version with time, operator and reason, and shall offer a rollback to it through the same plan and apply steps. | Draft | HAZ-015 |
| DCM-SRS-095 | A planned change that weakens a risk control (the allow-list becomes empty; an allow-list entry of a recently active peer is removed; a destination is removed or pointed at another address) shall carry a warning, and apply shall be refused unless the request acknowledges each such warning by its code. | Draft | HAZ-015, HAZ-007, HAZ-005 |
| DCM-SRS-096 | Control requests shall not degrade DICOM services: results and buffers shall be bounded, at most one backup and one send shall run at a time per node, and every action shall have a timeout. | Draft | HAZ-004 |
| DCM-SRS-097 | Every control response shall identify the node (node ID, AE title, version and a digest of the running live-reloadable settings). A state-changing request that carries an expected node ID or AE title that does not match shall be refused without any change. | Active | HAZ-015, HAZ-016 |
