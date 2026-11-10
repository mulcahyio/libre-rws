# Validation record: GitHub + Google Drive as the QMS record system (21 CFR Part 11)

| | |
|---|---|
| Record | VAL-0001 |
| Status | **Executed 2026-11-10 by Hermes/Claude; awaiting review and approval** by the QA approver |
| System | GitHub repository `mulcahyio/libre-rws` (public, free personal account) with GitHub Actions; Google Drive folder `LibreRWS/QMS` (id `1CdqxaUdTPYm88fUgV8I5LHEjrdh4fkeP`, owner patrick@mulcahy.io) |
| Procedure | QMS-SOP-001 §6; QMS-SOP-011 (risk-based tool validation) |
| Approach | Risk-based validation for intended use (FDA *General Principles of Software Validation*; *Computer Software Assurance for Production and Quality Management System Software*): configuration verification against requirements, plus evidence from real use. Commercial cloud services are not code-validated by the user; their configuration and our use of them are |

## 1. Intended use

Store and control the design history file and QMS procedures (GitHub), the
change records (issues, pull requests, CI logs), and records that are not
tied to code or need a signature (Drive); support approvals of documents and
records by authorised people.

**Risk of the intended use:** medium. A failure (lost, altered or
unattributable record) does not harm patients directly but would undermine
the evidence that the device was designed under control. Hence: verify the
configuration that protects records, keep independent copies, and do not
rely on GitHub approvals alone as signatures until a second approver exists.

## 2. Requirements (URS)

| ID | Requirement | Part 11 |
|---|---|---|
| UR-01 | Changes to the controlled branch only through a pull request, also for administrators | 11.10(f), (g) |
| UR-02 | Automated verification (CI) must pass and review conversations be resolved before a merge; approvals are dismissed when new commits are pushed | 11.10(f) |
| UR-03 | History of the controlled branch cannot be rewritten or deleted | 11.10(c), (e) |
| UR-04 | Release tags cannot be moved or deleted, by anyone | 11.10(c) |
| UR-05 | Only named, authenticated accounts can change records; secrets are kept out of the repository | 11.10(d), (g), 11.100, 11.300 |
| UR-06 | Every change shows who, when and what (audit trail); approvals show signer, time and meaning and are bound to the exact content | 11.10(e), 11.50, 11.70 |
| UR-07 | Complete, accurate copies of records can be produced in human-readable and electronic form | 11.10(b) |
| UR-08 | Drive records are owned by the QMS owner account, not shared outside named accounts; restricted folders are restricted; signed records can be checked for integrity | 11.10(c), (d), 11.70 |

## 3. Test cases and results (executed 2026-11-10)

