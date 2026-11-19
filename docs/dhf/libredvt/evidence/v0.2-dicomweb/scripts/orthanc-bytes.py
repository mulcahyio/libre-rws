#!/usr/bin/env python3
"""Independent end-to-end check of what Orthanc stored: Orthanc's own REST
API (not DICOMweb, not LibreDVT's worker) lists the instances and serves
each stored file; every file's SHA-256 must equal one fixture's.

usage: orthanc-bytes.py BASE CAFILE FIXTURE-DIR
Prints the Orthanc and DICOMweb plugin versions. Exit 0 when every stored
file is byte-identical to a fixture and every fixture was stored.
"""
import glob
import hashlib
import json
import os
import ssl
import sys
import urllib.request

base, ca, fx = sys.argv[1], sys.argv[2], sys.argv[3]
ctx = ssl.create_default_context(cafile=ca)


def get(path):
    with urllib.request.urlopen(base + path, context=ctx, timeout=30) as r:
        return r.read()


system = json.loads(get("/system"))
plugin = json.loads(get("/plugins/dicom-web"))
print(f"Orthanc {system.get('Version')}, DICOMweb plugin {plugin.get('Version')}")
want = {}
for f in sorted(glob.glob(os.path.join(fx, "*.dcm"))):
    with open(f, "rb") as fh:
        want[hashlib.sha256(fh.read()).hexdigest()] = os.path.basename(f)
ids = json.loads(get("/instances"))
seen = set()
bad = 0
for i in ids:
    tags = json.loads(get(f"/instances/{i}/simplified-tags"))
    digest = hashlib.sha256(get(f"/instances/{i}/file")).hexdigest()
    name = want.get(digest)
    print(f"{tags.get('SOPInstanceUID')}: {'identical to ' + name if name else 'NOT identical to any fixture'}")
    if name:
        seen.add(name)
    else:
        bad += 1
missing = sorted(set(want.values()) - seen)
print(f"stored {len(ids)} instance(s); {len(seen)} of {len(want)} fixtures byte-identical; missing {missing}")
sys.exit(1 if bad or missing else 0)
