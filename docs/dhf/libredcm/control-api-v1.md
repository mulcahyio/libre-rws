# LibreDCM control API v1: extension for Libre WT 0.2

| | |
|---|---|
| Document | DHF-DCM-CTRL |
| Status | **DRAFT for approval** (2026-11-14, written by Hermes/Claude for human review; scope approved by Patrick Mulcahy on 2026-11-14, issue #20) |
| Software item | LibreDCM, package `internal/control` (served on the admin listener by `platform/runtime`) |
| Requirements | DCM-SRS-077 (existing, Active); DCM-SRS-080…090 and -097 (Active); DCM-SRS-091…096 (Draft) in `SRS.md` §7 |
| Related | `architecture.md` §5.6, `risk-management.md` §2a, `cybersecurity.md` §3a, `../librewt/architecture.md` |

This is the interface contract between LibreDCM and Libre WT. It is a design
input: the endpoints below marked **planned** do not exist yet; those
marked **implemented** are in the code. Each becomes
real in the pull request that turns its requirement from `Draft` to `Active`.

Words used: **shall** is a requirement (it has an ID in the SRS). **Client**
means Libre WT or any other holder of the admin token.

## 1. What stays the same

- Base path `/control/v1` on the admin listener (default `:8081`), HTTP or
  HTTPS as configured (DCM-SRS-042).
- Every request needs `Authorization: Bearer <admin token>`. Without a
  configured token LibreDCM refuses every control request (fail closed,
  DCM-SRS-077). `/healthz`, `/readyz`, `/metrics` and `/version` are unchanged.
- The four conflict endpoints of v0.1 (`/control/v1/conflicts…`) are unchanged.
- LibreDCM stays the source of truth. The client never writes LibreDCM's
  files or database. LibreDCM validates every change and may refuse it.
- No endpoint starts, stops or restarts the process. Container lifecycle
  belongs to Docker and the installer scripts.

## 2. Rules for every endpoint (DCM-SRS-080)

| Rule | Detail |
|---|---|
| Authentication | Admin token, compared in constant time. No token configured: `401` for every control request |
| Format | Request and response bodies are JSON (`application/json`). Unknown request fields are refused (`400`). Request bodies are limited to 64 KiB, except licence install (256 KiB) |
| Caching | Every response carries `Cache-Control: no-store` |
| Errors | `{"error": "<plain text>", "code": "<machine code>"}`. Error text never contains a secret |
| Methods | `GET` never changes state. Every state change is a `POST` |
| Versioning | v1 only grows: new endpoints and new response fields. Fields are never removed or given a new meaning in v1 |
| Discovery | `GET /control/v1` lists the features this LibreDCM supports, so an older LibreDCM and a newer Libre WT can work together |

Status codes: `200` done, `400` malformed request, `401` no or wrong token,
`404` unknown item, `409` state changed since the plan, identity mismatch,
or a request ID that is still running or was used for another request,
`422` request understood but not allowed, `423` blocked by the
licence acknowledgement rule (DCM-SRS-060), `429` busy (another action of
this kind is running), `500` internal error.

Machine codes used by the action endpoints (the `code` field):

| Code | Status | Meaning |
|---|---|---|
| `invalid_body` | 400 | Not JSON, unknown field, wrong type, text after the object, or too large |
| `operator_required`, `operator_invalid`, `reason_required`, `reason_invalid`, `request_id_invalid` | 422 | Action envelope (§2.3) |
| `instance_mismatch` | 409 | §2.2 |
| `request_in_progress`, `request_id_reused` | 409 | §2.3 |
| `busy` | 429 | Another action of this kind runs, or too many recent actions |
| `audit_unavailable` | 500 | The audit record could not be written; the action was not done |
| `unknown_remote_ae` | 404 | C-ECHO test |
| `invalid_target` | 422 | C-ECHO test |
| `not_confirmed`, `plan_digest_required`, `warning_not_acknowledged` | 422 | Reload |
| `plan_changed` | 409 | Reload |
| `invalid_config` | 422 | Reload: the configuration does not load or validate |
| `license_ack_required` | 423 | Reload |
| `license_not_enforced`, `license_key_required`, `license_key_invalid`, `license_site_invalid`, `license_required`, `license_invalid`, `activation_failed` | 422 | Licence |
| `backup_not_configured`, `backup_unsupported`, `insufficient_space`, `space_check_failed` | 422 | Backup |
| `backup_exists` | 409 | Backup |
| `backup_failed` | 500 | Backup |

### 2.1 Discovery (implemented, card WT-0.2/2)

`GET /control/v1`

```json
{
  "api": "v1",
  "features": ["conflicts", "status", "config", "remote-aes", "allow-list",
               "audit", "logs", "echo", "reload", "license", "backup",
               "studies", "send", "config-edit"]
}
```

A node lists a feature only when it serves it. Today the list ends at
`backup`; `studies`, `send` and `config-edit` come with card WT-0.2/4.

### 2.2 Instance identity (DCM-SRS-097)

Every response of §3, §4 and §5 includes an `instance` object:

```json
{"instance": {"node_id": "dcm-1", "ae_title": "LIBREDCM", "version": "0.3.0",
              "config_digest": "sha256:9f2c…"}}
```

`config_digest` is a SHA-256 over the live-reloadable settings that are
running now (remote AEs and allow-list, in a fixed order). Two nodes with the
same digest run the same live settings.

Every `POST` may carry `expect_node_id` and `expect_ae_title`. When either is
present and does not match this node exactly, LibreDCM refuses with `409` and
code `instance_mismatch`, and changes nothing. This is the control for "wrong
instance targeted" (HAZ-015, WT-HAZ-05). **Implemented** (card WT-0.2/3) for
every action and plan endpoint of §4 and for the two conflict `POST`
endpoints of v0.1, which accept the two fields as an addition.

### 2.3 Action envelope (DCM-SRS-086)

Every `POST` in §4 and §5 carries these fields next to its own:

| Field | Required | Meaning |
|---|---|---|
| `operator` | yes | Name of the person, as logged in to Libre WT, 1 to 128 characters. Empty: `422`. Control characters are replaced by spaces |
| `reason` | yes | Free text, 1 to 500 characters. Empty: `422`. Control characters are replaced by spaces |
| `request_id` | yes | Random ID made by the client, 16 to 64 characters from `A-Z a-z 0-9 . _ -`. The same `request_id` sent again within 10 minutes returns the first result and does nothing again |
| `expect_node_id`, `expect_ae_title` | no | See §2.2 |

**Implemented** (card WT-0.2/3). Details:

- **Repeat.** A repeated request gets the stored status and body of the
  first one, with the response header `Idempotent-Replay: true`. This holds
  for failures too: the first result is the result. While the first request
  still runs, the repeat gets `409`, code `request_in_progress`. The same
  `request_id` with another endpoint or another body gets `409`, code
  `request_id_reused`. Exception: the answer of `license/request` holds a
  licence key inside its text and is not kept; a repeat makes a new request
  text (it changes nothing on the node).
- **Bounds.** A node remembers at most 1000 request IDs. When all 1000 are
  still inside their 10 minutes, a new action gets `429`; no result is
  forgotten early. The table is in memory: it is empty after a restart.
- **Audit.** Each action writes two audit records (§6): one before it
  starts (`phase: start`, `outcome: pending`) and one with the outcome
  (`phase: result`). If the first cannot be written, the action is not done
  (`500`, code `audit_unavailable`) and the request may be sent again. A
  request refused before it starts (bad envelope, wrong node, reused ID,
  busy) writes one `SecurityAlert.control-refused` record. These records
  are written at every log level, also with `audit_log_off` (that setting
  is about DICOM traffic records).
- **Unexpected failure.** If an action stops with an internal fault, the
  answer is `500`, code `internal`, without the fault text. The outcome
  record is written with `outcome: serious-failure`. The result is kept like
  any other, so the same `request_id` does not run the action again.
- **The action outlives the request.** When the client goes away, the action
  still finishes and its result is kept for the repeat.
- **Plan endpoints** (`…/plan`) change nothing. They need no operator,
  reason or request ID and write no audit record. They accept those fields,
  so a client may send one body shape, and they check `expect_node_id` and
  `expect_ae_title`.

The operator name is asserted by the client. LibreDCM cannot check it: there
is one shared admin token. This is a known residual risk (see
`cybersecurity.md` TM-18).

### 2.4 Plan, then apply (DCM-SRS-092, -093)

Actions that change data flow or settings have two steps:

1. `POST …/plan` describes exactly what would happen and returns a
   `plan_digest` (SHA-256 over the plan). Nothing changes.
2. `POST …` (apply) must carry that `plan_digest` and `"confirm": true`.
   LibreDCM builds the plan again. If the digest differs, the state has
   changed since the person looked at it: LibreDCM answers `409`, code
   `plan_changed`, and does nothing.

A plan may contain `warnings`, each with a `code`. A warning marked
`"must_acknowledge": true` weakens a risk control (for example: the
allow-list becomes empty, so every peer may connect). Apply is refused with
`422` unless the request lists that code in `acknowledge_warnings`
(DCM-SRS-095).

### 2.5 Resource protection (DCM-SRS-096)

Control requests must never slow DICOM services down. Query results are
bounded and paged. At most one backup and one send run at a time per node; a
second request gets `429`. Ring buffers have fixed sizes. An action has a
timeout and is cancelled when it expires.

## 3. Read-only endpoints (implemented, card WT-0.2/2)

### 3.1 Status: `GET /control/v1/status` (DCM-SRS-081)

```json
{
  "instance": {"node_id": "dcm-1", "ae_title": "LIBREDCM", "version": "0.3.0", "config_digest": "sha256:9f2c…"},
  "time": "2026-11-14T15:04:05Z",
  "uptime_seconds": 86400,
  "backends": {"storage": "fs", "index": "sqlite", "events": "nats"},
  "dicom": {"listen": ":11112", "tls": true, "allow_list_entries": 3, "remote_aes": 2},
  "admin": {"tls": true},
  "ready": true,
  "checks": [{"name": "storage", "ok": true}, {"name": "index", "ok": true},
             {"name": "events", "ok": false, "error": "nats: no servers available"}],
  "license": {"enforced": true, "state": "valid", "licensed": true,
              "ack_required": false, "message": "licensed until 2027-11-01", "days_left": 352},
  "counters": {
    "associations_active": 1, "associations_total": 120, "associations_rejected_total": 4,
    "cstore": {"success": 2040, "failure": 2, "conflict-open": 3},
    "cfind_total": 310, "retrieve_subops": {"success": 800, "failure": 0},
    "outbox_pending": 0, "event_publish_failures_total": 0,
    "conflicts_open": 1
  }
}
```

Counters are the values of the Prometheus metrics, read at request time.
Check errors may name hosts and paths, never credentials (a DSN is reduced
to driver and host).

### 3.2 Configuration: `GET /control/v1/config` (DCM-SRS-082)

Returns the configuration the node is running with, as a flat list:

```json
{
  "instance": {"…": "…"},
  "settings": [
    {"key": "dicom.ae_title", "value": "LIBREDCM", "source": "file", "reloadable": false},
    {"key": "dicom.allowed_aes", "value": ["CT1@10.0.0.5", "MR1"], "source": "overlay", "reloadable": true},
    {"key": "storage.s3.secret_key", "redacted": true, "set": true, "source": "env", "reloadable": false},
    {"key": "index.dsn", "value": "postgres://db.example:5432/libredcm", "redacted_parts": ["user", "password"], "source": "env", "reloadable": false}
  ],
  "restart_required_note": "Settings with reloadable=false take effect after a restart. Restart with the installer script or Docker."
}
```

- `source` is `default`, `file`, `overlay` (§5.3) or `env`.
- **Redaction.** These are never returned, in any field, by any endpoint:
  the admin token and token file content, `storage.s3.access_key`,
  `storage.s3.secret_key`, user and password parts of `index.dsn` and
  `events.url`, TLS private keys, licence keys (shown as last four
  characters only), licence file content. A redacted setting says only
  whether it is set. Redaction works from an allow-list of safe keys: a
  setting that is not on the list is returned as redacted. A new secret
  setting is therefore hidden by default.
- File paths (certificate, key, token file) are returned as paths. Their
  content is not.

### 3.3 Remote AEs and allow-list (DCM-SRS-083)

`GET /control/v1/remote-aes` → `{"remote_aes": [{"name": "VIEWER1", "host": "10.0.0.20", "port": 104, "ae_title": "VIEWER1", "source": "file"}]}`

`GET /control/v1/allow-list` → `{"enforced": true, "entries": [{"rule": "CT1@10.0.0.5", "source": "overlay"}], "note": "An empty list lets every calling AE connect."}`

Both return what is running now, not what is in the file.

### 3.4 Audit records: `GET /control/v1/audit` (DCM-SRS-084)

Query: `type` (event name prefix), `outcome`, `since` (RFC 3339), `limit`
(default 100, at most 500).

LibreDCM keeps the most recent audit records in memory (default 1000 per
node, fixed size). The records are the same ones written to the log. They
never hold more patient data than the log does, and they honour
`audit_omit_patient_id` (DCM-SRS-032). With `audit_log_off` the ring is
empty and the response says so. The ring is lost at restart. The log
collector stays the audit record of the site; the ring is a convenience view.

### 3.5 Log lines: `GET /control/v1/logs` (DCM-SRS-085)

Query: `level` (minimum: `debug`, `info`, `warn`, `error`), `since`, `limit`
(default 200, at most 1000). Fixed-size ring (default 2000 lines). Audit
records are left out (use §3.4). Lines are returned as written to the log.
LibreDCM does not log secrets; the endpoint adds no new data.

## 4. Actions (implemented, card WT-0.2/3)

All use the action envelope (§2.3). Every response holds the `instance`
block (§2.2).

### 4.1 C-ECHO test: `POST /control/v1/echo` (DCM-SRS-087)

Request: `{"remote_ae": "VIEWER1"}` or `{"host": "10.0.0.20", "port": 104, "ae_title": "VIEWER1", "tls": false}`, plus the envelope.

Response: `{"ok": true, "duration_ms": 42, "target": "10.0.0.20:104", "called_ae": "VIEWER1", "calling_ae": "LIBREDCM", "tls": false, "source": "configured"}` or `{"ok": false, "stage": "connect", "error": "connection refused", …}`.

- A test that ran and failed is a result: status `200` with `ok: false`.
- `stage` is `connect` (TCP or TLS), `association` (DICOM negotiation) or
  `echo` (the C-ECHO exchange). `error` is one of a fixed set of short
  texts (for example "connection refused", "no answer (timed out)",
  "association rejected or aborted by the peer"). The peer's own bytes and
  the operating system's error text are never returned or audited.
- The calling AE title is this node's AE title: the test shows what the
  peer will see on a C-MOVE.
- Timeout 15 seconds. At most two tests run at once on a node (`429`).
- An unknown `remote_ae` name is `404`, and no connection is made.
- Give either `remote_ae` or `host`, `port`, `ae_title` (and `tls`), not
  both (`422`, `invalid_target`). `host` is a host name or IP address
  (letters, digits, `.`, `-`, `_`, or an IP literal; no port, path or
  user); `port` 1 to 65535; `ae_title` 1 to 16 printable characters.
- `tls` applies to the free target only (configured remote AEs have no TLS
  setting today). The peer certificate is not checked: the test shows that
  the peer can be reached, not who it is. The response says
  `"tls_verified": false`.
- The test opens an association for the Verification SOP class only and
  sends no data set, so no patient data.

### 4.2 Reload: `POST /control/v1/reload/plan` and `POST /control/v1/reload` (DCM-SRS-088)

Same code path as SIGHUP (DCM-SRS-016), including the licence
acknowledgement block (DCM-SRS-060, answered as `423` by plan and by
reload). There is one reloader in the node; SIGHUP and this endpoint both
call it, one at a time.

Plan response:

```json
{"plan": {
  "changed": true,
  "remote_aes": {"added": [{"name": "VIEWER2", "host": "10.1.2.3", "port": 104, "ae_title": "VIEWER2", "source": "file"}],
                 "removed": [], "changed": [{"name": "VIEWER1", "from": {"…": "…"}, "to": {"…": "…"}}]},
  "allow_list": {"added": ["US1@192.168.7.0/24"], "removed": [], "enforced_before": true, "enforced_after": true},
  "warnings": [{"code": "remote_ae_retargeted", "message": "…", "must_acknowledge": true}],
  "plan_digest": "sha256:…"}}
```

- `plan_digest` is a SHA-256 over the settings that run now and the
  settings the file would load. It changes when either changes.
- Reload request: the envelope plus `"confirm": true`, `"plan_digest"` and,
  when the plan has warnings, `"acknowledge_warnings": ["code", …]`.
  Without `confirm`: `422`. Without the digest: `422`. Digest of another
  state: `409`, `plan_changed`. Load, digest check and apply are one step
  inside the reloader: no other reload can come in between.
- Warnings that must be acknowledged (DCM-SRS-095, the part built so far):
  `allow_list_emptied`, `remote_ae_removed`, `remote_ae_retargeted`. A
  SIGHUP on the host is not asked for an acknowledgement: the person at
  the host edited the file and sent the signal.
- A configuration that fails to load or validate returns `422`,
  `invalid_config`, with the loader's error text, from plan and from
  reload, and leaves the running settings unchanged.
- A reload that changes nothing answers `200` with `"changed": false`.
- Reload response: `applied`, `changed`, the plan, `config_digest_before`
  and `config_digest_after`.

### 4.3 Licence (DCM-SRS-089)

| Endpoint | Does | Notes |
|---|---|---|
| `GET /control/v1/license` | Status under `license`: `enforced`, `state`, `licensed`, `ack_required`, `message`, `days_left`, `machine_fingerprint`, `customer`, `site`, `term`, `expires`, `key_last4`, `acknowledgement` (`by`, `at`, `valid_until`, `valid`) | Source builds answer `{"enforced": false}` |
| `POST /control/v1/license/ack` | Records the acknowledgement by `operator` | Same effect as `-license-ack NAME` (DCM-SRS-061); the node reports it at once |
| `POST /control/v1/license/request` | Returns an activation request text for offline activation in `request` | Body: optional `key`, `site`. The text holds the key in its encoded block (the licence portal needs it); the answer is not kept for a repeat |
| `POST /control/v1/license/install` | Installs a licence file sent as text in `license` (body limit 256 KiB) | Signature, product, machine and dates checked by the existing code; a refused licence answers `422`, `license_invalid`, and the installed licence file is not touched |
| `POST /control/v1/license/activate` | Online activation with `key`, `site` | 30-second timeout; one at a time (`429`); a refusal or an unreachable server answers `422`, `activation_failed`, and nothing changes |

A licence key in a request is never logged, never written to an audit
record and never echoed back in full (`key_last4` only; a key that an error
text of the licence server repeats is removed from the response and the
audit record). The licence file content is never returned. These endpoints
call the existing licensing code. They live outside the DIMSE gateway: no
licence endpoint and no licence state can refuse, delay or interrupt DICOM
services (DCM-SRS-058). `TestGatewayHasNoLicenseDependency` now also refuses
an import of the control package by the gateway. In a source build every
licence action answers `422`, `license_not_enforced`.

### 4.4 Backup: `GET /control/v1/backups` and `POST /control/v1/backup` (DCM-SRS-090)

`POST /control/v1/backup` takes only the envelope. There is no path field.

- LibreDCM writes the snapshot into the directory set in its own
  configuration (`control.backup_dir`). Not configured: `422`.
  The setting is `control.backup_dir` in the file or
  `LIBRE_DCM_CONTROL_BACKUP_DIR`. It needs a restart to change.
- LibreDCM makes the file name: `index-<node_id>-<UTC yyyymmddThhmmssZ>.db`.
  In the node ID every character other than a letter, a digit, `_` and `-`
  becomes `_`, so the name can never hold a path element.
  The file is created exclusively (`O_EXCL`, mode `0600`): an existing file
  or symbolic link with that name is never written to or replaced (`409`,
  `backup_exists`).
- SQLite index only. With PostgreSQL the answer is `422` with a pointer to
  the database's own tools.
- Before starting, LibreDCM checks free space in the directory (at least the
  current index size plus 10 %). Too little, or not readable: `422`,
  nothing written.
- One backup at a time (`429`). Timeout 10 minutes. A snapshot that fails or
  does not finish leaves no file.
- Response: `{"file": "index-dcm-1-20261114T150405Z.db", "bytes": 1048576, "sha256": "…", "duration_ms": 900, "note": "…"}`.
  The checksum is computed from the finished file.

`GET /control/v1/backups` lists the regular files in that directory that
match the name pattern, newest first, at most 500: name, size, time. No
download and no delete endpoint exist: a backup holds patient data, and
removing backups is a deliberate act on the host.

The snapshot covers the index. Images must be backed up by the site
(limitation L5 in `intended-use.md`). The response repeats this note.

## 5. Data and configuration actions (planned, card WT-0.2/4)

### 5.1 Study query: `GET /control/v1/studies` (DCM-SRS-091)

Query: `level` (`patient`, `study`, `series`, `image`), matching keys
(`PatientID`, `PatientName`, `StudyDate`, `AccessionNumber`,
`StudyInstanceUID`, `SeriesInstanceUID`, `ModalitiesInStudy`), `limit`
(default 50, at most 200), `cursor`.

- Same matching rules as C-FIND (DCM-SRS-006…009): one shared query function.
- Same conflict visibility as C-FIND (DCM-SRS-073): with visibility `hidden`,
  held instances are not returned.
- A query with no matching key is refused (`422`): no listing of the whole
  archive.
- When more rows exist, the response says so (`"more": true` and a
  `cursor`). The result is never cut silently.
- LibreDCM does not write the query values of this request to its log; they
  appear only in the audit record, which honours patient-ID omission.
- Each query writes a `Query` audit record with the operator (sent in the
  header `X-Libre-Operator`; required for this endpoint).

### 5.2 Send: `POST /control/v1/send/plan` and `POST /control/v1/send` (DCM-SRS-092)

Request: `{"destination": "VIEWER1", "study_instance_uid": "1.2.3", "series_instance_uids": [], "patient_id": "P1"}`, plus the envelope; apply adds `plan_digest` and `confirm`.

- `destination` must be the name of a configured remote AE. There is no
  free host/port: images only go to configured destinations (DCM-SRS-011).
  Unknown name: `404`, nothing sent.
- `patient_id` is required and must match the indexed Patient ID of the
  study. A mismatch is refused with `422`. The person must name the patient
  as well as the study.
- The plan shows: destination name, host, port and AE title; patient ID and
  name; study date and description; number of series and instances.
- Held (hidden) conflict instances are never sent.
- Apply uses the existing C-MOVE send path (stored encoding, each instance
  on its own). One send at a time per node. At most 5000 instances per send.
- Response: counts of completed, failed and warning sub-operations and the
  SOP Instance UIDs that failed.
- Audit: `DICOMInstancesTransferred.sent` with operator, reason,
  destination, study and patient ID (honouring `audit_omit_patient_id`).

### 5.3 Configuration edit (DCM-SRS-093, -094, -095)

Only live-reloadable settings can be edited: `dicom.remote_aes` and
`dicom.allowed_aes`. A request that names any other key is refused with
`422`, code `restart_required`.

**Overlay file.** LibreDCM owns one extra file next to its data
(`control.overlay_file`, default `<data dir>/config-overlay.yaml`). The
administrator's own `config.yaml` is never rewritten. Loading order:
defaults, then `config.yaml`, then the overlay, then environment variables.
Environment variables still win (DCM-SRS-050). A setting fixed by an
environment variable cannot be edited: the plan refuses with `422`, code
`set_by_environment`, and says which variable.

| Endpoint | Does |
|---|---|
| `POST /control/v1/config/plan` | Body: the full new value of one or both settings. Returns the validated difference (added, removed, changed entries), warnings and `plan_digest`. Nothing changes |
| `POST /control/v1/config` | Apply. Needs `plan_digest`, `confirm`, and `acknowledge_warnings` when required. Steps: validate with the normal loader, keep the current overlay as the previous version, write the new overlay atomically (temporary file, fsync, rename), run the normal reload. If the reload fails, the previous overlay is put back and the running settings are unchanged |
| `GET /control/v1/config/history` | The current and the kept previous overlay versions: time, operator, reason, digest |
| `POST /control/v1/config/rollback/plan`, `POST /control/v1/config/rollback` | Put the previous overlay back, through the same plan and apply steps |

Warnings that must be acknowledged (`must_acknowledge: true`):

| Code | When |
|---|---|
| `allow_list_emptied` | The new allow-list is empty, so every calling AE may connect |
| `allow_list_entry_removed` | A calling AE that stored or queried in the last 24 hours (per the audit ring) would be refused |
| `remote_ae_removed` | A C-MOVE destination is removed |
| `remote_ae_retargeted` | An existing destination name gets a new host, port or AE title |

An invalid allow-list rule or remote AE entry is refused at plan time
(`422`); it never reaches the running node. The licence acknowledgement
block applies as for reload (`423`).

In a cluster each node has its own overlay file. A change is made node by
node. `config_digest` (§2.2) lets the client show when nodes differ.

## 6. Audit records written by the control API

All are structured log lines with `audit=true`, like the existing ones.

| Event name | Written by | Fields besides operator, reason, request_id, outcome |
|---|---|---|
| `PatientRecord.conflict-resolved` | conflict resolve (existing) | action, conflict |
| `Query` | `GET /studies` | level, keys (values honour patient-ID omission), rows |
| `DICOMInstancesTransferred.sent` | send | destination, study, patient ID, counts |
| `SecurityAlert.configuration-changed` | config apply, rollback, reload | difference (names added, removed, changed), digests before and after, acknowledged warnings |
| `SecurityAlert.license-action` | licence ack, install, activate, request | `license_action`, licence state before and after, `key_last4` (never the key) |
| `SecurityAlert.backup` | backup | file name, bytes, checksum |
| `NetworkEntry.verification` | C-ECHO test | target, called AE, source (configured or ad-hoc), stage, result |
| `SecurityAlert.control-refused` | any refused `POST` (identity mismatch, plan changed, missing operator or reason, unacknowledged warning, reused request ID, busy, malformed body) | endpoint, code |

Every record of an action endpoint also carries `component: control`,
`endpoint`, `phase` (`start` or `result`) and `action`. Text values pass
the secret filter of §3.2 before they are written.

Reads of status, configuration, audit and logs are not audited by LibreDCM
(they are frequent and hold no more than the token holder can already read).
Libre WT audits who opened which page.

## 7. What the API deliberately does not offer

| Not offered | Why |
|---|---|
| Start, stop, restart | Belongs to Docker and the installer; a remote stop is a remote way to make images unavailable (HAZ-004) |
| Editing restart-required settings (AE title, ports, TLS, storage, index, events, conflict policy, limits) | A wrong value can stop the node from starting, with nobody at the host to fix it (HAZ-004, HAZ-001) |
| `-gen-cert` | Writes a private key and needs a restart; stays on the host |
| Client-chosen backup path, backup download or delete | Path traversal, overwrite and patient-data disclosure (HAZ-001, HAZ-009) |
| Send to an address that is not configured | Images only go to configured destinations (HAZ-005) |
| Reading secrets | HAZ-009 |
| Uploading or deleting images | Not a LibreDCM function today |

## 8. Open decisions for Patrick

Listed with the Libre WT decisions in `../librewt/architecture.md` §8
(D1…D22). Those that shape this API: D3 (editable settings), D4 (no remote
restart), D6 (send only to configured destinations; ad hoc C-ECHO target),
D8 (backup directory, no download), D9 (in-memory rings), D10 (shared token,
operator asserted), D12 (plan digest and request ID), D15 (no `-gen-cert`).
Added while building the actions (card WT-0.2/3): D17 (reload needs a plan),
D18 (two audit records per action), D19 (plan endpoints need no operator),
D20 (backup directory off by default), D21 (C-ECHO TLS without certificate
check), D22 (licence acknowledgement through the API).
