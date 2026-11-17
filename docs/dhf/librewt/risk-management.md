# Libre WT risk management file (ISO 14971:2019)

| | |
|---|---|
| Document | DHF-WT-RMF |
| Status | **DRAFT** (2026-11-10, prepared by Hermes/Claude for human review); v0.2 analysis added 2026-11-14, **DRAFT for approval**; verification of the first v0.2 controls added 2026-11-16 (card WT-0.2/5) |
| Procedure | QMS-SOP-003 (same scales and matrix as LibreDCM) |

Libre WT's main hazard is the one it exists to control: a study filed under
the wrong patient. The system-level hazards HAZ-013 (silent reassignment)
and HAZ-014 (administrator correction applied wrongly) are analysed in
`../libredcm/risk-management.md`; this file adds the hazards specific to the
console.

| ID | Hazardous situation | Harm | Causes | S | Initial P | Risk controls | Residual P | Residual |
|---|---|---|---|---|---|---|---|---|
| WT-HAZ-01 (→ HAZ-014) | Admin applies a correction to the wrong exception or with the wrong action | Wrong-patient diagnosis | Similar names, misreading, double click, stale page | S4 | P3 | WT-SRS-008 (side-by-side, differences highlighted), -009 (plan shown, single-use confirmation bound to session/exception/action, approve box, reason, plan re-checked before sending), -004 (Admin only), -011 (audit); LibreDCM DCM-SRS-077/078 (token, confirm, audit, previous values kept in every file) | P2 | R |
| WT-HAZ-02 | Unauthorised person resolves exceptions (stolen password, session hijack, CSRF, default credentials) | Wrong-patient diagnosis; privacy harm | Weak passwords, open network, missing CSRF protection | S4 | P3 | WT-SRS-001 (CSP, HTTPS, headers), -002 (no default credentials), -003 (argon2id, policy), -004 (CSRF, rate limits), -005 (cookie flags, timeouts, end on account change), -012 | P2 | R |
| WT-HAZ-03 | Exceptions not noticed; held images stay unavailable | Delayed diagnosis | Nobody opens the console | S3 | P3 | LibreDCM metric `libre_dcm_patient_conflicts_open` (DCM-SRS-074); WT-SRS-007 | P3 | R (alerting rules to ship with monitoring guidance) |
| WT-HAZ-04 | LibreDCM admin token disclosed by Libre WT | Unauthorised control-API use (HAZ-014) | Token shown in pages or logs | S4 | P2 | WT-SRS-006 (token never shown; file option); test checks pages contain no token | P1 | A |

Residual risks to address before clinical use: TOTP/second factor (planned),
OIDC/LDAP (planned), per-user identity towards LibreDCM (today the shared
admin token plus the operator name asserted by Libre WT), alert rules for open
exceptions.

## Libre WT 0.2: risk analysis of the new functions (DRAFT for approval, 2026-11-14)

