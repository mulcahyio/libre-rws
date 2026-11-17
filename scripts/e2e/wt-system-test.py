#!/usr/bin/env python3
"""Libre WT system test against real LibreDCM containers.

Run by scripts/e2e/wt-system-test.sh, which starts
deploy/compose/docker-compose.wt-system.yml first. Python standard library
only. Drives Libre WT the way a browser does (forms, cookies, CSRF tokens,
warning pages, single-use confirmations) and checks the effect on LibreDCM
and on the DCMTK destination.

Usage: wt-system-test.py WT_URL RUN_DIR OUT_DIR

Every check prints one line "PASS <id> <text>" or "FAIL <id> <text>"; the
exit code is 1 when any check failed. Pages are saved to OUT_DIR/pages for
the record. Only synthetic test data is used.
"""
import hashlib
import html
import http.cookiejar
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from html.parser import HTMLParser

WT, RUN, OUT = sys.argv[1].rstrip("/"), sys.argv[2], sys.argv[3]
COMPOSE = ["docker", "compose", "-p", "wtsys", "-f",
           os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..",
                        "deploy", "compose", "docker-compose.wt-system.yml")]
PAGES = os.path.join(OUT, "pages")
os.makedirs(PAGES, exist_ok=True)
TOKENS = [open(os.path.join(RUN, f)).read().strip() for f in ("dcm-1.token", "dcm-2.token")]
PW = {"admin": "wt-system-admin-pass-1", "oper": "wt-system-oper-pass-1",  # gitleaks:allow (synthetic test value)
      "view": "wt-system-view-pass-1"}  # gitleaks:allow (synthetic test value)
RESULTS = []
SEEN = []  # every page body, checked for secrets at the end


def check(cid, ok, text, page=None):
    RESULTS.append((cid, bool(ok)))
    print(("PASS" if ok else "FAIL"), cid, text, flush=True)
    if not ok and page is not None:
        with open(os.path.join(PAGES, f"FAIL-{cid}.html"), "w") as f:
            f.write(page)


def dc(*args, input_=None):
    """docker compose ... ; returns (rc, stdout+stderr)."""
    p = subprocess.run(COMPOSE + list(args), capture_output=True, text=True, input=input_,
                       env=dict(os.environ, WT_SYS_RUN=RUN), timeout=300)
    return p.returncode, p.stdout + p.stderr


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


class Browser:
    """A cookie-keeping client that follows redirects like a browser."""

    def __init__(self, name):
        self.name = name
        self.jar = http.cookiejar.CookieJar()
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        self.raw = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar), NoRedirect)
        self.last = ""

    def _do(self, opener, method, path, data=None, ctype=None):
        body = None
        if data is not None:
            body = data if isinstance(data, bytes) else urllib.parse.urlencode(data, doseq=True).encode()
        req = urllib.request.Request(WT + path, data=body, method=method)
        if body is not None:
            req.add_header("Content-Type", ctype or "application/x-www-form-urlencoded")
        try:
            r = opener.open(req, timeout=150)
            code, headers, text = r.status, r.headers, r.read()
        except urllib.error.HTTPError as e:
            code, headers, text = e.code, e.headers, e.read()
        text = text.decode("utf-8", "replace")
        SEEN.append(text)
        self.last = text
        return code, text, headers

    def get(self, path):
        return self._do(self.op, "GET", path)

    def post(self, path, data, ctype=None):
        return self._do(self.op, "POST", path, data, ctype)

    def post_raw(self, path, data):
        return self._do(self.raw, "POST", path, data)

    def csrf(self, path="/account"):
        _, body, _ = self.get(path)
        m = re.search(r'name="csrf" value="([^"]+)"', body)
        return m.group(1) if m else ""

    def login(self, user):
        code, body, _ = self.post("/login", {"username": user, "password": PW[user]})
        return code == 200 and "Log out" in body


def save(name, body):
    with open(os.path.join(PAGES, name + ".html"), "w") as f:
        f.write(body)


def confirm_of(body):
    m = re.search(r'name="confirm" value="([^"]+)"', body)
    return m.group(1) if m else ""


