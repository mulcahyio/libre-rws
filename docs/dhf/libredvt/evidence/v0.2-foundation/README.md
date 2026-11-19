# LibreDVT v0.2 foundation: verification evidence (card DVT-0.2/2)

**DRAFT for approval.** Written 2026-11-19 by Hermes/Claude for issue #34.
Change: protocol-specific targets with shared TLS and secret configuration,
strict target and plan validation, production guard extended to STOW-RS
(and QIDO-RS/WADO-RS), protocol-neutral result model with redaction.
Requirements: DVT-SRS-110, -112, -134, -140…-144, -152, -153 (all still
`Draft`; card DVT-0.2/6 makes them `Active`).

All output below is real; the logs name the commit they ran on.

## 1. Mutation check — `01-mutation-check.log`

`scripts/mutation-check.sh` breaks one control at a time (33 mutants, each
still compiles) and runs the package's tests. Result: **33 of 33 killed**,
source restored clean. Covered: verdict rule (zero assertions, problems
ignored, failed assertion passing, warning flipped, status not from the
verdict, run dropping warnings, evidence check off), strict plan parsing
(unknown fields, fields of another step type, protocol check, limits,
QIDO expectation rule, lossy pixel comparison), production guard back to
v0.1 and `stow` not lab-only, protocol mismatch running steps, secret-file
preflight off, DIMSE-over-TLS falling back to clear text, file checks
(regular, blank, secrets directory), strict registry and target-file
decoding, DIMSE fields on DICOMweb targets, bearer over http to any host,
user-info in base URLs, each redaction layer, the token's base64 forms,
the scanner itself, the artefact scan, and the argv and environment guards.

Notes:
- A first run had one survivor, "drop the padded base64 form": the
  unpadded form is a prefix of the padded one and still redacts it, so the
  mutant was equivalent. The mutant now removes every base64 form and is
  killed. A second first-run mutant did not compile (unused variable) and
  was rewritten; the script reports such mutants as `BUILD-FAILED`, never
  as killed.
- Switching off only the CLI's or web UI's artefact scan is not caught by
  an end-to-end test, because the runner already redacted the run: the
  layers back each other up. `report.Render`'s own test kills the mutant
  that switches off both of its layers (M31).

## 2. Real-engine tests — `02-engine-tests.log`

`docker build -f deploy/docker/Dockerfile.dvt --target test` (as CI job
`libredvt` and `release-verify.sh`): exit 0, 9 packages `ok`, 0 FAIL. It
includes the v0.1 positive controls unchanged (`TestEnginePositiveControls*`,
`TestNetworkPositiveControls`, `TestNetworkTargetDownAndProductionGuard`,
`TestWorkerSendsLibreDVTIdentification`) and every new test.

## 3. v0.1 behaviour unchanged — `03-e2e-v01-unchanged.log`

`scripts/e2e-v01-unchanged.sh` ran the bundled LibreDCM plan
(`modules/dvt/plans/libredcm-conformance.yaml`, unchanged) against a fresh
LibreDCM container three times:

| Run | Image | Target | Verdict |
|---|---|---|---|
| `v01-base` | parent branch `feat/libredvt-0.2-design` (v0.1 code) | `-host … -lab` | pass, 5 of 5 steps |
| `v02-adhoc` | this branch | `-host … -lab` | pass, 5 of 5 steps |
| `v02-targets` | this branch | `-targets targets.yaml` (v0.2 target file, `dimse`, `lab`) | pass, 5 of 5 steps |

`scripts/compare-runs.py` found the step list, every status, every message
and the verdict **identical** to the v0.1 run, with no v0.2 fields on any
step. Negative control: the same comparison on a copy with one message
changed ("5 of 6 files stored") exits 1. The reports (`libredcm-plan/`)
are kept as records. The image also validated a fixture with
`--network none` (pass with warnings, as before).

## 4. Local gate

See the PR description for the full gate output (fmt, vet, race and
official-tag tests, gosec, govulncheck, trace/needs for every module,
mdlinks, dcs, QMS unit tests, gitleaks on the new commits).
