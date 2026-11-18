# Evidence: LibreDCM DICOM Conformance Statement, checked with DCMTK and pynetdicom

| | |
|---|---|
| Document checked | [dicom-conformance-statement.md](../../dicom-conformance-statement.md) (DHF-DCM-DCS, DRAFT), requirement DCM-SRS-101, issue #32 |
| Reference tools | DCMTK 3.6.7 (OFFIS), image `rws-e2e-dcmtk` (the release conformance image, `docs/qms/tool-validation/README.md` § DCMTK 3.6.7); pynetdicom 3.0.4 in image `libre/dvt:engines` for what the DCMTK command-line tools cannot propose (asynchronous operations window, extended negotiation, user identity, chosen TLS versions, refused presentation contexts) |
| System under test | LibreDCM image `libre/dcm:dcs` built from this branch with `--build-arg VERSION=0.2.0-dcs.1` (a release-like version, so the Implementation Class UID is `1.3.6.1.4.1.67686.1.1.1.0.2`). Five containers with different settings, see below |
| Script | [scripts/evidence.sh](scripts/evidence.sh), run by Hermes (Claude) from the repository root; DCMTK commands in [scripts/dcmtk-runs.sh](scripts/dcmtk-runs.sh), probes in [scripts/pynetdicom-probes.py](scripts/pynetdicom-probes.py), test data from [scripts/gen-data.py](scripts/gen-data.py) |
| Data | pydicom's public sample files only, rewritten with new UIDs under the LibreDCM test-data arc `1.3.6.1.4.1.67686.9.1.900` (uid-registry.md) and synthetic names `DCS^...`. No patient data. List: `01-test-data.txt` |
| Build record | `00-build.txt`: commit, image ID, `-version`, tool image IDs, container health |

## Containers

| Container | Setting changed from the defaults | Used for |
|---|---|---|
| `dcs-gw` | `LIBRE_DCM_REMOTE_AES=DCMTKMOVE=dcs-tools:11113` | Most runs |
| `dcs-allow` | `LIBRE_DCM_ALLOWED_AES=DCMTKOK` | Allow-list |
| `dcs-reject` | `LIBRE_DCM_PATIENT_CONFLICT_POLICY=reject`, `LIBRE_DCM_FIND_LIMIT=1` | Conflict reject, C-FIND limit |
| `dcs-coerce` | `LIBRE_DCM_PATIENT_CONFLICT_COERCE_PATIENT_ID=true` | Patient ID coercion |
| `dcs-tls` | `LIBRE_DCM_TLS_CERT`, `LIBRE_DCM_TLS_KEY` (certificate made by `libredcm -gen-cert`, `02-gen-cert.txt`) | TLS |

## Runs and results

"Evidence NN" in the conformance statement means file `NN-*.log` here. The
exit status of every DCMTK command is at the end of its log and listed in
`03-runs.txt`. A non-zero exit is expected where LibreDCM must refuse.

