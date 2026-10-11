# RWS — DICOM Module (rws-dicom) Development Document

| | |
|---|---|
| **Developed by** | Patrick Mulcahy, Mulcahy.io |
| **Contact** | dev@mulcahy.io |
| **Module** | rws-dicom 0.1.1-beta.1 (first RWS module) |
| **Language** | Go 1.26+ |
| **Status** | Beta. Not for clinical use. Workstation container package added in 0.1.1-beta.1. See [CHANGELOG.md](../CHANGELOG.md) and [release notes](releases/v0.1.1-beta.1.md). |
| **Date** | October 2026 |

---

## 1. Purpose

RWS is being built as a set of independent, container-native modules. The
DICOM module is the foundation. It receives, archives, indexes, queries and
retrieves medical images over the DICOM network protocol (DIMSE). It also
publishes an event for every image it stores, so later modules can build on
it without being wired into its code. Later modules include worklist,
routing, DICOMweb, viewers, reporting and AI.

### Design goals

1. **Modular.** Can be installed, configured, monitored, run and upgraded on
   its own or as part of the larger system.
2. **Dynamic.** Can run as a single instance or as a cluster for scaling,
   load balancing and automatic recovery.
3. **Containerized.** Runs under Docker or another container runtime, from a
   single computer up to enterprise infrastructure.

### Scope of phase 1

- In scope: Verification (C-ECHO), Storage (C-STORE), Query/Retrieve (C-FIND,
  C-MOVE, C-GET).
- Out of scope: Modality Worklist, which will be its own module. DICOMweb
  (QIDO/WADO/STOW) is planned as a separate module or component.

---

## 2. Technology decisions

| Decision | Choice | Reason |
|---|---|---|
| Language | Go | Single static binary, low memory, strong concurrency, first-class container and cloud tooling. |
| DICOM networking | gonetdicom (MIT), maintained as an RWS fork | The only Go library covering all DIMSE-C/N services as SCU and SCP. It is pre-1.0, so RWS carries its own fixes (section 7). |
| DICOM data handling | godicom | Companion parser and encoder for gonetdicom. |
| Index (single node) | SQLite via modernc.org/sqlite | Pure Go, no CGO, zero external dependencies. |
| Index (cluster) | PostgreSQL via pgx | Shared state for many gateway nodes. |
| Event bus | NATS JetStream | Lightweight, durable streams, built-in de-duplication, clusters easily. |
| Metrics | Prometheus client | Industry standard. |
| Container base | Distroless base (glibc), non-root | Minimal attack surface, no shell. Not `static`: the JPEG codec (golibjpeg) loads native libraries through purego, so the binary needs the glibc loader even with CGO off. |

Alternatives evaluated during discovery: Orthanc (C++, GPL, proven, less
control), dicom-rs (Rust, fast, networking too low-level), dcm4che (Java,
complete, heavier footprint). Go was selected for one language across the
stack and for resource efficiency.

---

## 3. Architecture

```
            modalities / PACS / viewers
                     │  DICOM (TCP 11112, optional TLS)
             ┌───────▼────────┐
             │  TCP load       │   HAProxy (leastconn, health = /readyz)
             │  balancer       │
             └──┬─────────┬───┘
        ┌───────▼──┐   ┌──▼───────┐
        │ rws-dicom │…│ rws-dicom │   stateless gateway nodes (1..N)
        └──┬──┬──┬──┘   └──┬──┬──┬─┘
           │  │  │         │  │  │
   archive │  │  │ index   │  │  │ events
   storage ▼  │  ▼         ▼  │  ▼
   (fs / S3)  │ SQLite | PostgreSQL     NATS JetStream ──► other RWS modules
              ▼
     admin: /healthz /readyz /metrics /version  (:8081)
```

The gateway holds no state. Everything durable sits behind three interfaces:

- `blob.Store` holds the archived DICOM Part 10 files.
- `index.Index` holds patient, study, series and instance metadata.
- `events.Publisher` sends events to the bus.

This is what allows any node to be added, removed or killed without data
loss or reconfiguration.

### Repository layout

```
platform/config/        YAML + environment-variable configuration loader
platform/runtime/       shared chassis: logging, admin endpoints, lifecycle
modules/dicom/cmd/rws-dicom/      entrypoint
modules/dicom/internal/gateway/   DIMSE services
modules/dicom/internal/blob/      archive storage
modules/dicom/internal/index/     metadata index
modules/dicom/internal/events/    event publishing
modules/dicom/internal/dcm/       Part 10 wrapping, transfer-syntax policy
third_party/gonetdicom/           RWS fork of the networking library
deploy/                           Dockerfile, compose files, HAProxy config, workstation installer
scripts/conformance.sh            DCMTK conformance test
docs/                             this document
```

