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
mappings:
  - skill: dex-sdk/references/core/timer.md
    docs:
      - docs/content/primitives/timer.mdx
      - docs/content/design-patterns/durable-timer/index.mdx

  - skill: dex-sdk/references/core/patterns.md
    docs:
      - docs/content/design-patterns/durable-timer/index.mdx

unmapped:
  - skills/dex-sdk/SKILL.md
  - skills/dex-sdk/references/go/*.md
"""


def write_files(root: Path, *paths: str) -> None:
    for path in paths:
        (root / path).parent.mkdir(parents=True, exist_ok=True)
        (root / path).write_text("content\n")


class ParseDocMapTest(unittest.TestCase):
    def test_parses_entries_and_unmapped(self):
        doc_map = CHECKER.parse_doc_map(DOC_MAP)
        self.assertEqual(
            [entry.skill for entry in doc_map.entries],
            ["dex-sdk/references/core/timer.md", "dex-sdk/references/core/patterns.md"],
        )
        self.assertEqual(len(doc_map.entries[0].docs), 2)
        self.assertEqual(doc_map.unmapped, ["skills/dex-sdk/SKILL.md", "skills/dex-sdk/references/go/*.md"])

    def test_rejects_other_shapes(self):
        for text in ("mappings:\n  - skill: a.md\n    docs: [b.mdx]\n", "- skill: a.md\n  docs:\n    - b.mdx\n"):
            with self.subTest(text=text), self.assertRaises(CHECKER.DocMapFormatError):
                CHECKER.parse_doc_map(text)


class ValidateEntriesTest(unittest.TestCase):
    def test_rejects_an_empty_map(self):
        with tempfile.TemporaryDirectory() as directory:
            self.assertEqual(CHECKER.validate_entries(CHECKER.parse_doc_map("").entries, Path(directory)), ["the map has no mappings"])

    def test_reports_missing_files_and_duplicates(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write_files(root, "skills/dex-sdk/references/core/timer.md", "docs/content/primitives/timer.mdx")
            text = DOC_MAP.split("\nunmapped:")[0]
            entries = CHECKER.parse_doc_map(text + text.split("mappings:\n", 1)[1]).entries
            problems = CHECKER.validate_entries(entries, root)
        self.assertTrue(any("durable-timer/index.mdx" in problem for problem in problems))
        self.assertTrue(any("patterns.md" in problem and "existing file" in problem for problem in problems))
        self.assertTrue(any("duplicate skill entry" in problem for problem in problems))

    def test_rejects_docs_outside_the_mapped_folders(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write_files(root, "skills/dex-sdk/SKILL.md", "docs/content/reference/index.mdx")
            entries = CHECKER.parse_doc_map(
                "mappings:\n  - skill: dex-sdk/SKILL.md\n    docs:\n      - docs/content/reference/index.mdx\n"
            ).entries
            problems = CHECKER.validate_entries(entries, root)
        self.assertEqual(len(problems), 1)


class ClassificationTest(unittest.TestCase):
    def setUp(self):
        self.doc_map = CHECKER.parse_doc_map(DOC_MAP)
        self.classified = [
            "skills/dex-sdk/SKILL.md",
            "skills/dex-sdk/references/go/go.md",
            "skills/dex-sdk/references/core/timer.md",
            "skills/dex-sdk/references/core/patterns.md",
            "docs/content/primitives/timer.mdx",
            "docs/content/design-patterns/durable-timer/index.mdx",
        ]

    def test_accepts_mapped_and_unmapped_files(self):
        self.assertEqual(CHECKER.find_unclassified_files(self.doc_map, self.classified, Path(".")), [])

    def test_requires_a_decision_for_a_new_file(self):
        for path in ("skills/dex-sdk/references/core/new-primitive.md", "docs/content/primitives/new-primitive.mdx"):
            with self.subTest(path=path):
                problems = CHECKER.find_unclassified_files(self.doc_map, [*self.classified, path], Path("."))
                self.assertEqual(len(problems), 1)
                self.assertIn(path, problems[0])

    def test_rejects_a_stale_unmapped_line(self):
        problems = CHECKER.find_unclassified_files(self.doc_map, self.classified[2:], Path("."))
        self.assertEqual(len(problems), 2)

    def test_lists_the_real_stable_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write_files(root, "skills/dex-sdk/SKILL.md", "skills/dex-sdk/scripts/tool.py", "docs/content/primitives/flow/index.mdx")
            self.assertEqual(
                CHECKER.list_stable_files(root),
                ["docs/content/primitives/flow/index.mdx", "skills/dex-sdk/SKILL.md"],
            )


class PullRequestBodyTest(unittest.TestCase):
    def test_prefers_the_fetched_body_file(self):
        with tempfile.TemporaryDirectory() as directory:
            body_file = Path(directory) / "body.txt"
            body_file.write_text("doc-map: no change needed: wording only\n")
            body = CHECKER.read_pr_body("stale body", str(body_file))
        self.assertTrue(CHECKER.OPT_OUT.search(body))
        self.assertEqual(CHECKER.read_pr_body("inline body", None), "inline body")


class FindUnpairedChangesTest(unittest.TestCase):
    def setUp(self):
        self.entries = CHECKER.parse_doc_map(DOC_MAP).entries

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

    def test_base_mappings_still_pair_after_an_entry_is_removed(self):
        current = self.entries[1:]
        changed = {"skills/dex-sdk/references/core/timer.md", "skills/doc-map.yaml"}
        self.assertEqual(CHECKER.find_unpaired_changes(current, changed), [])
        unpaired = CHECKER.find_unpaired_changes(current + self.entries, changed)
        self.assertEqual(unpaired[0][0], "skills/dex-sdk/references/core/timer.md")

    def test_opt_out_requires_a_reason(self):
        self.assertTrue(CHECKER.OPT_OUT.search("Summary\ndoc-map: no change needed: link fix only\n"))
        self.assertTrue(CHECKER.OPT_OUT.search("* doc-map: no change needed: link fix only\n"))
        self.assertFalse(CHECKER.OPT_OUT.search("doc-map: no change needed:\nnext line\n"))


if __name__ == "__main__":
    unittest.main()
