"""pynetdicom probes for the LibreDCM conformance statement: things the DCMTK
command-line tools cannot propose. Runs in the libre/dvt:engines image
(pynetdicom 3.0.4) on the evidence network. Prints one result block per probe.
"""
import socket
import ssl
import sys

from pydicom.dataset import Dataset
from pynetdicom import AE, build_role, debug_logger  # noqa: F401
from pynetdicom.pdu_primitives import (AsynchronousOperationsWindowNegotiation,
                                       SOPClassExtendedNegotiation,
                                       UserIdentityNegotiation)
from pynetdicom.sop_class import (CTImageStorage, StudyRootQueryRetrieveInformationModelFind,
                                  StudyRootQueryRetrieveInformationModelGet, Verification)

GW, PORT = "dcs-gw", 11112
S1 = "1.3.6.1.4.1.67686.9.1.900.1"
ok = True


def head(title):
    print()
    print("== " + title)


# 1. Asynchronous Operations Window: proposed 5/5, LibreDCM does not reply,
#    so the default (1 outstanding operation each way) applies.
head("Asynchronous Operations Window proposed (5 invoked, 5 performed)")
ae = AE(ae_title="DVTPROBE")
ae.add_requested_context(Verification)
assoc = ae.associate(GW, PORT, ae_title="LIBREDCM",
                     ext_neg=[AsynchronousOperationsWindowNegotiation()])
if assoc.is_established:
    print("established; acceptor asynchronous operations window:", assoc.acceptor.asynchronous_operations)
    print("acceptor max PDU:", assoc.acceptor.maximum_length)
    print("acceptor implementation:", assoc.acceptor.implementation_class_uid, assoc.acceptor.implementation_version_name)
    assoc.release()
else:
    ok = False
    print("NOT established")

# 2. SOP Class Extended Negotiation (relational queries) for Study Root FIND:
#    LibreDCM returns no extended negotiation item, so relational queries are
#    not agreed.
head("SOP Class Extended Negotiation (relational queries) proposed")
ae = AE(ae_title="DVTPROBE")
ae.add_requested_context(StudyRootQueryRetrieveInformationModelFind)
ext = SOPClassExtendedNegotiation()
ext.sop_class_uid = StudyRootQueryRetrieveInformationModelFind
ext.service_class_application_information = b"\x01"
assoc = ae.associate(GW, PORT, ae_title="LIBREDCM", ext_neg=[ext])
if assoc.is_established:
    print("established; acceptor SOP class extended negotiation:", assoc.acceptor.sop_class_extended or "{} (none returned)")
    assoc.release()
else:
    ok = False
    print("NOT established")

# 3. User Identity Negotiation (username, positive response requested):
#    LibreDCM ignores the item and sends no response.
head("User Identity Negotiation (username, positive response requested)")
ae = AE(ae_title="DVTPROBE")
ae.add_requested_context(Verification)
uid_neg = UserIdentityNegotiation()
uid_neg.user_identity_type = 1
uid_neg.primary_field = b"dcs-user"
uid_neg.positive_response_requested = True
assoc = ae.associate(GW, PORT, ae_title="LIBREDCM", ext_neg=[uid_neg])
if assoc.is_established:
    print("established; user identity response from acceptor:",
          getattr(assoc.acceptor, "user_identity", None) or "none")
    status = assoc.send_c_echo()
    print("C-ECHO status: 0x%04X" % status.Status)
    assoc.release()
else:
    ok = False
    print("NOT established")

# 4. C-GET when the requestor proposed a storage context for only one of the
#    SOP classes in the study (CT, not MR). Records what LibreDCM does.
head("C-GET of study 1 (CT + MR) with a storage context for CT Image Storage only")
received = []


def on_store(event):
    received.append(event.request.AffectedSOPClassUID)
    return 0x0000


from pynetdicom import evt  # noqa: E402

