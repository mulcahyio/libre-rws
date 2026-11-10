# SPDX-License-Identifier: Apache-2.0
"""LibreDVT engine worker.

Runs one DICOM job per process and prints exactly one JSON document on
stdout. The Go side (modules/dvt/internal/engine) decides verdicts; this
worker only reports what it observed. A failed DICOM check is a normal
result (exit 0); a non-zero exit means the worker itself failed, which Go
reports as an engine error (DVT-SRS-007).

LibreDVT must stay independent of LibreDCM's DICOM libraries
(DVT-SRS-080): everything here uses pydicom, pynetdicom and
dicom-validator.

Commands:
  versions
  probe FILE...
  iod --standard-path DIR --edition ED FILE...
  echo   TARGET-ARGS
  store  TARGET-ARGS FILE...
  find   TARGET-ARGS --study UID...
  move   TARGET-ARGS --dest-aet AE --scp-port N --out DIR --study UID...
  reject TARGET-ARGS
  compare --sent FILE... --received DIR
TARGET-ARGS: --host H --port N --called-aet AE --calling-aet AE --timeout SECONDS
"""
import argparse
import hashlib
import json
import logging
import os
import sys
import threading
import time

logging.disable(logging.CRITICAL)  # pynetdicom logs to stderr otherwise

WORKER_VERSION = "0.1.0"


def out(doc):
    json.dump(doc, sys.stdout, sort_keys=True)
    sys.stdout.write("\n")


# ---------------------------------------------------------------- versions

def cmd_versions(args):
    import pydicom
    import pynetdicom
    from importlib.metadata import version

    doc = {
        "worker": WORKER_VERSION,
        "python": sys.version.split()[0],
        "pydicom": pydicom.__version__,
        "pynetdicom": pynetdicom.__version__,
        "dicom-validator": version("dicom-validator"),
    }
    if args.standard_path and args.edition:
        doc["standard-edition"] = args.edition if _edition_present(args.standard_path, args.edition) else "missing"
    out(doc)


def _edition_present(path, edition):
    return os.path.isdir(os.path.join(path, edition, "json"))


# ---------------------------------------------------------------- probe

def _read(path, pixels=False):
    import pydicom
    from pydicom.errors import InvalidDicomError

    try:
        return pydicom.dcmread(path, stop_before_pixels=not pixels), None
    except InvalidDicomError:
        return None, "not a DICOM file"
    except Exception as e:  # unreadable, truncated, permission...
        return None, f"cannot read: {e.__class__.__name__}: {e}"


def probe_one(path):
    ds, err = _read(path)
    if ds is None:
        return {"file": path, "dicom": False, "error": err}
    meta = getattr(ds, "file_meta", None)
    return {
        "file": path,
        "dicom": True,
        "sop_class_uid": str(ds.get("SOPClassUID", "")),
        "sop_instance_uid": str(ds.get("SOPInstanceUID", "")),
        "study_instance_uid": str(ds.get("StudyInstanceUID", "")),
        "transfer_syntax_uid": str(getattr(meta, "TransferSyntaxUID", "")) if meta else "",
    }


def cmd_probe(args):
    out({"files": [probe_one(f) for f in args.files]})


# ---------------------------------------------------------------- iod

