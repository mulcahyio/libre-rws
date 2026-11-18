#!/usr/bin/env python3
"""Check the LibreDCM DICOM Conformance Statement against the Go source.

    python3 scripts/qms/dcs.py [ROOT]

The conformance statement (docs/dhf/libredcm/dicom-conformance-statement.md)
holds tables between "<!-- dcs:begin NAME -->" and "<!-- dcs:end NAME -->"
markers. They are generated from the running code by the Go test
TestConformanceStatementMatchesCode (modules/dicom/internal/gateway). This
script is the independent second check (QMS-SOP-011): it reads the Go source
as text, shares no code with LibreDCM, and fails when the document and the
code disagree on

- the Verification and Query/Retrieve SOP classes LibreDCM accepts,
- the Storage SOP classes it accepts (gonetdicom ae.AllStorageSOPClasses),
- the transfer syntaxes it stores (dcm.StorageSyntaxes),
- the C-FIND keys per level, their matching types and whether they are
  returned (gateway queryKeys, allowedPrefixes; index levelColumns),
- the DICOM listener settings and their environment variables (gateway
  Config),

and when a section the PS3.2 Annex N template requires is missing.
Exit status 1 on any finding.
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else pathlib.Path(__file__).resolve().parents[2]
DOC = ROOT / "docs/dhf/libredcm/dicom-conformance-statement.md"
SRC = {
    "storage": ROOT / "third_party/gonetdicom/ae/storage_sop_classes.go",
    "scp": ROOT / "third_party/gonetdicom/ae/scp.go",
    "gateway": ROOT / "modules/dicom/internal/gateway/gateway.go",
    "query": ROOT / "modules/dicom/internal/gateway/query.go",
    "dcm": ROOT / "modules/dicom/internal/dcm/dcm.go",
    "index": ROOT / "modules/dicom/internal/index/index.go",
}
LEVELS = ["PATIENT", "STUDY", "SERIES", "IMAGE"]
LEVEL_GO = {"Patient": "PATIENT", "Study": "STUDY", "Series": "SERIES", "Image": "IMAGE"}

# Sections of the PS3.2 (2026d) Annex N template, as numbered in the statement.
REQUIRED_HEADINGS = [
    "1 Overview", "1.1 Content and Transfer", "1.2 DIMSE Services", "1.3 DICOM Web Services",
    "1.4 Media Services", "1.5 Real-Time Video Service", "1.6 De-identification Profiles",
    "1.7 Specific Character Sets", "2 Table of Contents", "3 Introduction", "3.1 Revision History",
    "3.2 Audience", "3.3 Remarks", "3.4 Terms and Definitions", "3.5 Abbreviations", "3.6 References",
    "4 Implementation Model", "4.1 Application Entities and Data Flow",
    "5 Service and Interoperability Description", "5.1 Mapping of Services to Application Entities",
    "5.2 DIMSE Services", "6 Configuration", "7 Network and Media Communication Details",
    "7.1 General", "7.2 Specifications", "7.3 Status Codes", "8 Security",
    "9 Information Object Definitions", "10 Structured Report Content Encoding", "11 Security Details",
    "12 Mapping of Attributes",
]
BLOCKS = ["dimse-sop-classes", "storage-sop-classes", "transfer-syntaxes", "association-parameters",
          "configuration"] + ["query-keys-" + lvl.lower() for lvl in LEVELS]

problems = []


def problem(msg):
    problems.append(msg)


def read(key):
    p = SRC[key]
    if not p.exists():
        problem(f"source file missing: {p.relative_to(ROOT)}")
        return ""
    return p.read_text(encoding="utf-8")


# ---------------------------------------------------------------- code side

def code_storage_classes():
    txt = read("storage")
    m = re.search(r"var AllStorageSOPClasses = UIDStrings\((.*?)\n\)", txt, re.S)
    if not m:
        problem("AllStorageSOPClasses not found in storage_sop_classes.go")
        return []
    return re.findall(r"uid\.(\w+),", m.group(1))


def code_dimse_classes():
    out = []
    if re.search(r"pdu\.VerificationSOPClass:\s*\{\}", read("scp")):
        out.append("Verification")  # gonetdicom accepts Verification on every association
    m = re.search(r"func \(g \*Gateway\) serverConfig\(\).*?\n}\n", read("gateway"), re.S)
    if not m:
        problem("serverConfig not found in gateway.go")
        return out
    out += re.findall(r"ae\.(\w+QueryRetrieveInformationModel\w+)", m.group(0))
    return out


def code_transfer_syntaxes():
    txt = read("dcm")
    consts = dict(re.findall(r"^\s*(\w+)\s*=\s*\"([\d.]+)\"", txt, re.M))
    m = re.search(r"var StorageSyntaxes = \[\]string\{(.*?)\n\}", txt, re.S)
    if not m:
        problem("StorageSyntaxes not found in dcm.go")
        return []
    out = []
    for tok in re.findall(r"^\s*(\"[\d.]+\"|\w+),", m.group(1), re.M):
        if tok.startswith('"'):
            out.append(tok.strip('"'))
        elif tok in consts:
            out.append(consts[tok])
        else:
            problem(f"StorageSyntaxes entry {tok} cannot be resolved")
    return out


def code_query_keys():
    """keyword -> (column, kind), and level -> allowed column prefixes."""
    txt = read("query")
    m = re.search(r"var queryKeys = map\[string\]keyDef\{(.*?)\n\}", txt, re.S)
    keys = {kw: (col, kind) for kw, col, kind in re.findall(r'"(\w+)":\s*\{"([^"]*)",\s*(k\w+)\}', m.group(1))} if m else {}
    if not keys:
        problem("queryKeys not found in query.go")
    m = re.search(r"var allowedPrefixes = map\[index\.Level\]\[\]string\{(.*?)\n\}", txt, re.S)
    prefixes = {}
    if m:
        for lvl, body in re.findall(r"index\.(\w+):\s*\{([^}]*)\}", m.group(1)):
            prefixes[LEVEL_GO.get(lvl, lvl)] = re.findall(r'"([^"]+)"', body)
    if set(prefixes) != set(LEVELS):
        problem(f"allowedPrefixes levels {sorted(prefixes)} != {LEVELS}")
    # Keys the C-FIND handler fills in itself at one level (ModalitiesInStudy).
    special = re.findall(r'if q\.level == index\.(\w+) && dcm\.Has\(req\.IdentifierData, "(\w+)"\)', txt)
    return keys, prefixes, [(LEVEL_GO.get(lvl, lvl), kw) for lvl, kw in special]


def code_level_columns():
    txt = read("index")
    m = re.search(r"var levelColumns = map\[Level\]\[\]column\{(.*?)\n\}\n", txt, re.S)
    if not m:
        problem("levelColumns not found in index.go")
        return {}
    body = m.group(1)
    starts = [(mm.start(), LEVEL_GO[mm.group(1)]) for mm in re.finditer(r"^\t(Patient|Study|Series|Image): \{", body, re.M)]
    out = {}
    for i, (pos, lvl) in enumerate(starts):
        end = starts[i + 1][0] if i + 1 < len(starts) else len(body)
        out[lvl] = {kw for kw in re.findall(r',\s*"([A-Za-z]\w*)"\}', body[pos:end])}
    return out


def code_config():
    """yaml key -> first env variable (or None) of gateway.Config, in field order."""
    m = re.search(r"^type Config struct \{(.*?)^\}", read("gateway"), re.S | re.M)
    if not m:
        problem("type Config not found in gateway.go")
        return []
    out = []
    for tags in re.findall(r"`([^`]*yaml:\"[^`]*)`", m.group(1)):
        key = re.search(r'yaml:"([^",]*)', tags)
        env = re.search(r'env:"([^",]*)', tags)
        if key:
            out.append((key.group(1), env.group(1) if env else None))
    return out


def matching_type(kind, matches, returned):
    m = []
    if matches:
        m.append("SINGLE_VALUE")
        if kind in ("kString", "kPersonName"):
            m.append("WILDCARD")
        elif kind == "kUID":
            m.append("UID")
        elif kind in ("kDate", "kTime"):
            m.append("RANGE")
    if returned:
        m.append("UNIVERSAL")
    return "; ".join(m)


def expected_query_keys():
    keys, prefixes, special = code_query_keys()
    columns = code_level_columns()
    out = {}
    for lvl in LEVELS:
        returned = set(columns.get(lvl, set())) | {kw for l2, kw in special if l2 == lvl}
        rows = {}
        for kw, (col, kind) in keys.items():
            if kind == "kReturn":
                matches = False
            elif kind == "kModality":
                matches = lvl == "STUDY"
            else:
                matches = bool(col) and any(col.startswith(p) for p in prefixes.get(lvl, []))
            r = kw in returned
            if matches or r:
                rows[kw] = matching_type(kind, matches, r)
        out[lvl] = rows
    return out


# ---------------------------------------------------------------- document side

def doc_blocks(doc):
    blocks = {}
    for m in re.finditer(r"<!-- dcs:begin ([a-z0-9-]+) -->\n(.*?)<!-- dcs:end ([a-z0-9-]+) -->", doc, re.S):
        if m.group(1) != m.group(3):
            problem(f"block {m.group(1)!r} ends with {m.group(3)!r}")
            continue
        if m.group(1) in blocks:
            problem(f"block {m.group(1)!r} appears twice")
        blocks[m.group(1)] = m.group(2)
    return blocks


def table(body):
    """Rows of a Markdown table as dicts keyed by the header cells."""
    lines = [ln.strip() for ln in body.strip().splitlines() if ln.strip().startswith("|")]
    if len(lines) < 2:
        return []
    cells = lambda ln: [c.strip() for c in ln.strip("|").split("|")]  # noqa: E731
    header = cells(lines[0])
    return [dict(zip(header, cells(ln))) for ln in lines[2:]]


def unquote(s):
    return s.strip().strip("`")


def uid_key(u):
    return [int(x) for x in u.split(".")]


def check_list(name, got, want, ordered):
    if ordered:
        if got != want:
            problem(f"{name}: document {got} != code {want}")
        return
    dups = sorted({x for x in got if got.count(x) > 1})
    if dups:
        problem(f"{name}: listed twice: {', '.join(dups)}")
    missing, extra = sorted(set(want) - set(got)), sorted(set(got) - set(want))
    if missing:
        problem(f"{name}: in the code but not in the document: {', '.join(missing)}")
    if extra:
        problem(f"{name}: in the document but not in the code: {', '.join(extra)}")


def main():
    if not DOC.exists():
        print(f"missing {DOC.relative_to(ROOT)}")
        return 1
    doc = DOC.read_text(encoding="utf-8")
    if "DRAFT for approval" not in doc:
        problem("document is not marked 'DRAFT for approval'")
    headings = {re.sub(r"\s+", " ", ln.lstrip("#").strip()) for ln in doc.splitlines() if ln.startswith("#")}
    for h in REQUIRED_HEADINGS:
        if not any(x == h or x.startswith(h + " ") or x.startswith(h + " -") for x in headings):
            problem(f"required section missing: {h}")
    blocks = doc_blocks(doc)
    for b in BLOCKS:
        if b not in blocks:
            problem(f"generated table missing: {b}")

    counts = {}
    if "dimse-sop-classes" in blocks:
        got = [unquote(r.get("Keyword", "")) for r in table(blocks["dimse-sop-classes"])]
        check_list("Verification and Query/Retrieve SOP classes", got, code_dimse_classes(), ordered=True)
        counts["dimse"] = len(got)
    if "storage-sop-classes" in blocks:
        rows = table(blocks["storage-sop-classes"])
        got = [unquote(r.get("Keyword", "")) for r in rows]
        check_list("Storage SOP classes", got, code_storage_classes(), ordered=False)
        uids = [unquote(r.get("UID", "")) for r in rows]
        try:
            if uids != sorted(uids, key=uid_key):
                problem("Storage SOP classes: not in numerical UID order (PS3.2 Table N.1-1)")
        except ValueError:
            problem("Storage SOP classes: a UID cell is not a UID")
        counts["storage"] = len(got)
    if "transfer-syntaxes" in blocks:
        got = [unquote(r.get("UID", "")) for r in table(blocks["transfer-syntaxes"])]
        check_list("Transfer syntaxes", got, code_transfer_syntaxes(), ordered=False)
        counts["transfer syntaxes"] = len(got)
    if "configuration" in blocks:
        rows = table(blocks["configuration"])
        got = [unquote(r.get("Setting (`dicom:`)", "")) for r in rows]
        cfg = code_config()
        check_list("Configuration settings", got, [k for k, _ in cfg], ordered=True)
        envs = {unquote(r.get("Setting (`dicom:`)", "")): r.get("Environment variable", "") for r in rows}
        for key, env in cfg:
            if env and key in envs and not envs[key].startswith(f"`{env}`"):
                problem(f"Configuration {key}: document environment variable {envs[key]!r}, code {env!r}")
        counts["settings"] = len(got)
    want = expected_query_keys()
    for lvl in LEVELS:
        b = "query-keys-" + lvl.lower()
        if b not in blocks:
            continue
        rows = {unquote(r.get("Keyword", "")): r.get("Matching Type", "") for r in table(blocks[b])}
        check_list(f"C-FIND keys at {lvl} level", list(rows), list(want[lvl]), ordered=False)
        for kw, mt in rows.items():
            if kw in want[lvl] and mt != want[lvl][kw]:
                problem(f"C-FIND {lvl} {kw}: matching type {mt!r}, code gives {want[lvl][kw]!r}")
        counts[f"{lvl.lower()} keys"] = len(rows)

    for p in problems:
        print(p)
    summary = ", ".join(f"{k} {v}" for k, v in counts.items())
    print(f"dcs: {summary}; {len(problems)} problems")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
