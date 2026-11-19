# Evidence: LibreDVT v0.2 association and TLS (card DVT-0.2/3)

**DRAFT for approval.** Written 2026-11-19 by Hermes (Claude) for issue #34.
Human review required.

Code under test: branch `feat/libredvt-0.2-assoc-tls`, commits `4e53018`
(engine) and `52873de` (DNS-name CN control, cross-check plans, compile-safe
mutants). The mutation and engine logs were produced from `52873de`, the
cross-check from `03fac78` (adds the store plans; scripts only, same
module code). The commit that adds this README changes documents only.

| File | What it shows |
|---|---|
| `01-mutation-check.log` | `scripts/mutation.py`: 23 of 23 mutants killed (14 Go mutants checked with the unit tests, 9 worker mutants checked with the real-engine tests in the engines image). Baselines pass first; the worktree is clean afterwards |
| `02-engine-tests.log` | `docker build -f deploy/docker/Dockerfile.dvt --target test .`: unit and real-engine tests (`-tags engines`), 106 passed, 0 failed, exit 0 |
| `03-cross-check.log` | `scripts/cross-check.sh`: LibreDVT against DCMTK 3.6.7 and LibreDCM in TLS mode; 13 of 13 runs as expected, 0 leaks in 77 artefacts |
| `cross-check/` | Per run: CLI log, run JSON, JUnit XML and HTML report; DCMTK `echoscu` logs; server logs; the target file and plans used |
| `scripts/plans/` | The cross-check plans, each with the source of its expected values |

## Positive controls (real engines, Go test peer)

The test peer (`modules/dvt/internal/fakepeer`) is Go (`crypto/tls`,
`crypto/x509`, `net`); the worker is Python (`ssl`/OpenSSL). They share no
code. Each negative case must fail the step, and after a failed handshake
the peer must not receive an A-ASSOCIATE-RQ.

- TLS (`TestRealTLSVerification`, `TestRealTLSVersions`,
  `TestRealMutualTLS`, `TestRealV01EchoOverTLS`): wrong host, wrong
  `server_name`, name only in the CN (IP and DNS), unknown CA, expired, not
  yet valid, incomplete chain, self-signed leaf, system trust store without
  the test CA, TLS 1.0-only and 1.1-only servers, TLS 1.2 server with a 1.3
  minimum, client certificate missing, from another CA or with a
  mismatched key. Passing cases: IP SAN, DNS SAN via `server_name`, chain
  with intermediate, TLS 1.2-only and 1.3-only servers, mutual TLS.
- Refusal probes (`TestRealTLSReject`): plaintext, TLS 1.0, TLS 1.1 and
  no-client-certificate against peers that refuse (pass) and peers that
  accept (fail). The probe never sends a client certificate, P-DATA or a
  release.
- Association (`TestRealAssociateNegotiation`,
  `TestRealAssociateRejectAbortCloseGarbageSilence`): accepted/rejected
  contexts, other transfer syntax, missing and extra contexts, roles, async
  operations, max PDU, Implementation Class UID and Version Name, reject
  values, abort, close, garbage, silence, abort on release.
- Storage SCP with TLS during a move (`TestRealSCPTLSDuringMove`): the
  target's client certificate is required by default; a missing or foreign
  one is refused.

## Mutation check

`01-mutation-check.log`. The first run (before `52873de`) killed 20 of 23:
G4 and G12 did not compile (unused variables, so they proved nothing) and
P2 (Common Name fallback) survived, because the CN-only control used an IP
address and OpenSSL never matches an IP against the CN. `52873de` adds a
DNS-name CN control and compile-safe G4/G12 mutants; the rerun kills 23 of
23.

## Cross-check against DCMTK and LibreDCM

`03-cross-check.log`. Certificates come from the host's `openssl` command
(RSA), so neither Go nor the worker made them. Expected outcomes were set
before the run from the peers' own documentation (DCMTK 3.6.7 defaults,
`docs/qms/uid-registry.md` for LibreDCM's identification) and are coded in
`scripts/summarize.py`.

| Run | Peer | Expected | Result |
|---|---|---|---|
| plain-negotiation | `storescp` | pass: 5 contexts as DCMTK documents, max PDU 16384, `1.2.276.0.7230010.3.0.3.6.7` / `OFFIS_DCMTK_367` | pass |
| refuse | `storescp --refuse` | pass: A-ASSOCIATE-RJ 1/1/1 | pass |
| tls-negotiation | `storescp +tls` | pass, TLS facts recorded | pass (TLSv1.3) |
| tls-refusals | `storescp +tls` | pass: plaintext, TLS 1.0, 1.1 refused | pass |
| tls-no-client-cert-not-required | `storescp +tls` (no client auth) | **fail** (finding) | fail |
| mtls | `storescp +tls -rc` | pass | pass |
| mtls-stranger | `storescp +tls -rc`, client cert from an unknown CA | **fail** | fail |
| wrong-host | `storescp +tls`, certificate for `other.test` | **fail** | fail (host-name mismatch) |
| aes-profile-old-versions | `storescp +tls +pa` (retired AES profile) | **fail**: TLS 1.0/1.1 accepted | fail |
| libredcm-tls | LibreDCM, TLS cert | pass, `1.3.6.1.4.1.67686.1.1.1.0.0` / `LIBREDCM_0_0` | pass |
| libredcm-no-client-cert | LibreDCM (no client auth) | **fail** (finding) | fail |
| store-dcmtk-tls | `storescp +tls`, C-STORE of the uncompressed fixture | pass | pass; storescp wrote 1 file |
| store-libredcm-tls | LibreDCM, C-STORE of the 6 image fixtures, then C-FIND | pass | pass |

The DCMTK servers' own logs agree with every refusal LibreDVT reported:
`dcmtk-tls` logged two "unsupported protocol" (TLS 1.0/1.1 probes) and one
"wrong version number" (plaintext); `dcmtk-mtls` logged "peer did not
return a certificate" twice (LibreDVT's probe and `echoscu +tla`) and
"certificate verify failed" once (the unknown-CA client); `dcmtk-aes`
logged TLSv1 and TLSv1.1 handshakes with AES128-SHA (LibreDVT's probes).
DCMTK `echoscu` (which does not check host names) reached the same servers
as expected, and failed without a client certificate against `-rc` and in
clear text against LibreDCM's TLS port.

LibreDCM's `no_client_cert` result is a finding about LibreDCM, consistent
with its design (TLS server without client authentication, see its DICOM
Conformance Statement). It is not a LibreDVT defect.

### Deviation

The first run expected the plain echo to the AES-profile server to pass.
It failed with a TLS handshake alert. The DCMTK log shows "no shared
cipher": the retired AES profile offers only AES128-SHA (RSA key exchange,
no forward secrecy), which LibreDVT's client does not offer. That is
fail-closed behaviour, now documented in `cybersecurity.md` and the README;
the expectation in `summarize.py` was corrected with a comment. Nothing in
LibreDVT was changed for it.

## Not covered here

DICOMweb (card DVT-0.2/4). C-MOVE over TLS was not cross-checked with
LibreDCM (its move destinations do not use TLS); the TLS move path and the
Storage SCP's client-certificate check are covered by
`TestRealSCPTLSDuringMove` against the Go peer. PowerShell scripts were not
touched.
