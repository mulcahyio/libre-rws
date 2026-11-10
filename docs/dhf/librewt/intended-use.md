# Libre WT intended use and user needs

| | |
|---|---|
| Document | DHF-WT-IU |
| Status | **DRAFT** (2026-11-10) |

## 1. Intended use (v0.1)

Libre WT is the web management console of the Libre RWS PACS. Version 0.1
lets PACS administrators review images that LibreDCM held because they
contradict the patient, study or series already archived, and resolve them
(correct the incoming images, correct the archived study, or reject the
incoming images) with a recorded reason. It does not display images and is
not used for diagnosis. It is used by trained PACS administrators (U1 in
`../libredcm/intended-use.md`); Viewer and Operator accounts can see the
queue.

## 2. User needs

| ID | Need | Users | Requirements |
|---|---|---|---|
| UN-WT-01 | Only people we authorised can use the console, and nobody can log in with a default password | U1, U5 | WT-SRS-001, -002, -003, -004, -005, -012 |
| UN-WT-02 | I see every study LibreDCM held for review, with what conflicts with what | U1 | WT-SRS-006, -007, -008 |
| UN-WT-03 | I can't change a patient's images by accident: I see exactly what will change and must approve it, with a reason | U1 | WT-SRS-009, -010 |
| UN-WT-04 | We can show who looked at and changed what, and why | U1, U5 | WT-SRS-010, -011 |