def cmd_iod(args):
    from dicom_validator.spec_reader.edition_reader import EditionReader
    from dicom_validator.validator.dicom_file_validator import DicomFileValidator
    from dicom_validator.validator.error_handler import (
        NullValidationResultHandler,
        ValidationResultFormatter,
    )
    from dicom_validator.validator.validation_result import ErrorCode, Status

    if not _edition_present(args.standard_path, args.edition):
        print(f"standard edition {args.edition} not found in {args.standard_path}", file=sys.stderr)
        sys.exit(3)
    # load_dicom_info reads the local JSON only: no network at runtime (DVT-SRS-064).
    info = EditionReader(args.standard_path).load_dicom_info(args.edition)
    validator = DicomFileValidator(info, error_handler=NullValidationResultHandler())
    fmt = ValidationResultFormatter(info.dictionary)
    files = []
    for path in args.files:
        res = validator.validate_file(path)[path]
        entry = {"file": path, "status": res.status.name, "findings": []}
        if res.status not in (Status.Passed, Status.Failed):
            entry["findings"].append({"severity": "error", "message": fmt.failed_validation_message(res)})
        for module, tag_errors in sorted(res.module_errors.items()):
            for tag, err in sorted(tag_errors.items(), key=lambda kv: kv[0]):
                if not err.is_error():
                    continue
                name = info.dictionary.get(str(tag.tag), {}).get("name", "")
                path_str = str(tag)
                # Attributes outside the IOD's modules (standard extended SOP
                # class, trailing padding) do not fail the file in
                # dicom-validator either; dciodvfy reports them as warnings.
                sev = "warning" if err.code == ErrorCode.TagUnexpected else "error"
                entry["findings"].append({
                    "severity": sev,
                    "module": module,
                    "tag": path_str,
                    "attribute": name,
                    "message": f"{path_str} ({name}){fmt.error_message(err)}".replace("\n", " ").strip(),
                })
        files.append(entry)
    out({"files": files, "edition": args.edition})


# ---------------------------------------------------------------- network helpers

def _ae(calling, timeout):
    from pynetdicom import AE

    ae = AE(ae_title=calling)
    ae.acse_timeout = timeout
    ae.dimse_timeout = timeout
    ae.network_timeout = timeout
    ae.connection_timeout = timeout
    return ae


def _assoc_failure(assoc):
    if assoc.is_rejected:
        return {"rejected": True, "error": "association rejected"}
    if assoc.is_aborted:
        return {"rejected": False, "error": "association aborted"}
    return {"rejected": False, "error": "association failed (no answer or connection refused)"}


def _associate(ae, a, contexts=None):
    return ae.associate(a.host, a.port, contexts=contexts, ae_title=a.called_aet)


def _status(ds):
    return int(ds.Status) if ds is not None and "Status" in ds else None


# ---------------------------------------------------------------- echo

def cmd_echo(args):
    from pynetdicom.sop_class import Verification

    ae = _ae(args.calling_aet, args.timeout)
    ae.add_requested_context(Verification)
    assoc = _associate(ae, args)
    if not assoc.is_established:
        out({"associated": False, **_assoc_failure(assoc)})
        return
    st = _status(assoc.send_c_echo())
    assoc.release()
    out({"associated": True, "status": st})


# ---------------------------------------------------------------- reject

def cmd_reject(args):
    from pynetdicom.sop_class import Verification

    ae = _ae(args.calling_aet, args.timeout)
    ae.add_requested_context(Verification)
    assoc = _associate(ae, args)
    if assoc.is_established:
        assoc.release()
        out({"associated": True, "rejected": False})
        return
    rej = assoc.is_rejected
    out({"associated": False, "rejected": bool(rej), "aborted": bool(assoc.is_aborted),
         "error": "" if rej else "no association rejection received (connection failed or aborted)"})


# ---------------------------------------------------------------- store

def cmd_store(args):
    from pynetdicom.presentation import build_context

    infos = []
    for f in args.files:
        p = probe_one(f)
        infos.append(p)
    pairs = sorted({(p["sop_class_uid"], p["transfer_syntax_uid"]) for p in infos if p.get("dicom")})
    if len(pairs) > 128:
        print("more than 128 SOP class / transfer syntax pairs in one store step", file=sys.stderr)
        sys.exit(3)
    ae = _ae(args.calling_aet, args.timeout)
    contexts = [build_context(sc, [ts]) for sc, ts in pairs]
    results = []
    assoc = _associate(ae, args, contexts) if contexts else None
    if contexts and not assoc.is_established:
        out({"associated": False, **_assoc_failure(assoc),
             "files": [{"file": p["file"], "status": None, "error": "not sent"} for p in infos]})
        return
    for p in infos:
        r = {"file": p["file"], "sop_instance_uid": p.get("sop_instance_uid", ""),
             "study_instance_uid": p.get("study_instance_uid", ""),
             "transfer_syntax_uid": p.get("transfer_syntax_uid", "")}
        if not p.get("dicom"):
            r.update(status=None, error=p.get("error", "not a DICOM file"))
        else:
            accepted = [c for c in assoc.accepted_contexts
                        if c.abstract_syntax == p["sop_class_uid"] and p["transfer_syntax_uid"] in c.transfer_syntax]
            if not accepted:
                r.update(status=None, error="presentation context not accepted by the target")
            else:
                ds, err = _read(p["file"], pixels=True)
                if ds is None:
                    r.update(status=None, error=err)
                else:
                    try:
                        r["status"] = _status(assoc.send_c_store(ds))
                    except Exception as e:
                        r.update(status=None, error=f"{e.__class__.__name__}: {e}")
        results.append(r)
    if assoc is not None and assoc.is_established:
        assoc.release()
    out({"associated": assoc is not None, "files": results})


