# Evidence: LibreDCM Implementation Class UID and Version Name with DCMTK

| | |
|---|---|
| Requirement | DCM-SRS-098 (issue #30, closes ANOM-007) |
| Reference tool | DCMTK 3.6.7 (OFFIS), image `rws-e2e-dcmtk` (same image as the release conformance run, `docs/qms/tool-validation/README.md` § DCMTK 3.6.7) |
| System under test | LibreDCM container image built from branch `feat/uid-root` with `--build-arg VERSION=0.2.0-uidroot.1` (a release-like version, so the expected values are `1.3.6.1.4.1.67686.1.1.1.0.2` / `LIBREDCM_0_2`) |
| Script | Run by Hermes (Claude); commands are in `00-runs.txt` and the build record in `00-build.txt` |
| Data | pydicom's public sample `CT_small.dcm` only; no patient data |

## What was run

1. `echoscu -d` to LibreDCM: the A-ASSOCIATE-AC LibreDCM sends
   (`01-echoscu.log`).
2. `storescu -d CT_small.dcm`: the A-ASSOCIATE-AC for a storage association
   (`02-storescu.log`).
3. `movescu -d` of that study to `movescu`'s own Storage SCP: LibreDCM opens
   the sub-operation association, so DCMTK logs LibreDCM's A-ASSOCIATE-RQ
   ("Sub-Association Received") (`03-movescu.log`).
4. `dcmdump -M +P 0002,0012 +P 0002,0013` of the file LibreDCM stored, copied
   out of the container's storage volume (`04-dcmdump-stored-file.log`).

## Result

In all four places DCMTK reports `Their Implementation Class UID:
1.3.6.1.4.1.67686.1.1.1.0.2` and `Their Implementation Version Name:
LIBREDCM_0_2` (dcmdump: `(0002,0012) UI [1.3.6.1.4.1.67686.1.1.1.0.2]`,
`(0002,0013) SH [LIBREDCM_0_2]`). `LibreDCM -version` in the same image
prints `0.2.0-uidroot.1`. All DCMTK commands exited 0. The summary at the end
of `00-runs.txt` is a grep of the logs.

Negative control (`05-echoscu-before-0.2.0-beta.1.log`): the same `echoscu -d`
against the released image `libre/dcm:0.2.0-beta.1` shows gonetdicom's
`1.2.826.0.1.3680043.10.541.1` / `LIBREDCM_020` (ANOM-007), so the check can
tell the two apart.

Run on commit `32b29c9` (tree clean apart from untracked files), image
`sha256:d6fc1dda0e4f9f92d466de4ce95ff759ab04fbc577df18cbfec58ff0a7150abb`.

## Other records of this change

- `06-libredvt-engine-tests.log`: `docker build -f deploy/docker/Dockerfile.dvt
  --target test` with the real engines (57 tests passed, 0 failed), including
  `TestWorkerSendsLibreDVTIdentification` (DVT-SRS-082). Run on the working
  tree that was committed unchanged as `32b29c9`.
- `07-mutation-check.log`: 19 mutants (UID layout, Version Name format,
  association wiring, Part 10 meta, UID validity, generator persistence,
  overflow, lock, clock, restart floor, damaged state, test-data arc in shipped
  code, LibreDVT arguments and name). All 19 were killed; the first attempt at
  the test-data mutant did not build and was redone so that it builds. The
  tree was restored and checked clean afterwards.
