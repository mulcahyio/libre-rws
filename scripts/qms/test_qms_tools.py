#!/usr/bin/env python3
"""Validation tests for the QMS scripts in scripts/qms (QMS-SOP-011).

Each script is copied into a temporary repository with fixture documents
and run as a subprocess, so the tests exercise the real command-line
behaviour (exit status and messages) without touching this repository.

    python3 -m unittest scripts/qms/test_qms_tools.py -v
"""
import pathlib
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest

HERE = pathlib.Path(__file__).resolve().parent
REPO = HERE.parents[1]


class Repo:
    """A throwaway repository with the scripts copied in."""

    def __init__(self):
        self.dir = pathlib.Path(tempfile.mkdtemp(prefix="qms-tools-"))
        (self.dir / "scripts" / "qms").mkdir(parents=True)
        for s in ("trace.py", "needs.py", "mdlinks.py", "soup.py"):
            shutil.copy(HERE / s, self.dir / "scripts" / "qms" / s)

    def write(self, rel: str, text: str) -> None:
        p = self.dir / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(textwrap.dedent(text).lstrip("\n"), encoding="utf-8")

    def run(self, script: str, *args: str):
        r = subprocess.run([sys.executable, str(self.dir / "scripts" / "qms" / script), *args],
                           capture_output=True, text=True, cwd=self.dir)
        return r.returncode, r.stdout + r.stderr

    def close(self):
        shutil.rmtree(self.dir, ignore_errors=True)


SRS = """
| ID | Requirement | Status | Risk control for |
|---|---|---|---|
| DCM-SRS-001 | Echo. | Active | |
| DCM-SRS-002 | Store. | Active | HAZ-001 |
| DCM-SRS-003 | Future thing. | Draft | |
| DCM-SRS-004 | ~~Old~~ replaced. | Retired | |
"""


class TraceTests(unittest.TestCase):
    def setUp(self):
        self.r = Repo()
        self.r.write("docs/dhf/libredcm/SRS.md", SRS)

    def tearDown(self):
        self.r.close()

    def test_all_active_verified_passes(self):
        self.r.write("a/x_test.go", """
            // Verifies: DCM-SRS-001, DCM-SRS-002
            func TestX(t *testing.T) {}
        """)
        rc, out = self.r.run("trace.py", "--check")
        self.assertEqual(rc, 0, out)
        self.assertIn("Active without verification: 0", out)

    def test_untested_active_fails(self):
        self.r.write("a/x_test.go", """
            // Verifies: DCM-SRS-001
            func TestX(t *testing.T) {}
        """)
        rc, out = self.r.run("trace.py", "--check")
        self.assertEqual(rc, 1)
        self.assertIn("untested: DCM-SRS-002", out)

    def test_unknown_and_retired_ids_fail(self):
        self.r.write("a/x_test.go", """
            // Verifies: DCM-SRS-001, DCM-SRS-002, DCM-SRS-099, DCM-SRS-004
            func TestX(t *testing.T) {}
        """)
        rc, out = self.r.run("trace.py", "--check")
        self.assertEqual(rc, 1)
        self.assertIn("unknown ID in tests: DCM-SRS-099", out)
        self.assertIn("retired ID still referenced: DCM-SRS-004", out)

    def test_tag_must_be_directly_above_test(self):
        self.r.write("a/x_test.go", """
            // Verifies: DCM-SRS-001, DCM-SRS-002
            var helper = 1

            func TestX(t *testing.T) {}
        """)
        rc, out = self.r.run("trace.py", "--check")
        self.assertEqual(rc, 1, out)
        self.assertIn("untested: DCM-SRS-001", out)

    def test_fuzz_targets_and_manual_evidence_count(self):
        self.r.write("a/x_test.go", """
            // Verifies: DCM-SRS-001
            func FuzzX(f *testing.F) {}
        """)
        self.r.write("docs/dhf/libredcm/manual-verification.md", """
            - DCM-SRS-002: DCMTK run on the release build
        """)
        rc, out = self.r.run("trace.py", "--check", "--write")
        self.assertEqual(rc, 0, out)
        matrix = (self.r.dir / "docs/dhf/libredcm/generated/trace-matrix.md").read_text()
        self.assertIn("a/x_test.go:FuzzX", matrix)
        self.assertIn("manual: DCMTK run on the release build", matrix)
        self.assertIn("| DCM-SRS-003 | Draft | **NOT VERIFIED** |", matrix)


