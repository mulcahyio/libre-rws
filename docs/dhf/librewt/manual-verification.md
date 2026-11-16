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
