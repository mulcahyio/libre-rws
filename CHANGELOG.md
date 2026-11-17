# Changelog

All notable changes to Libre RWS (formerly RWS) are recorded here. Versions follow
[Semantic Versioning](https://semver.org) with a `-beta.N` pre-release label
until a release is approved for production use (see "Versioning" in
[README.md](README.md)).

Libre RWS is developed by Patrick Mulcahy, Mulcahy.io (dev@mulcahy.io).

## Unreleased

QMS and retrospective design-file work for LibreDCM (Kanban board
`libre-qms`), and the new LibreDVT module (board `libredvt`). No change to
LibreDCM's behaviour unless stated.

### Added
- **Libre WT: study browser, send to a destination, LibreDCM audit records
  and logs (card WT-0.2/8, issue #20; WT-SRS-025, -026, -029…034 and the
  new -037 now `Active`).**
  - Study search per instance (Operator, Admin): Patient ID and name with
    `*` and `?`, study date range, modality, accession number, Study
    Instance UID. Values go by `POST`, never in an address; a search without
    a value (or `*` alone) is refused before it is sent. 50 studies per
    page, with "more studies match" and a next page. Each search is in the
    Libre WT audit log (with its values) before LibreDCM is asked, under the
    user's name, and LibreDCM records it again.
  - Study page (patient, study, series) and series page (instances by
    identifier only). Libre WT shows no images; every page says it is not a
    diagnostic viewer. Each opening is audited (no record, no page).
  - Send a study, or chosen series, to a configured destination (Admin):
    warning page with the patient's name, ID and birth date in the warning,
    the table and the approval box, the study, the series and instance
    counts and the destination's AE title and address, as LibreDCM plans
    them. No approval when LibreDCM's plan names another patient, study or
    destination, has no digest, holds nothing, or comes from another node;
    no send form for a study held as a patient conflict or listed under two
    patients. The approval is bound to the study and LibreDCM's plan
    digest; LibreDCM refuses a send whose study changed. The result shows
    complete, partial or failed with every failed instance.
  - LibreDCM audit record page (Admin) and log page (Operator, Admin):
    recent records or lines kept in the node's memory, newest first, with
    filters (event type, outcome, level, period, number) and the statement
    that the site's log collector holds the full record.
  - CSV export of the audit records and logs shown (Admin): a download,
    audited before the file is sent; cells a spreadsheet would run as a
    formula get a leading apostrophe.

  Kept against the card text: sending stays Admin only (D52; the card said
  Operator and Admin, but Admin only is a listed risk control of
  WT-HAZ-07). No new dependency. No change to LibreDCM.
- **Libre WT: licence management and index backup (card WT-0.2/7, issue
  #20; WT-SRS-027 and -028 now `Active`).**
  - Licence page per instance (all roles): state, message, days left,
    expiry, customer, site, term, machine fingerprint and acknowledgement.
    The key shows only as its last four characters (Libre WT also cuts a
    longer value LibreDCM might send). Every licence page says that DICOM
    services keep running whatever the licence state.
  - Licence actions (Admin), each through a warning page with the
    single-use approval, the approval box and a reason: acknowledge the
    unlicensed notice (the Admin types their own user name to sign;
    LibreDCM records the logged-in user), install a licence file (upload,
    text, at most 128 KiB; the warning page shows name, size and SHA-256;
    LibreDCM checks it and a refused file leaves the installed licence),
    activate online (key in a password field), and download an activation
    request file for offline activation.
  - The licence key is kept only in memory with the pending approval, shown
    masked, sent once, removed from LibreDCM's error text, and never written
    to the Libre WT log or audit log. The request file is a download only.
  - Backup page per instance (Operator, Admin): the backup files on the
    node and the backups started from Libre WT (from its audit log: user,
    time, reason, file, size, SHA-256). Start a backup through a warning
    page with a reason; the result shows file, size and checksum. No path,
    file name, download or delete. The pages say the backup holds the index
    only and images must be backed up separately. A node without a backup
    directory offers nothing to approve.
  - The licence-block message of configuration changes now links to the
    licence page of the instance.
  - The licence file upload is the only `multipart/form-data` address
    (whole body at most 192 KiB, held in memory).

  Changed against the 0.2 design: acknowledging the licence notice is for
  Admins only (D47; the design had Operator and Admin). No new dependency.
  No change to LibreDCM.
- **Libre WT: configuration, destinations, allow-list, edit, reload,
  rollback and C-ECHO test (card WT-0.2/6, issue #20; WT-SRS-019…022 and
  -024 now `Active`).**
  - Configuration page per instance (Operator, Admin): every setting the
    node runs with, its source and whether it changes without a restart.
    Secrets show only as "set" or "not set"; Libre WT also drops any value
    of a setting LibreDCM marks as secret. Restart-only settings are
    read-only, with the host procedure.
  - Destinations and Allow-list pages (Operator, Admin): the C-MOVE
    destinations and calling-AE rules in force, with their source. An empty
    allow-list is shown as a warning (every calling AE may connect); an
    invalid one as "refuses every association".
  - Edit of the destinations and the allow-list (Admin): Libre WT sends the
    new value to LibreDCM as a plan, shows LibreDCM's exact difference on a
    warning page with a box for each warning that must be acknowledged,
    and applies it only with the single-use approval of that page, the
    approval box and a reason. A value LibreDCM refuses is shown with its
    reason and the typed form is kept.
  - Reload from the files and rollback to the previous console-made
    version (Admin), each through a plan page. When an unlicensed node
    blocks changes until the licence notice is acknowledged, Libre WT says
    so and that DICOM keeps running.
  - C-ECHO test from the node: to a configured destination (Operator,
    Admin) or to another host, port and AE title (Admin), with the result
    or the stage that failed and the duration.
  - One action flow for these actions: what is sent is kept on the server
    from the review (the browser can't change it); the approval is bound
    to the session, instance, action and plan, used once, valid 10
    minutes, at most 20 open per session; LibreDCM's plan digest, the
    logged-in user as operator, the reason, a random request ID and the
    expected node go with every action. The approval is written to the
    audit log before sending; without the record nothing is sent. When
    LibreDCM does not answer in 30 seconds the outcome is recorded as
    "unknown" with the request ID.
  - Audit: every opening of the configuration pages (without the record,
    the page is not shown), every review, approval and result, with the
    instance and the request ID.
  - The dashboard and the instance tabs list only the pages the user's
    role can open.

  No new dependency. No change to LibreDCM.
- **Libre WT: several LibreDCM instances, fleet page, dashboard, status and
  about pages (card WT-0.2/5, issue #20; WT-SRS-013…018, -023, -035, -036
  now `Active`).**
  - Connections: an `instances:` list in the Libre WT configuration file,
    each with a name, the address of that node's admin listener, its own
    token (`token_file` or `token`), an optional `ca_file` and an optional
    `cluster` label. The `dcm:` block and `LIBRE_WT_DCM_*` variables of 0.1
    keep working as one instance named `default`. Libre WT refuses to start
    on duplicate names (also when they differ only in case), other
    characters than letters, digits, `-` and `_`, a missing token, or when
    both forms are given.
  - Every page of an instance has the instance in its address
    (`/i/<name>/…`) and a banner with name, AE title, node ID and cluster
    label. Nothing is "selected" in the session.
  - Fleet page (`/`, all roles): all instances by cluster label with
    health, version, uptime, licence state, open associations, stored and
    not-stored counts, rejected associations, outbox depth and open
    exceptions. An instance that can't be reached shows the time of the
    last contact and no values. Inside a cluster label, instances with
    other live settings or another version are flagged.
  - Dashboard and status page per instance (all roles): identity, uptime,
    backends, each readiness check, licence state, counters per DICOM
    service, with the time of the reading. The status page names the host
    procedure for start, stop and restart; Libre WT has no such button.
  - About page: Libre WT version and, per instance, LibreDCM version, node
    ID and AE title.
  - Instance identity: Libre WT remembers the node ID and AE title each
    instance reported first. When another node answers, every page of the
    instance warns and no edit is sent until an Admin accepts the new
    identity (warning page, approval box, reason, audit). Edits carry the
    expected node ID and AE title, so LibreDCM refuses them on another
    node.
  - Warning on the pages and in the start-up log when an instance is
    reached over plain HTTP on another machine.
  - A LibreDCM without the newer control API still works for Study
    Exceptions; its status is shown as not available.
  - The Libre WT audit log names the instance and can be filtered by it.
    `librewt.db` is upgraded in place.
  - New setting `status_cache` (`LIBRE_WT_STATUS_CACHE`, default 5s): how
    long a status reading is reused for all users.

  No new dependency. No change to LibreDCM.
- **LibreDCM control API: study query, send and configuration edit for
  Libre WT (card WT-0.2/4, issue #20; DCM-SRS-091…096 now `Active`).** New
  endpoints on the admin listener, all behind the admin token:
  - `GET /control/v1/studies`: finds patients, studies, series or images in
    the index with the matching rules and the patient-conflict visibility of
    C-FIND (one shared function). Needs at least one matching key and the
    name of the person in the header `X-Libre-Operator`. A key the node does
    not match on is refused, not ignored. At most 200 rows per page, with a
    cursor when more rows exist; four queries at a time. Each query writes
    an audit record before rows are returned; the query values appear in no
    other log line.
  - `POST /control/v1/send/plan` and `POST /control/v1/send`: sends a study,
    chosen series or chosen instances to a configured remote AE by name,
    over the send path of C-MOVE. The Patient ID must be given and must be
    the one the study is indexed under. The plan shows the destination
    address, the patient, the study and the instances. The send needs
    `confirm` and the digest of an unchanged plan and sends exactly the
    instances of that plan. Instances recorded as a patient conflict are
    never sent. One send at a time, at most 5000 instances, 30 minutes. The
    answer lists the instances that failed or got a warning; when nothing
    arrived it is `502`. Audited as `DICOMInstancesTransferred.sent` with
    operator, destination, study and patient.
  - `POST /control/v1/config/plan`, `POST /control/v1/config`,
    `GET /control/v1/config/history`, `POST /control/v1/config/rollback/plan`
    and `POST /control/v1/config/rollback`: change the C-MOVE destinations
    and the calling-AE allow-list without a restart. No other setting can be
    changed: every other key is refused. The change is validated by the
    normal loader, written atomically to an overlay file that LibreDCM owns
    (new setting `control.overlay_file`, `LIBRE_DCM_CONTROL_OVERLAY_FILE`;
    default `./data/config-overlay.yaml`, in the container image
    `/var/lib/libredcm/config-overlay.yaml`; empty = function off) and put
    in force through the normal reload. `config.yaml` is never rewritten.
    Loading order: defaults, `config.yaml`, overlay, environment. A failed
    step leaves the files and the running settings as they were. One
    previous version is kept and can be put back. Settings fixed by an
    environment variable cannot be edited.
  - A plan (reload, edit or rollback) now also warns when the new allow-list
    would refuse a calling AE that was active in the last 24 hours. Like the
    other warnings it must be acknowledged by its code.

  No change to C-STORE, C-MOVE or C-GET. C-FIND now builds its filters
  through the function it shares with the study query; its results are
  unchanged (compared in the tests). No new dependency.
- **LibreDCM control API: actions for Libre WT (card WT-0.2/3, issue #20;
  DCM-SRS-086…090 and -097 now `Active`).** New endpoints on the admin
  listener, all behind the admin token:
  - `POST /control/v1/echo`: C-ECHO test to a configured remote AE by name,
    or to a given host, port and AE title. 15 s limit, two tests at a time.
    The answer says success or the stage that failed (connect, association,
    echo) and the duration. It never carries text from the peer or the
    operating system. Only the Verification class is proposed; no patient
    data is sent.
  - `POST /control/v1/reload/plan` and `POST /control/v1/reload`: the reload
    goes through the same reloader as `SIGHUP`, one at a time, including the
    licence acknowledgement block (answer `423`). The plan shows what would
    change. The reload needs `confirm` and the digest of an unchanged plan.
    A plan that empties the allow-list or removes or retargets a destination
    carries a warning that must be acknowledged by its code. A configuration
    that fails to load is reported and changes nothing.
  - `GET /control/v1/license`, `POST /control/v1/license/ack`, `/request`,
    `/install`, `/activate`: the same functions as the `-license-*` flags,
    through the existing licensing code. An invalid licence is refused and
    the installed one stays. A licence key is shown only by its last four
    characters and is never logged or audited. A source build says that no
    licence is needed and refuses the licence actions. Licensing still
    cannot reach the DICOM gateway: the architecture test now also forbids
    the gateway to import the control package.
  - `POST /control/v1/backup` and `GET /control/v1/backups`: online snapshot
    of the SQLite index into one directory set on the node
    (`control.backup_dir`, `LIBRE_DCM_CONTROL_BACKUP_DIR`; empty = off, the
    default). The request has no path or file name field. LibreDCM makes the
    name (`index-<node>-<UTC time>.db`), creates the file exclusively (no
    file is ever replaced), mode 0600, and returns name, size and SHA-256.
    Refused when free space is below the index size plus 10 % or cannot be
    read, when another backup runs, or when the index is PostgreSQL. No
    endpoint downloads or deletes a snapshot.

  Every action needs `operator`, `reason` and `request_id`. It writes one
  audit record before it starts (no record, no action) and one with the
  outcome; a refused request writes a refusal record. These records are
  written at every log level. A `request_id` sent again within 10 minutes
  returns the first result and does nothing twice; the same ID for another
  request is refused. Every `POST`, including the two conflict endpoints of
  0.1, accepts `expect_node_id` and `expect_ae_title` and answers `409`
  without any change when they name another node. No change to DICOM
  services. No new dependency.
- **LibreDCM control API: read-only views for Libre WT (card WT-0.2/2,
  issue #20; DCM-SRS-080…085 now `Active`).** New endpoints on the admin
  listener, all behind the admin token: `GET /control/v1` (features),
  `/control/v1/status` (identity, uptime, backends, readiness per check,
  licence state, live counters), `/control/v1/config` (running settings with
  source and whether they reload live; secrets hidden), `/control/v1/remote-aes`
  and `/control/v1/allow-list` (what is in force now, also after a reload),
  `/control/v1/audit` (recent audit records, filters `type`, `outcome`,
  `since`, `limit` ≤ 500) and `/control/v1/logs` (recent log lines, filters
  `level`, `since`, `limit` ≤ 1000). Records come from fixed-size in-memory
  rings (1000 audit records, 2000 log lines), fed by the logger after the
  real log line is written. Every response names the node and carries a
  digest of the live settings. Secrets are kept out by two independent
  controls: settings are shown from an allow-list (anything unlisted is
  redacted), and every control response, including the conflict endpoints,
  passes a secret filter before it is sent. No change to DICOM services.
- **Design inputs for Libre WT 0.2 (documents only, no behaviour change;
  issue #20).** Scope approved by Patrick Mulcahy on 2026-11-14: Libre WT is
  to support all LibreDCM functions developed to date. Added as drafts for
  approval: intended use, users and the use of each function
  (`docs/dhf/librewt/intended-use.md` §3); requirements WT-SRS-013…036 and
  DCM-SRS-080…097 (all `Draft` until implemented); the Libre WT 0.2
  architecture with screens, action flow and multi-instance connections; the
  LibreDCM control API v1 extension contract
  (`docs/dhf/libredcm/control-api-v1.md`); the risk analysis of every new
  function (WT-HAZ-05…17, new system hazards HAZ-015 remote configuration
  change and HAZ-016 study sent wrongly); and the threat tables for the new
  endpoints and pages (TM-19…30, WT-TM-09…19). Sixteen defaults chosen by
  Hermes are listed as open decisions (`docs/dhf/librewt/architecture.md`
  §8). Not offered by design: remote start/stop/restart, editing settings
  that need a restart, certificate generation, a free send address, a
  client-chosen backup path.
- Release verification (`scripts/qms/release-verify.sh`) now also runs
  LibreDVT: its engine positive controls (forced to re-run, never taken from
  the build cache) and the bundled LibreDCM conformance plan against the
  source-build and the unlicensed official-build containers, next to the
  DCMTK script; both must pass (Patrick, 2026-11-10, issue #16). JUnit and
  HTML reports are kept in the verification output.
- **LibreDVT 0.1 (DICOM Validation & Test)**, a new standalone module
  (`modules/dvt`, image `libre/dvt`, issue #12): web UI and command line to
  validate DICOM objects (dicom3tools `dciodvfy`/`dcentvfy`, pydicom
  `dicom-validator` with the 2026d standard built in) and to test DICOM
  services (C-ECHO, C-STORE, C-FIND, C-MOVE to a built-in Storage SCP with
  pixel comparison, association rejection) from strict YAML test plans, with
  JUnit XML and HTML reports. Store and move steps only run against targets
  classed `lab`. Engines are independent of LibreDCM's DICOM libraries. Login
  with CSRF protection and a login rate limit; published on 127.0.0.1 by
  default (`deploy/compose/docker-compose.dvt.yml`). Design file
  `docs/dhf/libredvt/`; validated as a high-risk QMS tool (positive
  controls in the new CI job `libredvt`). New tool configuration items (in
  the LibreDVT image only, not in LibreDCM): dicom3tools 20260927 (BSD),
  dicom-validator 0.9.0 (MIT), pydicom 3.0.2 (MIT), pynetdicom 3.0.4 (MIT),
  lxml 6.1.3 (BSD-3), pyparsing 3.3.3 (MIT). No new Go dependency.
- **Patient ID conflict control (issue #14, HAZ-013, fixes ANOM-002 and
  ANOM-009; behaviour change).** A study no longer moves to another patient
  when instances arrive with a different Patient ID, and a series no longer
  moves to another study. LibreDCM detects the conflict inside the index
  transaction and, by default, stores the new instances under their own key
  (`conflicts/...`), holds them out of C-FIND/C-MOVE/C-GET and records them
  for review. Settings (asked by the workstation installer):
  `patient_conflict_policy` `quarantine` | `reject` (C-STORE A778) |
  `accept_separate`; `patient_conflict_visibility` `hidden` | `flagged`;
  `patient_conflict_coerce_patient_id` stores held instances as
  `B000_<Patient ID>` with an Original Attributes Sequence item and answers
  Warning B000 (byte splicing: pixel data and all other elements unchanged).
  Same Patient ID with another name, birth date or sex keeps the first
  registration. New audit records `PatientRecord.conflict-detected` /
  `-resolved` and metrics `libre_dcm_patient_conflicts_total`,
  `libre_dcm_patient_conflicts_open`,
  `libre_dcm_patient_demographics_mismatch_total`,
  `libre_dcm_patient_id_coercions_total`,
  `libre_dcm_patient_conflict_resolutions_total`. Requirements
  DCM-SRS-070…079, user need UN-17, hazard HAZ-014 (administrator correction).
- **Control API for Libre WT** on the admin listener: `GET /control/v1/conflicts`,
  `GET /control/v1/conflicts/{id}`, `POST .../plan`, `POST .../resolve`
  (`correct_incoming`, `correct_existing`, `reject`). Refused unless an admin
  token is configured and presented; resolve needs operator, reason and
  `confirm: true`. Corrections write an Original Attributes Sequence item
  (reason CORRECT) into every rewritten file.
- Index schema v4 (`conflicts`, `conflict_instances`); upgrades in place.
- **Libre WT 0.1 (new module, issue #15)**: web management console for
  LibreDCM (`modules/wt`, image `libre/wt` from `deploy/docker/Dockerfile.wt`).
  Local accounts with argon2id password hashes, roles Viewer / Operator /
  Admin, first-run administrator creation (no default credentials), session
  timeouts, CSRF protection, login rate limiting, strict Content Security
  Policy (no scripts). Connects to one LibreDCM through the control API. Study
  Exceptions queue with side-by-side detail; every edit shows LibreDCM's plan
  on a warning page and needs a single-use approval, the "I approve this edit"
  box and a reason; the plan is re-checked before the edit is sent. Audit log
  of logins, user changes, reviews and edits. Design file `docs/dhf/librewt/`
  (WT-SRS-001…012). New direct dependency `golang.org/x/crypto` v0.57.0
  (BSD-3-Clause, argon2), already in the module graph as indirect.
- Automated verification for the last three untested active requirements:
  DICOM TLS only, TLS 1.2+ (DCM-SRS-043), graceful shutdown never acknowledges
  an instance that was not stored (DCM-SRS-053), and Prometheus metrics for
  associations, rejections, stores, the outbox and the licence state
  (DCM-SRS-054). The trace matrix now shows 38 of 38 active requirements
  verified, so the CI trace check is now blocking.
- Static analysis in CI: gosec v2.30.0, blocking for Libre RWS code,
  report-only for `third_party/`; the gonetdicom fork and LibreKey client
  copy tests now run in CI too. Triage in `docs/dhf/libredcm/cybersecurity.md` §6.
- Anomaly list `docs/dhf/libredcm/anomalies.md` (ANOM-001…007).
- LibreDCM intended use, limitations, user profiles, use specification and
  16 user needs (`docs/dhf/libredcm/intended-use.md`), with
  `scripts/qms/needs.py` (in CI) checking that every requirement serves a
  user need.
- LibreDCM architecture document (`docs/dhf/libredcm/architecture.md`):
  12 software items, interfaces, data flows, SOUP requirements, deployment
  topologies and risk-control segregation.
- Quality manual (QMS-QM-001) and SOP-001 document and record control with
  an executed Part 11 validation of the GitHub + Drive record system
  (VAL-0001); `scripts/qms/part11_check.py` re-checks the GitHub settings,
  `scripts/qms/mdlinks.py` (in CI) checks document links.
- SOPs 002–009 and 011 (design control, risk management, CAPA,
  complaints/MDR/corrections, suppliers, training, audit and management
  review, labeling/UDI, tool validation) with templates; completed SDP.
- Tool validation notes (`docs/qms/tool-validation/`) and scripted tests
  for the QMS scripts (`scripts/qms/test_qms_tools.py`, in CI).
- Go native fuzzing: five new targets for Part 10 writing/reading, dataset
  decoding and C-FIND identifiers, plus the four fork targets;
  `scripts/qms/fuzz.sh` and a weekly `fuzz` workflow. Record run in
  `cybersecurity.md` §7.
- Risk management file scored against SOP-003 (risk management plan, hazard
  analysis with residual risk, report); tests for the store-failure and
  called-AE risk controls.
- Threat model with exploitability and severity, security architecture
  views (global, multi-patient harm, updatability, security use cases) and
  the FDA security control categories in `cybersecurity.md`.
- Detailed design for the class-C-depth units (store path, index and
  patient association, pixel-data integrity) and a pixel-data integrity test
  across transfer syntaxes (`TestPixelDataIntegrity`). Anomalies ANOM-009
  (series moved between studies) and ANOM-010 (C-GET completed count) added.
- SOP-010 vulnerability management; `qms` workflow re-scans `master` weekly.
- Container image scan (Trivy 0.76.2) in CI, blocking for fixed HIGH/CRITICAL
  OS-package vulnerabilities; justified exceptions expire (`.trivyignore.yaml`).

### Changed
- Libre WT: opening the Study Exceptions list or one exception is now
  written to the Libre WT audit log, like every page with patient data;
  when the record can't be written the page is not shown (D56, WT-SRS-033).
- Libre WT: Study Exceptions moved from `/exceptions…` to
  `/i/<instance>/exceptions…`. The old addresses lead to the same page when
  there is one instance, and to the fleet page when there are several.
  After login the first page is the fleet page. The footer no longer shows
  the LibreDCM address (it is on the dashboard and the about page). An
  approval of an edit is now also bound to the instance.
- Configuration parsing uses `go.yaml.in/yaml/v3` v3.0.5 (maintained by the
  YAML organisation) instead of the archived `gopkg.in/yaml.v3`. No
  behaviour change: the example configuration decodes identically with both.

### Fixed
- C-FIND: an identifier containing a command (0000) or file-meta (0002)
  group element made the response unencodable and dropped the association;
  such elements are now ignored (found by fuzzing, ANOM-008).
- `scripts/qms/soup.py` wrote a stale runtime-image path; `soup.md` still
  described the LibreKey client risk as "C-STORE refused" (changed by issue
  #5).
- Part 10 writer refuses File Meta values too long to encode (or empty
  identifiers) instead of writing an unreadable file (DCM-SRS-018,
  ANOM-001, found by gosec). Over-long but encodable UIDs from
  non-conformant senders are still archived as received.

### Tested (2026-11-14, control API read-only views, card WT-0.2/2)
- Go 1.26.11 in Docker (`rws-go`): `vet` (plain and `-tags official`),
  `test -race` 22 packages ok, `-tags official` 20 packages ok, gosec
  v2.30.0 0 issues (48 files), govulncheck v1.9.0 no called
  vulnerabilities, trace/needs/mdlinks clean, 13 QMS tool tests ok. The new
  tests also ran 5 times in a row with `-race` without a flake.
- 20 new tests (plus an oversized-body check in the existing conflict
  test), among them: every endpoint without, with a wrong and with
  the right token, and with no token configured; every endpoint searched for
  every configured secret (also secrets planted in log lines, audit
  records, readiness errors and query values); real wiring through
  `build()` with secrets from the file and the environment; audit ring with
  patient-ID omission on and off through the real gateway; ring bounds,
  filters and limits; concurrent logging.
- Mutation check: 11 mutations of the risk controls (setting allow-list,
  response filter, admin secrets passed to the filter, token comparison,
  ring bound, limit cap, outcome and level filters, audit routing, live
  allow-list, patient-ID omission); each made a test fail. One mutation
  (filter failure after a successful encode) cannot be reached and was
  left out.
- Not tested: HTTPS admin listener with the new endpoints (unchanged TLS
  code path); PowerShell `curl.exe` example in INSTALL.md not run.

### Tested (2026-11-10, patient conflicts and Libre WT)
- `go test -race` all packages; PostgreSQL 16 index and gateway suites;
  `-tags official`; gosec and govulncheck clean on the new code; mutation
  checks on the conflict check, the store pre-check, retrieve gating, byte
  splicing, Libre WT's role check, CSRF, session epoch, edit confirmation and
  plan digest (each mutation makes a test fail).
- End to end with DCMTK `storescu`/`findscu` and synthetic data: conflict held
  with Warning B000, `B000_` Patient ID and Original Attributes Sequence,
  pixel data identical, `dciodvfy` output unchanged; resolved in Libre WT in
  a browser ("correct the archived study"); study then under the new patient
  with 4 instances and CORRECT history in every file; both audit logs name
  the operator and reason.
- Not tested: Windows installer questions (PowerShell not run), HTTPS for
  Libre WT, cluster (several LibreDCM nodes) with conflicts.

### Tested (2026-11-10)
- New tests pass with `-race`, 15 repeated runs without a flake. Mutation
  checks: allowing TLS 1.0, dropping the rejected-association counter and
  acknowledging a store whose index write failed each make the matching test
  fail (the last one is timing-dependent: caught in 2 of 3 runs).
- gosec: 15 findings in Libre RWS code triaged to 0 open (1 fixed defect,
  13 annotated, 1 cosmetic fix); CI scan commands run locally in the
  golang:1.26 image. Probe: before the fix an 84-byte SOP Instance UID was
  archived and a 70,000-byte one refused by the filesystem store (A700).
- LibreDVT 0.1: unit tests with fake engines pass with `-race`; engine
  positive controls pass in the test image (real dicom3tools,
  dicom-validator, pynetdicom against a fake archive that refuses stores,
  withholds instances, alters pixels or accepts any AE title: each failure
  detected); 6 of 6 mutation checks caught; the bundled LibreDCM plan passed
  against a LibreDCM container and agreed with `scripts/conformance.sh`
  (DCMTK) on the same fixtures; web UI flow exercised over HTTP; image works
  with `--network none`; gosec 0 open (22 annotated), govulncheck clean,
  Trivy 0 fixed HIGH/CRITICAL in `libre/dvt`. Trace: 34 of 34 DVT
  requirements verified.

## LibreDCM 0.2.0-beta.1 (2026-11-09)

First release made under the QMS process (docs/qms/release-checklist.md).
Release record: [docs/releases/v0.2.0-beta.1-record.md](docs/releases/v0.2.0-beta.1-record.md).

### Changed
- Release builds pin Go 1.26.10 and the golang/distroless base images by
  digest (`deploy/docker/Dockerfile.dicom`).
- Product renamed: RWS is now **Libre RWS**, the DICOM module (rws-dicom) is
  **Libre DCM**, and the planned management console is **Libre WT** (Libre
  Watch Tower). Tagline: "Open and extensible." The documentation uses the
  new names; see "Naming" in [README.md](README.md).
- The GitHub repository moved from `StoreHouseAdmin/RWS` to
  [`mulcahyio/libre-rws`](https://github.com/mulcahyio/libre-rws). GitHub
  redirects the old URLs; update your `git remote` to the new one.
- Code identifiers were renamed later in this release too (see "Code
  identifiers renamed" below), with backward compatibility.

### Added
- `-backup PATH` flag: writes a consistent snapshot of the SQLite index while
  the module keeps running (`VACUUM INTO`). PostgreSQL indexes use `pg_dump`.
- Event outbox: the `instance.stored` event is committed with the instance
  and published by a relay with retries, so a NATS outage delays events
  instead of losing them. New metric `rws_dicom_outbox_pending`. Index schema
  version 2 (adds `event_outbox`; applied automatically on start).
- Calling-AE allow-list: `allowed_aes` / `RWS_DICOM_ALLOWED_AES` accepts
  `AE`, `AE@IP` or `AE@CIDR` entries. Other peers get A-ASSOCIATE-RJ
  ("calling AE not recognized"), counted in
  `rws_dicom_associations_rejected_total`. Empty (the default) keeps the old
  behaviour. An invalid list rejects everything rather than allowing all.
- Admin endpoint token: with `RWS_ADMIN_TOKEN` (or `RWS_ADMIN_TOKEN_FILE`
  for Docker secrets) set, `/metrics` and `/version` need
  `Authorization: Bearer <token>`. `/healthz` and `/readyz` stay open for
  probes, but `/readyz` hides check details without the token. An unreadable
  token file refuses requests rather than serving them. The workstation
  scripts send the token from `.env`.
- Live reload: `SIGHUP` (`docker kill -s HUP rws-dicom`) re-reads the config
  and applies `remote_aes` and `allowed_aes` without dropping associations.
  A config that fails to load keeps the running settings.
- C-FIND: `PatientName` matches case-insensitively, including non-ASCII
  letters (`müller*` finds `MÜLLER^HANS`); turn off with
  `RWS_DICOM_FIND_CASE_SENSITIVE_NAMES=true`. `StudyTime` now matches single
  times and ranges (`0800-1200`, `0830-`, `-1700`, old `08:30` style); a
  partial bound covers its whole period. Responses containing non-ASCII text
  are labelled `ISO_IR 192` (UTF-8) instead of echoing a character set that
  didn't match the bytes. Index schema version 3 (adds a folded name column,
  filled in for existing studies on first start).
- S3 storage backend (`RWS_STORAGE_DRIVER=s3`) for AWS S3 and S3-compatible
  stores (MinIO, Ceph, NAS appliances). Uploads carry a Content-MD5 so a
  corrupted upload is rejected. The store contract and the full DIMSE
  end-to-end tests run on S3 too (`make test-s3`, SeaweedFS in Docker). New
  dependency: `github.com/minio/minio-go/v7` (Apache-2.0).
- Audit log: structured `audit=true` records for received instances,
  queries, exports (C-MOVE/C-GET, with destination, studies and patient IDs)
  and rejected associations, named after the DICOM audit message types
  (PS3.15 A.5) so they can become ATNA syslog messages later. Turn off with
  `RWS_DICOM_AUDIT_LOG_OFF=true`.
- Configurable patient IDs: `LIBRE_DCM_EVENTS_OMIT_PATIENT_ID` and
  `LIBRE_DCM_AUDIT_OMIT_PATIENT_ID` leave the patient ID out of bus events
  and audit records (default: included).
- Configurable TLS: admin endpoint over HTTPS (`LIBRE_DCM_ADMIN_TLS_CERT` /
  `_KEY`; the container health check follows), `-gen-cert DIR` creates a
  self-signed certificate (kept unless `-gen-cert-force`), and `-echo` uses
  DICOM TLS when the node has a certificate (`-echo-tls`).
- Index conformance suite: the same query, outbox and upgrade tests run
  against SQLite and PostgreSQL (`make test-postgres`).
- Stored instances and `instance.stored` events now record the sending AE
  title (`source_ae`), which was always empty before.
- `BACKLOG.md`: the clustering change backlog (storage abstraction, shared
  catalog, eviction, replication, role-based nodes).

### Changed (licensing, safety)
- **Licensing never stops DICOM services** (issue #5, risk control for
  HAZ-010, decided 2026-11-09). Previously an official build refused C-STORE
  once the license had expired and the 30-day grace period was over; in
  clinical use that would stop modalities from archiving studies. Now all
  DICOM services run in every license state. Instead:
  - the grace period logs a daily warning;
  - an unlicensed node logs an error every hour, blocks configuration reloads
    (SIGHUP) and reports the state in `/version` and the new metric
    `libre_dcm_license_ack_required`, until an administrator runs
    `LibreDCM -license-ack NAME` (`LibreDCM.sh` / `LibreDCM.ps1 license-ack NAME`);
  - the acknowledgement is written to the audit log and lasts 24 hours.
  `libre_dcm_license_state` 2 now means "unlicensed" (was "not receiving").
  `-license-status` exit codes: 0 licensed, 2 unlicensed, 3 no license needed.
  The gateway no longer has a receive gate (`UseReceiveGate` removed).

### Added (licensing)
- Libre RWS and LibreDCM are licensed under Apache-2.0 (`LICENSE`, `NOTICE`; decided 2026-11-08).
- **LibreKey licensing for official builds** (decided 2026-11-07): official
  builds (`BUILD_TAGS=official`) embed the LibreKey public key and need a
  machine-locked license (annual or perpetual). After expiry there is a
  30-day grace period (what happens after it: see "Changed (licensing, safety)" above). Source
  builds need no license. New flags `-license-status`, `-license-request`,
  `-license-activate`, `-license-install`; the installer activates online
  (license key) or offline (request file); metrics `libre_dcm_license_state`
  and `libre_dcm_license_days_left`. LibreKey client copied to
  `third_party/librekey` (standard library only).

### Changed
- **Code identifiers renamed** (decided 2026-11-07): program `LibreDCM`
  (was `rws-dicom`), image `libre/dcm` (was `rws/dicom`), settings
  `LIBRE_DCM_*` (was `RWS_DICOM_*` / `RWS_*`), event subjects `libre.dcm.*`
  (was `rws.dicom.*`), metrics `libre_dcm_*` (was `rws_dicom_*`), default
  AE title `LIBREDCM`, Go module `github.com/mulcahyio/libre-rws`. Backward
  compatible: `RWS_*` settings are still read (a warning names the
  replacement), events are also published under the old subjects
  (`LIBRE_DCM_EVENTS_LEGACY_SUBJECTS=false` stops it), and an upgraded install
  keeps its `RWS_DICOM` stream. Metric names changed without aliases.
- Workstation package: `LibreDCM.sh` / `LibreDCM.ps1` (were `rws-dicom.*`),
  container `libredcm`. `install` now asks the install-time choices (storage:
  this PC or S3; TLS: off, self-signed or your own files; patient IDs in
  events and audit log) and generates an admin token; `configure` asks again;
  `-Yes` / `LIBREDCM_YES=1` for unattended installs. `upgrade` moves a 0.1.x
  install over: renames `.env` keys (backup kept), removes the old container
  and keeps the `rws-dicom-data` volume.
- An environment variable that is set but empty no longer overrides the
  config file (compose passes every variable through, so empty values were
  wiping file settings).

### Fixed
- Cluster HAProxy: a gateway that has just died no longer fails new
  associations for ~10 s; connects are retried on another node
  (`retries 3`, `option redispatch`) and health checks run every 2 s.
- SQLite wildcard queries were case-insensitive for ASCII while PostgreSQL's
  were case-sensitive. Both are now case-sensitive (found by the conformance
  suite); name matching uses its own case-insensitive path.
- Workstation `backup` is now consistent while the module runs: it snapshots
  the index with `-backup`, then copies the snapshot and the images
  ([#1](https://github.com/mulcahyio/libre-rws/issues/1)). The stop/backup/start
  workaround is no longer needed.
- `install.cmd` no longer shows as modified in a fresh clone (`.gitattributes`
  now leaves it as stored, with CRLF line endings).
- The workstation `.env` and `backups/` folder are git-ignored, so local
  settings and backups can't be committed by mistake.

### Documentation
- `INSTALL.md`: Linux host firewalls can block C-MOVE to a viewer on the same
  PC (`host.docker.internal`); how to allow it.
- `INSTALL.md`: how to take a consistent backup (see Known issues).

### Tested (2026-11-07)
- Workstation install, upgrade (index v2 to v3 in place), `backup` under load
  and with a NATS outage: 12 events stored during the outage were all
  published once after recovery.
- DCMTK conformance against the workstation and the 3-node cluster:
  C-ECHO, C-STORE (Explicit VR LE, JPEG Baseline, JPEG 2000 lossless and
  lossy, RLE, JPEG-LS), C-FIND, C-MOVE with byte-identical compressed pixel
  data. JPEG 2000 is now covered.
- Cluster (PostgreSQL, NATS, HAProxy, 3 gateways): load spread over all
  nodes; with one node killed and NATS stopped, 15 stores succeeded, both
  surviving nodes drained the shared outbox after recovery and the stream
  held exactly one event per store (52 of 52).
- Windows (Surface Pro 7, Windows 11 Home, Docker Desktop 4.96, PowerShell
  5.1): installed 0.1.1-beta.1, stored studies, then upgraded with
  `LibreDCM.ps1 upgrade -Yes`: settings renamed, old container removed, old
  data volume kept, index upgraded v1 to v3, studies still found. Self-signed
  and own-file TLS (DICOM TLS store/find, plain DICOM refused, admin over
  HTTPS), `backup` and `status` passed.
- Linux: interactive `install` (self-signed TLS, patient IDs left out of
  events), then S3 storage (SeaweedFS) with TLS; events carried an empty
  patient ID and were also published under the legacy subject.
- Licensing (Linux, official build against a local LibreKey server): an
  unattended install without a key wrote an activation request and stopped;
  with a key it activated online and stored images; an expired license
  refused C-STORE while C-ECHO and C-FIND worked; reinstalling a valid
  license resumed receiving. Windows: an unattended official install without
  a key wrote the request file and stopped as designed.
- Not tested: completing the offline activation on Windows; the Windows question prompts (needs a signed-in desktop
  session) and S3 on Windows.

### Tested (2026-11-09, release verification of v0.2.0-beta.1)
Run with `scripts/qms/release-verify.sh` on the release branch (Go 1.26.10,
pinned base images); logs in
[docs/releases/v0.2.0-beta.1-verification/](docs/releases/v0.2.0-beta.1-verification/).
- `go vet` (default and `official` tags), `go test -race` (9 packages),
  `go test -tags official`, PostgreSQL index suites, S3 suites (SeaweedFS):
  all pass.
- govulncheck: no vulnerabilities found (GO-2026-6809 fixed by Go 1.26.10).
- SBOM: 49 components.
- Source and official container images built; DCMTK conformance against the
  source image: C-ECHO, C-STORE (Explicit VR LE, JPEG Baseline, JPEG 2000
  lossless and lossy, RLE, JPEG-LS), C-FIND, C-MOVE, compressed pixel data
  byte-identical. `/version` reports `0.2.0-beta.1`.
- Official build **with no licence**: container healthy, the same
  conformance run passes (DICOM keeps working), one "unlicensed" error
  logged, SIGHUP reload blocked, `/version` shows `license_state: missing`
  and `license_ack_required: true`; after `LibreDCM -license-ack` the audit
  record appears within a minute, reload works and `license_ack_required`
  turns false.
- Not tested in this release run: Windows (`LibreDCM.ps1 license-ack`), the
  cluster topology (last run 2026-11-07, pre-QMS), installer upgrade.

## rws-dicom 0.1.1-beta.1 (2026-10-11)

First published release. Workstation container package.
Release notes: [docs/releases/v0.1.1-beta.1.md](docs/releases/v0.1.1-beta.1.md)

### Added
- `deploy/docker/Dockerfile.dicom`. It was missing from 0.1.0, so
  `make image`, `make up` and `make cluster` could not work.
- `deploy/workstation/`: single-node compose, `.env` as the only settings
  file, `rws-dicom.ps1` / `rws-dicom.sh` (install, upgrade with index backup,
  verify, status, logs, backup, uninstall), `install.cmd`, `INSTALL.md`.
- `-echo HOST:PORT` flag: built-in C-ECHO for install checks, no DCMTK needed.
- `RWS_DICOM_REMOTE_AES`: C-MOVE destinations from one environment variable
  (`NAME=HOST:PORT[:AE]`, comma-separated).
- Beta version numbering. `make build` and `make image` take the version from
  the git tag and drop the leading `v`.
- `CHANGELOG.md` and release notes in `docs/releases/`.

### Changed
- Runtime image base is now `distroless/base-debian12:nonroot`. The JPEG
  codec needs the glibc loader, so `distroless/static` could not start the
  binary.

### Verified
- Go vet and tests pass (Go 1.26).
- Container image built and run: healthy, `/version` reports
  `0.1.1-beta.1`, `/readyz` 200, C-ECHO through the published port.

## rws-dicom 0.1.0-beta.1 (2026-10-04)

First version of the DICOM module. It was not released on its own; the git
tag was added when 0.1.1-beta.1 was published.

### Added
- Modular Go DICOM gateway (DIMSE): C-ECHO, C-STORE, C-FIND, C-MOVE, C-GET.
- Shared platform chassis: YAML + environment config, structured logging,
  `/healthz` `/readyz` `/metrics` `/version`, graceful shutdown.
- Pluggable back ends: filesystem storage; SQLite or PostgreSQL index with
  cluster-safe migrations; `none`, `log` or NATS JetStream events.
- RWS fork of gonetdicom v0.17 (`third_party/gonetdicom`): correct
  transfer-syntax negotiation for compressed data and a streaming C-MOVE
  sender that keeps the stored encoding.
- Compose files for single node and a 3-node cluster behind HAProxy; DCMTK
  conformance script; development document.
