# Evidence: LibreDVT v0.2 web UI, command line, reports and example plans (card DVT-0.2/5)

**DRAFT for approval.** Written 2026-11-22 by Hermes (Claude) for issue #34.
Human review required.

Code under test: branch `feat/libredvt-0.2-ui`. The engine tests and the
demo run come from `9cee795`; the final mutation run from `c3697d6` (module
code identical to `9cee795`; that commit only adds documents). The first
mutation run was at `a893780` (before the report tests were strengthened).

| File | What it shows |
|---|---|
| `01-mutation-check.log` | `scripts/mutation.py`: 21 of 21 mutants killed, 0 invalid; baselines pass first; the module tree is clean afterwards |
| `01a-mutation-check-first-run.log` | first run: 19 of 21 killed, 1 survived (U4), 1 did not compile (W4); see below |
| `02-engine-tests.log` | `docker build -f deploy/docker/Dockerfile.dvt --target test .`: unit and real-engine tests (`-tags engines`), 121 top-level tests passed, 0 failed, exit 0 |
| `03-ui-demo.log` | the demo stack (`scripts/ui-demo.sh`), the web-form setup and the three bundled plans (`scripts/ui-setup.sh`), the token scan, and the screenshot run |
| `runs/` | per bundled plan: the run JSON, HTML report, JUnit XML and plan as downloaded from the web UI; the targets page as served |
| `screenshots/` | 11 pages in light and dark (`scripts/e2e/dvt-screenshots.mjs`) |
| `scripts/` | mutation script, demo stack and setup scripts |

## What was checked

**Reports (DVT-SRS-145).** `report_v02_test.go` renders two real stored runs
(the LibreDCM TLS cross-check run of card DVT-0.2/3 and the Orthanc DICOMweb
audit run of card DVT-0.2/4, copied into `modules/dvt/internal/report/testdata`)
and finds every evidence field of every step in that step's own section of
the HTML report: TLS version, cipher, subject, issuer, SAN, validity and
SHA-256 by their labelled fields, Implementation Class UID and Version Name,
every presentation context, HTTP method, URL, status and request headers,
per-instance results, and every assertion. The same facts are checked as
JUnit properties, and every assertion in `system-out`. A synthetic failing
run shows target rows, an expired and unverified certificate, a rejected
association, STOW failure and warning instances, and a redacted
`Authorization` header. A run, HTML report and JUnit file written by the
v0.1 code (master `b932eb3`) load and render unchanged (JSON byte-identical,
HTML identical except the style sheet and white space, JUnit
byte-identical).

**Targets and secrets (DVT-SRS-146, -140, -141).** The web forms give exactly
the target the same record in a target file gives (8 cases, `reflect.DeepEqual`);
16 invalid forms are refused like the schema refuses them (inline `token`,
`password`, `private_key`, verification switches, fields of the other
protocol, TLS 1.1, TLS with http, bearer over http, relative paths, repeated
fields). A file outside `secrets_dir` is refused when saved; a missing file
under it is reported by field name. The token and the private-key body are
in no page and no file of the data directory. CSRF is required on the new
forms.

**CLI/web parity (DVT-SRS-146).** One plan and one target through
`libredvt run -targets FILE` and through the web UI (target saved with the
form, plan uploaded) give equal verdicts, assertions, problems, exchanges
and instances. With the real worker (engine image): a DIMSE TLS target
against the Go test peer (association with three contexts, plaintext and
TLS 1.1 refusal; 3 steps, 11 assertions, pass) and a DICOMweb TLS target
with a bearer token against the Go DICOMweb test server (refusal probe,
three QIDO searches; 4 steps, 14 assertions, pass; the server received the
token, no result holds it). With fake engines in the default build; there a
deliberately different target is shown to make the comparison fail.

**Bundled example plans (DVT-SRS-156).** Unit tests check each plan's
content against the requirement. Through the web UI against real systems
(`03-ui-demo.log`, `runs/`):

| Plan | Target | Result |
|---|---|---|
| LibreDCM smoke (DIMSE + TLS) | LibreDCM (`libre/dcm:v02-foundation`), TLS certificate from a synthetic CA | pass, 5 of 5 steps |
| Generic DICOM TLS audit | the same LibreDCM | pass, 5 of 5 steps |
| Generic DICOMweb audit | Orthanc 1.13.1 + DICOMweb 1.24 over HTTPS (`orthancteam/orthanc:26.11.1`, digest-pinned), bearer token file | pass, 8 of 8 steps |

The synthetic token was in none of the 13 downloaded files and in no file of
LibreDVT's data directory. These runs show the plans work; the formal
cross-checks (the DICOM TLS audit against DCMTK `+tls`) belong to card
DVT-0.2/6.

**Screenshots.** 22 images (11 pages, light and dark), 0 failed, no page
wider than 1280 px. Checked by eye: targets table and both forms, a refused
target (file outside `secrets_dir`), the plans page with step types, and the
run pages with the endpoint identity, TLS, association and STOW evidence.
After the first look, target names and exchange times were kept on one line
and the notice under the targets table got some space (commit `a893780`).

## Mutation check

`01-mutation-check.log`: 21 mutants (10 report, 6 web/store, 2 CLI/runner,
3 bundled plans), each in source, built, and checked with the tests that
should catch it. All killed. The first run (`01a-…`) killed 19: U4 (the
Implementation Class UID dropped from the association section) survived,
because the UID also appeared in an assertion row of the same section; the
report tests now check each fact by its labelled field. W4 did not compile
(an unused variable) and proved nothing; it was made compile-safe.

## Deviations and notes

- The bug fixed in this card (the web UI could not start after a report had
  been rendered in the same process) was found by the parity test, not by a
  user.
- The demo runs use a LibreDVT image built from the working tree at
  `9cee795`; the screenshot script's switch from PNG to WebP was not yet
  committed then (documentation only).
- Leftover: the demo stack containers `dvt05-libredvt`, `dvt05-libredcm`,
  `dvt05-orthanc` and network `dvt05-net` are stopped with
  `bash scripts/ui-demo.sh down`. Their data directories hold only synthetic
  data.