def approval(body, csrf, reason, **extra):
    form = {"csrf": csrf, "confirm": confirm_of(body), "approve": "yes", "reason": reason}
    for ack in re.findall(r'name="(ack_\d+)"', body):
        form[ack] = "yes"
    form.update(extra)
    return form


def err_of(body):
    m = re.search(r'class="msg error"[^>]*>(.*?)</', body, re.S)
    return m.group(1).strip() if m else ""


def has(body, *needles):
    return all(n in body for n in needles)


def text_of(body):
    return re.sub(r"\s+", " ", html.unescape(re.sub(r"<[^>]+>", " ", body)))


class _Inputs(HTMLParser):
    """Collects (name, value, type) of the inputs of every form, in order."""

    def __init__(self):
        super().__init__()
        self.forms, self.cur, self.ta = [], None, None

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "form":
            self.cur = []
            self.forms.append(self.cur)
        elif tag == "input" and self.cur is not None and a.get("name"):
            self.cur.append([a["name"], a.get("value", ""), a.get("type", "text"), "checked" in a])
        elif tag == "textarea" and self.cur is not None:
            self.ta = [a.get("name"), "", "textarea", False]
            self.cur.append(self.ta)

    def handle_data(self, data):
        if self.ta is not None:
            self.ta[1] += data

    def handle_endtag(self, tag):
        if tag == "textarea":
            self.ta = None
        elif tag == "form":
            self.cur = None


def destinations_form(edit_page, name, host, port, ae):
    """The destinations form of the edit page as a browser submits it, with
    the existing rows kept and NAME added in the empty row."""
    p = _Inputs()
    p.feed(edit_page)
    for f in p.forms:
        if ["setting", "remote_aes", "hidden", False] in f:
            out, filled = [], False
            new = {"name": name, "host": host, "port": port, "ae_title": ae}
            for n, val, typ, checked in f:
                if typ == "checkbox" and not checked:
                    continue
                if not filled and typ == "text" and n == "name" and val == "":
                    filled = "name"
                if filled and filled is not True and n in new:
                    val = new[n]
                    if n == "ae_title":
                        filled = True
                out.append((n, val))
            return out
    return []


def wait_ready():
    for _ in range(90):
        try:
            with urllib.request.urlopen(WT + "/healthz", timeout=3) as r:
                if r.status == 200:
                    return True
        except Exception:
            time.sleep(1)
    return False