# ---------------------------------------------------------------- find

def cmd_find(args):
    from pydicom.dataset import Dataset
    from pynetdicom.sop_class import StudyRootQueryRetrieveInformationModelFind

    ae = _ae(args.calling_aet, args.timeout)
    ae.add_requested_context(StudyRootQueryRetrieveInformationModelFind)
    assoc = _associate(ae, args)
    if not assoc.is_established:
        out({"associated": False, **_assoc_failure(assoc)})
        return
    queries = []
    for uid in args.study:
        q = Dataset()
        q.QueryRetrieveLevel = "STUDY"
        q.StudyInstanceUID = uid
        found, final, pending = [], None, 0
        for status, ident in assoc.send_c_find(q, StudyRootQueryRetrieveInformationModelFind):
            st = _status(status)
            if st in (0xFF00, 0xFF01):
                pending += 1
                if ident is not None and "StudyInstanceUID" in ident:
                    found.append(str(ident.StudyInstanceUID))
            else:
                final = st
        queries.append({"study": uid, "final_status": final, "matches": found, "pending": pending})
    assoc.release()
    out({"associated": True, "queries": queries})


# ---------------------------------------------------------------- move

def cmd_move(args):
    from pydicom.dataset import Dataset
    from pydicom.filewriter import write_file_meta_info
    from pynetdicom import AE, ALL_TRANSFER_SYNTAXES, AllStoragePresentationContexts, evt
    from pynetdicom.sop_class import StudyRootQueryRetrieveInformationModelMove

    os.makedirs(args.out, exist_ok=True)
    received, lock = [], threading.Lock()

    def on_store(event):
        uid = str(event.request.AffectedSOPInstanceUID)
        name = hashlib.sha256(uid.encode()).hexdigest()[:32] + ".dcm"
        path = os.path.join(args.out, name)
        # Write the bytes as received (no re-encoding), so pixel comparison is exact.
        with open(path, "wb") as fh:
            fh.write(b"\x00" * 128 + b"DICM")
            write_file_meta_info(fh, event.file_meta, enforce_standard=True)
            fh.write(event.request.DataSet.getvalue())
        with lock:
            received.append({"sop_instance_uid": uid, "file": path,
                             "transfer_syntax_uid": str(event.context.transfer_syntax),
                             "calling_aet": event.assoc.requestor.ae_title})
        return 0x0000

    scp = AE(ae_title=args.dest_aet)
    scp.network_timeout = args.timeout
    scp.dimse_timeout = args.timeout
    for cx in AllStoragePresentationContexts:
        scp.add_supported_context(cx.abstract_syntax, ALL_TRANSFER_SYNTAXES)
    # Only the target may store to the built-in SCP during this step.
    scp.require_calling_aet = [args.called_aet]
    server = scp.start_server(("0.0.0.0", args.scp_port), block=False,
                              evt_handlers=[(evt.EVT_C_STORE, on_store)])
    try:
        ae = _ae(args.calling_aet, args.timeout)
        ae.add_requested_context(StudyRootQueryRetrieveInformationModelMove)
        assoc = _associate(ae, args)
        if not assoc.is_established:
            out({"associated": False, **_assoc_failure(assoc), "received": []})
            return
        moves = []
        for uid in args.study:
            q = Dataset()
            q.QueryRetrieveLevel = "STUDY"
            q.StudyInstanceUID = uid
            final, last = None, None
            for status, _ in assoc.send_c_move(q, args.dest_aet, StudyRootQueryRetrieveInformationModelMove):
                st = _status(status)
                if st is None:
                    continue
                last = status
                if st not in (0xFF00,):
                    final = st
            entry = {"study": uid, "final_status": final}
            if last is not None:
                for k in ("NumberOfCompletedSuboperations", "NumberOfFailedSuboperations",
                          "NumberOfWarningSuboperations"):
                    if k in last:
                        entry[k] = int(last.get(k))
            moves.append(entry)
        assoc.release()
        time.sleep(0.5)  # let the last store association finish
    finally:
        server.shutdown()
    with lock:
        rec = list(received)
    out({"associated": True, "moves": moves, "received": rec})


