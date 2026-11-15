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

from component_changelogs import TARGETS, RELEASED_MENTION, parse_entry, split_entries  # noqa: E402
from component_release_notes import (  # noqa: E402
    COMPONENT_CHANGELOG_TARGETS,
    ReleaseNotesError,
    build_component_release_notes,
    build_release_notes,
    join_wrapped_lines,
    rewrite_relative_links,
    split_items,
)

SCRIPT = Path(__file__).with_name("component_release_notes.py")
REPOSITORY_ROOT = SCRIPT.parents[2]
PUBLISHED_NOTES_DIRECTORY = Path(__file__).with_name("testdata") / "agents_release_notes"
CHANGED_COMPONENTS_WORKFLOW = REPOSITORY_ROOT / ".github/workflows/release-changed-components.yml"
CLI_RELEASE_WORKFLOW = REPOSITORY_ROOT / ".github/workflows/cli-release.yml"
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
                "agents",
                "1.0.0",
                "--changelog",
                str(changelog),
                "--previous-tag",
                "agents/v0.9.0",
                "--output",
                str(output),
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertTrue(output.read_text(encoding="utf-8").startswith("- One.\n\n---\n"))

    def test_missing_section_fails(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            changelog = Path(directory) / "CHANGELOG.md"
            changelog.write_text("## 0.9.0\n\n- Old.\n", encoding="utf-8")
            result = self.run_command("agents", "1.0.0", "--changelog", str(changelog))
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("no '## 1.0.0' section", result.stderr)

    def test_previous_tag_of_another_component_fails(self) -> None:
        result = self.run_command("agents", "1.0.0", "--previous-tag", "server-zd/v0.1.20")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("agents/v*", result.stderr)


class ReleaseWorkflowTest(unittest.TestCase):
    def test_every_release_uses_its_own_changelog_notes(self) -> None:
        workflow = CHANGED_COMPONENTS_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("script/release/component_release_notes.py", workflow)
        self.assertNotIn("generate-notes", workflow)
        self.assertNotIn("None.", workflow)
        self.assertIn('--title "Dex agents ${version}"', workflow)
        self.assertIn("--latest=false", workflow)
        for component in ("go", "rust", "java", "python", "typescript", "web", "cli", "agents"):
            with self.subTest(component=component):
                self.assertIn(f'"{component}:', workflow)

    def test_cli_release_replaces_notes_without_breaking_section(self) -> None:
        workflow = CLI_RELEASE_WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("grep -qx '## Breaking Changes'", workflow)
        self.assertIn("script/release/component_release_notes.py cli", workflow)
        self.assertNotIn("None.", workflow)


def changelog_at_tag(tag: str, component: str) -> str:
    return subprocess.run(
        ("git", "show", f"{tag}:{TARGETS[COMPONENT_CHANGELOG_TARGETS[component]].output}"),
        cwd=REPOSITORY_ROOT,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    ).stdout


def docs_notes(component: str, tag: str, version: str) -> str:
    return build_component_release_notes(component, changelog_at_tag(tag, component), version, None, "superdurable/dex")


def section_of(notes: str, heading: str) -> str:
    """Returns the text under the H2 heading, up to the next H2 heading or the footer."""
    start = notes.index(f"## {heading}\n")
    rest = notes[start + len(heading) + 4 :]
    ends = [index for index in (rest.find("\n## "), rest.find("\nFull change log: ")) if index >= 0]
    return rest[: min(ends)].strip()


class DocsChangelogReleaseNotesTest(unittest.TestCase):
    """Each component's notes hold exactly the items of that version, read from the change log at a release tag."""

    def test_go_minor_excludes_items_of_later_patches(self) -> None:
        notes = docs_notes("go", "sdk-go/v1.45.3", "1.45.0")
        self.assertIn("**WorkerOptions.FlowDefinitionPaths**", section_of(notes, "Added"))
        self.assertIn('A Step whose Step type contains "-"', section_of(notes, "Breaking Changes"))
        self.assertNotIn("None.", notes)
        self.assertIn("The **Join** documentation", section_of(notes, "Fixed"))
        self.assertNotIn("Since 1.45.", notes)
        self.assertNotIn("**ClientOnlyFlow**", notes)
        self.assertNotIn("**RPCInvokeOptions.RequestID**", notes)
        self.assertTrue(notes.endswith("\n\nFull change log: https://docs.superdurable.io/reference/go/changelog#v1-45\n"))

    def test_go_patch_lists_only_its_own_items(self) -> None:
        notes = docs_notes("go", "sdk-go/v1.45.3", "1.45.2")
        self.assertEqual(section_of(notes, "Breaking Changes"), "None.")
        added = section_of(notes, "Added")
        self.assertIn("**ClientOnlyFlow**", added)
        self.assertIn("Named task queues give one Flow type separate Worker pools.", added)
        self.assertNotIn("**RPCInvokeOptions.RequestID**", added)
        self.assertNotIn("**WorkerOptions.FlowDefinitionPaths**", added)
        self.assertNotIn("## Fixed", notes)

        notes = docs_notes("go", "sdk-go/v1.45.3", "1.45.3")
        self.assertEqual(section_of(notes, "Added").count("\n- "), 0)
        self.assertIn("**RPCInvokeOptions.RequestID**", section_of(notes, "Added"))
        self.assertIn(
            "[API reference](https://docs.superdurable.io/api/go/latest/dex/#type-rpcinvokeoptions). Since 1.45.3.", notes
        )

    def test_java_python_rust_typescript_patches(self) -> None:
        cases = (
            ("java", "sdk-java/v1.45.1", "1.45.1", "**PullWorkerOptions.Builder.taskQueue**", "**WorkerOptions"),
            ("python", "sdk-python/v1.45.2", "1.45.1", "Named task queues.", "**Client.get_flow_state**"),
            ("rust", "sdk-rust/v1.45.1", "1.45.1", "**PullWorkerOptions::task_queue**", "**Client::upload_flow_definitions**"),
            ("typescript", "sdk-typescript/v1.45.4", "1.45.0", "**Client.uploadFlowDefinitions**", "**FlowState.taskQueue**"),
        )
        for component, tag, version, included, excluded in cases:
            with self.subTest(component=component, version=version):
                notes = docs_notes(component, tag, version)
                self.assertIn(included, notes)
                self.assertNotIn(excluded, notes)
                self.assertTrue(notes.endswith(f"/reference/{component}/changelog#v1-45\n"))

    def test_python_patch_joins_wrapped_items(self) -> None:
        added = section_of(docs_notes("python", "sdk-python/v1.45.2", "1.45.1"), "Added")
        self.assertEqual(added.count("\n"), 1)
        self.assertIn(
            "[Named task queues](https://docs.superdurable.io/reference/python/workers#named-task-queues). Since 1.45.1.",
            added,
        )

    def test_web_patch_lists_only_its_own_items(self) -> None:
        notes = docs_notes("web", "web/v1.45.5", "1.45.5")
        self.assertEqual(section_of(notes, "Breaking Changes"), "None.")
        changed = section_of(notes, "Changed")
        self.assertIn("**Connectors** has a calmer, borderless layout.", changed)
        self.assertIn("**Configuration**", changed)
        self.assertNotIn("Since 1.45.4.", notes)
        self.assertNotIn("## Added", notes)
        self.assertTrue(all(line.startswith("- ") for line in changed.splitlines()))

    def test_web_minor_keeps_breaking_items(self) -> None:
        notes = docs_notes("web", "web/v1.45.5", "1.45.0")
        breaking = section_of(notes, "Breaking Changes")
        self.assertIn("Each Flow type has its own definition revision.", breaking)
        self.assertIn("**GET /api/flow-definitions** lists definitions without their graphs.", breaking)
        self.assertNotIn("Since 1.45.", notes)

    def test_cli_lists_its_own_items_only(self) -> None:
        notes = docs_notes("cli", "cli-v1.47.7", "1.47.7")
        self.assertIn("**dexcli connectors mcp**", section_of(notes, "Added"))
        self.assertNotIn("## Changed", notes)
        notes = docs_notes("cli", "cli-v1.47.7", "1.47.0")
        self.assertIn("**/model** without an ID", section_of(notes, "Added"))
        self.assertNotIn("Since 1.47.", notes)
        self.assertNotIn("## Changed", notes)

    def test_server_release_has_its_own_entry(self) -> None:
        notes = docs_notes("server", "server-zd/v0.1.32", "0.1.32")
        self.assertEqual(section_of(notes, "Breaking Changes"), "None.")
        self.assertIn("A busy pull Worker stream", section_of(notes, "Fixed"))
        self.assertNotIn("tencentCOS", notes)
        self.assertTrue(notes.endswith("Full change log: https://docs.superdurable.io/reference/server/changelog#v0-1-32\n"))
        self.assertIn("**objectStore.provider: tencentCOS**", docs_notes("server", "server-zd/v0.1.32", "0.1.31"))

    def test_unrecorded_version_fails(self) -> None:
        with self.assertRaisesRegex(ReleaseNotesError, "does not record sdk-go/v1.45.4 yet"):
            docs_notes("go", "sdk-go/v1.45.3", "1.45.4")
        with self.assertRaisesRegex(ReleaseNotesError, "has no 1.46 entry"):
            docs_notes("go", "sdk-go/v1.45.3", "1.46.0")

    def test_previous_tag_is_agents_only(self) -> None:
        with self.assertRaisesRegex(ReleaseNotesError, "only to agents"):
            build_component_release_notes("go", "", "1.0.0", "sdk-go/v0.9.0", "superdurable/dex")

    def test_release_without_items_says_so(self) -> None:
        changelog = textwrap.dedent(
            """\
            # Change log

            ## 1.2 {#v1-2}

            Released 2026-12-01 as [sdk-go/v1.2.0](https://github.com/superdurable/dex/releases/tag/sdk-go%2Fv1.2.0); 1.2.1 released 2026-12-02 as [sdk-go/v1.2.1](https://github.com/superdurable/dex/releases/tag/sdk-go%2Fv1.2.1).

            ### Breaking

            - Old item.
            - Newer item that wraps
              onto a second line. Since 1.2.1.
              - A sub-bullet. Since 1.2.0.

            ### Added

            - Relative [link](#v1-2) and [page](/reference/go/).
            """
        )
        notes = build_component_release_notes("go", changelog, "1.2.1", None, "superdurable/dex")
        self.assertEqual(
            notes,
            "## Breaking Changes\n\n"
            "- Newer item that wraps onto a second line. Since 1.2.1.\n"
            "  - A sub-bullet. Since 1.2.0.\n\n"
            "Full change log: https://docs.superdurable.io/reference/go/changelog#v1-2\n",
        )
        notes = build_component_release_notes("go", changelog, "1.2.0", None, "superdurable/dex")
        self.assertIn(
            "## Added\n\n- Relative [link](https://docs.superdurable.io/reference/go/changelog#v1-2) and "
            "[page](https://docs.superdurable.io/reference/go/).",
            notes,
        )
        empty = changelog.replace("### Breaking\n\n- Old item.\n", "### Breaking\n\n")
        empty = empty[: empty.index("### Added")]
        notes = build_component_release_notes("go", empty, "1.2.0", None, "superdurable/dex")
        self.assertEqual(
            notes,
            "## Breaking Changes\n\nNone.\n\nNo user-visible changes.\n\n"
            "Full change log: https://docs.superdurable.io/reference/go/changelog#v1-2\n",
        )


class CurrentChangelogPartitionTest(unittest.TestCase):
    """Every item of every current docs change log belongs to exactly one release of its entry."""

    def test_items_partition_by_release(self) -> None:
        for component, target_name in sorted(COMPONENT_CHANGELOG_TARGETS.items()):
            if component == "agents":
                continue
            target = TARGETS[target_name]
            changelog = (REPOSITORY_ROOT / target.output).read_text(encoding="utf-8")
            _, blocks = split_entries(changelog)
            for block in blocks:
                entry = parse_entry(block)
                released_lines = [line for line in entry.preamble if line.startswith("Released ")]
                if not released_lines:
                    continue
                versions = {
                    mention.group("tag").removeprefix(target.tag_prefix)
                    for mention in RELEASED_MENTION.finditer(released_lines[0])
                }
                for name, lines in entry.sections:
                    for item in split_items(target, name, lines):
                        with self.subTest(component=component, entry=entry.heading, item=item.lines[0][:60]):
                            self.assertIn(item.since_version or next(iter(sorted(versions))), versions)
                for version in sorted(versions):
                    with self.subTest(component=component, version=version):
                        build_component_release_notes(component, changelog, version, None, "superdurable/dex")


if __name__ == "__main__":
    unittest.main()
