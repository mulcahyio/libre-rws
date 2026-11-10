# LibreDVT cybersecurity notes

| | |
|---|---|
| Document | DHF-DVT-CYBER |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Related | `risk-management.md` (DVT-HAZ-002, DVT-HAZ-003), `architecture.md` |

LibreDVT is an internal test tool, not a cyber device under FD&C §524B. It
still handles possibly sensitive files and talks DICOM to other systems, so
the controls below apply.

| Asset / interface | Threat | Control |
|---|---|---|
| Web UI (port 8095) | Unauthorised access; credential guessing; CSRF | Login required for everything except `/login` and `/healthz` (DVT-SRS-061); no default password, start refused without one; password compared in constant time with the configured secret; session cookie `HttpOnly`, `SameSite=Strict`, `Secure` when served over TLS; inactivity timeout; CSRF token on every POST (DVT-SRS-062); login rate limit (DVT-SRS-062); published on 127.0.0.1 by default (DVT-SRS-063) |
| Uploaded files and reports | PHI disclosure; path traversal through zip entries or file names | Synthetic-data warning (DVT-SRS-066); delete function (DVT-SRS-045); uploads stored under random IDs; zip entries with absolute paths or `..` rejected; size limits on uploads and zip expansion |
| Engine execution | Command injection through file names or plan values | Engines run with `exec` argument vectors, never a shell; arguments come from stored file paths under the data directory and validated plan fields (AE titles ≤ 16 chars, ports 1–65535, hosts without spaces) |
| Built-in Storage SCP (port 11115) | Unsolicited stores filling the disk | Runs only during a move step (DVT-SRS-026); accepts only the AE titles of the step's target; published on 127.0.0.1 by default |
| Test targets | Tests run against a clinical PACS | Production guard (DVT-SRS-028) |
| Supply chain | Vulnerable engine or base image | Pinned versions and digests (DVT-SRS-065); image scanned with Trivy in CI like the LibreDCM image |

DICOM TLS to targets is not in v0.1 (planned for v0.2 with the association
and security tests).
