#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "check_doc_map", Path(__file__).resolve().parents[1] / "script/check-doc-map.py",
)
CHECKER = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = CHECKER
SPEC.loader.exec_module(CHECKER)

DOC_MAP = """# comment
- skill: dex-sdk/references/core/timer.md
  docs:
    - docs/content/primitives/timer.mdx
    - docs/content/design-patterns/durable-timer/index.mdx

- skill: dex-sdk/references/core/patterns.md
  docs:
    - docs/content/design-patterns/durable-timer/index.mdx
"""


class ParseDocMapTest(unittest.TestCase):
    def test_parses_entries(self):
        entries = CHECKER.parse_doc_map(DOC_MAP)
        self.assertEqual([entry.skill for entry in entries], ["dex-sdk/references/core/timer.md", "dex-sdk/references/core/patterns.md"])
        self.assertEqual(len(entries[0].docs), 2)

    def test_rejects_other_shapes(self):
        with self.assertRaises(SystemExit):
            CHECKER.parse_doc_map("- skill: a.md\n  docs: [b.mdx]\n")


class ValidateEntriesTest(unittest.TestCase):
    def test_reports_missing_files_and_duplicates(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "skills/dex-sdk/references/core").mkdir(parents=True)
            (root / "skills/dex-sdk/references/core/timer.md").write_text("timer\n")
            (root / "docs/content/primitives").mkdir(parents=True)
            (root / "docs/content/primitives/timer.mdx").write_text("timer\n")
            entries = CHECKER.parse_doc_map(DOC_MAP + DOC_MAP.split("\n", 1)[1])
            problems = CHECKER.validate_entries(entries, root)
        self.assertTrue(any("durable-timer/index.mdx" in problem for problem in problems))
        self.assertTrue(any("patterns.md" in problem and "existing file" in problem for problem in problems))
        self.assertTrue(any("duplicate skill entry" in problem for problem in problems))


class FindUnpairedChangesTest(unittest.TestCase):
    def setUp(self):
        self.entries = CHECKER.parse_doc_map(DOC_MAP)

    def test_accepts_both_sides_changed(self):
        changed = {"skills/dex-sdk/references/core/timer.md", "docs/content/primitives/timer.mdx"}
        self.assertEqual(CHECKER.find_unpaired_changes(self.entries, changed), [])

    def test_accepts_any_counterpart_of_a_shared_page(self):
        changed = {"docs/content/design-patterns/durable-timer/index.mdx", "skills/dex-sdk/references/core/patterns.md"}
        self.assertEqual(CHECKER.find_unpaired_changes(self.entries, changed), [])

    def test_reports_a_one_sided_change(self):
        unpaired = CHECKER.find_unpaired_changes(self.entries, {"docs/content/primitives/timer.mdx", "README.md"})
        self.assertEqual(unpaired, [("docs/content/primitives/timer.mdx", ["skills/dex-sdk/references/core/timer.md"])])
        unpaired = CHECKER.find_unpaired_changes(self.entries, {"skills/dex-sdk/references/core/timer.md"})
        self.assertEqual(unpaired[0][0], "skills/dex-sdk/references/core/timer.md")

    def test_ignores_unmapped_files(self):
        self.assertEqual(CHECKER.find_unpaired_changes(self.entries, {"skills/dex-sdk/SKILL.md", "docs/content/index.mdx"}), [])

    def test_opt_out_requires_a_reason(self):
        self.assertTrue(CHECKER.OPT_OUT.search("Summary\ndoc-map: no change needed: link fix only\n"))
        self.assertFalse(CHECKER.OPT_OUT.search("doc-map: no change needed:\nnext line\n"))


if __name__ == "__main__":
    unittest.main()
