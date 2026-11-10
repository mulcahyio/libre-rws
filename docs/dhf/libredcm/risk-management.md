# LibreDCM risk management file (ISO 14971:2019)

| | |
|---|---|
| Document | DHF-DCM-RMF |
| Status | **DRAFT for approval** (scored and report written 2026-11-10 by Hermes/Claude; the initial hazard list of 2026-11-09 is in Git history) |
| Procedure | QMS-SOP-003 (scales §6, matrix §6.4; the matrix is still a proposal awaiting top-management approval) |
| Standards | ISO 14971:2019 (FDA 5-125), ISO/TR 24971:2020, AAMI TIR80002-1 (13-34), ANSI/AAMI SW96:2023 (13-131) |
| Related | `intended-use.md`, `SRS.md`, `architecture.md`, `cybersecurity.md`, `anomalies.md`, `generated/trace-matrix.md` |

## 1. Risk management plan

- **Scope:** LibreDCM as the archive component of the Libre RWS PACS, in all
  deployment topologies (workstation, single node, cluster), including its
  SOUP, the gonetdicom fork, the LibreKey client copy, the installer and the
  licensing behaviour. Life-cycle phases: design and development
  (retrospective to 0.2.0), release, maintenance, decommissioning (data
  export is the customer's backup).
- **Intended use and foreseeable misuse:** `intended-use.md` §2, §6.2.
- **Responsibilities:** risk owner Patrick Mulcahy (interim); analysis
  prepared by Hermes/Claude (AI tool) for human review; independent review
  when available (decision D5).
- **Acceptability criteria:** QMS-SOP-003 §6.4 (proposal). Severity S1–S5,
  probability P1–P5 (P = P1 × P2 with external controls; software failure
  assumed possible).
- **Verification of risk controls:** every control is an SRS requirement
  traced to tests by `scripts/qms/trace.py` (CI blocking); effectiveness
  tests exercise the hazardous situation (failure injection, outage,
  invalid input, fuzzing).
- **Production and post-production information:** QMS-SOP-003 §10; anomaly
  list; vulnerability monitoring (govulncheck in CI, quarterly SBOM review).
- **Review:** every PR marked as affecting a hazard; every release (risk
  management report §6); management review.

### 1.1 Software safety classification

**Class B** (accepted by Patrick Mulcahy 2026-11-09), with class-C depth of
detailed design and unit verification for the units carrying the HAZ-001,
HAZ-002/HAZ-013 and HAZ-003 controls (store path, index/patient
association, pixel-data integrity). Rationale: the worst credible outcome of
a LibreDCM failure is a delayed or wrong diagnosis caused by missing, late,
misattributed or altered images; independent external controls exist
outside the software (modality retains images until a success status,
radiologist review of image content and demographics, the viewer's patient
banner). Several hazards below reach severity S4; the class stays B because
the external controls are outside LibreDCM and are credited in P2.
**Re-assess** if LibreDCM gains image processing, lossy compression,
mammography claims or AI, and at system level when the viewer is added.

## 2. Hazard analysis and risk evaluation

Scores: S = severity, P = probability of harm (QMS-SOP-003). Initial =
before LibreDCM's own risk controls (external controls credited); Residual =
with the controls implemented today. Evaluation: A acceptable, R reduce as
far as possible (accept only with rationale), U unacceptable.

| ID | Hazardous situation | Harm | Main causes | S | Initial P | Initial | Risk controls (requirements / labeling) | Residual P | Residual | Notes |
|---|---|---|---|---|---|---|---|---|---|---|
| HAZ-001 | Image lost or not findable after the sender was told it was stored; modality deletes its copy | Missed finding, repeat exam (dose), delayed diagnosis | Crash between write and response; storage/S3 fault; failed schema upgrade; inconsistent backup; power loss | S3 | P4 | U | DCM-SRS-002 (success only after durable write and index commit), -005, -018, -021, -022, -053, -056; L4, L5 | P2 | R | Power-loss windows remain: no directory fsync (ANOM-003), SQLite `synchronous=NORMAL` (ANOM-004). With both fixed: P1 → A. No storage commitment yet (IHE RAD-10) |
| HAZ-002 | Query returns incomplete or wrong results; clinician believes a prior study does not exist | Missed comparison, wrong diagnosis | Matching bugs, case/charset mismatch, SQLite/PostgreSQL divergence, result cap reached | S3 | P3 | R | DCM-SRS-006–009, -020, -023 (draft); shared engine conformance suite; fuzzing of the query path | P2 | R | Result cap (`find_limit`, default 1000) truncates silently for the requestor (only a log warning): candidate improvement |
| HAZ-003 | Pixel data or attributes altered (transcoding, truncation, corrupted upload or storage) and shown to the radiologist | Wrong diagnosis | Transcoding, codec bugs, storage corruption (bit rot), unreadable Part 10 | S4 | P2 | R | DCM-SRS-003, -004, -005, -010, -018; no lossy path exists (inherent safety); byte-identical retrieval of compressed data | P1 | R | No checksum is stored and verified on retrieve, so silent storage corruption is not detected. Feasible control → planned (end-to-end checksum) |
| HAZ-004 | Study unavailable when needed | Delayed diagnosis or treatment | Node crash, network, load balancer, disk full, malformed input dropping an association, memory exhaustion on large studies | S3 | P4 | U | DCM-SRS-010, -012, -053, -054, -055; association limits and timeouts; streaming C-MOVE; fuzzing (parser robustness, ANOM-008 fixed); modalities keep queues | P3 single node / P2 cluster | R | No free-disk metric or shipped alert rules (intended-use US-07) |
| HAZ-005 | Images sent to the wrong destination | Privacy harm; images missing at the intended reader | Misconfigured remote AEs, empty variable overriding config, failed reload | S3 | P3 | R | DCM-SRS-011, -013, -016, -050 | P1 | A | |
| HAZ-006 | Downstream systems not told an image arrived | Delayed reading | Bus outage, lost events | S2 | P3 | R | DCM-SRS-030, -054 (outbox, pending metric) | P1 | A | |
| HAZ-007 | Unauthorised system stores or retrieves images (incl. injection of wrong-patient images) | Wrong-patient diagnosis; privacy harm | No peer authentication; spoofed AE title | S4 | P3 | U | DCM-SRS-014, -015 (fails closed), -016, -043; L6 | P2 | R | No mutual TLS yet (DCM-SRS-044 draft); AE@IP can be spoofed on a flat network |
| HAZ-008 | Accesses to patient images cannot be traced | Undetected breach (privacy harm) | Missing or untrustworthy audit records, clock drift | S2 | P3 | R | DCM-SRS-017, -040, -061 | P2 | A | ATNA syslog (DCM-SRS-045 draft) and NTP guidance planned |
| HAZ-009 | Patient data disclosed in transit, in logs, on the bus or through the admin API | Privacy harm (possibly many patients) | Plain-text DICOM/HTTP, open admin endpoints, PHI in events | S4 | P3 | U | DCM-SRS-032, -041, -042, -043; installer asks TLS and patient-ID options; L6 | P2 | R | TLS is optional; NATS/PostgreSQL TLS not configured by the installer |
| HAZ-010 | Licence problem stops or interrupts image flow | Delayed diagnosis; repeat exams if modality storage overflows | Expired/missing licence, hardware change, clock problems | S3 | P5 | U | DCM-SRS-058 (architecture test: gateway cannot reach licensing), -059–061 (admin-side reminders) | P1 | A | Residual: blocked config reloads can delay adding a modality; acknowledgement is one command |
| HAZ-011 | Stale demographics after a patient update/merge in the RIS | Images filed under outdated demographics; wrong-patient confusion | No HL7 ADT / IHE PIR | S3 | P3 | R | Information for safety only: limitation L3 | P3 | R | Planned IHE PIR (RAD-12). Information for safety alone gives little reduction |
| HAZ-012 | Vulnerable SOUP or own code exploited | Privacy harm; archive unavailable (ransomware) | Known CVEs, injection, parser bugs | S4 | P3 | U | govulncheck, gosec (blocking) and fuzzing in CI; SBOM; SECURITY.md patch targets; pinned base images | P2 | R | Independent penetration test still to do |
| HAZ-013 | **Study silently reassigned to another patient** when instances of one Study UID arrive with different Patient IDs (ANOM-002), or a series UID arrives under another study (ANOM-009) | Wrong-patient diagnosis; prior missing under the right patient | Mis-registration corrected at the modality and re-sent; UID collision | S4 | P3 | U | **None in LibreDCM today**; external: the viewer shows the file's own header (differs from the index) | P2 | R | Control needed (detect, audit, alert, keep first demographics); decision for Patrick |

## 3. Risk controls and verification of their effectiveness

| Hazard | Control (requirement) | Type | Verification evidence (automated unless stated) |
|---|---|---|---|
| HAZ-001 | Success status only after storage write and index commit (DCM-SRS-002) | Design | `TestEndToEnd`; **`TestStoreFailureNeverAcknowledged`** (storage failure, index failure with and without outbox: failure status, nothing indexed, no event; mutation check: acknowledging a failed write makes it fail) |
| HAZ-001 | Atomic, checksummed storage on both backends (DCM-SRS-005) | Design | `TestStoreContract` (filesystem; S3 when configured) |
| HAZ-001 | Unencodable identifiers refused, no unreadable file (DCM-SRS-018) | Design | `TestPart10RefusesUnencodableMeta`, `FuzzPart10RoundTrip` |
| HAZ-001 | Schema upgrade in place, migration lock (DCM-SRS-021) | Design | `TestUpgradeFromV1` |
| HAZ-001 | Consistent online backup (DCM-SRS-022) | Protective | `TestBackupWhileWriting`, `TestBackupRejectsEmptyDest` |
| HAZ-001 | Shutdown never acknowledges an unstored instance (DCM-SRS-053) | Design | `TestGracefulShutdown` |
| HAZ-001 | Installer backs up before upgrade (DCM-SRS-056) | Protective | Manual, pre-QMS (Linux, Windows); to be re-run under the system test protocol |
| HAZ-002 | Matching rules, charset (DCM-SRS-006–009) | Design | `TestBuildFilterKinds`, `TestTimeBounds`, `TestResponseCharset`, `TestFindRefinementsEndToEnd`, `FuzzCFindKeys`, `FuzzCFindIdentifierBytes` |
| HAZ-002 | Identical results on both engines (DCM-SRS-020) | Design | `TestConformance` (SQLite; PostgreSQL when configured) |
| HAZ-003 | Sender's encoding kept, no transcoding (DCM-SRS-003) | Inherent | `TestSelectStorageSyntaxKeepsSenderPreference`; DCMTK conformance (manual, pre-QMS: compressed pixel data byte-identical) |
| HAZ-003 | Part 10 wrapping without re-encoding (DCM-SRS-004) | Inherent | `TestPart10RoundTrip`, `FuzzPart10RoundTrip` (dataset bytes unchanged for any input) |
| HAZ-003, -004 | Stored encoding sent on C-MOVE (DCM-SRS-010) | Design | `TestEndToEnd` |
| HAZ-003 | Pixel values preserved on native re-encoding; compressed data never transcoded (two independent layers, detailed design §4) | Design | `TestPixelDataIntegrity` (mutation-checked) |
| HAZ-004 | Robust parsing of untrusted input | Protective | `FuzzDecodeAndExtract`, `FuzzDatasetOffset`, C-FIND fuzzers; fork fuzzers `FuzzRead`, `FuzzDecodePDataTF`, `FuzzDecodeElements`, `FuzzCommandDecoders` |
| HAZ-004 | Cluster serves any instance from any node (DCM-SRS-055) | Protective | Manual, pre-QMS 3-node test; to be re-run under the system test protocol |
| HAZ-004, -006 | Metrics (DCM-SRS-054) | Protective (detection) | `TestMetricsExposed`, `TestLicenseStateMetrics` |
| HAZ-005 | Unknown destination refused (DCM-SRS-011); invalid entries rejected (-013); safe reload (-016); empty env ignored (-050) | Design | `TestEndToEnd`, `TestParseRemoteAEs`, `TestReloadAppliesLiveSettings`, `TestEnvOverridesAndEmptyIsUnset` |
| HAZ-006 | Outbox (DCM-SRS-030) | Design | `TestRelaySurvivesBusOutage`, `TestOutboxLifecycle`, `TestOutboxConcurrentClaims`, `TestBackoff` |
| HAZ-007 | Called-AE check (DCM-SRS-014) | Protective | **`TestWrongCalledAERejected`** (new); DCMTK echoscu (manual, pre-QMS) |
| HAZ-007 | Allow-list, fails closed (DCM-SRS-015) | Protective | `TestParseAllowRule`, `TestAllowListEndToEnd` |
| HAZ-007, -009 | DICOM TLS only, ≥ 1.2 (DCM-SRS-043) | Protective | `TestDICOMListenerTLSOnly`; Windows run (manual, pre-QMS) |
| HAZ-008 | Audit records (DCM-SRS-040), source AE (-017), licence acknowledgement audited (-061) | Protective | `TestAuditRecords`, `TestAllowListEndToEnd`, `TestUnlicensedNagAndAcknowledgement` |
| HAZ-009 | Admin token, HTTPS, patient-ID omission (DCM-SRS-041, -042, -032) | Protective | `TestAdminToken`, `TestAdminTokenFile`, `TestAdminNoTokenIsOpen`, `TestGenCert`, `TestOmitPatientID` |
| HAZ-010 | No licence dependency in the gateway (DCM-SRS-058); reminders (-059–061) | Inherent (architecture) | `TestGatewayHasNoLicenseDependency`; licensing tests; reload test |
| HAZ-012 | SCA, SAST, fuzzing in CI; SBOM | Protective | CI `verify` job (govulncheck, gosec blocking, fuzz seed corpus), `generated/sbom.cdx.json` |

**New risks introduced by controls** (ISO 14971 §7.5), checked 2026-11-10:
the fail-closed allow-list can stop all traffic after a typo (mitigated:
audit record, metric and log name the cause; reload keeps the last good
list); the Part 10 guard can refuse an instance (only values that cannot be
written correctly); the licence reminders can block a configuration reload
(HAZ-010 residual). None creates an unacceptable risk.

## 4. Use-related risks (IEC 62366-1)

The safety-related use scenarios US-01…US-10 in `intended-use.md` §6.1 map
to HAZ-001, -004, -005, -007, -009 and -010. Remaining use-related gaps: no
tested restore procedure (US-06) and no shipped alert rules (US-07); both
contribute to the residual P of HAZ-001 and HAZ-004.

## 5. Overall residual risk and benefit-risk

**Benefit:** complete, unaltered prior and current images available to the
physician when needed, at low cost and with open interfaces.

**Overall residual risk (2026-11-10): not yet acceptable for clinical
use.** Seven hazards were initially U: five are now R (HAZ-001, -004, -007,
-009, -012), one is A (HAZ-010), and **HAZ-013 is R only because of an
external control** (the viewer shows the file's header); LibreDCM itself
has no control for it yet. HAZ-001 has two feasible, cheap controls not yet
implemented (ANOM-003, ANOM-004), so "reduced as far as possible" cannot be
claimed for it. This is consistent with the current labeling: beta releases
are not for clinical use (limitation L7).

**Before the QMS baseline / first clinical release:**
1. Control HAZ-013 (ANOM-002, ANOM-009): detect demographic conflicts, audit and alert,
   do not silently overwrite (decision needed).
2. Close ANOM-003 (directory fsync) and ANOM-004 (SQLite durability).
3. Add checksum storage and verification on retrieve (HAZ-003).
4. Ship a tested restore procedure and alert rules incl. free disk space
   (HAZ-001, HAZ-004).
5. Re-run the system tests under the signed protocol (manual evidence for
   DCM-SRS-003, -043, -055, -056 is pre-QMS only).
6. Mutual TLS and ATNA (DCM-SRS-044, -045) and an independent penetration
   test, to move HAZ-007/-009/-012 as low as possible.

## 6. Risk management report (draft, 2026-11-10, for 0.2.x on `qms/retro-dcm`)

| Item | Status |
|---|---|
| Plan followed | Yes for analysis, control verification and evaluation; independent review not yet possible (D5) |
| All hazards evaluated | 13 hazards, all scored. Residual: 0 U; 9 R; 4 A |
| Controls implemented and verified | Every control requirement is active and traced to tests (`trace.py`, 2026-11-10); DCM-SRS-003, -043, -055, -056 still rely partly on pre-QMS manual evidence |
| Residual risks in R | HAZ-001, -002, -003, -004, -007, -009, -011, -012, -013, each with an improvement listed in §2/§5 |
| Overall residual risk | **Not acceptable for clinical use yet** (§5); acceptable for beta and non-clinical evaluation, as labelled |
| Post-production information | Processes defined (QMS-SOP-003 §10, QMS-SOP-005, QMS-SOP-010); no field data yet (never used clinically) |
| Approval | Pending: risk owner and QA approver (Patrick Mulcahy); matrix approval (QMS-SOP-003) pending |
