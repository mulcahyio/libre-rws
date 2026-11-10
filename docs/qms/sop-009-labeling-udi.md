# SOP: Labeling and unique device identification (UDI)

| | |
|---|---|
| Document | QMS-SOP-009 |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude). UDI parts apply once a product is a regulated device; the version-identification parts apply now |
| Requirements | 21 CFR Part 801 (labeling), incl. §801.1, §801.5/§801.109 (directions for use; prescription devices), §801.20, §801.50 (stand-alone software); 21 CFR Part 830 (UDI system, GUDID); 21 CFR 820.45 (QMSR device labeling and packaging controls); ISO 13485 §7.5.1, §7.5.8; ISO 15223-1 (symbols); FD&C §524B (security labeling) |
| Related | QMS-SOP-002 (design outputs), `release-checklist.md`, `docs/dhf/<module>/intended-use.md` §8 |

Regulatory text of §801.50 checked against the eCFR (current to
2026-11-06) on 2026-11-10.

## 1. Purpose and scope

How labeling for Libre RWS products is defined, reviewed, approved and
released, and how products are identified (version now, UDI once
regulated). For software, labeling includes everything shipped with or
displayed by the product: version output, start-up and About text, the
admin `/version` response, installation guide, release notes, instructions
for use, DICOM Conformance Statement, security documentation (incl. MDS2),
licence texts and the download page.

## 2. Labeling content

| Item | Where | Requirement |
|---|---|---|
| Manufacturer name and place of business | IFU / installation guide, About text, release page | §801.1 (Libre Systems, address once formed) |
| Product name and version | `-version`, start-up log line, `/version`, IFU, release notes | Exact released version, matching the tag (DCM-SRS-057) |
| UDI | Start-up statement and `-version` / `/version` (no GUI in LibreDCM); About screen in Libre WT and the viewer | §801.50(b): readable plain-text UDI at start-up or via an About command; §801.50(a): the **production identifier conveys the version number** |
| Intended use, limitations, warnings | IFU / installation guide | From `intended-use.md` §2, §2.1, §8 |
| Prescription statement | System labeling (viewer) | "Rx only" (§801.109) when the system is a prescription device |
| Directions for use | Installation guide, IFU | Adequate for the intended users (administrators, integration engineers) |
| Security information | Security notes, `SECURITY.md`, MDS2, SBOM | §524B; FDA cybersecurity guidance labeling recommendations |
| DICOM Conformance Statement | `docs/dhf/<module>/` and the release | DICOM PS3.2 format |
| Non-device / beta / community-build statements | Release notes, README, start-up log | "Not for clinical use" for beta and community builds (`intended-use.md` §8) |
| "FDA cleared" claims | Anywhere | Only after clearance, never misleading (21 CFR 807.97) |

## 3. Process

1. **Define:** labeling content is a design output (QMS-SOP-002 §5);
   requirements that affect labeling are marked in the SRS (e.g.
   DCM-SRS-057) or in `intended-use.md`.
2. **Review and approve:** labeling changes go through a PR; the QA approver
   checks them against the intended use, the risk file (information for
   safety) and this SOP (820.45: inspected for accuracy before release).
3. **Verify at release** (release checklist): the built artifact reports
   the right version (and UDI) through `-version` and `/version`; release
   notes, installation guide, DICOM Conformance Statement and security notes
   are current; the record states which labeling versions were released.
4. **Archive:** released labeling is part of the release (tag, source
   tarball, GitHub release page).

## 4. UDI (once regulated)

1. **Issuing agency:** choose an FDA-accredited issuing agency (GS1, HIBCC or
   ICCBBA); GS1 is proposed (widely used; needs a company prefix, paid
   yearly). Decision for top management when Libre Systems is formed.
2. **Device identifier (DI):** one DI per product model (e.g. the Libre RWS
   PACS system and its sold configurations); a new DI when a change requires
   one under the issuing agency's rules and FDA's UDI guidance (for example
   a new model or a change that needs a new 510(k)). The decision is
   recorded in the PR of the change.
3. **Production identifier (PI):** the software version (`MAJOR.MINOR.PATCH`),
   per §801.50(a).
4. **Display:** human-readable UDI at start-up (log line and console where
   one exists) and through `-version`, `/version` and the About screens.
5. **GUDID:** submit the DI record before the device is placed in commercial
   distribution and keep it current (Part 830 Subpart E).
6. **Records:** the UDI is recorded in the release record, complaint records
   (820.35(a)(3)) and CAPA/field-action records (820.35(c)).

## 5. Today (pre-clearance)

- Version identification is in place: `LibreDCM -version` and `/version`
  report the build version set from the release tag (DCM-SRS-057, verified by
  `TestVersionInfo`).
- **Gap:** the start-up log line includes the version, but there is no UDI
  field yet and no "not for clinical use" statement at start-up. Add both
  (UDI empty until assigned) when the UID root and the company exist;
  tracked with the labeling items of the release checklist.
