# SPDX-License-Identifier: Apache-2.0
"""Fake DICOM archive for LibreDVT's positive-control tests (tool validation).

A small pynetdicom Storage / Query / Move SCP whose misbehaviour can be
chosen, so LibreDVT's tests can prove that every kind of failure is detected:

  ok            behaves correctly
  refuse-store  answers every C-STORE with 0xA700 (Out of Resources)
  drop-one      C-MOVE sends all but one instance and still reports success
  alter-pixels  C-MOVE flips one byte of pixel data in every instance
  accept-any    accepts associations for any called AE title

  python fake_scp.py --port 11199 --ae FAKEPACS --mode ok --dest LIBREDVT=127.0.0.1:11115
"""
import argparse
import logging
import signal
import sys
import threading

from pydicom.dataset import Dataset
from pynetdicom import AE, ALL_TRANSFER_SYNTAXES, AllStoragePresentationContexts, evt
from pynetdicom.presentation import build_context
from pynetdicom.sop_class import (
    StudyRootQueryRetrieveInformationModelFind,
    StudyRootQueryRetrieveInformationModelMove,
    Verification,
)

logging.disable(logging.CRITICAL)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--port", type=int, required=True)
    p.add_argument("--ae", default="FAKEPACS")
    p.add_argument("--mode", default="ok")
    p.add_argument("--dest", action="append", default=[], help="AE=HOST:PORT move destination")
    a = p.parse_args()
    dests = {}
    for d in a.dest:
        name, hp = d.split("=", 1)
        host, port = hp.rsplit(":", 1)
        dests[name] = (host, int(port))

    store, lock = {}, threading.Lock()  # SOP Instance UID -> Dataset (with file_meta)

    def on_store(event):
        if a.mode == "refuse-store":
            return 0xA700
        ds = event.dataset
        ds.file_meta = event.file_meta
        with lock:
            store[str(ds.SOPInstanceUID)] = ds
        return 0x0000

    def on_find(event):
        q = event.identifier
        uid = str(q.get("StudyInstanceUID", ""))
        with lock:
            studies = sorted({str(d.StudyInstanceUID) for d in store.values()})
        for s in studies:
            if uid in ("", "*", s):
                r = Dataset()
                r.QueryRetrieveLevel = "STUDY"
                r.StudyInstanceUID = s
                yield 0xFF00, r

    def on_move(event):
        q = event.identifier
        uid = str(q.get("StudyInstanceUID", ""))
        if event.move_destination not in dests:
            yield 0xA801, None
            return
        host, port = dests[event.move_destination]
        with lock:
            items = [d for d in store.values() if str(d.StudyInstanceUID) == uid]
        if a.mode == "drop-one" and items:
            items = items[1:]
        yield host, port, {"contexts": [build_context(d.SOPClassUID, [d.file_meta.TransferSyntaxUID]) for d in items] or None}
        yield len(items)
        for d in items:
            if event.is_cancelled:
                yield 0xFE00, None
                return
            if a.mode == "alter-pixels" and "PixelData" in d:
                b = bytearray(d.PixelData)
                b[len(b) // 2] ^= 0xFF
                d = d.copy()
                d.PixelData = bytes(b)
            yield 0xFF00, d

    ae = AE(ae_title=a.ae)
    ae.require_called_aet = a.mode != "accept-any"
    for cx in AllStoragePresentationContexts:
        ae.add_supported_context(cx.abstract_syntax, ALL_TRANSFER_SYNTAXES)
    for sc in (Verification, StudyRootQueryRetrieveInformationModelFind, StudyRootQueryRetrieveInformationModelMove):
        ae.add_supported_context(sc)
    handlers = [(evt.EVT_C_STORE, on_store), (evt.EVT_C_FIND, on_find), (evt.EVT_C_MOVE, on_move)]
    server = ae.start_server(("127.0.0.1", a.port), block=False, evt_handlers=handlers)
    print("ready", flush=True)
    signal.signal(signal.SIGTERM, lambda *_: (server.shutdown(), sys.exit(0)))
    signal.pause()


if __name__ == "__main__":
    main()
