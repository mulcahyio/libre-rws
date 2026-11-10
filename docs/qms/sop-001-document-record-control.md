# SOP: Document and record control

| | |
|---|---|
| Document | QMS-SOP-001 |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Requirements | ISO 13485:2016 §4.2.4, §4.2.5; 21 CFR 820.35 (QMSR control of records); 21 CFR Part 11 (electronic records, electronic signatures) |
| Related | QMS-QM-001 §7, `records-index.md`, `validation/part11-github-drive.md` (validation record), QMS-SOP-011 (tool validation) |

## 1. Purpose and scope

How QMS documents (manual, procedures, plans, templates, design documents)
are created, reviewed, approved, changed, distributed and retired, and how
records are identified, stored, protected, retrieved, retained and disposed
of. Covers both record systems decided on 2026-11-09: the GitHub repository
and Google Drive.

## 2. Definitions

- **Document:** information that tells people what to do or what the
  product is (procedures, plans, specifications, templates). It changes over
  time under control.
- **Record:** evidence that something was done (test logs, review minutes,
  release records, training records, complaint files). It is never changed
  after it is complete; corrections are added, not overwritten.
- **Controlled location:** the `master` branch of `mulcahyio/libre-rws`
  (later the Libre Systems organisation), and the Drive folder `LibreRWS/QMS`.
- **Approval:** the act by which an authorised person accepts a document or
  record (§5.3 and §6).

## 3. Responsibilities

| Role | Responsibility |
|---|---|
| Author (developer or AI tool) | Drafts or changes the document on a branch; opens the pull request |
| QA approver | Reviews and approves documents; approves records that need a signature |
| Management representative | Keeps this procedure and the records index current; checks the record systems at each internal audit |
| Everyone | Uses only the current version from the controlled location; never keeps private copies as working documents |

## 4. Where things live

| Kind | Location | Format |
|---|---|---|
| Quality manual, SOPs, SDP, templates | `docs/qms/` | Markdown |
| Design history file per module (requirements, architecture, design, risk, cybersecurity, SOUP, trace matrix, SBOM, anomaly list) | `docs/dhf/<module>/` | Markdown, generated JSON/Markdown |
| Release records, verification logs | `docs/releases/` | Markdown, text logs |
| Change records | GitHub issues and pull requests (template sections, review comments, CI results) | GitHub |
| Signed records, records that are not tied to code, sensitive records | Drive `LibreRWS/QMS` subfolders 01–10 | PDF (signed), Google Docs/Sheets |
| Index of Drive records | `docs/qms/records-index.md` | Markdown |
| Decisions and session notes (input, not records) | Patrick's Second Brain vault | Markdown |

Anything decided in chat, by email or in the vault becomes a record only
when it is written into the repository or Drive (SDP §4, compliance plan §3.6).

## 5. Document control

### 5.1 Identification

- Each controlled document carries a header table with **Document ID**,
  **Status** and the standards it implements. IDs: `QMS-QM-###` (manual),
  `QMS-SOP-###` (procedures), `QMS-SDP-###` (development plan),
  `DHF-<MOD>-<TYPE>` (design file, e.g. `DHF-DCM-SRS`), `REC-####` (Drive
  records).
- **Version** = the Git commit of `master` that contains the document. A
  released product version pins the document versions through its tag. A
  human-readable revision note is not needed: `git log -- <file>` is the
  revision history.
- Status values: `DRAFT`, `DRAFT for approval`, `Effective` (approved),
  `Superseded`, `Retired`.

### 5.2 Creation and change

1. Open or reuse a GitHub issue that says why the document is created or
   changed.
2. Edit on a branch (`docs/<short-name>` or as part of the code change it
   belongs to).
3. Open a pull request using the template; tick "documents updated".
4. CI runs the document checks: trace matrix (`trace.py --check`), user-need
   check (`needs.py`) and relative-link check (`mdlinks.py`).

### 5.3 Review and approval

- The **QA approver** reviews the pull request. Approval of a document is the
  **merge of the pull request into `master` by the QA approver** (while
  `master` needs 0 GitHub approvals, see §6.3), or a GitHub *Approve* review
  by the QA approver once a second person can approve.
- The PR description states the meaning of the approval: "Approved for use:
  <documents>". The merge record shows who approved, when (UTC) and which
  exact content (commit SHA).
- A document becomes **Effective** when its Status line is changed to
  `Effective (approved <date>, PR #n)` in the approving pull request.
- Until the QMS baseline, all documents stay `DRAFT`; the baseline approval
  is one pull request that sets them all to `Effective`, plus a signed
  approval record in Drive `03 Design Reviews (signed)`.

### 5.4 Distribution and use

The repository is public; the current version is always `master`.
Released product documentation ships inside the release (source tarball and
the GitHub release page). Printed or exported copies are uncontrolled.

### 5.5 Obsolete documents

Superseded content stays in Git history. A retired document is either
deleted from `master` (history kept) or marked `Retired` with a pointer to
its replacement. Requirement IDs are never reused (SDP §4.1).

### 5.6 External documents

Standards, FDA guidance and regulations used by the QMS are listed with
edition/date in `docs/qms/external-documents.md` (to be created at the
first management review). Their applicability is re-checked at each
management review (fees and guidance versions change yearly).

## 6. Electronic records and signatures (21 CFR Part 11)

### 6.1 What Part 11 covers here

Records required by the QMSR that are kept electronically: design history
files, change records (issues/PRs), CI verification logs, release records,
complaint/CAPA/training records in Drive. Electronic signatures: approvals
(PR merges/reviews by the QA approver) and signed PDFs in Drive.

### 6.2 Controls in place