### Shared platform chassis

Every RWS module uses `platform/runtime`, which gives each module the same
operational surface:

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | Liveness: the process is running. |
| `GET /readyz` | Readiness: every dependency check passes (storage, index, bus). Returns 503 otherwise. |
| `GET /metrics` | Prometheus metrics. |
| `GET /version` | Module name, version, node ID, uptime. |

It also provides structured JSON logging and graceful shutdown on
SIGINT/SIGTERM, which allows rolling upgrades.

---

## 4. Service behaviour

### C-STORE

1. The transfer syntax is negotiated using the sender's preference (section 6).
2. The received dataset bytes are wrapped in a Part 10 file without
   re-encoding, so the archive is byte-for-byte what the sender transmitted.
3. The file is written atomically (temp file, fsync, rename) at
   `<StudyUID>/<SeriesUID>/<SOPInstanceUID>.dcm`.
4. Study, series and instance rows are upserted in one transaction.
5. The `rws.dicom.instance.stored` event is published.

The order guarantees that an instance is durable before it is queryable, and
queryable before anyone is notified. Re-sent instances replace earlier ones.

### C-FIND

- Information models: Patient Root and Study Root.
- Levels: PATIENT, STUDY, SERIES and IMAGE.
- Matching:
  - single value
  - wildcard (`*`, `?`)
  - date range (`from-to`, open-ended)
  - UID list
  - `ModalitiesInStudy`
- Return keys include `NumberOfPatientRelatedStudies`,
  `NumberOfStudyRelatedSeries`, `NumberOfStudyRelatedInstances` and
  `NumberOfSeriesRelatedInstances`.
- The response contains exactly the keys requested. Results are capped by
  `find_limit`.

### C-MOVE

The library's built-in C-MOVE path was bypassed (section 7). RWS opens its
own association to the destination and proposes each instance's stored
transfer syntax. It then streams one instance at a time from storage, so
memory use stays flat regardless of study size. Compressed data is forwarded
unchanged. Native data is re-encoded only if the destination chooses a
different native syntax.

The final response reports completed, failed and warning counts and lists
the failed SOP Instance UIDs. Retrieves with no unique keys are refused to
prevent "move the whole archive". Unknown destinations return status A801.

### C-GET

Served for uncompressed instances, capped by `get_max_bytes`. Compressed
instances are reported as failed sub-operations until the related fork fix
lands; use C-MOVE for compressed data.

---

## 5. Data model

### Index schema (identical on SQLite and PostgreSQL)

| Table | Key | Main columns |
|---|---|---|
| `studies` | `study_uid` | patient ID, name, birth date, sex, study date/time, accession, study ID, description, referring physician |
| `series` | `series_uid` | study UID, modality, series number, description |
| `instances` | `sop_instance_uid` | series/study UID, SOP class, instance number, transfer syntax, blob key, size, source AE, received at |
| `schema_version` | | current schema version |

Migrations run at startup under a PostgreSQL advisory lock, so several nodes
can start at the same moment safely.

### Event: `rws.dicom.instance.stored`

| Field | Description |
|---|---|
| `event_id` | SOP Instance UID plus receive time; used for de-duplication |
| `occurred_at`, `node` | when and where the instance was stored |
| `patient_id`, `accession_number`, `modality` | routing context (contains PHI) |
| `study_instance_uid`, `series_instance_uid`, `sop_instance_uid`, `sop_class_uid` | identity |
| `transfer_syntax_uid`, `blob_key`, `size_bytes` | storage location and format |

The JetStream stream is `RWS_DICOM` (subjects `rws.dicom.>`), file-backed,
with configurable replicas and retention (default 7 days) and a 10-minute
de-duplication window.

---

## 6. Transfer syntax policy

| Context | Policy |
|---|---|
| Storage SOP classes | Accept the first syntax the sender proposed that RWS can store as-is. Supported: Explicit and Implicit VR LE; JPEG Baseline, Extended and Lossless (both); JPEG-LS (both); JPEG 2000 (both); HTJ2K (three variants); RLE Lossless. Others are rejected per context. |
| Verification, query/retrieve | Explicit VR LE, otherwise Implicit VR LE. |

