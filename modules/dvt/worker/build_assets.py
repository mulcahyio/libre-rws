# SPDX-License-Identifier: Apache-2.0
"""Image build step for LibreDVT: bake in the DICOM standard and the fixtures.

    python build_assets.py /opt/libredvt 2026d

1. Downloads and parses the given DICOM standard edition for dicom-validator,
   then deletes the DocBook sources (only the JSON is used), so LibreDVT
   needs no internet at runtime (DVT-SRS-064).
2. Copies synthetic/public sample files from pydicom's test data into
   plans/fixtures for the bundled LibreDCM plan. pydicom's MR_small_* files
   share one SOP Instance UID; the JPEG-LS copy gets a new deterministic UID
   so every fixture is a distinct instance.
"""
import os
import shutil
import sys

from dicom_validator.spec_reader.edition_reader import EditionReader
from pydicom import dcmread
from pydicom.data import get_testdata_file
from pydicom.uid import generate_uid

FIXTURES = {
    "CT_small.dcm": "ct_explicit_le.dcm",           # Explicit VR Little Endian
    "MR_small_RLE.dcm": "mr_rle.dcm",               # RLE Lossless
    "SC_rgb_jpeg_dcmtk.dcm": "sc_jpeg_baseline.dcm",  # JPEG Baseline (Process 1)
    "SC_rgb_jpeg_gdcm.dcm": "sc_jpeg_lossless.dcm",   # JPEG Lossless SV1
    "693_J2KI.dcm": "ct_j2k.dcm",                   # JPEG 2000
}


def main(base, edition):
    std = os.path.join(base, "standard")
    reader = EditionReader(std)
    ed = reader.get_edition(edition)
    if ed != edition:
        sys.exit(f"edition {edition} not available (got {ed})")
    if reader.get_edition_path(ed) is None:
        sys.exit(f"could not build edition {edition}")
    reader.load_dicom_info(edition)  # proves the JSON is complete
    shutil.rmtree(os.path.join(std, edition, "docbook"), ignore_errors=True)
    print("standard", edition, "->", os.path.join(std, edition, "json"))

    fx = os.path.join(base, "plans", "fixtures")
    os.makedirs(fx, exist_ok=True)
    for src, dst in FIXTURES.items():
        shutil.copyfile(get_testdata_file(src), os.path.join(fx, dst))
    ds = dcmread(get_testdata_file("MR_small_jpeg_ls_lossless.dcm"))
    uid = generate_uid(entropy_srcs=["LibreDVT fixture", "MR_small_jpeg_ls_lossless"])
    ds.SOPInstanceUID = uid
    ds.file_meta.MediaStorageSOPInstanceUID = uid
    ds.save_as(os.path.join(fx, "mr_jpeg_ls.dcm"), enforce_file_format=True)
    print("fixtures:", sorted(os.listdir(fx)))


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
