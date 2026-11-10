# LibreDVT Software Requirements Specification (SRS)

| | |
|---|---|
| Document | DHF-DVT-SRS |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude from issue #12 and Patrick Mulcahy's decisions of 2026-11-10) |
| Software item | LibreDVT (DICOM Validation & Test), a module of the libre-rws repository |
| Regulatory role | Not a medical device and not part of the LibreRWS PACS. A software tool used in the quality system to produce verification records (ISO 13485 §4.1.6, QMS-SOP-011). Validated as a **high-risk tool** (see `risk-management.md`) |
| Release | v0.1 scope |

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
