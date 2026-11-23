# Tool validation notes

| | |
|---|---|
| Procedure | QMS-SOP-011 |
| Status | **Executed 2026-11-10 by Hermes/Claude on branch `qms/retro-dcm`; awaiting review and approval** |

One section per tool: intended use, risk (if it failed silently), version,
activities, results, conclusion, re-validation triggers. Results are real
command output from 2026-11-10 unless stated.

## Summary

| Tool | Version | Risk | Activities | Result |
|---|---|---|---|---|
| Go toolchain | 1.26.x (≥ 1.26.10; image `golang:1.26-bookworm`, release build pinned by digest) | Low | Vendor test suite; our full test suite on every run | Fit |
| Docker / Docker Compose | Engine 29.9.2 on the dev machine; Docker Desktop on Windows | Low | Release verification checks the built image (health, C-ECHO, version) | Fit |
| GitHub Actions (`qms.yml`) | `ubuntu-latest`, `actions/checkout@v4`, `actions/setup-go@v5` | Medium | Every step run locally in the same Go image with the same commands; results match | Fit; new steps not yet run on GitHub (branch not pushed) |
| gosec | v2.30.0 | Medium | Positive control | Fit |
| govulncheck | v1.9.0 | Medium | Positive control | Fit |
| Trivy | 0.76.2 (image pinned by digest) | Medium | Positive control (finds the known libssl3 CVE); ignore-file expiry check | Fit |
| cyclonedx-gomod | v1.12.0 | Medium | Completeness check against the compiled module list | Fit, with note |
| `scripts/qms/trace.py` | repo version | High | Scripted tests (`test_qms_tools.py`), mutation check | Fit |
| `scripts/qms/needs.py` | repo version | High | Scripted tests | Fit |
| `scripts/qms/mdlinks.py` | repo version | Medium | Scripted tests | Fit |
| `scripts/qms/soup.py` | repo version | Medium | Scripted tests (regeneration identical; new dependency flagged); one defect fixed | Fit |
| `scripts/qms/part11_check.py` | repo version | Medium | Live run (10/10) and negative test | Fit |
| `scripts/qms/release-verify.sh` | repo version | High | Used for 0.2.0-beta.1; review of the script | Fit with findings |
| DCMTK | 3.6.7 (image `rws-e2e-dcmtk`) | Medium | Reference implementation; behaviour cross-checked by Go end-to-end tests | Fit |
| Orthanc + DICOMweb plugin | Orthanc 1.13.1, DICOMweb plugin 1.24 (`orthancteam/orthanc:26.11.1@sha256:25738bcd515926ac8d33cacf09729a410a32c0a65d6cb8e61bd86fce81a4cd30`) | Medium | Independent DICOMweb reference for the LibreDVT cross-check; stored files checked byte for byte through Orthanc's own REST API | Fit as a cross-check peer (test container only, not distributed) |
| LibreDVT | 0.1.0-dev (branch `feat/libredvt-v0.1`; engines pinned in `modules/dvt/worker/requirements.txt`) | High | Scripted positive controls with the real engines (CI job `libredvt`), failure injection, mutation check, end-to-end comparison with DCMTK | Fit for v0.1 use, pending review |
| Libre WT system test (`scripts/e2e/wt-system-test.sh`, `.py`, `wt_make_data.py`, `wt_compare.py`) | repo version (card WT-0.2/9) | High | Positive control of the comparison, failure injection (no console; already-used console), tightened vacuous checks, independence from the product code, failures on real discrepancies during development | Fit for Libre WT 0.2 verification, pending review |
| `scripts/e2e/wt-screenshots.mjs` | repo version (card WT-0.2/9) | Low (documentation only) | Negative control of its page-width check | Fit |
| AI coding tools | Claude (Hermes, Claude Code) | Low | Output controlled by review, CI and tests (SOP-011 §3) | Fit |

## Go toolchain

