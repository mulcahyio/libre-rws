# Libre WT intended use and user needs

| | |
|---|---|
| Document | DHF-WT-IU |
| Status | **DRAFT** (2026-11-10); §3 added 2026-11-14 for v0.2, **DRAFT for approval** |

## 1. Intended use (v0.1)

Libre WT is the web management console of the Libre RWS PACS. Version 0.1
lets PACS administrators review images that LibreDCM held because they
contradict the patient, study or series already archived, and resolve them
(correct the incoming images, correct the archived study, or reject the
incoming images) with a recorded reason. It does not display images and is
not used for diagnosis. It is used by trained PACS administrators (U1 in
`../libredcm/intended-use.md`); Viewer and Operator accounts can see the
queue.

## 2. User needs

| ID | Need | Users | Requirements |
|---|---|---|---|
| UN-WT-01 | Only people we authorised can use the console, and nobody can log in with a default password | U1, U5 | WT-SRS-001, -002, -003, -004, -005, -012 |
| UN-WT-02 | I see every study LibreDCM held for review, with what conflicts with what | U1 | WT-SRS-006, -007, -008 |
| UN-WT-03 | I can't change a patient's images by accident: I see exactly what will change and must approve it, with a reason | U1 | WT-SRS-009, -010 |
| UN-WT-04 | We can show who looked at and changed what, and why | U1, U5 | WT-SRS-010, -011 |
| UN-WT-05 | I manage all our LibreDCM nodes from one place, and I always know which node I am acting on | U1 | WT-SRS-013, -014, -015, -023, -036 |
| UN-WT-06 | I see at a glance whether every node is healthy, and old information is never shown as if it were current | U1 | WT-SRS-016, -017, -018, -035 |
| UN-WT-07 | I can see how a node is configured without secrets appearing on screen | U1, U5 | WT-SRS-019, -020, -032 |
| UN-WT-08 | I can add a modality or a destination without interrupting image traffic; I see exactly what will change before it happens, and I can undo it | U1 | WT-SRS-021, -022, -031 |
| UN-WT-09 | I can check from the console that a node reaches another DICOM system | U1, U2 | WT-SRS-024 |
| UN-WT-10 | I can find a study and send it to a workstation, and a slip can't send it to the wrong place or send the wrong patient's study | U1 | WT-SRS-025, -026, -031 |
| UN-WT-11 | I handle the licence from the console, and a licence problem never stops patient care | U1 | WT-SRS-027 |
| UN-WT-12 | I can back up the index before maintenance without logging in to the host, and a backup can't damage anything | U1 | WT-SRS-028 |
| UN-WT-13 | I can see what happened on a node (audit records, logs) and who did what in the console | U1, U5 | WT-SRS-029, -030, -033 |
| UN-WT-14 | People see and do only what their role allows | U1, U5 | WT-SRS-034, -032 |

