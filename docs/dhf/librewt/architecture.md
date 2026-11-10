# Libre WT architecture (v0.1)

| | |
|---|---|
| Document | DHF-WT-ARCH |
| Status | **DRAFT** (2026-11-10) |

| Item | Path | Responsibility |
|---|---|---|
| WT-SI-01 | `modules/wt/cmd/librewt` | Entry point: configuration (YAML + `LIBRE_WT_*`), wiring, HTTP(S) server, graceful shutdown |
| WT-SI-02 | `modules/wt/internal/accounts` | Users, roles, argon2id hashes, epochs, audit log (SQLite in the data directory) |
| WT-SI-03 | `modules/wt/internal/dcmclient` | LibreDCM control-API client (JSON contract only, no LibreDCM imports; architecture test `TestWTDoesNotImportDCM`) |
| WT-SI-04 | `modules/wt/internal/web` | Pages, sessions, CSRF, rate limiting, edit confirmations, security headers; templates and CSS embedded in the binary |

Resolve sequence: detail page → `GET /exceptions/{id}/review?action=` (Libre
WT asks LibreDCM for the plan, audits the review, issues a single-use
confirmation bound to session, exception, action and a digest of the plan) →
`POST /exceptions/{id}/resolve` (CSRF, role, confirmation, approve box,
reason; plan fetched again and compared; approval audited; LibreDCM
`POST /control/v1/conflicts/{id}/resolve` with operator, reason and
`confirm: true`; result audited).

Deployment: one container (`deploy/docker/Dockerfile.wt`, image `libre/wt`)
or the binary, next to LibreDCM. Libre WT keeps its own data directory and
can be upgraded separately from LibreDCM.
