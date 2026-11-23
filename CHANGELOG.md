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
- **LibreDVT v0.2 web UI, command line, reports and example plans for the
  new protocols (issue #34, card DVT-0.2/5, DRAFT for approval).**
  - Targets page: separate forms for DIMSE targets (with an optional DICOM
    TLS block: minimum version, CA file, server name, client certificate
    and key, the target's certificate on C-MOVE) and DICOMweb targets (base
    URL, bearer token from a file, TLS settings). The forms use the target
    file schema: an extra field such as an inline `token` refuses the form.
    Secrets are file paths only and must lie under `secrets_dir` when the
    target is saved. The table shows protocol, endpoint, TLS settings and
    authentication by path, and checks the referenced files; it never shows
    their contents. Any TLS field turns TLS on.
  - Reports (HTML, JUnit, JSON): the target's protocol, endpoint and class;
    an *Endpoint identity* section (TLS versions and ciphers, server
    certificate subject, issuer, names, validity with days left, SHA-256,
    verification, Implementation Class UID and Version Name); per step every
    exchange, TLS details, association results per presentation context,
    HTTP headers (`Authorization` always `[REDACTED]`), body facts,
    multipart parts, per-instance results and every assertion. JUnit
    carries the same facts as properties and every assertion in
    `system-out`. Runs stored by v0.1 load and render as before.
  - `libredvt run` prints the target and the endpoint identity. The web UI
    and the command line build their runner the same way; tests run one
    plan and target through both and compare the results.
  - Bundled example plans: *LibreDCM smoke (DIMSE + TLS)*, *Generic DICOM
    TLS audit* (sends no images) and *Generic DICOMweb audit* (keeps no
    response-body excerpts in its evidence). The plans page lists each
    bundled plan's step types.
  - Fixed: the web UI failed to start when a report had already been
    rendered in the same process (html/template cannot clone a used
    template set); found by the parity test.
  - Tested: unit tests, real-engine parity tests against the Go DIMSE TLS
    peer and DICOMweb TLS server, the three bundled plans through the web UI
    against LibreDCM (TLS) and Orthanc + DICOMweb (HTTPS), all passing with
    no token in any artefact, screenshots in light and dark mode checked by
    eye, mutation check; see
    `docs/dhf/libredvt/evidence/v0.2-ui/`.
- **LibreDVT v0.2 DICOMweb QIDO-RS, WADO-RS and STOW-RS tests (issue #34,
  card DVT-0.2/4, DRAFT for approval).**
  - New `qido` step: searches at study, series or instance level with
    query keys (keywords or tags), checks the HTTP status, the
    `application/dicom+json` content type, the JSON structure, each
    object's unique key and the queried values, expected or absent UIDs,
    required attributes and paging (`limit`/`offset`, a page larger than
    asked fails). `from_store` searches one stored UID at a time.
  - New `wado` step: retrieves a study, series or instance (or its
    metadata) and checks the multipart response strictly (boundary only from
    the Content-Type, closing delimiter required), each part's content type,
    transfer syntax (header and file), Study/Series/SOP Instance UIDs,
    missing, extra and duplicate instances, and, with `compare_pixels`,
    pixel data byte for byte against the sent file. `from_store` retrieves
    every stored study.
  - New `stow` step (lab targets only): sends files in batches and accounts
    for every instance in the response (Referenced SOP Sequence, Failed SOP
    Sequence with its Failure Reason, warnings, extra or wrong references).
    A failed instance fails the step even when the HTTP status says success;
    instance warnings fail unless `allow_warnings: true`. `verify_retrieve` reads
    each stored instance back and compares its pixel data.
  - `tls_reject` now also works on DICOMweb targets.
  - Security: bearer token read from its file and sent only to the base
    URL's own origin (over `http://` only to a loopback host); every
    recorded `Authorization` value is `[REDACTED]`; redirects are never
    followed; one request at a time; size limits fail the step and never
    truncate. No new dependency (the worker uses Python's standard library).
  - Tested: unit tests with a fake engine; real-engine tests against a new
    Go DICOMweb test server with about 50 fault switches (wrong boundary,
    truncated multipart, wrong content type, wrong UIDs, failed instances
    reported as success, redirects, oversized bodies, a token echoed into
    an error page), each of which fails the step; mutation check; cross-check
    against Orthanc 1.13.1 with the DICOMweb plugin 1.24 in a digest-pinned
    test container (5 of 5 runs as expected, 6 of 6 stored files
    byte-identical, no token or key in any of 30 artefacts). Evidence in
    `docs/dhf/libredvt/evidence/v0.2-dicomweb/`.
- **LibreDVT v0.2 association negotiation and DICOM TLS tests (issue #34,
  card DVT-0.2/3, DRAFT for approval).**
  - New `associate` step: proposes the plan's presentation contexts (with
    optional role selection and asynchronous operations window) and checks
    each accepted/rejected result and transfer syntax, an expected
    A-ASSOCIATE-RJ (result, source, reason), max PDU, Implementation Class
    UID and Version Name. Abort, closed connection, unparseable PDU and
    silence fail the step; a failed release is a warning. The worker speaks
    the association PDUs itself so nothing the target answered is hidden.
  - New `tls_reject` step: passes only when the target clearly refuses
    clear text, a TLS 1.0/1.1 handshake, or a client without a certificate.
    Probes carry no certificate, key or token.
  - DIMSE targets with a `tls` block now work for every step (the card 2
    refusal is removed): TLS 1.2+, chain checked against the target's CA
    (or the image trust store), host name against subjectAltName only, no
    bypass, optional client certificate; TLS version, cipher and peer
    certificate facts are in every report. A v0.1 step without verified TLS
    facts fails. A `move` to a TLS target needs the new
    `LIBRE_DVT_SCP_TLS_CERT`/`LIBRE_DVT_SCP_TLS_KEY`; the Storage SCP then
    requires the target's client certificate by default.
  - Servers offering only legacy cipher suites (no forward secrecy) fail
    the TLS handshake; this is intended (fail closed).
  - Tested: unit tests with fake engines; real-engine tests against a new
    Go test peer and synthetic PKI (wrong host, CN-only names, unknown CA,
    expired, not yet valid, incomplete chain, self-signed, TLS 1.0/1.1-only,
    client-certificate cases, reject/abort/close/garbage/silence); mutation
    check (host-name check off, TLS 1.1 accepted, rejected context ignored,
    and more); cross-check against DCMTK 3.6.7 storescp/echoscu with and
    without `+tls` and against LibreDCM in TLS mode. Evidence in
    `docs/dhf/libredvt/evidence/v0.2-assoc-tls/`. No new dependency.
- **LibreDVT v0.2 foundation (issue #34, card DVT-0.2/2, DRAFT for
  approval).** First code of v0.2; v0.1 plans, targets and results behave
  as before.
  - Targets now have a protocol, `dimse` (default, so v0.1 records load
    unchanged) or `dicomweb` (base URL, optional bearer token given as a
    path to a token file), and an optional TLS block (CA, server name,
    client certificate and key, TLS 1.2 or 1.3). There is no switch that
    turns certificate checks off. The registry and the new
    `libredvt run -targets FILE` are parsed strictly: unknown fields,
    inline secrets and fields of the other protocol are refused. A bearer
    token over `http://` is only allowed to a loopback host.
  - Plans accept the v0.2 step types (`associate`, `tls_reject`, `qido`,
    `wado`, `stow`) and a `limits` block with every rule of
    `plan-schema.md`; a field of another step type is refused even with an
    empty value, and a plan whose steps do not fit the target's protocol is
    refused before any step runs. Their engines followed in card 3
    (`associate`/`tls_reject`) and card 4 (DICOMweb).
  - Production guard extended: `stow`, `qido` and `wado` run only against
    `lab` targets.
  - Protocol-neutral evidence model and one verdict rule
    (`result.DecideStep`): a step with no assertion fails as "nothing was
    checked"; any problem fails; warnings give "pass with warnings".
  - Secrets: referenced files are checked before any connection (missing,
    empty, not a regular file, outside `LIBRE_DVT_SECRETS_DIR` for web runs);
    no process starts with a secret in its arguments or environment; step
    results, the run and the JSON/HTML/JUnit files are scanned for the
    configured secrets, and a hit is replaced by `[REDACTED]` and fails the
    run.
  - Tested: new unit tests for the target schema, plan rules, protocol
    check, guard, secret preflight, verdict rule, redaction, argv/env
    guard and artefact scan; mutation check 33 of 33 killed; real-engine
    test stage green; the bundled LibreDCM plan gives identical results
    with the v0.1 image and this build. Evidence in
    `docs/dhf/libredvt/evidence/v0.2-foundation/`.
- **LibreDVT v0.2 design inputs (issue #34, DRAFT for approval).** Patrick
  approved v0.2 on 2026-11-19 with decisions D1–D5 (association + TLS tests,
  DICOMweb QIDO-RS/WADO-RS/STOW-RS, bearer-token plumbing, TLS 1.2+ with no
  insecure bypass, any vendor's lab systems, no workflow protocols). Design
  documents only, no code change:
  - `docs/dhf/libredvt/SRS.md` §6–§12: 57 new `Draft` requirements,
    DVT-SRS-090…-156, each with acceptance criteria.
  - Intended use extended to Mulcahy.io client interoperability audits of
    other vendors' lab systems (user T4, needs UN-07…UN-11).
  - Tool risk analysis: new hazards DVT-HAZ-006…-010 (false pass on TLS or
    host name, secret leakage, store to production through new paths,
    misparsed multipart/STOW responses, load on third-party systems) and
    assurance activities 6–12.
  - Threat model for the new outbound connections in `cybersecurity.md`;
    new `plan-schema.md` (strict targets, plans, settings; secrets by
    reference only) and `evidence-model.md` (protocol-neutral evidence,
    verdict rule, redaction); `architecture.md` §7 with the settled design
    decisions; `v0.2-plan.md` with the decisions and the card plan.
- **LibreDCM DICOM Conformance Statement (issue #32, DRAFT for approval).**
  `docs/dhf/libredcm/dicom-conformance-statement.md` follows the DICOM PS3.2
  (2026d) Annex N template: SOP classes, transfer syntaxes, association
  policies, C-FIND keys per level with matching types, C-MOVE destination
  rules, C-GET role selection, status codes, character sets, configuration,
  TLS, allow-list and audit, and an honest list of known deviations. No
  change to LibreDCM's behaviour.
  - Its tables are generated from the running code by
    `TestConformanceStatementMatchesCode` (regenerate with `-update-dcs`) and
    checked again, independently, by the new QMS tool `scripts/qms/dcs.py`
    in the CI `verify` job (validated by 13 tests in
    `scripts/qms/test_qms_tools.py`), so the statement cannot drift from the
    code (DCM-SRS-101).
  - Every behaviour statement was checked with DCMTK 3.6.7 and pynetdicom
    3.0.4 against five LibreDCM containers; logs and the rerunnable script
    are in `docs/dhf/libredcm/evidence/dcs-dcmtk/`.
  - Found while writing it and recorded in `anomalies.md`, not fixed here:
    ANOM-011 (C-GET stops without a final response when a matched SOP class
    has no storage context, and the export audit overstates what was sent),
    ANOM-012 (C-FIND result limit answers Success), ANOM-013 (C-CANCEL not
    acted on during C-MOVE/C-GET), ANOM-014 (wrong Called AE Title logged but
    not audited).
- **Libre Systems DICOM UID root (issue #30, closes ANOM-007).** Libre
  Systems' own UID root `1.3.6.1.4.1.67686` (IANA Private Enterprise Number
  67686), with its layout as a controlled document
  (`docs/qms/uid-registry.md`, QMS-REG-001, owner Patrick Mulcahy; layout
  approved 2026-11-17).
  - **Changed behaviour:** LibreDCM now identifies itself with
    Implementation Class UID `1.3.6.1.4.1.67686.1.1.1.<major>.<minor>` and
    Implementation Version Name `LIBREDCM_<major>_<minor>` (e.g.
    `LIBREDCM_0_2`) in every association it accepts or requests and in the
    File Meta (0002,0012)/(0002,0013) of every file it stores (DCM-SRS-098).
    Before, it used gonetdicom's root `1.2.826.0.1.3680043.10.541.1` with
    `LIBREDCM_020`, and its C-MOVE sub-operations, `-echo` and the control
    API C-ECHO test sent `GONETDICOM_001`. Both values come from the build
    version; development builds identify as 0.0. Stored data sets are not
    changed.
  - The build version now lives in package
    `modules/dicom/internal/buildinfo` (one source for `-version`,
    `/version`, licensing, the control API and the UID). Builders that set
    `-X main.version` must use
    `-X github.com/mulcahyio/libre-rws/modules/dicom/internal/buildinfo.Version`
    (the Dockerfile and Makefile are updated); CI checks the image reports
    its build version.
  - LibreDVT's worker sends and writes LibreDVT's own values
    (`1.3.6.1.4.1.67686.1.3.1.<major>.<minor>`, `LIBREDVT_<major>_<minor>`,
    DVT-SRS-082) instead of pynetdicom's. Worker 0.1.1; tool re-validated.
  - New package `platform/dicomuid`: the layout, a UID validity check and a
    UID generator (`1.3.6.1.4.1.67686.2.<product>.<site>.<time>.<counter>`,
    DCM-SRS-099) for the coming IHE work; no product uses it yet.
  - Our own test data moved to the test-data arc `1.3.6.1.4.1.67686.9`; an
    architecture test keeps that arc and gonetdicom's root out of shipped
    code (DCM-SRS-100).
  - Tested: unit and architecture tests, a test that reads the
    A-ASSOCIATE-AC, a stored file and LibreDCM's C-MOVE A-ASSOCIATE-RQ, the
    LibreDVT engine tests (57 passed), and DCMTK 3.6.7 `echoscu`,
    `storescu`, `movescu` and `dcmdump` against a container built as
    `0.2.0-uidroot.1` (all show `1.3.6.1.4.1.67686.1.1.1.0.2` /
    `LIBREDCM_0_2`; logs in `docs/dhf/libredcm/evidence/uid-root-dcmtk/`).
- **Libre WT 0.2 release candidate: system test, user guide, risk
  management report (card WT-0.2/9, issue #20).**
  - System test against real containers
    (`scripts/e2e/wt-system-test.sh`, stack
    `deploy/compose/docker-compose.wt-system.yml`): two LibreDCM nodes (a
    source build and an unlicensed official build), Libre WT and DCMTK. 87
    checks through the web pages as Viewer, Operator and Admin cover every
    Libre WT function: first run, accounts, roles, CSRF, fleet, dashboard,
    status, Study Exceptions with an edit, configuration, destination and
    allow-list changes (the allow-list change takes effect on the node),
    rollback, reload (blocked until the licence notice is acknowledged on
    the unlicensed node), C-ECHO, studies, send of a study and of one series
    to DCMTK `storescp` with every received data set byte-identical to the
    stored one, licence acknowledgement and request file, backup (checksum
    compared with the file on the node), LibreDCM audit records, logs, CSV
    export, the Libre WT audit log and a scan for the admin tokens. Synthetic
    data only. Record: `docs/releases/librewt-v0.2.0-rc.1-verification/`.
  - User guide with screenshots of all 35 pages in light and dark
    (`docs/librewt/user-guide.md`), made by `scripts/e2e/wt-screenshots.mjs`
    (headless Chromium, no packages), which also fails when a page is
    wider than its window.
  - Install steps for Libre WT next to the workstation LibreDCM
    (`deploy/workstation/INSTALL.md`), checked with the candidate images.
  - Risk management report for Libre WT 0.2 with the residual risk of every
    hazard and the benefit-risk statement (`docs/dhf/librewt/risk-management.md`),
    system-level entries in both manual-verification files, tool validation
    of the system test, and open decisions D59…D62.

  No new dependency. No change to LibreDCM.
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
- **Libre WT tests no longer expire (issue #30).** The fake LibreDCM used by
  the Libre WT tests dated its audit records and log lines 2026-11-16, so
  `TestLibreDCMAuditPage` and `TestLogsPage` failed from 2026-11-18 on (the
  records fell outside the default 24-hour window). They are now dated an
  hour before the test runs. Test code only.
- Libre WT: tables on the configuration, LibreDCM audit record and log
  pages could be wider than the page (the log page up to 4164 px), which cut
  off the last column (for example "live"/"restart" on the configuration
  page). Long values now wrap inside their cell; native form controls
  follow the dark colour scheme. Found by the screenshot review of card
  WT-0.2/9 (D62).
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
