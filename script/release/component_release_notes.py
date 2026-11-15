# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Builds the GitHub release body of one Dex component release from that component's change log."""

from __future__ import annotations

import argparse
import posixpath
import re
import sys
from dataclasses import dataclass
from pathlib import Path

from component_changelogs import (
    TARGETS as CHANGELOG_TARGETS,
    ChangelogError,
    ChangelogTarget,
    entry_key,
    RELEASED_MENTION,
    parse_entry,
    release_key,
    split_entries,
)

AGENTS_COMPONENT = "agents"
AGENTS_TAG_PREFIX = "agents/v"
CHANGELOG_PATH = "agents/CHANGELOG.md"
DEFAULT_REPOSITORY = "superdurable/dex"
# Agents notes come from the package change log; every other component from its docs change log.
COMPONENT_CHANGELOG_TARGETS = {
    **{name: name for name in ("go", "java", "python", "rust", "typescript", "web", "cli", "server")},
    AGENTS_COMPONENT: "agents-packages",
}
DOCS_SITE_URL = "https://docs.superdurable.io"
BREAKING_SECTION = "Breaking"
BREAKING_HEADING = "## Breaking Changes"
NO_BREAKING_CHANGES = "None."
NO_CHANGES_TEXT = "No user-visible changes."
OLDER_RELEASES_PREFIX = "Older releases:"

FENCE_PATTERN = re.compile(r"^\s*(```|~~~)")
BLOCK_START_PATTERN = re.compile(r"^\s*([-*+]\s|\d+[.)]\s|#|>|\||<|(-\s*){3,}$|(\*\s*){3,}$|(_\s*){3,}$)")
QUOTE_PATTERN = re.compile(r"^\s*>\s?(.*)$")
INLINE_LINK_PATTERN = re.compile(r"\]\(\s*([^()\s]+)((?:\s+\"[^\"]*\")?)\s*\)")
REFERENCE_DEFINITION_PATTERN = re.compile(r"^(\s*\[[^\]]+\]:\s*)(\S+)")
ABSOLUTE_TARGET_PATTERN = re.compile(r"^([a-zA-Z][a-zA-Z0-9+.-]*:|#)")
DOCS_PATHNAME_PREFIX = "pathname://"
HEADING_ANCHOR = re.compile(r"\{#(?P<anchor>[^}]+)\}\s*$")
SINCE_MARKER = re.compile(r"\bSince (?P<version>\d+\.\d+\.\d+)\.$")


class ReleaseNotesError(Exception):
    pass


@dataclass(frozen=True)
class ChangelogItem:
    """One top-level bullet of a change-log section, with its continuation lines."""

    lines: tuple[str, ...]
    since_version: str | None


def build_component_release_notes(
    component: str, changelog_text: str, version: str, previous_tag: str | None, repository: str
) -> str:
    """Returns the release body of version, built only from the component's own change log."""
    if component == AGENTS_COMPONENT:
        return build_release_notes(changelog_text, version, previous_tag, repository)
    if previous_tag:
        raise ReleaseNotesError(f"--previous-tag applies only to {AGENTS_COMPONENT} releases")
    return build_docs_release_notes(CHANGELOG_TARGETS[COMPONENT_CHANGELOG_TARGETS[component]], changelog_text, version)


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


def build_docs_release_notes(target: ChangelogTarget, changelog_text: str, version: str) -> str:
    """Returns the notes of exactly the items that version shipped, from a docs change log entry.

    A patch folded into its minor's entry contributes only the items marked "Since <version>.".
    """
    try:
        key = release_key(target, version)
        _, blocks = split_entries(changelog_text)
        entries = [parse_entry(block) for block in blocks]
        entry = next((entry for entry in entries if entry_key(target, entry.heading) == key), None)
    except ChangelogError as error:
        raise ReleaseNotesError(str(error)) from error
    if entry is None:
        raise ReleaseNotesError(f"{target.output} has no {key} entry")
    first_version = released_versions(target, entry.preamble, version)[0]
    is_first_release = version == first_version

    paragraphs: list[str] = []
    if is_first_release:
        prose = [line for line in entry.preamble if line and not line.startswith("Released ") and line != NO_CHANGES_TEXT]
        if prose:
            paragraphs.append("\n".join(prose))
    breaking_items: list[ChangelogItem] = []
    sections: list[tuple[str, list[ChangelogItem]]] = []
    for name, lines in entry.sections:
        selected = [
            item
            for item in split_items(target, name, lines)
            if item.since_version == version or (is_first_release and item.since_version is None)
        ]
        if name == BREAKING_SECTION:
            breaking_items.extend(selected)
        elif selected:
            sections.append((name, selected))

    parts = [BREAKING_HEADING, render_items(breaking_items) if breaking_items else NO_BREAKING_CHANGES]
    if not sections and not paragraphs and not breaking_items:
        parts.append(NO_CHANGES_TEXT)
    parts.extend(paragraphs)
    for name, items in sections:
        parts.extend((f"## {name}", render_items(items)))
    anchor = HEADING_ANCHOR.search(entry.heading)
    if anchor is None:
        raise ReleaseNotesError(f"{target.output}: the {key} heading has no {{#anchor}}")
    changelog_url = f"{DOCS_SITE_URL}/{posixpath.dirname(target.output).removeprefix('docs/')}/changelog"
    # Joining first keeps a link destination that a soft wrap split in one line.
    body = rewrite_docs_links(join_wrapped_lines("\n\n".join(parts)), changelog_url)
    return f"{body}\n\nFull change log: {changelog_url}#{anchor.group('anchor')}\n"


