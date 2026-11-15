# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import sys
import unittest
from pathlib import Path
from typing import Sequence

sys.path.insert(0, str(Path(__file__).parent))

from component_release_notes import ReleaseNotesError, build_component_release_notes  # noqa: E402
from update_release_notes import component_release, update_release_notes  # noqa: E402

REPOSITORY_ROOT = Path(__file__).parents[2]
REPOSITORY = "superdurable/dex"


class FakeGitHub:
    """Answers gh and git commands like a release with body, and records each edit."""

    def __init__(self, body: str, agents_tags: Sequence[str] = ()) -> None:
        self.body = body
        self.agents_tags = agents_tags
        self.edited_notes: list[str] = []

    def run(self, command: Sequence[str]) -> str:
        if tuple(command[:3]) == ("gh", "release", "view"):
            return self.body + "\n"
        if tuple(command[:3]) == ("gh", "release", "edit"):
            self.edited_notes.append(Path(command[command.index("--notes-file") + 1]).read_text(encoding="utf-8"))
            return ""
        if tuple(command[:2]) == ("git", "tag"):
            return "\n".join(self.agents_tags) + "\n"
        raise AssertionError(f"unexpected command {command}")


def current_notes(component: str, version: str, previous_tag: str | None = None) -> str:
    target = {"server": "docs/reference/server/changelog.mdx", "agents": "agents/CHANGELOG.md"}[component]
    changelog = (REPOSITORY_ROOT / target).read_text(encoding="utf-8")
    return build_component_release_notes(component, changelog, version, previous_tag, REPOSITORY)


class UpdateReleaseNotesTest(unittest.TestCase):
    def test_tags_map_to_their_component(self) -> None:
        self.assertEqual(component_release("server-zd/v0.1.32"), ("server", "0.1.32"))
        self.assertEqual(component_release("sdk-go/v1.45.3"), ("go", "1.45.3"))
        self.assertEqual(component_release("sdk-typescript/v1.45.4"), ("typescript", "1.45.4"))
        self.assertEqual(component_release("web/v1.45.5"), ("web", "1.45.5"))
        self.assertEqual(component_release("cli-v1.47.7"), ("cli", "1.47.7"))
        self.assertEqual(component_release("agents/v0.6.11"), ("agents", "0.6.11"))
        self.assertIsNone(component_release("s3local/v0.1.0"))
        self.assertIsNone(component_release("blob-cache-go/v0.1.0"))

    def test_generated_notes_are_replaced(self) -> None:
        github = FakeGitHub("## What's Changed\n* web: a change by @someone")
        message = update_release_notes("server-zd/v0.1.32", REPOSITORY, REPOSITORY_ROOT, github.run)
        self.assertIn("replaced", message)
        self.assertEqual(github.edited_notes, [current_notes("server", "0.1.32")])

    def test_matching_notes_are_left_alone(self) -> None:
        github = FakeGitHub(current_notes("server", "0.1.32").replace("\n", "\r\n"))
        message = update_release_notes("server-zd/v0.1.32", REPOSITORY, REPOSITORY_ROOT, github.run)
        self.assertIn("already has", message)
        self.assertEqual(github.edited_notes, [])

    def test_agents_notes_link_the_previous_stable_tag(self) -> None:
        github = FakeGitHub("", agents_tags=("agents/v0.6.9", "agents/v0.6.10", "agents/v0.6.11", "agents/v0.7.0-rc.1"))
        update_release_notes("agents/v0.6.11", REPOSITORY, REPOSITORY_ROOT, github.run)
        self.assertEqual(github.edited_notes, [current_notes("agents", "0.6.11", "agents/v0.6.10")])

    def test_other_and_prerelease_tags_are_left_alone(self) -> None:
        github = FakeGitHub("anything")
        self.assertIn("not a release of a component", update_release_notes("s3local/v0.1.0", REPOSITORY, REPOSITORY_ROOT, github.run))
        self.assertIn("not a stable release", update_release_notes("sdk-go/v1.46.0-rc.1", REPOSITORY, REPOSITORY_ROOT, github.run))
        self.assertEqual(github.edited_notes, [])

    def test_unrecorded_version_fails_without_editing(self) -> None:
        github = FakeGitHub("anything")
        with self.assertRaisesRegex(ReleaseNotesError, "has no 0.9.0 entry"):
            update_release_notes("server-zd/v0.9.0", REPOSITORY, REPOSITORY_ROOT, github.run)
        self.assertEqual(github.edited_notes, [])


if __name__ == "__main__":
    unittest.main()
