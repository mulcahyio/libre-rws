# LibreDVT 0.2.0 release candidate 1 (not tagged)

**DRAFT for approval.** Written by Hermes (Claude), 2026-11-23. This is a
release candidate for review, not a release: no tag, no images published.
LibreDVT is a quality-system tool (QMS-SOP-011, high risk), not a medical
device. Verification record:
[`docs/dhf/libredvt/evidence/v0.2-rc/`](../dhf/libredvt/evidence/v0.2-rc/README.md).
Merge order of the pull requests: [`libredvt-v0.2.0-pr-stack.md`](libredvt-v0.2.0-pr-stack.md).
Sample audit report: [`libredvt-v0.2.0-rc.1-sample-audit/`](libredvt-v0.2.0-rc.1-sample-audit/README.md).

## What is new since LibreDVT 0.1

Scope approved by Patrick Mulcahy on 2026-11-19 (issue #34, decisions
D1–D5):

- **Association negotiation tests** (`associate`): which presentation
  contexts and transfer syntaxes a system accepts or rejects, rejection
  reasons, roles, maximum PDU, Implementation Class UID and Version Name,
  release and abort behaviour.
- **DICOM TLS**: every DIMSE step and the built-in Storage SCP can use TLS
  1.2 or later with certificate-chain and host-name checks, an own CA file
  and optional client certificates. There is no switch to turn
  verification off. **Refusal checks** (`tls_reject`): plaintext, TLS 1.0,
  TLS 1.1 and missing client certificates must be refused.
- **DICOMweb**: QIDO-RS search (`qido`), WADO-RS retrieve with strict
  multipart parsing and byte-for-byte pixel comparison (`wado`), STOW-RS
  store with per-instance accounting (`stow`, lab targets only), bearer
  tokens from files, never in a report.
- **Targets** for DIMSE and DICOMweb in the web UI and in target files
  (`libredvt run -targets FILE`), one schema, secrets as file references.
- **Reports** (HTML, JUnit, JSON) with the endpoint identity (TLS,
  certificate, implementation) and every exchange and assertion. The HTML
  report prints cleanly to PDF.
- **Bundled plans**: *LibreDCM smoke (DIMSE + TLS)*, *Generic DICOM TLS
  audit*, *Generic DICOMweb audit*.
- **User guide** for client audits: lab targets only, written consent,
  what a report proves and does not prove (`modules/dvt/README.md`).

v0.1 behaviour and plans are unchanged.

## Verification in short

- Requirements: 58 v0.2 rows (DVT-SRS-090…157) set `Active`; every one has
  tagged tests and every tagged test passed in the real-engine test run
  (122 distinct tests, 0 failed). Trace check: 93 of 93 active
  requirements verified.
- Mutation checks of all v0.2 cards re-run on the release-candidate
  commit: 121 of 121 applicable mutants killed.
- Release-candidate image against real systems: LibreDCM in TLS mode (smoke
  5/5, DICOM TLS audit 5/5), LibreDCM plain with the v0.1 plan (5/5),
  Orthanc 1.13.1 + DICOMweb 1.24 over HTTPS with a bearer token (8/8).
- Independent cross-check of the DICOM TLS audit against DCMTK 3.6.7: 26 of
  26 checks as expected, including three set-ups where the audit must find
  problems (no TLS, old TLS versions accepted, certificate for another host).
- No synthetic token or private-key line in any of the 56 artefacts.
- Full local gate and CI: see the pull request and
  `evidence/v0.2-rc/05-local-gate.log`.

## Known limits

- No certificate revocation check (CRL/OCSP).
- Not a vulnerability scanner or penetration test; cipher-suite lists
  beyond what LibreDVT's own client offers are not tested.
- DICOMweb: the resources, media types and transfer syntaxes listed in the
  README only; no OAuth or basic auth (D2).
- No Storage Commitment, MPPS, MWL or emulators (D1, D5).
- `echo`, `associate`, `tls_reject` and C-FIND run against production-class
  targets (open decision in issue #34, v0.1 behaviour kept).

## Upgrade from LibreDVT 0.1

No action needed. Existing plans, targets, runs and reports load and run as
before. New target fields (`protocol`, `tls`, `base_url`, `auth`) are
optional for DIMSE targets; a target without `protocol` is DIMSE.
