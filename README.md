# RWS

**Developed by Patrick Mulcahy, Mulcahy.io** · dev@mulcahy.io
Full design and developer documentation: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)

**Current release:** rws-dicom **0.1.1-beta.1** (beta, not for clinical use). See the [release notes](docs/releases/v0.1.1-beta.1.md) and [CHANGELOG.md](CHANGELOG.md).

Modular, container-native platform, written in Go. Each module is an
independent binary and container image that can run on its own or as part of
the whole system. The first module is **rws-dicom**.

```
platform/            shared chassis every module uses
  config/            YAML file + environment overrides
  runtime/           logging, /healthz /readyz /metrics /version, graceful shutdown
modules/dicom/       rws-dicom (DIMSE gateway)
  cmd/rws-dicom/     entrypoint
  internal/gateway/  C-ECHO, C-STORE, C-FIND, C-MOVE, C-GET
  internal/blob/     archive storage (filesystem; S3 planned)
  internal/index/    metadata index (SQLite or PostgreSQL)
  internal/events/   event bus (NATS JetStream)
  internal/dcm/      Part 10 wrapping, transfer-syntax policy, value helpers
third_party/gonetdicom/  RWS fork of the DICOM networking library (see RWS_FORK.md)
deploy/              Dockerfile, compose (single node + cluster), HAProxy,
  workstation/       one-step local install (Windows/macOS/Linux)
scripts/conformance.sh   DCMTK-based conformance smoke test
```

## How the module meets the goals

**Modular.** One binary per module, one config format, one operational
surface. Each module can be installed, configured, monitored and upgraded on
its own. Modules talk through the event bus, not through each other's code,
so the worklist, routing, viewer and reporting modules can be added without
touching rws-dicom.

**Dynamic.** The gateway is stateless. Durable state sits behind three
interfaces: archive storage, metadata index and event bus. With PostgreSQL,
shared storage and NATS, any number of gateway nodes can run behind a TCP load
balancer. Nodes can be added, removed or killed at will. Schema migrations are
cluster-safe (advisory lock).

**Containers.** Single binary (~30 MB, no CGO), distroless non-root image,
built-in health check, Docker Compose for single node and cluster. Kubernetes
uses the same image.

## Run it

```bash
make test                 # unit + end-to-end DIMSE tests
make build && ./bin/rws-dicom -config modules/dicom/config.example.yaml
make up                   # single node in Docker
make workstation          # workstation install with checks (see deploy/workstation/INSTALL.md)
make cluster              # PostgreSQL + NATS + 3 gateways behind HAProxy
make conformance DIR=path/to/dicom/files   # DCMTK check against a running node
```

Operational endpoints (default `:8081`): `/healthz` (liveness), `/readyz`
(storage, index and bus checks), `/metrics` (Prometheus), `/version`.

## DICOM behaviour

| Service | Notes |
|---|---|
| C-ECHO | Always available |
| C-STORE | All standard storage classes. Archived byte-for-byte in the transfer syntax the sender chose, including JPEG, JPEG-LS, JPEG 2000, HTJ2K and RLE. Order: durable on disk → indexed → event published. |
| C-FIND | Patient Root and Study Root; PATIENT/STUDY/SERIES/IMAGE levels; wildcards, date ranges, UID lists, ModalitiesInStudy, related-count keys. Capped by `find_limit`. |
| C-MOVE | Destinations from `remote_aes`. Streams one instance at a time from storage and keeps the stored transfer syntax, so memory stays flat for any study size. |
| C-GET | Uncompressed instances only, capped by `get_max_bytes` (library limitation, see below). Use C-MOVE for compressed data. |

Events: `rws.dicom.instance.stored` on JetStream stream `RWS_DICOM`,
de-duplicated by event ID. Payload includes patient ID and accession number,
so keep the bus on a private network with TLS in production.

## Verified so far

- End-to-end Go tests over real associations: echo, store, find, move, unknown
  destination (A801), get.
- DCMTK conformance script: native and compressed (JPEG Baseline, JPEG 2000,
  RLE) store; find; move with compressed pixel data byte-identical.
- Two-node cluster on PostgreSQL + NATS: store on node A, query and move from
  node B; node A stopped and node B kept serving; readiness turns 503 when the
  bus is down while stores keep succeeding.
- Load: 1,000 instances, 10 parallel associations across 2 nodes, zero
  failures, about 30 MB RAM per node. This was measured in a dev sandbox, not
  a benchmark.

- Container image (0.1.1-beta.1): built and started; healthy, `/readyz` 200,
  C-ECHO through the published port.

Not yet run: the compose topologies (single node, cluster) and the
workstation installer end to end.

## Known gaps / next work

1. **Event outbox.** If the bus is down, the store still succeeds but the
   event is lost (counted in `rws_dicom_event_publish_failures_total`). Fix:
   transactional outbox table plus relay.
2. **S3/MinIO storage backend.** Replaces the shared volume for real clusters.
3. **Fork items** (third_party/gonetdicom/RWS_FORK.md): streaming C-MOVE/C-GET
   with Pending responses and C-CANCEL, C-GET transfer-syntax awareness,
   calling-AE in handlers (needed for per-AE access control and
   `SourceApplicationEntityTitle`), lazy inbound decode.
4. **Security.** DICOM TLS is supported (`tls_cert_file`). AE allow-listing,
   admin endpoint auth and an audit trail (ATNA) are still to do.
5. Helm chart, config hot-reload for `remote_aes`, case-insensitive
   PatientName matching, StudyTime range matching.
6. Replace `ImplementationClassUID` (currently gonetdicom's root) with an
   RWS-owned UID root.

## Versioning

Releases use [Semantic Versioning](https://semver.org) with a beta
pre-release label while RWS is not ready for clinical use:

- Git tag: `vMAJOR.MINOR.PATCH-beta.N`, e.g. `v0.1.1-beta.1`.
- Binary (`-version`, `/version`) and image tag: the same without the `v`,
  e.g. `rws/dicom:0.1.1-beta.1`. `make build` and `make image` take it from
  the tag.
- `N` goes up for each beta of the same version. The `-beta.N` label is
  dropped only for a release approved for production use.

Every release has an entry in [CHANGELOG.md](CHANGELOG.md) and release notes
in [docs/releases/](docs/releases/).

## Attribution

RWS was developed by Patrick Mulcahy, Mulcahy.io (dev@mulcahy.io).
Copyright (c) 2026 Patrick Mulcahy, Mulcahy.io. See NOTICE for third-party
licenses (gonetdicom and godicom are MIT, Copyright (c) 2026 godicom-dev).
