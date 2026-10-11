# Changelog

All notable changes to RWS are recorded here. Versions follow
[Semantic Versioning](https://semver.org) with a `-beta.N` pre-release label
until a release is approved for production use (see "Versioning" in
[README.md](README.md)).

RWS is developed by Patrick Mulcahy, Mulcahy.io (dev@mulcahy.io).

## Unreleased

### Fixed
- `install.cmd` no longer shows as modified in a fresh clone (`.gitattributes`
  now leaves it as stored, with CRLF line endings).
- The workstation `.env` and `backups/` folder are git-ignored, so local
  settings and backups can't be committed by mistake.

### Documentation
- `INSTALL.md`: Linux host firewalls can block C-MOVE to a viewer on the same
  PC (`host.docker.internal`); how to allow it.
- `INSTALL.md`: how to take a consistent backup (see Known issues).

### Known issues
- `backup` copies the SQLite index and images while the module is running.
  Under write load the copy can be inconsistent. Workaround: stop the module,
  run `backup`, start it again. Tracked in
  [#1](https://github.com/StoreHouseAdmin/RWS/issues/1). Also affects
  0.1.1-beta.1.

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
