# Software Development Plan (SDP)

| | |
|---|---|
| Document | QMS-SDP-001 |
| Status | **DRAFT for approval** (completed 2026-11-10; all IEC 62304 §5.1 planning topics covered). Becomes effective when approved at the QMS baseline |
| Applies to | All Libre RWS software items: LibreDCM, Libre WT, LibreKey client copy, future modules |
| Standards | IEC 62304 Ed 1.1 (FDA rec. 13-79), ISO 14971:2019 (5-125), IEC 81001-5-1 (13-122), AAMI TIR45:2023 (13-143), ISO 13485:2016 §7.3 via 21 CFR 820 (QMSR) |
| Related | QMS-QM-001 (quality manual), QMS-SOP-001 (documents and records), QMS-SOP-002 (design control), QMS-SOP-003 (risk), QMS-SOP-010 (cybersecurity), QMS-SOP-011 (tool validation), `release-checklist.md` |

## 1. Purpose and scope

This plan defines how Libre RWS software is specified, designed, built,
verified, released and maintained so that the design file can support an FDA
510(k) for the Libre RWS PACS (21 CFR 892.2050, product code LLZ) and meets
the QMSR. It is the IEC 62304 §5.1 software development plan and, through
§10, the §6.1 software maintenance plan. Regulatory background: vault notes
*FDA 510(k) Requirements for PACS* and *Libre RWS FDA Compliance Plan*.

## 2. Safety classification

**IEC 62304 software safety class B** for LibreDCM (accepted by Patrick
Mulcahy 2026-11-09), with class-C depth of detailed design and unit
verification for the units that carry the main risk controls (store path,
index/patient association, pixel-data integrity). Rationale: a failure could
lead to non-serious injury (for example a delayed or wrong diagnosis caused
by missing, late or misattributed images), with the interpreting physician
as an external risk control. The classification and its rationale are
recorded in each module's risk management file and must be reviewed when
intended use changes (mammography, AI, Enhanced documentation) and when the
viewer is added (system-level re-classification).

## 3. Life cycle model

Agile, per AAMI TIR45: short iterations on GitHub, with the IEC 62304
activities done continuously and closed out at each release. A release is
the unit at which design reviews, system testing and design transfer happen
(QMS-SOP-002).

| IEC 62304 activity | How it is done here | Record |
|---|---|---|
| 5.1 Planning | This SDP; per-release plan in the GitHub milestone | SDP, milestone |
| 5.2 Requirements | Intended use and user needs, then SRS per module, IDs `<PREFIX>-SRS-###` (LibreDCM: `DCM`); `needs.py` checks every requirement serves a user need | `docs/dhf/<module>/intended-use.md`, `SRS.md` |
| 5.3 Architecture | Architecture document incl. SOUP items, interfaces, segregation | `docs/dhf/<module>/architecture.md` |
| 5.4 Detailed design | Class-C-depth units: design notes per unit; other units: Go doc comments | `docs/dhf/<module>/design/`, code |
| 5.5 Unit implementation and verification | Code review in the PR; `go vet`; gosec; unit tests with `-race`; fuzz seed corpus; acceptance criteria §6.1 | PR record, CI log |
| 5.6 Integration testing | End-to-end DIMSE tests over real associations; index conformance on SQLite and PostgreSQL; storage contract on filesystem and S3 | CI log, release verification logs |
| 5.7 System testing | System test protocol (DCMTK conformance, cluster failover, bus outage, installer upgrade on Linux and Windows) against the tagged build | Signed system test report per release |
| 5.8 Release | `release-checklist.md` | Release record, tag, archived artifacts |
| 6 Maintenance | §10 | Issue + PR |
| 7 Risk management | QMS-SOP-003; risk file per module, updated in every PR with risk impact | `docs/dhf/<module>/risk-management.md` |
| 8 Configuration management | §8 | Git history, `soup.md`, SBOM |
| 9 Problem resolution | §9 | Issue, `anomalies.md`, CAPA log |