---

## 7. gonetdicom fork

The fork is based on upstream v0.17.0 (MIT, Copyright (c) 2026
godicom-dev). Fork modifications are by Patrick Mulcahy, Mulcahy.io. The
upstream license and notice are retained in `third_party/gonetdicom/LICENSE`.

### Carried changes

1. **`ServerConfig.SelectTransferSyntax`.** Upstream always picks Implicit VR
   LE when it is offered, which forces modalities to decompress. Lossy-JPEG
   senders fail outright under that behaviour. The hook lets the acceptor
   choose. Upstream's test suite still passes with the change.

### Planned changes (to offer upstream)

2. Streaming C-MOVE/C-GET plans (an iterator), with Pending responses and
   C-CANCEL support.
3. C-GET: pass the negotiated transfer syntax to the handler, and count
   failed sub-operations.
4. Expose the calling AE title and peer address to handlers. This is needed
   for AE allow-listing and `SourceApplicationEntityTitle`.
5. Lazy decode of inbound datasets, to avoid holding raw and decoded copies
   at once.

### Library limitations found by testing (v0.17)

| Issue | Impact | RWS handling |
|---|---|---|
| C-MOVE holds every instance in memory and proposes only Implicit VR LE | Memory blow-up; compressed data breaks | Own streaming sender |
| C-GET picks the first accepted syntax silently and ignores sub-op results | Possible mismatched data; inaccurate counts | Uncompressed only, byte cap |
| SCP always prefers Implicit VR LE | Forced decompression | Fixed in fork (change 1) |
| Calling AE not available to handlers | No per-AE access control yet | Planned fork change 4 |

---

## 8. Configuration

The configuration file is YAML (`modules/dicom/config.example.yaml`). Every
setting can be overridden by environment variable, and environment wins over
the file.

| Area | Key settings (env var) |
|---|---|
| Admin | `RWS_ADMIN_ADDR`, `RWS_LOG_LEVEL`, `RWS_LOG_FORMAT`, `RWS_SHUTDOWN_TIMEOUT`, `RWS_NODE_ID` |
| DICOM | `RWS_DICOM_AE_TITLE`, `RWS_DICOM_LISTEN`, `RWS_DICOM_MAX_ASSOCIATIONS`, `RWS_DICOM_MAX_PDU`, `RWS_DICOM_HANDSHAKE_TIMEOUT`, `RWS_DICOM_IDLE_TIMEOUT`, `RWS_DICOM_FIND_LIMIT`, `RWS_DICOM_GET_MAX_BYTES`, `RWS_DICOM_TLS_CERT`, `RWS_DICOM_TLS_KEY`, `RWS_DICOM_ALLOW_ANY_CALLED_AE`, `RWS_DICOM_ACCEPT_ANY_SOP_CLASS` |
| C-MOVE destinations | `remote_aes` map (file only) |
| Storage | `RWS_STORAGE_DRIVER` (fs), `RWS_STORAGE_PATH` |
| Index | `RWS_INDEX_DRIVER` (sqlite \| postgres), `RWS_INDEX_DSN` |
| Events | `RWS_EVENTS_DRIVER` (none \| log \| nats), `RWS_EVENTS_URL`, `RWS_EVENTS_STREAM`, `RWS_EVENTS_REPLICAS`, `RWS_EVENTS_MAX_AGE_DAYS` |

---

## 9. Operations

### Metrics

| Metric | Meaning |
|---|---|
| `rws_dicom_connections_total` / `_active` | DICOM connections |
| `rws_dicom_cstore_total{result}`, `_cstore_bytes_total`, `_cstore_seconds` | storage volume and latency |
| `rws_dicom_cfind_total{level,result}`, `_cfind_seconds` | query volume and latency |
| `rws_dicom_retrieve_subops_total{op,result}` | C-MOVE/C-GET outcomes |
| `rws_dicom_event_publish_failures_total` | events lost while the bus was unavailable |
| `rws_module_info{module,version,node}` | build information |

### Deployment

| Topology | How |
|---|---|
| Bare metal / VM | `make build`, run `bin/rws-dicom -config …` |
| Workstation | `deploy/workstation/` installer (gateway only, SQLite, Docker volume; NATS optional) |
| Single container | `make up` (gateway + NATS, SQLite, local volume) |
| HA on one host | `make cluster` (PostgreSQL + NATS + 3 gateways behind HAProxy) |
| Kubernetes | Same image; Helm chart planned |

