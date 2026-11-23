# LibreDVT Software Requirements Specification (SRS)

| | |
|---|---|
| Document | DHF-DVT-SRS |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude from issue #12 and Patrick Mulcahy's decisions of 2026-11-10) |
| Software item | LibreDVT (DICOM Validation & Test), a module of the libre-rws repository |
| Regulatory role | Not a medical device and not part of the LibreRWS PACS. A software tool used in the quality system to produce verification records (ISO 13485 §4.1.6, QMS-SOP-011). Validated as a **high-risk tool** (see `risk-management.md`) |
| Release | v0.1 scope (§1–§5, Active). v0.2 additions (§6–§12) written 2026-11-19 by Hermes/Claude from issue #34 and Patrick Mulcahy's decisions D1–D5 of 2026-11-19 (`v0.2-plan.md`); set `Active` on 2026-11-23 in card DVT-0.2/6 after verification (tool-validation record, `docs/qms/tool-validation/README.md`) |

Rules: the same as the LibreDCM SRS. IDs are permanent; never renumber or
reuse an ID, set Status to `Retired` instead. Status is `Active` (must be
verified), `Draft` (not yet agreed) or `Retired`. Tests reference IDs with
`// Verifies: DVT-SRS-###`; `scripts/qms/trace.py --module libredvt --prefix DVT`
builds the trace matrix. "Tool hazard" names the `risk-management.md` entry
the requirement controls.

## 1. Object validation

| ID | Requirement | Status | Tool hazard |
|---|---|---|---|
| DVT-SRS-001 | LibreDVT shall validate DICOM files given as single files, directories (recursively) or zip archives, from the web UI and from the command line. | Active | |
| DVT-SRS-002 | LibreDVT shall validate each file with dicom3tools `dciodvfy` (PS3.3 IOD/module requirements, PS3.5 encoding, PS3.6 VR/VM) and record every error and warning it reports. | Active | DVT-HAZ-001 |
| DVT-SRS-003 | LibreDVT shall validate each file with pydicom `dicom-validator` against the DICOM standard edition built into the image, and record every error it reports. | Active | DVT-HAZ-001 |
| DVT-SRS-004 | LibreDVT shall check attribute consistency across all files of one validation run with dicom3tools `dcentvfy` (same Patient, Study and Series attributes for the same entity) and record every error it reports. | Active | DVT-HAZ-001 |
| DVT-SRS-005 | Every finding shall be stored in one result model with: engine, file, severity (error, warning), attribute path or tag when the engine gives one, and message; the engine's raw output shall be kept with the result. | Active | DVT-HAZ-004 |
| DVT-SRS-006 | A file that is not a DICOM file (no readable DICOM data set) shall be reported as an error "not a DICOM file", and shall not be passed to the IOD validators. | Active | DVT-HAZ-001 |
| DVT-SRS-007 | When an engine cannot run, crashes, times out, or produces output LibreDVT cannot parse, the file's result shall be "engine error" (a failure), never a pass. | Active | DVT-HAZ-001 |
| DVT-SRS-008 | Every validation result shall record the LibreDVT version, the version of each engine used and the DICOM standard edition of `dicom-validator`. | Active | DVT-HAZ-005 |
| DVT-SRS-009 | A file's verdict shall be "fail" if any engine reports an error or an engine error occurred, "pass with warnings" if only warnings were reported, and "pass" otherwise. | Active | DVT-HAZ-001 |

## 2. DICOM service tests

