# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Rewrites the body of a published component release with the notes its change log yields."""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Callable, Sequence

from component_changelogs import TARGETS as CHANGELOG_TARGETS
from component_release_notes import (
    AGENTS_TAG_PREFIX,
    COMPONENT_CHANGELOG_TARGETS,
    DEFAULT_REPOSITORY,
    ReleaseNotesError,
    build_component_release_notes,
    component_changelog_path,
)

STABLE_VERSION = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")
CommandRunner = Callable[[Sequence[str]], str]


def component_release(tag: str) -> tuple[str, str] | None:
    """Returns the component and version that tag releases, or None for a tag of no change-log component."""
    for component, target_name in COMPONENT_CHANGELOG_TARGETS.items():
        prefix = CHANGELOG_TARGETS[target_name].tag_prefix
        if tag.startswith(prefix):
            return component, tag.removeprefix(prefix)
    return None


def is_same_body(current: str, notes: str) -> bool:
    """Returns whether the bodies match, ignoring the line endings and trailing blanks GitHub may change."""
    return current.replace("\r\n", "\n").strip() == notes.strip()


def update_release_notes(tag: str, repository: str, checkout: Path, run_command: CommandRunner) -> str:
    """Returns what happened to the release body of tag; raises ReleaseNotesError when notes cannot be built."""
    release = component_release(tag)
    if release is None:
        return f"{tag} is not a release of a component with a change log; left unchanged"
    component, version = release
    if not STABLE_VERSION.fullmatch(version):
        return f"{tag} is not a stable release; left unchanged"
    changelog = (checkout / component_changelog_path(component)).read_text(encoding="utf-8")
    previous_tag = previous_agents_tag(version, run_command) if component == "agents" else None
    notes = build_component_release_notes(component, changelog, version, previous_tag, repository)
    current = run_command(("gh", "release", "view", tag, "--repo", repository, "--json", "body", "--jq", ".body"))
    if is_same_body(current, notes):
        return f"{tag} already has the notes of its change log"
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", suffix=".md", delete=False) as notes_file:
        notes_file.write(notes)
    run_command(("gh", "release", "edit", tag, "--repo", repository, "--notes-file", notes_file.name))
    return f"{tag}: replaced the release body with the notes of its change log"


def previous_agents_tag(version: str, run_command: CommandRunner) -> str | None:
    """Returns the newest stable agents/v tag older than version, or None for the first release."""
    tags = run_command(("git", "tag", "--list", f"{AGENTS_TAG_PREFIX}*")).split()
    key = version_key(version)
    older = [
        (version_key(tag.removeprefix(AGENTS_TAG_PREFIX)), tag)
        for tag in tags
        if STABLE_VERSION.fullmatch(tag.removeprefix(AGENTS_TAG_PREFIX))
        and version_key(tag.removeprefix(AGENTS_TAG_PREFIX)) < key
    ]
    return max(older)[1] if older else None


def version_key(version: str) -> tuple[int, ...]:
    return tuple(int(part) for part in version.split("."))


def run_command_output(command: Sequence[str]) -> str:
    result = subprocess.run(command, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode != 0:
        raise ReleaseNotesError(f"{' '.join(command[:3])} failed: {result.stderr.strip()}")
    return result.stdout


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag", help="Release tag, such as server-zd/v0.1.33 or sdk-go/v1.45.4")
    parser.add_argument("--repository", default=DEFAULT_REPOSITORY, help="GitHub owner/name")
    arguments = parser.parse_args()
    try:
        print(update_release_notes(arguments.tag, arguments.repository, Path.cwd(), run_command_output))
    except (OSError, ReleaseNotesError) as error:
        print(f"::warning::Release notes of {arguments.tag} were not updated: {error}")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