### 3.1 Deliverables by safety class

| Deliverable | Class A | Class B (LibreDCM) | Class C depth (LibreDCM key units) |
|---|---|---|---|
| SRS with risk-control requirements | ✓ | ✓ | ✓ |
| Architecture with SOUP and segregation | | ✓ | ✓ |
| Detailed design per unit, interfaces | | (doc comments) | ✓ `design/*.md` |
| Unit verification with acceptance criteria | | ✓ | ✓ plus §6.1 extra criteria |
| Integration and system testing | ✓ (system) | ✓ | ✓ |
| Anomaly list with safety impact at release | ✓ | ✓ | ✓ |

## 4. Change workflow (every change)

1. **Issue** (template: requirement, change request, anomaly, security) with a
   first risk/security impact note.
2. **Branch** `type/short-name` from the integration branch.
3. **Implement**: requirement in the SRS, code, tests tagged
   `// Verifies: <IDs>`, documents updated.
4. **Pull request** with every template section filled in.
5. **Gates**: CI `verify` job green (vet, race tests, fork and client-copy
   tests, gosec, govulncheck, SBOM, trace check, user-need check, link
   check), and approval by a human reviewer. For design reviews at
   milestones, at least one reviewer is independent of the work being
   reviewed (QMSR / ISO 13485 §7.3.5).
6. **Merge** (squash or merge commit; history is never rewritten on shared
   branches).

AI coding agents may write code and documents. Their output is tool output:
the PR names the tool, and a human reviews and approves it.

### 4.1 Rules for AI coding agents

1. Work from an issue; work on a branch; never push or merge without the
   human's explicit OK.
2. New behaviour needs a requirement ID in the SRS. Never renumber or reuse
   IDs; mark removed ones `Retired`.
3. Tag every verifying test with `// Verifies: <IDs>` directly above the test
   function.
4. A new or upgraded dependency is a SOUP change: run `scripts/qms/soup.py`,
   complete the evaluation, check licence compatibility with Apache-2.0.
5. Changes touching networking, authentication, cryptography, storage,
   patient data, logging, licensing or updates also update the module's
   `cybersecurity.md`.
6. Never weaken a risk control (integrity checks, AE allow-list, TLS, audit
   log, outbox, backup consistency) without a written risk assessment in the PR.
7. No real patient data in tests, fixtures, logs or commits.
8. Fill in the whole PR template, name the AI tool, report real test results
   (`make vet test`, `scripts/qms/trace.py --check`), update CHANGELOG and
   user documentation.

## 5. SOUP / off-the-shelf software

- Every third-party component is listed in `docs/dhf/<module>/soup.md`
  (generated by `scripts/qms/soup.py` from `go.mod`, evaluation columns
  maintained by hand) and in the CycloneDX SBOM of each release.
- For each SOUP item: title, manufacturer, version, purpose, functional and
  performance requirements it must meet, required hardware/software, known
  anomalies reviewed, risk analysis, support level and end-of-support date,
  licence (FDA OTS guidance 2023; IEC 62304 §5.3.3–5.3.4, §7.1.3).
- **Forked and modified code is not SOUP.** `third_party/gonetdicom` and
  `third_party/librekey` are LibreDCM software items, verified like own code
  (their tests run in CI); every fork change is recorded in `RWS_FORK.md`.
- **Selection:** prefer maintained projects with a compatible licence; an
  archived or unmaintained project is a risk to be planned out (example:
  `gopkg.in/yaml.v3`, replaced by `go.yaml.in/yaml/v3` on 2026-11-10).
- **Updates:** a SOUP update is a change (issue + PR) with the SOUP list
  re-run, release notes of the new version reviewed for anomalies, and the
  full test suite green.
- **Vulnerability review:** every CI run (govulncheck), quarterly full SBOM
  review against NVD and CISA KEV, and on every security advisory affecting
  a component (QMS-SOP-010).
