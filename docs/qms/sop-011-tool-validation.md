# SOP: Validation of software tools used in the QMS

| | |
|---|---|
| Document | QMS-SOP-011 |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Requirements | ISO 13485:2016 §4.1.6 (validation of software used in the QMS), §7.5.6 (process validation, for build/release tooling); FDA *Computer Software Assurance for Production and Quality Management System Software*; IEC 62304 §8.1.2 (identification of tools as configuration items) |
| Related | QMS-SOP-006 (suppliers), QMS-SDP-001 §7, `tool-validation/` (one note per tool), `validation/part11-github-drive.md` (VAL-0001) |

## 1. Purpose and scope

How Libre Systems shows that software tools used to produce, verify or
manage QMS records and the product are fit for their intended use, with
effort proportional to risk. In scope: build tools (Go toolchain, Docker),
CI (GitHub Actions), verification and analysis tools (gosec, govulncheck,
cyclonedx-gomod, DCMTK, release-verify.sh), QMS scripts (`scripts/qms/`),
record systems (GitHub, Drive: VAL-0001), and AI coding tools.

## 2. Approach (risk-based, CSA)

1. **Intended use:** what the tool does in our process and which records or
   product outputs depend on it.
2. **Risk:** what happens if the tool fails silently (wrong output that looks
   right), considering other controls that would catch it.
   - **High:** a silent failure could release a defective product or a false
     record without other detection.
   - **Medium:** a silent failure could hide a problem, but another control
     would likely catch it.
   - **Low:** failures are obvious or fully covered by other controls.
3. **Assurance activities by risk:**
   - Low: record the tool, version and supplier; rely on vendor quality and
     the visible results.
   - Medium: plus a documented check that the tool does its job for our use
     (a positive control: give it something it must detect, or compare its
     output with an independent source).
   - High: plus scripted tests kept under version control and re-run on
     every change of the tool or its configuration.
4. **Record:** a validation note per tool in `tool-validation/` with intended
   use, risk, version, activities, results (real output), conclusion and
   re-validation triggers.
5. **Re-validation:** when the tool version changes in a way that can change
   results (major/minor versions of scanners, any change to our own
   scripts), when its intended use changes, or after a failure. Pinned
   versions in CI make version changes visible in PRs.

## 3. AI coding tools

AI tools (Claude via Hermes / Claude Code) produce code, tests and
documents. Their output is never accepted on the tool's authority: it goes
through human review, the CI gates and the tests like any other change (SDP
§4.1). The tool is therefore **low risk as a QMS tool**: its failures are
caught by the controls that apply to all changes. The record of which tool
produced a change is the PR "Authorship" section.

## 4. Responsibilities

The management representative keeps the tool list and approves validation
notes; the developer performs the activities; the QA approver approves high-
risk validations.
