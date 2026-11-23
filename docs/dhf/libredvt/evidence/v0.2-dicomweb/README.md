# Evidence: LibreDVT v0.2 DICOMweb QIDO-RS, WADO-RS, STOW-RS (card DVT-0.2/4)

**DRAFT for approval.** Written 2026-11-22 by Hermes (Claude) for issue #34.
Human review required.

Code under test: branch `feat/libredvt-0.2-dicomweb`. Engine and worker
code: commits `73d9e81` (worker `qido`/`wado`/`stow`/HTTP probe, Go schema
checks and assertions), `33b574e` (Go DICOMweb test server, real-engine
controls) and `408814a` (`transfer-syntax=*` for pixel comparison). The
cross-check ran on `408814a`. `b0c51d5` and `900c935` change test-only
files (the test server's boundary-in-data fault and its concurrency
measurement, two tests) and the mutation script; the final mutation and
engine logs were produced from `900c935`. Product and worker code are the
same in all three (`git diff 408814a 900c935 -- modules` touches only
`internal/fakepeer/` and `_test.go` files).

| File | What it shows |
|---|---|
| `01-mutation-check.log` | `scripts/mutation.py` on `900c935`: 41 of 41 mutants killed (31 Go mutants checked with the unit tests, 10 worker mutants checked with the real-engine tests). Baselines pass first; the worktree is clean afterwards |
| `01a-mutation-check-first-run.log` | first run on `2f9ae5c`: 39 of 41 killed (see below) |
| `02-engine-tests.log` | `docker build -f deploy/docker/Dockerfile.dvt --target test .` on `900c935`: unit and real-engine tests (`-tags engines`), 110 top-level tests passed (181 with subtests), 0 failed, 10 packages ok, exit 0. The only uncommitted file was `modules/dvt/README.md` |
| `02a-engine-tests-first-run-failed.log` | first run on `b0c51d5`: exit 1, `TestRealDICOMwebHappyPath` reported 2 concurrent connections (a measurement race in the test server, see below) |
| `03-cross-check.log` | `scripts/cross-check.sh`: LibreDVT against Orthanc 1.13.1 with the DICOMweb plugin 1.24; 5 of 5 runs as expected, 6 of 6 stored files byte-identical, 0 leaks in 30 artefacts |
| `cross-check/` | Per run: CLI log, run JSON, JUnit XML and HTML report; Orthanc server logs; the target file and plans used |
| `scripts/plans/` | The cross-check plans, each with the source of its expected values |

## Independence

LibreDVT's DICOMweb client is Python in the worker: `http.client` over the
worker's one TLS context factory (`ssl`/OpenSSL) and a strict multipart
parser written for LibreDVT. The test server
(`modules/dvt/internal/fakepeer/web.go`) is Go: `net/http`,
`mime/multipart`, `encoding/json`. They share no code. Only tests import
the test server (architecture test). Orthanc (C++ on DCMTK, civetweb,
OpenSSL) shares no code with either.

## Positive controls (real engines, Go DICOMweb test server)

Each fault is one switch in the test server. Every one below must fail
the step (or give the stated verdict); `dicomweb_engines_test.go` checks
the verdict and the message.

- QIDO-RS (`TestRealQidoMalformedResponsesFail`): expected object
  missing, `application/json` instead of `application/dicom+json`, broken
  JSON, an HTML page, 204 and 500 where matches were expected, object
  without its unique key, object for another patient, a page larger than
  `limit`, endless paging (page limit), a DICOM JSON document over the
  size limit, a streamed body over the response limit, a redirect (not
  followed), a malformed HTTP response, a body shorter than its
  Content-Length.
- WADO-RS (`TestRealWadoMalformedResponsesFail`): truncated multipart (no
  closing delimiter), a body without the Content-Type's boundary, an
  `application/octet-stream` part, a part that is not DICOM, an instance
  missing, an extra instance, a duplicate, the stored syntax labelled as
  the requested one, a part header naming another syntax, a changed pixel
  byte, 206, 404, 503, a response that is not multipart, more parts than
  the limit, a part over the size limit, metadata with an instance
  missing or another Patient ID. Boundary text inside the part data (after
  CRLF but followed by other text, after CRLF and spaces, and without
  CRLF) must **pass**: the part stays whole and its pixel data still
  matches.
- STOW-RS (`TestRealStowControls`): an instance in the Failed SOP Sequence
  (0xA700), 409, an instance missing from the Referenced SOP Sequence, an
  extra reference, a wrong SOP Class, a 200 that also lists a failed
  instance, a warning without `allow_warnings` (with it: pass with
  warnings), an empty body, XML, a JSON array, broken JSON, a 307 (not
  re-sent; the other origin receives nothing), changed pixel data on
  `verify_retrieve`. A Retrieve URL on another origin gives a warning and
  is never requested. A non-DICOM file fails the step before anything is
  sent. Production and unclassified targets receive no connection.