- System-level SOUP (Docker, base images, PostgreSQL, NATS, HAProxy) is
  pinned in the deployment files and listed in the architecture document.

## 6. Verification and validation

- Unit and integration tests run in CI on every PR.
- System tests run against the tagged release build (container image digest
  recorded) and are written up in a signed test report (system test
  protocol in the module's DHF).
- Validation (does the system meet user needs in real use?) is done at system
  level with clinical users, before any 510(k). LibreDCM's user needs are
  validated as part of the system.
- Test data is synthetic. No real patient data in the repository.
- **Effectiveness of risk controls** is shown by tests that create the
  hazardous situation (failure injection, outages, invalid input), not only
  by happy-path tests (QMS-SOP-003 §7).
- **Security testing:** SAST (gosec, blocking), SCA (govulncheck), fuzzing
  (Go native fuzz targets; seed corpus in every CI run, longer runs before
  each release), penetration testing before a submission (QMS-SOP-010).

### 6.1 Unit acceptance criteria (IEC 62304 §5.5.2–5.5.4)

All units: code reviewed in a PR; `go vet` and gosec clean (or annotated
with a reason); tests pass with the race detector; no new untraced
requirement. Class-C-depth units additionally: the detailed design note is
updated with the change; tests cover the unit's error paths, boundary values
and resource-failure behaviour named in its design note; fault-handling
paths (e.g. failed write, failed commit, cancelled context) are exercised.

### 6.2 Avoiding common software defects (IEC 62304 Amd 1 §5.1.12)

Defect categories typical for this kind of software, and how they are
avoided: memory-safety defects (Go is memory safe; no cgo in Libre code;
the purego JPEG codecs are SOUP); data races (`-race` in every CI run;
shared state behind mutexes); integer overflow and unchecked lengths
(gosec G115; lengths compared in 64-bit before slicing in the DICOM
decoders; fuzzing); injection (SQL built only from fixed column names, all
values bound; no shell execution from network input); path traversal
(storage keys checked by `blob.FS.path`); resource exhaustion (association
limits, PDU limit, timeouts, streaming C-MOVE, `get_max_bytes`); error
handling (a failed write or commit never returns success, tested by failure
injection); time handling (UTC timestamps, fixed-width time columns);
configuration errors (fail closed, empty variables ignored). Reviewers use
the CWE Top 25 as a checklist for changes to network-facing code.

## 7. Tools

Go toolchain (pinned in the build image), Docker, GitHub, GitHub Actions,
gosec, govulncheck, cyclonedx-gomod, DCMTK, Python 3 for `scripts/qms/`.
Tools that produce or check QMS records are validated for their intended
use (ISO 13485 §4.1.6, QMS-SOP-011): a validation note per tool in
`docs/qms/tool-validation/`.

## 8. Configuration management

- **Configuration items:** source code, `go.mod`/`go.sum`, forked modules in
  `third_party/`, deployment files (Dockerfile with base images pinned by
  digest, Compose files, HAProxy config, installer scripts), CI workflows,
  QMS and DHF documents, generated records (trace matrix, SBOM, SOUP list).
- **Identification:** every item is identified by its path and the Git
  commit; a release by its annotated tag `vMAJOR.MINOR.PATCH[-beta.N]`
  and the image digest; dependencies by version and checksum (`go.sum`).
- **Branches:** `master` (protected, released-quality integration branch);
  short-lived `type/short-name` branches; `release/vX.Y.Z` branches during
  release verification. Branches are deleted after merge.
- **Change control:** §4; branch protection enforces it (validated in
  VAL-0001).
- **Builds:** reproducible from the tag with the pinned Go version and the
  base images pinned by digest in `deploy/docker/Dockerfile.dicom`;
  `scripts/qms/release-verify.sh` records the toolchain version and the
  built image IDs.
- **Status accounting:** CHANGELOG (Unreleased + per release), release
  records, GitHub history.
- **Archive:** released source, SBOM, image digest, checksums and release
  record attached to the GitHub release; offline mirror per QMS-SOP-001 §7.3.

## 9. Problem resolution (IEC 62304 §9)

1. **Report:** every problem (defect, test failure, scan finding, user
   report, vulnerability) becomes a GitHub issue of type *anomaly* or
   *security* (security issues privately, QMS-SOP-010), or a row in the
   module's `anomalies.md` until it is filed.
2. **Investigate:** identify the cause, the affected versions, and the
   **safety impact** using the hazards in the risk file (HAZ-###).
3. **Advise:** tell the relevant people; for released versions consider
   customer notification and field action (QMS-SOP-005).
4. **Change control:** fix through §4; link the issue to the PR.
5. **Records:** the issue, PR and anomaly row hold the history.
6. **Trends:** problems are reviewed for trends at each release and
   management review; a systemic cause opens a CAPA (QMS-SOP-004).
7. **Verify:** the fix is verified (regression test, or a fuzz crasher kept
   in `testdata/fuzz/` as a permanent regression input).
8. **Release:** open anomalies are listed with their safety impact in the
   release record; none may be unacceptable per QMS-SOP-003.

## 10. Maintenance plan (IEC 62304 §6)

- **Feedback sources:** GitHub issues, `SECURITY.md` reports, complaints
  (QMS-SOP-005), SOUP and vulnerability monitoring (QMS-SOP-010), test and
  audit findings.
- **Evaluation:** each item is assessed for safety and security impact and
  whether it is a problem (→ §9) or a change request.
- **Modification:** the same process as development (§4); a release after
  clearance gets a "new 510(k) needed?" assessment per FDA's software-change
  guidance (PR template).
- **Support:** during the beta period only the latest release receives
  fixes (SECURITY.md); a support and end-of-support policy is published with
  the first general release.

## 11. Records and signatures

Decided 2026-11-09 (Patrick Mulcahy): **GitHub + Google Drive split.** Design
records and QMS procedures live in this public repository; records that
aren't tied to code (training, supplier contracts, signed release and
design-review PDFs, regulatory correspondence, anything sensitive such as
penetration-test reports or complaints with patient data) live in Google
Drive (folder `LibreRWS/QMS`, owned by patrick@mulcahy.io until the
manufacturer, Libre Systems, has its own Workspace), indexed from
`docs/qms/records-index.md`. Control of documents and records, and the
Part 11 position, are in QMS-SOP-001; the validation record is
`validation/part11-github-drive.md` (VAL-0001).

`master` is protected (pull request required, also for admins; no force
pushes or deletions; conversations resolved; the `verify` CI check must
pass) and `v*` release tags cannot be moved or deleted. Required approvals
are 0 while there is a single developer, because GitHub does not let an
author approve their own pull request; this becomes 1 when a second reviewer
joins. Until GitHub approvals are validated as electronic signatures
(VAL-0001 findings F1, F2, F4, F6), release approvals are also signed on the
release record.

## 12. Open items before approval

- Name the roles: management representative, QA approver, independent
  reviewer (Kanban card "Name QMS roles"; interim holders in QMS-QM-001 §5).
- ~~Decide on docs-as-code vs an eQMS~~ decided 2026-11-09: GitHub + Drive (§11).
- ~~Enable branch protection~~ done 2026-11-09. The `verify` CI check
  (`qms.yml`) is a required status check on `master` since 2026-11-09.
- ~~Configuration management, problem resolution, maintenance and
  defect-avoidance sections~~ completed 2026-11-10 (§6.2, §8–§10). Checked
  against IEC 62304 §5.1.1–§5.1.12 and §6.1: every planning item has a
  section.
- Declare the QMS baseline release (proposed `v0.3.0`) once the risk file's
  pre-baseline actions are done (risk-management.md §5).