def main():
    check("ENV-1", wait_ready(), "Libre WT answers /healthz")
    if not RESULTS[-1][1]:
        print("SUMMARY 1 checks, 0 passed, 1 failed: ['ENV-1']")
        return 1

    # ---- WT-SRS-002, -003, -005: first run, accounts, login ----
    a = Browser("admin")
    code, body, _ = a.get("/")
    check("SYS-01", code == 200 and "/setup" in body and "first" in body.lower(),
          "a fresh Libre WT leads every page to the first-run page", body)
    code, body, _ = a.post("/setup", {"username": "admin", "password": "short", "password2": "short"})
    check("SYS-02", "admin" not in [c.name for c in a.jar] and ("12" in body),
          "first run refuses a password under 12 characters", body)
    code, body, _ = a.post("/setup", {"username": "admin", "password": PW["admin"], "password2": PW["admin"]})
    check("SYS-03", code == 200 and "Log out" in body, "first run creates the Admin and logs in", body)
    other = Browser("intruder")
    code, body, _ = other.post("/setup", {"username": "evil", "password": "evil-password-123",  # gitleaks:allow (synthetic test value)
                                          "password2": "evil-password-123"})  # gitleaks:allow (synthetic test value)
    check("SYS-04", "Log out" not in body and 'action="/login"' in body, "the first-run page stops working once an account exists", body)
    code, body, _ = other.post("/login", {"username": "admin", "password": "wrong-password-xyz"})
    code2, body2, _ = other.post("/login", {"username": "nobody", "password": "wrong-password-xyz"})
    check("SYS-05", "Log out" not in body and err_of(body) != "" and err_of(body) == err_of(body2),
          "a failed login does not reveal whether the user exists", body)
    csrf = a.csrf()
    for user, role in (("oper", "operator"), ("view", "viewer")):
        code, body, _ = a.post("/users", {"csrf": csrf, "username": user, "password": PW[user], "role": role})
        check("SYS-06-" + user, code == 200 and f"<td>{user}</td><td>{role}</td>" in body, f"Admin creates the {role} account", body)
    code, body, _ = a.post("/users", {"username": "nocsrf", "password": "no-csrf-password-1", "role": "admin"})  # gitleaks:allow (synthetic test value)
    check("SYS-07", code == 403, f"a state change without the CSRF token is refused (HTTP {code})", body)
    o, v = Browser("oper"), Browser("view")
    check("SYS-08", o.login("oper") and v.login("view"), "Operator and Viewer log in")

    # ---- WT-SRS-013…018, -023, -035, -036: fleet, dashboard, status, about ----
    code, body, _ = v.get("/")
    save("fleet", body)
    check("SYS-10", code == 200 and has(body, "dcm-1", "dcm-2", "LIBREDCM1", "LIBREDCM2", "Cluster <b>wtsys</b>"),
          "fleet page lists both instances under their cluster label", body)
    check("SYS-11", has(text_of(body), "not enforced (source build)", "missing, notice not acknowledged"),
          "fleet page shows the licence state of both nodes (dcm-2 unlicensed, not acknowledged)", body)
    for inst in ("dcm-1", "dcm-2"):
        code, body, _ = v.get(f"/i/{inst}/")
        save(f"dashboard-{inst}", body)
        check(f"SYS-12-{inst}", code == 200 and has(body, f'<b class="iname">{inst}</b>', "<b>ready</b>"),
              f"dashboard of {inst} shows the banner and readiness", body)
        check(f"SYS-13-{inst}", "plain HTTP" in body or "not encrypted" in body,
              f"plain-HTTP warning shown for {inst} (WT-SRS-036)", body)
        code, body, _ = v.get(f"/i/{inst}/status")
        save(f"status-{inst}", body)
        check(f"SYS-14-{inst}", code == 200 and "Readiness" in body and "restart" in body and "host" in body,
              f"status page of {inst} shows readiness and the host restart note", body)
    code, body, _ = v.get("/about")
    save("about", body)
    check("SYS-15", has(body, "0.2.0-rc.1", "<code>dcm-1</code>", "<code>dcm-2</code>"),
          "about page shows the Libre WT version and each node", body)
    code, body, _ = v.get("/i/nope/")
    check("SYS-16", code == 404, f"an unknown instance gives not found (HTTP {code})", body)

    # ---- store synthetic data with DCMTK storescu ----
    rc, out = dc("exec", "-T", "dcmtk", "python3", "/e2e/wt_make_data.py", "/tmp/wtdata")
    check("DATA-1", rc == 0, "synthetic study generated (2 series x 2 instances + 1 conflicting instance)")
    if rc != 0:
        print(out)
        return 1
    man = json.loads(out.strip().splitlines()[-1])
    with open(os.path.join(OUT, "manifest.json"), "w") as f:
        json.dump(man, f, indent=1)
    rc, out = dc("exec", "-T", "dcmtk", "storescu", "-aec", "LIBREDCM1", "-aet", "MODALITY1",
                 "+sd", "dcm-1", "11112", "/tmp/wtdata/study")
    check("DATA-2", rc == 0, "storescu stored the 4 instances of the study on dcm-1")
    rc, out = dc("exec", "-T", "dcmtk", "storescu", "-aec", "LIBREDCM1", "-aet", "MODALITY1",
                 "dcm-1", "11112", "/tmp/wtdata/conflict/c1.dcm")
    check("DATA-3", rc == 0, "storescu sent the conflicting instance (LibreDCM accepts and holds it)")
    time.sleep(1)

    # ---- WT-SRS-007…011: Study Exceptions ----
    code, body, _ = v.get("/i/dcm-1/exceptions")
    save("exceptions", body)
    m = re.search(r'href="/i/dcm-1/exceptions/([^"/?]+)"', body)
    check("SYS-20", code == 200 and m and "WTSYS-9999" in body,
          "Study Exceptions lists the held instance with its incoming Patient ID", body)
    if m:
        eid = m.group(1)
        code, body, _ = v.get(f"/i/dcm-1/exceptions/{eid}")
        save("exception", body)
        check("SYS-21", has(body, "WTSYS-0001", "WTSYS-9999") and "/review?" not in body,
              "detail shows archived and incoming patient; a Viewer gets no edit controls", body)
        code, body, _ = v.get(f"/i/dcm-1/exceptions/{eid}/review?action=reject")
        check("SYS-22", code == 403, f"a Viewer can't open the edit review (HTTP {code})", body)
        code, body, _ = a.get(f"/i/dcm-1/exceptions/{eid}/review?action=reject")
        save("exception-review", body)
        check("SYS-23", code == 200 and confirm_of(body), "Admin sees LibreDCM's plan on the warning page", body)
        csrf = a.csrf()
        bad = approval(body, csrf, "")
        code, b2, _ = a.post(f"/i/dcm-1/exceptions/{eid}/resolve", dict(bad, action="reject"))
        check("SYS-24", "Log out" in b2 and "Done:" not in b2, "the edit is refused without a reason", b2)
        code, body, _ = a.get(f"/i/dcm-1/exceptions/{eid}/review?action=reject")
        form = approval(body, csrf, "system test: synthetic conflicting image", action="reject")
        code, body, _ = a.post(f"/i/dcm-1/exceptions/{eid}/resolve", form)
        save("exception-resolved", body)
        check("SYS-25", code == 200 and "Done:" in body, "Admin rejects the incoming image after approval", body)
        code, b2, _ = a.post(f"/i/dcm-1/exceptions/{eid}/resolve", form)
        check("SYS-26", code == 409 and "already used" in b2 and "Done:" not in b2, "the same confirmation can't be used twice", b2)

    # ---- WT-SRS-019…022: configuration, destinations, allow-list, edit, reload, rollback ----
    code, body, _ = v.get("/i/dcm-1/config")
    check("SYS-30", code == 403, f"a Viewer can't open the configuration (HTTP {code})", body)
    code, body, _ = o.get("/i/dcm-1/config")
    save("config", body)
    check("SYS-31", code == 200 and "<code>dicom.ae_title</code>" in body and "/reload/review" not in body,
          "Operator reads the configuration without change controls", body)
    check("SYS-32", code == 200 and "admin.token" in body and not any(t in body for t in TOKENS), "the admin token is not on the configuration page", body)
    code, body, _ = o.get("/i/dcm-1/allow-list")
    save("allow-list", body)
    check("SYS-33", "every calling AE may connect" in body, "an empty allow-list is shown as a warning", body)
    csrf = a.csrf()
    code, body, _ = a.get("/i/dcm-1/config/edit")
    save("config-edit", body)
    code, body, _ = a.post("/i/dcm-1/config/review", destinations_form(body, "STORESCP", "dcmtk", "11113", "STORESCP"))
    save("config-review", body)
    check("SYS-34", code == 200 and "added" in body and "STORESCP" in body and confirm_of(body),
          "adding a destination shows LibreDCM's plan with the difference", body)
    code, body, _ = a.post("/i/dcm-1/config/apply", approval(body, csrf, "system test: add DCMTK destination"))
    check("SYS-35", code == 200 and "applied the change" in body, "Admin applies the destination change", body)
    code, body, _ = a.get("/i/dcm-1/config/edit")
    code, body, _ = a.post("/i/dcm-1/config/review", destinations_form(body, "SPARE", "192.0.2.10", "104", "SPARE"))
    check("SYS-36a", bool(confirm_of(body)) and "SPARE" in body and "removed" not in text_of(body), "the plan for SPARE removes nothing", body)
    code, body, _ = a.post("/i/dcm-1/config/apply", approval(body, csrf, "system test: second destination"))
    check("SYS-36", code == 200 and "applied the change" in body, "Admin adds a second destination SPARE", body)
    code, body, _ = o.get("/i/dcm-1/remote-aes")
    save("remote-aes", body)
    check("SYS-37", has(body, "<b>STORESCP</b>", "<b>SPARE</b>", "overlay"), "both destinations are in force", body)
    code, body, _ = a.post("/i/dcm-1/rollback/review", {"csrf": csrf})
    save("rollback-review", body)
    check("SYS-38", code == 200 and "SPARE" in body and confirm_of(body), "rollback shows the plan (SPARE removed)", body)
    code, body, _ = a.post("/i/dcm-1/rollback/apply", approval(body, csrf, "system test: undo SPARE"))
    check("SYS-39", code == 200 and "Done: Rollback" in body, "rollback applied", body)
    code, body, _ = o.get("/i/dcm-1/remote-aes")
    check("SYS-40", "<b>STORESCP</b>" in body and "<b>SPARE</b>" not in body,
          "after the rollback only STORESCP remains", body)
    code, body, _ = a.post("/i/dcm-1/reload/review", {"csrf": csrf})
    code, body, _ = a.post("/i/dcm-1/reload/apply", approval(body, csrf, "system test: reload"))
    check("SYS-41", code == 200 and "Done: Reload" in body, "reload of dcm-1 applied", body)
    code, body, _ = o.post("/i/dcm-1/config/review", {"csrf": o.csrf(), "setting": "remote_aes", "name": "X",
                                                     "host": "x", "port": "104", "ae_title": "X"})
    check("SYS-42", code == 403, f"an Operator can't change the configuration (HTTP {code})", body)

    # ---- WT-SRS-024: C-ECHO test ----
    ocsrf = o.csrf()
    code, body, _ = o.post("/i/dcm-1/echo/review", {"csrf": ocsrf, "remote_ae": "STORESCP"})
    code, body, _ = o.post("/i/dcm-1/echo/apply", approval(body, ocsrf, "system test: echo"))
    save("echo-result", body)
    check("SYS-45", code == 200 and 'state success">success' in body, "Operator C-ECHO to STORESCP succeeds", body)
    code, body, _ = o.post("/i/dcm-1/echo/review", {"csrf": ocsrf, "host": "dcmtk", "port": "11113", "ae_title": "STORESCP"})
    check("SYS-46", code == 403 and not confirm_of(body),
          f"an Operator can't test a free address (HTTP {code})", body)
    code, body, _ = a.post("/i/dcm-1/echo/review", {"csrf": csrf, "host": "dcm-2", "port": "11112", "ae_title": "LIBREDCM2"})
    code, body, _ = a.post("/i/dcm-1/echo/apply", approval(body, csrf, "system test: echo free address"))
    check("SYS-47", code == 200 and 'state success">success' in body,
          "Admin C-ECHO from dcm-1 to the free address dcm-2 succeeds", body)

    # ---- WT-SRS-025: studies ----
    code, body, _ = v.get("/i/dcm-1/studies")
    check("SYS-50", code == 403, f"a Viewer can't search studies (HTTP {code})", body)
    ocsrf = o.csrf()
    code, body, _ = o.post("/i/dcm-1/studies/search", {"csrf": ocsrf, "patient_id": "*"})
    check("SYS-51", "Log out" in body and "WTSYS-0001" not in body, "a search with only * is not sent", body)
    code, body, _ = o.post("/i/dcm-1/studies/search", {"csrf": ocsrf, "patient_id": "WTSYS-00*"})
    save("studies-search", body)
    check("SYS-52", code == 200 and has(body, man["study_uid"], "WTSYS-0001", "not a diagnostic viewer"),
          "Operator finds the study by Patient ID wildcard", body)
    check("SYS-53", "WTSYS-0001" in body and "WTSYS-9999" not in body, "the rejected conflicting image is not listed", body)
    code, body, _ = o.get(f"/i/dcm-1/studies/{man['study_uid']}")
    save("study-operator", body)
    check("SYS-54", code == 200 and all(s in body for s in man["series"]) and "/send/review" not in body,
          "study page lists both series; an Operator sees no send control", body)
    code, body, _ = o.get(f"/i/dcm-1/studies/{man['study_uid']}/series/{man['series'][0]}")
    save("series", body)
    first = [s for s, i in man["instances"].items() if i["series"] == man["series"][0]]
    check("SYS-55", code == 200 and all(s in body for s in first), "series page lists its 2 instances", body)
    code, body, _ = o.post(f"/i/dcm-1/studies/{man['study_uid']}/send/review",
                           {"csrf": ocsrf, "patient_id": man["patient_id"], "destination": "STORESCP"})
    check("SYS-56", code == 403, f"an Operator can't send (HTTP {code})", body)

    # ---- WT-SRS-026: send, byte-identical receipt ----
    code, body, _ = a.get(f"/i/dcm-1/studies/{man['study_uid']}")
    save("study-admin", body)
    code, body, _ = a.post(f"/i/dcm-1/studies/{man['study_uid']}/send/review",
                           {"csrf": csrf, "patient_id": "WTSYS-0002", "destination": "STORESCP"})
    check("SYS-57", "Log out" in body and not confirm_of(body), "a send naming another Patient ID gets no approval", body)
    code, body, _ = a.post(f"/i/dcm-1/studies/{man['study_uid']}/send/review",
                           {"csrf": csrf, "patient_id": man["patient_id"], "destination": "STORESCP"})
    save("send-review", body)
    check("SYS-58", code == 200 and confirm_of(body) and body.count("WTSYS-0001") >= 2 and "STORESCP" in body,
          "send warning page shows the patient and the destination", body)
    code, body, _ = a.post(f"/i/dcm-1/studies/{man['study_uid']}/send/apply",
                           approval(body, csrf, "system test: send study"))
    save("send-result", body)
    check("SYS-59", code == 200 and 'state success">complete' in body and "accepted all 4" in body,
          "send of the whole study: complete, 4 instances accepted", body)
    rc, out = dc("exec", "-T", "dcmtk", "python3", "/e2e/wt_compare.py", "/tmp/wtdata/manifest.json", "/recv")
    with open(os.path.join(OUT, "compare-study.jsonl"), "w") as f:
        f.write(out)
    check("SYS-60", rc == 0, "storescp received all 4 instances byte-identical (data set SHA-256)")
    print(out)
    # Positive control of the comparison: one flipped byte must be caught.
    rc, out = dc("exec", "-T", "dcmtk", "python3", "/e2e/wt_compare.py", "--tamper", "/tmp/wtdata/manifest.json", "/recv")
    check("CTL-1", rc == 1 and '"DIFFERENT"' in out and '"identical": 3' in out,
          "control: the comparison reports a received file with one flipped pixel byte as DIFFERENT")
    code, body, _ = a.post(f"/i/dcm-1/studies/{man['study_uid']}/send/review",
                           {"csrf": csrf, "patient_id": man["patient_id"], "destination": "STORESCP",
                            "series": man["series"][1]})
    check("SYS-61", bool(confirm_of(body)) and "send 2 instance(s) in 1 series" in text_of(body),
          "send of one series plans 2 instances in 1 series", body)
    code, body, _ = a.post(f"/i/dcm-1/studies/{man['study_uid']}/send/apply",
                           approval(body, csrf, "system test: send one series"))
    check("SYS-62", code == 200 and "accepted all 2" in body, "send of one series: complete, 2 accepted", body)
    second = [s for s, i in man["instances"].items() if i["series"] == man["series"][1]]
    rc, out = dc("exec", "-T", "dcmtk", "python3", "/e2e/wt_compare.py", "/tmp/wtdata/manifest.json", "/recv", *second)
    check("SYS-63", rc == 0, "the 2 instances of the series are byte-identical")

    # ---- WT-SRS-027, -022: licence ----
    code, body, _ = v.get("/i/dcm-1/license")
    save("license-dcm-1", body)
    check("SYS-70", "Not enforced" in body and "DICOM services keep running whatever the licence state" in body,
          "licence page of the source build: not enforced, DICOM keeps running", body)
    code, body, _ = v.get("/i/dcm-2/license")
    save("license-dcm-2", body)
    check("SYS-71", "Configuration reloads of this instance are blocked" in body and "/ack/review" not in body,
          "dcm-2 unlicensed: reloads blocked until acknowledged; a Viewer has no controls", body)
    rc, out = dc("exec", "-T", "dcmtk", "storescu", "-aec", "LIBREDCM2", "-aet", "MODALITY1",
                 "dcm-2", "11112", "/tmp/wtdata/study/s2i1.dcm")
    check("SYS-71b", rc == 0, "the unlicensed, not acknowledged official node stores images (C-STORE)")
    code, body, _ = a.post("/i/dcm-2/reload/review", {"csrf": csrf})
    save("reload-blocked", body)
    check("SYS-72", not confirm_of(body) and "/license" in body,
          "the reload of dcm-2 is refused before the acknowledgement and links to the licence page", body)
    code, body, _ = a.post("/i/dcm-2/license/ack/review", {"csrf": csrf})
    save("license-ack-review", body)
    code, b2, _ = a.post("/i/dcm-2/license/ack/apply", approval(body, csrf, "system test ack", signature="oper"))
    check("SYS-73", "Log out" in b2 and "Done:" not in b2, "the acknowledgement is refused when another user name is typed", b2)
    code, body, _ = a.post("/i/dcm-2/license/ack/review", {"csrf": csrf})
    code, body, _ = a.post("/i/dcm-2/license/ack/apply", approval(body, csrf, "system test ack", signature="admin"))
    save("license-ack-done", body)
    check("SYS-74", code == 200 and "recorded the acknowledgement" in body and "DICOM services kept running" in body,
          "Admin acknowledges the unlicensed notice of dcm-2", body)
    code, body, _ = a.post("/i/dcm-2/reload/review", {"csrf": csrf})
    code, body, _ = a.post("/i/dcm-2/reload/apply", approval(body, csrf, "system test: reload after ack"))
    check("SYS-75", code == 200 and "Done: Reload" in body, "the reload of dcm-2 works after the acknowledgement", body)
    code, body, _ = a.post("/i/dcm-2/license/request/review", {"csrf": csrf, "site": "WT system test"})
    code, body, hdr = a.post("/i/dcm-2/license/request/apply",
                             approval(body, csrf, "system test: request file", signature="admin"))
    disp = hdr.get("Content-Disposition", "") if hdr else ""
    check("SYS-76", code == 200 and disp.startswith("attachment;") and "dcm-2" in disp,
          f"the activation request is handed out as a file download ({disp})", body)
    code, body, _ = a.post("/i/dcm-2/config/review", {"csrf": csrf, "setting": "allow_list", "rules": "MODALITY1"})
    save("allow-list-review", body)
    code, body, _ = a.post("/i/dcm-2/config/apply", approval(body, csrf, "system test: restrict callers"))
    check("SYS-77a", code == 200 and "applied the change" in body, "Admin restricts the allow-list of dcm-2 to MODALITY1", body)
    rc, out = dc("exec", "-T", "dcmtk", "storescu", "-aec", "LIBREDCM2", "-aet", "INTRUDER",
                 "dcm-2", "11112", "/tmp/wtdata/study/s1i1.dcm")
    check("SYS-77b", rc != 0, "dcm-2 now refuses the calling AE INTRUDER")
    rc, out = dc("exec", "-T", "dcmtk", "storescu", "-aec", "LIBREDCM2", "-aet", "MODALITY1",
                 "dcm-2", "11112", "/tmp/wtdata/study/s1i1.dcm")
    check("SYS-77", rc == 0, "the unlicensed official node still stores images from MODALITY1 (C-STORE)")

    # ---- WT-SRS-028: backup ----
    code, body, _ = v.get("/i/dcm-1/backups")
    check("SYS-80", code == 403, f"a Viewer can't open backups (HTTP {code})", body)
    ocsrf = o.csrf()
    code, body, _ = o.post("/i/dcm-1/backup/review", {"csrf": ocsrf})
    save("backup-review", body)
    code, body, _ = o.post("/i/dcm-1/backup/apply", approval(body, ocsrf, "system test: backup"))
    save("backup-result", body)
    fm = re.search(r"<dt>File</dt><dd><code>([^<]+)</code>", body)
    sm = re.search(r"<dt>SHA-256</dt><dd><code class=\"uid\">([0-9a-f]{64})</code>", body)
    check("SYS-81", code == 200 and fm and sm and "index only" in body,
          "Operator backup: file, size and SHA-256 shown, index-only note", body)
    if fm and sm:
        name = os.path.basename(fm.group(1))
        dest = os.path.join(OUT, "backup-copy.db")
        rc, out = dc("cp", f"dcm-1:/var/lib/libredcm/backups/{name}", dest)
        real = hashlib.sha256(open(dest, "rb").read()).hexdigest() if rc == 0 else out
        os.remove(dest) if rc == 0 else None
        check("SYS-82", real == sm.group(1), f"the backup file on the node has the SHA-256 shown ({real[:16]}…)")
    code, body, _ = o.get("/i/dcm-1/backups")
    save("backups", body)
    check("SYS-83", "system test: backup" in body and "oper" in body, "backups page lists the backup with user and reason", body)

    # ---- WT-SRS-029, -030, -037: LibreDCM audit records, logs, CSV ----
    code, body, _ = o.get("/i/dcm-1/audit")
    check("SYS-90", code == 403, f"an Operator can't read LibreDCM audit records (HTTP {code})", body)
    code, body, _ = a.get("/i/dcm-1/audit?period=1h")
    save("dcm-audit", body)
    check("SYS-91", code == 200 and "admin" in body and "system test: send study" in body,
          "Admin reads LibreDCM audit records naming the WT user and reason", body)
    code, body, hdr = a.get("/i/dcm-1/audit.csv?period=1h")
    check("SYS-92", code == 200 and "attachment;" in (hdr.get("Content-Disposition", "") if hdr else ""),
          "Admin exports the audit records as CSV")
    code, body, _ = o.get("/i/dcm-1/logs?period=1h")
    save("dcm-logs", body)
    check("SYS-93", code == 200 and "memory" in body, "Operator reads the log lines with the memory-buffer note", body)
    code, body, _ = o.get("/i/dcm-1/logs.csv?period=1h")
    check("SYS-94", code == 403, f"an Operator can't export logs (HTTP {code})", body)

    # ---- WT-SRS-011, -033: Libre WT audit log ----
    code, body, _ = a.get("/audit")
    save("wt-audit", body)
    t = text_of(body)
    needed = ["login", "setup", "send", "backup", "config", "echo", "licen", "resolve", "export"]
    missing = [n for n in needed if n not in t.lower()]
    check("SYS-95", code == 200 and not missing, f"Libre WT audit log records every action (missing: {missing})", body)
    code, body, _ = o.get("/audit")
    check("SYS-96", code == 403, f"an Operator can't read the Libre WT audit log (HTTP {code})", body)

    # ---- WT-SRS-012: account ----
    code, body, _ = o.post("/account/password", {"csrf": o.csrf(), "current": PW["oper"],
                                                  "password": "wt-system-oper-pass-2", "password2": "wt-system-oper-pass-2"})  # gitleaks:allow (synthetic test value)
    code, body, _ = o.get("/account")
    o2 = Browser("oper2")
    PW["oper"] = "wt-system-oper-pass-2"  # gitleaks:allow (synthetic test value)
    check("SYS-97", o2.login("oper"), "a user changes their own password and logs in with the new one")
    code, body, _ = o.post("/logout", {"csrf": o.csrf()})
    code, body, _ = o.get("/i/dcm-1/")
    check("SYS-98", "Log out" not in body and 'action="/login"' in body, "after logout the session no longer works", body)

    # ---- WT-SRS-032: no secret anywhere ----
    rc, logs = dc("logs", "--no-color", "wt")
    with open(os.path.join(OUT, "librewt.log"), "w") as f:
        f.write(logs)
    leaks = [i for i, t in enumerate(TOKENS) if any(t in p for p in SEEN) or t in logs]
    check("SYS-99", not leaks and len(SEEN) > 80, f"no admin token in any of {len(SEEN)} pages or in the Libre WT log")

    failed = [c for c, ok in RESULTS if not ok]
    print(f"SUMMARY {len(RESULTS)} checks, {len(RESULTS) - len(failed)} passed, {len(failed)} failed: {failed}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
