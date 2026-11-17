#!/usr/bin/env python3
"""Compare what storescp received with what was stored (runs in rws-e2e-dcmtk).

Usage: wt_compare.py [--tamper] MANIFEST RECV_DIR [SOP_UID ...]

--tamper is the positive control: the comparison runs on a copy of
RECV_DIR in which one byte of one file is flipped, and must fail.

For every expected SOP Instance UID (all of the manifest when none is
given), find the received file in RECV_DIR by its SOP Instance UID and
compare the SHA-256 of its data set (the bytes after the File Meta
Information) with the SHA-256 of the file that was sent to LibreDCM.
Prints one JSON line per instance and a summary; exits 1 when an instance
is missing or differs.
"""
import json
import os
import shutil
import sys

import pydicom

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wt_make_data import dataset_sha256  # noqa: E402


def main():
    tamper = sys.argv[1] == "--tamper"
    args = sys.argv[2:] if tamper else sys.argv[1:]
    manifest = json.load(open(args[0]))
    recv = args[1]
    want = args[2:] or list(manifest["instances"])
    if tamper:
        # Positive control: copy RECV_DIR and flip the last byte (pixel
        # data) of the first file; the comparison must report it.
        bad = "/tmp/recv-tampered"
        shutil.rmtree(bad, ignore_errors=True)
        shutil.copytree(recv, bad)
        first = os.path.join(bad, sorted(os.listdir(bad))[0])
        with open(first, "r+b") as f:
            f.seek(-1, os.SEEK_END)
            b = f.read(1)
            f.seek(-1, os.SEEK_END)
            f.write(bytes([b[0] ^ 0xFF]))
        recv = bad
    got = {}
    for name in sorted(os.listdir(recv)):
        p = os.path.join(recv, name)
        try:
            ds = pydicom.dcmread(p, stop_before_pixels=True)
        except Exception as e:  # not DICOM: report, never pass silently
            print(json.dumps({"file": name, "error": str(e)}))
            continue
        got[str(ds.SOPInstanceUID)] = (p, str(ds.file_meta.TransferSyntaxUID), str(ds.PatientID))
    ok = True
    identical = 0
    for sop in want:
        exp = manifest["instances"][sop]
        if sop not in got:
            print(json.dumps({"sop": sop, "result": "missing"}))
            ok = False
            continue
        p, ts, pid = got[sop]
        sha = dataset_sha256(p)
        same = sha == exp["dataset_sha256"]
        identical += same
        ok = ok and same and pid == manifest["patient_id"]
        print(json.dumps({"sop": sop, "result": "identical" if same else "DIFFERENT",
                          "transfer_syntax": ts, "patient_id": pid,
                          "sent_sha256": exp["dataset_sha256"], "received_sha256": sha}))
    print(json.dumps({"expected": len(want), "received_files": len(got),
                      "identical": identical, "ok": ok}))
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
