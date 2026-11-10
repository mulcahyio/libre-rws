# LibreDCM intended use, user needs and use specification

| | |
|---|---|
| Document | DHF-DCM-IU |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude from the settled decisions of 2026-11-09; needs approval by Patrick Mulcahy and, later, independent review) |
| Software item | LibreDCM, the DICOM archive component of the Libre RWS PACS |
| Standards | ISO 13485:2016 §7.2.1 / §7.3.3 (design inputs), IEC 62366-1:2015+A1:2020 §5.1 (use specification), IEC 62304 §5.2, ISO 14971:2019 §5.2 (intended use and reasonably foreseeable misuse), IEC 82304-1 §4 |
| Related | `SRS.md`, `risk-management.md`, `cybersecurity.md`, `docs/qms/SDP.md` |

## 1. Regulatory context

- **Today LibreDCM is a non-device software function** under FD&C Act
  §520(o)(1)(D): it only transfers, stores and converts the format of medical
  images. It is not FDA cleared and cannot be cleared on its own.
- **Future regulated product:** the *Libre RWS PACS*, a system that adds a
  diagnostic viewer to LibreDCM, intended for a 510(k) under 21 CFR 892.2050
  (product code LLZ, Class II) (decisions D1/D2, 2026-11-09). LibreDCM is
  developed under the QMS now so that its design file can go into that
  submission without retrospective work.
- The **legal manufacturer** will be Libre Systems (to be formed). Until then
  the developer is Patrick Mulcahy, Mulcahy.io.
- **Official builds** (released under the QMS, licensed) are the product this
  document describes. Community builds from the Apache-2.0 source are not
  covered and are labelled "not for clinical use" (see §8).

### 1.1 Draft system-level intended use (Libre RWS PACS)

> The Libre RWS PACS is a software system intended for receiving,
> archiving, distributing, viewing and processing medical images and related
> information, for use by trained healthcare professionals. Final diagnosis
> remains the responsibility of the physician. *(Draft wording modelled on
> cleared PACS such as K253927; to be finalised with the viewer design and a
> Pre-Submission. Mammography and AI claims are excluded unless added through
> a design change.)*

This statement is **not yet approved** and is recorded only so that the
component intended use below stays inside it.

## 2. Intended use of LibreDCM (component)

> LibreDCM receives DICOM images and related objects from imaging devices and
> other systems over the DICOM network protocol, archives them without
> altering their content, makes them searchable, and delivers them on request
> to viewers, workstations and other systems. It runs on general-purpose
> computers or servers, on premises or in a private cloud, as a single
> installation or a cluster. It is intended to be installed and operated by
> trained IT or PACS administrators as part of an imaging system used by
> healthcare professionals.

### 2.1 What LibreDCM does not do (limitations)

These limits are part of the intended use and must appear in the labeling
(installation guide, release notes, DICOM Conformance Statement):

| # | Limitation | Reason / hazard |
|---|---|---|
| L1 | Does not display, measure, process, interpret or prioritise images. It provides no clinical decision support | Non-device function; viewing is the viewer's job |
| L2 | Does not change pixel data: compressed images are stored and sent in the sender's encoding; no lossy compression is ever applied | HAZ-003 |
| L3 | Does not reconcile patient demographics with a RIS/HIS (no HL7 ADT, no IHE PIR yet). Demographics are those in the received images | HAZ-011, ANOM-002 |
| L4 | Does not yet offer DICOM Storage Commitment. Modalities must not delete images on the strength of a C-STORE success alone where site policy requires commitment | HAZ-001 |
| L5 | Is not a backup system. The site must back up the index and the image storage (the `backup` command covers the SQLite index) | HAZ-001 |
| L6 | Is not a substitute for network security. It must run on a protected network; TLS and the calling-AE allow-list must be turned on before the DICOM port is exposed beyond a trusted network | HAZ-007, HAZ-009 |
| L7 | Beta releases (`-beta.N`) are **not for clinical use** | QMS baseline not yet declared |

## 3. Users and user profiles

LibreDCM has no clinical user interface. Its human users are administrators;
clinical staff use it indirectly through other devices.

| User group | Role | Education / skills assumed | Interfaces used |
|---|---|---|---|
| **U1 IT / PACS administrator** (primary) | Installs, configures, monitors, upgrades, backs up, licenses LibreDCM | IT professional; knows networking, Docker or Linux/Windows administration, DICOM basics (AE titles, ports, transfer syntaxes). Reads English documentation | Workstation installer (`LibreDCM.sh` / `LibreDCM.ps1`), configuration file and environment variables, command-line flags, admin HTTP endpoints, logs, metrics dashboards |
| **U2 Modality / integration engineer** | Configures modalities, viewers and other systems to send to and query LibreDCM | Vendor service engineer or site integration engineer; DICOM networking | DICOM Conformance Statement; the AE title, address and port reported by the installer |
| **U3 Technologist** (indirect) | Sends images from a modality | Radiologic technologist; not expected to know LibreDCM exists | The modality's own send/queue screens |
| **U4 Radiologist / clinician** (indirect) | Queries and reads prior and current studies in a viewer | Physician | The viewer; sees LibreDCM only through query results and retrieved images |
| **U5 Security / compliance officer** | Reviews audit records and security settings | IT security | Audit log, MDS2, security documentation |