| ID | Requirement | Status | Tool hazard |
|---|---|---|---|
| DVT-SRS-020 | LibreDVT shall keep a registry of test targets, each with name, host, port, called AE title, calling AE title and a class of `lab` or `production`; a target without a class shall be treated as `production`. | Active | DVT-HAZ-002 |
| DVT-SRS-021 | LibreDVT shall test Verification (C-ECHO) against a target and pass only on a Success status. | Active | DVT-HAZ-001 |
| DVT-SRS-022 | LibreDVT shall send selected files to a target with C-STORE, proposing each file's own transfer syntax (compressed files stay compressed), and pass only when every file receives a Success status. | Active | DVT-HAZ-001 |
| DVT-SRS-023 | LibreDVT shall query a target with C-FIND (Study Root) at study level for given Study Instance UIDs and pass only when every expected study is returned with a final Success status. | Active | DVT-HAZ-001 |
| DVT-SRS-024 | LibreDVT shall retrieve studies from a target with C-MOVE to its built-in Storage SCP and pass only when the final status is Success and the set of received SOP Instance UIDs equals the set expected. | Active | DVT-HAZ-001 |
| DVT-SRS-025 | For each retrieved instance, LibreDVT shall compare the pixel data and SOP Instance UID with the file originally sent and fail on any difference. | Active | DVT-HAZ-001 |
| DVT-SRS-026 | The built-in Storage SCP's AE title and port shall be configurable, and it shall only run for the duration of the test step that needs it. | Active | |
| DVT-SRS-027 | LibreDVT shall test that a target rejects an association with a given calling or called AE title, and pass only when the association is rejected. | Active | DVT-HAZ-001 |
| DVT-SRS-028 | LibreDVT shall refuse to run C-STORE and C-MOVE steps against a target whose class is not `lab`, and report the step as failed with the reason. | Active | DVT-HAZ-002 |
| DVT-SRS-029 | Every network step shall have a timeout; a target that does not answer within it shall fail the step. | Active | DVT-HAZ-001 |

## 3. Test plans, results and reports

| ID | Requirement | Status | Tool hazard |
|---|---|---|---|
| DVT-SRS-040 | Test plans shall be YAML files naming a target and a list of steps (validate, echo, store, find, move, reject); each step may list the requirement IDs it verifies (`verifies:`). Unknown step types or fields shall be rejected before any step runs. | Active | DVT-HAZ-004 |
| DVT-SRS-041 | The command `libredvt run PLAN` shall run a plan with the same code as the web UI and exit with status 0 only when every step passed. | Active | DVT-HAZ-001 |
| DVT-SRS-042 | A plan run shall pass only when every step passed; a step that was not run (refused, aborted, earlier fatal error) shall count as failed. | Active | DVT-HAZ-001 |
| DVT-SRS-043 | LibreDVT shall write each run as JUnit XML (one test case per step, with the verified requirement IDs) for CI. | Active | |
| DVT-SRS-044 | LibreDVT shall write each run as a self-contained HTML report with the overall verdict, each step's result and findings, timestamps, the SHA-256 of the plan, the verified requirement IDs and the versions of LibreDVT and its engines. | Active | DVT-HAZ-005 |
| DVT-SRS-045 | Runs and validation results shall be stored in the data directory and listed in the web UI, newest first, and shall be deletable from the web UI. | Active | DVT-HAZ-003 |

## 4. Web interface, security and deployment

| ID | Requirement | Status | Tool hazard |
|---|---|---|---|
| DVT-SRS-060 | LibreDVT shall provide a web interface to validate uploaded files, manage targets, upload and run test plans, and view and delete results and reports. | Active | |
| DVT-SRS-061 | Every page and API call except the login page and a health check shall require a logged-in session. The admin password shall come from a file or environment variable; LibreDVT shall refuse to start without one. Sessions shall expire after inactivity. | Active | DVT-HAZ-003 |
| DVT-SRS-062 | State-changing requests shall be protected against cross-site request forgery, and login attempts shall be rate-limited. | Active | DVT-HAZ-003 |
| DVT-SRS-063 | The provided Docker Compose file shall publish the web port and the Storage SCP port on 127.0.0.1 only, unless the installer changes the bind address. | Active | DVT-HAZ-003 |
| DVT-SRS-064 | LibreDVT shall not need internet access at runtime; the DICOM standard used by `dicom-validator` shall be built into the image. | Active | DVT-HAZ-005 |
| DVT-SRS-065 | The container image shall run as a non-root user and pin the versions of every engine and base image. | Active | DVT-HAZ-005 |
| DVT-SRS-066 | The web interface shall warn users not to upload real patient data. | Active | DVT-HAZ-003 |

## 5. Independence and integration

| ID | Requirement | Status | Tool hazard |
|---|---|---|---|
| DVT-SRS-080 | LibreDVT's DICOM parsing and networking shall not use the libraries LibreDCM uses (godicom, gonetdicom or its fork). | Active | DVT-HAZ-001 |
| DVT-SRS-081 | LibreDVT shall ship a test plan for LibreDCM that covers at least the checks of `scripts/conformance.sh` (C-ECHO, C-STORE of native and compressed transfer syntaxes, C-FIND, C-MOVE with pixel-data comparison) and the called-AE rejection. | Active | |
| DVT-SRS-082 | LibreDVT shall identify itself in every association its worker requests or accepts and in the File Meta of every Part 10 file its built-in Storage SCP writes with Implementation Class UID `1.3.6.1.4.1.67686.1.3.1.<major>.<minor>` and Implementation Version Name `LIBREDVT_<major>_<minor>` from its build version (0.0 for a build without a release version), not with the engine library's own values (issue #30, `docs/qms/uid-registry.md`). | Active | |

