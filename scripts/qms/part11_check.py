#!/usr/bin/env python3
"""Read the GitHub settings that the Part 11 validation relies on and compare
them with the validated configuration (QMS-SOP-001 §6,
docs/qms/validation/part11-github-drive.md).

Needs the GitHub CLI (`gh`) logged in with read access to the repository's
administration settings. Read-only: it never changes a setting.

    python3 scripts/qms/part11_check.py [--repo mulcahyio/libre-rws]

Exit status 1 when a setting differs from the validated configuration.
"""
import argparse
import json
import subprocess
import sys


def gh(path: str):
    out = subprocess.run(["gh", "api", path], capture_output=True, text=True)
    if out.returncode != 0:
        raise RuntimeError(f"gh api {path}: {out.stderr.strip()}")
    return json.loads(out.stdout)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", default="mulcahyio/libre-rws")
    ap.add_argument("--branch", default="master")
    a = ap.parse_args()
    results = []

    def check(test_id: str, what: str, ok: bool, seen) -> None:
        results.append(ok)
        print(f"{'PASS' if ok else 'FAIL'}  {test_id}  {what}: {seen}")

    try:
        p = gh(f"repos/{a.repo}/branches/{a.branch}/protection")
    except RuntimeError as e:
        print(f"FAIL  VT-01  branch protection of {a.branch} could not be read: {e}")
        return 1
    rsc = p.get("required_status_checks") or {}
    prr = p.get("required_pull_request_reviews") or {}
    en = lambda k: (p.get(k) or {}).get("enabled")
    check("VT-01", "pull request required to change the branch", bool(prr), "reviews block present" if prr else "absent")
    check("VT-01", "rules apply to administrators", en("enforce_admins") is True, en("enforce_admins"))
    check("VT-02", "required status check 'verify'", "verify" in (rsc.get("contexts") or []), rsc.get("contexts"))
    check("VT-02", "conversations must be resolved", en("required_conversation_resolution") is True, en("required_conversation_resolution"))
    check("VT-02", "stale approvals dismissed on new commits", prr.get("dismiss_stale_reviews") is True, prr.get("dismiss_stale_reviews"))
    check("VT-03", "force pushes blocked", en("allow_force_pushes") is False, en("allow_force_pushes"))
    check("VT-03", "branch deletion blocked", en("allow_deletions") is False, en("allow_deletions"))

    rulesets = gh(f"repos/{a.repo}/rulesets")
    tag_ok, seen = False, []
    for rs in rulesets:
        if rs.get("target") != "tag":
            continue
        full = gh(f"repos/{a.repo}/rulesets/{rs['id']}")
        inc = full.get("conditions", {}).get("ref_name", {}).get("include", [])
        types = sorted(r["type"] for r in full.get("rules", []))
        seen.append({"name": full.get("name"), "enforcement": full.get("enforcement"), "include": inc,
                     "rules": types, "bypass": full.get("bypass_actors")})
        if (full.get("enforcement") == "active" and "refs/tags/v*" in inc and not full.get("bypass_actors")
                and {"deletion", "non_fast_forward", "update"} <= set(types)):
            tag_ok = True
    check("VT-04", "release tags v* cannot be deleted, moved or updated (no bypass)", tag_ok, seen)

    collabs = gh(f"repos/{a.repo}/collaborators")
    writers = sorted(f"{c['login']}:{c.get('role_name')}" for c in collabs if c.get("role_name") in ("admin", "maintain", "write"))
    print(f"INFO  VT-05  accounts with write access (compare with the approved list): {writers}")

    repo = gh(f"repos/{a.repo}")
    sa = repo.get("security_and_analysis") or {}
    st = lambda k: (sa.get(k) or {}).get("status")
    check("VT-05", "secret scanning and push protection on",
          st("secret_scanning") == "enabled" and st("secret_scanning_push_protection") == "enabled",
          {"secret_scanning": st("secret_scanning"), "push_protection": st("secret_scanning_push_protection")})
    check("VT-05", "repository visibility public (decision 2026-11-09)", repo.get("visibility") == "public", repo.get("visibility"))

    print(f"\n{sum(results)}/{len(results)} checks passed")
    return 0 if all(results) else 1


if __name__ == "__main__":
    sys.exit(main())