| Test | Method | Expected | Result |
|---|---|---|---|
| VT-01 (UR-01) | `scripts/qms/part11_check.py` reads branch protection of `master` | PR required; `enforce_admins` true | **PASS** |
| VT-02 (UR-02) | Same script | Required check `verify`; conversation resolution on; stale reviews dismissed | **PASS** |
| VT-03 (UR-03) | Same script | Force push and deletion blocked | **PASS** |
| VT-03b (UR-01, UR-03) | Real-use evidence: `git log --first-parent master --since=2026-11-09` | Every change to `master` since protection was enabled is a PR merge commit | **PASS**: 4 of 4 (`47fb53c` PR #3, `6fac7b3` PR #7, `5a5135c` PR #8, `d850607` PR #9) |
| VT-04 (UR-04) | Same script reads rulesets | Active tag ruleset on `refs/tags/v*` with deletion, non-fast-forward and update rules, no bypass actors | **PASS** (ruleset "protect release tags", id 26867069) |
| VT-05 (UR-05) | Same script; collaborators list | Secret scanning + push protection on; write access only for approved accounts | **PASS with finding F1**: write access = `mulcahyio` (admin) and `StoreHouseAdmin` (write). Both are Patrick Mulcahy |
| VT-05b (UR-05) | 2FA on accounts with write access | 2FA enabled | **Not verifiable with the current token** (`two_factor_authentication` not returned without the `user` scope). **Finding F2**: Patrick confirms in GitHub settings |
| VT-06 (UR-06) | GitHub API commit verification of merge commit `d850607`; `gh pr list --state merged --json mergedBy,mergedAt` | Merge commits signed by GitHub; PR records show who merged and when | **PASS** (`verified: true, reason: valid`; all 7 merged PRs, #2–#4 and #6–#9, show `mergedBy mulcahyio` with UTC times). Note: `reviewDecision` empty, because 0 approvals are required (see SOP-001 §6.3) |
| VT-07 (UR-07) | `git clone` gives the full history; `gh pr view N --json ...` exports PR records; Drive file download | Copies complete and readable | **PASS** (local clone holds all commits and tags; PR JSON export works; Drive PDF downloaded) |
| VT-08 (UR-08) | Drive API: folder metadata and permissions of `QMS`, `08 Complaints (restricted)`, `10 Security Test Reports (restricted)` | Owner patrick@mulcahy.io; no `anyone`/domain sharing; only named accounts | **PASS with finding F3**: owner patrick@mulcahy.io; writer patrickm@storehouse.gold (inherited from the parent folder `LibreRWS`, which is owned by patrickm@storehouse.gold); no link or domain sharing |
| VT-09 (UR-08) | Download REC-0001 (signed release record PDF) and compute SHA-256 | Matches `records-index.md` | **PASS**: `10af1533d65f0fdc81a708c4833bf18863914e0c47ef6fa7a26b1931db4e0900` |
| VT-10 (UR-06) | Drive revision history of `QMS/index.md` | Revisions with user and time are kept | **PASS** (1 revision, patrick@mulcahy.io, 2026-11-09T17:24:34Z) |

Negative check of the tool: `part11_check.py --branch <unprotected branch>`
reports FAIL and exits 1 (run 2026-11-10).

## 4. Findings and actions

| ID | Finding | Risk | Proposed action (owner) |
|---|---|---|---|
| F1 | A second account controlled by the same person (`StoreHouseAdmin`) has write access | It could approve or merge PRs and look like an independent reviewer | Remove its write access, or record it as a non-approving account (Patrick). SOP-001 §6.3 already forbids its use for approvals |
| F2 | 2FA status could not be read | Weak authentication would break §11.10(d)/§11.300 | Patrick confirms 2FA on both GitHub accounts and both Google accounts; record the confirmation here |
| F3 | Drive parent folder `LibreRWS` is owned by a different account (patrickm@storehouse.gold), which also has write access to the QMS folder | Two accounts can change records; ownership split across Workspaces | Acceptable while both are Patrick's; move everything to Libre Systems' Workspace when it exists (already planned) |
| F4 | No second person can approve in GitHub | GitHub approvals are not yet usable as electronic signatures | Keep signed PDFs for release, design-review and baseline records (SOP-001 §6.3) |
| F5 | No offline mirror exists yet | Loss of the GitHub or Google account would lose records | Create the first mirror at the next release (SOP-001 §7.3) |
| F6 | FDA e-signature certification letter (§11.100(c)) not sent | Required before relying on e-signatures for FDA-required records | Top management, before the first regulated release |

## 5. Re-validation

Re-run `python3 scripts/qms/part11_check.py` and VT-08/VT-09 (Drive):
at every internal audit, after any change to branch protection, rulesets,
collaborators or Drive sharing, and when the repository moves to the Libre
Systems organisation. Record the result as a new revision of this record.

## 6. Conclusion

The configuration meets UR-01 to UR-08 for its intended use, with findings
F1–F6 open. **Use for design-history and change records is acceptable now.
Electronic signatures in GitHub are not relied on until F1, F2, F4 and F6
are closed**; signed PDFs are used for approvals in the meantime.

| Role | Name | Signature / date |
|---|---|---|
| Executed by | Hermes (Claude), AI tool, for Patrick Mulcahy | 2026-11-10 |
| Reviewed and approved (QA approver) | | |
