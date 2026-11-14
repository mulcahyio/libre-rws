# Libre WT risk management file (ISO 14971:2019)

| | |
|---|---|
| Document | DHF-WT-RMF |
| Status | **DRAFT** (2026-11-10, prepared by Hermes/Claude for human review); v0.2 analysis added 2026-11-14, **DRAFT for approval** |
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
| WT-HAZ-09 (→ HAZ-010) | **Licence mis-action**: wrong licence installed, notice acknowledged by someone who did not mean to, or the user stops work because the page seems to say images will stop | Mostly commercial. Safety relevant only if a licence action could disturb DICOM services, or if staff divert patients because they believe the archive is down | Wrong file; wrong node; unclear wording | S3 | P2 | R | WT-SRS-027 (statement that DICOM keeps running; acknowledgement under the user's own name; refused licence leaves the old one), -031, -034; LibreDCM DCM-SRS-089, -058 (licensing can't reach the gateway, architecture test) | P1 | A |
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
