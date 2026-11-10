# LibreDVT intended use and user needs

| | |
|---|---|
| Document | DHF-DVT-IU |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude from Patrick Mulcahy's request and decisions of 2026-11-09/10) |
| Related | `SRS.md`, `risk-management.md`, QMS-SOP-011 §2 step 1 (intended use of a QMS tool) |

## 1. Intended use

LibreDVT is a software tool used by Libre Systems developers and testers to
validate DICOM objects against the DICOM standard and to test the DICOM
services of an image archive (first LibreDCM, later any PACS) in a test
environment. Its results are used as verification records in the design
history file.

LibreDVT is **not** a medical device and not part of the LibreRWS product. It
is not used on patient data, for diagnosis, or to operate a clinical system.

## 2. Users

| ID | User | Context |
|---|---|---|
| T1 | Developer | Runs plans from CI and the command line while changing LibreDCM |
| T2 | Test engineer / release verifier | Runs plans and validations through the web UI, keeps reports as records |
| T3 | Integration engineer (later) | Tests a customer's PACS or modality interface in a lab before go-live |

## 3. User needs

| ID | User need (in users' words) | User | SRS requirements |
|---|---|---|---|
| UN-01 | Tell me whether a DICOM file follows the standard, and exactly what is wrong if not | T1, T2, T3 | DVT-SRS-001, -002, -003, -004, -005, -006, -009 |
| UN-02 | Tell me whether an archive really stores, finds and returns images unchanged | T1, T2, T3 | DVT-SRS-021, -022, -023, -024, -025, -026, -027, -029, -081 |
| UN-03 | Never let a broken tool say "pass" | T1, T2 | DVT-SRS-007, -042, -080 |
| UN-04 | Give me results I can file as verification evidence and reproduce later | T2 | DVT-SRS-008, -040, -041, -043, -044, -045, -064, -065 |
| UN-05 | Make it impossible to fill a clinical archive with test data by mistake | T2, T3 | DVT-SRS-020, -028 |
| UN-06 | Keep the tool and anything uploaded to it private | T2, T3 | DVT-SRS-060, -061, -062, -063, -066 |
