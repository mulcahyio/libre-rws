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

### 2.1 v0.2 hazards (DRAFT for approval)

Added 2026-11-19 by Hermes/Claude for v0.2 (issue #34): association and TLS
tests, DICOMweb, and use for client audits of other vendors' systems
(intended use §1, user T4). The v0.2 requirements became `Active` in card
DVT-0.2/6 (2026-11-23), after each had passing verifying tests and the tool
validation was recorded; the controls below count from then on.

| ID | Hazard | Cause | Consequence | Controls (requirements) |
|---|---|---|---|---|
| DVT-HAZ-006 | **False pass on TLS or host name**: LibreDVT reports a TLS connection as secure, or a refusal check as passed, when it is not | Certificate or host-name check switched off or never applied (e.g. `CERT_NONE`, Common-Name matching); silent fallback to plaintext or an older TLS version; a handshake error read as "refused"; a refusal probe that could not offer the old version counted as a pass; a test CA trusted by default | A client is told their DICOM traffic is protected when it is not; a LibreDCM TLS defect is released | DVT-SRS-100…-108 (TLS 1.2+, chain and SAN verification, no bypass anywhere, engine error when an old version cannot be offered), -144, -150; positive controls with wrong-host, unknown-CA, expired and TLS 1.1 servers; mutation that disables host-name checking must be caught |
| DVT-HAZ-007 | **Secret leakage into reports or logs**: a bearer token, private key or password ends up in an HTML/JUnit/JSON report, the results store, a log, a worker command line, or is sent to the wrong server | Authorization header recorded verbatim; secret in an error message echoed by the target; secret passed in `argv` (visible in `/proc`); redirect followed with the token; token sent over plain HTTP; inline secret in a target or plan file that is later shared | A client's credentials are disclosed in an audit report or to a third party; loss of trust; possible access to clinical systems | DVT-SRS-104, -106, -107, -111, -112, -118, -132, -141, -151, -152 (redaction plus scan-before-write, failing the run), -153; artefact grep for configured synthetic secrets in the tool validation |
| DVT-HAZ-008 | **Store to a production system through the new paths**: STOW-RS (or a store re-sent through a redirect) reaches a clinical archive | DICOMweb target registered without a class; v0.1 guard only checks DIMSE step types; a 307/308 redirect sends the body to another server; a STOW Retrieve URL followed to another origin | Test images in a client's clinical archive | DVT-SRS-020 (missing class = production), -110 (class kept on every protocol), -134 (data steps only against `lab`), -151 (no redirects), -132 |
| DVT-HAZ-009 | **Misparsed multipart or STOW response giving a false pass** | Boundary taken from the body instead of the header; truncated body without closing delimiter accepted; parts counted instead of SOP Instance UIDs compared; a part that is not DICOM skipped; STOW Failed SOP Sequence or Warning Reason ignored; HTTP 200 trusted over the body; 202/206 treated as success; a size limit cutting the read short and the partial result checked | A DICOMweb server that loses, duplicates or corrupts instances, or refuses stores, is reported as conformant | DVT-SRS-120, -121, -122, -125, -129, -130, -150, -154; malformed-response positive controls (truncated, wrong boundary, wrong part type, failed and warning instances); mutation that ignores failed STOW instances must be caught |
| DVT-HAZ-010 | **Unintended load on, or data pulled from, a third-party system** during an audit | Parallel requests; runaway QIDO paging; huge WADO responses; WADO or QIDO run against a production system and pulling patient data into the results store | Disruption of a client's system; patient data in LibreDVT's data directory (also DVT-HAZ-003) | DVT-SRS-117 (page maximum), -134 (data steps only against `lab`), -154 (size limits), -155 (one request at a time); policy: audits run against lab/test systems with the client's written authorisation (user documentation) |

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

v0.2 adds (DRAFT for approval; done in the v0.2 cards, recorded in the same
tool-validation section):

6. **TLS positive controls:** test servers with a wrong-host certificate, a
   Common-Name-only certificate, an unknown CA, an expired certificate, an
   incomplete chain, TLS 1.1 only, and a server that accepts plaintext or
   TLS 1.1 (for `tls_reject`) must each fail the step. A correct TLS 1.2 and
   TLS 1.3 server must pass.
7. **Association positive controls:** a peer that rejects an expected
   context, accepts an expected-rejected one, picks another transfer syntax,
   rejects with other reason values, aborts, closes, sends garbage or stays
   silent must each fail.
8. **DICOMweb positive controls:** a deterministic test server returning a
   missing or extra UID, wrong Content-Type, malformed JSON, a truncated or
   mis-bounded multipart body, a non-DICOM part, an altered pixel, a 202/206,
   a redirect, a STOW failure or warning, and an oversized body must each
   fail the step. The test server is written for the tests and does not share
   code with LibreDVT's DICOMweb client.
9. **Secret leakage check:** a full run with configured synthetic secrets,
   then a grep of every artefact (data directory, HTML, JUnit, JSON, logs,
   captured worker `argv` and environment) for each secret: zero hits. A
   secret injected into an error message must fail the run.
10. **Production guard:** `qido`, `wado` and `stow` against `production` and
    unclassified targets must be refused with no request reaching the server.
11. **Mutation check:** host-name check off, unknown plan fields accepted,
    failed STOW instances ignored, zero-assertion step passed, verdict
    flipped: each mutant must make a test fail.
12. **Independent cross-check:** the example DICOMweb plan against Orthanc
    with its DICOMweb plugin (digest-pinned, test container only), and the
    LibreDCM TLS smoke plan compared with DCMTK `echoscu`/`storescu` with
    `+tls` on the same LibreDCM.

Status 2026-11-19 (card DVT-0.2/3, DRAFT for approval): activities 6 and 7,
the association/TLS part of 11 and the DCMTK/LibreDCM part of 12 are done;
results in `docs/qms/tool-validation/README.md` and
`docs/dhf/libredvt/evidence/v0.2-assoc-tls/`. One case is proven in a
different form than listed: the Common-Name case uses a certificate whose
only name is a DNS name in the CN, because OpenSSL never matches an IP
address against the CN (an IP-only CN certificate could not catch a CN
fallback; the mutation check showed it).

Status 2026-11-19 (card DVT-0.2/4, DRAFT for approval): activities 8, 9 and
10, the DICOMweb part of 11 and the Orthanc part of 12 are done; results in
`docs/qms/tool-validation/README.md` and
`docs/dhf/libredvt/evidence/v0.2-dicomweb/`. The DICOMweb test server is Go
(`internal/fakepeer/web.go`: `net/http`, `mime/multipart`); LibreDVT's
client is Python (`http.client` and its own multipart parser). Two controls
are proven in a slightly different form than listed: the "202" control is
a 202 with a failed instance (a 202 whose problems are all warnings passes
with warnings only under `allow_warnings`, DVT-SRS-130), and the oversized
body is shown for the response, the DICOM JSON document and the part size
and count limits separately.

Status 2026-11-22 (card DVT-0.2/5, DRAFT for approval): reports show the
new evidence (DVT-SRS-145, mitigation of DVT-HAZ-004 and -005: a reader sees
what was checked, against which endpoint, with which certificate), the web
UI and the command line run plans through the same runner (DVT-SRS-146,
shown by parity tests with the real worker), and web target forms keep
secrets as file references under `secrets_dir` (DVT-HAZ-007). The three
bundled example plans (DVT-SRS-156) passed through the web UI against
LibreDCM in TLS mode and against Orthanc with its DICOMweb plugin; the
formal cross-check with DCMTK is part of card DVT-0.2/6. New residual point
(DVT-HAZ-003, -010): HTML reports now show response-body excerpts, which can
hold data returned by a client's system; the generic DICOMweb audit keeps
none, and the README says how to switch them off (open decision in issue
#34).

Status 2026-11-23 (card DVT-0.2/6, DRAFT for approval): all twelve
activities are done for the release candidate 0.2.0-rc.1, re-run on one
commit; results in `docs/qms/tool-validation/README.md` (section *LibreDVT
0.2 release candidate*) and `docs/dhf/libredvt/evidence/v0.2-rc/`:

- every v0.2 requirement (58 rows, DVT-SRS-090…157) has tagged tests and
  every one of those tests passed in the real-engine test run
  (`04-trace-evidence.md`);
- all v0.2 mutation checks re-run on that commit (activity 11);
- the release-candidate image ran the bundled plans against LibreDCM in TLS
  mode, LibreDCM in plain mode (the v0.1 plan, unchanged) and Orthanc +
  DICOMweb over HTTPS: all as expected;
- activity 12 is complete: the Generic DICOM TLS audit ran against DCMTK
  `storescp` in four set-ups (correct TLS, no TLS, a retired profile that
  accepts TLS 1.0/1.1, a certificate for another host) and DCMTK's own
  `echoscu` and server logs state the same facts (26 of 26 checks as
  expected);
- no token or private-key line in any of the 56 artefacts of that run.

## 5. v0.2 residual risk (DRAFT for approval)

Statement for LibreDVT 0.2.0-rc.1, written 2026-11-23 by Hermes/Claude after
card DVT-0.2/6. It needs review and approval by Patrick Mulcahy.

| Hazard | Controls verified | What remains | Residual risk |
|---|---|---|---|
| DVT-HAZ-006 false pass on TLS or host name | TLS 1.2+, chain and SAN checks, no bypass (architecture test), refusal probes fail closed, engine error when an old version cannot be offered; positive controls (wrong host, CN-only, unknown CA, expired, incomplete chain, old-version servers); mutants that switch host-name checking off or accept TLS 1.1 are caught; DCMTK cross-check agrees | No CRL or OCSP check (a revoked but unexpired certificate passes); LibreDVT tests what the target offers to LibreDVT's client, not every client the target might accept | Acceptable. Revocation is stated as a limit in the README and in every audit's scope |
| DVT-HAZ-007 secret leakage | Secrets only as file references; `Authorization` always `[REDACTED]`; scan-before-write fails the run on any configured secret; no secrets on the worker command line or environment; no redirects; bearer over `http://` only to loopback; artefact greps in every card (0 hits) | An operator who pastes a secret into a free-text field is caught by the scan (it searches values, not field names), but the secret is still in their own plan file | Acceptable |
| DVT-HAZ-008 store to production | `stow`, `qido`, `wado` (and v0.1 `store`, `move`) only against `lab` targets; missing class means production; no redirects; Retrieve URLs on another origin never requested | Depends on the operator classifying the client's system correctly | Acceptable with the operating rules in the README (lab targets only, written consent naming the systems) |
| DVT-HAZ-009 misparsed multipart or STOW | Boundary from the header only, closing delimiter required, UID sets compared, failed and warning instances counted, size limits fail; malformed-response controls and mutants caught; Orthanc cross-check | Only the media types and transfer syntaxes listed in the README are claimed | Acceptable |
| DVT-HAZ-010 load on or data from a third-party system | One request at a time (measured), page maximum, size limits, data steps only against `lab`, the DICOMweb audit keeps no body excerpts | HTML reports of user plans may hold response-body excerpts (default 64 KiB; open decision in issue #34); `echo`, `associate`, `tls_reject` and C-FIND still run against `production` targets (v0.1 behaviour, open decision) | Acceptable with the operating rules in the README |
| DVT-HAZ-001…005 (v0.1) | Unchanged; the v0.1 positive controls and the LibreDCM plan pass unchanged on the release candidate | As in §3 | Acceptable |

**Overall:** with the controls verified as recorded, the residual risk of
using LibreDVT 0.2 for its intended use (verification records for LibreDCM
and client interoperability and TLS audits of lab systems) is acceptable.
The benefit is reproducible, independently cross-checked evidence; the
remaining risks depend on documented operator rules (lab targets only,
written client consent, synthetic data, delete stored test studies when the
client asks) and on two limits stated in every report's scope (no
revocation checking; a report shows what the target did in that run, not
that it is secure or conformant in general). LibreDCM's own tests and the
DCMTK conformance script stay in release verification as independent
checks. Open decisions in issue #34 (C-FIND to production, body excerpts,
TLS form behaviour) do not change this conclusion; each has a safe default.