**System "users"** (software peers): modalities (Storage SCU), viewers and
workstations (Query/Retrieve SCU, C-MOVE destinations), other PACS and
routers, monitoring (Prometheus), orchestrators (Docker, Kubernetes), the
event bus consumers (other Libre RWS modules), the LibreKey licence server.

## 4. Use environment

| Aspect | Specification |
|---|---|
| Sites | Hospitals, imaging centres, clinics, teleradiology providers; test and training environments |
| Hosting | General-purpose x86-64 (arm64 possible) computers: a workstation PC (Windows 10/11 with Docker Desktop, macOS, Linux), a server or VM, or a container platform. On premises or private cloud |
| Network | Hospital LAN or VPN with DICOM devices; optional internet access for S3 storage and online licence activation. Not intended to be exposed directly to the public internet |
| Physical | Data centre or office; no patient environment requirements (no patient contact, not in the patient vicinity) |
| Operation | Unattended 24/7 service; administrators work from a console, usually remotely |
| Language | English (documentation, logs, messages) |

## 5. Operating principle

Modalities send images with DICOM C-STORE. LibreDCM writes each image
atomically to storage (filesystem or S3) exactly as received, records its
identifiers in an index (SQLite or PostgreSQL), and only then returns a
success status and announces the image on an event bus. Viewers search with
C-FIND and fetch images with C-MOVE or C-GET; LibreDCM sends the stored bytes
unchanged where the receiver accepts the stored encoding. Several identical
nodes can share one index and storage behind a load balancer. Health,
readiness, metrics and version are exposed on an admin endpoint. Details:
`architecture.md`.

## 6. Use specification (IEC 62366-1 §5.1)

| Element | LibreDCM |
|---|---|
| Intended medical indication | None directly; supports diagnosis by making images available to the physician's viewer |
| Intended patient population | Any patient whose medical images are acquired at the site (all ages, sexes, body parts, imaging modalities). LibreDCM never interacts with patients |
| Intended part of body / tissue | Not applicable (no patient contact) |
| Intended user profile | §3 |
| Use environment | §4 |
| Operating principle | §5 |
| User interface | Command-line installer and flags, YAML configuration and environment variables, admin HTTP endpoints (JSON, Prometheus text), structured logs. No graphical UI in LibreDCM itself (the planned Libre WT console will be the graphical admin UI and gets its own use specification) |

### 6.1 Frequently used and safety-related use scenarios

These are the user-interface tasks whose failure could contribute to a
hazard. They feed the use-related risk analysis (`risk-management.md`) and
the installer and documentation design.

| ID | Task (user) | Possible use error | Hazard | Current protection |
|---|---|---|---|---|
| US-01 | Install and answer the install-time questions (U1) | Wrong AE title, storage on a disk that is too small or not backed up, TLS off on an open network | HAZ-001, HAZ-007, HAZ-009 | Installer verifies container health, admin endpoint, port reachability and a real C-ECHO; prints the values to enter on modalities; security note in INSTALL.md |
| US-02 | Configure C-MOVE destinations (U1/U2) | Typo in `NAME=HOST:PORT[:AE]`, wrong host, empty environment variable overriding the file | HAZ-005 | Invalid entries are reported and not used (DCM-SRS-013); empty variables do not override (DCM-SRS-050); unknown destination returns A801 and sends nothing (DCM-SRS-011) |
| US-03 | Restrict which peers may connect (U1) | Typo in the allow-list | HAZ-007 | An invalid list rejects all peers (fails closed, DCM-SRS-015); rejections are audited and counted |
| US-04 | Reload configuration (U1) | Broken file at reload | HAZ-005, HAZ-007 | Running settings stay unchanged (DCM-SRS-016) |
| US-05 | Upgrade (U1) | Upgrade without a backup; data volume not kept | HAZ-001 | Installer backs up the index first and keeps data (DCM-SRS-056); schema upgrades in place (DCM-SRS-021) |
| US-06 | Back up and restore (U1) | Backing up only images or only the index; restore never tested | HAZ-001 | `backup` writes a consistent index snapshot while running (DCM-SRS-022). **Gap:** no documented, tested restore procedure yet |
| US-07 | Monitor (U1) | Not noticing a full disk, a bus outage or a node failure | HAZ-004, HAZ-006 | `/readyz`, metrics (DCM-SRS-054); **gap:** no alerting rules shipped |
| US-08 | Handle licence expiry (U1) | Ignoring warnings | HAZ-010 | Licence state never affects DICOM services (DCM-SRS-058); hourly reminders and blocked reloads until acknowledged (DCM-SRS-059–061) |
| US-09 | Configure a modality to send (U2) | Wrong called AE, port or TLS setting | HAZ-001 (images stay on the modality) | Association is rejected visibly (DCM-SRS-014); troubleshooting table in INSTALL.md |
| US-10 | Read audit records (U5) | Patient IDs disclosed in logs against site policy | HAZ-009 | Installer asks whether to include patient IDs (DCM-SRS-032) |

