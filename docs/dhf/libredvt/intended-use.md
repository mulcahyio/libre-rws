# LibreDVT intended use and user needs

| | |
|---|---|
| Document | DHF-DVT-IU |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude from Patrick Mulcahy's request and decisions of 2026-11-09/10; v0.2 additions 2026-11-19 from issue #34, decisions D1–D5) |
| Related | `SRS.md`, `risk-management.md`, QMS-SOP-011 §2 step 1 (intended use of a QMS tool) |

## 1. Intended use

LibreDVT is a software tool used by Libre Systems developers and testers to
validate DICOM objects against the DICOM standard and to test the DICOM
services of an image archive (first LibreDCM, later any PACS) in a test
environment. Its results are used as verification records in the design
history file.

From v0.2 (issue #34, decision D4 of 2026-11-19, **DRAFT for approval**),
LibreDVT is also used by Mulcahy.io interoperability auditors to test other
vendors' DICOM and DICOMweb systems for clients: association negotiation,
DICOM TLS, and DICOMweb QIDO-RS, WADO-RS and STOW-RS. These audits run
against the client's lab or test systems. On a client's production system
LibreDVT only runs connection-level checks (C-ECHO, association, rejection
and TLS checks, C-FIND as in v0.1); it never stores to it or retrieves image
data from it (DVT-SRS-028, -134). Audit reports are evidence for the client,
not a certification.

LibreDVT is **not** a medical device and not part of the LibreRWS product. It
is not used on patient data, for diagnosis, or to operate a clinical system.

## 2. Users

| ID | User | Context |
|---|---|---|
| T1 | Developer | Runs plans from CI and the command line while changing LibreDCM |
| T2 | Test engineer / release verifier | Runs plans and validations through the web UI, keeps reports as records |
| T3 | Integration engineer (later) | Tests a customer's PACS or modality interface in a lab before go-live |
| T4 | Interoperability auditor (Mulcahy.io, from v0.2) | Tests a client's or another vendor's DICOM / DICOMweb lab system and hands the report to the client as audit and cybersecurity evidence |

## 3. User needs

| ID | User need (in users' words) | User | SRS requirements |
|---|---|---|---|
| UN-01 | Tell me whether a DICOM file follows the standard, and exactly what is wrong if not | T1, T2, T3 | DVT-SRS-001, -002, -003, -004, -005, -006, -009 |
| UN-02 | Tell me whether an archive really stores, finds and returns images unchanged | T1, T2, T3 | DVT-SRS-021, -022, -023, -024, -025, -026, -027, -029, -081, -156 |
| UN-03 | Never let a broken tool say "pass" | T1, T2, T4 | DVT-SRS-007, -042, -080, -144, -150, -154 |
| UN-04 | Give me results I can file as verification evidence and reproduce later | T2, T4 | DVT-SRS-008, -040, -041, -043, -044, -045, -064, -065, -082, -140, -142, -143, -145, -146 |
| UN-05 | Make it impossible to fill a clinical archive with test data by mistake | T2, T3, T4 | DVT-SRS-020, -028, -110, -134, -151 |
| UN-06 | Keep the tool and anything uploaded to it private | T2, T3 | DVT-SRS-060, -061, -062, -063, -066 |
| UN-07 | Show me exactly how a system negotiates DICOM associations: what it accepts, what it refuses, and why | T1, T3, T4 | DVT-SRS-090, -091, -092, -093, -094, -095, -096 |
| UN-08 | Prove that a system's DICOM and DICOMweb traffic is protected by TLS that really verifies, and that it refuses weaker connections | T1, T4 | DVT-SRS-100, -101, -102, -103, -104, -105, -106, -107, -108 |
| UN-09 | Tell me whether a DICOMweb server finds, returns and stores images correctly | T3, T4 | DVT-SRS-111, -113, -114, -115, -116, -117, -118, -119, -120, -121, -122, -123, -124, -125, -126, -127, -128, -129, -130, -131, -132, -133, -135, -136 |
| UN-10 | Never put a client's credentials or keys into my reports or logs | T4 | DVT-SRS-111, -112, -141, -152, -153 |
| UN-11 | Do not overload or change a client's system while I audit it | T4 | DVT-SRS-134, -151, -154, -155 |
