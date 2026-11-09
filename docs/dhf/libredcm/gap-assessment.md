# LibreDCM retrospective gap assessment

| | |
|---|---|
| Document | DHF-DCM-GAP |
| Date | 2026-11-09 |
| Baseline assessed | branch `dev/0.2.0` (PR #3), commit `3b44b86` |
| Method | IEC 62304 Amd 1 §4.4 (legacy software) style gap analysis against IEC 62304, ISO 14971, the QMSR and the FDA premarket software and cybersecurity guidance |

LibreDCM has not been used clinically, so there is no post-production
feedback to evaluate. The plan is to bring it under the QMS at a declared
baseline release (proposed `v0.3.0`) and treat everything before it as
pre-QMS history.

| Area | Expected evidence | Current state | Gap | Closure |
|---|---|---|---|---|
| Intended use / user needs | Approved intended use, user needs, use specification | Product notes in the vault; no approved statement | High | Decision D1, then `intended-use.md` |
| Software development plan | Approved SDP | Draft `docs/qms/SDP.md` (this branch) | Medium | Approve at baseline |
| Requirements (SRS) | Approved, testable, IDs, traced | Retrospective draft `SRS.md`: 38 requirements (35 active) | Medium | Review against code, approve |
| Architecture | Architecture doc with SOUP and interfaces | `docs/DEVELOPMENT.md`, vault design notes | Medium | Write `architecture.md` from these |
| Detailed design | Unit-level design (needed for Enhanced level) | Code comments, fork notes | Medium | `design/` notes for gateway, index, outbox, storage |
| Risk management | Plan, analysis, controls, report | Initial hazard list `risk-management.md` (12 hazards) | High | Score, trace, report |
| SOUP | List + evaluation | `soup.md` generated, 10 direct items evaluated in draft | Low | Review; fix yaml.v3 (archived upstream) |
| Traceability | Requirement ↔ test ↔ risk control | 31 of 35 active requirements traced to tests (31 test functions tagged) | Medium | Tests for DCM-SRS-043, -053, -054, -057 |
| Verification records | Signed reports against tagged builds | CI-less; pre-QMS test runs recorded in vault and CHANGELOG | High | CI (this branch) + signed system test report at baseline |
| Configuration management | Controlled branches, tags, reproducible builds | Git, tags; no branch protection; base image not pinned | Medium | Branch protection, digest pinning |
| Problem resolution | Anomaly process and list | GitHub issues, no templates | Low | Templates (this branch), anomaly list per release |
| Cybersecurity | Threat model, SBOM, testing, CVD | Seed `cybersecurity.md`, SBOM generated, `SECURITY.md` draft; 1 reachable stdlib vulnerability | High | See cybersecurity.md §5; build with Go ≥ 1.26.10 now |
| Labeling | IFU, DICOM Conformance Statement, security notes | INSTALL.md only | High | DICOM Conformance Statement (PS3.2), IFU, MDS2 |
| Usability | IEC 62366-1 file | None (no UI in LibreDCM; installer and Libre WT are user interfaces) | Medium | Use specification; installer and Libre WT usability |
| Release process | Release checklist and records | CHANGELOG, release notes | Medium | `docs/qms/release-checklist.md` (this branch) |
| QMS processes | Quality manual, SOPs, training, CAPA, audits, management review | None | High | Phase 1 of the compliance plan |
| Independent review | Reviewer independent of the design | None (single developer + AI tools) | High | Decision D5 (consultant) |

## Safety-relevant findings to act on now

1. **HAZ-010 licence lock-out:** official builds refuse C-STORE after licence
   expiry plus grace. Decide before any clinical use (decision D7).
2. **GO-2026-6809** in the Go standard library is reachable: rebuild with Go
   1.26.10 or later.
3. **No storage commitment** (IHE RAD-10): modalities cannot confirm that
   LibreDCM took responsibility for images before deleting them (HAZ-001).
4. **No patient update/merge handling** (IHE PIR): demographics can become
   stale (HAZ-011). Labeling must say so until it is implemented.
