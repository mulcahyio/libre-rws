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

> **v0.2 in progress (DRAFT).** Association negotiation and DICOM TLS tests
> (card DVT-0.2/3), DICOMweb QIDO-RS, WADO-RS and STOW-RS tests (card
> DVT-0.2/4) and the web UI, command line, reports and example plans for
> them (card DVT-0.2/5) are built.
> See the [v0.2 plan](../../docs/dhf/libredvt/v0.2-plan.md). The v0.2
> functions are not released for use until the v0.2 tool validation is
> approved. The sections below describe v0.1 unless they say v0.2.

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

`run -targets FILE` looks the plan's target up in a YAML target file instead
of the data directory's registry. The file uses the v0.2 target schema
(`docs/dhf/libredvt/plan-schema.md` §2): `dimse` or `dicomweb` targets, an
optional `tls` block, and a bearer token only as `auth.token_file`, a path
to a file holding it. Unknown fields, inline secrets (`token`, `password`,
`private_key`) and switches that would turn off certificate checks are
refused (exit 2).

**v0.2 in progress (DRAFT).** Plans may already contain the v0.2 step types
`associate`, `tls_reject`, `qido`, `wado` and `stow`; they are checked
strictly and against the target's protocol. `qido`, `wado` and `stow` run
only against `lab` targets. Configured secrets never appear in reports, stored runs or
process arguments; if one is found in any output it is replaced by
`[REDACTED]` and the run fails.

### Association and TLS tests (v0.2, DRAFT)

```yaml
name: Vendor PACS association and TLS
target: vendor-pacs-lab          # a dimse target with a tls block
timeout: 10s
steps:
  - id: negotiation
    type: associate
    contexts:
      - {abstract_syntax: "1.2.840.10008.1.1", transfer_syntaxes: ["1.2.840.10008.1.2"], expect: accepted}
      - {abstract_syntax: "1.2.840.10008.5.1.4.1.1.2", transfer_syntaxes: ["1.2.840.10008.1.2.1", "1.2.840.10008.1.2"],
         expect: accepted, expect_transfer_syntax: "1.2.840.10008.1.2.1"}
      - {abstract_syntax: "1.2.840.10008.5.1.4.1.1.2", transfer_syntaxes: ["1.2.840.10008.1.2.4.50"], expect: rejected}
    async_ops: {invoked: 4, performed: 4}
    expect: {max_pdu: 16384, impl_class_uid: "1.2.3.4", impl_version: "VENDOR_1"}
  - {id: plaintext-refused, type: tls_reject, mode: plaintext}
  - {id: tls11-refused,     type: tls_reject, mode: tls_version, tls_version: "1.1"}
  - {id: client-cert-required, type: tls_reject, mode: no_client_cert}
```

| Step | Passes when |
|---|---|
| `associate` | every context is accepted or rejected as expected (and with the expected transfer syntax and roles, when given), the named negotiated values match, and the association is released. With `expect: {association: rejected, reject: {result, source, reason}}` it passes only on exactly that A-ASSOCIATE-RJ. An abort, a closed connection, a PDU LibreDVT cannot parse or silence fails the step. A failed release is a warning. The report records max PDU, Implementation Class UID and Version Name, roles and the asynchronous operations reply |
| `tls_reject` | the target clearly refuses: clear text to the TLS port (`plaintext`), a TLS 1.0 or 1.1 handshake (`tls_version`), or a TLS client without a certificate (`no_client_cert`). A timeout or connection error is not a refusal and fails. Probes send no client certificate, key or token |

With a `tls` block, every step to the target (including v0.1 `echo`,
`store`, `find`, `move`, `reject`) uses TLS 1.2 or later, checks the
certificate chain against `ca_file` (or the image's trust store) and the
host name against the certificate's subjectAltName (or `server_name`).
There is no setting that turns these checks off, and no fallback to clear
text. Each TLS step reports the TLS version, cipher suite and the peer
certificate's subject, issuer, names, validity and SHA-256 fingerprint.
Servers that offer only legacy cipher suites (no forward secrecy) fail the
handshake. A `move` to a TLS target needs `LIBRE_DVT_SCP_TLS_CERT` and
`LIBRE_DVT_SCP_TLS_KEY`; the built-in Storage SCP then requires the
target's client certificate unless the target sets
`move_client_cert: optional`. Certificate revocation (CRL/OCSP) is not
checked.

### DICOMweb tests (v0.2, DRAFT)

```yaml
# targets.yaml (run -targets targets.yaml)
targets:
  - name: vendor-web-lab
    class: lab                     # qido, wado and stow run only against lab targets
    protocol: dicomweb
    base_url: https://pacs.lab.example:8443/dicom-web
    tls: {ca_file: /pki/ca.crt}
    auth: {type: bearer, token_file: /run/secrets/token}   # optional
```

```yaml
name: Vendor DICOMweb audit
target: vendor-web-lab
timeout: 60s
steps:
  - {id: plaintext-refused, type: tls_reject, mode: plaintext}
  - {id: stow, type: stow, files: [fixtures/*.dcm], verify_retrieve: true}
  - {id: qido-studies, type: qido, level: studies, from_store: true,
     require_attributes: [StudyInstanceUID, PatientID, StudyDate]}
  - {id: qido-paged, type: qido, level: studies, limit: 2, expect_uids: ["1.2.3", "1.2.4", "1.2.5"]}
  - {id: wado, type: wado, from_store: true, compare_pixels: true}
  - {id: wado-meta, type: wado, level: study, study: "1.2.3", files: [fixtures/a.dcm],
     metadata: true, compare_attributes: [PatientID, Modality]}
```

| Step | Passes when |
|---|---|
| `qido` | every search answers 200 (or 204 for no match) with `application/dicom+json`, the body is a DICOM JSON array, every object has the level's unique key and matches the query, the expected UIDs are all there (or none, with `expect_none`) and every `require_attributes` key is present. With `limit`, LibreDVT pages with `offset` until a short page; a page larger than `limit` fails |
| `wado` | the response is `multipart/related` with the boundary from its Content-Type and a closing delimiter, every part is `application/dicom` and a DICOM Part 10 object in the asked transfer syntax (header and file agree), exactly the expected instances come back (none missing, extra or twice) with the right Study, Series and SOP Instance UIDs, and, with `compare_pixels`, pixel data is byte-identical to the sent file. With `metadata`, the DICOM JSON metadata holds every expected instance and the compared attributes match the files |
| `stow` | every sent instance is in the Referenced SOP Sequence with its SOP Class, none is in the Failed SOP Sequence, and no unexpected instance is referenced. A failed instance fails the step even when the HTTP status is 200. Instance warnings fail unless `allow_warnings: true`. `verify_retrieve` reads each instance back and compares its pixel data |

The worker uses Python's standard library: one request at a time over the
same TLS settings as DIMSE (TLS 1.2+, chain and host name checked, no
bypass), redirects never followed, and size limits (`limits:` in the plan)
that fail the step instead of truncating. The bearer token is read from its
file, sent only to the base URL's own origin (over `http://` only to a
loopback host) and recorded as `[REDACTED]` everywhere. Retrieve URLs from
STOW responses are recorded but never requested. `tls_reject` works on
DICOMweb targets too.

