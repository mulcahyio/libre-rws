# SOP: Internal audit and management review

| | |
|---|---|
| Document | QMS-SOP-008 |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Requirements | ISO 13485:2016 §8.2.4 (internal audit), §5.6 (management review), §8.4 (analysis of data) via 21 CFR 820.10. Under the QMSR, management review, internal audit and supplier audit reports are open to FDA inspection |
| Related | QMS-QM-001 §4 (objectives), QMS-SOP-004 (CAPA), QMS-SOP-001 (records) |
| Records | Drive `LibreRWS/QMS/06 Internal Audits` and `05 Management Review`; audit programme and minutes; findings tracked as CAPAs or issues |

## Part A: Internal audit

### A1. Purpose

Determine whether the QMS conforms to planned arrangements, ISO 13485, the
QMSR and other applicable regulatory requirements, and is effectively
implemented and maintained.

### A2. Audit programme

- Every QMS process is audited at least **once a year**; processes with
  high risk, recent changes or past findings more often.
- Annual programme, approved by top management, lists: process, criteria
  (procedure + standard clauses), planned month, auditor.
- Proposed first-year programme (2027): Q1 document control and Part 11
  (re-run VAL-0001 checks), design control and SDP (sample of PRs against the
  template), risk management; Q2 CAPA, complaints/MDR readiness, suppliers;
  Q3 cybersecurity and SOUP/SBOM review, training; Q4 release process
  (latest release record against the checklist), management review inputs.

### A3. Auditor independence and competence

Auditors do not audit their own work (ISO 13485 §8.2.4). **In a one-person
organisation this requires an external auditor** (consultant) for any area
the developer works in, which is all of them. Until there is budget
(decision D5), Libre Systems performs **self-assessments** using the same
checklists, records them clearly as self-assessments (not independent
internal audits), and plans the first independent audit before the QMS
baseline is declared effective and before any submission. AI tools may
prepare checklists and gather evidence; they are not auditors.

### A4. Conducting an audit

1. Plan: scope, criteria, checklist, sample (e.g. 5 PRs merged since the
   last audit, the latest release record, 2 Drive records).
2. Collect evidence (records, CI logs, interviews); compare with criteria.
3. Classify findings: **major** (requirement not met, risk to product or
   compliance, or systemic), **minor** (isolated lapse), **observation**
   (improvement opportunity).
4. Report within 10 business days: scope, criteria, findings with evidence,
   conclusion. The auditee agrees corrections and corrective actions
   without undue delay.
5. Follow up: CAPA for major findings (QMS-SOP-004); verify actions and
   report the verification.

## Part B: Management review

### B1. Purpose and frequency

Top management reviews the QMS to ensure it remains suitable, adequate and
effective. Frequency: **at least once a year (full review)**, plus a short
review at each release; extra reviews after a significant event (field
action, MDR, major audit finding, regulatory change).

### B2. Inputs (ISO 13485 §5.6.2, all required)

| # | Input | Source |
|---|---|---|
| a | Feedback | Issues, customer feedback, beta users |
| b | Complaint handling | Complaint log and trends (SOP-005) |
| c | Reporting to regulatory authorities | MDRs, corrections/removals, FDA correspondence |
| d | Audits | Internal (self-assessment) and external audit results |
| e | Monitoring and measurement of processes | Quality objectives QO-1…QO-6, CI gate statistics |
| f | Monitoring and measurement of product | Release verification results, anomaly lists, fuzzing/SAST/SCA results |
| g | Corrective action | CAPA status and effectiveness (SOP-004) |
| h | Preventive action | Same |
| i | Follow-up actions from previous reviews | Previous minutes |
| j | Changes that could affect the QMS | Organisation (Libre Systems formation), new products (viewer), tools |
| k | Recommendations for improvement | Anyone |
| l | Applicable new or revised regulatory requirements | Guidance and standards updates, fee changes (checked yearly) |

Also reviewed: risk management process suitability (ISO 14971 §4.2),
supplier performance (SOP-006), training status (SOP-007), resources.

### B3. Outputs (ISO 13485 §5.6.3)

Recorded decisions and actions on: improvement needed to maintain the
suitability, adequacy and effectiveness of the QMS and its processes;
improvement of product related to customer requirements; changes needed to
respond to new or revised regulatory requirements; resource needs. Each
action has an owner and due date.

### B4. Record

Minutes (template `templates/management-review-minutes.md`): date,
attendees, each input with summary and data, decisions and actions, signed by
top management; PDF in Drive 05, indexed in `records-index.md`.

## Part C: Analysis of data (§8.4)

Before each management review the management representative compiles data
on feedback, conformity to product requirements (release verification),
process and product characteristics and trends (CI, anomalies, fuzzing
findings), suppliers, audits and service reports, and states conclusions on
the effectiveness of the QMS.