# ---------------------------------------------------------------- compare

def _pixel_digest(path):
    ds, err = _read(path, pixels=True)
    if ds is None:
        return None, None, err
    raw = ds.get("PixelData")  # bytes as stored (encapsulated stays encapsulated)
    ts = str(ds.file_meta.TransferSyntaxUID) if hasattr(ds, "file_meta") and "TransferSyntaxUID" in ds.file_meta else ""
    return str(ds.get("SOPInstanceUID", "")), (hashlib.sha256(raw).hexdigest() if raw is not None else "", ts), None


def cmd_compare(args):
    recv = {}
    for name in sorted(os.listdir(args.received)):
        uid, dig, err = _pixel_digest(os.path.join(args.received, name))
        if uid:
            recv[uid] = (dig, os.path.join(args.received, name))
    results = []
    for f in args.sent:
        uid, dig, err = _pixel_digest(f)
        r = {"file": f, "sop_instance_uid": uid or ""}
        if err:
            r.update(match=False, error=err)
        elif uid not in recv:
            r.update(match=False, error="not received")
        else:
            (rdig, rts), rpath = recv[uid]
            sdig, sts = dig
            r.update(received=rpath, sent_pixel_sha256=sdig, received_pixel_sha256=rdig,
                     sent_transfer_syntax=sts, received_transfer_syntax=rts,
                     match=(sdig == rdig and sts == rts))
            if not r["match"]:
                r["error"] = "transfer syntax changed" if sdig == rdig else "pixel data differs"
        results.append(r)
    out({"files": results})


# ---------------------------------------------------------------- main

def main(argv=None):
    p = argparse.ArgumentParser(prog="libredvt_worker")
    sub = p.add_subparsers(dest="cmd", required=True)

    v = sub.add_parser("versions")
    v.add_argument("--standard-path")
    v.add_argument("--edition")

    pr = sub.add_parser("probe")
    pr.add_argument("files", nargs="+")

    i = sub.add_parser("iod")
    i.add_argument("--standard-path", required=True)
    i.add_argument("--edition", required=True)
    i.add_argument("files", nargs="+")

    def target(sp):
        sp.add_argument("--host", required=True)
        sp.add_argument("--port", type=int, required=True)
        sp.add_argument("--called-aet", required=True)
        sp.add_argument("--calling-aet", required=True)
        sp.add_argument("--timeout", type=float, default=30)

    for name in ("echo", "reject"):
        target(sub.add_parser(name))
    s = sub.add_parser("store")
    target(s)
    s.add_argument("files", nargs="+")
    f = sub.add_parser("find")
    target(f)
    f.add_argument("--study", action="append", required=True)
    m = sub.add_parser("move")
    target(m)
    m.add_argument("--dest-aet", required=True)
    m.add_argument("--scp-port", type=int, required=True)
    m.add_argument("--out", required=True)
    m.add_argument("--study", action="append", required=True)
    c = sub.add_parser("compare")
    c.add_argument("--sent", nargs="+", required=True)
    c.add_argument("--received", required=True)

    args = p.parse_args(argv)
    {"versions": cmd_versions, "probe": cmd_probe, "iod": cmd_iod, "echo": cmd_echo,
     "reject": cmd_reject, "store": cmd_store, "find": cmd_find, "move": cmd_move,
     "compare": cmd_compare}[args.cmd](args)


if __name__ == "__main__":
    main()
