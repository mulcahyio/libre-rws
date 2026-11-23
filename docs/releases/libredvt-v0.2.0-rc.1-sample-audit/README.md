# Sample audit report: Generic DICOM TLS audit (LibreDVT 0.2.0-rc.1)

**DRAFT for approval.** Written by Hermes (Claude), 2026-11-23, for
Patrick Mulcahy's Mulcahy.io sample deliverable. Synthetic lab set-up; no
client, no patient data.

| File | What |
|---|---|
| `sample-dicom-tls-audit-report.html` | The HTML report exactly as LibreDVT wrote it (self-contained, opens in any browser) |
| `sample-dicom-tls-audit-report.pdf` | The same file printed to PDF with Chromium 155 headless (`--print-to-pdf --no-pdf-header-footer`), 4 Letter pages |

SHA-256 (HTML): `d38babd09579fecfb15e461820084b1cbe350912745ac49d795aa14475142854`.
It is byte-identical to `sample-dicom-tls-audit-report.html` in the
verification record (`docs/dhf/libredvt/evidence/v0.2-rc/system-run/`),
where the run JSON, JUnit file and log of the same run are kept.

## How it was made

`docs/dhf/libredvt/evidence/v0.2-rc/scripts/system-run.sh`, part 3: the
release-candidate image `libre/dvt:0.2.0-rc.1` ran the bundled *Generic
DICOM TLS audit* plan from the command line against a LibreDCM lab
container (built from the same commit) with TLS on.

**Anonymised by set-up, not by editing.** The names in the report are the
names the systems really used in that run, chosen to identify nobody: the
system answered on the Docker network as `pacs.lab.example` with AE title
`LABPACS`; its certificate was issued for `pacs.lab.example` by a
throw-away CA named "Example Clinic Lab CA" (valid two days); LibreDVT
called as `AUDITOR`; the target was registered as
`example-clinic-lab-pacs`. Nothing in the file was changed after LibreDVT
wrote it. The Implementation Class UID and Version Name
(`1.3.6.1.4.1.67686.1.1.1.0.0`, `LIBREDCM_0_0`) are what the system
announced; they show the system under test was LibreDCM (a development
build). When you show it to prospects, say it was made against a lab
system.

## What the sample shows

Verdict **pass**, 5 of 5 checks: a verified TLS 1.3 association
(TLS_AES_128_GCM_SHA256, certificate verified for the host name, valid for
one more day at run time), C-ECHO over TLS, and refusal of plaintext, TLS
1.0 and TLS 1.1. The report lists every exchange and assertion with expected
and actual values, the certificate subject, issuer, names, validity and
SHA-256, and the tool and engine versions.

## What it does not show

It is not a certification or a penetration test. It shows what that system
did in that run against that endpoint; see *What a report does not prove*
in [`modules/dvt/README.md`](../../../modules/dvt/README.md). A real client
audit needs written consent and lab targets (same section). For a failing
example, see the DCMTK runs in the verification record
(`xc-audit-dcmtk-plain`, `-aes`, `-wrong-host`): plaintext accepted, TLS
1.0/1.1 accepted, certificate for another host.
