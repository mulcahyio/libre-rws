# LibreDVT manual and system-level verification

Requirements verified outside `go test`. Format: `- <ID>: <how>`. Read by
`scripts/qms/trace.py --module libredvt --prefix DVT`.

Runs below were done on 2026-11-10 by Hermes/Claude on branch
`feat/libredvt-v0.1` (image `libre/dvt:0.1.0-dev`, LibreDCM image built from
the same branch). Logs and screenshots: vault note *LibreDVT*.

- DVT-SRS-081: `libredvt run` of `modules/dvt/plans/libredcm-conformance.yaml` against a LibreDCM container (6 fixtures: Explicit VR LE, RLE, JPEG Baseline, JPEG Lossless, JPEG-LS, JPEG 2000): echo, called-AE rejection, store, find and move with pixel comparison all passed, exit 0; `scripts/conformance.sh` (DCMTK 3.6.7) on the same fixtures and LibreDCM also passed (2026-11-10)
- DVT-SRS-064: the image ran `validate` and `iod` with `--network none` using the built-in 2026d standard (2026-11-10)
- DVT-SRS-060: web UI exercised over HTTP against the running image: login, add `lab` and `production` targets, run the bundled plan (pass on the lab target; store/move refused and run failed on the production target), upload and validate a file (fail with dciodvfy findings), report downloads (2026-11-10)