## v0.2 additions

**DRAFT for approval.** Sections 6 to 12 are the v0.2 design inputs
(issue #34). The rows were `Draft` while cards DVT-0.2/1 to /5 built them
and became `Active` in card DVT-0.2/6, once each had passing verifying tests
(trace check) and the tool validation was recorded. Scope (decision D1): association negotiation, TLS,
and DICOMweb QIDO-RS, WADO-RS and STOW-RS. Storage Commitment, MPPS, MWL and
emulators are out of scope. Targets can be any vendor's lab system (D4).
The "Acceptance criteria" column says what the verifying test or positive
control must show. Unused numbers in the ranges stay free for later rows.

Terms used below:

- **Positive control:** a deliberately wrong input or test peer that the step
  must report as failed.
- **Test peer:** a small, deterministic DICOM or HTTP endpoint used only by
  the tests. It must not use LibreDCM's libraries (DVT-SRS-080).
- **Data step:** a step that sends or retrieves image data or attributes:
  `store`, `move`, `qido`, `wado`, `stow`. `find` keeps its v0.1 rule.
- **Assertion:** one expectation checked by a step, recorded with its expected
  value, actual value and outcome (`evidence-model.md`).

## 6. Association negotiation

| ID | Requirement | Status | Tool hazard | Acceptance criteria |
|---|---|---|---|---|
| DVT-SRS-090 | LibreDVT shall provide an `associate` step that requests an association with a DIMSE target, proposing the presentation contexts listed in the step (one abstract syntax UID and one or more transfer syntax UIDs each), and shall record for every proposed context whether it was accepted, the accepted transfer syntax, or the rejection reason. | Active | DVT-HAZ-001 | Against a test peer with a known acceptance table, the evidence lists every proposed context with the peer's result and transfer syntax. A proposed context missing from the A-ASSOCIATE-AC fails the step as a protocol error. |
| DVT-SRS-091 | The `associate` step shall pass only when every presentation context matches its expectation in the step: `accepted` or `rejected`, and, when given, the expected accepted transfer syntax. | Active | DVT-HAZ-001 | Positive controls fail: peer rejects a context expected accepted; peer accepts a context expected rejected; peer accepts with a transfer syntax other than the expected one. A peer that meets every expectation passes. |
| DVT-SRS-092 | The `associate` step shall be able to expect association rejection with given Result, Source and Reason values (PS3.8 A-ASSOCIATE-RJ), and shall pass only when the target rejects with exactly those values. | Active | DVT-HAZ-001 | Positive controls fail: peer accepts; peer rejects with another Reason; peer rejects with another Source. A matching rejection passes. |
| DVT-SRS-093 | The `associate` step shall record the target's negotiated parameters: maximum PDU length, Implementation Class UID, Implementation Version Name, and the asynchronous operations window reply when proposed. It shall check each one the step names an expectation for. | Active | DVT-HAZ-001 | Evidence holds the test peer's values. A peer whose maximum PDU length or Implementation Class UID differs from the expectation fails. |
| DVT-SRS-094 | The `associate` step shall be able to propose SCP/SCU role selection for a presentation context and check the target's role reply against the step's expectation. | Active | DVT-HAZ-001 | A peer that refuses an expected SCP role fails; a peer that grants it passes; the reply is in the evidence. |
| DVT-SRS-095 | When a target aborts (A-ABORT), closes the connection, sends a PDU LibreDVT cannot parse, or does not answer within the step timeout during association or release, the step shall fail and the evidence shall name the event (abort source and reason, connection closed, malformed PDU, timeout). | Active | DVT-HAZ-001 | Test peers that abort, close, send garbage bytes, and stay silent each fail the step with the matching event recorded. |
| DVT-SRS-096 | After its checks, the `associate` step shall release the association (A-RELEASE) and record whether the release completed. A failed release shall add a warning finding; it shall not change a failed check into a pass. | Active | DVT-HAZ-001 | A peer that aborts on release gives "pass with warnings" when all checks passed and "fail" when a check failed. |

## 7. TLS

| ID | Requirement | Status | Tool hazard | Acceptance criteria |
|---|---|---|---|---|
| DVT-SRS-100 | A DIMSE or DICOMweb target may enable TLS. With TLS, LibreDVT shall use TLS 1.2 or later only (the target may raise the minimum to 1.3) and shall never fall back to plaintext or to an older version. | Active | DVT-HAZ-006 | A test server that offers only TLS 1.1 fails the step; TLS 1.2 and TLS 1.3 servers pass. The schema rejects a minimum version below 1.2. No plaintext bytes reach a test server after a failed handshake. |
| DVT-SRS-101 | LibreDVT shall verify the target's certificate chain against the CA certificates configured for the target (a PEM file), or the image's system trust store when none is configured, and shall fail the step when verification fails. | Active | DVT-HAZ-006 | Positive controls fail: unknown CA, expired certificate, not-yet-valid certificate, incomplete chain, self-signed leaf. A chain to the configured CA passes. |
| DVT-SRS-102 | LibreDVT shall verify that the target's certificate matches the target's host name, or the `server_name` configured for the target, using the subjectAltName DNS and IP entries only (never the Common Name), and shall fail the step on a mismatch. | Active | DVT-HAZ-006 | Positive controls fail: certificate for another host; host name only in the Common Name. A matching DNS SAN and a matching IP SAN pass. |
| DVT-SRS-103 | No setting, flag, plan field or environment variable shall disable certificate or host-name verification. A target or plan field with that purpose shall be rejected as unknown. | Active | DVT-HAZ-006 | Schema tests reject `insecure_skip_verify`, `verify: false` and `check_hostname: false`. An architecture test finds no `InsecureSkipVerify`, `CERT_NONE` or `check_hostname = False` in non-test code under `modules/dvt`. |
| DVT-SRS-104 | A target may name a client certificate and private key (PEM files) for mutual TLS. LibreDVT shall present them when the target requests a client certificate. An unreadable or mismatched certificate or key shall fail the step before any connection is made. | Active | DVT-HAZ-006, DVT-HAZ-007 | A test server that requires a client certificate accepts LibreDVT with the configured pair. A missing key file and a key that does not match the certificate each fail before the test server sees a connection. |
| DVT-SRS-105 | LibreDVT shall provide a `tls_reject` step that passes only when the target refuses a connection that breaks the TLS policy. Modes: `plaintext` (no TLS to the TLS port), `tls_version` 1.0 or 1.1 (a handshake offering only that version), `no_client_cert` (no client certificate where the target requires one). Refused means the handshake fails or the connection closes before any association is accepted or any HTTP response is received. | Active | DVT-HAZ-006 | Positive controls fail: a test server that accepts plaintext; one that accepts TLS 1.1; one that does not require a client certificate (`no_client_cert`). Correct servers pass. If the engine cannot offer the requested old version, the step fails as an engine error, never passes or is skipped. |
| DVT-SRS-106 | A `tls_reject` probe shall never send a bearer token, a client private-key proof or any DICOM data beyond the A-ASSOCIATE-RQ or HTTP request line needed to see whether the target answers. | Active | DVT-HAZ-007 | A recording test server that accepts the probe receives no `Authorization` header, no P-DATA PDU and no client certificate. |
| DVT-SRS-107 | The evidence of every TLS connection shall record the negotiated TLS version and cipher suite, the peer certificate's subject, issuer, subjectAltName entries, validity dates and SHA-256 fingerprint, and the verification result. It shall never record private keys. | Active | DVT-HAZ-005, DVT-HAZ-007 | Against the test server, every field is present in the JSON evidence and the HTML report, and the configured private key's bytes appear in no artefact. |
| DVT-SRS-108 | When a `move` step's target uses TLS, the built-in Storage SCP shall listen with TLS 1.2 or later using the server certificate and key configured for LibreDVT (settings `scp_tls_cert` and `scp_tls_key`, `plan-schema.md` §4). It shall verify any client certificate the target presents against the target's CA, and shall refuse a connection without one unless the target sets `tls.move_client_cert: optional`. Without a configured server certificate the step shall fail with the reason; it shall not fall back to plaintext. | Active | DVT-HAZ-006 | A TLS move from a test peer passes; the same move without a LibreDVT server certificate fails before any C-MOVE is sent; a peer presenting a certificate from another CA is refused; a peer without a certificate is refused unless `optional` is set. |

## 8. DICOMweb targets and QIDO-RS

| ID | Requirement | Status | Tool hazard | Acceptance criteria |
|---|---|---|---|---|
| DVT-SRS-110 | A target shall have a protocol, `dimse` or `dicomweb`; a target without one shall be `dimse`, so v0.1 targets load unchanged. A `dicomweb` target shall have a base URL (scheme `http` or `https`, host, optional port and path, no user-info, query or fragment) and a class as in DVT-SRS-020. | Active | DVT-HAZ-002, DVT-HAZ-004 | A v0.1 target record loads as `dimse` with its class unchanged. URLs with another scheme, user-info, a query or a fragment are rejected. |
| DVT-SRS-111 | A `dicomweb` target shall use no authentication or a bearer token read from the file named in `auth.token_file`. LibreDVT shall send the token as `Authorization: Bearer <token>` on requests to the target's own origin (scheme, host and port of the base URL) and never to another origin. | Active | DVT-HAZ-007 | A test server sees the header on every request. No request to any other origin carries it. |
| DVT-SRS-112 | LibreDVT shall refuse to send a bearer token over `http://` unless the target host is a loopback address (127.0.0.0/8, ::1 or `localhost`). The refusal shall fail the step before any connection is made. | Active | DVT-HAZ-007 | A token-bearing `http://` target on a non-loopback address fails with no connection attempt; the same target on 127.0.0.1 runs. |
| DVT-SRS-113 | LibreDVT shall provide a `qido` step that searches at study, series or instance level (PS3.18 search resources under the base URL) with the query parameters given in the step (attribute keyword or tag with value, `includefield`, `fuzzymatching`) and `Accept: application/dicom+json`. | Active | DVT-HAZ-001 | The test server's request log shows the exact path, query and Accept header for each level. |
| DVT-SRS-114 | The `qido` step shall pass only when the response is HTTP 200 with Content-Type `application/dicom+json` and a body that is a JSON array of DICOM JSON objects (PS3.18 Annex F), and every expected UID (listed in the step or from earlier store or stow steps) is present at the requested level. When the step expects no match, only 204 or an empty array passes. | Active | DVT-HAZ-001 | Positive controls fail: a missing expected UID; wrong Content-Type; malformed JSON; an HTML error page with 200; 204 when results are expected; 500. |
| DVT-SRS-115 | The `qido` step shall check that every returned object holds the unique key of its level and each attribute named in the step's `require_attributes`, and shall add an error finding for every missing one. | Active | DVT-HAZ-001 | A response object without Series Instance UID at series level fails; one missing a required attribute fails with a finding naming the attribute and the object. |
| DVT-SRS-116 | The `qido` step shall check that every returned object matches the step's single-value, non-wildcard query keys, and shall fail on any object that does not match. | Active | DVT-HAZ-001 | A test server that returns a study with another Patient ID than queried fails the step. |
| DVT-SRS-117 | When the `qido` step sets `limit` (and optionally `offset`), LibreDVT shall request page after page until a page holds fewer than `limit` objects or is empty, shall fail when a page holds more than `limit` objects or the page count passes the configured maximum, and shall check the combined result as in DVT-SRS-114. | Active | DVT-HAZ-001 | Paged results that together hold all expected UIDs pass; a page larger than `limit` fails; a server that never stops paging fails at the maximum. |
| DVT-SRS-118 | Every DICOMweb request shall be recorded as an exchange: method, URL with query (credentials never part of it), request headers with authentication values redacted, response status, response headers, elapsed time, and the raw response body up to a size cap with the SHA-256 and size of the full body. | Active | DVT-HAZ-004, DVT-HAZ-007 | Evidence holds every field for each request of a qido, wado and stow step; the `Authorization` value shows `[REDACTED]`. |

## 9. WADO-RS

| ID | Requirement | Status | Tool hazard | Acceptance criteria |
|---|---|---|---|---|
| DVT-SRS-119 | LibreDVT shall provide a `wado` step that retrieves a study, series or instance (PS3.18 retrieve resources under the base URL), named in the step or taken from earlier store or stow steps, with `Accept: multipart/related; type="application/dicom"` and the step's optional `transfer_syntax` parameter. | Active | DVT-HAZ-001 | The test server's request log shows the path and Accept header for each level, with and without a transfer syntax. |
| DVT-SRS-120 | LibreDVT shall parse multipart responses strictly (RFC 2046, RFC 2387): boundary from the Content-Type header, each part's headers, and the closing delimiter. A missing or unknown boundary, a missing closing delimiter, a part whose Content-Type is not `application/dicom`, or a part that does not parse as DICOM shall fail the step as a protocol error. | Active | DVT-HAZ-009 | Positive controls fail: truncated body without closing delimiter; body boundary different from the header's; part with `application/octet-stream`; part with garbage bytes; boundary string inside a part's data handled without splitting the part. |
| DVT-SRS-121 | The `wado` step shall check that the set of SOP Instance UIDs received equals the expected set exactly: no missing, no extra and no duplicate instance. | Active | DVT-HAZ-001, DVT-HAZ-009 | Positive controls fail: one instance missing; one extra instance; one instance twice. |
| DVT-SRS-122 | When the `wado` step names a transfer syntax, every received part's File Meta Transfer Syntax UID shall equal it, and a `transfer-syntax` parameter in the part's Content-Type, when present, shall agree. Otherwise any transfer syntax is accepted and recorded. | Active | DVT-HAZ-001 | A server that answers Explicit VR Little Endian to a JPEG Lossless request fails; a part whose header and File Meta disagree fails. |
| DVT-SRS-123 | With `compare_pixels: true`, the `wado` step shall compare each retrieved instance's pixel data and SOP Instance UID with the original file in the same way as DVT-SRS-025, and fail on any difference. A plan that combines `compare_pixels` with a lossy transfer syntax shall be rejected. | Active | DVT-HAZ-001 | A server that alters one pixel fails; an unaltered retrieve passes; a plan requesting JPEG Baseline with `compare_pixels` is rejected before any step runs. |
| DVT-SRS-124 | With `metadata: true`, the `wado` step shall retrieve the metadata resource (`application/dicom+json`) and check that every expected instance appears once with matching Study, Series and SOP Instance UIDs and that each attribute named in `compare_attributes` equals the original file's value. | Active | DVT-HAZ-001 | Positive controls fail: an instance missing from the metadata; a changed Patient ID when `compare_attributes` lists PatientID. |
| DVT-SRS-125 | The `wado` step shall pass only on HTTP 200. 206 (partial content), every other 2xx, 4xx and 5xx shall fail the step with the status recorded; 3xx follows DVT-SRS-151. | Active | DVT-HAZ-001 | A 206 response with all parts present fails; 404 and 503 fail. |
| DVT-SRS-126 | The `wado` step shall record per received part: Content-Type, transfer syntax, SOP Instance UID, size and SHA-256. | Active | DVT-HAZ-004 | Evidence of a retrieve of N instances holds N part records whose SHA-256 values match the parts' bytes. |
| DVT-SRS-127 | With `validate: true`, the `wado` step shall run the object validation engines (DVT-SRS-002, -003) on every received instance and fail on any error finding or engine error. | Active | DVT-HAZ-001 | A server that returns an instance with a missing Type 1 attribute fails with the dciodvfy finding; a valid instance passes. |

## 10. STOW-RS

| ID | Requirement | Status | Tool hazard | Acceptance criteria |
|---|---|---|---|---|
| DVT-SRS-128 | LibreDVT shall provide a `stow` step that sends selected files to the target's studies resource (or to one study when the step names it) as `multipart/related; type="application/dicom"` requests, each file as one part in its own transfer syntax, unchanged, with at most `batch_size` parts per request. | Active | DVT-HAZ-001 | The test server receives byte-identical files (SHA-256 per part) in the expected number of requests. |
| DVT-SRS-129 | LibreDVT shall parse the STOW-RS response as `application/dicom+json` holding Referenced SOP Sequence (0008,1199) and Failed SOP Sequence (0008,1198), with Failure Reason (0008,1197) and Warning Reason (0008,1196). A body that is missing, of another type, or not a DICOM JSON object shall fail the step as a protocol error. | Active | DVT-HAZ-009 | Positive controls fail: empty body with 200; XML body; JSON array instead of object; malformed JSON. |
| DVT-SRS-130 | The `stow` step shall pass only when every response is HTTP 200, every sent SOP Instance UID appears exactly once in a Referenced SOP Sequence with the sent SOP Class UID, no Failed SOP Sequence item is present, and no Warning Reason is given. With `allow_warnings: true`, a 202 response whose problems are all warnings passes with warnings. | Active | DVT-HAZ-001, DVT-HAZ-009 | Positive controls fail: 202 with a failed instance; 409; a sent UID missing from the Referenced SOP Sequence; an unexpected extra UID; a mismatched SOP Class UID; a 200 response that also lists a failure; a warning without `allow_warnings`. |
| DVT-SRS-131 | The `stow` step shall record each failed instance's Failure Reason and each Warning Reason as a finding with the SOP Instance UID. | Active | DVT-HAZ-004 | A test server that fails one instance with 0xA700 gives an error finding naming that UID and reason. |
| DVT-SRS-132 | The `stow` step shall record the Retrieve URL (0008,1190) the target returns, and shall never follow it to an origin other than the target's own. With `verify_retrieve: true`, it shall retrieve each stored instance from the target's own base URL by UID and compare it as in DVT-SRS-123. | Active | DVT-HAZ-001, DVT-HAZ-007 | A Retrieve URL pointing to another origin is recorded and not requested; `verify_retrieve` against a server that changes pixel data fails. |
| DVT-SRS-133 | Before sending, the `stow` step shall check that every selected file is a readable DICOM Part 10 file, and shall fail without sending anything when one is not. | Active | DVT-HAZ-001 | A plan with one non-DICOM file fails and the test server receives no request. |
| DVT-SRS-134 | LibreDVT shall refuse to run `qido`, `wado` and `stow` steps against a target whose class is not `lab`, and report the step as failed with the reason, without sending any request. | Active | DVT-HAZ-002, DVT-HAZ-008, DVT-HAZ-010 | Against `production` and unclassified DICOMweb targets, each data step fails and the test server receives nothing. |
| DVT-SRS-135 | Instances and studies sent by `stow` steps shall be available to later steps with `from_store: true`, in the same way as those sent by `store` steps. | Active | DVT-HAZ-001 | A plan stow → qido `from_store` → wado `from_store` checks exactly the stowed instances. |
| DVT-SRS-136 | The `stow` step shall record per sent file its SOP Instance UID, SOP Class UID, transfer syntax, size, SHA-256 and the target's per-instance result. | Active | DVT-HAZ-004 | Evidence of a stow of N files holds N records with the target's result for each. |

## 11. Targets, plans and evidence

| ID | Requirement | Status | Tool hazard | Acceptance criteria |
|---|---|---|---|---|
| DVT-SRS-140 | Target records (web registry and target files) shall be parsed strictly by the schema in `plan-schema.md`. Unknown fields, wrong types, fields of the other protocol (for example `called_aet` on a `dicomweb` target or `base_url` on a `dimse` target) and missing required fields shall be rejected when the target is saved or loaded, before any step runs. | Active | DVT-HAZ-004 | Schema tests reject each case with an error naming the field; a valid target of each protocol loads. |
| DVT-SRS-141 | Secrets (bearer token, client private key) shall only be configured as references to files. A target or plan that holds an inline secret field (for example `token`, `password`, `private_key`) shall be rejected as unknown. A referenced secret file that is missing, empty or not a regular file shall fail the step before any connection is made. | Active | DVT-HAZ-007 | Schema tests reject inline secret fields; missing, empty and directory references fail before the test server sees a connection. |
| DVT-SRS-142 | Plans shall accept the step types `associate`, `tls_reject`, `qido`, `wado` and `stow` with the fields in `plan-schema.md`. Before any step runs, each step's type shall be checked against its target's protocol (a DICOMweb step on a `dimse` target, or a DIMSE step on a `dicomweb` target, rejects the plan). v0.1 plans shall parse and run unchanged. | Active | DVT-HAZ-004 | `qido` on a `dimse` target and `echo` on a `dicomweb` target reject the plan; the bundled v0.1 LibreDCM plan and the v0.1 plan tests pass unchanged. |
| DVT-SRS-143 | Each step result shall carry protocol-neutral evidence as defined in `evidence-model.md`: one exchange per network interaction (protocol, operation, endpoint, timing, request and response summaries, TLS details, association details) and the assertions checked, each with expected value, actual value and outcome. | Active | DVT-HAZ-004, DVT-HAZ-005 | The JSON result of each new step type validates against the evidence model; every assertion has expected, actual and outcome. |
| DVT-SRS-144 | The verdict of a v0.2 step shall be decided in the result package from its assertions and errors only: the step passes only when it has at least one assertion, all assertions passed, and no protocol or engine error occurred. A step with no assertions shall fail as "nothing was checked". | Active | DVT-HAZ-001 | Unit tests: zero assertions fail; one failed assertion fails; an engine error with all assertions passed fails. Mutants that flip each rule are caught. |
| DVT-SRS-145 | JUnit, HTML and JSON reports shall show the target's protocol, endpoint and class and the new evidence (TLS details, association results, HTTP statuses, assertions, per-instance STOW results). Runs stored by v0.1 shall still load and render. | Active | DVT-HAZ-004, DVT-HAZ-005 | Report tests find each field for each new step type; a stored v0.1 run file loads and renders unchanged. |
| DVT-SRS-146 | The web UI shall manage `dimse` and `dicomweb` targets with their TLS settings and secret references, and `libredvt run` shall accept a target file (`-targets FILE`) with the same schema. Both shall run plans through the same code. | Active | DVT-HAZ-004 | The same plan and target give the same step verdicts and assertions from the CLI and the web UI. |
| DVT-SRS-157 | The HTML report shall print (for example to PDF from a browser) with every evidence section expanded, light colours whatever the reader's colour scheme, no scroll boxes that clip content, and step names broken only between words. | Active | DVT-HAZ-004 | A report test finds the print rules in the report's stylesheet; a report printed to PDF with Chromium shows the HTTP exchange sections and no clipped text (`manual-verification.md`). |

## 12. Fail-safe behaviour, secrets and integration

| ID | Requirement | Status | Tool hazard | Acceptance criteria |
|---|---|---|---|---|
| DVT-SRS-150 | Any failure to get trustworthy evidence in a v0.2 step (worker missing, crash, hang past the timeout, output that is not JSON or does not match the worker's result schema, connection reset, TLS error, truncated response) shall fail the step as an engine or protocol error, never pass or skip it. | Active | DVT-HAZ-001, DVT-HAZ-009 | Failure injection for each new worker command: missing worker, crash, hang, garbage output and schema-invalid JSON each fail the step. |
| DVT-SRS-151 | LibreDVT shall not follow HTTP redirects. A 3xx response shall fail the step with the `Location` value recorded, and no request shall be sent to the redirect target. | Active | DVT-HAZ-007, DVT-HAZ-008 | A test server answering 302 to another host fails the step; the other host receives nothing. A 307 on a `stow` is not re-sent. |
| DVT-SRS-152 | Bearer tokens, private keys and passwords shall never appear in reports (HTML, JUnit, JSON), the results store, logs or error messages. LibreDVT shall replace authentication header values with `[REDACTED]`, and before writing any artefact shall scan it for every configured secret value; when one is found it shall fail the run and write the artefact without the secret. | Active | DVT-HAZ-007 | A run with configured synthetic secrets leaves no secret in any artefact (grep of every file in the data directory and report outputs). A secret injected into an error message by a test peer is caught, redacted and fails the run. |
| DVT-SRS-153 | Secrets shall reach the Python worker through a file path or standard input, never as command-line arguments or environment variables. | Active | DVT-HAZ-007 | A test captures the worker's argument vector and environment for every new command and finds no configured secret value. |
| DVT-SRS-154 | LibreDVT shall cap HTTP response bodies, multipart part counts, part sizes and DICOM JSON documents at configurable limits (defaults in `plan-schema.md`), and shall fail the step when a limit is reached, never pass on a partial read. | Active | DVT-HAZ-009, DVT-HAZ-010 | A test server streaming past each limit fails the step with the limit named. |
| DVT-SRS-155 | LibreDVT shall send at most one association or HTTP request at a time to a target, and steps shall run one after another, so a plan cannot load a third-party system with parallel traffic. | Active | DVT-HAZ-010 | A test peer records the maximum number of concurrent connections during a full plan run: 1. |
| DVT-SRS-156 | LibreDVT shall ship a LibreDCM smoke plan covering association negotiation (Verification and CT Image Storage accepted with Explicit VR Little Endian, an unsupported abstract syntax rejected) and TLS (echo and store over TLS pass; plaintext and TLS 1.1 refused), plus example plans for generic targets: a DICOM TLS audit and a DICOMweb audit. | Active | | The smoke plan passes against a LibreDCM container with TLS configured; the DICOM TLS audit plan passes against the same LibreDCM and agrees with DCMTK `+tls`; the DICOMweb audit plan passes against the digest-pinned independent DICOMweb server used in the tool validation. |
