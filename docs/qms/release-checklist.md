# Release checklist (design transfer)

Copy this list into the release record `docs/releases/<version>-record.md`
and tick each item. A release without a complete record is not a release.

## Before tagging
- [ ] All PRs in the milestone merged; each has a linked issue and a filled-in template
- [ ] SRS, architecture, risk file, cybersecurity file and SOUP list current
- [ ] `python3 scripts/qms/trace.py --check`: every non-retired requirement verified by at least one test (or a documented manual test)
- [ ] `govulncheck ./...`: no unassessed findings; each finding documented with exploitability and patient-safety impact
- [ ] SBOM generated for the release build (`sbom.cdx.json`)
- [ ] Unresolved anomaly list reviewed; each open anomaly has a safety impact assessment and none is unacceptable
- [ ] Base image pinned by digest; Go toolchain version recorded

## Verification of the exact build
- [ ] Container image built from the tag; image digest recorded
- [ ] `make vet test` and `make test-postgres` (and S3 suite) pass on the tag; logs archived
- [ ] System tests: DCMTK conformance and LibreDVT conformance plan, both passing (incl. JPEG, JPEG 2000, RLE, JPEG-LS); LibreDVT engine positive controls pass, cluster failover, NATS outage, installer upgrade from previous release (Linux and Windows)
- [ ] Signed test report written

## Review and release
- [ ] Design review held with at least one reviewer independent of the work; minutes recorded
- [ ] Release notes, CHANGELOG, INSTALL, DICOM Conformance Statement and security notes updated
- [ ] Labeling: version and (after clearance) UDI shown by `-version` and the admin `/version` endpoint
- [ ] Signed tag pushed; GitHub release with SBOM, checksums and release record attached
- [ ] Release record approved (signature) by QA

## After release
- [ ] Artifacts archived (image digest, binaries, SBOM, record) as the device master record
- [ ] Vault project note updated with a handoff
