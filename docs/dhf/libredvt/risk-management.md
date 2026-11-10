# LibreDVT risk analysis (tool risk)

| | |
|---|---|
| Document | DHF-DVT-RISK |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Procedure | QMS-SOP-011 (risk-based tool validation); QMS-SOP-003 used for the structure only |
| Related | `SRS.md`, `architecture.md`, `docs/qms/tool-validation/README.md` (LibreDVT section) |

## 1. Scope and classification

LibreDVT is a test tool, not a medical device: it never handles images for
diagnosis and is not shipped to hospitals as part of the LibreRWS PACS. Its
risk is the risk to the **quality system**: LibreDVT results will be used as
verification records for LibreDCM releases and requirements.

**Tool risk: High** (QMS-SOP-011 §2). A silent false pass could release a
defective LibreDCM build with no other control catching it, because the
release verification relies on these results. Consequence: scripted tests
with positive controls, kept under version control and re-run on every
change of LibreDVT or its engines (§4).

## 2. Tool hazards

| ID | Hazard | Cause | Consequence | Controls (requirements) |
|---|---|---|---|---|
| DVT-HAZ-001 | **False pass**: LibreDVT reports pass for a defective object or service | Engine not run, crashed or timed out and the result was read as "no findings"; parser misses a finding; network step reads a failure status as success; step skipped; tester shares a bug with LibreDCM (same library) | A defective LibreDCM build or non-conformant object is accepted as verified | DVT-SRS-002/003/004, -006, -007, -009, -021…-025, -027, -029, -041, -042, -080; positive controls in the tool validation (§4) |
| DVT-HAZ-002 | **Destructive test against a production system**: test data stored into, or moves triggered on, a clinical PACS | Wrong target selected; target registered without a class | Test images mixed with patient studies in a clinical archive; load on a clinical system | DVT-SRS-020 (missing class = production), DVT-SRS-028 (store/move only against `lab`) |
| DVT-HAZ-003 | **Exposure of patient data** | Real images uploaded despite the policy; web UI reachable from the network without login; reports with PHI shared | Privacy breach | DVT-SRS-061, -062, -063 (localhost by default), -045 (delete), -066 (warning); policy: synthetic data only (SDP §4.1 rule 7) |
| DVT-HAZ-004 | **Wrong result attribution**: a finding attached to the wrong file or step, or a plan run differently from what was written | Output parsing errors; lenient plan parsing ignores a typo (e.g. `verfies:`) | A record says a requirement was verified when it was not | DVT-SRS-005 (raw output kept), DVT-SRS-040 (strict plan parsing), parser unit tests |
| DVT-HAZ-005 | **Unreproducible or stale result**: the record does not say which tool versions and standard edition produced it, or the standard changes between runs | Floating versions; runtime download of the standard | A record cannot be reproduced or audited | DVT-SRS-008, -044, -064, -065 |

## 3. Residual risk

With the controls verified, residual risk is acceptable for a test tool:
DVT-HAZ-001 is further reduced because LibreDCM's own Go end-to-end tests and
the DCMTK conformance script remain as independent checks during v0.1, and
DVT-HAZ-002/003 depend partly on user discipline (target classification,
synthetic data), which is stated in the user documentation.

## 4. Assurance activities (tool validation, high risk)

Scripted, version-controlled, re-run on every change of `modules/dvt/` or the
LibreDVT image:

1. **Positive controls for validation:** a set of deliberately broken
   fixture files (missing Type 1 attribute, bad enumerated value, wrong VR
   length, two files with conflicting Patient IDs for one study, a non-DICOM
   file) must each produce a fail with the expected finding; a known-good
   file must not produce an error.
2. **Engine failure injection:** a missing engine binary, an engine that
   exits abnormally, an engine that hangs past the timeout and unparseable
   output must each give "engine error" and a failed verdict.
3. **Positive controls for services:** a target that is down, a target
   that rejects the association, a C-STORE refused by the SCP, a C-MOVE that
   returns fewer instances than expected, and a retrieved instance with
   altered pixel data must each fail the step.
4. **Production guard:** store and move steps against a `production` or
   unclassified target must be refused.
5. **End-to-end:** the LibreDCM plan passes against a running LibreDCM, and
   its results agree with `scripts/conformance.sh` (DCMTK) on the same data.

Results go in the LibreDVT section of `docs/qms/tool-validation/README.md`.
