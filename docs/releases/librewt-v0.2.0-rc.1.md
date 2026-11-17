# Libre WT 0.2.0 release candidate 1 (not tagged)

**DRAFT for approval.** Written by Hermes (Claude), 2026-11-17. This is a
release candidate for review, not a release: no tag, no images published,
not for clinical use. Verification record:
[`librewt-v0.2.0-rc.1-verification/`](librewt-v0.2.0-rc.1-verification/README.md).
Merge order of the pull requests: [`librewt-v0.2.0-pr-stack.md`](librewt-v0.2.0-pr-stack.md).

## What is new since Libre WT 0.1

Libre WT now covers every LibreDCM function built to date (scope approved
by Patrick Mulcahy, 2026-11-14, issue #20):

- **Several LibreDCM nodes** in one console, grouped by cluster label, each
  with its own token. Fleet page, dashboard, status page, about page. Libre
  WT remembers each node's identity and blocks changes when another node
  answers.
- **Configuration**: running settings with source and restart flag, secrets
  hidden; C-MOVE destinations and the calling-AE allow-list; Admins change
  them, reload and roll back, always through LibreDCM's plan.
- **C-ECHO test** from a node.
- **Licence**: state for everyone; acknowledgement, activation request,
  licence file and online activation for Admins. The licence never stops
  DICOM services.
- **Index backup** with checksum.
- **Studies**: search, study and series pages (no images); **send** a study
  or some series to a configured destination (Admin), with the patient on
  the warning page.
- **LibreDCM audit records and logs**, CSV export.
- Every change: warning page, reason, single-use approval, operator name
  sent to LibreDCM, audit on both sides.

LibreDCM gained the control API v1 extension Libre WT uses
(`docs/dhf/libredcm/control-api-v1.md`, DCM-SRS-080…097).

## Verification in short

- Libre WT requirements: 37 of 37 active and verified (trace check).
- System test against real containers (two LibreDCM builds, Libre WT,
  DCMTK): 87 checks, 87 passed. Study and series sends received
  byte-identical by DCMTK `storescp` (with a tamper control).
- Screenshots of all 35 pages in light and dark checked; one layout defect
  found and fixed.
- Full gate (vet, race tests, official build, gosec, govulncheck, QMS
  scripts) in the record.

## Known limits

- HTTPS between Libre WT and LibreDCM, and for the console, is supported
  but was not exercised at system level.
- The installer scripts do not install Libre WT; INSTALL.md gives the
  manual steps (checked on Linux; Windows not run).
- No second factor, no OIDC/LDAP; LibreDCM sees Libre WT's shared admin
  token plus the operator name Libre WT asserts.
- The system test runs locally, not in CI (D59).

## Upgrade from Libre WT 0.1

The single `dcm:` setting keeps working as the instance `default`;
addresses of 0.1 (`/exceptions`) lead to the same pages. The data file
`librewt.db` is upgraded in place; back it up first. See
`modules/wt/README.md` ("Upgrade from Libre WT 0.1").

## Open for Patrick

Decisions D1…D62 (`docs/dhf/librewt/architecture.md` §8), the risk
management report (`docs/dhf/librewt/risk-management.md`), and the version
and tag (D60: proposal `v0.3.0-beta.1` for the repository).