Prepared by Hermes (Claude) for human review, before coding (design input,
issue #20). The functions are not implemented yet. The risk controls are
`Draft` requirements (WT-SRS-013…036, DCM-SRS-080…097). "Target residual" is
what is expected once the controls are implemented and verified; each row is
confirmed, with its verification evidence, in the pull request that
implements it. Scales and matrix: QMS-SOP-003, as for LibreDCM. Initial P is
without the controls listed; external controls (trained administrators,
modalities that keep their queue, radiologists who check the patient banner)
are credited.

System-level hazards are in `../libredcm/risk-management.md`: §2 (HAZ-001…014)
and §2a (HAZ-015 remote configuration change, HAZ-016 study sent wrongly,
both new for this work).

### Hazards

| ID | Hazardous situation | Harm | Causes | S | Initial P | Initial | Risk controls (Draft) | Target residual P | Target residual |
|---|---|---|---|---|---|---|---|---|---|
| WT-HAZ-05 (→ HAZ-015, HAZ-016, HAZ-014) | **Wrong instance targeted**: an action meant for one LibreDCM node is done on another | Depends on the action: modalities refused or images sent wrongly on a node that was working (delayed diagnosis, privacy harm); a correction applied on the wrong archive | Similar names; several tabs; a "selected instance" remembered from earlier; address or DNS now reaching another node; connection pointed at a load balancer | S4 | P3 | U | WT-SRS-014 (instance in every address, banner on every page, no selected instance in the session), -015 (identity remembered, change warned, expected identity sent with every action), -031 (confirmation bound to the instance), -013 (unique names); LibreDCM DCM-SRS-097 (refuses on identity mismatch) | P1 | R |
| WT-HAZ-06 (→ HAZ-015, HAZ-005, HAZ-007, HAZ-004) | **Configuration change breaks or opens DICOM traffic**: allow-list rule or destination added, changed or removed wrongly; reload or rollback with an unexpected result | Modality refused: images wait at the modality, exam delayed or repeated; study can't be sent to the reader; with an emptied allow-list any system may store or retrieve (wrong-patient images, privacy harm) | Typo; removing a device still in use; emptying the list; misunderstanding the rule format; stale page; double submit | S4 | P3 | U | WT-SRS-021 (plan page with exact difference; LibreDCM validates; each weakening warning has its own box), -022 (reload and rollback through a plan), -020 (empty list stated clearly), -031 (approval, reason, single-use confirmation, plan digest), -024 (C-ECHO test to check afterwards), -034 (Admin only); LibreDCM DCM-SRS-093…095, -088; restart-required settings are read-only (WT-SRS-019) | P2 | R |
| WT-HAZ-07 (→ HAZ-016, HAZ-005, HAZ-009) | **Wrong study sent, or study sent to the wrong AE** | Privacy harm; confusion at the receiver when the wrong patient's study arrives; the intended reader waits | Similar patient names; wrong row clicked; wrong destination chosen; double submit | S3 | P3 | R | WT-SRS-026 (destination from the configured list only; warning page with patient, study, counts and destination address), -031, -034 (Admin only), -025 (search shows Patient ID, name and date together); LibreDCM DCM-SRS-092 (Patient ID must match, plan digest, held instances never sent) | P2 | R |
| WT-HAZ-08 (→ HAZ-009) | **Secret disclosed** through the console: admin token, S3 keys, database password, licence key shown on a page, in an address, in the log or in the audit log | Unauthorised access to the archive or its storage: privacy harm for many patients, tampering | Configuration shown unfiltered; error message that repeats a DSN; key typed into a form and logged; token over plain HTTP | S4 | P3 | U | WT-SRS-032 (never displayed, logged or audited; not in addresses), -019 (redacted settings shown only as set or not set), -027 (licence key not logged), -036 (plain-HTTP warning), -006 (token from file, 0.1); LibreDCM DCM-SRS-082 (redaction at the source: Libre WT never receives the secrets) | P1 | R |
| WT-HAZ-09 (→ HAZ-010) | **Licence mis-action**: wrong licence installed, notice acknowledged by someone who did not mean to, or the user stops work because the page seems to say images will stop | Mostly commercial. Safety relevant only if a licence action could disturb DICOM services, or if staff divert patients because they believe the archive is down | Wrong file; wrong node; unclear wording | S3 | P2 | R | WT-SRS-027 (statement that DICOM keeps running; acknowledgement by an Admin under their own typed name; refused licence leaves the old one), -031, -034; LibreDCM DCM-SRS-089, -058 (licensing can't reach the gateway, architecture test) | P1 | A |
| WT-HAZ-10 (→ HAZ-001, HAZ-004, HAZ-009) | **Backup harms the node**: it overwrites the index or an older backup, fills the disk, or the user believes images are backed up | Index damaged (images not findable); node stops storing when the disk is full; no usable backup when needed | Path chosen by the user; repeated backups; misunderstanding | S3 | P3 | R | WT-SRS-028 (no path or name field, no download, no delete; statement that images are not included), -031; LibreDCM DCM-SRS-090 (own directory and file name, exclusive create, free-space check, one at a time) | P1 | A |
| WT-HAZ-11 (→ HAZ-004, HAZ-006) | **Status misleads**: the console shows a node as healthy when it is not, or shows old values as current | Problem not noticed: images unavailable for longer | Cached values; unreachable node shown with its last values; counters misread; unsupported function shown as an error | S3 | P3 | R | WT-SRS-016 (unreachable shown as such, with time of last contact), -017 (time of reading on the page; each readiness check shown), -018 (unsupported functions marked), -035 | P2 | R |
| WT-HAZ-12 (→ HAZ-015) | **Cluster nodes drift apart**: a change is made on one node and not on the others | A modality or a destination works on some nodes only: stores or retrieves fail now and then (delay) | Each node has its own settings; the user forgets a node | S3 | P3 | R | WT-SRS-023 (flag when the settings digest or the version differs within a cluster label); LibreDCM DCM-SRS-097 (digest) | P2 | R |
| WT-HAZ-13 (→ HAZ-009) | **Patient data shown to people who do not need it** through study search, audit records or logs | Privacy harm | Roles too wide; shared screens; cached pages | S3 | P3 | R | WT-SRS-034 (roles: search and logs Operator and Admin, audit records Admin), -025 (no search without a value; every search audited), -033 (page openings audited), -001 (no-store, 0.1), -005 (idle timeout, 0.1); LibreDCM DCM-SRS-084 (patient-ID omission honoured), -091 | P2 | R |
| WT-HAZ-14 (→ HAZ-012) | **C-ECHO test misused** to probe the hospital network from the LibreDCM host | Security only: an insider or a stolen session learns which hosts answer | Free address field | S2 | P3 | A | WT-SRS-024 (free address Admin only; Operators only configured destinations), -031 (reason, audit); LibreDCM DCM-SRS-087 | P2 | A |
| WT-HAZ-15 (→ HAZ-015) | **Reload or rollback activates settings nobody reviewed**: the file on the host was edited by someone else, or the previous version is not what the user thinks | As WT-HAZ-06 | Hand edits on the host; several administrators | S4 | P2 | R | WT-SRS-022 (plan page shows the difference between running and to-be-loaded settings before reload or rollback); LibreDCM DCM-SRS-088, -094, plan digest | P1 | R |
| WT-HAZ-16 (→ HAZ-004) | **Console load disturbs DICOM service**: frequent status reads, large searches, repeated sends or backups slow the node | Slower or failed stores and retrieves (delay) | Many open consoles; polling too often; large queries | S3 | P2 | R | Status read on page load only, with a short cache per instance shared by all users (design, `architecture.md` §4); WT-SRS-025 (bounded search); LibreDCM DCM-SRS-096 | P1 | A |
| WT-HAZ-17 (→ HAZ-008) | **Administrative actions can't be traced** to a person | Undetected misuse; no basis for investigation | Missing audit entries; shared accounts; audit write fails silently | S2 | P3 | R | WT-SRS-033 (who, instance, action, reason, outcome; approval written before sending, no send without it), -031 (operator name and reason forwarded), -029; LibreDCM DCM-SRS-086 | P2 | A |

### Coverage: every new function and its hazards

| New function | Hazards analysed | Changes the archive? |
|---|---|---|
| Multi-instance connections, fleet page | WT-HAZ-05, -08, -11, -12, -16 | No |
| Dashboard, status, about | WT-HAZ-11, -16 | No |
| Configuration view | WT-HAZ-08 | No |
| Destinations and allow-list view | WT-HAZ-06 (misreading), -08 | No |
| Change destinations or allow-list | WT-HAZ-05, -06, -12, -17 | Yes |
| Reload, rollback | WT-HAZ-05, -06, -15, -17 | Yes |
| C-ECHO test | WT-HAZ-14, -05 | No |
| Study search | WT-HAZ-13, -16 | No |
| Send study | WT-HAZ-05, -07, -17 | Yes |
| Licence status, acknowledgement, request, installation, activation | WT-HAZ-09, -08, -05 | Licence only |
| Backup | WT-HAZ-10, -05 | New file only |
| LibreDCM audit records, logs | WT-HAZ-13, -08, -17 | No |
| Study Exceptions per instance | WT-HAZ-01, -05 | Yes (as in 0.1) |
| Not offered: restart, restart-required settings, certificate generation, free send address, backup path | Removed by design (inherent safety): HAZ-004, HAZ-001, HAZ-005 | — |

### New risks from the controls (ISO 14971 §7.5)

- Warning pages and boxes on every action can lead to "click-through"
  habits. Limit: plain pages for read-only functions; boxes only where a
  risk control is weakened; the reason field is free text and required.
- The identity check can block work after a planned node replacement. An
  Admin accepts the new identity with a reason (audited).
- Showing unreachable nodes without values hides the last known state. The
  time of last contact is shown instead.

### Residual risks to carry forward

Per-user identity towards LibreDCM (shared admin token; the operator name is
asserted by Libre WT); second factor for Admin; HTTPS between Libre WT and
LibreDCM is optional; a syntactically valid but wrong destination address
can't be detected before a C-ECHO or a send; cluster-wide changes are made
node by node. None is new in kind; each is listed as an open decision or a
gap in `architecture.md` §8 and `cybersecurity.md`.

## Libre WT 0.2: controls implemented in card WT-0.2/5 (DRAFT for approval, 2026-11-16)

Prepared by Hermes (Claude) for human review. Card WT-0.2/5 built the
connections to several instances, the fleet page, dashboard, status and
about pages, and moved Study Exceptions under the instance. The table gives,
for each hazard touched, the controls that now exist and the tests that
verify them. Controls of later cards stay `Draft`; the target residual risk
of a hazard is confirmed only when all its controls are in.

| Hazard | Controls now implemented | Verification (tests) | State of the hazard |
|---|---|---|---|
| WT-HAZ-05 wrong instance targeted | WT-SRS-013 (unique names, own token per instance), -014 (instance only from the address, banner, no selected instance), -015 (identity remembered; change warned; no action while changed or unreachable; expected identity sent; LibreDCM's refusal shown). The confirmation of an edit is bound to the instance (part of WT-SRS-031, still `Draft`) | `TestConnectionsValidated`, `TestInstancesConfiguration`, `TestInstanceFromAddressOnly`, `TestDashboardStatusAndAbout`, `TestIdentityRememberedAndChangeDetected`, `TestIdentityChangeBlocksActionsUntilAccepted`, `TestIdentityAcceptedThenActionsResume`, `TestLibreDCMRefusesWrongNode`, `TestInstanceIdentityStore`, `TestClientStatusDiscoveryAndExpectedIdentity`, `TestAgainstRealLibreDCM` (the real node refuses another node's name) | Controls in place for the one action that exists (resolve). Confirmed again for each new action in its card |
| WT-HAZ-08 secret disclosed | WT-SRS-036 (plain-HTTP warning on pages and at startup). Tokens stay out of pages, log and errors (WT-SRS-006) | `TestPlainHTTPRemote`, `TestPlainHTTPWarningOnPages`, `TestInstancesConfiguration`, `TestFleetPage`, `TestUsersAuditAndDCMDown` | Open: WT-SRS-019, -027, -032 come with later cards |
| WT-HAZ-11 status misleads | WT-SRS-016 (unreachable shown as such, last contact, no old values), -017 (time of reading, every readiness check), -018 (unsupported functions marked), -035 | `TestUnreachableShowsNoOldValues`, `TestStatusReadIsCachedAndShared`, `TestFleetPage`, `TestFleetNeverReached`, `TestDashboardStatusAndAbout`, `TestFeaturesAndLegacyNode`, `TestOlderLibreDCM`, `TestAgainstRealLibreDCM` | Controls complete. Target residual P2 (R) confirmed for review |
| WT-HAZ-12 cluster nodes drift apart | WT-SRS-023 (flags for settings digest and version) | `TestDivergenceWithinCluster`, `TestFleetPage` | Control complete. Target residual P2 (R) confirmed for review |
| WT-HAZ-16 console load | Status read on page load only; reading shared by all users for 5 seconds; a failed reading is kept as long; 5-second timeout | `TestStatusReadIsCachedAndShared`, `TestClientStatusDiscoveryAndExpectedIdentity` | Open: bounded search comes with its card |
| WT-HAZ-01, WT-HAZ-02 (0.1) | Unchanged. Study Exceptions run the 0.1 flow per instance; every address has one row in the role table | `TestReviewAndApproveEdit`, `TestNoAuditNoEdit`, `TestViewerRole`, `TestRoleTable` | Unchanged |

Mutation check (2026-11-16): each control above was broken on purpose, one
at a time, and the tests were run. The result is in the pull request of
card WT-0.2/5.

### New risks from this implementation (ISO 14971 §7.5)

| Risk | Assessment | Handling |
|---|---|---|
| The identity is trusted on first use. An address that is wrong from the first day is remembered as "right" | The banner shows node ID and AE title on every page, and the administrator who adds the instance sees them at once. LibreDCM still checks the identity Libre WT sends. S4, P1 | Accepted for 0.2; open decision D33 (`architecture.md` §8) offers the identity in the configuration file |
| An older LibreDCM gets no expected identity with a resolve | Same protection as Libre WT 0.1 had. Libre WT's own identity check on the node ID from `/version` still blocks actions when the node ID changes | Accepted; D35. Ends when the node is upgraded |
| The identity check blocks Study Exceptions after a planned node replacement or AE title change until an Admin accepts | Held images stay unavailable a little longer (WT-HAZ-03). The warning is on every page of the instance and names the cause | Accepted; the acceptance is one audited step |
| The fleet page waits up to 5 seconds when a node does not answer | Slower page, no wrong information | Accepted |
| An instance removed from the configuration and added again under the same name keeps its remembered identity | Intended: the name is the key. A new node behind an old name shows the identity warning | Accepted |

## Libre WT 0.2: controls implemented in card WT-0.2/6 (DRAFT for approval, 2026-11-16)

Prepared by Hermes (Claude) for human review. Card WT-0.2/6 built the
configuration view, the destinations and allow-list views, the edit of
these two live settings, reload, rollback and the C-ECHO test, on one
shared action flow (`architecture.md` §5, "As built").

| Hazard | Controls now implemented | Verification (tests) | State of the hazard |
|---|---|---|---|
| WT-HAZ-05 wrong instance targeted | As in card WT-0.2/5, now for every new action: instance only from the address; banner and instance named on each warning page; confirmation bound to session, instance, action and the expected node; no review and no apply while the node differs or can't be reached; expected node ID and AE title sent with every plan and action; an answer from another node is not shown | `TestConfigEditFlow` (confirmation of instance a refused on b), `TestConfigActionsNeedTheRememberedNode`, `TestReadAnsweredByAnotherNode`, `TestConfigAgainstRealLibreDCM` | Controls in place for all actions built so far |
| WT-HAZ-06 configuration change breaks or opens DICOM traffic | WT-SRS-021 (plan from LibreDCM; warning page with the exact difference; one box per warning that must be acknowledged; LibreDCM's refusal shown with its reason, the typed values kept), -022 (reload and rollback through a plan page; the version that comes back is named), -020 (empty allow-list shown as a warning; invalid list shown as "refuses every association"), -031 (approval box, reason, single-use confirmation; the plan digest shown is the one sent; what is sent is kept on the server from the review), -034 (Admin only), -019 (restart-required settings read-only), -024 (C-ECHO test to check a destination) | `TestConfigEditFlow`, `TestConfigEditWarningsAndRefusals`, `TestPlanChangedAfterReview`, `TestReloadAndRollback`, `TestDestinationsAndAllowList`, `TestConfigRolesAndCSRF`, `TestEcho`, `TestConfigAgainstRealLibreDCM` | Controls complete. Target residual P2 (R) confirmed for review |
| WT-HAZ-08 secret disclosed | WT-SRS-019 and -032 for the configuration page: redacted settings shown only as set, not set or hidden; the client also drops any value of a redacted setting; LibreDCM text shown as text | `TestConfigPage`, `TestConfigDropsRedactedValues`, `TestConfigAgainstRealLibreDCM` (admin token not on the page) | Open: licence key handling (WT-SRS-027) comes with card WT-0.2/7 |
| WT-HAZ-14 C-ECHO test misused | WT-SRS-024: Operators only configured destinations (checked in the review and again at apply); free address Admin only; host, port and AE title checked before anything is sent; reason; audit on both sides | `TestEcho`, `TestConfigRolesAndCSRF`, `TestConfigAgainstRealLibreDCM` (LibreDCM's record names the operator and reason) | Control complete. Target residual P2 (A) confirmed for review |
| WT-HAZ-15 reload or rollback activates settings nobody reviewed | WT-SRS-022: the plan page shows the difference between the running and the to-be-loaded settings; the rollback page names who made the version that comes back, when and why; LibreDCM's plan digest is sent, so a file changed after the review is refused ("Nothing was changed") | `TestReloadAndRollback`, `TestPlanChangedAfterReview`, `TestConfigAgainstRealLibreDCM` | Control complete. Target residual P1 (R) confirmed for review |
| WT-HAZ-17 actions can't be traced | WT-SRS-033 for the new pages and actions: every opening of a configuration page (no record, no page); review, approval (before sending; no record, nothing sent) and result with instance, target, reason, request ID and outcome; `unknown` when LibreDCM gave no answer. WT-SRS-031: operator, reason and request ID sent to LibreDCM | `TestConfigPage`, `TestNoAuditNoConfigAction`, `TestNoAnswerMeansUnknownOutcome`, `TestConfigEditFlow`, `TestConfigAgainstRealLibreDCM` (LibreDCM's records carry the request ID Libre WT recorded) | Controls in place for all actions built so far |

Mutation check (2026-11-16): 37 mutants, each removing one control above;
the result is in the pull request of card WT-0.2/6.

### New risks from this implementation (ISO 14971 §7.5)

| Risk | Assessment | Handling |
|---|---|---|
| A page that can't write its audit record is not shown: a broken audit database hides the configuration from Operators | Availability of a view, not of DICOM. The audit database is the same one logins use; when it fails, actions fail too. S2, P1 | Accepted; D43 |
| An action without an answer is reported as "outcome not known"; the person may repeat it | A repeated change is refused by LibreDCM (the plan digest no longer fits the changed settings); a repeated C-ECHO is harmless | Accepted; D44 |
| Warning boxes on every weakening change can be ticked without reading | Each box carries LibreDCM's own sentence naming the device or the effect; the reason is required and audited | Accepted; as in the 0.2 analysis above |
| A free C-ECHO address can still be used by an Admin to probe hosts | Recorded on both sides with reason; Admin only | Accepted; WT-HAZ-14 residual |

## Libre WT 0.2: controls implemented in card WT-0.2/7 (DRAFT for approval, 2026-11-16)

Prepared by Hermes (Claude) for human review. Card WT-0.2/7 built the
licence page with its four actions and the index backup, on the shared
action flow (`architecture.md` §4 and §5, "As built"). No LibreDCM code
changed.

| Hazard | Controls now implemented | Verification (tests) | State of the hazard |
|---|---|---|---|
| WT-HAZ-09 licence mis-action | WT-SRS-027: every licence page, warning page and result page states that DICOM services keep running whatever the licence state; the acknowledgement is Admin only and needs the person to type their own user name, and LibreDCM records the logged-in user (D47); a licence file is shown on the warning page by name, size and SHA-256 and the approval is bound to that checksum; a file LibreDCM refuses is reported with LibreDCM's reason and "the installed licence is unchanged"; source builds offer no action. WT-SRS-031: warning page, single-use confirmation, approval box, reason, expected node, audit before sending | `TestLicencePage`, `TestLicenceAckFlow`, `TestLicenceInstallFlow`, `TestLicenceActivateAndRequest`, `TestLicenceBackupRolesAndCSRF`, `TestLicenceBackupGuards`, `TestLicenceAndBackupAgainstRealLibreDCM` | Controls complete for the console. Target residual P1 (A) confirmed for review. Not yet tested against a real unlicensed official build |
| WT-HAZ-08 secret disclosed (licence key) | WT-SRS-027, -032: the key is typed into a password field, kept only in memory with the confirmation (10 minutes at most), shown as `••••` and its last four characters, sent once; the client cuts any key end LibreDCM reports to four characters and removes the key (any case) from LibreDCM's error text before it is shown or audited; the activation request text (which holds the key, encoded) is handed out as a `text/plain` download only; the licence file content is in no page, log line or audit record | `TestLicenceActivateAndRequest`, `TestLicenceInstallFlow`, `TestLicencePage`, `TestLicenceKeyIsMasked`, `TestLicenceAndBackupRequests` | Controls complete for the console. Target residual P1 (R) confirmed for review |
| WT-HAZ-10 backup harms the node | WT-SRS-028: no path, file name, download or delete in the console (extra form fields are ignored; the request to LibreDCM holds the envelope only); every backup page says the backup holds the index only and images are not included; a node without a backup directory or with a PostgreSQL index gets no confirmation; LibreDCM's refusals (busy, not enough space) shown with its reason; result with file, size and SHA-256; history from the WT audit log. WT-SRS-031 as above | `TestBackupFlow`, `TestBackupWithoutAnswer`, `TestLicenceAndBackupRequests`, `TestLicenceAndBackupAgainstRealLibreDCM` (checksum on the page equals the SHA-256 of the file LibreDCM wrote) | Controls complete for the console. Target residual P1 (A) confirmed for review |
| WT-HAZ-05 wrong instance targeted | As in card WT-0.2/6, now for the licence and backup actions: no review and no apply while the node differs; expected node sent; LibreDCM's refusal reported; an answer that names another node is not shown | `TestLicenceBackupGuards` | Controls in place for all actions built so far |
| WT-HAZ-17 actions can't be traced | Review, approval (written before sending; no record, nothing sent) and result of every licence and backup action, with instance, reason, request ID and outcome (`unknown` without an answer); LibreDCM's own record of the backup names the Libre WT user and reason | `TestLicenceAckFlow`, `TestLicenceBackupGuards`, `TestBackupWithoutAnswer`, `TestLicenceAndBackupAgainstRealLibreDCM` | Controls in place for all actions built so far |

Mutation check (2026-11-16): the mutants and their result are in the pull
request of card WT-0.2/7.

### New risks from this implementation (ISO 14971 §7.5)

| Risk | Assessment | Handling |
|---|---|---|
| A file upload is a new way to send data to Libre WT | One address only; the body is capped at 192 KiB and held in memory (nothing written to disk); the file must be UTF-8 text of 128 KiB at most; it is passed to LibreDCM, which checks the signature; it is never rendered. S2, P1 | Accepted; D48, `cybersecurity.md` |
| The licence key and the licence file stay in Libre WT's memory between the warning page and the approval | At most 10 minutes, at most 20 approvals per session, dropped at logout; never written to disk, log or audit log. A memory dump of the Libre WT host would show it, as it would show the admin tokens | Accepted |
| An Operator can no longer acknowledge the licence notice (D47) | The block only stops configuration reloads, which are Admin actions; DICOM services are not affected | Accepted; open decision D47 |
| A backup that takes longer than 110 seconds is shown as "outcome not known" | LibreDCM finishes it and lists the file; the page says so; a new backup in the same second is refused by LibreDCM (no overwrite) | Accepted; D49 |

## Libre WT 0.2: controls implemented in card WT-0.2/8 (DRAFT for approval, 2026-11-16)

Prepared by Hermes (Claude) for human review. Card WT-0.2/8 built the
study search with the study and series pages, the send of a study to a
configured destination, the LibreDCM audit record page, the log page and
the CSV export (`architecture.md` §4, "As built (card WT-0.2/8)"). It is
the last action card: WT-SRS-031…034 are now `Active`. No LibreDCM code
changed.

| Hazard | Controls now implemented | Verification (tests) | State of the hazard |
|---|---|---|---|
| WT-HAZ-07 wrong study sent, or sent to the wrong AE | WT-SRS-026: Admin only (D52); destination chosen from the node's configured list and checked by LibreDCM; the request names the Patient ID the study page showed and LibreDCM refuses a mismatch; Libre WT issues no approval when LibreDCM's plan names another patient, study or destination, has no digest, holds no instance or comes from another node; the warning page shows the patient name, ID and birth date in the warning box, the table and the approval box, with study date, description, accession number, series and instance counts and the destination's name, AE title and address; the approval is bound to the study in the address and the request is taken from the server, never from the approval form; LibreDCM's plan digest is sent and a changed study is refused (nothing sent); no send offered for a study held as a patient conflict or listed under two patients. WT-SRS-031: single-use confirmation, approval box, reason, expected node, audit before sending | `TestSendFlow`, `TestSendWrongPatientGuard`, `TestSendResults`, `TestStudyAndSeriesPages`, `TestRoleTable`, `TestStudiesAuditLogsAgainstRealLibreDCM` | Controls complete for the console. Target residual P2 (R) confirmed for review. Not yet tested with a real study and a real destination in an automated test (manual run: see `manual-verification.md`) |
| WT-HAZ-13 patient data shown to people who do not need it | WT-SRS-034: study pages Operator and Admin, audit records Admin, logs Operator and Admin, exports Admin; tabs show only what the role may open. WT-SRS-025: no search without a value (`*` alone counts as none); values by `POST`, never in an address. WT-SRS-033: every opening of a search result, study, series, Study Exceptions, audit or log page and every export is audited first; no record, no page or file. `no-store` on every response (0.1) | `TestStudySearch`, `TestStudyAndSeriesPages`, `TestLibreDCMAuditPage`, `TestLogsPage`, `TestCSVExport`, `TestExceptionViewsAudited`, `TestRoleTable` | Controls complete. Target residual P2 (R) confirmed for review |
| WT-HAZ-17 actions can't be traced | WT-SRS-033: review, approval (before sending; no record, nothing sent) and result of every send, with patient, study, destination, reason, request ID, outcome, counts and failed instances; `unknown` without an answer; searches with their keys and values (D55); exports with filter and row count. LibreDCM's own `Query` record names the Libre WT user. WT-SRS-029: Admins read LibreDCM's recent audit records in the console | `TestSendFlow`, `TestSendResults`, `TestStudySearch`, `TestCSVExport`, `TestStudiesAuditLogsAgainstRealLibreDCM` | Controls complete for all actions. Target residual P2 (A) confirmed for review |
| WT-HAZ-11 status misleads (log page) | WT-SRS-030: the log page says it shows only the recent lines kept in memory, without audit records; the time of reading is shown | `TestLogsPage` | Controls complete |
| WT-HAZ-08 secret disclosed | WT-SRS-032 for the new pages: LibreDCM's text is shown as text; no token or key is in the new pages; filter values outside the offered choices are not sent; the export file name comes from the configured instance name and the time | `TestLibreDCMAuditPage`, `TestStudySearch`, `TestCSVExport` | Controls complete for the console |

Mutation check (2026-11-16): the mutants and their result are in the pull
request of card WT-0.2/8.

### New risks from this implementation (ISO 14971 §7.5)

| Risk | Assessment | Handling |
|---|---|---|
| A CSV export can carry a formula that a spreadsheet program runs (CSV injection): AE titles, log messages and patient names come from outside | Every cell that starts with `=`, `+`, `-`, `@`, tab or carriage return gets a leading apostrophe (WT-SRS-037). S2, P1 | Accepted; `TestCSVCell`, `TestCSVExport` |
| An exported file leaves the console's access control | Admin only; every export audited with filter and row count; the page says so. The file is then the site's responsibility. S3, P2 | Accepted; D53 |
| `librewt.db` now holds patient identifiers (search values in the audit log, D55) | The file was already sensitive (password hashes, actions). It lives next to the configuration, readable by the Libre WT service account only (installer). S3, P1 | Accepted; install guide says to protect the data folder |
| A send that takes longer than 110 seconds is shown as "outcome not known" | LibreDCM finishes it; the page gives the request ID to look up in LibreDCM's audit records; a new send needs a new review | Accepted; D54 |
| Study Exceptions pages are refused when the audit log can't be written (D56) | The audit log is a local SQLite file; when it fails, actions are refused already. DICOM services are not affected | Accepted |
| The card asked for Operators to send; the build keeps Admin only (D52) | Stricter than asked; no risk control weakened. A site with Operators who send today must use an Admin account until Patrick decides | Open decision D52 |
