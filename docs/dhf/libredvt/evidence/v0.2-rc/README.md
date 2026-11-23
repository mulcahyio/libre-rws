# Evidence: LibreDVT 0.2.0 release candidate 1 (card DVT-0.2/6)

**DRAFT for approval.** Written 2026-11-23 by Hermes (Claude) for issue
#34. Human review required. This folder is the verification record of the
release candidate; the tool-validation summary is in
`docs/qms/tool-validation/README.md` (section *LibreDVT 0.2 release
candidate*).

Code under test: branch `feat/libredvt-0.2-rc`, commit `015f3b3` (module
code; the later commits of this card change documents and evidence only:
`git diff --stat 015f3b3 HEAD -- modules deploy go.mod go.sum` lists only
`modules/dvt/README.md`). Images built from `3641f31`, whose module tree
is identical to `015f3b3` (that commit only changed the SRS and
intended-use documents): `libre/dvt:0.2.0-rc.1`
(`sha256:46d4f935…8694247`, `libredvt version` = `LibreDVT 0.2.0-rc.1`),
`libre/dcm:dvt-0.2-rc` (`sha256:61b3f840…7395af1`), test stage
`libre/dvt-test:0.2-rc` (`sha256:c94c4acf…1fc77`).

| File | What it shows |
|---|---|
| `02-engine-tests.log` | `docker build -f deploy/docker/Dockerfile.dvt --target test` at `015f3b3`: `go test -v -tags engines ./modules/dvt/...` with the real engines, 10 packages ok, 122 distinct tests passed (195 PASS lines with subtests), 0 failed, exit 0 |
| `03-system-run.log`, `system-run/` | `scripts/system-run.sh`: the release-candidate image against LibreDCM (TLS and plain), Orthanc + DICOMweb (HTTPS, bearer token) and DCMTK 3.6.7 (four set-ups), DCMTK `echoscu`, server logs, the sample audit, the summary against expectations written before the run: **26 of 26 checks as expected**, secret check 0 leaks in 56 artefacts |
| `04-trace-evidence.md` | `scripts/trace-evidence.py 02-engine-tests.log`: per v0.2 requirement the tagged tests and whether each passed in that log: **58 of 58 rows verified by passing tests** |
| `04a-trace-evidence-negative-controls.log` | the same script on two broken logs (one PASS line removed; three tests marked FAIL): rows flagged, exit 1 |
| `mutation/` | `scripts/mutation-all.sh`: every v0.2 mutation check re-run on `015f3b3` in a git worktree, plus the card 6 mutants |
| `05-local-gate.log` | the full local gate on the final commit |
| `scripts/` | the scripts above |

## Results

**Requirements.** All 58 v0.2 rows (DVT-SRS-090…156 and the new -157) are
`Active`. Every row has at least one tagged test, and every tagged test
passed in the real-engine run. `trace.py --module libredvt --prefix DVT
--check`: 93 of 93 active requirements verified; `needs.py`: all traced to
user needs. Rows that also have a manual or system-level record:
DVT-SRS-081, -145, -146, -152, -156, -157 (`manual-verification.md`).

**Mutation re-run.** Every v0.2 mutation script, unchanged, on `015f3b3`
in a git worktree (the main tree was never touched); worker mutants run in
the test-stage image with the real engines. Baselines passed first; the
worktree was clean afterwards.

| Card | Log | Result |
|---|---|---|
| DVT-0.2/2 foundation | `mutation/card2-foundation.log` | 32 of 32 applicable killed; M18 not applicable (see below) |
| DVT-0.2/3 association and TLS | `mutation/card3-assoc-tls.log` | 23 of 23 killed (14 Go, 9 worker) |
| DVT-0.2/4 DICOMweb | `mutation/card4-dicomweb.log` | 41 of 41 killed (31 Go, 10 worker) |
| DVT-0.2/5 UI, CLI, reports, plans | `mutation/card5-ui.log` | 21 of 21 killed, 0 invalid |
| DVT-0.2/6 printable report | `mutation/card6-print.log` | 4 of 4 killed (details closed in print, raw output clipped, dark print colours, step names broken mid-word) |
| **Total** | | **121 of 121 applicable mutants killed** |

