# Libre RWS quality system and design files

Libre RWS is developed as a future FDA-regulated medical device: the Libre RWS
PACS (21 CFR 892.2050, product code LLZ, Class II), with LibreDCM as its
archive component. LibreDCM on its own is a non-device software function
(FD&C Act §520(o)(1)(D)), but it is developed under this quality system so
its design file can support the system's 510(k).

> **Status: DRAFT.** Nothing here is approved yet. The QMS takes effect at
> the declared baseline release once the roles in `SDP.md` §9 are filled.

## Company procedures (`docs/qms/`)

| Document | Status |
|---|---|
| [SDP.md](SDP.md): software development plan, change workflow, rules for AI agents | Draft |
| [release-checklist.md](release-checklist.md): release / design transfer | Draft |
| Quality manual, SOPs (document control, CAPA, complaints/MDR, supplier control, training, audits, management review, UDI/labeling) | Not started |
| `tool-validation/` | Not started |

## Design files (`docs/dhf/<module>/`)

### LibreDCM (`docs/dhf/libredcm/`)

| Document | Status |
|---|---|
| [gap-assessment.md](../dhf/libredcm/gap-assessment.md) | Done 2026-11-09 |
| [SRS.md](../dhf/libredcm/SRS.md) | Retrospective draft |
| [risk-management.md](../dhf/libredcm/risk-management.md) | Initial hazard list |
| [cybersecurity.md](../dhf/libredcm/cybersecurity.md) | Seed threat model + baseline scan |
| [soup.md](../dhf/libredcm/soup.md) | Generated, draft evaluation |
| [manual-verification.md](../dhf/libredcm/manual-verification.md) | Pre-QMS evidence only |
| [generated/trace-matrix.md](../dhf/libredcm/generated/trace-matrix.md) | Generated |
| [generated/sbom.cdx.json](../dhf/libredcm/generated/sbom.cdx.json) | Generated |
| [intended-use.md](../dhf/libredcm/intended-use.md): intended use, limitations, users, use specification, user needs | Draft for approval (2026-11-10) |
| [anomalies.md](../dhf/libredcm/anomalies.md): known anomalies with safety impact | Draft, living list (2026-11-10) |
| [architecture.md](../dhf/libredcm/architecture.md): software items, interfaces, data flows, SOUP, topologies, segregation | Draft (2026-11-10), verified against the import graph |
| design/, DICOM Conformance Statement | Not started |

### Libre WT, future modules

Not started. Each module gets its own `docs/dhf/<module>/` with the same
documents and its own requirement prefix.

## Tools

```sh
python3 scripts/qms/trace.py --write     # trace matrix; --check fails on gaps
python3 scripts/qms/needs.py             # every requirement serves a user need
python3 scripts/qms/soup.py              # SOUP list from go.mod + SBOM
# SBOM and vulnerability scan (dev machine has no host Go; rws-go runs Go in Docker)
rws-go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.12.0 mod -licenses -json -output docs/dhf/libredcm/generated/sbom.cdx.json .
rws-go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

CI (`.github/workflows/qms.yml`) runs vet, race tests, govulncheck, the SBOM
and the trace check on every pull request.
