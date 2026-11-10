# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Builds the GitHub release body of a Dex agents release from agents/CHANGELOG.md."""

from __future__ import annotations

import argparse
import posixpath
import re
import sys
from pathlib import Path

AGENTS_TAG_PREFIX = "agents/v"
CHANGELOG_PATH = "agents/CHANGELOG.md"
DEFAULT_REPOSITORY = "superdurable/dex"

FENCE_PATTERN = re.compile(r"^\s*(```|~~~)")
BLOCK_START_PATTERN = re.compile(r"^\s*([-*+]\s|\d+[.)]\s|#|>|\||<|(-\s*){3,}$|(\*\s*){3,}$|(_\s*){3,}$)")
QUOTE_PATTERN = re.compile(r"^\s*>\s?(.*)$")
INLINE_LINK_PATTERN = re.compile(r"\]\(([^()\s]+)((?:\s+\"[^\"]*\")?)\)")
REFERENCE_DEFINITION_PATTERN = re.compile(r"^(\s*\[[^\]]+\]:\s*)(\S+)")
ABSOLUTE_TARGET_PATTERN = re.compile(r"^([a-zA-Z][a-zA-Z0-9+.-]*:|#)")


class ReleaseNotesError(Exception):
    pass


def build_release_notes(
    changelog_text: str, version: str, previous_tag: str | None, repository: str
) -> str:
    tag = f"{AGENTS_TAG_PREFIX}{version}"
    blob_base = f"https://github.com/{repository}/blob/{tag}/"
    section = extract_version_section(changelog_text, version)
    body = join_wrapped_lines(rewrite_relative_links(section, blob_base))
    footer = f"Full changelog: [{CHANGELOG_PATH}]({blob_base}{CHANGELOG_PATH})."
    if previous_tag:
        previous_version = previous_tag.removeprefix(AGENTS_TAG_PREFIX)
        compare_url = f"https://github.com/{repository}/compare/{previous_tag}...{tag}"
        footer += f" Changes since {previous_version}: {compare_url}"
    return f"{body}\n\n---\n\n{footer}\n"


def extract_version_section(changelog_text: str, version: str) -> str:
    lines = changelog_text.splitlines()
    heading_pattern = re.compile(rf"^## {re.escape(version)}(\s|$)")
    start = next((index for index, line in enumerate(lines) if heading_pattern.match(line)), None)
    if start is None:
        raise ReleaseNotesError(f"{CHANGELOG_PATH} has no '## {version}' section")
    end = next(
        (index for index in range(start + 1, len(lines)) if lines[index].startswith("## ")),
        len(lines),
    )
    section = "\n".join(lines[start + 1 : end]).strip()
    if not section:
        raise ReleaseNotesError(f"{CHANGELOG_PATH} section '## {version}' is empty")
    return section


def rewrite_relative_links(markdown: str, blob_base: str) -> str:
    rewritten: list[str] = []
    is_in_fence = False
    for line in markdown.split("\n"):
        if FENCE_PATTERN.match(line):
            is_in_fence = not is_in_fence
        elif not is_in_fence:
            line = INLINE_LINK_PATTERN.sub(
                lambda match: f"]({absolute_link_target(match.group(1), blob_base)}{match.group(2)})",
                line,
            )
            line = REFERENCE_DEFINITION_PATTERN.sub(
                lambda match: f"{match.group(1)}{absolute_link_target(match.group(2), blob_base)}",
                line,
            )
        rewritten.append(line)
    return "\n".join(rewritten)


def absolute_link_target(target: str, blob_base: str) -> str:
    if ABSOLUTE_TARGET_PATTERN.match(target):
        return target
    path, separator, anchor = target.partition("#")
    if path.startswith("/"):
        repository_path = posixpath.normpath(path.lstrip("/"))
    else:
        repository_path = posixpath.normpath(posixpath.join(posixpath.dirname(CHANGELOG_PATH), path))
    if repository_path == ".." or repository_path.startswith("../"):
        raise ReleaseNotesError(f"link target {target} points outside the repository")
    return f"{blob_base}{repository_path}{separator}{anchor}"


# GitHub renders a single newline in a release body as a line break.
def join_wrapped_lines(markdown: str) -> str:
    joined: list[str] = []
    is_in_fence = False
    can_join_previous = False
    for line in markdown.split("\n"):
        if FENCE_PATTERN.match(line):
            is_in_fence = not is_in_fence
            joined.append(line)
            can_join_previous = False
            continue
        if is_in_fence or not line.strip():
            joined.append(line)
            can_join_previous = False
            continue
        quote_continuation = quoted_text_continuation(joined[-1], line) if can_join_previous else None
        if quote_continuation is not None:
            joined[-1] = f"{joined[-1].rstrip()} {quote_continuation}"
        elif can_join_previous and not BLOCK_START_PATTERN.match(line):
            joined[-1] = f"{joined[-1].rstrip()} {line.strip()}"
        else:
            joined.append(line)
        can_join_previous = is_joinable_text_line(joined[-1])
    return "\n".join(joined)


def quoted_text_continuation(previous_line: str, line: str) -> str | None:
    """Returns the text of a wrapped blockquote line that continues previous_line, or None."""
    previous_match = QUOTE_PATTERN.match(previous_line)
    match = QUOTE_PATTERN.match(line)
    if previous_match is None or match is None:
        return None
    previous_text, text = previous_match.group(1), match.group(1)
    if not previous_text.strip() or not text.strip() or BLOCK_START_PATTERN.match(text):
        return None
    if not is_joinable_text_line(previous_text):
        return None
    return text.strip()


def is_joinable_text_line(line: str) -> bool:
    if (quote_match := QUOTE_PATTERN.match(line)) is not None:
        line = quote_match.group(1)
    stripped = line.lstrip()
    if stripped.startswith(("#", "|", "<")) or re.fullmatch(r"([-*_]\s*){3,}", stripped):
        return False
    return not (line.endswith("  ") or line.endswith("\\"))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version", help="Agents version, such as 0.4.0")
    parser.add_argument("--changelog", type=Path, default=Path(CHANGELOG_PATH))
    parser.add_argument("--previous-tag", default="", help="Previous agents/v* tag; empty for the first release")
    parser.add_argument("--repository", default=DEFAULT_REPOSITORY, help="GitHub owner/name")
    parser.add_argument("--output", type=Path, help="Write the notes here instead of stdout")
    arguments = parser.parse_args()

    if arguments.previous_tag and not arguments.previous_tag.startswith(AGENTS_TAG_PREFIX):
        print(f"Previous tag {arguments.previous_tag} is not an {AGENTS_TAG_PREFIX}* tag", file=sys.stderr)
        return 1
    try:
        notes = build_release_notes(
            arguments.changelog.read_text(encoding="utf-8"),
            arguments.version,
            arguments.previous_tag or None,
            arguments.repository,
        )
    except ReleaseNotesError as error:
        print(error, file=sys.stderr)
        return 1
    if arguments.output:
        arguments.output.write_text(notes, encoding="utf-8")
    else:
        sys.stdout.write(notes)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