The image uses a built-in health check (`rws-dicom -healthcheck 127.0.0.1:8081`).
Upgrades are rolling: stop one node (it drains on SIGTERM), replace it, and
repeat.

---

## 10. Verification results

All results below come from the development sandbox.

| Test | Result |
|---|---|
| Go end-to-end tests over real associations (echo, store, find, move, unknown destination, get) | Pass |
| Part 10 round trip: archived file readable, dataset bytes unchanged | Pass |
| Transfer-syntax policy (sender preference kept, unsupported rejected) | Pass |
| gonetdicom fork's own upstream test suite | Pass |
| DCMTK: echo; store Explicit LE, JPEG Baseline, JPEG 2000, RLE; find; move | Pass; compressed pixel data byte-identical after retrieval |
| Mixed proposal (JPEG 2000 + native in one context) | JPEG 2000 kept; pixel data identical |
| Two nodes on PostgreSQL + NATS: store on A, query/move from B; A stopped, B kept serving | Pass |
| Bus outage | `/readyz` returns 503; stores still succeed; failure counted |
| Concurrent node start (migration race) | Found and fixed (advisory lock) |
| Load: 1,000 instances, 10 parallel associations, 2 nodes | 0 failures, about 190 images/s, about 30 MB RAM per node, all indexed, 1,000 events |
| Docker images / compose files | Dockerfile added in 0.1.1-beta.1 (it was missing from 0.1.0). Release check, 2026-10-11 (Docker 29.7.2, Linux): image `rws/dicom:0.1.1-beta.1` built (74 MB); container reports healthy; `/version` returns `0.1.1-beta.1`; `/readyz` returns 200; C-ECHO succeeds inside the container and through the published port; all three compose files pass `docker compose config`. **Not yet run:** the compose topologies themselves (single, cluster, workstation install scripts). |

---

## 11. Known gaps and roadmap

| # | Item | Priority |
|---|---|---|
| 1 | Run the compose topologies and the workstation installer end to end (image build verified in 0.1.1-beta.1) | High |
| 2 | Transactional event outbox, so no event is lost during a bus outage | High |
| 3 | S3/MinIO storage backend, replacing the shared volume for clusters | High |
| 4 | Fork changes 2–5 (streaming retrieve, C-GET fixes, calling AE, lazy decode) and upstream PRs | High |
| 5 | Security: AE allow-listing, admin endpoint authentication, audit trail (ATNA), TLS by default | High |
| 6 | RWS-owned DICOM UID root (replace the borrowed Implementation Class UID) | Medium |
| 7 | Helm chart; config hot-reload for `remote_aes` | Medium |
| 8 | C-FIND refinements: case-insensitive names, time ranges, character sets | Medium |
| 9 | Next modules: Modality Worklist, DICOMweb | Next phase |

---

## 12. Attribution and licensing

RWS and the rws-dicom module were developed by **Patrick Mulcahy, Mulcahy.io**
(dev@mulcahy.io). Copyright (c) 2026 Patrick Mulcahy, Mulcahy.io. The license
for RWS itself is to be determined.

Third-party components keep their own licenses. gonetdicom (fork) and godicom
are MIT, Copyright (c) 2026 godicom-dev. Other dependencies are listed in
`go.mod`.

## Change log

### 0.1.1-beta.1 — Workstation container package (October 2026)

Developed by Patrick Mulcahy, Mulcahy.io (dev@mulcahy.io).

- **Added `deploy/docker/Dockerfile.dicom`.** The Makefile and both compose files
  referenced it, but it was missing from the 0.1.0 commit, so `make image`,
  `make up` and `make cluster` could not have worked.
- **Runtime base changed to `distroless/base-debian12:nonroot`.** The binary is
  dynamically linked (purego, via the JPEG codec), so `distroless/static` would
  fail to start it.
- **`deploy/workstation/`:** single-node compose, `.env` as the only settings
  file, `rws-dicom.ps1` / `rws-dicom.sh` (install, upgrade with index backup,
  verify, status, logs, backup, uninstall), `install.cmd`, and `INSTALL.md`.
- **`-echo HOST:PORT` flag:** built-in C-ECHO for install verification, with no
  DCMTK needed.
- **`RWS_DICOM_REMOTE_AES`:** C-MOVE destinations from one environment variable
  (`NAME=HOST:PORT[:AE]`, comma-separated). This removes the need to bind-mount
  a config file, which fails when the repository sits on a network drive.
