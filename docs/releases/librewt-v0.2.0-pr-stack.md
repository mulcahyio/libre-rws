# Libre WT 0.2: pull-request stack and merge order

**DRAFT for approval.** Written by Hermes (Claude), 2026-11-17. Nothing in
this stack is merged. Patrick merges; no tag is made before his OK.

Libre WT 0.2 was built in nine stacked pull requests. Each branch starts
from the one below it, and each pull request targets the branch below it.
The scope approval is recorded in issue #20 (Patrick Mulcahy, 2026-11-14).

| # | Card | Branch | Into | What |
|---|---|---|---|---|
| 29 | WT-0.2/9 | `feat/librewt-0.2-release` | `feat/librewt-0.2-studies` | System test, user guide with screenshots, risk management report, install steps, verification record of the release candidate; table layout fix |
| 28 | WT-0.2/8 | `feat/librewt-0.2-studies` | `feat/librewt-0.2-licence` | Study search, study and series pages, send, LibreDCM audit records and logs, CSV export |
| 27 | WT-0.2/7 | `feat/librewt-0.2-licence` | `feat/librewt-0.2-config` | Licence page and actions, index backup |
| 26 | WT-0.2/6 | `feat/librewt-0.2-config` | `feat/librewt-0.2-instances` | Configuration, destinations, allow-list, edit, reload, rollback, C-ECHO test |
| 25 | WT-0.2/5 | `feat/librewt-0.2-instances` | `feat/librewt-0.2-control-data` | Several LibreDCM instances, fleet, dashboard, status, about |
| 24 | WT-0.2/4 | `feat/librewt-0.2-control-data` | `feat/librewt-0.2-control-actions` | LibreDCM control API: study query, send, configuration edit |
| 23 | WT-0.2/3 | `feat/librewt-0.2-control-actions` | `feat/librewt-0.2-control-read` | LibreDCM control API: C-ECHO test, reload, licence, backup |
| 22 | WT-0.2/2 | `feat/librewt-0.2-control-read` | `feat/librewt-0.2-design` | LibreDCM control API: read-only views |
| 21 | WT-0.2/1 | `feat/librewt-0.2-design` | `master` | Design inputs (docs only) |

## Merge order: top down

Merge the child into its parent first, `master` last. Merging #21 first
would leave the rest on branches that no longer lead anywhere.

1. `gh pr merge 29 --merge` (into `feat/librewt-0.2-studies`), then wait
   for CI on the new head of that branch:
   `gh run list --branch feat/librewt-0.2-studies` and `gh run watch <id>`.
2. `gh pr merge 28 --merge`, wait for CI on `feat/librewt-0.2-licence`.
3. `gh pr merge 27 --merge`, wait for CI on `feat/librewt-0.2-config`.
4. `gh pr merge 26 --merge`, wait for CI on `feat/librewt-0.2-instances`.
5. `gh pr merge 25 --merge`, wait for CI on `feat/librewt-0.2-control-data`.
6. `gh pr merge 24 --merge`, wait for CI on `feat/librewt-0.2-control-actions`.
7. `gh pr merge 23 --merge`, wait for CI on `feat/librewt-0.2-control-read`.
8. `gh pr merge 22 --merge`, wait for CI on `feat/librewt-0.2-design`.
9. `gh pr merge 21 --merge` into `master` (needs `verify` green on that
   exact head and resolved conversations).

Afterwards:

- Issue #20 closes with #21 (`Closes #20` only works into the default
  branch). If it stays open, close it with the merge commit and CI result.
- Delete the merged branches (`git push origin --delete <branch>`,
  `git branch -d <branch>`) and watch the `qms` run on `master`.
- Check that `master` contains every change: `git diff --stat
  feat/librewt-0.2-release master` should be empty before the branches are
  deleted.

## Before merging

- Review the open decisions D1…D62 in `docs/dhf/librewt/architecture.md`
  §8, especially D52 (send is Admin only), D53 (log page Operator, export
  Admin), D55 (search values in `librewt.db`), D59 (system test local, not
  CI), D60 (version and tag).
- Sign or correct the risk management report
  (`docs/dhf/librewt/risk-management.md`, last section).
- After merging, a release follows `docs/qms/release-checklist.md`
  (release branch, `release-verify.sh`, plus this system test).