### 6.2 Reasonably foreseeable misuse (ISO 14971 §5.2)

- Using a beta or community build for clinical work (L7).
- Treating LibreDCM as the only copy of the images without backups (L5).
- Exposing the DICOM or admin port to the internet without TLS, allow-list
  or admin token (L6).
- Relying on LibreDCM to correct patient demographics (L3).
- Running several nodes on separate SQLite files (each node then sees only
  its own images); a cluster needs PostgreSQL and shared storage.
- Deleting or editing files in the storage folder by hand.

## 7. User needs

User needs are the validation targets: they are met when the system does
what users need in real use. Each is refined into the SRS requirements in
the last column (design inputs). `scripts/qms/needs.py` (run in CI) fails
when a non-retired requirement serves no user need or a need cites an
unknown or retired requirement; `scripts/qms/trace.py` traces the
requirements on to tests.

| ID | User need (in users' words) | User | SRS requirements |
|---|---|---|---|
| UN-01 | Images sent by our modalities are kept safely, and the modality is only told "stored" when they really are | U3, U1 | DCM-SRS-002, -005, -018, -021, -053 |
| UN-02 | Images come back exactly as they were acquired; nothing is compressed, converted or lost | U4 | DCM-SRS-003, -004, -010 |
| UN-03 | When I search for a patient's studies I find all of them, and only theirs | U4 | DCM-SRS-006, -007, -008, -009, -020, -023 |
| UN-04 | I can pull a study to my workstation or viewer when I need it | U4 | DCM-SRS-010, -012, -055 |
| UN-05 | Images only go to destinations we configured | U1 | DCM-SRS-011, -013 |
| UN-06 | Only systems we approve can store or retrieve images | U1, U5 | DCM-SRS-014, -015, -044 |
| UN-07 | Patient data is protected on the network and in logs | U5 | DCM-SRS-032, -041, -042, -043, -044 |
| UN-08 | We can see who sent, searched for and exported which images | U5 | DCM-SRS-017, -040, -045 |
| UN-09 | I can change settings without interrupting image traffic, and a mistake does not break the running system | U1 | DCM-SRS-016, -050, -051 |
| UN-10 | Other systems (worklist, routing, reporting) learn about every new image, even after an outage | U1 | DCM-SRS-030, -031 |
| UN-11 | I can install, upgrade, back up and check the system (including a DICOM connectivity check) without specialist tools | U1, U2 | DCM-SRS-001, -022, -056, -057 |
| UN-12 | The service keeps running through node failures, maintenance and restarts | U1 | DCM-SRS-053, -055 |
| UN-13 | I can monitor the health and load of the service | U1 | DCM-SRS-041, -054 |
| UN-14 | A licence problem never stops patient care; it is my problem as administrator, not the technologist's | U1, U3 | DCM-SRS-058, -059, -060, -061 |
| UN-15 | I know exactly which version (and later, which UDI) is running | U1, U5 | DCM-SRS-057 |
| UN-16 | Modality and viewer engineers can tell what LibreDCM supports before connecting | U2 | DICOM Conformance Statement (planned, blocked on the UID root) |

Requirements that serve no listed user need: none (checked 2026-11-10; see §9).

## 8. Labeling statements derived from this document

Required in the installation guide, release notes and (later) the IFU:

1. Intended use (§2) and the limitations L1–L7.
2. "Beta release: not for clinical use" on every `-beta` release.
3. For community builds: "Built from source; not a released Libre Systems
   product; not for clinical use."
4. Security prerequisites (L6) and the backup responsibility (L5).
5. Rx-only and "final diagnosis rests with the physician" statements belong
   to the system-level (viewer) labeling, not to LibreDCM alone.

## 9. Verification of this document (2026-11-10)

- Every SRS requirement (43 rows: 39 active, 3 draft, 1 retired) was
  checked against §7 with `scripts/qms/needs.py`. The first run found DCM-SRS-001 (C-ECHO) unmapped; it was
  added to UN-11. Second run: every active and draft requirement maps to a
  user need; retired DCM-SRS-052 is not mapped.
- Limitations L2–L6 were checked against the code and existing documents:
  no transcoding of compressed data on store (`gateway/store.go`,
  `dcm.SelectStorageSyntax`); no HL7/PIR code exists; no Storage Commitment
  SCP is registered (`gateway.serverConfig`); `-backup` handles SQLite only
  (`cmd/libredcm/main.go`).

## 10. Open items for Patrick

1. Approve the component intended use (§2) and limitations (§2.1).
2. Confirm the draft system-level statement (§1.1) as the working target.
3. Gaps found while writing this: no tested restore procedure (US-06) and no
   shipped alerting rules (US-07). Proposed: add both as backlog items.
