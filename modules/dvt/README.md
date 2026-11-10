# LibreDVT (DICOM Validation & Test)

LibreDVT validates DICOM objects and tests DICOM services through a web
interface and a command line. It is a module of Libre RWS that runs in its own
container, separate from LibreDCM and Libre WT, and treats the system under
test as a black box reached only over DICOM.

> **Test data only.** LibreDVT stores uploaded files and reports until you
> delete them. Do not upload real patient data.

LibreDVT is a quality-system tool, not a medical device. Its results are used
as verification records, so it is validated as a high-risk tool
(QMS-SOP-011). Design file: [`docs/dhf/libredvt/`](../../docs/dhf/libredvt/SRS.md).

## What it does (v0.1)

| Area | What |
|---|---|
| Object validation | Files, folders or zip archives are checked with dicom3tools `dciodvfy` (IOD, encoding, VR/VM), pydicom `dicom-validator` (IOD rules from the DICOM standard 2026d) and, across all files, `dcentvfy` (consistent patient/study/series attributes). Non-DICOM files fail. If an engine cannot check a file, the file fails. |
| Service tests | C-ECHO, C-STORE in each file's own transfer syntax, C-FIND (Study Root), C-MOVE to a built-in Storage SCP with a check that exactly the expected instances come back and their pixel data is byte-identical, and association rejection tests. |
| Test plans | YAML plans with `verifies:` requirement IDs; strict parsing; JUnit XML and self-contained HTML reports; results history. |
| Safety | Targets are `lab` or `production`; store and move steps only run against `lab`. A target without a class is `production`. |

The engines (dicom3tools, dicom-validator, pydicom, pynetdicom) are
deliberately **not** the libraries LibreDCM uses, so a bug in LibreDCM's DICOM
stack cannot hide in its own test (DVT-SRS-080).

## Run it

```sh
printf '%s' 'choose-a-long-admin-password' > deploy/compose/libredvt-admin-password.txt
docker compose -f deploy/compose/docker-compose.dvt.yml up -d --build
# open http://127.0.0.1:8095 and log in with that password
```

The web UI (8095) and the Storage SCP (11115) are published on 127.0.0.1.
Set `LIBRE_DVT_BIND_IP` to expose them on a network, and put TLS in front of
the web UI (or set `LIBRE_DVT_TLS_CERT` / `LIBRE_DVT_TLS_KEY`) when you do.

### Testing LibreDCM

1. Put LibreDVT and LibreDCM on one Docker network, e.g.
   `docker network create dicomtest` and `docker network connect dicomtest libredvt`
   (and the same for the LibreDCM container).
2. Tell LibreDCM where LibreDVT's Storage SCP is, so C-MOVE can deliver:
   `LIBRE_DCM_REMOTE_AES=LIBREDVT=libredvt:11115`.
3. In LibreDVT, add a target named `libredcm-lab`: host = the LibreDCM
   container name, port 11112, called AE = LibreDCM's AE title, calling AE
   `LIBREDVT`, class **lab**. If LibreDCM has a calling-AE allow-list, add
   `LIBREDVT` to it.
4. Under **Test plans**, run the bundled *LibreDCM conformance* plan.

## Command line

```sh
docker run --rm --network dicomtest libre/dvt:dev run \
  -host libredcm -port 11112 -called-aet LIBREDCM -lab \
  -junit /tmp/junit.xml /opt/libredvt/plans/libredcm-conformance.yaml

docker run --rm -v "$PWD/files":/files:ro libre/dvt:dev validate /files
```

`run` exits 0 only when every step passed, 1 when a step failed or did not
run, and 2 when the plan is invalid. `validate` exits 0 only when no file
failed.

## Test plans

```yaml
name: My PACS smoke test
target: my-lab-pacs        # target name in LibreDVT
timeout: 30s               # per DICOM operation (default 30s)
steps:
  - {id: validate, type: validate, files: [fixtures/*.dcm]}
  - {id: echo,     type: echo,     verifies: [DCM-SRS-001]}
  - {id: reject,   type: reject,   called_aet: WRONG-AE}
  - {id: store,    type: store,    files: [fixtures/*.dcm]}
  - {id: find,     type: find,     from_store: true}
  - {id: move,     type: move,     from_store: true, compare_pixels: true}
```

| Step | Fields | Passes when |
|---|---|---|
| `validate` | `files` | no file fails validation |
| `echo` | | C-ECHO status is Success |
| `reject` | `calling_aet` and/or `called_aet` | the association is rejected |
| `store` | `files` | every file gets status Success (target must be `lab`) |
| `find` | `studies` or `from_store` | every study is returned with a final Success |
| `move` | `files`, or `studies` + `files`, or `from_store`; `compare_pixels` | final Success, exactly the expected instances received, pixel data and transfer syntax unchanged (target must be `lab`) |

File patterns are relative to the plan file and must each match at least one
file. Unknown fields or step types reject the whole plan before anything runs.

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `LIBRE_DVT_ADMIN_PASSWORD_FILE` / `LIBRE_DVT_ADMIN_PASSWORD` | (required, ≥ 12 characters) | admin login |
| `LIBRE_DVT_LISTEN` | `:8095` | web address |
| `LIBRE_DVT_DATA` | `/var/lib/libredvt` | targets, uploads, results |
| `LIBRE_DVT_SCP_AE`, `LIBRE_DVT_SCP_PORT` | `LIBREDVT`, `11115` | built-in Storage SCP |
| `LIBRE_DVT_SESSION_IDLE` | `30m` | session inactivity timeout |
| `LIBRE_DVT_TLS_CERT`, `LIBRE_DVT_TLS_KEY` | | serve the UI over HTTPS |
| `LIBRE_DVT_ENGINE_TIMEOUT` | `2m` | per engine call |

## Development

```sh
rws-go test -race ./modules/dvt/...                                     # unit tests, fake engines
docker build -f deploy/docker/Dockerfile.dvt --target test .            # positive controls with the real engines
python3 scripts/qms/trace.py --module libredvt --prefix DVT --check     # requirement trace
```

Changing an engine version (`worker/requirements.txt`) or a base-image digest
requires re-running the tool validation.
