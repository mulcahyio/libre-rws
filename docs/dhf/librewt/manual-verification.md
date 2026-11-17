# Libre WT manual and system-level verification

Format: `- <ID>: <how>`. Read by `scripts/qms/trace.py --module librewt --prefix WT`.

**2026-11-10, Hermes (pre-baseline, development evidence):** LibreDCM
(branch `feat/patient-conflict`, coercion on) and Libre WT 0.1.0-dev built
from source and run on Linux; synthetic CT instances (pydicom, no real
patient data) sent with DCMTK `storescu`: 3 images for P1, then a corrected
resend of image 2 and a new image 4 for P2. LibreDCM answered Warning
CoercionOfDataElements (B000) and held both; `dcmdump` showed `B000_P2` and an
Original Attributes Sequence item (COERCE); pixel data identical to the sent
file; `dciodvfy` output identical before and after coercion. In Libre WT the
first-run setup, the exceptions list, the side-by-side detail, the warning
page and the approval of "correct the archived study" worked; afterwards
`findscu` returned the study under P2 with 4 instances, every file had the
new patient and a CORRECT history item, and both audit logs named the
operator and reason. Screenshots: vault `Attachments/LibreWT-0.1-*.png`.

- WT-SRS-001: browser walkthrough over HTTP on 127.0.0.1 (2026-11-10, see above); HTTPS not yet exercised manually
- WT-SRS-009: browser walkthrough: warning page, reason, approve box, edit made (2026-11-10)

**2026-11-16, Hermes (pre-baseline, development evidence, card WT-0.2/5):**
Libre WT and LibreDCM built from branch `feat/librewt-0.2-instances` and run
on Linux as three processes: two LibreDCM nodes (`dcm-1`, AE `ARCHIVE1`,
allow-list `CT1`; `dcm-2`, AE `ARCHIVE2`, no allow-list; each with its own
token file) and Libre WT with an `instances:` list of three: the two nodes
under cluster label `site-a` and a third instance at `http://192.0.2.10:8081`
(an address that never answers). No patient data. Walked with `curl` and a
cookie jar after first-run setup. Seen: start-up log names each instance and
warns about plain HTTP for the third only; fleet page 200 with both nodes
`ready`, version, uptime, "not enforced (source build)", all counters 0,
"settings differ in cluster" on both (the allow-lists differ), the third
instance `unreachable` with "Last successful contact: none since Libre WT
started" and no values; dashboard and status of `dcm-1` with banner (name,
AE title, node ID, cluster), three readiness checks `ok`, counters per
service, reading time and the restart note; dashboard of the third instance
with the unreachable and plain-HTTP warnings; `/i/nope/` 404; `/exceptions`
redirected to the fleet page; security headers on the page; neither token
file's content in any saved page or in the Libre WT log (0 matches each).
Not done by hand: a browser walkthrough with screenshots, HTTPS between
Libre WT and LibreDCM, an identity change against real nodes (covered by
automated tests with the fake control API).

- WT-SRS-013: three instances from a configuration file with token files, real processes (2026-11-16, see above)
- WT-SRS-016: fleet page with two real nodes and one address that does not answer (2026-11-16)
- WT-SRS-017: dashboard and status page of a real node (2026-11-16)
- WT-SRS-023: "settings differ in cluster" shown for two real nodes with different allow-lists (2026-11-16)
- WT-SRS-036: start-up warning and page warning for the plain-HTTP instance (2026-11-16)