| NN | Run | Expected (statement section) | Result |
|---|---|---|---|
| 10 | `echoscu -d` | A-ASSOCIATE-AC with Implementation Class UID / Version Name, Max PDU 131072 (7.1.1) | As expected, exit 0 |
| 11 | `echoscu` calling `NOTLIBREDCM` | Rejected permanent, service user, called AE title not recognized (7.3.1.2) | As expected, exit 1; warning in `80-gateway-dcs-gw.log`, no audit record (ANOM-014) |
| 12, 13 | `echoscu` to `dcs-allow` as `DCMTKOK` / `INTRUDER` | Accepted / rejected calling AE title not recognized, audit `SecurityAlert.association-rejected` (8.7.2) | As expected |
| 14 | `storescu -d` of the native files, default proposal | 128 storage contexts accepted; Success (7.2.1.4.2) | As expected |
| 15 | `dcmsend -d` of 7 compressed files | Each accepted in its own compressed syntax; stored unchanged (5.2.5.2, 7.2.1.4) | As expected; `70-stored-files.log` shows the 7 compressed syntaxes |
| 16 | `storescu -xb` (Explicit VR Big Endian proposed alone in some contexts) | Big-endian-only contexts refused (transfer syntaxes not supported), little endian accepted (1.1) | As expected |
| 17 | `storescu` of a file without Study Instance UID | A900 (Table 5-3) | As expected |
| 18 | `storescu` of the same instance again | Success, overwrite (Table 5-3) | As expected |
| 19, 30 | Conflicting Patient ID under `quarantine`; then C-FIND | Success; held instance not returned (Table 5-3, 5.2.7.5) | As expected |
| 20 | Conflicting Patient ID under `reject` | A778 (7.3.2.5.2) | As expected |
| 21, 71 | Conflicting Patient ID with coercion | Warning B000; stored copy has Patient ID `B000_…` and an Original Attributes Sequence item (Table 5-2) | As expected |
| 22–29 | `findscu` at each level (Patient Root and Study Root), date range, Modalities in Study, UTF-8 name case-insensitive, UID list, image level, PATIENT level in Study Root | Keys and matching as in Tables 5-5 to 5-8; ISO_IR 192 in responses with non-ASCII values; A900 for PATIENT level in Study Root | As expected |
| 31 | `findscu` on `dcs-reject` (`find_limit` 1) with 2 matches | 1 match, then Success (deviation 5, ANOM-012) | As expected |
| 32 | `movescu` of the native study | LibreDCM's A-ASSOCIATE-RQ proposes Explicit then Implicit VR Little Endian; 2 completed; Success | As expected |
| 33 | `movescu` of the compressed study to a destination accepting only native syntaxes | All sub-operations fail, A702; no transcoding (5.2.5.1.1) | As expected |
| 34 | Same, destination accepting all syntaxes | 7 completed, Success | As expected |
| 35 | `movescu` to unknown destination `NOWHERE` | A801 | As expected |
| 36 | `movescu` with an empty Study Instance UID only | A900 (5.2.7.7) | As expected |
| 37 | `movescu` Patient Root | Success | As expected |
| 38 | `getscu -d` of the native study | SCP role accepted for the storage classes of Table 1-1; 2 completed; Success | As expected: 106 storage contexts accepted with the SCP role; the 14 retired storage classes `getscu` also proposes refused (abstract syntax not supported); 2 completed |
| 39 | `getscu` of the compressed study | 7 failed, A702 (deviation 3); DCMTK's release then fails with "DUL P-Data PDU arrived" (DCMTK does not read the Failed SOP Instance UID List; note in 3.3.1) | As expected |
| 40 | `echoscu +tla +cf tls.crt` to `dcs-tls` | TLS 1.3 connection, certificate verified, Success (8.4.2) | As expected |
| 41 | Plain `echoscu` to `dcs-tls` | Refused (7.3.1.2) | As expected, exit 1 |
| 60 | pynetdicom probes | TLS 1.1 refused, 1.2 and 1.3 accepted; asynchronous operations window not answered (1/1); no SOP class extended negotiation reply; user identity ignored, no response; unknown SOP class refused (abstract syntax not supported) and Deflated-only context refused (transfer syntaxes not supported); C-GET with a missing storage context ends the association without a final response (ANOM-011) | As expected; ANOM-011 recorded |
| 70 | `dcmdump -M` of all stored files | Stored transfer syntaxes = received ones; Implementation Class UID `1.3.6.1.4.1.67686.1.1.1.0.2`, Version Name `LIBREDCM_0_2` in every file | As expected |
| 80 | Gateway logs of the five containers | Audit records (`audit=true`) for stored, query, sent, conflict and allow-list rejection | As expected: 14 received, 9 query, 7 sent, 3 conflict-detected, 1 association-rejected |
| 90 | Mutation check of the two drift checks (`90-mutation-check.log`) | Each product-code mutant that makes the statement wrong fails `TestConformanceStatementMatchesCode` and, where it checks that table, `dcs.py` | Go test killed 5/5 (transfer syntax removed, matching type changed, Q/R class removed, default `find_limit` changed, return key dropped); `dcs.py` killed 4/4 applicable (it does not check default values, by design). Source restored, `git diff` clean |

## How to rerun

From the repository root, with Docker and the two tool images present:

```
bash docs/dhf/libredcm/evidence/dcs-dcmtk/scripts/evidence.sh
```

The script deletes the old logs in this directory (including
`90-mutation-check.log`), builds the image, writes new logs and removes its
containers and network. Compare with `git diff --stat`; the logs contain
times, container IDs and random parts of storage keys, so lines of that kind
always change. The mutation check is separate:
`bash docs/dhf/libredcm/evidence/dcs-dcmtk/scripts/mutation-check.sh > docs/dhf/libredcm/evidence/dcs-dcmtk/90-mutation-check.log`
(run it only on a clean tree; it edits and restores product files).

## Other records of this change

- Tables of the statement: `TestConformanceStatementMatchesCode`
  (`modules/dicom/internal/gateway/conformance_statement_test.go`) and
  `scripts/qms/dcs.py` with its validation tests in
  `scripts/qms/test_qms_tools.py` (`DcsTests`).
- New anomalies found while writing the statement: ANOM-011 to ANOM-014 in
  [anomalies.md](../../anomalies.md).
