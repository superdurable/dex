# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from agents_release_notes import (  # noqa: E402
    ReleaseNotesError,
    build_release_notes,
    join_wrapped_lines,
    rewrite_relative_links,
)

SCRIPT = Path(__file__).with_name("agents_release_notes.py")
REPOSITORY_ROOT = SCRIPT.parents[2]
PUBLISHED_NOTES_DIRECTORY = Path(__file__).with_name("testdata") / "agents_release_notes"
CHANGED_COMPONENTS_WORKFLOW = REPOSITORY_ROOT / ".github/workflows/release-changed-components.yml"
BLOB_BASE = "https://github.com/superdurable/dex/blob/agents/v1.0.0/"


class PublishedAgentsReleaseNotesTest(unittest.TestCase):
    """The notes of each published release must match its GitHub release body."""

    def test_published_releases_match_changelog_at_tag(self) -> None:
        for version, previous_tag in (
            ("0.2.0", "agents/v0.1.0"),
            ("0.3.0", "agents/v0.2.0"),
            ("0.4.0", "agents/v0.3.0"),
        ):
            with self.subTest(version=version):
                changelog = subprocess.run(
                    ("git", "show", f"agents/v{version}:agents/CHANGELOG.md"),
                    cwd=REPOSITORY_ROOT,
                    check=True,
                    text=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                ).stdout
                expected = (PUBLISHED_NOTES_DIRECTORY / f"{version}.md").read_text(encoding="utf-8")
                notes = build_release_notes(changelog, version, previous_tag, "superdurable/dex")
                self.assertEqual(notes, expected)


class AgentsReleaseNotesTest(unittest.TestCase):
    def test_builds_section_with_footer(self) -> None:
        changelog = textwrap.dedent(
            """\
            # Changelog

            ## Unreleased

            - Pending.

            ## 1.0.0 (2026-12-01)

            ### Added

            - A wrapped
              item.

            ## 0.9.0 (2026-11-01)

            - Older.
            """
        )
        notes = build_release_notes(changelog, "1.0.0", "agents/v0.9.0", "superdurable/dex")
        self.assertEqual(
            notes,
            "### Added\n\n- A wrapped item.\n\n---\n\n"
            f"Full changelog: [agents/CHANGELOG.md]({BLOB_BASE}agents/CHANGELOG.md). "
            "Changes since 0.9.0: "
            "https://github.com/superdurable/dex/compare/agents/v0.9.0...agents/v1.0.0\n",
        )

    def test_first_release_has_no_compare_link(self) -> None:
        notes = build_release_notes("## 1.0.0\n\nFirst.\n", "1.0.0", None, "superdurable/dex")
        self.assertTrue(notes.endswith(f"[agents/CHANGELOG.md]({BLOB_BASE}agents/CHANGELOG.md).\n"))
        self.assertNotIn("compare", notes)

    def test_version_heading_must_match_exactly(self) -> None:
        changelog = "## 1.0.10 (2026-12-01)\n\n- Ten.\n"
        with self.assertRaises(ReleaseNotesError):
            build_release_notes(changelog, "1.0.1", None, "superdurable/dex")

    def test_empty_section_fails(self) -> None:
        with self.assertRaises(ReleaseNotesError):
            build_release_notes("## 1.0.0\n\n## 0.9.0\n\n- Old.\n", "1.0.0", None, "superdurable/dex")

    def test_rewrites_relative_links_against_tag(self) -> None:
        markdown = textwrap.dedent(
            """\
            See [deploy](../docs/deploy.mdx#connector-tools) and [readme](README.md).
            Root [file](/LICENSE "License"), [site](https://example.com/x), [anchor](#added).
            [reference]: python/core/README.md
            ```text
            [kept](../docs/deploy.mdx)
            ```
            """
        )
        self.assertEqual(
            rewrite_relative_links(markdown, BLOB_BASE),
            textwrap.dedent(
                f"""\
                See [deploy]({BLOB_BASE}docs/deploy.mdx#connector-tools) and [readme]({BLOB_BASE}agents/README.md).
                Root [file]({BLOB_BASE}LICENSE "License"), [site](https://example.com/x), [anchor](#added).
                [reference]: {BLOB_BASE}agents/python/core/README.md
                ```text
                [kept](../docs/deploy.mdx)
                ```
                """
            ),
        )

    def test_link_outside_repository_fails(self) -> None:
        with self.assertRaises(ReleaseNotesError):
            rewrite_relative_links("[x](../../outside.md)", BLOB_BASE)

    def test_joins_paragraph_and_list_continuations(self) -> None:
        markdown = textwrap.dedent(
            """\
            A paragraph
            that wraps.

            - Item one
              continues.
              - Nested item
                continues.
            - Item two.
            1. Numbered
               continues.
            > Quote
            > continues.
            >
            > - Quoted item.
            """
        )
        self.assertEqual(
            join_wrapped_lines(markdown),
            textwrap.dedent(
                """\
                A paragraph that wraps.

                - Item one continues.
                  - Nested item continues.
                - Item two.
                1. Numbered continues.
                > Quote continues.
                >
                > - Quoted item.
                """
            ),
        )

    def test_keeps_block_structure(self) -> None:
        markdown = textwrap.dedent(
            """\
            ### Heading
            Text after heading
            wraps.
            ---
            | Name | Value |
            |---|---|
            | a | b |
            Hard break\\
            next line.

            ```python
            first = 1
            second = 2
            ```
            - Item with fence:
              ```bash
              echo one
              echo two
              ```
              after fence.
            """
        )
        self.assertEqual(
            join_wrapped_lines(markdown),
            textwrap.dedent(
                """\
                ### Heading
                Text after heading wraps.
                ---
                | Name | Value |
                |---|---|
                | a | b |
                Hard break\\
                next line.

                ```python
                first = 1
                second = 2
                ```
                - Item with fence:
                  ```bash
                  echo one
                  echo two
                  ```
                  after fence.
                """
            ),
        )


    def test_keeps_two_space_hard_break(self) -> None:
        markdown = "Two-space break" + " " * 2 + "\nnext line."
        self.assertEqual(join_wrapped_lines(markdown), markdown)


class AgentsReleaseNotesCommandTest(unittest.TestCase):
    def run_command(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ("python3", str(SCRIPT), *arguments),
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def test_writes_notes_file(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            changelog = Path(directory) / "CHANGELOG.md"
            changelog.write_text("## 1.0.0 (2026-12-01)\n\n- One.\n", encoding="utf-8")
            output = Path(directory) / "notes.md"
            result = self.run_command(
                "1.0.0", "--changelog", str(changelog), "--previous-tag", "agents/v0.9.0", "--output", str(output)
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(output.read_text(encoding="utf-8").startswith("- One.\n\n---\n"))

    def test_missing_section_fails(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            changelog = Path(directory) / "CHANGELOG.md"
            changelog.write_text("## 0.9.0\n\n- Old.\n", encoding="utf-8")
            result = self.run_command("1.0.0", "--changelog", str(changelog))
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("no '## 1.0.0' section", result.stderr)

    def test_previous_tag_of_another_component_fails(self) -> None:
        result = self.run_command("1.0.0", "--previous-tag", "server-zd/v0.1.20")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("agents/v*", result.stderr)


class AgentsReleaseWorkflowTest(unittest.TestCase):
    def test_agents_release_uses_changelog_notes(self) -> None:
        workflow = CHANGED_COMPONENTS_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("script/release/agents_release_notes.py", workflow)
        self.assertIn('--title "Dex agents ${agents_version}"', workflow)
        self.assertIn("--latest=false", workflow)


if __name__ == "__main__":
    unittest.main()
