# Libre WT risk management file (ISO 14971:2019)

| | |
|---|---|
| Document | DHF-WT-RMF |
| Status | **DRAFT** (2026-11-10, prepared by Hermes/Claude for human review) |
| Procedure | QMS-SOP-003 (same scales and matrix as LibreDCM) |

Libre WT's main hazard is the one it exists to control: a study filed under
the wrong patient. The system-level hazards HAZ-013 (silent reassignment)
and HAZ-014 (administrator correction applied wrongly) are analysed in
`../libredcm/risk-management.md`; this file adds the hazards specific to the
console.

| ID | Hazardous situation | Harm | Causes | S | Initial P | Risk controls | Residual P | Residual |
|---|---|---|---|---|---|---|---|---|
| WT-HAZ-01 (→ HAZ-014) | Admin applies a correction to the wrong exception or with the wrong action | Wrong-patient diagnosis | Similar names, misreading, double click, stale page | S4 | P3 | WT-SRS-008 (side-by-side, differences highlighted), -009 (plan shown, single-use confirmation bound to session/exception/action, approve box, reason, plan re-checked before sending), -004 (Admin only), -011 (audit); LibreDCM DCM-SRS-077/078 (token, confirm, audit, previous values kept in every file) | P2 | R |
| WT-HAZ-02 | Unauthorised person resolves exceptions (stolen password, session hijack, CSRF, default credentials) | Wrong-patient diagnosis; privacy harm | Weak passwords, open network, missing CSRF protection | S4 | P3 | WT-SRS-001 (CSP, HTTPS, headers), -002 (no default credentials), -003 (argon2id, policy), -004 (CSRF, rate limits), -005 (cookie flags, timeouts, end on account change), -012 | P2 | R |
| WT-HAZ-03 | Exceptions not noticed; held images stay unavailable | Delayed diagnosis | Nobody opens the console | S3 | P3 | LibreDCM metric `libre_dcm_patient_conflicts_open` (DCM-SRS-074); WT-SRS-007 | P3 | R (alerting rules to ship with monitoring guidance) |
| WT-HAZ-04 | LibreDCM admin token disclosed by Libre WT | Unauthorised control-API use (HAZ-014) | Token shown in pages or logs | S4 | P2 | WT-SRS-006 (token never shown; file option); test checks pages contain no token | P1 | A |

Residual risks to address before clinical use: TOTP/second factor (planned),
OIDC/LDAP (planned), per-user identity towards LibreDCM (today the shared
admin token plus the operator name asserted by Libre WT), alert rules for open
exceptions.
