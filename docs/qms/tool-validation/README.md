# Tool validation notes

| | |
|---|---|
| Procedure | QMS-SOP-011 |
| Status | **Executed 2026-11-10 by Hermes/Claude on branch `qms/retro-dcm`; awaiting review and approval** |

One section per tool: intended use, risk (if it failed silently), version,
activities, results, conclusion, re-validation triggers. Results are real
command output from 2026-11-10 unless stated.

## Summary

| Tool | Version | Risk | Activities | Result |
|---|---|---|---|---|
| Go toolchain | 1.26.x (≥ 1.26.10; image `golang:1.26-bookworm`, release build pinned by digest) | Low | Vendor test suite; our full test suite on every run | Fit |
| Docker / Docker Compose | Engine 29.9.2 on the dev machine; Docker Desktop on Windows | Low | Release verification checks the built image (health, C-ECHO, version) | Fit |
| GitHub Actions (`qms.yml`) | `ubuntu-latest`, `actions/checkout@v4`, `actions/setup-go@v5` | Medium | Every step run locally in the same Go image with the same commands; results match | Fit; new steps not yet run on GitHub (branch not pushed) |
| gosec | v2.30.0 | Medium | Positive control | Fit |
| govulncheck | v1.9.0 | Medium | Positive control | Fit |
| cyclonedx-gomod | v1.12.0 | Medium | Completeness check against the compiled module list | Fit, with note |
| `scripts/qms/trace.py` | repo version | High | Scripted tests (`test_qms_tools.py`), mutation check | Fit |
| `scripts/qms/needs.py` | repo version | High | Scripted tests | Fit |
| `scripts/qms/mdlinks.py` | repo version | Medium | Scripted tests | Fit |
| `scripts/qms/soup.py` | repo version | Medium | Scripted tests (regeneration identical; new dependency flagged); one defect fixed | Fit |
| `scripts/qms/part11_check.py` | repo version | Medium | Live run (10/10) and negative test | Fit |
| `scripts/qms/release-verify.sh` | repo version | High | Used for 0.2.0-beta.1; review of the script | Fit with findings |
| DCMTK | 3.6.7 (image `rws-e2e-dcmtk`) | Medium | Reference implementation; behaviour cross-checked by Go end-to-end tests | Fit |
| AI coding tools | Claude (Hermes, Claude Code) | Low | Output controlled by review, CI and tests (SOP-011 §3) | Fit |

## Go toolchain

- **Intended use:** compile LibreDCM; run tests (`go test`), static checks
  (`go vet`), fuzzing.
- **Risk: low.** A compiler defect would most likely show up as test
  failures; the Go project runs an extensive test suite and publishes
  security releases. Version pinned (Dockerfile digest; CI `1.26.x`,
  ≥ 1.26.10 required by GO-2026-6809).
- **Activities/results:** full suite green with `-race` (all packages,
  2026-11-10) and with `-tags official`.
- **Re-validate:** on a Go minor version change (1.27).

## Docker

- **Intended use:** build the product image; run test dependencies.
- **Risk: low.** A faulty build fails the release verification checks
  (container healthy, `/version` reports the version, real C-ECHO and DCMTK
  conformance against the built image), done for every release.
- **Re-validate:** not required beyond the per-release verification.

## GitHub Actions (`.github/workflows/qms.yml`)

- **Intended use:** run the quality gates on every PR and on `master`;
  produce CI logs as verification records; the `verify` check gates merges.
- **Risk: medium.** A misconfigured step could pass without checking.
  Mitigation: each step uses an exit-status-checked command (no
  `continue-on-error` since 2026-11-10); results are reproduced locally.
- **Activities/results (2026-11-10):** every step of `verify` was run locally
  in `golang:1.26-bookworm` with the same commands: vet ✓, race tests ✓,
  fork and client-copy tests ✓, gosec (blocking) rc 0 ✓, gosec third_party
  report-only rc 0 with reports written ✓, govulncheck ✓, SBOM ✓, trace check
  rc 0 ✓, `needs.py` rc 0 ✓, `mdlinks.py` rc 0 ✓, QMS script tests ✓. Earlier `verify`
  runs on GitHub: 12 of 12 successful on 2026-11-09 (PRs #3–#9 and the
  `master` pushes, e.g. run 42723579767).
- **Open:** confirm the first GitHub run of the new steps after the branch is
  pushed; record the run ID here.
- **Re-validate:** on any change to the workflow (the PR shows it).

## gosec v2.30.0

- **Intended use:** SAST in CI (blocking for Libre RWS code).
- **Risk: medium** (could miss a weakness; code review and fuzzing also apply).
- **Positive control:** a throwaway module containing
  `&tls.Config{InsecureSkipVerify: true}` → `G402 (CWE-295) ... Issues: 1` ✓.
  On the real code it found 15 issues incl. one real defect (ANOM-001).
