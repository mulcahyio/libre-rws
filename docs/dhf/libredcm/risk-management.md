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
| HAZ-014 | **Administrator correction applied to the wrong patient or study** (Libre WT / control API resolve action) | Wrong-patient diagnosis for every image of the study | Wrong conflict chosen, wrong action (correct_existing vs correct_incoming), misread demographics | S4 | P3 | U | DCM-SRS-077 (plan shown before any change, mandatory operator + reason + confirmation, token-protected API, audit), DCM-SRS-078 (Original Attributes Sequence in every rewritten file keeps the previous values, so a correction can be traced and reversed), WT-SRS (Admin role only, warning dialog with the planned changes, approve-edit confirmation) | P2 | R | Reversal is manual in v0.1 (previous values are in each file). IOCM propagation to peers that already received the study is not implemented (warning shown in the plan) |
| HAZ-013 | **Study silently reassigned to another patient** when instances of one Study UID arrive with different Patient IDs (ANOM-002), or a series UID arrives under another study (ANOM-009) | Wrong-patient diagnosis; prior missing under the right patient | Mis-registration corrected at the modality and re-sent; UID collision | S4 | P3 | U | DCM-SRS-070–079 (2026-11-10, issue #14): conflicts detected in the index transaction, existing associations never change, conflicting instances held under their own storage key and hidden by default, audit + metrics, administrator resolution in Libre WT | P1 | R | Residuals: (a) visibility `flagged` / `accept_separate` show a second entry with the same Study UID; retrieve without Patient ID returns only the indexed patient (DCM-SRS-073), viewers that ignore Patient ID could still confuse the two; default `hidden` avoids it. (b) Race between pre-check and index transaction on two nodes receiving the same SOP instance for different patients within milliseconds can overwrite that file (`lateConflict`, store.go). (c) Conflicts with the indexed Patient ID empty vs. filled are treated as conflicts (safe, may add review work) |

## 2a. Planned control API v1 extension (Draft, issue #20, 2026-11-14)

**DRAFT for approval.** Libre WT 0.2 adds remote administration through the
control API (`control-api-v1.md`). The functions are not implemented yet.
This section is the risk analysis done before coding (design input). The
controls are `Draft` requirements; "Target residual" is what is expected
once they are implemented and verified. The hazards below move into §2 and
§3, with their verification evidence, in the pull requests that implement
them. They are **not** counted in the report of §6 yet. Console-side hazards
are in `../librewt/risk-management.md` (WT-HAZ-05…17).

**State after card WT-0.2/3 (2026-11-14).** The action endpoints exist:
C-ECHO test, reload, licence, backup. DCM-SRS-086…090 and -097 are `Active`
and verified; see "Verification of the implemented controls" below.

**State after card WT-0.2/4 (2026-11-16).** The study query, the send and
the configuration edit with rollback exist. DCM-SRS-091…096 are `Active`
and verified; every requirement of this extension is now implemented on the
LibreDCM side. The controls of HAZ-015 and HAZ-016 are verified; see
"Verification of the implemented controls (card WT-0.2/4)" below. The
residual risk of both stays as targeted (P2, level R) and needs the benefit
and risk decision of the approver, as the other R rows of this file. The
Libre WT pages that use these endpoints are a later card; the use-related
part of both hazards (what the person sees and confirms) is analysed in
`../librewt/risk-management.md` and verified there.

### New hazards

| ID | Hazardous situation | Harm | Main causes | S | Initial P | Initial | Risk controls (Draft requirements) | Target residual P | Target residual | Notes |
|---|---|---|---|---|---|---|---|---|---|---|
| HAZ-015 | **Remote configuration change breaks or opens DICOM traffic**: a change to the allow-list or the C-MOVE destinations, a reload or a rollback made through the control API is wrong, or is made on the wrong node | Delayed diagnosis (modality refused, images queue or are lost at the modality; study cannot be moved to the reader); wrong-patient or privacy harm when the allow-list is opened (HAZ-007) or a destination is pointed elsewhere (HAZ-005) | Typo; wrong node chosen; stale page; file edited by hand between plan and apply; nodes of a cluster drifting apart; stolen admin token | S4 | P3 | U | DCM-SRS-093 (only live-reloadable settings; validated by the normal loader; overlay file, atomic write; failed reload changes nothing; settings fixed by environment refused), -094 (previous version kept, rollback), -095 (weakening changes need an acknowledged warning), -088 (reload plan shows the difference), -097 (node identity check, settings digest), -086 (operator, reason, audit), -080 (token) | P2 | R | S4 is taken from HAZ-007 (opened allow-list). Restart-required settings cannot be changed remotely at all (inherent safety). Residual: a valid but wrong entry (the right syntax and the wrong IP) cannot be detected by LibreDCM; the C-ECHO test (-087) and rollback limit how long it lasts |
| HAZ-016 | **Study sent from the console to the wrong destination, or the wrong study sent** | Privacy harm (patient data at a system that should not have it); wrong-patient confusion at the receiver; the intended reader does not get the study (delay) | Wrong study or destination picked; similar names; double submit; wrong node | S3 | P3 | R | DCM-SRS-092 (configured destinations only; Patient ID must be given and must match; plan shows patient, study, count and destination address; digest of an unchanged plan and confirmation; held conflict instances never sent; audit), -097 (identity), -086 (request ID stops a double send) | P2 | R | The receiving system decides what it does with the images. A recall is not possible in DICOM; the audit record shows what was sent where |

### Effect on existing hazards

| Hazard | New cause from the extension | Controls (Draft) | Assessment |
|---|---|---|---|
| HAZ-001 (image lost) | Backup overwrites the index or an older backup; the administrator believes images are backed up | DCM-SRS-090 (no client path, name made by LibreDCM, exclusive create, snapshot method of DCM-SRS-022 unchanged; note that images are not included) | No increase expected. The existing backup control is reused, not changed |
| HAZ-002 (wrong query result) | The console's study search gives other results than C-FIND | DCM-SRS-091 (one shared matching function; "more rows exist" is stated) | No increase expected |
| HAZ-004 (unavailable) | A backup fills the disk; large queries, sends or log reads load the node; a remote stop | DCM-SRS-090 (free-space check, one at a time), -096 (bounds, timeouts), -084 and -085 (fixed-size buffers); no stop or restart endpoint exists (inherent safety) | No increase expected |
| HAZ-005 (wrong destination) | See HAZ-015, HAZ-016 | | Analysed there |
| HAZ-007 (unauthorised peer) | Allow-list emptied or widened from the console | DCM-SRS-095, -093 | Analysed in HAZ-015 |
| HAZ-008 (not traceable) | Administrative actions without a named person | DCM-SRS-086 (operator and reason required; audit before acting); -084 does not replace the log | Improves traceability of administration. Residual: the operator name is asserted by Libre WT (shared token) |
| HAZ-009 (disclosure) | Configuration, log or audit endpoints return secrets or more patient data; study search widens who can read patient data | DCM-SRS-082 (redaction from a list of safe settings: unknown settings are hidden), -084 (no more patient data than the audit log, omission honoured), -091 (no listing without a key, audited), -090 (no download), -080 (token, no-store) | The risk grows with the number of endpoints; controlled by the listed requirements. Verified for the read-only endpoints (card WT-0.2/2): `TestNoSecretInAnyResponse` searches every response for every configured secret, `TestAuditRingHonoursPatientIDOmission` checks omission through the real gateway; both mutation-checked. A second, independent control was added: every control response passes a secret filter before it is written (fails closed) |
| HAZ-010 (licence stops image flow) | Licence actions through the control API change DICOM behaviour | DCM-SRS-089 (existing licensing code, outside the gateway), -058 with `TestGatewayHasNoLicenseDependency` | No increase allowed: the architecture test must keep passing in every PR |
| HAZ-012 (exploited vulnerability) | More attack surface on the admin listener; C-ECHO test used to probe the network | DCM-SRS-080, -087 (Verification only, result reduced to success or stage), -096; gosec, govulncheck, fuzzing of request decoding | Slight increase, controlled; see `cybersecurity.md` §3a |
| HAZ-013 (wrong patient association) | Held conflict instances shown or sent through the new endpoints | DCM-SRS-091 (same visibility rules as C-FIND), -092 (held instances never sent) | No increase. Verified (card WT-0.2/4): the study query is compared with C-FIND on the same node at every level, with visibility hidden and flagged; over HTTP a held instance is returned by no query; a send never contains a conflict instance. Six mutants of the hidden-conflict filter (in the query function, in the visibility setting and in the index) and two of the send filter were each caught |
| HAZ-014 (wrong correction) | Resolve sent to the wrong node | DCM-SRS-097 (identity check, also offered to the conflict resolve endpoint) | Improves. Implemented (card WT-0.2/3): the conflict plan and resolve endpoints accept `expect_node_id` and `expect_ae_title` |

### Verification of the implemented controls (card WT-0.2/3)

| Hazard | Control now implemented | Type | Verification of effectiveness |
|---|---|---|---|
| HAZ-015 | Reload through the node's one reloader; plan with the difference; confirmation and digest of an unchanged plan; warnings for an emptied allow-list and a removed or retargeted destination must be acknowledged; invalid configuration changes nothing (DCM-SRS-088, part of -095) | Protective | `TestReloadPlanAndApply`, `TestReloadRefusesAStalePlan`, `TestReloadWithBadConfigKeepsRunningSettings`, `TestReloadWarningsMustBeAcknowledged`, `TestNormalizeAllowedAEsMatchesSetAllowedAEs`; through the real reloader and gateway: `TestControlActionsWiring` (three bad files, each refused by plan, by reload and by the SIGHUP path; gateway settings unchanged; C-ECHO still answered). Mutation-checked |
| HAZ-015, -016, -014 | Expected node ID and AE title checked on every action, plan and conflict `POST` (DCM-SRS-097) | Protective | `TestActionsRefuseWrongInstance`, `TestConflictEndpointsRefuseWrongInstance` (plan and resolve: 409, conflict still open) |
| HAZ-008 | Operator, reason and request ID required; audit record before the action, no record no action; outcome record; refusal record (DCM-SRS-086) | Protective | `TestActionsRequireTokenOperatorReasonAndRequestID`, `TestActionAuditRecords`, `TestActionNotDoneWhenAuditCannotBeWritten` |
| HAZ-015, -016 | Request ID remembered for 10 minutes: a double submit acts once (DCM-SRS-086) | Protective | `TestActionRequestIDIsRememberedForTenMinutes`, `TestActionInProgressAndBoundedMemory` |
| HAZ-001, -009 | Backup only into the configured directory, name made by LibreDCM, exclusive create, no download or delete (DCM-SRS-090) | Design | `TestBackupAction` (snapshot opens as an index with the data; checksum matches), `TestBackupAcceptsNoPathFromTheClient`, `TestBackupRefusals`, `TestBackupListAndNoDownloadOrDelete` |
| HAZ-004 | Free-space check; one backup, one activation and two C-ECHO tests at a time; timeouts; bounded request-ID table (DCM-SRS-090, part of -096) | Protective | `TestBackupRefusals`, `TestBackupTimeout`, `TestEchoConcurrencyLimit`, `TestLicenceActivateOneAtATime`, `TestActionInProgressAndBoundedMemory` |
| HAZ-010 | Licence endpoints call the licensing code from the control package only; the gateway imports neither (DCM-SRS-089, -058) | Inherent (architecture) | `TestGatewayHasNoLicenseDependency` (extended to the control package); `TestControlActionsWiring` runs a real C-ECHO against the node's DICOM listener while reloads are blocked by the licence, after a refused licence install and after a failed activation, in the source build and in the official build |
| HAZ-012 | C-ECHO: Verification only, validated target, fixed error texts (DCM-SRS-087) | Protective | `TestDICOMEchoProposesVerificationOnlyAndNamesTheStage`, `TestEchoAction`, `TestEchoRefusesUnknownAndMalformedTargets` |

**Risks from these controls, found while building them** (ISO 14971 §7.5):

- An action that stops with an internal fault could leave its request ID
  "in progress" for good and fill the table. Controlled: the fault is
  caught, recorded as a serious failure and remembered as the result
  (`TestActionPanicIsRecordedAndNotRepeated`).
- The request-ID table is bounded (1000). When it is full, new actions are
  refused for up to 10 minutes. Safe direction: DICOM services are not
  touched, and SIGHUP and the command line still work on the host.
- A reload through the API asks for acknowledged warnings; SIGHUP does not.
  This is deliberate: the person at the host edited the file. The plan
  digest makes the API reload apply exactly what the administrator saw.
- The backup refuses when free space cannot be read (for example on a file
  system that does not report it). The command-line `-backup` stays
  available on the host.
- A licence acknowledgement through the API lifts the reload block as the
  command line does. No new path to DICOM services was added.

**New risks from the new controls** (ISO 14971 §7.5): the identity check and
the plan digest can refuse a correct action after a harmless change (the
administrator repeats it: inconvenience only); the overlay file adds a
second place where settings live (controlled: `GET /control/v1/config` shows
the source of every setting, and the overlay never touches restart-required
settings); the audit-before-action rule blocks actions when logging fails
(safe direction).

### Verification of the implemented controls (card WT-0.2/4)

**DRAFT for approval (2026-11-16).** Test names are in
`modules/dicom/internal/gateway/admin_test.go`,
`modules/dicom/internal/control/data_test.go` and `configedit_test.go`, and
`modules/dicom/cmd/libredcm/overlay_test.go` and
`control_data_wiring_test.go`. Each control below was also broken on
purpose (148 mutants in all); every mutant made a named test fail.

| Hazard | Control now implemented | Type | Verification of effectiveness |
|---|---|---|---|
| HAZ-015 | Only the two live settings can be edited; every other setting of the node is refused, whatever its value (DCM-SRS-093) | Inherent (no code path writes another setting) | `TestConfigEditRefusesEverythingElse` (every other setting of the node, alone and next to a valid edit: `422`, nothing written), `TestOverlayFileHoldsOnlyLiveSettings` (18 overlay files with another key, version or a bad entry: not loaded, node does not start, reload keeps the running settings), `TestControlDataAndConfigWiring` (TLS, AE title, token, overlay path, conflict visibility refused through the real wiring) |
| HAZ-015 | The change is validated by the normal loader before it is written; written atomically to the overlay; `config.yaml` never rewritten; a failed step puts the overlay and the previous version back and leaves the running settings (DCM-SRS-093) | Protective | `TestOverlayEditReloadRestartAndRollback` (in force at once, after a reload and after a restart; `config.yaml` byte-identical; file mode 0600; no temporary file left), `TestOverlayFailedChangeLeavesEverythingAsItWas` (12 failures, each with and without an existing overlay: refused check, invalid rule or destination, licence block, broken `config.yaml`, write error, half-written file, other content written, gateway refusing the settings; files and running settings compared before and after), `TestWriteFileAtomic`, `TestConfigEditFailuresChangeNothing` |
| HAZ-015 | Plan with the exact difference; apply only with confirmation and the digest of that plan; the digest covers the kind of plan, the running settings and the new settings (DCM-SRS-093, -088) | Protective | `TestConfigEditPlanAndApply` (six refusals: no confirmation, no digest, wrong digest, digest of another edit, digest of a reload plan to the same settings; a repeated request writes once; an applied plan is stale), `TestConfigRollback`, `TestControlDataAndConfigWiring` |
| HAZ-015, HAZ-007, HAZ-005 | Warnings that must be acknowledged by code: allow-list emptied; allow-list that would refuse a calling AE active in the last 24 hours; destination removed or pointed elsewhere (DCM-SRS-095) | Protective | `TestConfigEditWarnings` (13 edits; each warning refused with no, wrong and incomplete acknowledgement; a peer older than 24 hours and a peer that was already refused are not counted; a peer kept by a network rule gives no warning; a list that starts to be enforced is checked; with audit records off the warning is always given), `TestAllowListPermits`, `TestConfigRollback`, `TestControlDataAndConfigWiring` (allow-list not emptied without the acknowledgement; the gateway then refuses an AE that is not listed, on the wire) |
| HAZ-015 | Previous overlay version kept with time, operator and reason; rollback through the same plan and apply (DCM-SRS-094) | Protective (recovery) | `TestOverlayEditReloadRestartAndRollback` (rollback and rollback of the rollback; history), `TestConfigRollback` (same guard; warnings apply; no previous version refused), `TestControlDataAndConfigWiring` (rollback after a restart) |
| HAZ-015 | Settings fixed by the environment cannot be edited; the environment still wins over the overlay (DCM-SRS-093, -050) | Protective | `TestConfigEditSetByEnvironment`, `TestOverlayLoadOrder` |
| HAZ-016 | Send only to a configured destination by name; no address field (DCM-SRS-092, -011) | Inherent | `TestSendPlanAndSend` (unknown and empty destination refused), `TestSendRefusals` (a `host`, `port` or `path` field refused as unknown), `TestControlDataAndConfigWiring` |
| HAZ-016 | Patient ID required and equal to the indexed Patient ID of the study; chosen series and instances must belong to the study (DCM-SRS-092) | Protective | `TestSendPlanAndSend` (12 refusals, among them another patient, other letter case, wildcard, series and instances of another study; nothing arrives at the destination), `TestSendNeverSendsHeldInstances` |
| HAZ-016 | Plan shows destination address, patient, study and instance count; the send goes out only for the digest of that plan and sends exactly its instances (DCM-SRS-092) | Protective | `TestSendPlanAndSend` (plan content; a new instance changes the digest; the instances that arrive are those of the plan), `TestSendPlanThenApply` (seven refusals: no confirmation, no digest, wrong digest, digest used for another destination, study or patient, study changed after the plan; a repeated request sends once), `TestControlDataAndConfigWiring` (real DICOM destination) |
| HAZ-016, HAZ-008 | Outcome per sub-operation; a send in which nothing arrived answers `502`; audit record with operator, destination, study and patient (DCM-SRS-092, -086) | Protective (information for the user) | `TestSendOutcomes`, `TestSendPlanAndSend` (dead destination: every instance failed, by UID), `TestSendPlanThenApply` (audit record; patient ID omitted when the site set so) |
| HAZ-013 | A send never contains an instance recorded as a patient conflict (DCM-SRS-092) | Inherent (the send reads indexed instances only) | `TestSendNeverSendsHeldInstances` (visibility hidden and flagged; the held patient and a held instance named in the request are refused; the destination receives the indexed instances of the indexed patient only) |
| HAZ-013, HAZ-002 | Study query through the query function and the visibility rules of C-FIND (DCM-SRS-091) | Inherent (one shared function) | `TestQueryIndexMatchesCFind` (23 queries at four levels compared with a C-FIND on the same node), `TestQueryIndexConflictVisibility` (hidden and flagged, compared with C-FIND), `TestStudiesHideHeldConflictInstances` (over HTTP: 12 queries, the held instance in none), `TestControlDataAndConfigWiring` |
| HAZ-002 | Never more rows than asked for: no listing without a key; unknown key and key of a lower level refused; paging says when more rows exist (DCM-SRS-091) | Protective | `TestQueryIndexMatchesCFind` (11 refusals), `TestQueryIndexPages`, `TestStudiesPagingAndBounds` (450 rows in five pages, each row once; cursor bound to its query), `TestConformance` (limit and offset on SQLite and PostgreSQL) |
| HAZ-009, HAZ-008 | Operator required; one audit record per query, written before rows are returned; query values in no other log line; patient values omitted when the site set so (DCM-SRS-091) | Protective | `TestStudiesNeedTokenAndOperator`, `TestStudiesAuditAndLogSecrecy` (the log ring searched for the query values; no audit record, no rows) |
| HAZ-004 | One send and four study queries at a time; 5000 instances per send; 200 rows per page and 10 000 rows deep; timeouts; bounded overlay (200 destinations, 500 rules, 1 MiB) (DCM-SRS-096) | Protective | `TestSendOneAtATime`, `TestStudiesConcurrencyLimit`, `TestSendPlanAndSend` (limit lowered to 2 in the test: 3 refused, 2 sent), `TestStudiesPagingAndBounds`, `TestOverlayFileHoldsOnlyLiveSettings` (file too large), `TestOverlayFailedChangeLeavesEverythingAsItWas` (too many destinations) |

**Risks from these controls, found while building them** (ISO 14971 §7.5):

- **Two places hold the same setting.** After the first edit, the overlay
  replaces the value of `config.yaml` as a whole. An administrator who then
  edits `config.yaml` on the host sees no effect. Controlled by
  information: `GET /control/v1/config`, `/remote-aes` and `/allow-list`
  name the source `overlay`; the overlay file starts with a comment that
  says what it is and how to drop it; INSTALL.md describes it. Residual:
  confusion and a short delay, no wrong data flow (the stricter of the two
  views is the one the administrator approved last).
- **A damaged overlay file stops the node from starting** (availability,
  HAZ-004). Chosen deliberately over starting with `config.yaml` alone,
  which could be the wider allow-list (HAZ-007). The file is written
  atomically and read back before it is used, so this takes a manual edit
  or a damaged disk. The error message names the file. Decision D25.
- **The "active peer" warning depends on the audit ring**, which is empty
  after a restart and holds 1000 records. A modality that was active before
  a restart, or whose records were pushed out by many others, is not
  named. The emptied-list and removed-destination warnings do not depend on
  it. With audit records off the warning is always given (safe direction).
- **Sends and queries load the node.** One send at a time and bounded
  queries; a long send holds the send slot for up to 30 minutes, and a
  second person gets `429`. DICOM services have their own limits and are
  not blocked by it.
- **Accepted separate studies cannot be sent from the console** (D26).
  Safe direction for HAZ-013; a delay for the reader in that rare case,
  who can still retrieve the study with C-MOVE.
- **A regression found by the existing fuzz corpus while building this
  card:** making C-FIND and the study query share one function first moved
  a look-up in front of the "known key" check, so an identifier with an
  element that has no keyword (a private tag) made the query handler
  fail. It never left the development branch. The check is back in front
  and a mutant that removes it is caught (`FuzzCFindIdentifierBytes`).

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
| HAZ-013 | Conflict detection in the index transaction; existing associations never change (DCM-SRS-070) | Design | `TestUpsertNeverReassignsStudy`, `TestUpsertNeverMovesSeriesOrInstance` (the ANOM-002/009 probes), `TestConcurrentFirstRegistration` (8 writers, SQLite and PostgreSQL); mutation-checked (removing the check fails them) |
| HAZ-013 | Quarantine / reject / accept_separate, separate storage key, hidden by default, retrieve only with Patient ID (DCM-SRS-071–073) | Design | `TestConflictQuarantineHidden` (archived file of a resent SOP instance not overwritten), `TestConflictReject`, `TestConflictAcceptSeparate`, `TestConflictFlaggedCoerced`, `TestConflictRecordAndVisibility`; mutation-checked |
| HAZ-013 | Audit, log, metrics (DCM-SRS-074); first demographics kept (-076) | Protective (detection) | `TestConflictQuarantineHidden`, `TestDemographicsMismatchKeepsFirst`, `TestFirstDemographicsKept` |
| HAZ-013, -003 | B000_ coercion by byte splicing, pixel data unchanged (DCM-SRS-075) | Design | `TestCoercePatientID`, `TestCoerceKeepsHistoryAndSkipsNoop`, `TestCoerceUndefinedLengthHistoryAndEncapsulated`, `TestCoerceRefusesInvalid`; mutation-checked |
| HAZ-014 | Token-only control API, plan before change, operator/reason/confirmation, audit (DCM-SRS-077); corrections recorded in the object (-078); reject keeps data (-079) | Design / protective | `TestControlRequiresToken`, `TestControlAPI`, `TestResolveValidation`, `TestResolveCorrectIncoming`, `TestResolveCorrectExisting`, `TestResolveReject` |

**New risks introduced by controls** (ISO 14971 §7.5), checked 2026-11-10:
the fail-closed allow-list can stop all traffic after a typo (mitigated:
audit record, metric and log name the cause; reload keeps the last good
list); the Part 10 guard can refuse an instance (only values that cannot be
written correctly); the licence reminders can block a configuration reload
(HAZ-010 residual). None creates an unacceptable risk.

Added 2026-11-10 (issue #14): the HAZ-013 controls introduce (1) images held
back from viewers (delayed availability, HAZ-004 class): mitigated by the
open-conflicts metric and the Libre WT queue; (2) a data-modifying
administrator function: analysed as HAZ-014; (3) the `reject` policy can
make a modality hold images: chosen per site, A7xx keeps them queued at the
sender.

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
1. ~~Control HAZ-013 (ANOM-002, ANOM-009)~~: implemented 2026-11-10 (DCM-SRS-070…079, issue #14);
   HAZ-014 added for the correction function.
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
