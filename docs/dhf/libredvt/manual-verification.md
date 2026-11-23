# LibreDVT manual and system-level verification

Requirements verified outside `go test`. Format: `- <ID>: <how>`. Read by
`scripts/qms/trace.py --module libredvt --prefix DVT`.

Runs below were done on 2026-11-10 by Hermes/Claude on branch
`feat/libredvt-v0.1` (image `libre/dvt:0.1.0-dev`, LibreDCM image built from
the same branch). Logs and screenshots: vault note *LibreDVT*.

- DVT-SRS-081: `libredvt run` of `modules/dvt/plans/libredcm-conformance.yaml` against a LibreDCM container (6 fixtures: Explicit VR LE, RLE, JPEG Baseline, JPEG Lossless, JPEG-LS, JPEG 2000): echo, called-AE rejection, store, find and move with pixel comparison all passed, exit 0; `scripts/conformance.sh` (DCMTK 3.6.7) on the same fixtures and LibreDCM also passed (2026-11-10)
- DVT-SRS-064: the image ran `validate` and `iod` with `--network none` using the built-in 2026d standard (2026-11-10)
- DVT-SRS-060: web UI exercised over HTTP against the running image: login, add `lab` and `production` targets, run the bundled plan (pass on the lab target; store/move refused and run failed on the production target), upload and validate a file (fail with dciodvfy findings), report downloads (2026-11-10)

Runs below were done on 2026-11-22 by Hermes/Claude on branch
`feat/libredvt-0.2-ui` (card DVT-0.2/5; image `libre/dvt:v02-ui`, LibreDCM
image `libre/dcm:v02-foundation`, Orthanc `orthancteam/orthanc:26.11.1`
digest-pinned). Logs, reports and screenshots:
`docs/dhf/libredvt/evidence/v0.2-ui/`. DRAFT for approval.

- DVT-SRS-146: through the web UI (curl with the page's CSRF token): a DIMSE TLS target (`libredcm-tls-lab`), a DICOMweb target with bearer token file and CA file (`dicomweb-lab`) and a production DIMSE target saved through the new forms; a token file outside `secrets_dir` refused with the field named; the three bundled plans run from the plans page (2026-11-22)
- DVT-SRS-156: *LibreDCM smoke (DIMSE + TLS)* passed against LibreDCM in TLS mode (5 of 5 steps), *Generic DICOM TLS audit* passed against the same LibreDCM (5 of 5), *Generic DICOMweb audit* passed against Orthanc + DICOMweb over HTTPS (8 of 8); the synthetic token in no downloaded artefact and no file of the data directory (2026-11-22). The DCMTK `+tls` comparison of the DICOM TLS audit is part of card DVT-0.2/6
- DVT-SRS-145: the run pages of those three runs (HTML report body) and the downloaded HTML/JUnit/JSON files show the target protocol, endpoint and class, the endpoint identity (TLS 1.3, cipher, LibreDCM's and Orthanc's certificate subject, issuer, SAN, validity, SHA-256, LibreDCM's Implementation Class UID) and per-instance STOW/WADO results; screenshots in light and dark checked by eye (2026-11-22)
