# LibreDCM risk management file (ISO 14971:2019)

| | |
|---|---|
| Document | DHF-DCM-RMF |
| Status | **RETROSPECTIVE DRAFT** (2026-11-09). Initial hazard list for review; scoring to be agreed in the risk management plan at the QMS baseline |
| Standards | ISO 14971:2019 (FDA 5-125), AAMI TIR80002-1 (13-34), ANSI/AAMI SW96:2023 (13-131) for security risks |

## 1. Risk management plan (summary)

- **Scope:** LibreDCM as the archive component of the Libre RWS PACS, all
  deployment topologies (workstation, single node, cluster), its SOUP, its
  installer and its licensing.
- **Intended use context:** receives images from modalities and other
  systems, archives them and makes them available to viewers and other
  systems used by trained healthcare professionals for diagnosis. LibreDCM
  does not display, process or interpret images.
- **Severity scale:** S1 negligible (inconvenience) · S2 minor (delay without
  harm) · S3 serious (delayed or wrong diagnosis that leads to injury or
  wrong treatment) · S4 critical (permanent impairment or death).
- **Probability:** P1 improbable … P5 frequent, estimated for the hazardous
  situation *after* external controls (radiologist review, modality retains
  images until storage commitment).
- **Acceptability:** to be set in the approved plan; proposed: S3/S4 risks
  must be reduced as far as possible, S1/S2 acceptable at P≤3.
- **Software failures** are assumed to occur (probability of a software
  failure = 1 per IEC 62304); estimation focuses on the hazardous situation.
- **Security risks** are assessed with the threat model in
  `cybersecurity.md` and moved into this table when they can lead to harm.
- **Software safety class:** **B**. Rationale: the worst credible outcome of a
  LibreDCM failure is a delayed or wrong diagnosis caused by missing,
  late, misattributed or altered images; independent external controls
  exist (modality-side retention, radiologist review of image content and
  demographics, the viewer's patient banner). Re-assess if LibreDCM gains
  image processing, lossy compression, mammography claims or AI.

## 2. Hazard analysis (initial)

| ID | Hazard / hazardous situation | Possible harm | Causes (examples) | Risk controls (requirements) |
|---|---|---|---|---|
| HAZ-001 | Image lost after the sender was told it was stored; modality deletes its copy | Missed finding, repeat exam (radiation dose), delayed diagnosis | Crash between response and write; disk/S3 failure; failed schema upgrade; inconsistent backup | DCM-SRS-002, -005, -021, -022, -053, -056; storage commitment (planned, IHE RAD-10) |
| HAZ-002 | Query returns incomplete or wrong results; clinician believes a prior study does not exist | Missed comparison, wrong diagnosis | Matching bugs, case/charset mismatch, SQLite/PostgreSQL divergence, duplicate entries | DCM-SRS-006–009, -020, -023 |
| HAZ-003 | Pixel data or attributes altered (lossy transcoding, truncation, corrupted upload) | Wrong diagnosis | Transcoding, codec bugs, storage corruption | DCM-SRS-003, -004, -005, -010; planned end-to-end checksum verification |
| HAZ-004 | Study unavailable when needed (outage, node failure, retrieval failure) | Delayed diagnosis/treatment | Node crash, network, load balancer, memory exhaustion on large studies | DCM-SRS-010, -012, -055; HA topology; monitoring (DCM-SRS-054) |
| HAZ-005 | Images sent to the wrong destination | PHI disclosure; images missing at intended reader | Misconfigured remote AEs, empty env overriding config, reload error | DCM-SRS-011, -013, -016, -050 |
| HAZ-006 | Downstream systems (worklist, routing, reporting) not told an image arrived | Delayed reading | Event bus outage, lost events | DCM-SRS-030, -054 |
| HAZ-007 | Unauthorized system stores or retrieves images | PHI disclosure; wrong-patient images injected into the archive | No peer authentication; spoofed AE title | DCM-SRS-014, -015, -016, -044 (Draft) |
| HAZ-008 | Accesses to patient images cannot be traced | Undetected breach; HIPAA/accountability failure | Missing or untrustworthy audit records, clock drift | DCM-SRS-017, -040, -045 (Draft); NTP time client (planned) |
| HAZ-009 | Patient data disclosed in transit, in logs, on the bus or via the admin API | Privacy harm | Plain-text DICOM/HTTP, open admin endpoints, PHI in events | DCM-SRS-032, -041, -042, -043, -044 (Draft) |
| HAZ-010 | **Licence lock-out:** after licence expiry plus grace, C-STORE is refused; modalities cannot archive new studies | Delayed diagnosis; repeat exams if modality storage overflows | Expired or missing licence, wrong machine after hardware change, clock problems | DCM-SRS-052 (current behaviour is itself the cause). **Open:** recommend that clinical builds never refuse storage or retrieval; enforce licensing through warnings and blocked upgrades/support instead (decision D7 in the compliance plan) |
| HAZ-011 | Stale or wrong demographics after patient update/merge in the RIS | Images filed under the wrong patient; wrong-patient diagnosis | No HL7 ADT handling yet | Planned IHE PIR support (RAD-12); until then, labeling must state that LibreDCM does not reconcile demographics |
| HAZ-012 | Vulnerable SOUP exploited (e.g. Go standard library, x/net, x/crypto) | PHI disclosure, archive unavailable (ransomware) | Known CVEs in dependencies | govulncheck in CI, SBOM, quarterly review, patch policy (SECURITY.md) |

## 3. Next steps

1. Approve the plan (scales, acceptability matrix).
2. Score each hazard before and after controls.
3. Link each risk control to its verification (trace matrix) and confirm effectiveness.
4. Write the risk management report at the QMS baseline.
