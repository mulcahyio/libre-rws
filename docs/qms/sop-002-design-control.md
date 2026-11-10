# SOP: Design and development control

| | |
|---|---|
| Document | QMS-SOP-002 |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Requirements | ISO 13485:2016 §7.3.1–§7.3.10 (via 21 CFR 820.10); IEC 62304 §5, §6, §8; IEC 62366-1 (usability); FDA *Content of Premarket Submissions for Device Software Functions*; FDA *Deciding When to Submit a 510(k) for a Software Change to an Existing Device* |
| Related | QMS-SDP-001 (how software work is done day to day), QMS-SOP-001 (documents), QMS-SOP-003 (risk), QMS-SOP-010 (cybersecurity), `release-checklist.md` |

## 1. Purpose and scope

This is the documented design and development procedure required by ISO 13485 §7.3.1. It ties together how a Libre RWS product moves from user needs to a released,
maintained product under design control: planning, inputs, outputs, reviews,
verification, validation, transfer, changes and the design history file.
The SDP covers the software engineering detail; this SOP covers the design
control framework around it. Applies to every module and to the system.

## 2. Responsibilities

| Role | Responsibility |
|---|---|
| Top management | Approves product scope and intended use; assigns resources |
| Module lead (interim Patrick Mulcahy) | Plans design work; owns the design history file of the module |
| Developer (people and AI tools under review) | Produces design outputs and verification under the SDP |
| QA approver | Approves design inputs, design review records, release records |
| Independent reviewer | Takes part in each formal design review (§6) without responsibility for the work reviewed |

## 3. Design and development planning (§7.3.2)

- **Per product/module:** the SDP (life cycle, deliverables, verification,
  configuration management) plus the module's DHF index in
  `docs/qms/README.md`.
- **Per release:** a GitHub milestone lists the issues in scope, the planned
  design review(s) and the target date. The milestone is the release plan; it
  is updated as the work changes (records: milestone history).
- **Interfaces between groups** (e.g. an outside consultant, a future second
  developer, the LibreKey server team) are recorded in the milestone or the
  issue.

## 4. Design inputs (§7.3.3)

| Input | Where | Notes |
|---|---|---|
| Intended use, users, use environment, limitations | `docs/dhf/<module>/intended-use.md` | Approved by top management |
| User needs | same, §7 | Each traced to requirements (`needs.py`) |
| Functional, performance, interface and security requirements | `SRS.md` | Unambiguous, testable, with permanent IDs |
| Risk-control requirements | `SRS.md` rows naming the hazard; labeling statements | From QMS-SOP-003 |
| Regulatory and standards requirements | Quality manual §9, compliance plan, DICOM PS3, IHE profiles | E.g. DICOM conformance, TLS, audit (ATNA) |
| Usability inputs | Use specification and safety-related use scenarios | IEC 62366-1 |
| Lessons from previous designs, complaints, anomalies | `anomalies.md`, CAPA | |

**Review of inputs:** inputs are reviewed for completeness, clarity and
conflicts in the PR that adds or changes them and at the first design review
of a release. Incomplete, ambiguous or conflicting inputs are resolved before
they are approved (§7.3.3 last paragraph).

## 5. Design outputs (§7.3.4)

Architecture, detailed design (class-C-depth units), source code,
configuration and deployment files, installer, labeling (installation guide,
release notes, DICOM Conformance Statement, security documentation), SBOM.
Outputs must: meet the inputs (trace matrix, user-need check), give the
information needed for release and distribution (release checklist),
contain or reference acceptance criteria (tests tagged `Verifies:`), and
identify the characteristics essential for safe use (risk-control
requirements and labeling limitations). Outputs are approved through the PR
process (QMS-SOP-001 §5.3).

## 6. Design reviews (§7.3.5)

| Review | When | Inputs | Participants |
|---|---|---|---|
| DR-1 Requirements review | Start of a release with new or changed requirements | Intended use, user needs, SRS changes, risk analysis update | Module lead, QA approver, independent reviewer |
| DR-2 Design review | Before system testing | Architecture and design changes, risk file, cybersecurity file, anomaly list, unit/integration results | Same |
| DR-3 Release review | Before tagging (part of the release checklist) | Release record draft: system test report, trace matrix, SBOM, vulnerability assessment, anomaly list, risk management report | Same |

For small releases DR-1 to DR-3 may be combined into one review, stated in
the record. **Record:** minutes in `docs/dhf/<module>/reviews/DR-<date>.md`
(attendees with roles, items reviewed with versions, findings, actions with
owners, decision), and a signed PDF in Drive `03 Design Reviews (signed)`.
Actions are tracked as issues until closed. **Until an independent reviewer
is available (decision D5) reviews are held and recorded, the absence is
stated in the record, and the independent review is done retrospectively
before the QMS baseline is declared and before any submission.**

## 7. Design verification (§7.3.6)

Verification confirms outputs meet inputs: automated tests in CI (unit,
integration, fuzz seeds, SAST, SCA), the trace matrix (`trace.py`), system
tests against the tagged build with a signed report (system test protocol),
and document checks (user needs, links). Methods, acceptance criteria and
results are recorded (CI logs, verification logs in
`docs/releases/v<V>-verification/`). Where the design connects to other
devices (modalities, viewers), verification uses real DICOM peers (DCMTK)
in the configurations described in the DICOM Conformance Statement.

## 8. Design validation (§7.3.7)

Validation confirms the product meets user needs and intended use under
actual or simulated use conditions, on production-equivalent builds, before
release for clinical use. For LibreDCM (non-device today) validation is done
as part of the Libre RWS PACS system: simulated clinical workflow with real
modalities or simulators, a viewer and representative users (radiologists,
technologists, PACS administrators), plus usability validation of user
interfaces (installer, Libre WT, viewer). Clinical evaluation follows the
Pre-Submission outcome. Record: validation protocol and signed report per
system release.

## 9. Design transfer (§7.3.8)

Transfer into "production" (the released, distributable build) follows
`release-checklist.md`: reproducible build from the tag, pinned toolchain
and base images, verification of the exact build, artifacts archived (source
tarball, SBOM, checksums, image digest, release record), installation
instructions verified by the installer's own checks. The release record is
the transfer record.

## 10. Design changes (§7.3.9)

Every change after the first approved design is a design change: issue →
branch → PR with the template's design-control, risk, security and SOUP
sections (SDP §4). The PR records the evaluation of the change's effect on
constituent parts, on products already delivered (customers on older
versions: notify? field action? QMS-SOP-005), on inputs and outputs, on the
risk file and on regulatory status. **After clearance**, each change gets
the "new 510(k) needed?" assessment per FDA's software-change guidance
(PR template, Regulatory section), recorded in the PR.

## 11. Design and development files (§7.3.10)

The design history file per module is `docs/dhf/<module>/` together with
the change records (issues, PRs, CI), release records (`docs/releases/`)
and signed records in Drive. Its index is `docs/qms/README.md`. Contents:
gap assessment (retrospective), intended use, SRS, architecture, detailed
design, risk management file, cybersecurity file, SOUP list and SBOM, trace
matrix, anomaly list, system test protocol and reports, DICOM Conformance
Statement, design review records.

## 12. Retrospective design control (legacy software)

Software developed before the QMS (LibreDCM up to 0.2.0) is brought under
design control as legacy software (IEC 62304 Amd 1 §4.4): gap assessment
(`docs/dhf/libredcm/gap-assessment.md`), retrospective design documents,
verification of the baseline build under the QMS, and a declared QMS
baseline release. Development history before the baseline is kept as
pre-QMS evidence, not as design records.