**System run.** Release-candidate image, command line, plans from the
image (`/opt/libredvt/plans`):

| Run | Target | Expected | Result |
|---|---|---|---|
| `rc-libredcm-smoke` | LibreDCM, TLS | pass 5/5 | pass 5/5 |
| `rc-dicom-tls-audit-libredcm` | LibreDCM, TLS | pass 5/5 | pass 5/5 |
| `rc-libredcm-conformance-v01` (v0.1 plan) | LibreDCM, plain | pass 5/5 | pass 5/5 (6 of 6 stored and moved back, pixels identical) |
| `rc-dicomweb-audit` | Orthanc 1.13.1 + DICOMweb 1.24, HTTPS, bearer token | pass 8/8 | pass 8/8 (6 of 6 stored and read back) |
| `xc-audit-dcmtk-tls` | DCMTK `storescp +tls` (default profile) | pass 5/5 | pass 5/5 |
| `xc-audit-dcmtk-plain` | DCMTK `storescp`, no TLS | fail: TLS association, echo, plaintext refusal | as expected; TLS 1.0/1.1 probes count as refused (no handshake possible) |
| `xc-audit-dcmtk-aes` | DCMTK `+tls +pa` (retired AES profile) | fail: TLS association and echo (no shared cipher), TLS 1.0 and 1.1 refusals | as expected; plaintext refused |
| `xc-audit-dcmtk-wrong-host` | DCMTK `+tls`, certificate for `other.test` | fail: TLS association and echo (host name) | as expected; refusals pass |
| `sample-dicom-tls-audit` | LibreDCM as `pacs.lab.example`, AE `LABPACS` | pass 5/5 | pass 5/5 |

DCMTK `echoscu` (10 runs) gave the expected result each time, and in 7 of 7
comparisons it states the same fact as the audit (echo over TLS works or
not; plaintext accepted or refused). The DCMTK server logs record the
protocol of each connection: TLS 1.3 with LibreDVT; "unsupported protocol"
and "wrong version number" for the old-version and plaintext probes on a
correct server; TLS 1.0 and TLS 1.1 handshakes on the AES profile (which
the audit reports as findings) and "no shared cipher" for LibreDVT's
verified client there; "sslv3 alert bad certificate" on the wrong-host
server, where LibreDVT refused the certificate. DCMTK's own client does not
check host names and connects to the wrong-host server: LibreDVT is
stricter by design (DVT-SRS-102).

**Secrets.** The synthetic token (plain, base64, base64url) and every
private-key line of the synthetic PKI: 0 hits in the 56 artefacts.

**Sample audit.** `system-run/sample-dicom-tls-audit-report.html` is copied
unchanged to `docs/releases/libredvt-v0.2.0-rc.1-sample-audit/` with its
PDF; how it was anonymised (by set-up, not by editing) is in that folder's
README.

## Deviations and notes

- Foundation mutant M18 is not applicable any more (its code was replaced
  by real DIMSE TLS in card DVT-0.2/3, covered by card 3's mutants); the
  script reports "PATTERN NOT FOUND". Counted as not applicable, not as
  killed.
- The line "orthanc stored instances (REST)" in `03-system-run.log` shows an
  exec error: the Orthanc image has no `curl`. It was informational only;
  the DICOMweb results rest on LibreDVT's read-back and pixel comparison
  here, and on the byte-for-byte REST comparison of card DVT-0.2/4.
- The card DVT-0.2/5 handoff asked for the DCMTK `+tls` comparison of the
  Generic DICOM TLS audit: done here (`xc-audit-*`).
- Leftovers: none from this card's system run (containers and network
  removed by the script's exit trap). The synthetic PKI and token stay in
  a scratch directory under `$TMPDIR` (`dvt06-pki.*`) until it is pruned.