ae = AE(ae_title="DVTPROBE")
ae.dimse_timeout = 15
ae.add_requested_context(StudyRootQueryRetrieveInformationModelGet)
ae.add_requested_context(CTImageStorage)
ae.add_requested_context(Verification)
assoc = ae.associate(GW, PORT, ae_title="LIBREDCM", ext_neg=[build_role(CTImageStorage, scp_role=True)],
                     evt_handlers=[(evt.EVT_C_STORE, on_store)])
if assoc.is_established:
    q = Dataset()
    q.QueryRetrieveLevel = "STUDY"
    q.StudyInstanceUID = S1
    try:
        for status, ident in assoc.send_c_get(q, StudyRootQueryRetrieveInformationModelGet):
            if status:
                print("C-GET-RSP status: 0x%04X" % status.Status,
                      "completed=%s failed=%s warning=%s" % (status.get("NumberOfCompletedSuboperations"),
                                                            status.get("NumberOfFailedSuboperations"),
                                                            status.get("NumberOfWarningSuboperations")))
            else:
                print("C-GET: no response (association aborted or timed out)")
    except Exception as e:  # noqa: BLE001
        print("C-GET raised:", type(e).__name__, e)
    print("C-STORE sub-operations received:", received)
    try:
        st = assoc.send_c_echo()
        print("C-ECHO on the same association afterwards:", ("0x%04X" % st.Status) if st else "no response")
    except Exception as e:  # noqa: BLE001
        print("C-ECHO on the same association afterwards raised:", type(e).__name__, e)
    print("association aborted:", assoc.is_aborted, " established:", assoc.is_established)
    if assoc.is_established:
        assoc.release()
else:
    ok = False
    print("NOT established")

# 5. Presentation contexts LibreDCM must refuse: an unknown (private) SOP
#    class, and a standard class offered only in a transfer syntax LibreDCM
#    does not store (Deflated Explicit VR Little Endian).
head("Presentation contexts: unknown SOP class; CT Image Storage in Deflated Explicit VR Little Endian only")
ae = AE(ae_title="DVTPROBE")
ae.add_requested_context("1.3.6.1.4.1.67686.9.1.900.99", ["1.2.840.10008.1.2.1"])
ae.add_requested_context(CTImageStorage, ["1.2.840.10008.1.2.1.99"])
ae.add_requested_context(Verification)
assoc = ae.associate(GW, PORT, ae_title="LIBREDCM")
if assoc.is_established:
    for cx in assoc.rejected_contexts:
        print("rejected: context %d %s result %s (%s)" % (cx.context_id, cx.abstract_syntax, cx.result, cx.status))
    for cx in assoc.accepted_contexts:
        print("accepted: context %d %s transfer syntax %s" % (cx.context_id, cx.abstract_syntax, cx.transfer_syntax[0]))
    assoc.release()
else:
    ok = False
    print("NOT established")

# 6. TLS protocol versions accepted by the DICOM TLS listener (dcs-tls).
head("DICOM TLS listener: protocol versions")
for name, ver in (("TLSv1.1", ssl.TLSVersion.TLSv1_1), ("TLSv1.2", ssl.TLSVersion.TLSv1_2),
                  ("TLSv1.3", ssl.TLSVersion.TLSv1_3)):
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    # Only the protocol version is probed here, against a throwaway test
    # container with a self-signed certificate; trust is checked separately by
    # DCMTK echoscu +cf (certificate verification on).
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    try:
        ctx.set_ciphers("ALL:@SECLEVEL=0")
    except ssl.SSLError:
        pass
    ctx.minimum_version = ver
    ctx.maximum_version = ver
    try:
        with socket.create_connection(("dcs-tls", PORT), timeout=10) as raw:
            with ctx.wrap_socket(raw, server_hostname="dcs-tls") as s:
                print(f"{name}: accepted, negotiated {s.version()} cipher {s.cipher()[0]}")
    except Exception as e:  # noqa: BLE001
        print(f"{name}: refused ({type(e).__name__}: {e})")

sys.exit(0 if ok else 1)