- TLS on DICOMweb (`TestRealDICOMwebOverTLS`): a verified TLS 1.2+
  search passes with its TLS facts recorded; a wrong host name and an
  unknown CA fail before any request reaches the server; mutual TLS passes
  with the client certificate and fails without it. Refusal probes pass
  against servers that refuse and fail against servers that accept (clear
  text, TLS 1.1, no client certificate), and carry no token.
- Happy path (`TestRealDICOMwebHappyPath`): a correct server passes every
  step; the token is on every request; the 6 STOW parts are byte-identical
  to the fixtures (SHA-256 recorded by the server); at most one open
  connection; the evidence records `Authorization` as `[REDACTED]`.
- Secrets (`TestRealDICOMwebSecretsNeverInArtefacts`): the test server
  echoes the `Authorization` header into an error page; the token is
  caught, redacted and the run fails; no artefact contains it.

## Mutation check

`01-mutation-check.log`. Go mutants run in a git worktree with the unit
tests; worker mutants replace the image's worker in a test container that
runs the real-engine tests against the Go test server.

The first run (`01a-mutation-check-first-run.log`, head `2f9ae5c`) killed
39 of 41. Two survivors, both gaps in the controls, not in the code:

- P3 (boundary text inside part data splits the part) survived because the
  boundary-in-data fault used the SOP Class UID as boundary. That text
  occurs in the files, but never after `--`, so the fault did not exercise
  the parser at all. `b0c51d5` makes the fault append a Data Set Trailing
  Padding element holding the boundary in three non-delimiter forms
  (pixel data and UIDs unchanged); the step must still pass.
- P9 (STOW sends changed bytes) replaced each file's last byte with 0x00.
  All six fixtures already end in 0x00 (the native ones in zero pixel
  bytes, the encapsulated ones in a sequence delimiter of length 0), so the
  mutant was equivalent. It now flips the last byte.

The rerun on `b0c51d5` killed 41 of 41; the final run
(`01-mutation-check.log`, head `900c935`) kills **41 of 41** (31 Go, 10
worker). Each entry logs the failing test lines. Notes:

- G7 is killed by a panic in the test (it indexes a result the mutant left
  empty). A panic fails the test, so the mutant is detected, but less
  precisely than by an assertion.
- P2 (boundary taken from the body) is killed by the message check: under
  the mutant the step still fails, but later and for another reason. The
  verdict stays fail-safe either way.
- G16 removes a whole family (Go assertion and worker error) because the
  two layers overlap; removing one alone is equivalent.

## Concurrency measurement (DVT-SRS-155)

The first engine-test run (`02a-…`) failed: `TestRealDICOMwebHappyPath`
saw "max concurrent connections 2". The worker is single-threaded and
sends each request on a new connection only after reading the previous
response to the end. The test server counted open connections through Go's
`ConnState` callbacks; the client can see the server's close and connect
again before the server's `StateClosed` callback has run, so a sequential
client could be counted as parallel under load. `900c935` counts each
request as in progress for 10 ms before any response byte is written: a
sequential client cannot overlap that window, a parallel one does.
`TestWebConcurrencyMeasurement` (50 sequential requests count as 1, 4
parallel requests as at least 2) is the measurement's own control; removing
the count or its decrement makes it fail (checked by hand, not in
`mutation.py`). The failure was in the test server, not in LibreDVT; it
was a false alarm, never a false pass.

## Cross-check against Orthanc

`03-cross-check.log`. Certificates come from the host's `openssl` command
(RSA, synthetic CA). Expected outcomes were set before the run from the
Orthanc Book and the fixtures, and are coded in `scripts/summarize.py`.

| Run | Orthanc setup | Expected | Result |
|---|---|---|---|
| orthanc-audit | HTTPS, TLS 1.2+ | pass: clear text and TLS 1.1 refused; STOW 6 files with `verify_retrieve`; QIDO at three levels, by Patient ID, paged (`limit: 2`), no match; WADO from the store with pixel comparison, an explicit transfer syntax, metadata | pass |
| orthanc-mtls | HTTPS with client certificate required | pass: no-certificate probe refused, search with certificate | pass |
| orthanc-token-loopback | HTTP on 127.0.0.1, token configured | pass; token sent, never in an artefact | pass |
| orthanc-no-client-cert | HTTPS without client authentication | **fail**: the no-certificate probe is answered with 200 | fail |
| orthanc-wrong-host | certificate without the host name used | **fail**: host-name mismatch, nothing sent | fail |

Orthanc's REST API then served each stored instance: 6 of 6 are
byte-identical to the fixtures (outside DICOMweb, so the STOW result does
not rest on LibreDVT alone). The secret check grepped all 30 artefacts for
79 needles (token forms and private-key lines): 0 hits.
