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
  associate TARGET-ARGS --context AS;TS1,TS2[;ROLES]... [--async-ops I,P]
  tls-probe TARGET-ARGS --mode plaintext|tls_version|no_client_cert [--tls-version 1.0|1.1]
TARGET-ARGS: --host H --port N --called-aet AE --calling-aet AE --timeout SECONDS
             [--impl-uid UID --impl-version NAME]  (LibreDVT's DICOM identification, DVT-SRS-082)
             [--tls --tls-min 1.2|1.3 --ca-file F --server-name N --client-cert F --client-key F]
             (tls-probe takes no client certificate: a probe never carries credentials, DVT-SRS-106)
move also takes [--scp-tls-cert F --scp-tls-key F --scp-client-cert required|optional]: the
built-in Storage SCP then listens with TLS and checks the target's client certificate
against --ca-file (DVT-SRS-108).

Secrets (client private keys) reach the worker only as file paths; the worker
never prints their contents (DVT-SRS-153, DVT-SRS-107).
"""
import argparse
import datetime
import hashlib
import json
import logging
import os
import socket
import ssl
import struct
import sys
import threading
import time

# Nothing is logged to stderr: the root logger gets a null handler, and
# pynetdicom's errors are captured (TLS failures are reported as facts).
logging.root.addHandler(logging.NullHandler())
logging.getLogger("pydicom").setLevel(logging.CRITICAL + 1)

WORKER_VERSION = "0.2.0"


def out(doc):
    tls = globals().get("_TLS") or {}
    if tls.get("used"):  # v0.1 network commands over TLS (DVT-SRS-107)
        if tls.get("facts"):
            doc.setdefault("tls", tls["facts"])
        if tls.get("error"):
            doc.setdefault("tls_error", tls["error"])
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
        "series_instance_uid": str(ds.get("SeriesInstanceUID", "")),
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


# ---------------------------------------------------------------- TLS (DVT-SRS-100..107)
#
# One client context factory for every command (architecture.md §7, decision
# 3). There is no switch that turns verification off (DVT-SRS-103).

TLS_MIN = {"1.2": ssl.TLSVersion.TLSv1_2, "1.3": ssl.TLSVersion.TLSv1_3}


class ConfigError(Exception):
    """A TLS file or setting is unusable: nothing was sent (DVT-SRS-104)."""


def _check_client_policy(ctx):
    """Refuse a context that would not enforce the TLS policy."""
    if (ctx.verify_mode != ssl.CERT_REQUIRED or not ctx.check_hostname
            or ctx.hostname_checks_common_name
            or ctx.minimum_version < ssl.TLSVersion.TLSv1_2):
        raise ConfigError("TLS context does not enforce certificate and host-name verification with TLS 1.2+")


def _load_trust(ctx, args):
    try:
        if args.ca_file:
            ctx.load_verify_locations(cafile=args.ca_file)
        else:
            ctx.load_default_certs(ssl.Purpose.SERVER_AUTH)
    except (OSError, ssl.SSLError, ValueError) as e:
        raise ConfigError(f"CA file not usable: {e.__class__.__name__}") from None


def tls_client_context(args, client_cert=True):
    """TLS 1.2+ (or 1.3), CA file or system store, SAN host-name check, optional
    client certificate (DVT-SRS-100, -101, -102, -104)."""
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)  # CERT_REQUIRED and check_hostname
    ctx.minimum_version = TLS_MIN[args.tls_min]
    ctx.hostname_checks_common_name = False  # subjectAltName only (DVT-SRS-102)
    _load_trust(ctx, args)
    if client_cert and getattr(args, "client_cert", None):
        try:
            ctx.load_cert_chain(args.client_cert, args.client_key)
        except (OSError, ssl.SSLError, ValueError) as e:
            # The message names the problem (e.g. key values mismatch), never key bytes.
            reason = getattr(e, "reason", None) or e.__class__.__name__
            raise ConfigError(f"client certificate or key not usable: {reason}") from None
    _check_client_policy(ctx)
    return ctx


def _server_name(args):
    return args.server_name or args.host


def _now():
    return datetime.datetime.now(datetime.timezone.utc)


def _rfc3339(t):
    return t.strftime("%Y-%m-%dT%H:%M:%S.") + f"{t.microsecond // 1000:03d}Z"


_RDN = {"commonName": "CN", "organizationName": "O", "organizationalUnitName": "OU",
        "countryName": "C", "localityName": "L", "stateOrProvinceName": "ST"}


def _dn(rdns):
    return ",".join(f"{_RDN.get(k, k)}={v}" for rdn in rdns for k, v in rdn)


def _cert_time(s):
    return _rfc3339(datetime.datetime.fromtimestamp(ssl.cert_time_to_seconds(s), datetime.timezone.utc))


def tls_facts(sock, client_cert_sent):
    """TLS facts of an established connection (DVT-SRS-107). Never key material."""
    info = {"version": sock.version() or "", "cipher": (sock.cipher() or ("",))[0],
            "verified": True, "client_cert_sent": bool(client_cert_sent)}
    der = sock.getpeercert(binary_form=True)
    d = sock.getpeercert() or {}
    if der:
        san = d.get("subjectAltName", ())
        info["peer"] = {
            "subject": _dn(d.get("subject", ())),
            "issuer": _dn(d.get("issuer", ())),
            "san_dns": [v for k, v in san if k == "DNS"],
            "san_ip": [v.strip() for k, v in san if k == "IP Address"],
            "not_before": _cert_time(d["notBefore"]) if "notBefore" in d else "",
            "not_after": _cert_time(d["notAfter"]) if "notAfter" in d else "",
            "sha256": hashlib.sha256(der).hexdigest(),
        }
    return info


def classify_error(e):
    """(observation, problem kind, message) for a connection or TLS error.
    The worker reports facts; Go decides what they mean."""
    if isinstance(e, ssl.SSLCertVerificationError):
        return "cert_verify_failed", "tls", f"certificate verification failed: {e.verify_message} (code {e.verify_code})"
    if isinstance(e, (socket.timeout, TimeoutError)):
        return "timeout", "timeout", "no answer within the timeout"
    if isinstance(e, (ssl.SSLEOFError, ssl.SSLZeroReturnError)):
        return "connection_closed", "connection_closed", "the target closed the connection"
    if isinstance(e, (ConnectionResetError, BrokenPipeError, ConnectionAbortedError)):
        return "connection_reset", "connection_closed", f"the connection was reset ({e.__class__.__name__})"
    if isinstance(e, ssl.SSLError):
        reason = e.reason or ""
        if reason in ("NO_PROTOCOLS_AVAILABLE", "NO_CIPHERS_AVAILABLE", "NO_SUITABLE_SIGNATURE_ALGORITHM"):
            return "local_error", "engine", f"the TLS engine cannot offer this handshake: {reason}"
        if "ALERT" in reason:
            return "handshake_alert", "tls", f"the target sent a TLS alert: {reason}"
        return "handshake_error", "tls", f"TLS handshake failed: {reason or e.__class__.__name__}"
    if isinstance(e, ConnectionRefusedError):
        return "connect_error", "connection_closed", "connection refused"
    if isinstance(e, OSError):
        return "connect_error", "connection_closed", f"connection failed: {e.__class__.__name__}: {e.strerror or e}"
    return "local_error", "engine", f"{e.__class__.__name__}: {e}"


class Exchanges:
    """Collects exchanges in the evidence-model shape (evidence-model.md §3.2)."""

    def __init__(self, endpoint):
        self.endpoint = endpoint
        self.items = []

    def start(self, protocol, operation):
        x = {"seq": len(self.items) + 1, "protocol": protocol, "operation": operation,
             "endpoint": self.endpoint, "started": _rfc3339(_now()), "duration_ms": 0,
             "outcome": "completed", "_t0": time.monotonic()}
        self.items.append(x)
        return x

    @staticmethod
    def done(x, outcome="completed", kind=None, message=None):
        x["duration_ms"] = int((time.monotonic() - x.pop("_t0")) * 1000)
        x["outcome"] = outcome
        if kind:
            x["error"] = {"kind": kind, "message": message}

    def doc(self):
        for x in self.items:
            if "_t0" in x:  # never finished: an engine bug, reported as such
                self.done(x, "error", "engine", "exchange not finished")
        return self.items


# ---------------------------------------------------------------- PDUs (PS3.8 §9.3)
#
# associate and tls-probe speak the upper-layer protocol directly, so every
# byte the target sends is visible: what it accepted, rejected, aborted,
# closed or garbled (DVT-SRS-090..096). Peers in LibreDVT's own tests are
# written independently in Go and DCMTK.

APP_CONTEXT = "1.2.840.10008.3.1.1.1"
MAX_PDU_IN = 1 << 20  # largest A-ASSOCIATE-AC/RJ/A-ABORT/A-RELEASE PDU we read
PROPOSED_MAX_PDU = 16384
PDU_NAMES = {1: "A-ASSOCIATE-RQ", 2: "A-ASSOCIATE-AC", 3: "A-ASSOCIATE-RJ", 4: "P-DATA-TF",
             5: "A-RELEASE-RQ", 6: "A-RELEASE-RP", 7: "A-ABORT"}
CONTEXT_RESULT = {1: "user-rejection", 2: "no-reason", 3: "abstract-syntax-not-supported",
                  4: "transfer-syntaxes-not-supported"}


class Closed(Exception):
    def __init__(self, got):
        super().__init__(f"connection closed after {got} byte(s) of a PDU")
        self.got = got


class Malformed(Exception):
    pass


def _item(t, payload):
    return struct.pack(">BBH", t, 0, len(payload)) + payload


def _ae_field(s):
    return s.encode("ascii").ljust(16, b" ")[:16]


def build_rq(called, calling, contexts, impl_uid, impl_version, async_ops=None):
    """A-ASSOCIATE-RQ. contexts: list of (abstract syntax, [transfer syntaxes],
    roles or None) with presentation context IDs 1, 3, 5, ..."""
    var = _item(0x10, APP_CONTEXT.encode())
    roles = []
    for i, (asx, tss, r) in enumerate(contexts):
        sub = _item(0x30, asx.encode()) + b"".join(_item(0x40, ts.encode()) for ts in tss)
        var += _item(0x20, struct.pack(">BBBB", 2 * i + 1, 0, 0, 0) + sub)
        if r is not None and asx not in [u for u, _ in roles]:
            roles.append((asx, r))
    ui = _item(0x51, struct.pack(">I", PROPOSED_MAX_PDU)) + _item(0x52, impl_uid.encode())
    if async_ops is not None:
        ui += _item(0x53, struct.pack(">HH", *async_ops))
    for uid, (scu, scp) in roles:
        ui += _item(0x54, struct.pack(">H", len(uid)) + uid.encode() + bytes([scu, scp]))
    ui += _item(0x55, impl_version.encode())
    var += _item(0x50, ui)
    body = struct.pack(">HH", 1, 0) + _ae_field(called) + _ae_field(calling) + b"\x00" * 32 + var
    return struct.pack(">BBI", 1, 0, len(body)) + body


RELEASE_RQ = struct.pack(">BBI", 5, 0, 4) + b"\x00" * 4
ABORT_RQ = struct.pack(">BBI", 7, 0, 4) + b"\x00\x00\x00\x00"  # source 0 (service-user), reason 0


def _recv_exact(sock, n, got=0):
    buf = b""
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise Closed(got + len(buf))
        buf += chunk
    return buf


def read_pdu(sock):
    hdr = _recv_exact(sock, 6)
    t, _, length = struct.unpack(">BBI", hdr)
    if t not in PDU_NAMES:
        raise Malformed(f"unknown PDU type 0x{t:02X} (first bytes {hdr.hex()})")
    if length > MAX_PDU_IN:
        raise Malformed(f"{PDU_NAMES[t]} length {length} exceeds {MAX_PDU_IN}")
    return t, _recv_exact(sock, length, 6)


def _uid_text(b):
    return b.rstrip(b"\x00 ").decode("ascii", "replace")


def _items(b, where):
    pos = 0
    while pos < len(b):
        if pos + 4 > len(b):
            raise Malformed(f"truncated item header in {where}")
        t, _, ln = struct.unpack(">BBH", b[pos:pos + 4])
        if pos + 4 + ln > len(b):
            raise Malformed(f"item 0x{t:02X} in {where} runs past its container")
        yield t, b[pos + 4:pos + 4 + ln]
        pos += 4 + ln


def parse_ac(body):
    """Facts of an A-ASSOCIATE-AC: contexts by ID, user information."""
    if len(body) < 68:
        raise Malformed(f"A-ASSOCIATE-AC is {len(body)} bytes, shorter than its fixed fields")
    ac = {"contexts": {}, "max_pdu": None, "impl_class_uid": "", "impl_version": "",
          "async_ops": None, "roles": {}, "duplicate_ids": []}
    for t, v in _items(body[68:], "A-ASSOCIATE-AC"):
        if t == 0x21:
            if len(v) < 4:
                raise Malformed("presentation context item too short")
            pcid, result = v[0], v[2]
            ts = ""
            for st, sv in _items(v[4:], "presentation context"):
                if st == 0x40:
                    ts = _uid_text(sv)
            if pcid in ac["contexts"]:
                ac["duplicate_ids"].append(pcid)
            ac["contexts"][pcid] = (result, ts)
        elif t == 0x50:
            for st, sv in _items(v, "user information"):
                if st == 0x51 and len(sv) == 4:
                    ac["max_pdu"] = struct.unpack(">I", sv)[0]
                elif st == 0x52:
                    ac["impl_class_uid"] = _uid_text(sv)
                elif st == 0x53 and len(sv) == 4:
                    ac["async_ops"] = struct.unpack(">HH", sv)
                elif st == 0x54 and len(sv) >= 4:
                    n = struct.unpack(">H", sv[:2])[0]
                    if len(sv) != 2 + n + 2:
                        raise Malformed("role selection item length does not match its UID length")
                    ac["roles"][_uid_text(sv[2:2 + n])] = (sv[2 + n], sv[3 + n])
                elif st == 0x55:
                    ac["impl_version"] = sv.decode("ascii", "replace").rstrip(" \x00")
    return ac


def _connect(args, xs, protocol="dimse"):
    """TCP connect, then TLS when configured. Returns (sock, None) or
    (None, (observation, kind, message)) with the failure in the exchanges."""
    x = xs.start(protocol, "TCP-CONNECT")
    try:
        sock = socket.create_connection((args.host, args.port), timeout=args.timeout)
    except Exception as e:
        obs, kind, msg = classify_error(e)
        xs.done(x, "error", kind, msg)
        return None, (obs, kind, msg)
    xs.done(x)
    xs.items.pop()  # a plain TCP connect is not an exchange of its own
    return sock, None


def _tls_handshake(sock, ctx, args, xs, client_cert_sent):
    x = xs.start("tls", "TLS-HANDSHAKE")
    try:
        ts = ctx.wrap_socket(sock, server_hostname=_server_name(args), do_handshake_on_connect=False)
        ts.settimeout(args.timeout)
        ts.do_handshake()
    except Exception as e:
        obs, kind, msg = classify_error(e)
        info = {"version": "", "cipher": "", "verified": False, "client_cert_sent": False}
        if obs == "cert_verify_failed":
            info["verify_error"] = msg
        x["tls"] = info
        xs.done(x, "error" if obs not in ("handshake_alert", "connection_closed", "connection_reset") else "refused", kind, msg)
        sock.close()
        return None, (obs, kind, msg)
    x["tls"] = tls_facts(ts, client_cert_sent)
    xs.done(x)
    return ts, None


def _parse_context_arg(s):
    parts = s.split(";")
    if len(parts) not in (2, 3) or not parts[0] or not parts[1]:
        raise SystemExit(f"bad --context {s!r}")
    roles = None
    if len(parts) == 3:
        if parts[2] not in ("00", "01", "10", "11"):
            raise SystemExit(f"bad roles in --context {s!r}")
        roles = (int(parts[2][0]), int(parts[2][1]))
    return parts[0], parts[1].split(","), roles


def _send_abort(sock):
    try:
        sock.sendall(ABORT_RQ)
    except Exception:
        pass


def _read_answer(sock, x, xs):
    """Read one PDU; on failure finish x as an error. Returns (type, body) or None."""
    try:
        return read_pdu(sock)
    except Closed as e:
        if e.got == 0:
            xs.done(x, "error", "connection_closed", "the target closed the connection without answering")
        else:
            xs.done(x, "error", "protocol", f"malformed PDU: {e}")
    except Malformed as e:
        xs.done(x, "error", "protocol", f"malformed PDU: {e}")
    except Exception as e:
        _, kind, msg = classify_error(e)
        xs.done(x, "error", kind, msg)
    return None


def cmd_associate(args):
    contexts = [_parse_context_arg(c) for c in args.context]
    if len(contexts) > 128:
        raise SystemExit("more than 128 presentation contexts")
    async_ops = None
    if args.async_ops:
        i, p = args.async_ops.split(",")
        async_ops = (int(i), int(p))
    xs = Exchanges(f"{args.host}:{args.port}")
    ctx = None
    if args.tls:
        try:
            ctx = tls_client_context(args)
        except ConfigError as e:
            x = xs.start("tls", "TLS-HANDSHAKE")
            x["tls"] = {"version": "", "cipher": "", "verified": False, "client_cert_sent": False}
            xs.done(x, "error", "config", f"{e}; nothing was sent")
            out({"exchanges": xs.doc()})
            return
    sock, fail = _connect(args, xs)
    if sock is not None and ctx is not None:
        sock, fail = _tls_handshake(sock, ctx, args, xs, bool(args.client_cert))
    if sock is None:
        out({"exchanges": xs.doc()})
        return
    try:
        _associate_on(sock, args, contexts, async_ops, xs)
    finally:
        try:
            sock.close()
        except Exception:
            pass
    out({"exchanges": xs.doc()})


def _associate_on(sock, args, contexts, async_ops, xs):
    x = xs.start("dimse", "A-ASSOCIATE")
    info = {"result": "no_response", "released": False, "contexts": [
        {"id": 2 * i + 1, "abstract_syntax": a, "proposed_transfer_syntaxes": t, "result": "missing"}
        for i, (a, t, _) in enumerate(contexts)]}
    x["association"] = info
    sock.settimeout(args.timeout)
    try:
        sock.sendall(build_rq(args.called_aet, args.calling_aet, contexts, args.impl_uid or "",
                              args.impl_version or "", async_ops))
    except Exception as e:
        _, kind, msg = classify_error(e)
        xs.done(x, "error", kind, msg)
        return
    got = _read_answer(sock, x, xs)
    if got is None:
        return
    t, body = got
    if t == 3:  # A-ASSOCIATE-RJ
        if len(body) < 4:
            xs.done(x, "error", "protocol", "malformed PDU: A-ASSOCIATE-RJ too short")
            return
        info["result"] = "rejected"
        info["reject"] = {"result": body[1], "source": body[2], "reason": body[3]}
        for c in info["contexts"]:
            c["result"] = "rejected"
            c["reason"] = "association rejected"
        xs.done(x, "refused")
        return
    if t == 7:  # A-ABORT
        src, reason = (body[2], body[3]) if len(body) >= 4 else (-1, -1)
        info["result"] = "aborted"
        info["abort"] = {"source": src, "reason": reason}
        xs.done(x, "error", "protocol", f"the target aborted the association (A-ABORT source {src}, reason {reason})")
        return
    if t != 2:
        xs.done(x, "error", "protocol", f"unexpected {PDU_NAMES[t]} in answer to A-ASSOCIATE-RQ")
        _send_abort(sock)
        return
    try:
        ac = parse_ac(body)
    except Malformed as e:
        xs.done(x, "error", "protocol", f"malformed PDU: {e}")
        _send_abort(sock)
        return
    info["result"] = "accepted"
    if ac["max_pdu"] is not None:
        info["max_pdu"] = ac["max_pdu"]
    info["impl_class_uid"] = ac["impl_class_uid"]
    info["impl_version"] = ac["impl_version"]
    if async_ops is not None and ac["async_ops"] is not None:
        info["async_ops"] = {"invoked": ac["async_ops"][0], "performed": ac["async_ops"][1]}
    proposed_ids = {c["id"] for c in info["contexts"]}
    for c in info["contexts"]:
        if c["id"] in ac["contexts"]:
            result, ts = ac["contexts"][c["id"]]
            if result == 0:
                c["result"] = "accepted"
                c["accepted_transfer_syntax"] = ts
            else:
                c["result"] = "rejected"
                c["reason"] = f"{CONTEXT_RESULT.get(result, 'unknown')} ({result})"
        if c["abstract_syntax"] in ac["roles"]:
            scu, scp = ac["roles"][c["abstract_syntax"]]
            c["roles"] = {"scu": scu == 1, "scp": scp == 1}
    problems = []
    extra = sorted(set(ac["contexts"]) - proposed_ids)
    if extra:
        problems.append(f"A-ASSOCIATE-AC holds presentation context ID(s) {extra} that were not proposed")
    if ac["duplicate_ids"]:
        problems.append(f"A-ASSOCIATE-AC repeats presentation context ID(s) {sorted(set(ac['duplicate_ids']))}")
    if problems:
        xs.done(x, "error", "protocol", "; ".join(problems))
    else:
        xs.done(x)
    _release(sock, info, xs)


def _release(sock, info, xs):
    """A-RELEASE (DVT-SRS-096). A failed release is recorded, never hidden."""
    x = xs.start("dimse", "A-RELEASE")
    try:
        sock.sendall(RELEASE_RQ)
    except Exception as e:
        _, kind, msg = classify_error(e)
        xs.done(x, "error", kind, msg)
        return
    got = _read_answer(sock, x, xs)
    if got is None:
        _send_abort(sock)
        return
    t, body = got
    if t == 6:
        info["released"] = True
        xs.done(x)
        return
    if t == 7:
        src, reason = (body[2], body[3]) if len(body) >= 4 else (-1, -1)
        xs.done(x, "error", "protocol", f"the target aborted instead of releasing (A-ABORT source {src}, reason {reason})")
        return
    xs.done(x, "error", "protocol", f"unexpected {PDU_NAMES[t]} in answer to A-RELEASE-RQ")
    _send_abort(sock)


# ---------------------------------------------------------------- tls-probe (DVT-SRS-105, -106)

VERIFICATION = "1.2.840.10008.1.1"
IMPLICIT_LE = "1.2.840.10008.1.2"
OLD_TLS = {"1.0": ssl.TLSVersion.TLSv1, "1.1": ssl.TLSVersion.TLSv1_1}


def _old_version_context(args):
    """A narrow context that offers only TLS 1.0 or 1.1, for a refusal probe.
    Certificate and host-name checks stay on; no client certificate."""
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    ctx.hostname_checks_common_name = False
    try:
        ctx.set_ciphers("DEFAULT:@SECLEVEL=0")  # old versions need SHA-1 signatures
        ctx.minimum_version = OLD_TLS[args.tls_version]
        ctx.maximum_version = OLD_TLS[args.tls_version]
    except (ValueError, ssl.SSLError) as e:
        raise ConfigError(f"the TLS engine cannot offer TLS {args.tls_version}: {e}") from None
    _load_trust(ctx, args)
    if ctx.verify_mode != ssl.CERT_REQUIRED or not ctx.check_hostname:
        raise ConfigError("probe context does not verify the target")
    return ctx


def cmd_tls_probe(args):
    """Facts for a tls_reject step: what happened when LibreDVT broke the TLS
    policy on purpose. observation is one of: dicom_reply, non_dicom_reply,
    connection_closed, connection_reset, handshake_alert, handshake_error,
    handshake_completed, cert_verify_failed, timeout, connect_error,
    local_error. Go decides what each means for the mode."""
    xs = Exchanges(f"{args.host}:{args.port}")
    doc = {"mode": args.mode, "observation": "", "detail": ""}
    web = None
    if args.base_url:  # DICOMweb target: the probe is one bare HTTP request
        import urllib.parse

        u = urllib.parse.urlsplit(args.base_url)
        args.host = u.hostname or ""
        args.port = u.port or (443 if u.scheme == "https" else 80)
        web = (u.netloc, u.path.rstrip("/") + "/studies?limit=1")
        xs = Exchanges(args.base_url)

    def finish(obs, detail):
        doc["observation"], doc["detail"] = obs, detail
        doc["exchanges"] = xs.doc()
        out(doc)

    ctx = None
    if args.mode == "tls_version":
        try:
            ctx = _old_version_context(args)
        except ConfigError as e:
            finish("local_error", str(e))
            return
    elif args.mode == "no_client_cert":
        try:
            ctx = tls_client_context(args, client_cert=False)
        except ConfigError as e:
            finish("local_error", str(e))
            return
    sock, fail = _connect(args, xs, "http" if web else "dimse")
    if sock is None:
        finish(fail[0], fail[2])
        return
    try:
        if ctx is not None:
            sock, fail = _tls_handshake(sock, ctx, args, xs, False)
            if sock is None:
                finish(fail[0], fail[2])
                return
            if args.mode == "tls_version":
                # The target completed a handshake with the old version.
                finish("handshake_completed", f"handshake completed with {sock.version()}")
                return
        if web:
            obs, detail = _probe_http(sock, args, xs, web[0], web[1])
        else:
            obs, detail = _probe_association(sock, args, xs)
        finish(obs, detail)
    finally:
        try:
            sock.close()
        except Exception:
            pass


def _probe_association(sock, args, xs):
    """Send only an A-ASSOCIATE-RQ (Verification) and see whether the target
    answers it. An accepted association is aborted at once: no P-DATA, no
    release (DVT-SRS-106)."""
    x = xs.start("dimse", "A-ASSOCIATE")
    x["association"] = {"result": "no_response", "released": False}
    sock.settimeout(args.timeout)
    try:
        sock.sendall(build_rq(args.called_aet, args.calling_aet, [(VERIFICATION, [IMPLICIT_LE], None)],
                              args.impl_uid or "", args.impl_version or ""))
        t, body = read_pdu(sock)
    except Closed as e:
        if e.got == 0:
            xs.done(x, "refused")
            return "connection_closed", "the target closed the connection without answering"
        xs.done(x, "refused")
        return "non_dicom_reply", f"the target sent {e.got} byte(s) that are not a DICOM PDU, then closed"
    except Malformed as e:
        xs.done(x, "refused")
        return "non_dicom_reply", f"the target answered with bytes that are not a DICOM PDU: {e}"
    except Exception as e:
        obs, kind, msg = classify_error(e)
        if obs in ("handshake_alert", "connection_closed", "connection_reset"):
            xs.done(x, "refused")
            return obs, msg
        xs.done(x, "error", kind, msg)
        return obs, msg
    name = PDU_NAMES[t]
    if t == 2:
        x["association"]["result"] = "accepted"
        _send_abort(sock)
    elif t == 3:
        x["association"]["result"] = "rejected"
        if len(body) >= 4:
            x["association"]["reject"] = {"result": body[1], "source": body[2], "reason": body[3]}
    elif t == 7:
        x["association"]["result"] = "aborted"
    xs.done(x)
    return "dicom_reply", f"the target answered the A-ASSOCIATE-RQ with {name}"


# ---------------------------------------------------------------- network helpers

class _TLSCapture(logging.Handler):
    """Keeps the reason pynetdicom gives when a TLS connection fails."""

    def __init__(self):
        super().__init__(logging.ERROR)
        self.error = None

    def emit(self, record):
        # The exception itself (logged after the text) is preferred: it
        # carries the verification reason.
        if record.exc_info and isinstance(record.exc_info[1], OSError):
            if not isinstance(self.error, OSError):
                self.error = record.exc_info[1]
        elif self.error is None and record.getMessage().startswith("TCP Initialisation Error:"):
            self.error = record.getMessage()


_TLS = {"used": False, "facts": None, "error": None}


class _RecordingServerContext:
    """Wraps the built-in Storage SCP's server context: every incoming TLS
    connection is recorded (accepted or refused, with the client certificate
    subject), and a silent client cannot block the server (DVT-SRS-108)."""

    def __init__(self, ctx, timeout, log, lock):
        self.ctx, self.timeout, self.log, self.lock = ctx, timeout, log, lock

    def __bool__(self):
        return True

    def wrap_socket(self, sock, server_side=True, **_):
        peer = ""
        try:
            peer = "%s:%s" % sock.getpeername()[:2]
        except OSError:
            pass
        entry = {"peer": peer, "started": _rfc3339(_now())}
        sock.settimeout(self.timeout)
        try:
            s = self.ctx.wrap_socket(sock, server_side=True)
        except Exception as e:
            entry.update(outcome="refused", error=classify_error(e)[2])
            with self.lock:
                self.log.append(entry)
            try:
                sock.close()
            except OSError:
                pass
            raise OSError(entry["error"]) from None
        s.settimeout(None)
        info = {"version": s.version() or "", "cipher": (s.cipher() or ("",))[0]}
        cert = s.getpeercert()
        if cert:
            info["client_subject"] = _dn(cert.get("subject", ()))
            info["client_issuer"] = _dn(cert.get("issuer", ()))
            der = s.getpeercert(binary_form=True)
            info["client_sha256"] = hashlib.sha256(der).hexdigest() if der else ""
        entry.update(outcome="completed", tls=info)
        with self.lock:
            self.log.append(entry)
        return s


def scp_server_context(args):
    """TLS server context of the built-in Storage SCP (DVT-SRS-108): TLS 1.2+
    with LibreDVT's certificate; the target's client certificate is checked
    against the target's CA (required unless the target says optional)."""
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.minimum_version = TLS_MIN[args.tls_min]
    try:
        ctx.load_cert_chain(args.scp_tls_cert, args.scp_tls_key)
        if args.ca_file:
            ctx.load_verify_locations(cafile=args.ca_file)
        else:
            ctx.load_default_certs(ssl.Purpose.CLIENT_AUTH)
    except (OSError, ssl.SSLError, ValueError) as e:
        reason = getattr(e, "reason", None) or e.__class__.__name__
        raise ConfigError(f"Storage SCP certificate, key or CA not usable: {reason}") from None
    ctx.verify_mode = ssl.CERT_OPTIONAL if args.scp_client_cert == "optional" else ssl.CERT_REQUIRED
    if ctx.minimum_version < ssl.TLSVersion.TLSv1_2:
        raise ConfigError("Storage SCP context allows TLS below 1.2")
    return ctx


def _identify(ae, args):
    """Send LibreDVT's own Implementation Class UID and Version Name (from the
    Go side) instead of pynetdicom's (DVT-SRS-082)."""
    if args.impl_uid:
        ae.implementation_class_uid = args.impl_uid
    if args.impl_version:
        ae.implementation_version_name = args.impl_version


def _ae(args):
    from pynetdicom import AE

    timeout = args.timeout
    ae = AE(ae_title=args.calling_aet)
    ae.acse_timeout = timeout
    ae.dimse_timeout = timeout
    ae.network_timeout = timeout
    ae.connection_timeout = timeout
    _identify(ae, args)
    return ae


def _assoc_failure(assoc):
    tls_error = _TLS.get("error")
    if tls_error:
        return {"rejected": False, "error": f"TLS: {tls_error}"}
    if assoc.is_rejected:
        return {"rejected": True, "error": "association rejected"}
    if assoc.is_aborted:
        return {"rejected": False, "error": "association aborted"}
    return {"rejected": False, "error": "association failed (no answer or connection refused)"}


class _NoAssociation:
    """Stands in for an association that was never attempted (TLS
    configuration error: nothing was sent)."""
    is_established = False
    is_rejected = False
    is_aborted = False


def _describe(err):
    if isinstance(err, OSError):
        return classify_error(err)[2]
    return str(err).replace("TCP Initialisation Error: ", "", 1)


def _associate(ae, a, contexts=None):
    """Request an association; with --tls over the one TLS context factory
    (DVT-SRS-100..104). TLS facts and errors go into the command's output
    (DVT-SRS-107)."""
    if not getattr(a, "tls", False):
        return ae.associate(a.host, a.port, contexts=contexts, ae_title=a.called_aet)
    from pynetdicom import evt

    _TLS["used"] = True
    try:
        ctx = tls_client_context(a)
    except ConfigError as e:
        _TLS["error"] = f"{e}; nothing was sent"
        return _NoAssociation()
    cap = _TLSCapture()
    logger = logging.getLogger("pynetdicom")
    logger.addHandler(cap)

    def on_open(event):
        sock = getattr(getattr(event.assoc.dul, "socket", None), "socket", None)
        if isinstance(sock, ssl.SSLSocket):
            _TLS["facts"] = tls_facts(sock, bool(a.client_cert))

    try:
        assoc = ae.associate(a.host, a.port, contexts=contexts, ae_title=a.called_aet,
                             tls_args=(ctx, _server_name(a)), evt_handlers=[(evt.EVT_CONN_OPEN, on_open)])
    finally:
        logger.removeHandler(cap)
    if cap.error is not None and _TLS["facts"] is None:
        _TLS["error"] = _describe(cap.error)
    elif _TLS["facts"] is None and not assoc.is_established:
        _TLS["error"] = "no TLS connection was established"
    return assoc


def _status(ds):
    return int(ds.Status) if ds is not None and "Status" in ds else None


# ---------------------------------------------------------------- echo

def cmd_echo(args):
    from pynetdicom.sop_class import Verification

    ae = _ae(args)
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

    ae = _ae(args)
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
    ae = _ae(args)
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
             "series_instance_uid": p.get("series_instance_uid", ""),
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

    ae = _ae(args)
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
        meta = event.file_meta
        if args.impl_uid:  # the file is written by LibreDVT, not pynetdicom (DVT-SRS-082)
            meta.ImplementationClassUID = args.impl_uid
        if args.impl_version:
            meta.ImplementationVersionName = args.impl_version
        # Write the bytes as received (no re-encoding), so pixel comparison is exact.
        with open(path, "wb") as fh:
            fh.write(b"\x00" * 128 + b"DICM")
            write_file_meta_info(fh, meta, enforce_standard=True)
            fh.write(event.request.DataSet.getvalue())
        with lock:
            received.append({"sop_instance_uid": uid, "file": path,
                             "transfer_syntax_uid": str(event.context.transfer_syntax),
                             "calling_aet": event.assoc.requestor.ae_title})
        return 0x0000

    scp = AE(ae_title=args.dest_aet)
    scp.network_timeout = args.timeout
    scp.dimse_timeout = args.timeout
    _identify(scp, args)
    for cx in AllStoragePresentationContexts:
        scp.add_supported_context(cx.abstract_syntax, ALL_TRANSFER_SYNTAXES)
    # Only the target may store to the built-in SCP during this step.
    scp.require_calling_aet = [args.called_aet]
    # With a TLS target the SCP listens with TLS too, never in clear text
    # (DVT-SRS-108).
    scp_log, scp_ctx = [], None
    if args.tls or args.scp_tls_cert:
        try:
            if not (args.tls and args.scp_tls_cert and args.scp_tls_key):
                raise ConfigError("a TLS move needs the Storage SCP certificate and key; nothing was sent")
            scp_ctx = _RecordingServerContext(scp_server_context(args), args.timeout, scp_log, lock)
        except ConfigError as e:
            _TLS["used"] = True
            _TLS["error"] = str(e)
            out({"associated": False, "rejected": False, "error": f"TLS: {e}", "received": [],
                 "scp_connections": []})
            return
    server = scp.start_server(("0.0.0.0", args.scp_port), block=False, ssl_context=scp_ctx,
                              evt_handlers=[(evt.EVT_C_STORE, on_store)])
    try:
        ae = _ae(args)
        ae.add_requested_context(StudyRootQueryRetrieveInformationModelMove)
        assoc = _associate(ae, args)
        if not assoc.is_established:
            out({"associated": False, **_assoc_failure(assoc), "received": [], "scp_connections": list(scp_log)})
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
        conns = list(scp_log)
    doc = {"associated": True, "moves": moves, "received": rec}
    if scp_ctx is not None:
        doc["scp_connections"] = conns
    out(doc)


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
            # Facts only; Go decides whether a transfer syntax change matters.
            r.update(received=rpath, sent_pixel_sha256=sdig, received_pixel_sha256=rdig,
                     sent_transfer_syntax=sts, received_transfer_syntax=rts,
                     match=(sdig == rdig))
            if not r["match"]:
                r["error"] = "pixel data differs"
        results.append(r)
    out({"files": results})


# ---------------------------------------------------------------- DICOMweb (DVT-SRS-111..136, -151, -154, -155)
#
# A plain HTTP/1.1 client on the standard library (http.client over the one
# TLS context factory). Every request is one connection, opened after the
# previous one closed, so a target never sees parallel traffic
# (DVT-SRS-155). Redirects are never followed (DVT-SRS-151). The worker
# reports facts; Go decides.

REDACTED = "[REDACTED]"
TOKEN_CHARS = set("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~+/")
MIN_TOKEN = 16
DICOM_JSON = "application/dicom+json"
ACCEPT_MULTIPART = 'multipart/related; type="application/dicom"'
UNIQUE_KEY = {"studies": "0020000D", "series": "0020000E", "instances": "00080018"}
TAG_STUDY, TAG_SERIES, TAG_SOP, TAG_CLASS = "0020000D", "0020000E", "00080018", "00080016"


class WebConfigError(Exception):
    """A DICOMweb setting or keyword is unusable: nothing was sent."""


class LimitError(Exception):
    """A configured size limit was reached (DVT-SRS-154)."""


def _read_token(path):
    """The bearer token from its file (DVT-SRS-111, -153). Errors never quote it."""
    try:
        with open(path, "rb") as fh:
            raw = fh.read(1 << 20)
    except OSError:
        raise WebConfigError("auth token file not readable") from None
    try:
        tok = raw.decode("ascii").rstrip("\r\n")
    except UnicodeDecodeError:
        raise WebConfigError("auth token file is not ASCII") from None
    body = tok.rstrip("=")
    if ("\r" in tok or "\n" in tok or len(tok) < MIN_TOKEN or not body
            or any(c not in TOKEN_CHARS for c in body)):
        raise WebConfigError(f"auth token must be one line of at least {MIN_TOKEN} RFC 6750 token characters")
    return tok


def _is_loopback(host):
    if host.lower() == "localhost":
        return True
    import ipaddress
    try:
        return ipaddress.ip_address(host.strip("[]")).is_loopback
    except ValueError:
        return False


def media_type(value):
    """(lower-case media type, {param: value}) of a Content-Type value."""
    if value is None:
        return "", {}
    parts, cur, quoted = [], "", False
    for ch in value:
        if ch == '"':
            quoted = not quoted
            cur += ch
        elif ch == ";" and not quoted:
            parts.append(cur)
            cur = ""
        else:
            cur += ch
    parts.append(cur)
    params = {}
    for p in parts[1:]:
        if "=" not in p:
            continue
        k, v = p.split("=", 1)
        v = v.strip()
        if len(v) >= 2 and v[0] == v[-1] == '"':
            v = v[1:-1]
        params[k.strip().lower()] = v
    return parts[0].strip().lower(), params


class Web:
    """One DICOMweb target: base URL, TLS, token, limits; records exchanges."""

    def __init__(self, args):
        import urllib.parse

        u = urllib.parse.urlsplit(args.base_url)
        if u.scheme not in ("http", "https") or not u.hostname or u.username or u.query or u.fragment:
            raise WebConfigError("base URL must be http(s)://host[:port][/path] without user-info, query or fragment")
        self.args = args
        self.https = u.scheme == "https"
        self.host = u.hostname
        self.port = u.port or (443 if self.https else 80)
        self.netloc = u.netloc
        self.origin = (u.scheme, self.host.lower(), self.port)
        self.base = f"{u.scheme}://{u.netloc}"
        self.path = u.path.rstrip("/")
        self.timeout = args.timeout
        self.xs = Exchanges(args.base_url)
        self.token = None
        if args.token_file:
            if not self.https and not _is_loopback(self.host):
                # DVT-SRS-112: never a token in clear text off the host.
                raise WebConfigError("a bearer token over http is only sent to a loopback host")
            self.token = _read_token(args.token_file)
        self.ctx = None
        if self.https:
            args.host = self.host  # name checked against the certificate unless --server-name
            try:
                self.ctx = tls_client_context(args)
            except ConfigError as e:
                raise WebConfigError(str(e)) from None

    def url(self, target):
        return self.base + target

    def same_origin(self, url):
        import urllib.parse

        u = urllib.parse.urlsplit(url)
        try:
            port = u.port or (443 if u.scheme == "https" else 80)
        except ValueError:
            return False
        return (u.scheme, (u.hostname or "").lower(), port) == self.origin

    def request(self, method, target, accept, body=None, content_type=None, max_bytes=None):
        """One request on its own connection. Returns (status, content type,
        body, exchange) or None when it failed (the failure is in the
        exchange)."""
        import http.client

        a = self.args
        max_bytes = max_bytes or a.max_response_bytes
        url = self.url(target)
        x = self.xs.start("http", method)
        x["endpoint"] = url
        headers = [("Host", self.netloc), ("Accept", accept), ("User-Agent", f"LibreDVT-worker/{WORKER_VERSION}"),
                   ("Connection", "close")]
        if body is not None:
            headers += [("Content-Type", content_type), ("Content-Length", str(len(body)))]
        if self.token and self.same_origin(url):  # own origin only (DVT-SRS-111)
            headers.append(("Authorization", "Bearer " + self.token))
        rec = {}
        for k, v in headers:
            rec.setdefault(k, []).append(REDACTED if k.lower() == "authorization" else v)
        info = {"method": method, "url": url, "request_headers": rec, "status": 0}
        x["http"] = info
        try:
            sock = socket.create_connection((self.host, self.port), timeout=self.timeout)
        except Exception as e:
            _, kind, msg = classify_error(e)
            self.xs.done(x, "error", kind, msg)
            return None
        try:
            if self.ctx is not None:
                try:
                    ts = self.ctx.wrap_socket(sock, server_hostname=a.server_name or self.host,
                                              do_handshake_on_connect=False)
                    ts.settimeout(self.timeout)
                    ts.do_handshake()
                except Exception as e:
                    obs, kind, msg = classify_error(e)
                    t = {"version": "", "cipher": "", "verified": False, "client_cert_sent": False}
                    if obs == "cert_verify_failed":
                        t["verify_error"] = msg
                    x["tls"] = t
                    self.xs.done(x, "error", kind, msg)
                    return None
                x["tls"] = tls_facts(ts, bool(getattr(a, "client_cert", None)))
                sock = ts
            conn = http.client.HTTPConnection(self.host, self.port, timeout=self.timeout)
            conn.sock = sock
            conn.putrequest(method, target, skip_host=True, skip_accept_encoding=True)
            for k, v in headers:
                conn.putheader(k, v)
            conn.endheaders()
            if body is not None:
                view = memoryview(body)
                for i in range(0, len(body), 1 << 16):
                    conn.send(view[i:i + (1 << 16)])
            resp = conn.getresponse()
            info["status"] = resp.status
            rh = {}
            for k, v in resp.getheaders():
                rh.setdefault(k, []).append(REDACTED if k.lower() in ("authorization", "set-cookie") else v)
            info["response_headers"] = rh
            data = bytearray()
            while True:
                chunk = resp.read(1 << 20)
                if not chunk:
                    break
                data += chunk
                if len(data) > max_bytes:
                    raise LimitError(f"response body larger than the limit ({max_bytes} bytes)")
            data = bytes(data)
        except LimitError as e:
            self.xs.done(x, "error", "limit", str(e))
            return None
        except http.client.IncompleteRead as e:
            self.xs.done(x, "error", "protocol", f"truncated response body ({len(e.partial)} bytes, more expected)")
            return None
        except (http.client.HTTPException, ValueError) as e:
            self.xs.done(x, "error", "protocol", f"malformed HTTP response: {e.__class__.__name__}: {e}")
            return None
        except Exception as e:
            _, kind, msg = classify_error(e)
            self.xs.done(x, "error", kind, msg)
            return None
        finally:
            try:
                sock.close()
            except Exception:
                pass
        ctype = resp.getheader("Content-Type") or ""
        b = {"content_type": ctype, "size": len(data), "sha256": hashlib.sha256(data).hexdigest()}
        mt, _ = media_type(ctype)
        if data and not mt.startswith("multipart/") and a.max_excerpt > 0:
            b["excerpt"] = data[:a.max_excerpt].decode("utf-8", "replace")
            if len(data) > a.max_excerpt:
                b["excerpt_truncated"] = True
        info["body"] = b
        self.xs.done(x)
        return resp.status, ctype, data, x

    def fail(self, x, kind, message):
        """Turn a completed exchange into a failed one (protocol or limit
        error found while reading its body)."""
        x["outcome"] = "error"
        x["error"] = {"kind": kind, "message": message}


def parse_multipart(ctype, body, max_parts, max_part_bytes):
    """Strict multipart/related parser (RFC 2046 §5.1, RFC 2387). Returns a
    list of (headers, data). Raises ValueError (protocol) or LimitError."""
    mt, params = media_type(ctype)
    if mt != "multipart/related":
        raise ValueError(f"Content-Type is {mt or 'missing'}, not multipart/related")
    boundary = params.get("boundary", "")
    if not boundary or len(boundary) > 70:
        raise ValueError("no usable boundary parameter in the Content-Type")
    delim = b"--" + boundary.encode("latin-1")
    if body.startswith(delim):
        pos = 0
    else:
        i = body.find(b"\r\n" + delim)
        if i < 0:
            raise ValueError("the body does not contain the boundary from the Content-Type")
        pos = i + 2
    parts = []
    while True:
        pos += len(delim)
        if body[pos:pos + 2] == b"--":
            return parts  # closing delimiter
        j = pos
        while body[j:j + 1] in (b" ", b"\t"):
            j += 1
        if body[j:j + 2] != b"\r\n":
            raise ValueError("a boundary delimiter is not followed by CRLF or '--' (truncated or malformed body)")
        hstart = j + 2
        if body[hstart:hstart + 2] == b"\r\n":
            hraw, dstart = b"", hstart + 2
        else:
            hend = body.find(b"\r\n\r\n", hstart)
            if hend < 0:
                raise ValueError(f"part {len(parts) + 1}: headers not terminated")
            hraw, dstart = body[hstart:hend], hend + 4
        headers = {}
        for line in hraw.split(b"\r\n") if hraw else []:
            if b":" not in line:
                raise ValueError(f"part {len(parts) + 1}: malformed header line")
            k, v = line.split(b":", 1)
            headers[k.decode("latin-1").strip().lower()] = v.decode("latin-1").strip()
        # Next delimiter: CRLF--boundary followed by '--' or LWSP*CRLF. The
        # boundary text elsewhere in the data does not end the part.
        k = dstart
        while True:
            k = body.find(b"\r\n" + delim, k)
            if k < 0:
                raise ValueError(f"part {len(parts) + 1}: no closing boundary delimiter (truncated body)")
            after = k + 2 + len(delim)
            rest = body[after:after + 2]
            m = after
            while body[m:m + 1] in (b" ", b"\t"):
                m += 1
            if rest == b"--" or body[m:m + 2] == b"\r\n" or after == len(body):
                break
            k += 1
        data = body[dstart:k]
        if len(data) > max_part_bytes:
            raise LimitError(f"part {len(parts) + 1} is larger than the limit ({max_part_bytes} bytes)")
        parts.append((headers, data))
        if len(parts) > max_parts:
            raise LimitError(f"more than {max_parts} parts (limit)")
        pos = k + 2


def _keyword_tag(key):
    """8-hex tag for a keyword or tag (pydicom's dictionary), or WebConfigError."""
    from pydicom.datadict import tag_for_keyword

    if len(key) == 8 and all(c in "0123456789abcdefABCDEF" for c in key):
        return key.upper()
    t = tag_for_keyword(key)
    if t is None:
        raise WebConfigError(f"{key!r} is not a keyword in the DICOM dictionary; nothing was sent")
    return f"{t:08X}"


def _vr(tag):
    from pydicom.datadict import dictionary_VR

    try:
        return dictionary_VR(int(tag, 16))
    except KeyError:
        return "UN"


def _json_value_strings(el):
    """String forms of a DICOM JSON element's values (PS3.18 F.2). Sequences
    and bulk data give no strings."""
    vals = el.get("Value")
    if not isinstance(vals, list):
        return []
    outv = []
    for v in vals:
        if isinstance(v, str):
            outv.append(v)
        elif isinstance(v, bool):
            continue
        elif isinstance(v, (int, float)):
            outv.append(json.dumps(v))
        elif isinstance(v, dict) and "Alphabetic" in v:
            outv.append(str(v["Alphabetic"]))
    return outv


def check_dicom_json_object(o):
    """'' when o looks like a DICOM JSON object (PS3.18 F.2), else why not."""
    if not isinstance(o, dict):
        return f"an item is a JSON {type(o).__name__}, not an object"
    for k, v in o.items():
        if len(k) != 8 or any(c not in "0123456789ABCDEF" for c in k):
            return f"key {k[:20]!r} is not an 8-hex-digit tag"
        if not isinstance(v, dict) or not isinstance(v.get("vr"), str) or len(v["vr"]) != 2:
            return f"attribute {k} has no two-letter vr"
    return ""


def _load_json(data, limit):
    if len(data) > limit:
        raise LimitError(f"DICOM JSON document larger than the limit ({limit} bytes)")
    return json.loads(data.decode("utf-8"))


def web_doc(web, extra):
    doc = {"exchanges": web.xs.doc()}
    doc.update(extra)
    return doc


def _config_failure(e, extra):
    out(dict(extra, exchanges=[], config_error=str(e)))


# ---- qido

def _qido_path(level, study, series):
    if level == "studies":
        return "/studies"
    if level == "series":
        return f"/studies/{study}/series" if study else "/series"
    if study and series:
        return f"/studies/{study}/series/{series}/instances"
    return f"/studies/{study}/instances" if study else "/instances"


def _exact(value, vr, fuzzy):
    """Single-value matching without wildcards (DVT-SRS-116)."""
    if value == "" or any(c in value for c in "*?\\"):
        return False
    if vr == "UI" and "," in value:
        return False  # UID list matching
    if vr in ("DA", "TM", "DT") and "-" in value:
        return False  # range matching
    if vr == "PN" and fuzzy:
        return False
    return True


def cmd_qido(args):
    import urllib.parse

    base = {"keywords": {}, "query": [], "pages": [], "page_limit_reached": False}
    try:
        web = Web(args)
        query = []
        for q in args.query or []:
            if "=" not in q:
                raise WebConfigError(f"bad query {q!r}")
            k, v = q.split("=", 1)
            tag = _keyword_tag(k)
            vr = _vr(tag)
            query.append({"key": k, "tag": tag, "vr": vr, "value": v, "exact": _exact(v, vr, args.fuzzymatching)})
        keywords = {}
        for k in (args.keyword or []):
            keywords[k] = _keyword_tag(k)
        for k in (args.includefield or []):
            if k != "all":
                keywords.setdefault(k, _keyword_tag(k))
    except WebConfigError as e:
        _config_failure(e, base)
        return
    base["keywords"], base["query"] = keywords, query
    want = set(UNIQUE_KEY.values()) | {q["tag"] for q in query} | set(keywords.values())
    path = web.path + _qido_path(args.level, args.study, args.series)
    # from_store: one search per expected UID on the level's unique key, so
    # a large archive's default page size cannot hide an object.
    searches = list(args.uid_filter) if args.uid_filter else [None]
    pages = []
    for uid in searches:
        params = [(q["key"], q["value"]) for q in query]
        if uid is not None:
            params.append((UNIQUE_KEY[args.level], uid))
        for k in args.includefield or []:
            params.append(("includefield", k))
        if args.fuzzymatching:
            params.append(("fuzzymatching", "true"))
        offset = args.offset if args.limit else None
        if args.limit and offset is None:
            offset = 0
        while True:
            p = list(params)
            if args.limit:
                p += [("limit", str(args.limit)), ("offset", str(offset))]
            target = path + ("?" + urllib.parse.urlencode(p, quote_via=urllib.parse.quote) if p else "")
            page = {"search": uid or "", "offset": offset, "status": 0, "content_type": "", "json": "none",
                    "detail": "", "objects": []}
            pages.append(page)
            got = web.request("GET", target, DICOM_JSON)
            if got is None:
                page["exchange"] = web.xs.items[-1]["seq"]
                break
            status, ctype, data, x = got
            page.update(exchange=x["seq"], status=status, content_type=ctype)
            if status == 204 or (status == 200 and not data):
                page["json"] = "empty"
            elif data:
                try:
                    doc = _load_json(data, args.max_json_bytes)
                except LimitError as e:
                    web.fail(x, "limit", str(e))
                    break
                except (ValueError, UnicodeDecodeError) as e:
                    page["json"], page["detail"] = "invalid", f"not JSON: {e.__class__.__name__}"
                    doc = None
                if doc is not None:
                    if not isinstance(doc, list):
                        page["json"], page["detail"] = "not_array", f"a JSON {type(doc).__name__}, not an array"
                    else:
                        bad = next((w for w in (check_dicom_json_object(o) for o in doc) if w), "")
                        if bad:
                            page["json"], page["detail"] = "not_dicom_json", bad
                        else:
                            page["json"] = "array"
                            for o in doc:
                                page["objects"].append({
                                    "present": sorted(o.keys()),
                                    "values": {t: _json_value_strings(o[t]) for t in sorted(want) if t in o}})
            n = len(page["objects"])
            if not args.limit or status != 200 or page["json"] != "array" or n != args.limit:
                break
            if len([pg for pg in pages if pg["search"] == (uid or "")]) >= args.max_pages:
                base["page_limit_reached"] = True
                break
            offset += args.limit
    base["pages"] = pages
    out(web_doc(web, base))


# ---- wado

def _canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def _file_attrs(path, tags):
    """sop uid and canonical DICOM JSON values of the named tags in a file."""
    ds, err = _read(path)
    if ds is None:
        return {"file": path, "error": err}
    attrs = {}
    for t in tags:
        tag = int(t, 16)
        if tag not in ds:
            attrs[t] = "(absent)"
            continue
        j = ds[tag].to_json_dict(None, 0)
        attrs[t] = _canonical(j["Value"]) if "Value" in j else "(empty)"
    return {"file": path, "sop_instance_uid": str(ds.get("SOPInstanceUID", "")), "attrs": attrs}


def _dicom_part(data, path):
    """Facts of one received part; the bytes are written to path unchanged."""
    import io

    import pydicom

    try:
        ds = pydicom.dcmread(io.BytesIO(data), stop_before_pixels=True)
        ts = str(ds.file_meta.TransferSyntaxUID)
    except Exception as e:
        return {"error": f"not a DICOM Part 10 object: {e.__class__.__name__}"}
    with open(path, "wb") as fh:
        fh.write(data)
    return {"transfer_syntax": ts, "sop_instance_uid": str(ds.get("SOPInstanceUID", "")),
            "sop_class_uid": str(ds.get("SOPClassUID", "")), "study_instance_uid": str(ds.get("StudyInstanceUID", "")),
            "series_instance_uid": str(ds.get("SeriesInstanceUID", "")), "file": path}


def cmd_wado(args):
    base = {"parts": [], "metadata": [], "originals": [], "keywords": {}}
    try:
        web = Web(args)
        keywords = {k: _keyword_tag(k) for k in (args.attr or [])}
    except WebConfigError as e:
        _config_failure(e, base)
        return
    base["keywords"] = keywords
    if args.metadata:
        base["originals"] = [_file_attrs(f, sorted(set(keywords.values()))) for f in args.expect_file or []]
    os.makedirs(args.out, exist_ok=True)
    for res in args.resource:
        if args.metadata:
            got = web.request("GET", web.path + res + "/metadata", DICOM_JSON, max_bytes=args.max_json_bytes)
            if got is None:
                continue
            status, ctype, data, x = got
            if status != 200:
                continue
            mt, _ = media_type(ctype)
            if mt != DICOM_JSON:
                web.fail(x, "protocol", f"metadata Content-Type is {mt or 'missing'}, not {DICOM_JSON}")
                continue
            try:
                doc = _load_json(data, args.max_json_bytes)
            except LimitError as e:
                web.fail(x, "limit", str(e))
                continue
            except (ValueError, UnicodeDecodeError) as e:
                web.fail(x, "protocol", f"metadata is not JSON: {e.__class__.__name__}")
                continue
            if not isinstance(doc, list):
                web.fail(x, "protocol", "metadata is not a JSON array")
                continue
            bad = next((w for w in (check_dicom_json_object(o) for o in doc) if w), "")
            if bad:
                web.fail(x, "protocol", f"metadata is not DICOM JSON: {bad}")
                continue
            for o in doc:
                rec = {"exchange": x["seq"]}
                for name, tag in (("study_instance_uid", TAG_STUDY), ("series_instance_uid", TAG_SERIES),
                                  ("sop_instance_uid", TAG_SOP)):
                    v = _json_value_strings(o.get(tag, {}))
                    rec[name] = v[0] if v else ""
                rec["attrs"] = {}
                for tag in sorted(set(keywords.values())):
                    if tag not in o:
                        rec["attrs"][tag] = "(absent)"
                    elif "Value" in o[tag]:
                        rec["attrs"][tag] = _canonical(o[tag]["Value"])
                    else:
                        rec["attrs"][tag] = "(empty)"
                base["metadata"].append(rec)
            continue
        accept = ACCEPT_MULTIPART
        if args.transfer_syntax:
            accept += f"; transfer-syntax={args.transfer_syntax}"
        got = web.request("GET", web.path + res, accept)
        if got is None:
            continue
        status, ctype, data, x = got
        if status != 200:
            continue
        try:
            parts = parse_multipart(ctype, data, args.max_parts, args.max_part_bytes)
        except LimitError as e:
            web.fail(x, "limit", str(e))
            continue
        except ValueError as e:
            web.fail(x, "protocol", f"multipart: {e}")
            continue
        infos = []
        for i, (h, pdata) in enumerate(parts, 1):
            pct = h.get("content-type", "")
            pmt, pparams = media_type(pct)
            rec = {"exchange": x["seq"], "index": i, "content_type": pct, "media_type": pmt,
                   "ts_param": pparams.get("transfer-syntax", ""), "size": len(pdata),
                   "sha256": hashlib.sha256(pdata).hexdigest()}
            if pmt != "application/dicom":
                rec["error"] = f"part Content-Type is {pmt or 'missing'}, not application/dicom"
            else:
                rec.update(_dicom_part(pdata, os.path.join(args.out, f"x{x['seq']:03d}-p{i:05d}.dcm")))
            base["parts"].append(rec)
            infos.append({"index": i, "content_type": pct, "transfer_syntax": rec.get("transfer_syntax", ""),
                          "sop_instance_uid": rec.get("sop_instance_uid", ""), "size": len(pdata),
                          "sha256": rec["sha256"]})
        x["http"]["parts"] = infos
    out(web_doc(web, base))


# ---- stow

def _reason(item, tag):
    v = item.get(tag, {}).get("Value") if isinstance(item.get(tag), dict) else None
    if isinstance(v, list) and v and isinstance(v[0], int) and not isinstance(v[0], bool):
        return f"0x{v[0]:04X}"
    if isinstance(v, list) and v:
        return str(v[0])
    return ""


def _first(item, tag):
    v = _json_value_strings(item.get(tag, {})) if isinstance(item.get(tag), dict) else []
    return v[0] if v else ""


def _seq_items(o, tag):
    el = o.get(tag)
    if not isinstance(el, dict):
        return []
    v = el.get("Value")
    return [i for i in v if isinstance(i, dict)] if isinstance(v, list) else []


def parse_stow_response(ctype, data, limit):
    """Facts of a STOW-RS response body (DVT-SRS-129)."""
    r = {"json": "", "detail": "", "referenced": [], "failed": [], "retrieve_url": "", "warning_reason": ""}
    mt, _ = media_type(ctype)
    if not data:
        r["json"] = "empty"
        return r
    if mt != DICOM_JSON:
        r["json"], r["detail"] = "other_type", f"Content-Type {mt or 'missing'}"
        return r
    doc = _load_json(data, limit) if len(data) <= limit else None
    if doc is None:
        raise LimitError(f"DICOM JSON document larger than the limit ({limit} bytes)")
    if not isinstance(doc, dict):
        r["json"], r["detail"] = "not_object", f"a JSON {type(doc).__name__}, not an object"
        return r
    bad = check_dicom_json_object(doc)
    if bad:
        r["json"], r["detail"] = "not_dicom_json", bad
        return r
    r["json"] = "object"
    r["retrieve_url"] = _first(doc, "00081190")
    r["warning_reason"] = _reason(doc, "00081196")
    for it in _seq_items(doc, "00081199"):
        r["referenced"].append({"sop_class_uid": _first(it, "00081150"), "sop_instance_uid": _first(it, "00081155"),
                                "retrieve_url": _first(it, "00081190"), "warning_reason": _reason(it, "00081196")})
    for it in _seq_items(doc, "00081198"):
        r["failed"].append({"sop_class_uid": _first(it, "00081150"), "sop_instance_uid": _first(it, "00081155"),
                            "failure_reason": _reason(it, "00081197")})
    return r


def cmd_stow(args):
    import uuid

    files = []
    for f in args.files:
        p = probe_one(f)
        rec = {"file": f}
        if not p.get("dicom") or not p.get("transfer_syntax_uid") or not p.get("sop_instance_uid"):
            rec["error"] = p.get("error") or "not a DICOM Part 10 file with file meta and SOP Instance UID"
        else:
            try:
                with open(f, "rb") as fh:
                    data = fh.read()
            except OSError as e:
                rec["error"] = f"cannot read: {e.__class__.__name__}"
                data = b""
            rec.update(sop_instance_uid=p["sop_instance_uid"], sop_class_uid=p["sop_class_uid"],
                       study_instance_uid=p["study_instance_uid"], series_instance_uid=p.get("series_instance_uid", ""),
                       transfer_syntax=p["transfer_syntax_uid"], size=len(data),
                       sha256=hashlib.sha256(data).hexdigest())
        files.append(rec)
    base = {"files": files, "requests": []}
    if any("error" in f for f in files):  # DVT-SRS-133: nothing is sent
        out(dict(base, exchanges=[]))
        return
    try:
        web = Web(args)
    except WebConfigError as e:
        _config_failure(e, base)
        return
    target = web.path + (f"/studies/{args.study}" if args.study else "/studies")
    for start in range(0, len(files), args.batch_size):
        batch = list(range(start, min(start + args.batch_size, len(files))))
        boundary = "libredvt-" + uuid.uuid4().hex
        body = bytearray()
        for i in batch:
            with open(files[i]["file"], "rb") as fh:
                body += b"--" + boundary.encode() + b"\r\nContent-Type: application/dicom\r\n\r\n" + fh.read() + b"\r\n"
        body += b"--" + boundary.encode() + b"--\r\n"
        ctype = f'multipart/related; type="application/dicom"; boundary={boundary}'
        req = {"files": batch}
        base["requests"].append(req)
        got = web.request("POST", target, DICOM_JSON, body=bytes(body), content_type=ctype,
                          max_bytes=args.max_json_bytes)
        if got is None:
            req["exchange"] = web.xs.items[-1]["seq"]
            continue
        status, rctype, data, x = got
        req.update(exchange=x["seq"], status=status)
        try:
            req.update(parse_stow_response(rctype, data, args.max_json_bytes))
        except LimitError as e:
            web.fail(x, "limit", str(e))
        except (ValueError, UnicodeDecodeError) as e:
            req.update(json="invalid", detail=f"not JSON: {e.__class__.__name__}", referenced=[], failed=[],
                       retrieve_url="", warning_reason="")
    out(web_doc(web, base))


# ---- tls-probe over HTTP (DVT-SRS-105, -106)

def _probe_http(sock, args, xs, web_host, target):
    """Send one bare GET (no Authorization) and see whether an HTTP response
    comes back."""
    x = xs.start("http", "GET")
    x["endpoint"] = args.base_url.rstrip("/") + "/studies?limit=1"
    x["http"] = {"method": "GET", "url": x["endpoint"], "status": 0}
    sock.settimeout(args.timeout)
    try:
        sock.sendall(f"GET {target} HTTP/1.1\r\nHost: {web_host}\r\nAccept: {DICOM_JSON}\r\n"
                     f"Connection: close\r\n\r\n".encode("latin-1"))
        buf = b""
        while b"\r\n" not in buf and len(buf) < 65536:
            chunk = sock.recv(4096)
            if not chunk:
                break
            buf += chunk
    except Exception as e:
        obs, kind, msg = classify_error(e)
        if obs in ("handshake_alert", "connection_closed", "connection_reset"):
            xs.done(x, "refused")
            return obs, msg
        xs.done(x, "error", kind, msg)
        return obs, msg
    if not buf:
        xs.done(x, "refused")
        return "connection_closed", "the target closed the connection without answering"
    line = buf.split(b"\r\n", 1)[0]
    parts = line.split(b" ")
    if len(parts) >= 2 and parts[0] in (b"HTTP/1.0", b"HTTP/1.1") and parts[1].isdigit():
        x["http"]["status"] = int(parts[1])
        xs.done(x)
        return "http_reply", f"the target answered with HTTP status {int(parts[1])}"
    xs.done(x, "refused")
    return "non_http_reply", f"the target sent {len(buf)} byte(s) that are not an HTTP response"


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

    def target(sp, client_cert=True, required=True):
        sp.add_argument("--host", required=required)
        sp.add_argument("--port", type=int, required=required)
        sp.add_argument("--called-aet", required=required)
        sp.add_argument("--calling-aet", required=required)
        sp.add_argument("--timeout", type=float, default=30)
        sp.add_argument("--impl-uid")
        sp.add_argument("--impl-version")
        # TLS: paths only; there is no option that weakens verification (DVT-SRS-103).
        sp.add_argument("--tls", action="store_true")
        sp.add_argument("--tls-min", choices=sorted(TLS_MIN), default="1.2")
        sp.add_argument("--ca-file")
        sp.add_argument("--server-name")
        if client_cert:
            sp.add_argument("--client-cert")
            sp.add_argument("--client-key")

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
    m.add_argument("--scp-tls-cert")
    m.add_argument("--scp-tls-key")
    m.add_argument("--scp-client-cert", choices=["required", "optional"], default="required")
    c = sub.add_parser("compare")
    c.add_argument("--sent", nargs="+", required=True)
    c.add_argument("--received", required=True)
    a = sub.add_parser("associate")
    target(a)
    a.add_argument("--context", action="append", required=True)
    a.add_argument("--async-ops")
    tp = sub.add_parser("tls-probe")
    target(tp, client_cert=False, required=False)
    tp.add_argument("--base-url")  # DICOMweb target: probe with one bare HTTP request
    tp.add_argument("--mode", choices=["plaintext", "tls_version", "no_client_cert"], required=True)
    tp.add_argument("--tls-version", choices=sorted(OLD_TLS))

    def web(sp):
        # A DICOMweb target: base URL, TLS by its scheme, a token by file path
        # only (DVT-SRS-153), and the plan's limits (DVT-SRS-154).
        sp.add_argument("--base-url", required=True)
        sp.add_argument("--timeout", type=float, default=30)
        sp.add_argument("--token-file")
        sp.add_argument("--tls-min", choices=sorted(TLS_MIN), default="1.2")
        sp.add_argument("--ca-file")
        sp.add_argument("--server-name")
        sp.add_argument("--client-cert")
        sp.add_argument("--client-key")
        for name, default in (("max-response-bytes", 512 << 20), ("max-parts", 1000), ("max-part-bytes", 256 << 20),
                              ("max-json-bytes", 32 << 20), ("max-pages", 100), ("max-excerpt", 64 << 10)):
            sp.add_argument("--" + name, type=int, default=default)

    q = sub.add_parser("qido")
    web(q)
    q.add_argument("--level", choices=sorted(UNIQUE_KEY), required=True)
    q.add_argument("--study")
    q.add_argument("--series")
    q.add_argument("--query", action="append")
    q.add_argument("--includefield", action="append")
    q.add_argument("--fuzzymatching", action="store_true")
    q.add_argument("--limit", type=int)
    q.add_argument("--offset", type=int)
    q.add_argument("--keyword", action="append")
    q.add_argument("--uid-filter", action="append")
    w = sub.add_parser("wado")
    web(w)
    w.add_argument("--resource", action="append", required=True)
    w.add_argument("--transfer-syntax")
    w.add_argument("--out", required=True)
    w.add_argument("--metadata", action="store_true")
    w.add_argument("--attr", action="append")
    w.add_argument("--expect-file", action="append")
    st = sub.add_parser("stow")
    web(st)
    st.add_argument("--study")
    st.add_argument("--batch-size", type=int, default=10)
    st.add_argument("files", nargs="+")

    args = p.parse_args(argv)
    if args.cmd == "tls-probe":
        if (args.mode == "tls_version") != (args.tls_version is not None):
            p.error("--tls-version is required with --mode tls_version and not allowed otherwise")
        if not args.base_url and not (args.host and args.port and args.called_aet and args.calling_aet):
            p.error("tls-probe needs --base-url or --host, --port, --called-aet and --calling-aet")
    if args.cmd == "qido" and args.limit is not None and not 1 <= args.limit <= 1000:
        p.error("--limit must be 1-1000")
    if args.cmd == "stow" and not 1 <= args.batch_size <= 100:
        p.error("--batch-size must be 1-100")
    {"versions": cmd_versions, "probe": cmd_probe, "iod": cmd_iod, "echo": cmd_echo,
     "reject": cmd_reject, "store": cmd_store, "find": cmd_find, "move": cmd_move,
     "compare": cmd_compare, "associate": cmd_associate, "tls-probe": cmd_tls_probe,
     "qido": cmd_qido, "wado": cmd_wado, "stow": cmd_stow}[args.cmd](args)


if __name__ == "__main__":
    main()