- **Intended use:** compile LibreDCM; run tests (`go test`), static checks
  (`go vet`), fuzzing.
- **Risk: low.** A compiler defect would most likely show up as test
  failures; the Go project runs an extensive test suite and publishes
  security releases. Version pinned (Dockerfile digest; CI `1.26.x`,
  ≥ 1.26.10 required by GO-2026-6809).
- **Activities/results:** full suite green with `-race` (all packages,
  2026-11-10) and with `-tags official`.
- **Re-validate:** on a Go minor version change (1.27).

## Docker

- **Intended use:** build the product image; run test dependencies.
- **Risk: low.** A faulty build fails the release verification checks
  (container healthy, `/version` reports the version, real C-ECHO and DCMTK
  conformance against the built image), done for every release.
- **Re-validate:** not required beyond the per-release verification.

## GitHub Actions (`.github/workflows/qms.yml`)

- **Intended use:** run the quality gates on every PR and on `master`;
  produce CI logs as verification records; the `verify` check gates merges.
- **Risk: medium.** A misconfigured step could pass without checking.
  Mitigation: each step uses an exit-status-checked command (no
  `continue-on-error` since 2026-11-10); results are reproduced locally.
- **Activities/results (2026-11-10):** every step of `verify` was run locally
  in `golang:1.26-bookworm` with the same commands: vet ✓, race tests ✓,
  fork and client-copy tests ✓, gosec (blocking) rc 0 ✓, gosec third_party
  report-only rc 0 with reports written ✓, govulncheck ✓, SBOM ✓, trace check
  rc 0 ✓, `needs.py` rc 0 ✓, `mdlinks.py` rc 0 ✓, QMS script tests ✓. Earlier `verify`
  runs on GitHub: 12 of 12 successful on 2026-11-09 (PRs #3–#9 and the
  `master` pushes, e.g. run 42723579767).
- **Open:** confirm the first GitHub run of the new steps after the branch is
  pushed; record the run ID here.
- **Re-validate:** on any change to the workflow (the PR shows it).

## gosec v2.30.0

- **Intended use:** SAST in CI (blocking for Libre RWS code).
- **Risk: medium** (could miss a weakness; code review and fuzzing also apply).
- **Positive control:** a throwaway module containing
  `&tls.Config{InsecureSkipVerify: true}` → `G402 (CWE-295) ... Issues: 1` ✓.
  On the real code it found 15 issues incl. one real defect (ANOM-001).
- **Re-validate:** on a version change.

## govulncheck v1.9.0

- **Intended use:** SCA of reachable known vulnerabilities in CI and at release.
- **Risk: medium** (quarterly SBOM review is a second control).
- **Positive control:** a throwaway module requiring `golang.org/x/text v0.3.5`
  and calling `language.Parse` → `Vulnerability #1: GO-2021-0113 ... Your
  code is affected by 1 vulnerability from 1 module` ✓. On 2026-11-09 it
  found the reachable GO-2026-6809 in the Go standard library.
- **Re-validate:** on a version change.

## Trivy 0.76.2

- **Intended use:** scan OS packages of the LibreDCM image in CI (blocking)
  and record a full report.
- **Risk: medium** (could miss an OS vulnerability; the base image is
  minimal distroless and rebuilt deliberately).
- **Positive control (2026-11-10):** scanning the release image without an
  ignore file reported `CVE-2026-84782 libssl3 HIGH fixed` and exited 1 ✓;
  with an expired ignore entry it exited 1 again ✓; with the valid entry 0 ✓.
- **Re-validate:** on a version change.

## cyclonedx-gomod v1.12.0