UN-WT-05…14 were added 2026-11-14 for Libre WT 0.2 (issue #20). Their
requirements are `Draft` until implemented.

## 3. Libre WT 0.2 (DRAFT for approval, 2026-11-14)

Written by Hermes (Claude) for human review. Scope approved by Patrick
Mulcahy on 2026-11-14 (issue #20): "Build Libre WT to support all LibreDCM
functions developed to date."

### 3.1 Intended use (v0.2)

Libre WT is the web management console of the Libre RWS PACS. Version 0.2
lets trained PACS administrators watch and administer one or more LibreDCM
archive nodes from a browser: see health and activity, see the
configuration, change the C-MOVE destinations and the calling-AE allow-list,
reload the configuration, test DICOM connectivity, find a study and send it
to a configured destination, handle the licence, start an index backup, read
recent audit records and logs, and resolve Study Exceptions (as in 0.1).

Libre WT does not display images. It is not used for diagnosis and gives no
clinical information to clinicians. It does not start, stop or restart
LibreDCM. It does not change settings that need a restart.

Regulatory note: Libre WT is part of the Libre RWS PACS (the device under
design; see `../libredcm/intended-use.md` §1). On its own it is an
administration tool. Its functions matter for safety because they can change
which systems may store and retrieve images, where images are sent, and
(through Study Exceptions) which patient a study belongs to.

### 3.2 Users

| User (see `../libredcm/intended-use.md` §3) | Libre WT role | What they do in Libre WT | Training assumed |
|---|---|---|---|
| U1 IT / PACS administrator, senior | Admin | Everything: configuration changes, sending studies, licence installation, resolving exceptions, user management, audit | IT professional with DICOM basics (AE titles, ports, C-MOVE destinations); has read the Libre WT guide; authorised by the site to change the archive |
| U1 IT / PACS administrator, on duty | Operator | Daily checks, connectivity tests, backups, licence acknowledgement, study search, logs | IT professional with DICOM basics |
| U2 Modality / integration engineer | Operator (while on site) | Connectivity tests, reading the allow-list and destinations while connecting a device | DICOM networking |
| U5 Security / compliance officer | Viewer or Admin (site decides) | Reads status; with Admin, reads audit records | IT security |
| Help desk, manager | Viewer | Looks at the fleet page and dashboards | None beyond the guide |

Technologists and radiologists (U3, U4) do not use Libre WT.

Use environment: a desktop browser on the hospital's administration
network; no scripts needed; sessions end after 30 minutes idle. Not
intended for phones and not for use from the public internet.

### 3.3 Use of each function

"Changes the archive?" says whether the function can change how LibreDCM
behaves or where patient data goes. Those functions follow the action rule
(WT-SRS-031): plan or warning page, approval box, reason, single-use
confirmation, audit on both sides.

| Function | LibreDCM function behind it | Who | What the user does with it | Changes the archive? | Requirements |
|---|---|---|---|---|---|
| Fleet overview | Health, readiness, version, metrics | All roles | Checks every node at a glance; finds the node with a problem | No | WT-SRS-016, -023 |
| Instance dashboard and status | Readiness checks, metrics for C-ECHO, C-STORE, C-FIND, C-MOVE, C-GET, outbox, conflicts | All roles | Sees whether a node receives and serves images, and what is wrong when it does not | No | WT-SRS-017, -018 |
| About | `/version` | All roles | Checks which version runs where | No | WT-SRS-035 |
| Configuration view | Configuration (AE title, listen address, TLS, storage, index, events, limits, timeouts, conflict policy) | Operator, Admin | Reads how the node is set up; learns which settings need a restart on the host | No | WT-SRS-019 |
| Destinations and allow-list view | Remote AEs, calling-AE allow-list | Operator, Admin | Checks whether a modality may connect and where studies can be sent | No | WT-SRS-020 |
| Change destinations or allow-list | Live-reloadable configuration | Admin | Adds a new modality or workstation; removes a retired one | **Yes** | WT-SRS-021, -031 |
| Reload, rollback | Live reload (same path as SIGHUP) | Admin | Applies a change made in the file on the host; undoes the last console change | **Yes** | WT-SRS-022, -031 |
| C-ECHO test | C-ECHO client (`-echo`) | Operator, Admin (free address: Admin) | Proves that a node reaches a workstation or PACS before relying on it | No (network test only) | WT-SRS-024 |
| Study search | Index query, same rules as C-FIND | Operator, Admin | Confirms that a study arrived; finds the study to send | No (shows patient data) | WT-SRS-025 |
| Send study | Same send path as C-MOVE | Admin | Sends a study to a configured workstation or PACS, for example after a failed automatic route | **Yes** (patient data leaves the archive) | WT-SRS-026, -031 |
| Licence | Licensing (`-license-status`, `-request`, `-activate`, `-install`, `-ack`) | View: all. Acknowledge: Operator, Admin. Others: Admin | Sees the licence state; acknowledges the notice; installs or activates a licence | Licence only. Never DICOM services | WT-SRS-027, -031 |
| Backup | Online `-backup` | Operator, Admin | Makes a snapshot of the index before maintenance | Writes a new file on the node; changes nothing else | WT-SRS-028, -031 |
| LibreDCM audit records | Structured audit records | Admin | Looks at recent stores, queries, exports and rejections | No (shows patient data) | WT-SRS-029 |
| LibreDCM logs | Logs | Operator, Admin | Looks for the cause of a problem | No | WT-SRS-030 |
| Study Exceptions | Patient-conflict control API | View: all. Resolve: Admin | As in 0.1, now per instance | **Yes** | WT-SRS-007…010 |
| Users, console audit log | (Libre WT itself) | Admin | As in 0.1; the audit log now names the instance | No | WT-SRS-011, -012, -033 |

LibreDCM functions with no Libre WT function, on purpose: container start,
stop and restart (host and installer); `-gen-cert` (writes a private key on
the host and needs a restart; the console shows whether TLS is on);
settings that need a restart (read-only in the console); image upload and
image display (not console functions).

### 3.4 Safety-related use scenarios (IEC 62366-1)

| ID | Task (role) | Possible use error | Hazard | Protection (Draft requirements) |
|---|---|---|---|---|
| US-WT-01 | Resolve a study exception (Admin) | Wrong exception or wrong action | WT-HAZ-01 | As in 0.1 (WT-SRS-008, -009) |
| US-WT-02 | Act on one node of several (Operator, Admin) | Acting on the wrong node (similar names, several browser tabs) | WT-HAZ-05 | Instance in every address and banner; no selected instance in the session; identity check; confirmation bound to the instance (WT-SRS-014, -015, -031) |
| US-WT-03 | Add or remove an allow-list rule or a destination (Admin) | Typo; removing a modality still in use; emptying the list; wrong address for a destination | WT-HAZ-06 | Plan page with the exact difference; LibreDCM validation; warnings with their own boxes; rollback; C-ECHO test afterwards (WT-SRS-021, -022, -024) |
| US-WT-04 | Change one node of a cluster and forget the others (Admin) | Nodes behave differently | WT-HAZ-12 | Divergence flag on the fleet page (WT-SRS-023) |
| US-WT-05 | Send a study (Admin) | Wrong patient's study; wrong destination | WT-HAZ-07 | Destination from the configured list only; warning page with patient, study and destination; LibreDCM checks the Patient ID (WT-SRS-026) |
| US-WT-06 | Check health (all roles) | Reading old values as current; missing an unreachable node | WT-HAZ-11 | Unreachable shown as unreachable with the time of last contact; time of reading on every status page (WT-SRS-016, -017) |
| US-WT-07 | Handle the licence (Operator, Admin) | Expecting images to stop; installing the wrong licence | WT-HAZ-09 | Statement that DICOM keeps running; invalid licence refused, old one kept (WT-SRS-027) |
| US-WT-08 | Back up (Operator, Admin) | Believing images are included; many backups filling the disk | WT-HAZ-10 | Statement on the page; LibreDCM free-space check and one backup at a time (WT-SRS-028) |
| US-WT-09 | Read configuration, logs, audit records (Operator, Admin) | Copying or showing a screen with patient data or secrets | WT-HAZ-08, WT-HAZ-13 | Secrets never shown; role limits; no-store pages (WT-SRS-019, -032, -034) |

Reasonably foreseeable misuse: sharing one Admin account; pointing a
connection at a load balancer instead of a node; leaving the console on
plain HTTP on a network; using the study search as a patient look-up tool
for clinical questions (it is not validated for that and shows no images);
treating the in-memory audit view as the audit archive.
