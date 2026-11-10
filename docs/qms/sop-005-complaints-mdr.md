# SOP: Complaint handling, medical device reporting (21 CFR 803), corrections and removals (21 CFR 806)

| | |
|---|---|
| Document | QMS-SOP-005 |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude). Effective for regulated products from first commercial distribution; applied voluntarily before |
| Requirements | ISO 13485:2016 §8.2.1, §8.2.2, §8.2.3, §8.3.3 (via 21 CFR 820.10); 21 CFR 820.35 (complaint records); 21 CFR Part 803 (MDR, incl. §803.17 written procedures, §803.18 files); 21 CFR Part 806 (reports of corrections and removals) |
| Related | QMS-SOP-004 (CAPA), QMS-SOP-003 (risk), QMS-SOP-010 (security reports), SDP §9 |
| Records | Drive `LibreRWS/QMS/08 Complaints (restricted)`: complaint log (Sheet) and one folder per complaint; MDR event files; correction/removal files |

Regulatory text checked against the eCFR (Title 21 current to 2026-11-06)
on 2026-11-10: §820.35(a), §803.3, §803.17, §803.18(c), §803.50, §803.53,
§803.56, §806.10(b), §806.20(c).

## 1. Purpose and scope

How Libre Systems receives, documents, evaluates and investigates complaints
and other feedback; decides whether an event must be reported to FDA as a
medical device report (MDR); and decides on, carries out and reports
corrections and removals. Applies to all released products, including beta
releases used for evaluation.

## 2. Definitions

- **Complaint:** any written, electronic or oral communication that alleges
  deficiencies related to the identity, quality, durability, reliability,
  usability, safety or performance of a device after release (ISO 13485 §3.4).
  For software this includes reports of lost, missing, wrong or altered
  images, wrong query results, outages, security weaknesses reported by
  customers, and installation failures.
- **Feedback:** information from users that is not a complaint (questions,
  feature requests); reviewed as input to design and risk management.
- **MDR reportable event (803.3, 803.50):** information that reasonably
  suggests that a device **may have caused or contributed to a death or
  serious injury**, or has **malfunctioned and the malfunction would be
  likely to cause or contribute to a death or serious injury if it were to
  recur**.
- **Serious injury (803.3):** injury that is life-threatening, results in
  permanent impairment or damage, or needs medical or surgical intervention
  to preclude permanent impairment or damage.
- **Correction:** repair, modification, adjustment, relabeling, destruction
  or inspection (including patient monitoring) of a device without its
  physical removal. A software patch sent to users is a correction.
  **Removal:** physical removal of a device from its point of use (for
  software: withdrawing a version from use or distribution).
- **Become aware (803.3):** when any employee becomes aware of information
  that reasonably suggests a reportable event.

## 3. Responsibilities

| Role | Responsibility |
|---|---|
| Anyone at Libre Systems | Passes every complaint to the complaint handler the same business day; never handles it informally |
| Complaint handler | Logs, acknowledges, evaluates and investigates; prepares MDR decisions |
| Top management | Decides MDR reportability and field actions with the complaint handler; signs reports to FDA |
| QA approver | Approves complaint closure |
| Security officer | Handles vulnerability reports (QMS-SOP-010); passes those with customer impact here |

## 4. Receiving complaints

Channels: GitHub issues on the public repository, email (dev@ / support@
addresses published in the labeling), customer support contacts, field
reports from partners, social media and literature. **Patient data:**
reporters are asked never to send patient data. If it arrives anyway, the
complaint and its attachments go only into the restricted Drive folder 08;
public GitHub issues containing patient data are hidden/edited at once and
the content moved to Drive.

## 5. Recording and evaluation

1. **Log** within 1 business day: complaint ID `CMP-YYYY-NNN` and the fields
   required by 21 CFR 820.35(a): (1) device name; (2) date received; (3) UDI
   (once assigned) and any other identification: version and image digest;
   (4) name, address and phone number of the complainant; (5) nature and
   details of the complaint; (6) any correction or corrective action taken;
   (7) any reply to the complainant. Libre Systems also records the dates and
   results of the review, evaluation and investigation.
