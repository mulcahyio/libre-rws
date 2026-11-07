# Changelog

All notable changes to Libre RWS (formerly RWS) are recorded here. Versions follow
[Semantic Versioning](https://semver.org) with a `-beta.N` pre-release label
until a release is approved for production use (see "Versioning" in
[README.md](README.md)).

Libre RWS is developed by Patrick Mulcahy, Mulcahy.io (dev@mulcahy.io).

## Unreleased

### Changed
- Product renamed: RWS is now **Libre RWS**, the DICOM module (rws-dicom) is
  **Libre DCM**, and the planned management console is **Libre WT** (Libre
  Watch Tower). Tagline: "Open and extensible." The documentation uses the
  new names; see "Naming" in [README.md](README.md).
- The GitHub repository moved from `StoreHouseAdmin/RWS` to
  [`mulcahyio/libre-rws`](https://github.com/mulcahyio/libre-rws). GitHub
  redirects the old URLs; update your `git remote` to the new one.
- Code identifiers are unchanged in this release: the `rws-dicom` binary and
  installer scripts, the `rws/dicom` image, `RWS_*` environment variables,
  `rws.dicom.*` event subjects and `rws_*` metrics. Existing installs keep
  working without changes.

### Added
- `-backup PATH` flag: writes a consistent snapshot of the SQLite index while
  the module keeps running (`VACUUM INTO`). PostgreSQL indexes use `pg_dump`.
- Event outbox: the `instance.stored` event is committed with the instance
  and published by a relay with retries, so a NATS outage delays events
  instead of losing them. New metric `rws_dicom_outbox_pending`. Index schema
  version 2 (adds `event_outbox`; applied automatically on start).
- Calling-AE allow-list: `allowed_aes` / `RWS_DICOM_ALLOWED_AES` accepts
  `AE`, `AE@IP` or `AE@CIDR` entries. Other peers get A-ASSOCIATE-RJ
  ("calling AE not recognized"), counted in
  `rws_dicom_associations_rejected_total`. Empty (the default) keeps the old
  behaviour. An invalid list rejects everything rather than allowing all.
- Admin endpoint token: with `RWS_ADMIN_TOKEN` (or `RWS_ADMIN_TOKEN_FILE`
  for Docker secrets) set, `/metrics` and `/version` need
  `Authorization: Bearer <token>`. `/healthz` and `/readyz` stay open for
  probes, but `/readyz` hides check details without the token. An unreadable
  token file refuses requests rather than serving them. The workstation
  scripts send the token from `.env`.
- Live reload: `SIGHUP` (`docker kill -s HUP rws-dicom`) re-reads the config
  and applies `remote_aes` and `allowed_aes` without dropping associations.
  A config that fails to load keeps the running settings.
- C-FIND: `PatientName` matches case-insensitively, including non-ASCII
  letters (`müller*` finds `MÜLLER^HANS`); turn off with
  `RWS_DICOM_FIND_CASE_SENSITIVE_NAMES=true`. `StudyTime` now matches single
  times and ranges (`0800-1200`, `0830-`, `-1700`, old `08:30` style); a
  partial bound covers its whole period. Responses containing non-ASCII text
  are labelled `ISO_IR 192` (UTF-8) instead of echoing a character set that
  didn't match the bytes. Index schema version 3 (adds a folded name column,
  filled in for existing studies on first start).
- S3 storage backend (`RWS_STORAGE_DRIVER=s3`) for AWS S3 and S3-compatible
  stores (MinIO, Ceph, NAS appliances). Uploads carry a Content-MD5 so a
  corrupted upload is rejected. The store contract and the full DIMSE
  end-to-end tests run on S3 too (`make test-s3`, SeaweedFS in Docker). New
  dependency: `github.com/minio/minio-go/v7` (Apache-2.0).
- Audit log: structured `audit=true` records for received instances,
  queries, exports (C-MOVE/C-GET, with destination, studies and patient IDs)
  and rejected associations, named after the DICOM audit message types
  (PS3.15 A.5) so they can become ATNA syslog messages later. Turn off with
  `RWS_DICOM_AUDIT_LOG_OFF=true`.
- Configurable patient IDs: `LIBRE_DCM_EVENTS_OMIT_PATIENT_ID` and
  `LIBRE_DCM_AUDIT_OMIT_PATIENT_ID` leave the patient ID out of bus events
  and audit records (default: included).
- Configurable TLS: admin endpoint over HTTPS (`LIBRE_DCM_ADMIN_TLS_CERT` /
  `_KEY`; the container health check follows), `-gen-cert DIR` creates a
  self-signed certificate (kept unless `-gen-cert-force`), and `-echo` uses
  DICOM TLS when the node has a certificate (`-echo-tls`).
- Index conformance suite: the same query, outbox and upgrade tests run
  against SQLite and PostgreSQL (`make test-postgres`).
- Stored instances and `instance.stored` events now record the sending AE
  title (`source_ae`), which was always empty before.
- `BACKLOG.md`: the clustering change backlog (storage abstraction, shared
  catalog, eviction, replication, role-based nodes).

### Changed
- **Code identifiers renamed** (decided 2026-11-07): program `LibreDCM`
  (was `rws-dicom`), image `libre/dcm` (was `rws/dicom`), settings
  `LIBRE_DCM_*` (was `RWS_DICOM_*` / `RWS_*`), event subjects `libre.dcm.*`
  (was `rws.dicom.*`), metrics `libre_dcm_*` (was `rws_dicom_*`), default
  AE title `LIBREDCM`, Go module `github.com/mulcahyio/libre-rws`. Backward
  compatible: `RWS_*` settings are still read (a warning names the
  replacement), events are also published under the old subjects
  (`LIBRE_DCM_EVENTS_LEGACY_SUBJECTS=false` stops it), and an upgraded install
  keeps its `RWS_DICOM` stream. Metric names changed without aliases.
- Workstation package: `LibreDCM.sh` / `LibreDCM.ps1` (were `rws-dicom.*`),
  container `libredcm`. `install` now asks the install-time choices (storage:
  this PC or S3; TLS: off, self-signed or your own files; patient IDs in
  events and audit log) and generates an admin token; `configure` asks again;
  `-Yes` / `LIBREDCM_YES=1` for unattended installs. `upgrade` moves a 0.1.x
  install over: renames `.env` keys (backup kept), removes the old container
  and keeps the `rws-dicom-data` volume.
- An environment variable that is set but empty no longer overrides the
  config file (compose passes every variable through, so empty values were
  wiping file settings).

### Fixed
- Cluster HAProxy: a gateway that has just died no longer fails new
  associations for ~10 s; connects are retried on another node
  (`retries 3`, `option redispatch`) and health checks run every 2 s.
- SQLite wildcard queries were case-insensitive for ASCII while PostgreSQL's
  were case-sensitive. Both are now case-sensitive (found by the conformance
  suite); name matching uses its own case-insensitive path.
- Workstation `backup` is now consistent while the module runs: it snapshots
  the index with `-backup`, then copies the snapshot and the images
  ([#1](https://github.com/mulcahyio/libre-rws/issues/1)). The stop/backup/start
  workaround is no longer needed.
- `install.cmd` no longer shows as modified in a fresh clone (`.gitattributes`
  now leaves it as stored, with CRLF line endings).
- The workstation `.env` and `backups/` folder are git-ignored, so local
  settings and backups can't be committed by mistake.

### Documentation
- `INSTALL.md`: Linux host firewalls can block C-MOVE to a viewer on the same
  PC (`host.docker.internal`); how to allow it.
- `INSTALL.md`: how to take a consistent backup (see Known issues).

### Tested (2026-11-07)
- Workstation install, upgrade (index v2 to v3 in place), `backup` under load
  and with a NATS outage: 12 events stored during the outage were all
  published once after recovery.
- DCMTK conformance against the workstation and the 3-node cluster:
  C-ECHO, C-STORE (Explicit VR LE, JPEG Baseline, JPEG 2000 lossless and
  lossy, RLE, JPEG-LS), C-FIND, C-MOVE with byte-identical compressed pixel
  data. JPEG 2000 is now covered.
- Cluster (PostgreSQL, NATS, HAProxy, 3 gateways): load spread over all
  nodes; with one node killed and NATS stopped, 15 stores succeeded, both
  surviving nodes drained the shared outbox after recovery and the stream
  held exactly one event per store (52 of 52).
- Windows (Surface Pro 7, Windows 11 Home, Docker Desktop 4.96, PowerShell
  5.1): installed 0.1.1-beta.1, stored studies, then upgraded with
  `LibreDCM.ps1 upgrade -Yes`: settings renamed, old container removed, old
  data volume kept, index upgraded v1 to v3, studies still found. Self-signed
  and own-file TLS (DICOM TLS store/find, plain DICOM refused, admin over
  HTTPS), `backup` and `status` passed.
- Linux: interactive `install` (self-signed TLS, patient IDs left out of
  events), then S3 storage (SeaweedFS) with TLS; events carried an empty
  patient ID and were also published under the legacy subject.
- Not tested: the Windows question prompts (needs a signed-in desktop
  session) and S3 on Windows.

## rws-dicom 0.1.1-beta.1 (2026-10-11)

First published release. Workstation container package.
Release notes: [docs/releases/v0.1.1-beta.1.md](docs/releases/v0.1.1-beta.1.md)

### Added
- `deploy/docker/Dockerfile.dicom`. It was missing from 0.1.0, so
  `make image`, `make up` and `make cluster` could not work.
- `deploy/workstation/`: single-node compose, `.env` as the only settings
  file, `rws-dicom.ps1` / `rws-dicom.sh` (install, upgrade with index backup,
  verify, status, logs, backup, uninstall), `install.cmd`, `INSTALL.md`.
- `-echo HOST:PORT` flag: built-in C-ECHO for install checks, no DCMTK needed.
- `RWS_DICOM_REMOTE_AES`: C-MOVE destinations from one environment variable
  (`NAME=HOST:PORT[:AE]`, comma-separated).
- Beta version numbering. `make build` and `make image` take the version from
  the git tag and drop the leading `v`.
- `CHANGELOG.md` and release notes in `docs/releases/`.

### Changed
- Runtime image base is now `distroless/base-debian12:nonroot`. The JPEG
  codec needs the glibc loader, so `distroless/static` could not start the
  binary.

### Verified
- Go vet and tests pass (Go 1.26).
- Container image built and run: healthy, `/version` reports
  `0.1.1-beta.1`, `/readyz` 200, C-ECHO through the published port.

## rws-dicom 0.1.0-beta.1 (2026-10-04)

First version of the DICOM module. It was not released on its own; the git
tag was added when 0.1.1-beta.1 was published.

### Added
- Modular Go DICOM gateway (DIMSE): C-ECHO, C-STORE, C-FIND, C-MOVE, C-GET.
- Shared platform chassis: YAML + environment config, structured logging,
  `/healthz` `/readyz` `/metrics` `/version`, graceful shutdown.
- Pluggable back ends: filesystem storage; SQLite or PostgreSQL index with
  cluster-safe migrations; `none`, `log` or NATS JetStream events.
- RWS fork of gonetdicom v0.17 (`third_party/gonetdicom`): correct
  transfer-syntax negotiation for compressed data and a streaming C-MOVE
  sender that keeps the stored encoding.
- Compose files for single node and a 3-node cluster behind HAProxy; DCMTK
  conformance script; development document.
