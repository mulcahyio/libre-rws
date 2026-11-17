# Libre WT Software Requirements Specification (SRS)

| | |
|---|---|
| Document | DHF-WT-SRS |
| Status | **DRAFT** (2026-11-10, v0.1 scope approved by Patrick Mulcahy in chat, recorded in issue #15). §5 added 2026-11-14 for v0.2 (issue #20), **DRAFT for approval**: rows are `Draft` until the pull request that implements and verifies them; WT-SRS-013…018, -023, -035 and -036 are `Active` since card WT-0.2/5 (2026-11-16). WT-SRS-019…022 and -024 are `Active` since card WT-0.2/6 (2026-11-16); WT-SRS-027 and -028 are `Active` since card WT-0.2/7 (2026-11-16); WT-SRS-025, -026, -029…034 and -037 are `Active` since card WT-0.2/8 (2026-11-16), the last action card. Card WT-0.2/9 (2026-11-17) added system-level verification of the rows in `manual-verification.md` (system test against real LibreDCM containers); no row changed. Must be reviewed and approved at the QMS baseline |
| Software item | Libre WT (Watch Tower): management console of the Libre RWS PACS; v0.1 covers the Study Exceptions queue for LibreDCM |
| Safety class | B with class-C depth for the resolve path (it changes patient identity on stored images through LibreDCM; HAZ-013, HAZ-014 in `../libredcm/risk-management.md`) |

Rules as in `../libredcm/SRS.md`: IDs are permanent, `Retired` instead of
deleting, tests carry `// Verifies: WT-SRS-###`, checked by
`scripts/qms/trace.py --module librewt --prefix WT`.

Decisions behind this SRS (Patrick Mulcahy, 2026-11-10): build Libre WT now
instead of a LibreDCM admin CLI; local accounts with argon2id, roles
Viewer/Operator/Admin, first-run admin creation, no default credentials;
connection to one LibreDCM through its REST control API (gRPC later); Study
Exceptions queue; every edit shows a warning and needs an explicit
approve-edit confirmation; WT audit log. Fleet view, configuration, logs,
OIDC/LDAP and TOTP come later.

## 1. Web interface and access

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-001 | Libre WT shall serve its user interface from its own Go backend, as server-rendered pages without scripts, with a Content-Security-Policy that blocks scripts, framing and foreign form targets, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and `Cache-Control: no-store`; over HTTPS (TLS 1.2+) when a certificate is configured, with a warning in the log when it is not. | Active | HAZ-009, WT-HAZ-02 |
| WT-SRS-002 | Libre WT shall ship without accounts or default credentials. While no account exists, every page shall lead to a first-run page that creates one Admin; the first-run page shall stop working once an account exists. | Active | WT-HAZ-02 |
| WT-SRS-003 | Passwords shall be stored only as argon2id hashes (random salt, at least m=19 MiB, t=2) and shall have at least 12 characters and differ from the username. A failed login shall not reveal whether the username exists. | Active | WT-HAZ-02 |
| WT-SRS-004 | Access shall be role-based: Viewer and Operator may view the Study Exceptions queue and details; only Admin may resolve exceptions, manage users and read the audit log. Every state-changing request shall carry the session's CSRF token. Failed logins shall be limited per username (5) and per address (10) in 15 minutes. | Active | HAZ-014, WT-HAZ-02 |
| WT-SRS-005 | Sessions shall use a random token in an HTTP-only, SameSite=Strict cookie (Secure over HTTPS), end after 30 minutes idle (configurable) and 12 hours at most, and end at the next request when the user is disabled or their role or password changes. The last active Admin can't be disabled or demoted. | Active | WT-HAZ-02 |

## 2. LibreDCM connection

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-006 | Libre WT shall connect to LibreDCM (one instance in 0.1; one or more since 0.2, see WT-SRS-013) through its REST control API with the LibreDCM admin token (from configuration or a token file, never shown in the interface), optionally trusting a given CA certificate. Libre WT shall not import LibreDCM or DICOM code; it shall change archived data only through LibreDCM's resolve endpoint. It shall refuse to start without a token. | Active | HAZ-009, HAZ-014 |

## 3. Study Exceptions

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-007 | The Study Exceptions page shall list LibreDCM's patient-association conflicts with filters (active, held, accepted, resolved, all), showing time, kind, state, archived and incoming Patient ID, incoming name, stored (coerced) Patient ID, study, number of images and sending AE. When LibreDCM can't be reached, the page shall say so. | Active | HAZ-013 |
| WT-SRS-008 | The detail page shall show the archived and the incoming patient attributes side by side with differences highlighted, the held images, and, for resolved exceptions, who resolved them, when, how and why. | Active | HAZ-014 |
| WT-SRS-009 | Before any edit, Libre WT shall show a warning page with LibreDCM's plan of exactly what will change (files and attribute values from → to, warnings). The edit shall be sent only when the request carries a single-use confirmation issued with that page for the same session, exception and action and less than 10 minutes old, the "I approve this edit" box is ticked, a reason is given, and LibreDCM's plan is still identical to the one shown. | Active | HAZ-014 |
| WT-SRS-010 | The resolve request to LibreDCM shall name the logged-in user as operator and carry the reason, so LibreDCM's audit record identifies the person. | Active | HAZ-014, HAZ-008 |

## 4. Audit

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-011 | Libre WT shall keep an audit log of logins (success, failure, rate limited), logouts, first-run setup, user changes, password changes, edit reviews, approvals and resolve results (who, address, action, target, outcome, detail). The approval shall be recorded before the edit is sent; if it can't be written, the edit shall not be sent. Admins can read the log in the interface. | Active | HAZ-008, HAZ-014 |
| WT-SRS-012 | Administrators shall be able to add users with a role, change roles, disable and enable users and reset passwords; every user can change their own password after entering the current one. | Active | WT-HAZ-02 |

## 5. Libre WT 0.2: all LibreDCM functions (Draft)

Added 2026-11-14 as **Draft** (issue #20). Scope approved by Patrick Mulcahy
on 2026-11-14: "Build Libre WT to support all LibreDCM functions developed to
date. Do not stop until done. Document and test along the way to stay
compliant with FDA 510(k)." Each row becomes `Active` in the pull request
that implements and verifies it. The defaults chosen by Hermes where the
instruction left room are listed as open decisions D1…D16 in
`architecture.md` §8. LibreDCM's side of the interface is
`../libredcm/control-api-v1.md` (DCM-SRS-080…097). Hazards WT-HAZ-05…17 are
in `risk-management.md`; HAZ-015 and HAZ-016 in
`../libredcm/risk-management.md` §2a.

### 5.1 Connections and instance identity

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-013 | Libre WT shall connect to one or more LibreDCM instances, each configured with a unique name, the address of that node's admin listener, its own admin token (value or token file), an optional CA certificate and an optional cluster label. The single-instance `dcm:` setting of 0.1 shall keep working as one instance, named `default`. Libre WT shall refuse to start when two instances share a name (upper and lower case count as the same), a name has characters outside letters, digits, `-` and `_` or is longer than 64 characters, an instance has no token, or both the `instances:` list and a `dcm:` token are given. | Active | WT-HAZ-05, HAZ-009 |
| WT-SRS-014 | Every page that shows or changes data of one LibreDCM instance shall carry the instance name in its address (`/i/{instance}/…`) and shall show a banner with the instance name, AE title, node ID and cluster label. (The fleet and about pages list all instances and name each row.) Libre WT shall keep no "currently selected instance" in the session: a request acts on the instance named in its own address only, and an unknown name gives a "not found" page. | Active | WT-HAZ-05 |
| WT-SRS-015 | Libre WT shall remember the node ID and AE title each instance reported first and shall show a warning on that instance's pages when they change, until an Admin accepts the new identity with a reason. While the identity differs, or the instance can't be reached, Libre WT shall send no state-changing request to that instance. Every state-changing request to LibreDCM shall carry the expected node ID and AE title, so LibreDCM refuses it when the address now reaches another node; Libre WT shall report that refusal and that nothing was changed. | Active | WT-HAZ-05, HAZ-015, HAZ-016 |
| WT-SRS-036 | Libre WT shall show a warning on an instance's pages, and log it at startup, when the connection uses plain HTTP to an address that is not a loopback address. | Active | WT-HAZ-08, HAZ-009 |

### 5.2 Overview and status

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-016 | The fleet page shall list all configured instances, grouped by cluster label, each with: reachable or not, ready or not, version, uptime, licence state, open associations, stored and rejected counts, pending outbox events and open study exceptions. An instance that cannot be reached shall be shown as unreachable with the time of the last successful contact, never with old values shown as current. A status reading shall be reused for all users for a short time (default 5 seconds), so open consoles do not load a node. | Active | WT-HAZ-11, HAZ-004 |
| WT-SRS-017 | The instance dashboard and status page shall show the values of LibreDCM's status endpoint: identity, uptime, backends, each readiness check with its result, licence state, counters per DICOM service (associations, C-STORE by result, C-FIND, C-MOVE and C-GET sub-operations), outbox and conflicts, with the time the data was read. The page shall state that container start, stop and restart are done on the host and name the documented procedure; Libre WT shall offer no such action. | Active | WT-HAZ-11, HAZ-004 |
| WT-SRS-018 | Libre WT shall ask each instance which control functions it supports and shall show a function that an instance does not support as unavailable for that instance, without error pages. A LibreDCM without the discovery endpoint shall be handled as an instance that supports Study Exceptions only. | Active | WT-HAZ-11 |
| WT-SRS-023 | Within one cluster label, the fleet page shall flag instances whose digest of live-reloadable settings differs, and instances whose LibreDCM version differs. | Active | WT-HAZ-12, HAZ-015 |
| WT-SRS-035 | The about page shall show the Libre WT version and, per instance, the LibreDCM version, node ID and AE title. | Active | WT-HAZ-11 |

### 5.3 Configuration

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-019 | The configuration page shall show the instance's running settings with source and whether each can change without a restart. Settings LibreDCM marks as redacted shall be shown only as "set" or "not set". Settings that need a restart shall be read-only, with a note on how to change them on the host. | Active | WT-HAZ-08, WT-HAZ-06 |
| WT-SRS-020 | The pages for C-MOVE destinations and for the calling-AE allow-list shall show the entries in force now with their source, and shall state clearly when the allow-list is empty (every calling AE may connect). | Active | WT-HAZ-06, HAZ-005, HAZ-007 |
| WT-SRS-021 | An Admin shall be able to add, change and remove C-MOVE destinations and allow-list rules. Libre WT shall send the change to LibreDCM as a plan first and show a warning page with the exact difference (added, removed, changed entries), the instance banner and LibreDCM's warnings. Each warning that LibreDCM marks as needing acknowledgement shall have its own box that must be ticked. A change LibreDCM refuses as invalid shall be shown with LibreDCM's reason and not applied. | Active | WT-HAZ-06, HAZ-015 |
| WT-SRS-022 | An Admin shall be able to reload the configuration of an instance and to roll back to the previous version of the console-made settings, each through a plan page that shows the difference and the general action rule (WT-SRS-031). When LibreDCM blocks the reload until the licence notice is acknowledged, Libre WT shall say so and link to the licence page. | Active | WT-HAZ-06, WT-HAZ-15, HAZ-015 |

### 5.4 DICOM functions

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-024 | Operators and Admins shall be able to run a C-ECHO test from an instance to one of its configured C-MOVE destinations and see success, or the stage that failed, and the duration. Only an Admin shall be able to test a free host, port and AE title. | Active | WT-HAZ-14, HAZ-005 |
| WT-SRS-025 | Operators and Admins shall be able to search the studies of an instance by Patient ID, patient name (both with the wildcards `*` and `?`), study date range, accession number, modality or Study Instance UID, open a study to see its series, and open a series to see its instances (identifiers only). The search values shall be sent by `POST`, never in a page address. A search without a search value (empty, or `*` only) shall not be sent. The page shall show when more results exist than are shown and offer the next page. Libre WT shall display what LibreDCM returns and shall not add held conflict instances. Each search and each opening of a study or series page shall be recorded in the Libre WT audit log before LibreDCM is asked, and sent to LibreDCM with the user's name. The study, series and search pages shall state that Libre WT is not a diagnostic viewer and shall show no images. | Active | WT-HAZ-13, HAZ-002, HAZ-009 |
| WT-SRS-026 | An Admin shall be able to send a study, or chosen series of it, to one of the instance's configured C-MOVE destinations (D52). The destination shall be chosen from that list only. The request shall name the Patient ID the study page showed as well as the study. The warning page shall show the patient (name, ID, birth date) prominently, the study (date, description, accession number), the number of series and instances, and the destination name, address and AE title, as planned by LibreDCM; the approval box shall name the patient. Libre WT shall issue no approval when LibreDCM's plan names another patient, study or destination than the one chosen, has no plan digest, holds no instance, or comes from another node. The approval shall be bound to the study in the address and to LibreDCM's plan digest, which is sent with the send. The page shall not offer a send for a study held as a patient conflict or listed under more than one patient. After the send, the page shall show the outcome (complete, partial, failed), how many instances were accepted and each instance that failed or was accepted with a warning. | Active | WT-HAZ-07, HAZ-016 |

### 5.5 Licence, backup, audit records and logs

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-027 | The licence page shall show the licence state of an instance, its message, days left, expiry, machine fingerprint, customer, site, term and acknowledgement, with licence keys masked (last four characters at most). It shall always state that DICOM services keep running whatever the licence state, and so shall every licence warning and result page. Only an Admin shall be able to acknowledge the unlicensed notice, create an activation request file, install a licence file and activate online (D47). The acknowledgement shall be recorded under the logged-in user, who shall type their own user name to sign it. A licence file shall be uploaded as text of at most 128 KiB. A licence that LibreDCM refuses shall be reported and the installed licence shall stay. A licence key typed into Libre WT shall be shown only masked and shall not be written to its audit log or its log; the activation request text shall be handed out as a file download only. | Active | WT-HAZ-09, WT-HAZ-08, HAZ-010 |
| WT-SRS-028 | Operators and Admins shall be able to start an online backup of the index of an instance, through a warning page with a reason, and see the result (file name, size, SHA-256 checksum). The backup page shall list the backups on the node (file name, size, time) and the backups started from Libre WT, from its audit log (time, user, outcome, file, size, checksum, reason). Libre WT shall offer no field for a path or file name and no download or delete. The pages shall state that the backup holds the index only and that images must be backed up separately. | Active | WT-HAZ-10, HAZ-001 |
| WT-SRS-029 | An Admin shall be able to read the recent audit records of an instance, newest first, filtered by event type (prefix), outcome and period. The page shall state that it shows only the most recent records kept in memory and that the site's log collector holds the full record, and shall say when the node runs with `audit_log_off`. Filter values outside the offered choices shall not be sent. | Active | WT-HAZ-17, HAZ-008, WT-HAZ-13 |
| WT-SRS-030 | Operators and Admins shall be able to read the recent log lines of an instance, newest first, filtered by lowest level and period, with the same statement about the memory buffer (D53). | Active | WT-HAZ-11, WT-HAZ-13 |
| WT-SRS-037 | An Admin shall be able to export the audit records or log lines shown with a filter as a CSV file download (D53). The export shall be recorded in the Libre WT audit log before the file is sent; when the record can't be written, no file shall be sent. A cell that starts with `=`, `+`, `-`, `@`, a tab or a carriage return shall get a leading apostrophe, so a spreadsheet program does not run it as a formula. | Active | WT-HAZ-13, WT-HAZ-17, HAZ-008 |

### 5.6 Rules for all functions

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-031 | Every action that changes something on a LibreDCM instance (configuration change, reload, rollback, send, licence action, backup, C-ECHO test, accepting a new instance identity) shall be sent only when: the user's role allows it; the request carries the session's CSRF token; the user was shown a plan or warning page for it; the request carries a single-use confirmation issued with that page for the same session, instance, action and plan, less than 10 minutes old; the approval box is ticked; and a reason is given. For actions with a LibreDCM plan, the plan digest shown shall be sent with the action so LibreDCM refuses it when the state has changed. The request to LibreDCM shall name the logged-in user as operator and carry the reason and a request ID. | Active | WT-HAZ-05, WT-HAZ-06, WT-HAZ-07, WT-HAZ-09, WT-HAZ-10, HAZ-015, HAZ-016 |
| WT-SRS-032 | Libre WT shall never display, log or write to its audit log an admin token, a licence key in full or any value LibreDCM marks as redacted, and shall not put them in page addresses. Error messages from LibreDCM shall be shown as text, never interpreted as markup. | Active | WT-HAZ-08, WT-HAZ-04, HAZ-009 |
| WT-SRS-033 | The Libre WT audit log shall record, for every action of WT-SRS-031, for every opening of a page that shows patient data (Study Exceptions, study search, study and series pages), audit records or logs of an instance, and for every export: who, from which address, which instance, which action, the target, the reason, the outcome and LibreDCM's answer. The approval shall be recorded before the action is sent; when it cannot be written, the action shall not be sent. When the record of a page opening or an export cannot be written, the page or file shall not be shown. The audit page shall filter by instance. | Active | WT-HAZ-17, HAZ-008 |
| WT-SRS-034 | Roles shall apply as follows, checked on the server for every request. Viewer: fleet, dashboard, status, licence state, about, Study Exceptions (as in 0.1). Operator: also configuration view, destinations, allow-list, logs, study search with study and series pages, C-ECHO to configured destinations, backup. Admin: also configuration change, reload, rollback, send (D52), licence acknowledgement, request, install and activation (D47), C-ECHO to a free address, LibreDCM audit records, CSV export of audit records and logs (D53), accepting an instance identity, and everything Admin could do in 0.1. A page shall not show a control the user's role cannot use. | Active | WT-HAZ-02, WT-HAZ-13 |