2. **Acknowledge** to the complainant within 2 business days.
3. **MDR screening** within 2 business days of receipt (§6) and record the
   decision with its rationale, even when the answer is "not reportable".
4. **Investigate** unless a documented reason says investigation is not
   needed (e.g. a similar complaint was already investigated: reference it
   and record the justification, as 820.35(a) requires).
   Investigation is **always** done for complaints involving a possible
   death, serious injury or malfunction (§6). Use the risk file to judge
   the hazard involved (HAZ-###), reproduce with the reported version,
   check logs and audit records supplied by the customer (without patient
   data where possible).
5. **Act:** fix through the SDP (anomaly issue), open a CAPA when the
   criteria of QMS-SOP-004 §3 are met, update the risk file if the event
   shows a new hazard or a higher probability.
6. **Reply** to the complainant with the outcome and **close** with QA
   approval. Target: triage within 5 business days; closure within 60 days
   or a documented reason.

## 6. Medical device reporting (21 CFR 803)

| Event | Report | Deadline |
|---|---|---|
| Information reasonably suggesting the device may have caused or contributed to a death or serious injury | MDR (Form FDA 3500A content, electronic eMDR) | **30 calendar days** after becoming aware |
| Malfunction that would be likely to cause or contribute to death or serious injury if it recurred | MDR | **30 calendar days** |
| Event that requires remedial action to prevent an unreasonable risk of substantial harm to the public health, or FDA has asked for 5-day reports | 5-day report | **5 work days** after becoming aware |
| New information about a reported event | Supplemental report | **30 calendar days** after receiving it |

- **eMDR:** reports are sent electronically through the FDA Electronic
  Submissions Gateway (or the eMDR web interface). Account set-up is an
  action for top management before first commercial distribution.
- **Decision support for LibreDCM / Libre RWS:** consider reportable, for
  example: an image or study lost after a success status, images associated
  with the wrong patient, altered pixel data, or a prolonged outage, **where
  it caused or could on recurrence cause a delayed or wrong diagnosis
  leading to serious injury**. When in doubt, report.
- **MDR event files (803.18):** for each event: all information, the
  deliberations and decision-making process (including why an event was not
  reported), copies of reports and FDA correspondence. Kept for **2 years
  from the date of the event or for a period equal to the expected life of
  the device, whichever is longer**.
- This SOP serves as the written MDR procedure required by 803.17
  (identification and evaluation of events, timely transmission, record
  keeping).

## 7. Corrections and removals (21 CFR 806)

1. **Decide:** when a problem in a released version may present a risk to
   health, top management decides on a field action: advisory notice,
   software patch (correction), withdrawal of a version (removal), with
   the risk file, CAPA and complaint data as inputs.
2. **Report to FDA within 10 working days** of initiating a correction or
   removal that is done **to reduce a risk to health posed by the device or
   to remedy a violation of the Act caused by the device that may present a
   risk to health** (806.10). The report contains the information listed in
   806.10(c) (device identification incl. versions/UDI, reason, actions,
   consignees, health hazard evaluation, etc.).
3. **Not reportable** corrections and removals (e.g. routine improvements,
   actions not related to a risk to health) are still recorded (806.20) with
   the justification for not reporting; records are kept for **2 years beyond
   the expected life of the device**.
4. **Carry out:** notify all affected customers (release-notes channel,
   email to registered customers, security advisory for security fixes),
   track acknowledgement, and verify the effectiveness (customers on fixed
   version, or mitigation applied).
5. If FDA classifies the action as a recall (21 CFR Part 7), follow FDA's
   instructions.

## 8. Trending and management review

Complaints are trended by product, version, type, hazard and root cause at
each management review (QMS-SOP-008). Adverse trends open a CAPA.

## 9. Records and retention

Complaint files, MDR event files and correction/removal files are kept in
the restricted Drive folder 08, indexed (without patient data) in
`records-index.md`, and retained per this SOP and QMS-SOP-001 §7.3.