- **Intended use:** generate the CycloneDX SBOM (§524B, release record).
- **Risk: medium.** A missing component would leave the SBOM incomplete.
- **Activity:** compared the modules actually compiled into LibreDCM and its
  tests (`go list -deps -test -f '{{.Module}}' ./...`, 47 modules) with
  the committed SBOM (49 components). **Result:** no compiled module is
  missing ✓. **Note:** the two locally replaced modules (gonetdicom fork,
  LibreKey client copy) carry the main repository's pseudo-version in the
  SBOM, not the upstream base version; readers must use `RWS_FORK.md` /
  `LIBREKEY_COPY.md` for their provenance. The committed SBOM must be
  regenerated whenever `go.mod` changes (CI regenerates it on every run).
- **Lesson recorded:** `go list -m all` must not be used for this check: it
  lists the whole module graph (minio-go declares golangci-lint as a tool
  dependency, 279 modules) and rewrites `go.sum`.
- **Re-validate:** on a version change.

## QMS scripts (`trace.py`, `needs.py`, `mdlinks.py`, `soup.py`)

- **Intended use:** produce the trace matrix and the SOUP list (records) and
  gate merges on traceability, user-need coverage and document links.
- **Risk: high** for `trace.py` and `needs.py` (a silent pass would hide
  unverified requirements); medium for the others.
- **Activities:** scripted tests `scripts/qms/test_qms_tools.py` (13 tests:
  untested active requirement fails; unknown and retired IDs fail; a tag not
  directly above a test is ignored; fuzz targets and manual evidence count;
  user-need shorthand, unserved and retired citations; missing files,
  missing anchors, code blocks ignored; SOUP regeneration identical and new
  dependencies flagged). Run in CI.
- **Results:** `Ran 13 tests ... OK`. Mutation check: making `trace.py` never
  exit 1 → 3 tests fail ✓.
