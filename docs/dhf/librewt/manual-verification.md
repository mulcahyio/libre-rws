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
