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
- Stored instances and `instance.stored` events now record the sending AE
  title (`source_ae`), which was always empty before.
- `BACKLOG.md`: the clustering change backlog (storage abstraction, shared
  catalog, eviction, replication, role-based nodes).

### Changed
- An environment variable that is set but empty no longer overrides the
  config file (compose passes every variable through, so empty values were
  wiping file settings).

### Fixed
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
