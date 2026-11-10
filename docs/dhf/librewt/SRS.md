# Libre WT Software Requirements Specification (SRS)

| | |
|---|---|
| Document | DHF-WT-SRS |
| Status | **DRAFT** (2026-11-10, v0.1 scope approved by Patrick Mulcahy in chat, recorded in issue #15). Must be reviewed and approved at the QMS baseline |
| Software item | Libre WT (Watch Tower): management console of the Libre RWS PACS; v0.1 covers the Study Exceptions queue for LibreDCM |
| Safety class | B with class-C depth for the resolve path (it changes patient identity on stored images through LibreDCM; HAZ-013, HAZ-014 in `../libredcm/risk-management.md`) |

Rules as in `../libredcm/SRS.md`: IDs are permanent, `Retired` instead of
deleting, tests carry `// Verifies: WT-SRS-###`, checked by
`scripts/qms/trace.py --module librewt --prefix WT`.

Decisions behind this SRS (Patrick Mulcahy, 2026-11-10): build Libre WT now
instead of a LibreDCM admin CLI; local accounts with argon2id, roles
Viewer/Operator/Admin, first-run admin creation, no default credentials;
connection to one LibreDCM through its REST control API (gRPC later); Study
Exceptions queue; every edit shows a warning and needs an explicit
approve-edit confirmation; WT audit log. Fleet view, configuration, logs,
OIDC/LDAP and TOTP come later.

## 1. Web interface and access

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-001 | Libre WT shall serve its user interface from its own Go backend, as server-rendered pages without scripts, with a Content-Security-Policy that blocks scripts, framing and foreign form targets, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and `Cache-Control: no-store`; over HTTPS (TLS 1.2+) when a certificate is configured, with a warning in the log when it is not. | Active | HAZ-009, WT-HAZ-02 |
| WT-SRS-002 | Libre WT shall ship without accounts or default credentials. While no account exists, every page shall lead to a first-run page that creates one Admin; the first-run page shall stop working once an account exists. | Active | WT-HAZ-02 |
| WT-SRS-003 | Passwords shall be stored only as argon2id hashes (random salt, at least m=19 MiB, t=2) and shall have at least 12 characters and differ from the username. A failed login shall not reveal whether the username exists. | Active | WT-HAZ-02 |
| WT-SRS-004 | Access shall be role-based: Viewer and Operator may view the Study Exceptions queue and details; only Admin may resolve exceptions, manage users and read the audit log. Every state-changing request shall carry the session's CSRF token. Failed logins shall be limited per username (5) and per address (10) in 15 minutes. | Active | HAZ-014, WT-HAZ-02 |
| WT-SRS-005 | Sessions shall use a random token in an HTTP-only, SameSite=Strict cookie (Secure over HTTPS), end after 30 minutes idle (configurable) and 12 hours at most, and end at the next request when the user is disabled or their role or password changes. The last active Admin can't be disabled or demoted. | Active | WT-HAZ-02 |

## 2. LibreDCM connection

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-006 | Libre WT shall connect to one LibreDCM through its REST control API with the LibreDCM admin token (from configuration or a token file, never shown in the interface), optionally trusting a given CA certificate. Libre WT shall not import LibreDCM or DICOM code; it shall change archived data only through LibreDCM's resolve endpoint. It shall refuse to start without a token. | Active | HAZ-009, HAZ-014 |

## 3. Study Exceptions

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-007 | The Study Exceptions page shall list LibreDCM's patient-association conflicts with filters (active, held, accepted, resolved, all), showing time, kind, state, archived and incoming Patient ID, incoming name, stored (coerced) Patient ID, study, number of images and sending AE. When LibreDCM can't be reached, the page shall say so. | Active | HAZ-013 |
| WT-SRS-008 | The detail page shall show the archived and the incoming patient attributes side by side with differences highlighted, the held images, and, for resolved exceptions, who resolved them, when, how and why. | Active | HAZ-014 |
| WT-SRS-009 | Before any edit, Libre WT shall show a warning page with LibreDCM's plan of exactly what will change (files and attribute values from → to, warnings). The edit shall be sent only when the request carries a single-use confirmation issued with that page for the same session, exception and action and less than 10 minutes old, the "I approve this edit" box is ticked, a reason is given, and LibreDCM's plan is still identical to the one shown. | Active | HAZ-014 |
| WT-SRS-010 | The resolve request to LibreDCM shall name the logged-in user as operator and carry the reason, so LibreDCM's audit record identifies the person. | Active | HAZ-014, HAZ-008 |

## 4. Audit

| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| WT-SRS-011 | Libre WT shall keep an audit log of logins (success, failure, rate limited), logouts, first-run setup, user changes, password changes, edit reviews, approvals and resolve results (who, address, action, target, outcome, detail). The approval shall be recorded before the edit is sent; if it can't be written, the edit shall not be sent. Admins can read the log in the interface. | Active | HAZ-008, HAZ-014 |
| WT-SRS-012 | Administrators shall be able to add users with a role, change roles, disable and enable users and reset passwords; every user can change their own password after entering the current one. | Active | WT-HAZ-02 |