- **Defect found and fixed:** `soup.py` wrote a stale runtime-image path
  (`deploy/Dockerfile`); now `deploy/docker/Dockerfile.dicom`. The LibreKey
  risk text in `soup.md` still said "C-STORE refused" (behaviour changed by
  issue #5); corrected.
- **Re-validate:** automatically on every change (tests in CI).

## `scripts/qms/part11_check.py`

- **Intended use:** re-check the GitHub settings that VAL-0001 relies on.
- **Results:** live run 10/10 PASS; against a branch without protection it
  reports FAIL and exits 1 ✓.

## `scripts/qms/release-verify.sh`

- **Intended use:** verify the exact release build (vet, race, official
  tags, PostgreSQL, S3, govulncheck, SBOM, source and official images, DCMTK
  conformance, unlicensed official build); logs become release records.
- **Risk: high** (its logs are the release verification evidence).
- **Activities:** used for 0.2.0-beta.1 (logs in
  `docs/releases/v0.2.0-beta.1-verification/`); script reviewed 2026-11-10.
- **Findings:** test-helper images are not pinned (`chrislusf/seaweedfs:latest`,
  `curlimages/curl:latest`), so a later run may use a different version;
  the script records the Go version and built image IDs but not the helper
  image digests. **Action:** pin helper images by digest and log them
  (small change, next release).

## DCMTK 3.6.7

- **Intended use:** independent DICOM peer for system/conformance tests
  (store of compressed transfer syntaxes, C-FIND, C-MOVE, called-AE check).
- **Risk: medium.** It is the de-facto reference implementation; its results
  are cross-checked by our own Go end-to-end tests over real associations,
  and pixel data is compared byte for byte after retrieval.
- **Re-validate:** on a version change of the test image.

## Orthanc + DICOMweb plugin (test container)

(added 2026-11-22 by Hermes/Claude for card DVT-0.2/4, DRAFT for approval)

- **Intended use:** independent DICOMweb server (QIDO-RS, WADO-RS,
  STOW-RS over HTTPS, mutual TLS and loopback HTTP) for the LibreDVT v0.2
  cross-check. Not part of any product; never distributed. Orthanc is
  GPL-3.0 and the plugin AGPL-3.0; both run unmodified in their published
  image.
- **Configuration item:** `orthancteam/orthanc:26.11.1@sha256:25738bcd515926ac8d33cacf09729a410a32c0a65d6cb8e61bd86fce81a4cd30`,
  reporting Orthanc 1.13.1 and DICOMweb plugin 1.24 (recorded in
  `docs/dhf/libredvt/evidence/v0.2-dicomweb/03-cross-check.log`).
- **Risk: medium.** A defect in Orthanc could make a correct LibreDVT check
  look wrong (seen as an unexpected result and investigated) or hide a
  LibreDVT defect only if both share it. They share no code (Orthanc is C++
  built on DCMTK; LibreDVT's client is Python with its own multipart parser). The stored
  files are compared byte for byte with the fixtures through Orthanc's own
  REST API, outside DICOMweb, so the STOW result does not rest on LibreDVT
  alone.
- **Re-validate:** on a change of the image digest.

## LibreDVT 0.1 (DICOM Validation & Test)

(added 2026-11-10 by Hermes/Claude; design file `docs/dhf/libredvt/`)

- **Intended use:** validate DICOM objects and test DICOM services of
  LibreDCM (later other archives); its plan runs are verification records
  (`docs/dhf/libredvt/intended-use.md`).
- **Risk: high.** A silent false pass could release a defective LibreDCM
  build (DVT-HAZ-001). Controls: DVT-SRS-007/-009/-042 (engine errors and
  steps not run always fail), DVT-SRS-080 (engines independent of LibreDCM's
  godicom/gonetdicom), strict plans (DVT-SRS-040).
- **Configuration:** dicom3tools 20260927 (1.00.snapshot.20260927113651),
  dicom-validator 0.9.0 with DICOM 2026d built in, pydicom 3.0.2,
  pynetdicom 3.0.4, lxml 6.1.3, pyparsing 3.3.3; base images by digest.
- **Activities and results (2026-11-10, real output):**
  1. *Object positive controls* (`internal/engine/integration_test.go`, real
     engines): missing Type 1 attribute, invalid enumerated value and an
     over-long LO value each fail with the expected dciodvfy and/or
     dicom-validator finding; conflicting Patient IDs across two files fail
     with a dcentvfy finding; a text file fails as "not a DICOM file" and is
     not passed to dciodvfy; the known-good CT file passes. All passed.
  2. *Failure injection* (`internal/engine/engine_test.go`, fake engines):
     missing binary, crash (SIGSEGV), hang past the timeout, garbage output,
     worker crash/garbage/hang, worker dropping results: every case gives an
     engine error and a failed verdict. All passed.
  3. *Service positive controls* (`internal/runner/integration_test.go`, real
     pynetdicom against `worker/testdata/fake_scp.py`): correct archive →
     every step passes; C-STORE refused (0xA700) → store fails; one instance
     per study withheld on C-MOVE → move fails ("4 expected instance(s) not
     received"); one pixel byte flipped → move fails ("pixel data differs",
     6 of 6); archive accepting any called AE → reject step fails; no
     listener → echo fails within the timeout; production-class target →
     store and move refused. All passed.
  4. *Mutation check* (code deliberately broken, tests re-run): ignoring
     engine errors in the verdict, removing the production guard, lenient
     YAML parsing, ignoring unknown dciodvfy output, disabling the login
     rate limit, ignoring missing C-MOVE instances: each made at least one
     test fail (6 of 6 mutants caught).
  5. *End-to-end:* the bundled LibreDCM plan passed against a LibreDCM
     container, and `scripts/conformance.sh` (DCMTK 3.6.7) passed on the same
     fixtures and LibreDCM; the image validated files with `--network none`.
- **Findings during validation:** the worker's pixel comparison crashed on
  its first real run (`'bytes' object has no attribute 'value'`); LibreDVT
  reported it as an engine error and failed the step, as designed, and the
  bug was fixed. An Explicit VR Little Endian instance came back as Implicit
  VR Little Endian: LibreDCM proposes [Explicit, Implicit] for native data
  and LibreDVT's pynetdicom Storage SCP, as acceptor, chose Implicit. DICOM
  leaves that choice to the acceptor and the pixel data was identical, so it
  is not a LibreDCM defect; LibreDVT now notes native re-encodings and fails
  only when compressed data is transcoded.
- **Conclusion:** fit for its intended use in v0.1, pending human review.
- **Re-validate:** any change under `modules/dvt/` or `Dockerfile.dvt`
  (CI job `libredvt` re-runs activities 1–3 automatically), any engine or
  base-image version change, or a new step type.
- **Change 2026-11-18 (issue #30, worker 0.1.1):** the worker now sends
  LibreDVT's own Implementation Class UID and Version Name
  (`1.3.6.1.4.1.67686.1.3.1.<major>.<minor>`, `LIBREDVT_<major>_<minor>`,
  passed from Go as `--impl-uid`/`--impl-version`) instead of pynetdicom's,
  and writes them in the File Meta of files its Storage SCP receives
  (DVT-SRS-082). Verdict logic, engines and versions are unchanged.
  Re-validation (real output, `docker build -f deploy/docker/Dockerfile.dvt
  --target test`): 57 tests passed, 0 failed, including activities 1–3
  unchanged and the new `TestWorkerSendsLibreDVTIdentification` (the
  A-ASSOCIATE-RQ read off the wire and a received file's (0002,0012)/(0002,0013)
  carry LibreDVT's values). Mutation check: see the PR. Fit for use, pending
  review.
- **Change 2026-11-19 (issue #34, card DVT-0.2/2, DRAFT for approval):**
  v0.2 foundation: `dimse`/`dicomweb` targets with TLS and secret file
  references, strict target and plan schema, production guard extended to
  `stow`/`qido`/`wado`, protocol-neutral evidence and verdict rule,
  redaction and the worker argv/environment guard. The new step types fail
  safe (nothing sent) until cards DVT-0.2/3 and /4 add their engines.
  Re-validation (real output, evidence in
  `docs/dhf/libredvt/evidence/v0.2-foundation/`): real-engine test stage
  exit 0 with the v0.1 positive controls unchanged; mutation check 33 of 33
  mutants killed; the bundled LibreDCM plan gives identical step results
  (5 of 5 passed, same messages) with the v0.1 image, this build with an
  ad-hoc target, and this build with a v0.2 target file. Fit for v0.1 use
  unchanged; the v0.2 functions are not yet released for use (validated
  in card DVT-0.2/6).
- **Change 2026-11-19 (issue #34, card DVT-0.2/3, DRAFT for approval):**
  association negotiation (`associate`) and TLS refusal (`tls_reject`)
  steps, DICOM TLS for every DIMSE step and for the Storage SCP during a
  move. Re-validation (real output, evidence in
  `docs/dhf/libredvt/evidence/v0.2-assoc-tls/`):
  - Positive controls with the real engines against a Go test peer and a
    synthetic PKI that share no code with the worker (real-engine test
    stage exit 0, 106 tests passed, 0 failed): wrong host, wrong
    `server_name`, CN-only names (IP and DNS), unknown CA, expired, not yet
    valid, incomplete chain, self-signed leaf, system trust store without
    the test CA, TLS 1.0- and 1.1-only servers, a TLS 1.2 server with a
    1.3 minimum, client certificate missing/unknown/mismatched; association
    reject, abort, close, garbage, silence, missing or extra context, other
    transfer syntax, roles, max PDU, Implementation Class UID and Version
    Name, release abort. Each fails the step, and after a failed handshake
    no A-ASSOCIATE-RQ reaches the peer. Correct TLS 1.2 and 1.3 peers pass.
  - Mutation check: 23 of 23 mutants killed (14 Go, 9 worker), including
    host-name check off, Common Name fallback, TLS 1.1 accepted (worker and
    Go verdict), rejected context ignored, expired certificate accepted and
    clear-text fallback for v0.1 steps. A first run found that the
    Common-Name control used an IP address, which OpenSSL never matches
    against the CN, so the CN-fallback mutant survived; a DNS-name CN
    control was added and kills it.
  - Independent cross-check: 13 of 13 LibreDVT runs as expected against
    DCMTK 3.6.7 `storescp` (plain, `--refuse`, `+tls`, `+tls -rc`, wrong
    host certificate, retired AES profile) and LibreDCM in TLS mode
    (association, echo, refusals, C-STORE and C-FIND over TLS), with
    DCMTK `echoscu` against the same servers and the DCMTK servers' own
    logs confirming each refusal (unsupported protocol, wrong version
    number, peer did not return a certificate, certificate verify failed).
    LibreDVT read DCMTK's Implementation Class UID/Version Name and max PDU
    as documented by DCMTK, and LibreDCM's as given by the UID registry.
    No private-key material in any of the 77 artefacts.
  - Deviation: the first cross-check expected LibreDVT's echo to the AES
    profile server to pass; it failed because that profile offers only
    AES128-SHA and LibreDVT's client offers forward-secret suites only.
    The DCMTK log shows "no shared cipher". The expectation was corrected;
    the behaviour is fail-closed and documented.
  - Not done here: DICOMweb (card DVT-0.2/4); C-MOVE over TLS against
    LibreDCM (its move destinations have no TLS; the Go peer covers it).
    Fit for v0.1 use
    unchanged; the v0.2 functions are not yet released for use (validated
    in card DVT-0.2/6).
- **Change 2026-11-22 (issue #34, card DVT-0.2/4, DRAFT for approval):**
  DICOMweb `qido`, `wado` and `stow` steps and `tls_reject` on DICOMweb
  targets. Client in the worker on Python's standard library; no new
  dependency. Re-validation (real output, evidence in
  `docs/dhf/libredvt/evidence/v0.2-dicomweb/`):
  - Positive controls with the real engines against a Go DICOMweb test
    server (`net/http`, `mime/multipart`; no shared code with the worker);
    real-engine test stage exit 0, 110 top-level tests passed, 0 failed.
    Controls: wrong boundary, truncated multipart, wrong content
    type, non-DICOM part, missing/extra/duplicate instances, wrong UIDs,
    wrong transfer syntax (body and part header), changed pixel data,
    failed STOW instances reported as success (200 with a Failed SOP
    Sequence), missing or extra references, wrong SOP Class, unusable
    STOW responses, 3xx redirects (never followed), oversized bodies,
    JSON documents, parts and part counts, endless paging, short bodies,
    and a token echoed into an error page. Each fails the step. Boundary
    text inside part data must not split a part (passes).
  - Mutation check: 41 of 41 mutants killed (31 Go, 10 worker),
    including failed STOW instances ignored, truncated multipart accepted,
    boundary taken from the body, boundary text inside data splitting a
    part, unredacted `Authorization` accepted, paging stopped after one
    page, changed STOW bytes and any HTTP reply counted as a refusal. The
    first run killed 39 of 41: the boundary-in-data control did not put
    real boundary text in the data, and one mutant was equivalent (the
    fixtures already end in 0x00). Both controls were corrected.
  - Deviation: the first real-engine run failed once because the test
    server counted a sequential client's connections as overlapping (a
    race between the client's reconnect and the server's close callback).
    The server now counts requests before any response byte is written;
    its own test shows sequential requests count as 1 and parallel ones as
    more. A false alarm in the test server, not a LibreDVT defect.
  - Independent cross-check against Orthanc 1.13.1 with the DICOMweb
    plugin 1.24 (`orthancteam/orthanc:26.11.1`, digest-pinned, test
    container only): 5 of 5 runs as expected (HTTPS audit with refusal
    probes, STOW of 6 files with retrieve check, QIDO at three levels with
    paging, WADO with pixel comparison, explicit syntax and metadata;
    mutual TLS; a token over loopback HTTP; negatives: no client
    authentication, wrong host name). Orthanc's REST API served each
    stored instance byte-identical to its fixture (6 of 6). No token or
    private-key line in any of the 30 artefacts.
  - Bearer-token redaction: the real worker against a server echoing the
    `Authorization` header into its error page: token caught, redacted,
    run failed; clean runs leave no token form (plain, base64,
    base64url) in the JSON, HTML or JUnit artefacts.
  - Fit for v0.1 use unchanged; the v0.2 functions are not yet released
    for use (validated in card DVT-0.2/6).

## Libre WT system test (card WT-0.2/9, 2026-11-17)

- **Intended use:** verify Libre WT end to end against real LibreDCM
  containers (source build and unlicensed official build) and DCMTK, through
  the web pages as Viewer, Operator and Admin; its log becomes a Libre WT
  release verification record. Files: `scripts/e2e/wt-system-test.sh`
  (runner), `wt-system-test.py` (checks), `wt_make_data.py` (synthetic
  data), `wt_compare.py` (sent against received), stack
  `deploy/compose/docker-compose.wt-system.yml`.
- **Risk: high** (SOP-011: a test tool whose output is a verification
  record). A check that passes wrongly would hide a defect in a risk
  control.
- **Independence:** written in Python (standard library on the host;
  pydicom and DCMTK in the `rws-e2e-dcmtk` image); it imports no Libre WT
  or LibreDCM code and checks DICOM effects with DCMTK, not with the
  product's libraries.
- **Activities and results (2026-11-17):**
  - Positive control of the comparison: `wt_compare.py --tamper` flips one
    pixel byte of one received file; it must report `DIFFERENT` and exit 1.
    Runs inside every system test as check CTL-1 (passed: 3 identical, 1
    different, exit 1).
  - Failure injection 1, no console: the test against an address where
    nothing listens fails ENV-1 and exits 1 after 90 s.
  - Failure injection 2, wrong state: the test against a console that was
    already set up (accounts exist, data changed) exits 1. The first try
    left 19 checks passing; each was reviewed. Negative checks that passed
    only because a session was missing were tightened to also require a
    logged-in page, the exact refusal code (403, 409) or the expected
    content; afterwards 13 passed, each for a real reason (the console was
    in fact set up, the stores and the logout worked).
  - Real discrepancies found during development made it fail as it
    should: a hand-made destinations request removed the existing entry
    (SYS-37 failed; the test now posts the form as the page builds it), and
    the received data sets differed because DCMTK drops Data Set Trailing
    Padding (SYS-60 failed; the generator now writes files without it).
  - The runner exits non-zero when any check fails, and scans every saved
    file for both admin tokens.
- **Limits:** one fixed scenario with synthetic data; HTTPS and the
  installer scripts are not covered; it runs locally, not in CI (D59).
- **Re-validate:** when the test changes, or when the pages it parses
  change their form field names.

## `scripts/e2e/wt-screenshots.mjs`

- **Intended use:** screenshots of every Libre WT page in light and dark
  for the user guide, and a check that no page is wider than its 1280 px
  window. Documentation only; not a verification record.
- **Risk: low.**
- **Negative control (2026-11-17):** with the style sheet before the fix
  of card WT-0.2/9 it reported 6 pages wider than the window
  (configuration 1297 px, LibreDCM audit 1532 px, logs 4164 px, each in
  light and dark) and exited 1; with the fix, 0.

## AI coding tools

See QMS-SOP-011 §3: low risk as a QMS tool because all output is reviewed
and verified by the controls that apply to every change. In this session the
controls caught AI-introduced problems before commit (e.g. fuzz oracles that
were too strict, a too-broad first version of the Part 10 guard that would
have refused over-long UIDs from real modalities).
