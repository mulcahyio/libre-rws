# Libre DCM change backlog

Changes needed to support the clustering architecture decided in discovery on
2026-10-14 (HA plus horizontal scale, hybrid local/object storage, role-based
nodes). Libre DCM is not built this way yet. Items are chosen at deployment
time, so a single-node clinic install stays simple.

Status of each item is tracked in the CHANGELOG as it lands.

## Storage layer
- Introduce a storage abstraction interface with pluggable backends: local-only, object-store-only, and local-staged-with-async-replication.
- Add object-store backend (S3 / MinIO). DICOM module is sole owner of storage; studies stored as objects.
- Add local-eviction support, gated by the replication-conditional safety gate (see Eviction engine).

## Metadata index (catalog)
- Required regardless of backend — even local-only needs it rather than directory listing.
- Repository interface with two deliberate hand-written implementations: SQLite (single-node/clinic) and Postgres (clustered/enterprise, shared catalog), chosen at deployment time.
- Hold both to a shared conformance test suite so they cannot drift.
- Operation groups: ingestion (idempotent upsert), lookup-and-query (point lookup + richer search backing C-FIND), replication-state (where an object lives, mark replicated, list pending), lifecycle (eviction eligibility + state updates).

## Eviction engine
- Configurable triggers: capacity (high/low water marks), age, access-based (LRU).
- Compound rules over a bounded attribute set (age, last-access, replication state, capacity pressure) with AND/OR and single-level grouping. No full nested expression language.
- Replication-confirmed gate is conditional on whether replication is enabled: when on, eligibility requires confirmed + integrity-checked; when off (local-only), gate does not apply and eviction = deletion of sole copy, governed by explicit operator retention rules, default off.
- Rule engine runs strictly SUBORDINATE to the gate: it selects within the already-cleared set and can never widen what "safe" means.

## Replication
- Pluggable transport behind one replication interface: DICOM-router backend (protocol-native, for interop; coarser resilience) and streaming backend (per-instance resumability, integrity checks, backfill).
- Per-instance ledger in the index is source of truth: pending / in-flight / confirmed / failed. A failed image is one row still pending; worker backfills just that object.
- Integrity verification (checksum stored in index) on the streaming path so "confirmed" means intact, not merely transferred.
- Console must surface that transport choice is also a resilience-tier choice.

## Role-based nodes
- Refactor to a single Libre DCM service exposing store, find, move, and worklist as independently selectable functions; node config selects which it runs (all-in-one for a clinic down to a single role per enterprise tier).
- Absorb the standalone MWL module into this as a selectable service — supersedes the earlier standalone-MWL-module decision.
- Role separation leans hard on shared index + storage being concurrent-safe (a C-MOVE node routes studies it never received) — reinforces getting the Postgres shared catalog right.
- Surface role and resilience-tier selection in the management console (Libre WT).

## Open design questions

1. Replication mechanics: trigger, worker pickup, retry/backoff, integrity check.
2. How much DICOM metadata the index stores (routing only vs full C-FIND set).
3. Shared catalog details for PostgreSQL clusters.
4. Object layout in the object store.
