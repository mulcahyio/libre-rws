# Software Development Plan (SDP)

| | |
|---|---|
| Document | QMS-SDP-001 |
| Status | **DRAFT**, not yet approved. Becomes effective when signed off at the QMS baseline |
| Applies to | All Libre RWS software items: LibreDCM, Libre WT, LibreKey client copy, future modules |
| Standards | IEC 62304 Ed 1.1 (FDA rec. 13-79), ISO 14971:2019 (5-125), IEC 81001-5-1 (13-122), AAMI TIR45:2023 (13-143), ISO 13485:2016 §7.3 via 21 CFR 820 (QMSR) |

## 1. Purpose and scope

This plan defines how Libre RWS software is specified, designed, built,
verified, released and maintained so that the design file can support an FDA
510(k) for the Libre RWS PACS (21 CFR 892.2050, product code LLZ) and meets
the QMSR. Regulatory background: vault notes *FDA 510(k) Requirements for PACS*
and *Libre RWS FDA Compliance Plan*.

## 2. Safety classification

**IEC 62304 software safety class B** for LibreDCM (accepted by Patrick
Mulcahy 2026-11-09), with class-C depth of detailed design and unit
verification for the units that carry the main risk controls (store path,
index/patient association, pixel-data integrity). Rationale: a failure could lead to non-serious injury (for example a delayed
or wrong diagnosis caused by missing, late or misattributed images), with the
interpreting physician as an external risk control. The classification and
its rationale are recorded in each module's risk management file and must be
reviewed when intended use changes (mammography, AI, Enhanced documentation).

## 3. Life cycle model

Agile, per AAMI TIR45: short iterations on GitHub, with the IEC 62304
activities done continuously and closed out at each release.

| IEC 62304 activity | How it is done here | Record |
|---|---|---|
| 5.1 Planning | This SDP; per-release plan in the milestone | SDP, GitHub milestone |
| 5.2 Requirements | SRS per module, IDs `<PREFIX>-SRS-###` (LibreDCM: `DCM`) | `docs/dhf/<module>/SRS.md` |
| 5.3 Architecture | Architecture document incl. SOUP items and interfaces | `docs/dhf/<module>/architecture.md` |
| 5.4 Detailed design | Design notes per software unit; Go doc comments | `docs/dhf/<module>/design/`, code |
| 5.5 Unit implementation & verification | Code review in PR, `go vet`, unit tests with `-race` | PR record, CI log |
| 5.6 Integration testing | End-to-end DIMSE tests, index conformance on SQLite/PostgreSQL/S3 | CI log, test report |
| 5.7 System testing | DCMTK conformance, cluster failover, load, installer tests | Signed test report per release |
| 5.8 Release | `docs/qms/release-checklist.md` | Release record, signed tag |
| 6 Maintenance | Same process as development, started from an issue | Issue + PR |
| 7 Risk management | ISO 14971 file per module, updated in every PR with risk impact | `docs/dhf/<module>/risk-management.md` |
| 8 Configuration management | Git, protected branches, tags, SOUP list, pinned base-image digests | Git history, `soup.md`, SBOM |
| 9 Problem resolution | GitHub issues (type *anomaly*), safety impact triage, CAPA link if systemic | Issue, CAPA log |

## 4. Change workflow (every change)

1. **Issue** (template: requirement, change request, anomaly, security) with a
   first risk/security impact note.
2. **Branch** `type/short-name` from the integration branch.
3. **Implement**: requirement in the SRS, code, tests tagged
   `// Verifies: <IDs>`, documents updated.
4. **Pull request** with every template section filled in.
5. **Gates**: CI (vet, race tests, govulncheck, SBOM, trace check) green, and
   approval by a human reviewer. For design reviews at milestones, at least
   one reviewer is independent of the work being reviewed (QMSR / ISO 13485
   §7.3.5).
6. **Merge** (squash or merge commit; history is never rewritten on shared branches).

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
- **Forked and modified code is not SOUP.** `third_party/gonetdicom` is a
  LibreDCM software item and is verified like own code.
- Vulnerability review of all SOUP: every CI run (govulncheck) and quarterly
  (full SBOM review against NVD/CISA KEV).

## 6. Verification and validation

- Unit and integration tests run in CI on every PR.
- System tests run against the tagged release build (container image digest
  recorded) and are written up in a signed test report.
- Validation (does the system meet user needs in real use?) is done at system
  level with clinical users, before any 510(k).
- Test data is synthetic. No real patient data in the repository.

## 7. Tools

Go toolchain (pinned in the build image), Docker, GitHub, GitHub Actions,
govulncheck, cyclonedx-gomod, DCMTK, Python 3 for `scripts/qms/`. Tools that
produce or check QMS records are validated for their intended use (ISO 13485
§4.1.6): a short validation note per tool in `docs/qms/tool-validation/`.

## 8. Records and signatures

Decided 2026-11-09 (Patrick Mulcahy): **GitHub + Google Drive split.** Design
records and QMS procedures live in this public repository; records that
aren't tied to code (training, supplier contracts, signed release and
design-review PDFs, regulatory correspondence, anything sensitive such as
penetration-test reports or complaints with patient data) live in Google
Drive (folder `LibreRWS/QMS`, owned by patrick@mulcahy.io until the
manufacturer, Libre Systems, has its own Workspace), indexed from `docs/qms/records-index.md`.

`master` is protected (pull request required, also for admins; no force
pushes or deletions; conversations resolved; the `verify` CI check must pass) and `v*` release tags cannot be
moved or deleted. Required approvals are 0 while there is a single
developer, because GitHub does not let an author approve their own pull
request; this becomes 1 when a second reviewer joins. Design records are kept in this repository. Approval is a GitHub PR
approval by an authenticated user, plus signed release tags. Before these
count as electronic signatures under 21 CFR Part 11, the setup (branch
protection, required reviews, signed commits, audit log retention) must be
validated and documented. Until then, release approvals are also signed on
the release record.

## 9. Open items before approval

- Name the roles: management representative, QA approver, independent reviewer.
- ~~Decide on docs-as-code vs an eQMS~~ decided 2026-11-09: GitHub + Drive (§8).
- ~~Enable branch protection~~ done 2026-11-09 (see §8). The `verify` CI check (`qms.yml`) is a required status check on `master` since 2026-11-09.
