# LibreDVT target and plan schema (v0.2)

| | |
|---|---|
| Document | DHF-DVT-SCHEMA |
| Status | **DRAFT for approval** (written 2026-11-19 by Hermes/Claude for issue #34) |
| Related | `SRS.md` §3 and §6–§12, `evidence-model.md`, `architecture.md`, `cybersecurity.md` |

This document fixes the v0.2 schema before code, so the later cards
implement one design. It extends the v0.1 plan format (`architecture.md`
§4). Every v0.1 plan and target stays valid and means the same thing
(DVT-SRS-142, -110).

## 1. Rules for every file

- **Strict parsing.** YAML plans and target files are decoded with
  known-field checking; the JSON target registry is decoded with
  unknown fields disallowed. An unknown field, a wrong type, a field that
  belongs to another protocol or step type, a missing required field, or a
  second YAML document is an error before any step runs (DVT-SRS-040, -140,
  -142).
- **No insecure switch.** There is no field that turns off certificate or
  host-name verification, or allows TLS below 1.2. Names such as
  `insecure_skip_verify`, `verify`, `check_hostname` or `allow_insecure` are
  unknown fields and rejected like any other (DVT-SRS-103).
- **Secrets by reference only.** A secret is a path to a file
  (`token_file`, `client_key_file`). Fields such as `token`, `password`,
  `bearer` or `private_key` do not exist and are rejected (DVT-SRS-141). The
  file is read when the step starts; a missing, empty or non-regular file
  fails the step before any connection.
- **Paths.** Secret and certificate paths must be absolute. In the web UI
  they must lie under the secrets directory (default `/run/secrets`, setting
  `secrets_dir`), so a web user cannot point LibreDVT at an arbitrary file.

## 2. Targets

A target is one system under test. The web UI stores targets in the
registry (`targets.json` in the data directory). `libredvt run -targets
FILE` reads the same records from a YAML file (`targets:` list) (DVT-SRS-146).

### 2.1 Common fields

| Field | Type | Required | Default | Rule |
|---|---|---|---|---|
| `name` | string | yes | | 1–64 of `A-Z a-z 0-9 . _ -`, unique |
| `class` | `lab` or `production` | no | `production` | missing means production (DVT-SRS-020) |
| `protocol` | `dimse` or `dicomweb` | no | `dimse` | v0.1 records have none and stay DIMSE (DVT-SRS-110) |
| `tls` | TLS block (§2.4) | no | none | DIMSE: TLS on when present. DICOMweb: only with an `https` base URL |

### 2.2 DIMSE target (`protocol: dimse`)

| Field | Type | Required | Default | Rule |
|---|---|---|---|---|
| `host` | string | yes | | DNS name or IP address, no spaces |
| `port` | integer | yes | | 1–65535 |
| `called_aet` | string | yes | | AE title rules of `plan.ValidAET` |
| `calling_aet` | string | no | `LIBREDVT` | AE title rules |

`base_url` and `auth` are not allowed on a DIMSE target.

### 2.3 DICOMweb target (`protocol: dicomweb`)

| Field | Type | Required | Default | Rule |
|---|---|---|---|---|
| `base_url` | string | yes | | `http` or `https`; host; optional port and path; no user-info, query or fragment (DVT-SRS-110). Requests go to paths under it |
| `auth.type` | `none` or `bearer` | no | `none` | No other type in v0.2 (decision D2) |
| `auth.token_file` | absolute path | with `bearer` | | file holding the token on one line; never echoed (DVT-SRS-111, -141) |

Rules: `bearer` with an `http` base URL is allowed only when the host is a
loopback address (DVT-SRS-112). A `tls` block with an `http` base URL is
rejected. `host`, `port`, `called_aet` and `calling_aet` are not allowed on a
DICOMweb target.

### 2.4 TLS block

| Field | Type | Required | Default | Rule |
|---|---|---|---|---|
| `min_version` | `"1.2"` or `"1.3"` | no | `"1.2"` | nothing lower exists (DVT-SRS-100) |
| `ca_file` | absolute path | no | image trust store | PEM CA certificates the target's chain must reach (DVT-SRS-101) |
| `server_name` | string | no | target host | name checked against the certificate's subjectAltName (DVT-SRS-102) |
| `client_cert_file` | absolute path | no | | PEM; both or neither of the pair (DVT-SRS-104) |
| `client_key_file` | absolute path | no | | PEM private key; secret |
| `move_client_cert` | `required` or `optional` | no | `required` | DIMSE only: whether the built-in Storage SCP demands the target's client certificate during a TLS move (DVT-SRS-108) |

### 2.5 Example target file

```yaml
targets:
  - name: vendor-pacs-lab
    class: lab
    protocol: dimse
    host: pacs.lab.example
    port: 2762
    called_aet: VENDORPACS
    tls:
      ca_file: /run/secrets/client-lab-ca.pem
      client_cert_file: /run/secrets/libredvt-client.pem
      client_key_file: /run/secrets/libredvt-client.key
  - name: vendor-web-lab
    class: lab
    protocol: dicomweb
    base_url: https://pacs.lab.example/dicom-web
    auth:
      type: bearer
      token_file: /run/secrets/vendor-web-token
    tls:
      ca_file: /run/secrets/client-lab-ca.pem
```

## 3. Plans

### 3.1 Plan fields

| Field | Type | Required | Default | Rule |
|---|---|---|---|---|
| `name` | string | yes | | as v0.1 |
| `target` | string | for network steps | | target name; the CLI may override it |
| `timeout` | duration | no | `30s` | per network operation, 1 s – 30 min (DVT-SRS-029) |
| `limits` | limits block (§3.3) | no | defaults | DVT-SRS-154 |
| `steps` | list | yes | | at least one |

### 3.2 Steps

Common step fields stay as in v0.1: `id`, `type`, `verifies`. A step type
must match the target's protocol (DVT-SRS-142).

| Type | Protocol | Lab only | Fields (all others rejected) |
|---|---|---|---|
| `validate` | none | no | `files` |
| `echo` | dimse | no | none |
| `store` | dimse | yes (DVT-SRS-028) | `files` |
| `find` | dimse | no (v0.1 rule; open decision) | `studies` or `from_store` |
| `move` | dimse | yes (DVT-SRS-028) | `from_store` or `files`/`studies`, `compare_pixels` |
| `reject` | dimse | no | `calling_aet`, `called_aet` |
| `associate` | dimse | no | `contexts`, `expect`, `async_ops` |
| `tls_reject` | dimse or dicomweb | no | `mode`, `tls_version` |
| `qido` | dicomweb | yes (DVT-SRS-134) | `level`, `study`, `series`, `query`, `includefield`, `fuzzymatching`, `limit`, `offset`, `expect_uids`, `from_store`, `expect_none`, `require_attributes` |
| `wado` | dicomweb | yes (DVT-SRS-134) | `level`, `study`, `series`, `instance`, `from_store`, `transfer_syntax`, `compare_pixels`, `metadata`, `compare_attributes`, `validate`, `files` |
| `stow` | dicomweb | yes (DVT-SRS-134) | `files`, `study`, `batch_size`, `allow_warnings`, `verify_retrieve` |

#### `associate` (DVT-SRS-090…-096)

| Field | Type | Rule |
|---|---|---|
| `contexts` | list, 1–128 | each: `abstract_syntax` (UID), `transfer_syntaxes` (1–16 UIDs), `expect` (`accepted` or `rejected`), optional `expect_transfer_syntax` (UID, only with `accepted`), optional `roles` (`scu`, `scp` booleans) with `expect_roles` |
| `expect.association` | `accepted` or `rejected` | default `accepted`; with `rejected`, `contexts` may be a single context and `expect.reject` is required |
| `expect.reject` | `result`, `source`, `reason` integers | PS3.8 Table 9-21 values |
| `expect.max_pdu` | integer | optional |
| `expect.impl_class_uid` | UID | optional |
| `expect.impl_version` | string | optional |
| `async_ops` | `invoked`, `performed` integers 0–65535 | optional; proposes the Asynchronous Operations Window |

#### `tls_reject` (DVT-SRS-105, -106)

| Field | Type | Rule |
|---|---|---|
| `mode` | `plaintext`, `tls_version` or `no_client_cert` | required |
| `tls_version` | `"1.0"` or `"1.1"` | required with `mode: tls_version`, not allowed otherwise |

The target must have TLS (a `tls` block or an `https` base URL), otherwise
the plan is rejected.

#### `qido` (DVT-SRS-113…-118)

| Field | Type | Rule |
|---|---|---|
| `level` | `studies`, `series` or `instances` | required |
| `study`, `series` | UID | optional; narrow the search resource |
| `query` | map of keyword or 8-hex-digit tag to string | keywords from the image's DICOM dictionary; unknown keywords rejected |
| `includefield` | list of keywords or tags, or `all` | optional |
| `fuzzymatching` | boolean | optional |
| `limit`, `offset` | integers | `limit` 1–1000; `offset` ≥ 0 and only with `limit` |
| `expect_uids` | list of UIDs | unique keys of the level expected in the result |
| `from_store` | boolean | expect what earlier `store`/`stow` steps sent |
| `expect_none` | boolean | expect no match; excludes `expect_uids` and `from_store` |
| `require_attributes` | list of keywords or tags | each must be present in every returned object |

One of `expect_uids`, `from_store` or `expect_none` is required.

#### `wado` (DVT-SRS-119…-127)

| Field | Type | Rule |
|---|---|---|
| `level` | `study`, `series` or `instance` | required unless `from_store` |
| `study`, `series`, `instance` | UID | as needed by the level |
| `from_store` | boolean | retrieve and expect what earlier steps sent |
| `files` | glob list | the instances expected back, when not `from_store` |
| `transfer_syntax` | UID or `*` | optional; lossy syntaxes with `compare_pixels` rejected (DVT-SRS-123) |
| `compare_pixels` | boolean | as DVT-SRS-025 |
| `metadata` | boolean | retrieve the metadata resource instead of instances |
| `compare_attributes` | list of keywords | with `metadata` only |
| `validate` | boolean | run the validation engines on each instance |

#### `stow` (DVT-SRS-128…-136)

| Field | Type | Rule |
|---|---|---|
| `files` | glob list | required; each must match at least one file |
| `study` | UID | optional; store into this study resource |
| `batch_size` | integer 1–100 | default 10 parts per request |
| `allow_warnings` | boolean | default false (DVT-SRS-130) |
| `verify_retrieve` | boolean | re-retrieve and compare (DVT-SRS-132) |

### 3.3 Limits (DVT-SRS-154, -117)

| Field | Default | Allowed |
|---|---|---|
| `max_response_bytes` | 512 MiB | 1 MiB – 4 GiB |
| `max_parts` | 1000 | 1 – 10000 |
| `max_part_bytes` | 256 MiB | 1 KiB – 2 GiB |
| `max_json_bytes` | 32 MiB | 64 KiB – 256 MiB |
| `max_pages` | 100 | 1 – 1000 |
| `max_body_excerpt_bytes` | 64 KiB | 0 – 1 MiB (how much raw body the evidence keeps) |

Reaching a limit fails the step; it never truncates and passes.

### 3.4 Example DICOMweb plan

```yaml
name: Vendor DICOMweb audit
target: vendor-web-lab
timeout: 60s
steps:
  - id: tls-plaintext-refused
    type: tls_reject
    mode: plaintext
  - id: stow-fixtures
    type: stow
    files: [fixtures/*.dcm]
  - id: qido-studies
    type: qido
    level: studies
    from_store: true
    require_attributes: [StudyInstanceUID, PatientID, StudyDate]
  - id: wado-and-compare
    type: wado
    from_store: true
    compare_pixels: true
```

## 4. LibreDVT settings for v0.2

Added to the LibreDVT configuration (`libredvt.yaml` or environment), not to
targets or plans:

| Setting | Environment | Default | Use |
|---|---|---|---|
| `scp_tls_cert` | `LIBRE_DVT_SCP_TLS_CERT` | none | server certificate of the built-in Storage SCP for TLS moves (DVT-SRS-108) |
| `scp_tls_key` | `LIBRE_DVT_SCP_TLS_KEY` | none | its private key (secret, file path) |
| `secrets_dir` | `LIBRE_DVT_SECRETS_DIR` | `/run/secrets` | directory web-managed targets may reference |

Empty environment values count as unset.

## 5. Implementation notes (card DVT-0.2/2, 2026-11-19)

Written by Hermes/Claude while implementing this schema. They settle
details the tables above leave open. **DRAFT for approval.**

1. **v0.1 step types keep v0.1 rules.** A v0.1 field with an empty or
   false value on another v0.1 step type is still accepted, as in v0.1.
   A v0.2 field on a v0.1 step type is rejected by its key. For v0.2 step
   types every key outside the table in §3.2 is rejected, whatever its
   value (`fuzzymatching: false` on a `wado` step fails).
2. **Rejected association.** With `expect.association: rejected`, the
   contexts carry no `expect` (nothing is negotiated) and `expect.reject`
   is required. `expect_transfer_syntax` must be one of the context's
   proposed transfer syntaxes.
3. **QIDO resources.** `level: studies` allows neither `study` nor
   `series`; `level: series` allows `study` only; `level: instances` allows
   both, and `series` needs `study`. Query values are at most 1024
   printable characters.
4. **Keywords.** Go checks keyword and tag syntax only. Whether a keyword
   exists in the DICOM dictionary is checked by the worker before any
   request (card DVT-0.2/4); Go has no DICOM dictionary (DVT-SRS-080).
5. **WADO.** `files` (the instances expected back) is required unless
   `from_store`. `metadata` excludes `compare_pixels`, `transfer_syntax` and
   `validate`.
6. **Target files.** The `calling_aet` default `LIBREDVT` applies to target
   files. Registry records from the web form always carry a calling AE
   title, as in v0.1.
7. **Bearer token file.** One line (a trailing newline is removed), at
   least 16 characters from the RFC 6750 token set
   (`A-Z a-z 0-9 - . _ ~ + /`, trailing `=`). Shorter or other values are
   refused. Reason: the artefact scan must find the token reliably, and
   these characters are never changed by JSON, HTML or XML escaping, so the
   scan finds it in every artefact (DVT-SRS-152). Spaces and line breaks
   are also refused so a token cannot inject an HTTP header.
8. **Referenced files** (CA, client certificate, key, token) must be
   regular, non-empty files of at most 1 MiB. The web UI's runs also
   require them under `secrets_dir`; the command line does not.
9. **Until the engines exist** (cards DVT-0.2/3 and /4), the new step
   types are parsed and checked but fail as an engine error without
   sending anything, and a v0.1 DIMSE step against a target with a `tls`
   block fails the same way, so LibreDVT never falls back to clear text.
