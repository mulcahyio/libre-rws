"""Synthetic test data for the LibreDCM conformance statement evidence.

Starts from pydicom's public sample files (no patient data) and gives every
instance new identifiers under the Libre Systems test-data arc
1.3.6.1.4.1.67686.9.1 (LibreDCM test data, docs/qms/uid-registry.md).
Writes to /data.
"""
import copy
import os

import pydicom

SRC = os.path.join(os.path.dirname(pydicom.__file__), "data", "test_files")
OUT = "/data"
ARC = "1.3.6.1.4.1.67686.9.1.900"
os.makedirs(OUT, exist_ok=True)


def make(src, name, study, series, sop, pid, pname, date, time_, acc, modality=None,
         charset=None, sop_class=None, drop=()):
    ds = pydicom.dcmread(os.path.join(SRC, src))
    ds.StudyInstanceUID = study
    ds.SeriesInstanceUID = series
    ds.SOPInstanceUID = sop
    ds.file_meta.MediaStorageSOPInstanceUID = sop
    if sop_class:
        ds.SOPClassUID = sop_class
        ds.file_meta.MediaStorageSOPClassUID = sop_class
    ds.PatientID = pid
    if charset:
        ds.SpecificCharacterSet = charset
    ds.PatientName = pname
    ds.StudyDate = date
    ds.StudyTime = time_
    ds.AccessionNumber = acc
    ds.StudyID = "DCS"
    ds.StudyDescription = "DCS evidence " + name
    if modality:
        ds.Modality = modality
    for kw in drop:
        if kw in ds:
            delattr(ds, kw)
    path = os.path.join(OUT, name + ".dcm")
    ds.save_as(path, write_like_original=False)  # pydicom 2.x in the DCMTK image
    print(f"{name}.dcm  ts={ds.file_meta.TransferSyntaxUID}  class={ds.SOPClassUID}  "
          f"study={study}  sop={sop}  patient={pid}")


S1, S2, S3 = ARC + ".1", ARC + ".2", ARC + ".3"
# Study 1: native (uncompressed) instances, patient DCS-P1.
make("CT_small.dcm", "s1-ct-explicit", S1, S1 + ".1", S1 + ".1.1", "DCS-P1", "DCS^ALPHA", "20260110", "101500", "DCSACC1")
make("MR_small_implicit.dcm", "s1-mr-implicit", S1, S1 + ".2", S1 + ".2.1", "DCS-P1", "DCS^ALPHA", "20260110", "101500", "DCSACC1")
# Study 2: compressed instances, non-ASCII patient name in UTF-8.
p2 = ("DCS-P2", "Müller^Hans", "20260220", "143000", "DCSACC2")
make("MR_small_RLE.dcm", "s2-mr-rle", S2, S2 + ".1", S2 + ".1.1", *p2, charset="ISO_IR 192")
make("MR_small_jp2klossless.dcm", "s2-mr-j2k-lossless", S2, S2 + ".1", S2 + ".1.2", *p2, charset="ISO_IR 192")
make("MR_small_jpeg_ls_lossless.dcm", "s2-mr-jpegls-lossless", S2, S2 + ".1", S2 + ".1.3", *p2, charset="ISO_IR 192")
make("JPGExtended.dcm", "s2-sc-jpeg-extended", S2, S2 + ".2", S2 + ".2.1", *p2, charset="ISO_IR 192")
make("SC_rgb_jpeg_dcmtk.dcm", "s2-sc-jpeg-baseline", S2, S2 + ".2", S2 + ".2.2", *p2, charset="ISO_IR 192")
make("SC_rgb_jpeg_gdcm.dcm", "s2-sc-jpeg-lossless-sv1", S2, S2 + ".2", S2 + ".2.3", *p2, charset="ISO_IR 192")
make("JPEG2000.dcm", "s2-sc-j2k", S2, S2 + ".2", S2 + ".2.4", *p2, charset="ISO_IR 192")
# Study 3: Explicit VR Big Endian file (retired syntax).
make("MR_small_bigendian.dcm", "s3-mr-bigendian", S3, S3 + ".1", S3 + ".1.1", "DCS-P3", "DCS^GAMMA", "20250305", "090000", "DCSACC3")
# Negative cases. (The unknown-SOP-class case is proposed by pynetdicom-probes.py.)
make("CT_small.dcm", "neg-no-study-uid", ARC + ".5", ARC + ".5.1", ARC + ".5.1.1", "DCS-P5", "DCS^EPSILON",
     "20260101", "000000", "DCSACC5", drop=("StudyInstanceUID",))
# Patient conflict: study 1's Study Instance UID with another Patient ID.
make("CT_small.dcm", "conflict-s1-other-patient", S1, S1 + ".9", S1 + ".9.1", "DCS-OTHER", "DCS^OTHER", "20260110", "101500", "DCSACC1")
