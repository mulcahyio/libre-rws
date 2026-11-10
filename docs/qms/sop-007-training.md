# SOP: Training and competence

| | |
|---|---|
| Document | QMS-SOP-007 |
| Status | **DRAFT for approval** (written 2026-11-10 by Hermes/Claude) |
| Requirements | ISO 13485:2016 §6.2 (via 21 CFR 820.10); 21 CFR 11.10(i) |
| Related | QMS-QM-001 §5 (roles), QMS-SOP-001 (records) |
| Records | Drive `LibreRWS/QMS/01 Training`: training matrix (Sheet), one training record per person and topic (template `templates/training-record.md`), CVs/qualification evidence, signed e-signature acknowledgements |

## 1. Purpose and scope

How Libre Systems determines the competence needed for work affecting
product quality, provides training, checks that it worked, and keeps
records. Applies to everyone who designs, verifies, approves, releases,
supports or audits Libre RWS products, including contractors and
consultants.

## 2. Responsibilities

| Role | Responsibility |
|---|---|
| Top management | Provides resources for training; ensures people are competent before they work unsupervised |
| Management representative | Keeps the training matrix; plans training; checks records at internal audit |
| Each person | Completes required training before doing the work; keeps their record current |

## 3. Competence requirements (training matrix)

| Role | Required competence | Required QMS training (read and understood, with quiz or discussion) |
|---|---|---|
| Developer | Go, DICOM networking basics, secure coding (CWE Top 25), Git/GitHub workflow | SDP (incl. §4.1 AI rules), SOP-001, SOP-002, SOP-003 (overview), SOP-010 |
| QA approver | Design control, ISO 13485, IEC 62304, ISO 14971 at working level | Quality manual, all SOPs |
| Management representative | QMS management, regulatory requirements (QMSR, 803, 806, 807) | Quality manual, all SOPs |
| Complaint handler | Complaint handling, MDR criteria | SOP-005, SOP-004, SOP-003 (overview) |
| Security officer | Vulnerability management, CVD, SBOM | SOP-010, SOP-005 §7 |
| Internal auditor | Audit techniques (ISO 19011), ISO 13485 | SOP-008, quality manual |
| Anyone signing electronically | Part 11 responsibilities | SOP-001 §6; signed acknowledgement (QMS-TPL-001) |

**AI tools** are not trained under this SOP; they are controlled as tools
(QMS-SOP-011) and their operating rules are in SDP §4.1. The **human** who
directs and reviews AI output must be trained on the SDP and the SOPs that
apply to the work.

## 4. Training process

1. **Identify needs:** on joining, on role change, when a procedure is new
   or changes in substance (the PR that changes it names who must be
   retrained), and from CAPAs and audit findings.
2. **Deliver:** read-and-understand with a short quiz or discussion for
   procedures; courses, workshops or supervised work for skills.
3. **Evaluate effectiveness:** quiz result, a reviewed work sample (e.g. the
   first PRs under the SDP pass review without process findings), or
   supervisor assessment. Record the method and result.
4. **Record:** training record per person and topic (who, what document and
   version/commit, date, method, effectiveness, trainer, signature).
5. **Awareness:** each person understands the relevance of their work and how
   it contributes to quality objectives and patient safety (ISO 13485 §6.2 e);
   covered in the quality manual training.

## 5. Current status (2026-11-10)

Patrick Mulcahy holds all roles on an interim basis. His experience
(software architecture, the design of LibreDCM, regulatory research done for
the compliance plan) is to be recorded as qualification evidence, and he
completes the read-and-understand training on the approved QMS documents
at the QMS baseline. External competence (regulatory consultant,
independent reviewer, penetration tester) is obtained through QMS-SOP-006.
