# LibreDVT 0.2: pull-request stack and merge order

**DRAFT for approval.** Written by Hermes (Claude), 2026-11-23. Nothing in
this stack is merged. Patrick merges; no tag is made before his OK.

LibreDVT 0.2 was built in six stacked pull requests (Kanban board
`libredvt-v02`). Each branch starts from the one below it, and each pull
request targets the branch below it. The scope approval and decisions D1–D5
are recorded in issue #34 (Patrick Mulcahy, Telegram, 2026-11-19).

| # | Card | Branch | Into | What |
|---|---|---|---|---|
| 40 | DVT-0.2/6 | `feat/libredvt-0.2-rc` | `feat/libredvt-0.2-ui` | Tool validation, verification and release candidate 0.2.0-rc.1: all v0.2 requirements `Active`, printable report, DCMTK cross-check of the DICOM TLS audit, system run, sample audit, user guide for client audits |
| 39 | DVT-0.2/5 | `feat/libredvt-0.2-ui` | `feat/libredvt-0.2-dicomweb` | Web UI, CLI and reports for DIMSE-TLS and DICOMweb; bundled example plans |
| 38 | DVT-0.2/4 | `feat/libredvt-0.2-dicomweb` | `feat/libredvt-0.2-assoc-tls` | DICOMweb QIDO-RS, WADO-RS, STOW-RS |
| 37 | DVT-0.2/3 | `feat/libredvt-0.2-assoc-tls` | `feat/libredvt-0.2-foundation` | Association negotiation and DICOM TLS tests |
| 36 | DVT-0.2/2 | `feat/libredvt-0.2-foundation` | `feat/libredvt-0.2-design` | Protocol targets, strict schema, evidence model, redaction |
| 35 | DVT-0.2/1 | `feat/libredvt-0.2-design` | `feat/dicom-conformance-statement` | Design inputs (docs only) |

The LibreDVT stack sits on top of other open pull requests, which reach
`master` through their own stack:

| # | Branch | Into |
|---|---|---|
| 33 | `feat/dicom-conformance-statement` (LibreDCM DICOM Conformance Statement) | `feat/uid-root` |
| 31 | `feat/uid-root` (Libre Systems DICOM UID root) | `feat/librewt-0.2-release` |
| 29…21 | the Libre WT 0.2 stack | … `master` (see [`librewt-v0.2.0-pr-stack.md`](librewt-v0.2.0-pr-stack.md)) |

## Merge order: top down

Merge the child into its parent first, `master` last. Merging a lower pull
request first would leave the ones above it on branches that no longer
lead anywhere. After each merge, wait for CI on the parent's new head
(`gh run list --branch <parent>`, `gh run watch <id> --exit-status`).

1. `gh pr merge 40 --merge` (into `feat/libredvt-0.2-ui`)
2. `gh pr merge 39 --merge` (into `feat/libredvt-0.2-dicomweb`)
3. `gh pr merge 38 --merge` (into `feat/libredvt-0.2-assoc-tls`)
4. `gh pr merge 37 --merge` (into `feat/libredvt-0.2-foundation`)
5. `gh pr merge 36 --merge` (into `feat/libredvt-0.2-design`)
6. `gh pr merge 35 --merge` (into `feat/dicom-conformance-statement`)
7. `gh pr merge 33 --merge` (into `feat/uid-root`)
8. `gh pr merge 31 --merge` (into `feat/librewt-0.2-release`)
9. then the Libre WT stack from #29 down to #21 into `master`.

Afterwards: close issue #34 with the merge commit and CI result (`Closes`
works only into the default branch), delete the merged branches, check
`git diff --stat feat/libredvt-0.2-rc master` is empty, and watch the `qms`
run on `master`.

## Before merging

- Approve or correct the open decisions in issue #34 (each has a safe
  default in the code): C-FIND against production targets stays allowed;
  bearer tokens over `http://` only to loopback; a TLS-version probe the
  image cannot offer is an engine error; legacy-only cipher servers fail
  closed; the Storage SCP requires the target's client certificate on TLS
  moves; refusal probes at OpenSSL security level 0 without credentials; no
  CRL/OCSP; HTTP 400 counts as a DICOMweb refusal; STOW instance warnings
  fail unless `allow_warnings`; a Retrieve URL on another origin is a
  warning and never requested; HTML body excerpts (64 KiB default for user
  plans, 0 in the bundled DICOMweb audit); referenced files outside
  `secrets_dir` refused when saved; any TLS field turns TLS on.
- Approve or correct the tool validation (`docs/qms/tool-validation/README.md`,
  section *LibreDVT 0.2 release candidate*) and the residual-risk statement
  (`docs/dhf/libredvt/risk-management.md` §5).
- Tag only after the merge: `libredvt-v0.2.0` is the proposed tag name
  (LibreDVT has no tag yet; open decision for Patrick), from the merge
  commit on `master`, with the image built with `VERSION=0.2.0`.