**2026-11-16, Hermes (pre-baseline, development evidence, card WT-0.2/6):**
LibreDCM and Libre WT built from branch `feat/librewt-0.2-config` and run on
Linux as three processes: LibreDCM `manual-dcm-1` (AE `ARCHIVE1`, overlay
file in its data folder, token file) managed by Libre WT as instance
`dcm-1`, and LibreDCM `manual-dcm-2` (AE `ARCHIVE2`) used only as a C-ECHO
target. No patient data. Walked with `curl` and two cookie jars (Admin and
Operator) by a scratch script. Seen: configuration page for both roles with
2 live and 4 secret rows, change controls for the Admin only; empty
allow-list warning; Operator refused on the edit page (403). Admin added
destination `DCM2` (dcm-2's DICOM port): warning page with LibreDCM's
difference; apply without a reason refused (422); the same confirmation a
second time refused (409); proper apply done, destination shown with source
`overlay`. Operator C-ECHO to `DCM2`: success. Operator free address:
refused (403). Admin C-ECHO to a port where nothing listens: failed at the
stage `connect`, "connection refused". Removing `DCM2`: LibreDCM's warning
`remote_ae_removed` had its own box; apply without it refused (422), with it
done. Allow-list restricted to two rules (no device had been active, so
LibreDCM gave no warning), then rolled back to the previous version (the
rollback page named who made it); allow-list empty again. Reload from the
files done. Review without the CSRF token and with another session's token:
403. LibreDCM's own audit records of the 7 actions name the Libre WT user
(`admin1`, `op1`), the reason and a request ID; Libre WT's audit log has the
matching reviews, approvals, results and the two page views. The token was
in no saved page and not in the Libre WT log (0 matches). Not done by hand:
a browser walkthrough with screenshots, HTTPS between Libre WT and
LibreDCM, the licence block on a real unlicensed official build.

- WT-SRS-019: configuration page of a real node for Operator and Admin, secrets hidden (2026-11-16, see above)
- WT-SRS-020: destinations and allow-list pages of a real node, empty allow-list warning (2026-11-16)
- WT-SRS-021: destination added and removed (warning box) and allow-list changed on a real node (2026-11-16)
- WT-SRS-022: rollback and reload on a real node (2026-11-16)
- WT-SRS-024: C-ECHO success to a real node, connect failure to a closed port, free address refused for the Operator (2026-11-16)

**2026-11-16, Hermes (pre-baseline, development evidence, card WT-0.2/7):**
Libre WT and LibreDCM built from branch `feat/librewt-0.2-licence` and run
on Linux as two processes: LibreDCM `manual-dcm-1` (AE `ARCHIVE1`, source
build, SQLite index, backup directory set with
`LIBRE_DCM_CONTROL_BACKUP_DIR`, token file) managed by Libre WT as instance
`dcm-1`. No patient data. Walked with `curl` and three cookie jars (Admin,
Operator, Viewer) by a scratch script. Seen: licence page 200 for the
Viewer with "DICOM services keep running whatever the licence state" and
"Not enforced"; no licence action form for the Admin on this source build;
acknowledgement review refused for Viewer and Operator (403). Admin uploaded
a synthetic licence file: the warning page showed the same SHA-256 as
`sha256sum` of the file (`e7d720f9…51da`); the approved install was refused
by LibreDCM ("source build: no licence is needed", 422, "Nothing was
changed"). A 300 KB upload was refused (413). Backup page refused for the
Viewer (403); shown to the Operator with "The backup holds the index only".
Backup without a reason refused (422); with a reason done: the result page
named `index-manual-dcm-1-20261116T232742Z.db` and SHA-256 `26f3c60b…588b`,
equal to `sha256sum` of the file LibreDCM wrote (102400 bytes, mode 0600);
an extra `path` form field was ignored; the same approval a second time was
refused (409); the backups page then listed the file and the history row
with the reason. LibreDCM's own audit records show the start and result of
the backup under `op1` with the reason, and the refused licence action
under `admin1`. The token and the licence text were in no saved page and
not in the Libre WT log (0 matches each). Not done by hand: a browser
walkthrough with screenshots, HTTPS between Libre WT and LibreDCM, the
licence actions against a real unlicensed official build (acknowledge,
install, activate, request file: automated tests with the fake control API
only).

- WT-SRS-027: licence page of a real source-build node for Viewer and Admin, acknowledgement refused below Admin, licence file upload with checksum, LibreDCM's refusal shown, oversized upload refused (2026-11-16, see above)
- WT-SRS-028: index backup on a real node by an Operator, checksum on the page equal to the file on disk, list and history shown, Viewer refused (2026-11-16)

**2026-11-16, Hermes (pre-baseline, development evidence, card WT-0.2/8):**
LibreDCM and Libre WT built from branch `feat/librewt-0.2-studies` and run
on Linux as two processes: LibreDCM `manual-dcm-8` (AE `ARCHIVE1`, empty
allow-list, one configured destination `STORESCP=127.0.0.1:11399`, token
file) managed by Libre WT as instance `dcm-1`. DCMTK `storescu` (container
`rws-e2e-dcmtk`, host network) stored two pydicom sample files
(`CT_small.dcm`, Patient ID `1CT1`; `MR_small.dcm`, Patient ID `4MR1`;
public synthetic test data, no real patient data) into LibreDCM. DCMTK
`storescp` (same image) listened as `STORESCP` on port 11399. Walked with
`curl` and two cookie jars (Admin and Operator) by a scratch script. Seen:
a search with `*` only refused before sending (422, "give at least one
search value"); a date-range search by the Operator listed both studies
(`4MR1`, `1CT1`) with the "not a diagnostic viewer" statement; study page
200 without a send form for the Operator; series page listed the study,
series and SOP Instance UIDs; the Operator's send review refused (403).
For the Admin the study page offered the send form with Patient ID `4MR1`
and destination `STORESCP: STORESCP at 127.0.0.1:11399`; a review naming
another Patient ID was refused by LibreDCM (422, "not indexed under this
Patient ID") with no approval issued; the review showed "Patient
CompressedSamples MR1, Patient ID 4MR1" in the warning and "send 1
instance(s) of CompressedSamples MR1 (Patient ID 4MR1) to STORESCP" in the
approval box; an approval without a reason was refused (422); the approved
send (with extra `destination` and `patient_id` form fields, ignored)
answered "complete", "The destination accepted all 1 instance(s)", and
`storescp` wrote the file `MR.1.3.6.1.4.1.5962.1.1.4.1.1.20040826185059.5457`
(9708 bytes); the same approval a second time was refused (409). The
LibreDCM audit page (Admin) showed `DICOMInstancesTransferred.sent`
(`pending` then `success`) under `admin1` with `op=control-send`, and four
`Query` records of `op1`; the CSV export came as
`text/csv; charset=utf-8` with `Content-Disposition: attachment`
(`librewt-dcm-1-audit-…csv`, 11 lines); the audit page and the log export
were refused for the Operator (403); the log page (Operator) showed the
in-memory statement and 14 lines. The Libre WT audit log held the search
(1), study and series views (3), send reviews (3), the approval (1), audit
page views (2), the export (1) and the log view (1). The token was in no
saved page and not in the Libre WT log (0 matches each). Not done by hand:
a browser walkthrough with screenshots, HTTPS between Libre WT and
LibreDCM, a partial or failed send against a real destination (automated
tests with the fake control API only), a study with several series.

- WT-SRS-025: study search with a date range by an Operator on a real node with two stored studies, study and series pages, search without a value refused (2026-11-16, see above)
- WT-SRS-026: send of a real study to a real DCMTK storescp by an Admin; patient on the warning page and in the approval box; wrong Patient ID refused; file received (2026-11-16)
- WT-SRS-029: LibreDCM audit page with the send and query records of a real node (2026-11-16)
- WT-SRS-030: log page of a real node for an Operator (2026-11-16)
- WT-SRS-037: CSV export of a real node's audit records as an attachment; refused for an Operator (2026-11-16)

**2026-11-17, Hermes (pre-baseline, release-candidate evidence, card
WT-0.2/9):** scripted **system test** (`scripts/e2e/wt-system-test.sh`,
stack `deploy/compose/docker-compose.wt-system.yml`): LibreDCM `dcm-1`
(source build, AE `LIBREDCM1`) and `dcm-2` (official build without a
licence, AE `LIBREDCM2`), Libre WT 0.2.0-rc.1 with both as instances under
cluster label `wtsys`, and DCMTK (`storescp` as destination `STORESCP`,
`storescu`), all as containers built from this branch. Synthetic data only:
a study of 2 series × 2 CT instances (`WTSYS^ALPHA`, `WTSYS-0001`) and one
instance of the same study under another patient. Every check, its result
and the pages are in the record
[`docs/releases/librewt-v0.2.0-rc.1-verification/`](../../releases/librewt-v0.2.0-rc.1-verification/README.md).
A browser walkthrough of every page in light and dark (72 screenshots,
`docs/librewt/images/`) was checked by eye and by a page-width check; it
found the layout defect fixed in this card (tables wider than the page).

- WT-SRS-001: every page fetched over HTTP through the published port; screenshots of all 36 pages in light and dark checked; no page wider than its window after the fix (2026-11-17, system test record)
- WT-SRS-002: fresh console leads to first run; first run refuses a short password, creates the Admin, then stops working (SYS-01…04, 2026-11-17)
- WT-SRS-003: failed login gives the same answer for an existing and an unknown user (SYS-05, 2026-11-17)
- WT-SRS-004: request without CSRF token refused 403; Viewer and Operator refused on Admin pages (SYS-07, -22, -30, -42, -96, 2026-11-17)
- WT-SRS-005: logout ends the session (SYS-98, 2026-11-17)
- WT-SRS-007: held instance listed with its incoming Patient ID (SYS-20, 2026-11-17)
- WT-SRS-008: archived and incoming patient side by side (SYS-21, 2026-11-17)
- WT-SRS-009: reject of the incoming image through the warning page; refused without a reason; confirmation refused a second time with 409 (SYS-23…26, 2026-11-17)
- WT-SRS-012: Admin creates an Operator and a Viewer; a user changes their own password (SYS-06, -97, 2026-11-17)
- WT-SRS-013: two real nodes configured with their own token files (SYS-10, 2026-11-17)
- WT-SRS-014: banner on every instance page; unknown instance 404 (SYS-12, -16, 2026-11-17)
- WT-SRS-016: fleet page with both nodes under their cluster label and their licence states (SYS-10, -11, 2026-11-17)
- WT-SRS-017: status pages of both nodes with readiness and the host restart note (SYS-14, 2026-11-17)
- WT-SRS-019: configuration page for an Operator, no change controls, token not shown (SYS-31, -32, 2026-11-17)
- WT-SRS-020: empty allow-list warning; destinations with source `overlay` (SYS-33, -37, 2026-11-17)
- WT-SRS-021: destination added through LibreDCM's plan; second destination planned without removing the first; allow-list of dcm-2 restricted and in force (INTRUDER refused, MODALITY1 stores) (SYS-34…37, -77a, -77b, 2026-11-17)
- WT-SRS-022: rollback removes the second destination; reload of dcm-1; reload of the unlicensed dcm-2 refused with a link to the licence page until acknowledged, then done (SYS-38…41, -72, -75, 2026-11-17)
- WT-SRS-024: Operator C-ECHO to a configured destination succeeds; Operator refused a free address (403); Admin C-ECHO to a free address succeeds (SYS-45…47, 2026-11-17)
- WT-SRS-025: Operator finds the study by Patient ID wildcard; `*` alone not sent; rejected image not listed; study page with both series; series page with its instances (SYS-51…55, 2026-11-17)
- WT-SRS-026: wrong Patient ID gets no approval; whole study (4 instances) and one series (2 instances) sent to DCMTK, every received data set byte-identical to the stored one, with a tamper control (SYS-57…63, CTL-1, 2026-11-17)
- WT-SRS-027: licence page "not enforced" on the source build; unlicensed official build: acknowledgement refused with another user's name, accepted with the Admin's own; activation request handed out as a file download (SYS-70…76, 2026-11-17)
- WT-SRS-028: Operator backup of dcm-1; the SHA-256 shown equals the SHA-256 of the file copied from the node; backup listed with user and reason (SYS-81…83, 2026-11-17)
- WT-SRS-029: Admin reads LibreDCM audit records naming the Libre WT user and reason; Operator refused (SYS-90, -91, 2026-11-17)
- WT-SRS-030: Operator reads the node's log lines with the memory-buffer statement (SYS-93, 2026-11-17)
- WT-SRS-032: neither admin token in any of the pages fetched, the Libre WT log or the saved record (SYS-99 and runner scan, 2026-11-17)
- WT-SRS-033: Libre WT audit log holds logins, setup, configuration, C-ECHO, licence, backup, send, resolve and export records (SYS-95, 2026-11-17)
- WT-SRS-034: role refusals across Viewer and Operator for every Admin-only or Operator function tried (SYS-22, -30, -42, -46, -50, -56, -80, -90, -94, -96, 2026-11-17)
- WT-SRS-036: plain-HTTP warning on both instances (SYS-13, 2026-11-17)
- WT-SRS-037: Admin exports the LibreDCM audit records as a CSV attachment; Operator refused the log export (SYS-92, -94, 2026-11-17)

Still not done at system level: HTTPS between Libre WT and LibreDCM and for
the console, the installer scripts on Windows, a partial or failed send to a
real destination, an identity change of a real node (automated tests only).
