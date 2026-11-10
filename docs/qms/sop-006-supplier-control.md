# SOP: Supplier control and approved supplier list

| | |
|---|---|
| Document | QMS-SOP-006 |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Requirements | ISO 13485:2016 §4.1.5 (outsourced processes), §7.4.1–§7.4.3 (purchasing) via 21 CFR 820.10; FDA *Off-The-Shelf Software Use in Medical Devices* (2023); FD&C §524B (SBOM) |
| Related | SDP §5 (SOUP), QMS-SOP-010 (vulnerabilities), QMS-SOP-011 (tool validation), QMS-SOP-001 (records) |
| Records | Approved supplier list (§6, this document); evaluation records and contracts/terms in Drive `LibreRWS/QMS/02 Suppliers` |

## 1. Purpose and scope

How Libre Systems selects, evaluates, approves, monitors and re-evaluates
suppliers whose products or services affect product quality, safety,
security or QMS records. Covers:

- **Service suppliers:** code hosting and CI, container registries, cloud
  storage of records, AI coding tools, (later) contract developers,
  consultants, penetration testers, test labs.
- **Software components (SOUP / open-source):** controlled through the SOUP
  process in SDP §5 (each component evaluated in `soup.md`); the *project*
  behind a critical component is listed here as a supplier.
- **Infrastructure the customer runs** (Docker, PostgreSQL, NATS, HAProxy,
  base images) is system-level SOUP, named in the architecture document and
  labeling, and evaluated like SOUP.

## 2. Responsibilities

| Role | Responsibility |
|---|---|
| Requester (developer) | Proposes a new supplier or component, with the purpose and risk |
| Management representative | Evaluates suppliers, keeps the approved supplier list, runs re-evaluations |
| QA approver | Approves suppliers and changes to the list |

## 3. Classification

| Class | Meaning | Examples | Controls |
|---|---|---|---|
| **Critical** | Failure can directly affect the released product, its integrity or QMS records | GitHub (source, CI, releases), container base-image registries, Google Workspace (Drive records), the gonetdicom/godicom projects | Documented evaluation; written terms/contract reviewed; security posture checked (2FA, SOC 2 or equivalent where available); re-evaluated yearly and on any incident |
| **Major** | Affects development efficiency or quality, with detection by other controls | AI coding tools (all output human-reviewed and tested), Go toolchain, scanners, DCMTK | Evaluation; tool validation where it produces or checks records (QMS-SOP-011); re-evaluated every 2 years |
| **Minor** | No effect on product or records | Office tools, communication | Listed only |

## 4. Selection and evaluation (§7.4.1)

Criteria, weighted by class: ability to meet the requirement; quality and
security practices (certifications such as SOC 2 / ISO 27001, 2FA,
incident history, vulnerability handling); for open-source projects:
licence compatibility with Apache-2.0, maintenance activity, release
practice, security advisories, bus factor; continuity risk and exit plan;
data protection (no patient data is given to any supplier; Libre Systems
holds none today). The evaluation is recorded with a date, the evaluator and
the decision.

## 5. Purchasing information and verification (§7.4.2, §7.4.3)

- **Services:** the terms of service or contract are kept; where a supplier
  must notify changes that affect quality (e.g. a contract developer, a test
  lab), the agreement says so. Free services cannot be negotiated; their
  public terms and status pages are monitored instead.
- **Verification of purchased product:** SOUP is pinned (Go modules by
  version and checksum in `go.sum`; base images by digest) and verified by
  CI (tests, govulncheck, SBOM); AI-generated code and documents are
  verified by human review and the CI gates; services are verified by the
  validation checks of the systems that use them (e.g. VAL-0001).
- **Outsourced processes** (§4.1.5): none today. A future contract developer
  or consultant works under these procedures, and their work enters through
  pull requests like internal work.

## 6. Approved supplier list

Evaluated 2026-11-10 (initial list, by Hermes/Claude for approval). Next
re-evaluation: at the first management review.

| Supplier | Product / service | Class | Use | Evaluation summary | Status |
|---|---|---|---|---|---|
| GitHub, Inc. (Microsoft) | Repository hosting, pull requests, GitHub Actions CI, releases, private vulnerability reporting, secret scanning | Critical | Design history file, change control, CI verification records, distribution | Industry standard; GitHub publishes SOC 2 reports (to enterprise customers) and requires 2FA for accounts that contribute code (confirmation for the Libre accounts: VAL-0001 F2); branch protection and rulesets validated (VAL-0001). Risk: free personal account, account loss → mitigated by offline mirror (SOP-001 §7.3). Exit: Git is portable | Approved (conditional on F1/F2 of VAL-0001) |
| Google LLC (Google Workspace / Drive) | Storage of non-code and signed records | Critical | QMS records outside Git | Version history and sharing controls verified (VAL-0001). Risk: account split across two Workspaces (F3) | Approved |
| Docker Inc. (Docker Hub) and Google (gcr.io distroless) | Base images `golang` (build) and `gcr.io/distroless/base-debian12` (runtime) | Critical | Build and runtime image | Images pinned by digest in `Dockerfile.dicom`; official / Google-maintained images; scanned by govulncheck (Go) and planned Trivy (OS packages) | Approved |
| The Go project (Google) | Go toolchain and standard library | Major | Compiler, runtime | Pinned version (≥ 1.26.10); security releases monitored via govulncheck | Approved |
| godicom-dev (open-source project) | godicom (SOUP), gonetdicom (forked) | Critical | DICOM parsing and networking | MIT; pre-1.0, small team. gonetdicom is forked and maintained as a LibreDCM item; godicom: watch releases, fork if needed (`soup.md`) | Approved with monitoring |
| Other SOUP projects (pgx, minio-go, nats.go, prometheus client, modernc sqlite, x/sync, yaml) | Libraries | Major | See `soup.md` | Evaluated per component in `soup.md`; archived yaml.v3 replaced by go.yaml.in/yaml/v3 (2026-11-10) | Approved per `soup.md` |
| Anthropic, PBC | Claude models used through Hermes and Claude Code as AI coding/documentation tools | Major | Drafting code, tests and documents | Output is treated as tool output: human review, CI gates and tests verify it (SDP §4.1). No patient data or secrets are given to it. Data handling per Anthropic's commercial terms | Approved |
| Nous Research (Hermes agent, open source) | Agent framework running the AI tools locally | Minor | Orchestration of AI tool sessions | No effect on product beyond the AI tool output, which is reviewed | Listed |
| DCMTK (OFFIS, open source) | DICOM test tools (storescu, findscu, movescu, echoscu) | Major | System/conformance testing | Widely used reference implementation; tool validation note (QMS-SOP-011) | Approved |
| securego (gosec), Go team (govulncheck), CycloneDX (cyclonedx-gomod) | Scanners and SBOM generator | Major | CI verification records | Pinned versions in CI; tool validation notes | Approved |

## 7. Monitoring and re-evaluation

- Critical suppliers: yearly, and after any incident affecting Libre Systems
  (outage, security breach, terms change). Major: every 2 years.
- Inputs: incidents, status pages, security advisories, SOUP vulnerability
  reports, CI failures attributable to the supplier, licence changes.
- A supplier problem that affected product or records opens a CAPA
  (QMS-SOP-004). Removing a supplier from the list needs an exit plan
  (e.g. migrate records, replace a component).