class NeedsTests(unittest.TestCase):
    def setUp(self):
        self.r = Repo()
        self.r.write("docs/dhf/libredcm/SRS.md", SRS)

    def tearDown(self):
        self.r.close()

    def needs(self, cell1: str, cell2: str):
        self.r.write("docs/dhf/libredcm/intended-use.md", f"""
            | ID | Need | User | SRS requirements |
            |---|---|---|---|
            | UN-01 | a | U1 | {cell1} |
            | UN-02 | b | U1 | {cell2} |
        """)
        return self.r.run("needs.py")

    def test_all_traced_with_shorthand(self):
        rc, out = self.needs("DCM-SRS-001, -002", "DCM-SRS-003")
        self.assertEqual(rc, 0, out)

    def test_unserved_requirement_fails(self):
        rc, out = self.needs("DCM-SRS-001", "DCM-SRS-003")
        self.assertEqual(rc, 1)
        self.assertIn("not traced to a user need: DCM-SRS-002", out)

    def test_retired_or_unknown_citation_fails(self):
        rc, out = self.needs("DCM-SRS-001, -002, -004", "DCM-SRS-003, DCM-SRS-077")
        self.assertEqual(rc, 1)
        self.assertIn("cited but unknown or retired: DCM-SRS-004", out)
        self.assertIn("cited but unknown or retired: DCM-SRS-077", out)


class MdLinksTests(unittest.TestCase):
    def setUp(self):
        self.r = Repo()
        self.r.write("docs/b.md", "# Target heading\n\ntext\n")

    def tearDown(self):
        self.r.close()

    def test_good_links_pass(self):
        self.r.write("docs/a.md", "[b](b.md) [h](b.md#target-heading) [w](https://example.com) [s](#top)\n\n# Top\n")
        rc, out = self.r.run("mdlinks.py", str(self.r.dir))
        self.assertEqual(rc, 0, out)

    def test_missing_file_and_anchor_fail(self):
        self.r.write("docs/a.md", "[x](nope.md) [y](b.md#no-such)\n")
        rc, out = self.r.run("mdlinks.py", str(self.r.dir))
        self.assertEqual(rc, 1)
        self.assertIn("missing nope.md", out)
        self.assertIn("missing anchor b.md#no-such", out)

    def test_links_in_code_blocks_ignored(self):
        self.r.write("docs/a.md", "```\n[x](nope.md)\n```\n")
        rc, out = self.r.run("mdlinks.py", str(self.r.dir))
        self.assertEqual(rc, 0, out)


class SoupTests(unittest.TestCase):
    """soup.py must reproduce the committed soup.md exactly (evaluation
    columns preserved) and report new direct dependencies as TODO."""

    def setUp(self):
        self.r = Repo()
        shutil.copy(REPO / "go.mod", self.r.dir / "go.mod")
        for rel in ("docs/dhf/libredcm/soup.md", "docs/dhf/libredcm/generated/sbom.cdx.json"):
            (self.r.dir / rel).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy(REPO / rel, self.r.dir / rel)

    def tearDown(self):
        self.r.close()

    def test_regeneration_is_identical(self):
        before = (REPO / "docs/dhf/libredcm/soup.md").read_text()
        rc, out = self.r.run("soup.py")
        self.assertEqual(rc, 0, out)
        after = (self.r.dir / "docs/dhf/libredcm/soup.md").read_text()
        self.assertEqual(before, after, "soup.py changed the committed soup.md; run it and commit the result")
        self.assertIn("0 direct without evaluation", out)

    def test_new_dependency_is_flagged(self):
        gm = self.r.dir / "go.mod"
        gm.write_text(gm.read_text().replace("require (\n", "require (\n\texample.com/newdep v1.2.3\n", 1))
        rc, out = self.r.run("soup.py")
        self.assertEqual(rc, 0, out)
        self.assertIn("1 direct without evaluation", out)
        self.assertIn("| `example.com/newdep` | v1.2.3 |", (self.r.dir / "docs/dhf/libredcm/soup.md").read_text())


if __name__ == "__main__":
    unittest.main()