- **Re-validate:** on a version change.

## govulncheck v1.9.0

- **Intended use:** SCA of reachable known vulnerabilities in CI and at release.
- **Risk: medium** (quarterly SBOM review is a second control).
- **Positive control:** a throwaway module requiring `golang.org/x/text v0.3.5`
  and calling `language.Parse` → `Vulnerability #1: GO-2021-0113 ... Your
  code is affected by 1 vulnerability from 1 module` ✓. On 2026-11-09 it
  found the reachable GO-2026-6809 in the Go standard library.
- **Re-validate:** on a version change.

## cyclonedx-gomod v1.12.0

- **Intended use:** generate the CycloneDX SBOM (§524B, release record).
- **Risk: medium.** A missing component would leave the SBOM incomplete.
- **Activity:** compared the modules actually compiled into LibreDCM and its
  tests (`go list -deps -test -f '{{.Module}}' ./...`, 47 modules) with
  the committed SBOM (49 components). **Result:** no compiled module is
  missing ✓. **Note:** the two locally replaced modules (gonetdicom fork,
  LibreKey client copy) carry the main repository's pseudo-version in the
  SBOM, not the upstream base version; readers must use `RWS_FORK.md` /
  `LIBREKEY_COPY.md` for their provenance. The committed SBOM must be
  regenerated whenever `go.mod` changes (CI regenerates it on every run).
- **Lesson recorded:** `go list -m all` must not be used for this check: it
  lists the whole module graph (minio-go declares golangci-lint as a tool
  dependency, 279 modules) and rewrites `go.sum`.
- **Re-validate:** on a version change.

## QMS scripts (`trace.py`, `needs.py`, `mdlinks.py`, `soup.py`)

- **Intended use:** produce the trace matrix and the SOUP list (records) and
  gate merges on traceability, user-need coverage and document links.
- **Risk: high** for `trace.py` and `needs.py` (a silent pass would hide
  unverified requirements); medium for the others.
- **Activities:** scripted tests `scripts/qms/test_qms_tools.py` (13 tests:
  untested active requirement fails; unknown and retired IDs fail; a tag not
  directly above a test is ignored; fuzz targets and manual evidence count;
  user-need shorthand, unserved and retired citations; missing files,
  missing anchors, code blocks ignored; SOUP regeneration identical and new
  dependencies flagged). Run in CI.
- **Results:** `Ran 13 tests ... OK`. Mutation check: making `trace.py` never
  exit 1 → 3 tests fail ✓.
- **Defect found and fixed:** `soup.py` wrote a stale runtime-image path
  (`deploy/Dockerfile`); now `deploy/docker/Dockerfile.dicom`. The LibreKey
  risk text in `soup.md` still said "C-STORE refused" (behaviour changed by
  issue #5); corrected.
- **Re-validate:** automatically on every change (tests in CI).

## `scripts/qms/part11_check.py`

- **Intended use:** re-check the GitHub settings that VAL-0001 relies on.
- **Results:** live run 10/10 PASS; against a branch without protection it
  reports FAIL and exits 1 ✓.

## `scripts/qms/release-verify.sh`

- **Intended use:** verify the exact release build (vet, race, official
  tags, PostgreSQL, S3, govulncheck, SBOM, source and official images, DCMTK
  conformance, unlicensed official build); logs become release records.
- **Risk: high** (its logs are the release verification evidence).
- **Activities:** used for 0.2.0-beta.1 (logs in
  `docs/releases/v0.2.0-beta.1-verification/`); script reviewed 2026-11-10.
- **Findings:** test-helper images are not pinned (`chrislusf/seaweedfs:latest`,
  `curlimages/curl:latest`), so a later run may use a different version;
  the script records the Go version and built image IDs but not the helper
  image digests. **Action:** pin helper images by digest and log them
  (small change, next release).

## DCMTK 3.6.7

- **Intended use:** independent DICOM peer for system/conformance tests
  (store of compressed transfer syntaxes, C-FIND, C-MOVE, called-AE check).
- **Risk: medium.** It is the de-facto reference implementation; its results
  are cross-checked by our own Go end-to-end tests over real associations,
  and pixel data is compared byte for byte after retrieval.
- **Re-validate:** on a version change of the test image.

## AI coding tools

See QMS-SOP-011 §3: low risk as a QMS tool because all output is reviewed
and verified by the controls that apply to every change. In this session the
controls caught AI-introduced problems before commit (e.g. fuzz oracles that
were too strict, a too-broad first version of the Part 10 guard that would
have refused over-long UIDs from real modalities).