def released_versions(target: ChangelogTarget, preamble: list[str], version: str) -> list[str]:
    """Returns the versions the entry's Released line records, oldest first; fails unless version is one."""
    released_lines = [line for line in preamble if line.startswith("Released ")]
    if len(released_lines) != 1:
        raise ReleaseNotesError(f"{target.output}: the {release_key(target, version)} entry needs exactly one Released line")
    versions = [
        mention.group("tag").removeprefix(target.tag_prefix)
        for mention in RELEASED_MENTION.finditer(released_lines[0])
        if mention.group("tag").startswith(target.tag_prefix)
    ]
    if version not in versions:
        raise ReleaseNotesError(
            f"{target.output} does not record {target.tag(version)} yet. Merge its release-prep pull request first: "
            f"python3 script/release/changelog_fragments.py prepare {target.name} --version {version} --date <YYYY-MM-DD>"
        )
    return versions


def split_items(target: ChangelogTarget, section: str, lines: list[str]) -> list[ChangelogItem]:
    """Returns the section's top-level bullets; the page's trailing "Older releases" line is not an item."""
    groups: list[list[str]] = []
    for line in lines:
        if line.startswith("- "):
            groups.append([line])
        elif line.startswith(OLDER_RELEASES_PREFIX):
            continue
        elif not line.strip() or line.startswith(" "):
            if groups:
                groups[-1].append(line)
            elif line.strip():
                raise ReleaseNotesError(f"{target.output}: section {section} starts with an indented line: {line!r}")
        else:
            raise ReleaseNotesError(f"{target.output}: section {section} has a line that is not in a bullet: {line!r}")
    return [ChangelogItem(tuple(trim_trailing_blank_lines(group)), since_marker(group)) for group in groups]


def since_marker(lines: list[str]) -> str | None:
    """Returns the version of the bullet's "Since X.Y.Z." marker, ignoring sub-bullets and code fences."""
    markers = [
        match.group("version")
        for paragraph in own_paragraphs(lines)
        if (match := SINCE_MARKER.search(paragraph)) is not None
    ]
    return markers[-1] if markers else None


def own_paragraphs(lines: list[str]) -> list[str]:
    """Returns the bullet's own prose, with soft-wrapped lines joined, before any sub-bullet and outside fences."""
    paragraphs: list[list[str]] = [[]]
    is_in_fence = False
    for index, line in enumerate(lines):
        stripped = line.strip().removeprefix("- ") if index == 0 else line.strip()
        if index > 0 and line.strip().startswith("- ") and not is_in_fence:
            break
        if FENCE_PATTERN.match(line):
            is_in_fence = not is_in_fence
            paragraphs.append([])
            continue
        if is_in_fence or not stripped:
            if paragraphs[-1]:
                paragraphs.append([])
            continue
        paragraphs[-1].append(stripped)
    return [" ".join(paragraph) for paragraph in paragraphs if paragraph]


def trim_trailing_blank_lines(lines: list[str]) -> list[str]:
    end = len(lines)
    while end > 0 and not lines[end - 1].strip():
        end -= 1
    return lines[:end]


def render_items(items: list[ChangelogItem]) -> str:
    return "\n".join(line for item in items for line in item.lines)


def rewrite_docs_links(markdown: str, changelog_url: str) -> str:
    """Returns markdown with docs-site links made absolute, so they work on GitHub."""
    rewritten: list[str] = []
    is_in_fence = False
    for line in markdown.split("\n"):
        if FENCE_PATTERN.match(line):
            is_in_fence = not is_in_fence
        elif not is_in_fence:
            line = INLINE_LINK_PATTERN.sub(
                lambda match: f"]({absolute_docs_target(match.group(1), changelog_url)}{match.group(2)})", line
            )
        rewritten.append(line)
    return "\n".join(rewritten)


def absolute_docs_target(target: str, changelog_url: str) -> str:
    if target.startswith(DOCS_PATHNAME_PREFIX):
        target = target.removeprefix(DOCS_PATHNAME_PREFIX)
    elif target.startswith("#"):
        return f"{changelog_url}{target}"
    elif ABSOLUTE_TARGET_PATTERN.match(target):
        return target
    if not target.startswith("/"):
        raise ReleaseNotesError(f"link target {target} is relative; docs change logs link from the site root")
    return f"{DOCS_SITE_URL}{target}"


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


def component_changelog_path(component: str) -> str:
    return CHANGELOG_TARGETS[COMPONENT_CHANGELOG_TARGETS[component]].output


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("component", choices=sorted(COMPONENT_CHANGELOG_TARGETS))
    parser.add_argument("version", help="Component version, such as 1.45.2 or 0.6.0")
    parser.add_argument("--changelog", type=Path, help="Change log to read; defaults to the component's own")
    parser.add_argument("--previous-tag", default="", help="Agents only: the previous agents/v* tag")
    parser.add_argument("--repository", default=DEFAULT_REPOSITORY, help="GitHub owner/name")
    parser.add_argument("--output", type=Path, help="Write the notes here instead of stdout")
    arguments = parser.parse_args()

    if arguments.previous_tag and not arguments.previous_tag.startswith(AGENTS_TAG_PREFIX):
        print(f"Previous tag {arguments.previous_tag} is not an {AGENTS_TAG_PREFIX}* tag", file=sys.stderr)
        return 1
    changelog = arguments.changelog or Path(component_changelog_path(arguments.component))
    try:
        notes = build_component_release_notes(
            arguments.component,
            changelog.read_text(encoding="utf-8"),
            arguments.version,
            arguments.previous_tag or None,
            arguments.repository,
        )
    except (OSError, ReleaseNotesError) as error:
        print(error, file=sys.stderr)
        return 1
    if arguments.output:
        arguments.output.write_text(notes, encoding="utf-8")
    else:
        sys.stdout.write(notes)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
