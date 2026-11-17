#!/usr/bin/env python3
"""Synthetic test data for the Libre WT system test (runs in rws-e2e-dcmtk).

Writes into OUT (default /tmp/wtdata):
  study/   study A: 2 CT series x 2 instances, patient WTSYS^ALPHA / WTSYS-0001
  conflict/ one instance of study A that names another patient
            (WTSYS^OTHER / WTSYS-9999): LibreDCM must hold it as a
            Study Exception (patient-conflict policy "quarantine")
  manifest.json  UIDs and, per instance, the SHA-256 of the data set
            (everything after the File Meta Information)

Only synthetic values. The pixel data and the other attributes come from
pydicom's public sample CT_small.dcm; every identifying attribute is
replaced and every UID is new.
"""
import hashlib
import json
import os
import sys

import pydicom
from pydicom.data import get_testdata_file
from pydicom.uid import generate_uid

# A test-only UID root (2.25 = UUID-derived, ISO/IEC 9834-8); generate_uid
# with prefix None gives 2.25.<uuid>.
OUT = sys.argv[1] if len(sys.argv) > 1 else "/tmp/wtdata"


def dataset_sha256(path):
    """SHA-256 of the bytes after the File Meta Information group."""
    with open(path, "rb") as f:
        data = f.read()
    if data[128:132] != b"DICM":
        raise SystemExit(f"{path}: no DICM prefix")
    # (0002,0000) UL, explicit VR little endian: tag 4 + VR 2 + len 2 + value 4
    if data[132:136] != b"\x02\x00\x00\x00":
        raise SystemExit(f"{path}: first element is not (0002,0000)")
    group_len = int.from_bytes(data[140:144], "little")
    return hashlib.sha256(data[144 + group_len:]).hexdigest()


def make(template, path, patient, study, series_uid, series_no, inst_no, desc):
    ds = pydicom.dcmread(template)
    name, pid = patient
    ds.PatientName = name
    ds.PatientID = pid
    ds.PatientBirthDate = "19700101"
    ds.PatientSex = "O"
    for kw in ("OtherPatientIDs", "InstitutionName", "ReferringPhysicianName",
               "OperatorsName", "PerformingPhysicianName"):
        if kw in ds:
            delattr(ds, kw)
    ds.StudyInstanceUID = study["uid"]
    ds.StudyID = "WT1"
    ds.StudyDate = study["date"]
    ds.StudyTime = "120000"
    ds.AccessionNumber = study["accession"]
    ds.StudyDescription = "WT SYSTEM TEST"
    ds.SeriesInstanceUID = series_uid
    ds.SeriesNumber = series_no
    ds.SeriesDescription = desc
    ds.InstanceNumber = inst_no
    sop = generate_uid()
    ds.SOPInstanceUID = sop
    ds.file_meta.MediaStorageSOPInstanceUID = sop
    # CT_small.dcm ends with Data Set Trailing Padding (FFFC,FFFC). PS3.10
    # allows it only in files; DICOM network stacks drop it (storescu
    # does), so the sent data set would never equal the file's. Remove it.
    if 0xFFFCFFFC in ds:
        del ds[0xFFFCFFFC]
    ds.save_as(path, write_like_original=False)  # pydicom 2.x: full File Meta Information
    return sop


def main():
    template = get_testdata_file("CT_small.dcm")
    os.makedirs(os.path.join(OUT, "study"), exist_ok=True)
    os.makedirs(os.path.join(OUT, "conflict"), exist_ok=True)
    study = {"uid": generate_uid(), "date": "20260101", "accession": "WTACC0001"}
    alpha = ("WTSYS^ALPHA", "WTSYS-0001")
    manifest = {"study_uid": study["uid"], "patient_id": alpha[1],
                "patient_name": alpha[0], "accession": study["accession"],
                "series": [], "instances": {}}
    for s in (1, 2):
        suid = generate_uid()
        manifest["series"].append(suid)
        for i in (1, 2):
            path = os.path.join(OUT, "study", f"s{s}i{i}.dcm")
            sop = make(template, path, alpha, study, suid, s, i, f"WT SERIES {s}")
            manifest["instances"][sop] = {"series": suid, "file": path,
                                          "dataset_sha256": dataset_sha256(path)}
    path = os.path.join(OUT, "conflict", "c1.dcm")
    sop = make(template, path, ("WTSYS^OTHER", "WTSYS-9999"), study,
               generate_uid(), 9, 1, "WT CONFLICT")
    manifest["conflict"] = {"sop": sop, "patient_id": "WTSYS-9999", "file": path}
    with open(os.path.join(OUT, "manifest.json"), "w") as f:
        json.dump(manifest, f, indent=1)
    print(json.dumps(manifest))


if __name__ == "__main__":
    main()
