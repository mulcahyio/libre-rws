# SOP: Corrective and preventive action (CAPA)

| | |
|---|---|
| Document | QMS-SOP-004 |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Requirements | ISO 13485:2016 §8.5.2, §8.5.3, §8.4 (via 21 CFR 820.10); 21 CFR 820.35 (records) |
| Related | QMS-SOP-005 (complaints), QMS-SOP-003 (risk), QMS-SOP-008 (audit, management review), SDP §9 (problem resolution) |
| Records | CAPA log `LibreRWS/QMS/07 CAPA/CAPA log` (Google Sheet) with one folder per CAPA; repository work linked through GitHub issues labelled `capa` |

## 1. Purpose and scope

How Libre Systems finds the causes of actual nonconformities (corrective
action) and of potential ones (preventive action), acts on them without
undue delay, and checks that the actions worked. Applies to product
problems, process problems, audit findings, complaints, security issues and
supplier problems.

**Not every problem is a CAPA.** A single software defect with a clear cause
is handled by problem resolution (SDP §9). A CAPA is opened when the cause
is systemic or unknown, the problem recurs, it has a safety impact, or a
source in §3 requires it.

## 2. Responsibilities

| Role | Responsibility |
|---|---|
| Anyone | Reports a potential CAPA source to the CAPA owner |
| Management representative (CAPA owner) | Decides whether to open a CAPA, assigns an owner and due dates, keeps the log, reports CAPA status at management review |
| CAPA action owner | Investigates, proposes and carries out actions |
| QA approver | Approves the plan and the effectiveness check; closes the CAPA |

## 3. Sources and criteria for opening a CAPA

| Source | Open a CAPA when |
|---|---|
| Complaints (QMS-SOP-005) | Any complaint involving a possible MDR, a correction or removal, or a trend |
| Anomalies and test failures | Recurring defect class, escaped defect found after release, defect in a risk-control unit |
| Vulnerabilities (QMS-SOP-010) | Exploitable vulnerability in a released version, or the same weakness class found twice |
| Internal or external audit | Every major finding; minor findings at the auditor's recommendation |
| Management review, data analysis (§8.4) | Quality objective missed twice in a row; adverse trend |
| Suppliers (QMS-SOP-006) | Supplier failure that affected product or records |
| Process monitoring | CI gate bypassed, record missing, change merged without design control |

## 4. Procedure

1. **Open** — log the CAPA (ID `CAPA-YYYY-NN`), source, description,
   affected products and versions, date, owner. Assess urgency with the risk
   file: if a released product may be unsafe, start containment at once and
   evaluate field action under QMS-SOP-005 §7 (corrections and removals).
2. **Contain** (if needed) — immediate actions to limit the effect (advisory
   to customers, workaround, stop distribution of a build, revoke a release
   from the download page).
3. **Investigate the root cause** — collect evidence (logs, code history,
   tests, interviews); use 5 Whys or a fishbone diagram; for software, check
   the requirement, design, implementation, review and test that should have
   caught the problem. Record the root cause and the evidence.
4. **Plan actions** — corrective actions remove the cause; preventive actions
   stop it happening elsewhere (other modules, similar code, the process).
   Each action has an owner and due date. Check that actions do not
   adversely affect the device's ability to meet regulatory requirements or
   its safety (ISO 13485 §8.5.2 c) and update the risk file if risks change.
5. **Implement** — through the normal processes (design changes via the SDP,
   document changes via QMS-SOP-001, training via QMS-SOP-007).
6. **Verify effectiveness** — define the check in advance (for example: no
   recurrence over N releases, a new CI gate catches the seeded defect, audit
   follow-up). Record the result. If not effective, return to step 3.
7. **Close** — the QA approver closes the CAPA when actions are done and
   effective. Report at the next management review.

## 5. Timelines (targets)

| Step | Target |
|---|---|
| Decision to open after the source is known | 5 business days |
| Containment where patient safety may be affected | Immediately (same day) |
| Root cause recorded | 30 days |
| Actions implemented | 90 days, or as set in the plan with justification |
| Effectiveness check | As defined in the plan, usually 1–3 releases or 3 months |

## 6. Records

Each CAPA folder holds: the CAPA form (template `templates/capa-form.md`),
investigation evidence, links to issues/PRs, the effectiveness evidence and
the closure approval. The CAPA log is indexed in `records-index.md`.
Retention per QMS-SOP-001 §7.3.

## 7. Trend analysis

At each management review the CAPA owner reports: open/closed CAPAs, overdue
actions, sources, recurring root causes, and the effectiveness rate
(quality objective QO-5).