| Part 11 requirement | Control | Evidence |
|---|---|---|
| §11.10(a) validation | The GitHub + Drive setup is validated for its intended use | `validation/part11-github-drive.md` |
| §11.10(b) accurate and complete copies | Any clone of the repository is a complete copy with full history; `gh pr view --json` and the GitHub API export issue and PR records; Drive files download as PDF | Validation VT-07 |
| §11.10(c) protection and retrieval for the retention period | Git history cannot be rewritten on `master` (no force push, no deletion, admins included); release tags cannot be moved or deleted; release artifacts archived; periodic offline mirror (§7.3) | VT-01…VT-04 |
| §11.10(d) limited system access | Only named accounts have write access; 2FA required on every account with write access | VT-05 |
| §11.10(e) audit trail | Git commits (author, committer, time, content), PR timeline (who opened, reviewed, merged, when), CI run logs; Drive version history and activity | VT-06 |
| §11.10(f) operational checks (sequence) | Branch protection forces issue → branch → PR → CI → merge; the release checklist forces the release sequence | VT-01, VT-02 |
| §11.10(g) authority checks | Only accounts with write/admin role can merge; only the QA approver may approve QMS documents and records (this SOP) | VT-05 |
| §11.10(i) training | People who create, approve or sign records are trained on this SOP (QMS-SOP-007) | Training records |
| §11.10(j) accountability | Signers acknowledge in writing that their electronic signature is the legally binding equivalent of their handwritten signature (form in `templates/esignature-acknowledgement.md`, signed copy in Drive 01) | Drive 01 |
| §11.10(k) documentation control | This SOP; system documentation changes go through PRs | Git history |
| §11.50 signature manifestation | A GitHub approval/merge shows the signer's account and name, UTC time and action; the meaning is stated in the PR ("Approved for use", "Approved for release"). Signed PDFs show printed name, date, time and meaning | VT-06 |
| §11.70 signature/record linking | The approval is bound to the exact commit SHA; a signed PDF records the SHA-256 of the Markdown it signs (REC-0001 practice) | VT-06 |
| §11.100 uniqueness | One personal GitHub account per person for QMS actions; accounts are never shared or reassigned. **A second account controlled by the same person may not approve anything** (§6.3) | VT-05 |
| §11.100(c) certification to FDA | Before electronic signatures are relied on for FDA-required records, Libre Systems sends FDA the letter certifying that its electronic signatures are the legally binding equivalent of handwritten signatures | Open: action for top management before the first regulated release |
| §11.200 signature components | Account login with password and second factor; each approval is a deliberate action inside an authenticated session | VT-05 |
| §11.300 identification codes/passwords | Managed by GitHub/Google (password policy, 2FA, session expiry); lost or compromised credentials are revoked at once and reported as a security incident (QMS-SOP-010) | Supplier controls (QMS-SOP-006) |

### 6.3 Current limitations (and how they are handled)

1. **No second approver yet.** `master` requires 0 approving reviews because
   GitHub does not let an author approve their own PR. The merge by the QA
   approver is the approval (§5.3). For records that need a clear
   signature (release records, design reviews, baseline approval) a signed
   PDF is filed in Drive as well, as was done for REC-0001.
2. **Second account with write access.** `StoreHouseAdmin` (also Patrick
   Mulcahy) has write access to the repository. It must not be used to
   approve or merge QMS changes, because that would look like independent
   review. Proposed: remove its write access (decision for Patrick).
3. **Commits are not signed** by their authors; merge commits made by
   GitHub are signed by GitHub. Signed tags and commits are planned (Kanban
   card "Signed releases").
4. **Audit-log retention:** GitHub keeps PR and commit history for the life
   of the repository, but account security logs are kept for a limited time.
   PR metadata is therefore exported into the release record at each
   release (§7.3).
5. **Vendor dependence:** if GitHub or Google closes the account, records
   could be lost. Mitigated by the offline mirror (§7.3).

## 7. Record control

### 7.1 Identification and index

- Records in the repository are identified by path and commit; generated
  records by the CI run ID.
- Records in Drive get a `REC-####` number and a row in
  `docs/qms/records-index.md` (ID, title, type, date, link, owner, and the
  SHA-256 of the file when it is a signed PDF).

### 7.2 Corrections

Completed records are never edited in place. A correction is a new commit
(repository) or a new version with a note (Drive), stating what changed, why,
who and when; the original stays readable in history.

### 7.3 Protection, backup and retention

- **Retention:** at least the expected life of the device version the record
  relates to, and **not less than 2 years from the date the version is
  released for commercial distribution** (21 CFR 820.35 / ISO 13485
  §4.2.5). Proposed lifetime definition: 5 years after the last release of a
  major version. Design history files are kept for the life of the product
  family.
- **Offline mirror:** at every release and at least quarterly, a full
  mirror of the repository (`git clone --mirror`, plus issues and PRs
  exported as JSON with `gh`) and an export of Drive `LibreRWS/QMS` are
  stored in a separate location under Libre Systems' control. The mirror is
  logged in the records index.
- **Restricted records** (complaints with patient data, security test
  reports, unpublished vulnerabilities): Drive subfolders 08 and 10 only,
  shared with named people only, never in the public repository.
- **No patient data** in the repository, ever (SDP §4.1 rule 7).

### 7.4 Disposal

Records are disposed of only after their retention period, with the
approval of the management representative, and the disposal is logged.

## 8. Checks

- Internal audit (QMS-SOP-008) samples PRs and Drive records against this
  SOP and re-runs the validation checks in `validation/part11-github-drive.md`
  §5 (configuration read-outs).
- Any change to branch protection, rulesets, repository access or the Drive
  folder sharing is a change to a validated system: re-run the affected
  validation tests and record the result.