### Web UI, reports and example plans (v0.2, DRAFT)

**Targets page.** Two forms: *DIMSE target* (host, port, AE titles, class,
and an optional DICOM TLS block) and *DICOMweb target* (base URL, class,
authentication none or bearer, optional TLS settings for an `https` URL).
The forms use the same schema as a target file: what `run -targets`
refuses, the web UI refuses too, including any extra field such as an
inline `token`. Secrets are entered as **file paths** only (CA file, client
certificate and key, token file). They must lie under `LIBRE_DVT_SECRETS_DIR`
(default `/run/secrets`; mount your files there). The table shows each
target's protocol, endpoint, TLS settings and authentication by path, and
checks the referenced files (`ok`, or the problem by field name). It never
shows a file's contents. Any TLS field turns TLS on, so a forgotten check box
never means clear text.

**Reports.** HTML, JUnit and JSON show, for v0.2 steps:

- the target's protocol, endpoint and class;
- an *Endpoint identity* section: TLS versions and cipher suites seen, the
  server certificate (subject, issuer, subjectAltName, validity, SHA-256,
  verified or not, days left at the start of the run) and the
  Implementation Class UID and Version Name the system announced;
- per step: every exchange (protocol, operation, endpoint, outcome, HTTP
  status or association result, time), TLS details, association results
  with each presentation context, HTTP request and response headers (the
  `Authorization` value is always `[REDACTED]`), body size, hash and a short
  excerpt, multipart parts, per-instance results (remote result and reason,
  pixel comparison, Retrieve URL) and every assertion with expected value,
  actual value and outcome.

JUnit carries the same facts as properties (`target.*`, `identity.*`,
`exchange.N.*`, `instance.N`) and every assertion in `system-out`.
`libredvt run` prints the target and the endpoint identity. Runs stored by
v0.1 load and render as before.

Response excerpts can contain data the target returns. On a client system
that holds real data, set `limits: {max_body_excerpt_bytes: 0}` in the plan
(the bundled *Generic DICOMweb audit* does).

**Bundled example plans** (in `/opt/libredvt/plans`, listed under
*Test plans* with their step types):

| Plan | Target it expects | What it checks |
|---|---|---|
| *LibreDCM smoke (DIMSE + TLS)* (`libredcm-smoke-tls.yaml`) | `libredcm-tls-lab`: dimse, class lab, `tls` with LibreDCM's CA | Verification and CT Image Storage accepted with Explicit VR Little Endian, Modality Worklist rejected; C-ECHO and C-STORE over TLS; plaintext and TLS 1.1 refused |
| *Generic DICOM TLS audit* (`generic-dicom-tls-audit.yaml`) | `dicom-tls-lab`: dimse with `tls` | verified TLS 1.2+ association, C-ECHO, plaintext, TLS 1.0 and TLS 1.1 refused. Sends no images, so it also runs against a production-class target |
| *Generic DICOMweb audit* (`generic-dicomweb-audit.yaml`) | `dicomweb-lab`: dicomweb, class lab, `https` base URL | plaintext and TLS 1.1 refused; STOW-RS of the six sample images with read-back; QIDO-RS at study, series and instance level and a no-match search; WADO-RS with byte-for-byte pixel comparison |

Choose another target when you run a plan if yours has another name. Run
audits only against systems you have written permission to test; plans that
store images need a `lab` target.

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
| `LIBRE_DVT_SECRETS_DIR` | `/run/secrets` | web UI runs: certificate, key and token files a target references must lie here |
| `LIBRE_DVT_SCP_TLS_CERT`, `LIBRE_DVT_SCP_TLS_KEY` | | v0.2: certificate and key of the built-in Storage SCP for a `move` to a TLS target |

## Development

```sh
rws-go test -race ./modules/dvt/...                                     # unit tests, fake engines
docker build -f deploy/docker/Dockerfile.dvt --target test .            # positive controls with the real engines
python3 scripts/qms/trace.py --module libredvt --prefix DVT --check     # requirement trace
```

Changing an engine version (`worker/requirements.txt`) or a base-image digest
requires re-running the tool validation.
