# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Writes release entries into the component change logs from pending change-log items."""

from __future__ import annotations

import datetime
import re
from dataclasses import dataclass
from typing import Sequence

STANDARD_SECTIONS = ("Breaking", "Added", "Changed", "Deprecated", "Fixed")
LEGACY_MARKER = "Not released yet"
NO_CHANGES_TEXT = "No user-visible changes."
RELEASES_URL = "https://github.com/superdurable/dex/releases/tag/"

DOCS_STYLE = "docs"
PACKAGES_STYLE = "packages"
ENTRY_START = re.compile(r"^## ", re.MULTILINE)
SECTION_HEADING = re.compile(r"^### (?P<name>.+?)\s*$")
UNRELEASED_HEADING = re.compile(r"^## Unreleased(?: \{#unreleased\})?\s*$")
DOCS_HEADING = re.compile(r"^## (?P<key>\d+\.\d+(?:\.\d+)?) \{#v[0-9-]+\}\s*$")
PACKAGES_HEADING = re.compile(r"^## (?P<key>\d+\.\d+\.\d+) \((?P<date>\d{4}-\d{2}-\d{2})\)\s*$")
PACKAGES_UNRELEASED_HEADING = re.compile(r"^## (?P<key>\d+\.\d+\.\d+) \(not released yet\)\s*$", re.IGNORECASE)
RELEASED_FIRST = re.compile(
    r"^Released (?P<date>\S+) as \[(?P<tag>[^\]]+)\]\((?P<url>[^)\s]+)\)(?P<rest>(?:; .*)?)\.$"
)
RELEASED_LATER = re.compile(
    r"; (?P<version>\d+\.\d+\.\d+) released (?P<date>\S+) as \[(?P<tag>[^\]]+)\]\((?P<url>[^)\s]+)\)"
)
RELEASED_MENTION = re.compile(r"[Rr]eleased (?P<date>\d{4}-\d{2}-\d{2}) as \[(?P<tag>[^\]]+)\]")
VERSION = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")


class ChangelogError(Exception):
    """A component change log or a release request for it is invalid."""


@dataclass(frozen=True)
class ChangelogTarget:
    name: str
    output: str
    tag_prefix: str
    style: str

    def tag(self, version: str) -> str:
        return f"{self.tag_prefix}{version}"


@dataclass(frozen=True)
class PendingItem:
    section: str
    lines: tuple[str, ...]


@dataclass
class Entry:
    heading: str
    preamble: list[str]
    sections: list[tuple[str, list[str]]]


def docs_target(name: str, tag_prefix: str) -> ChangelogTarget:
    return ChangelogTarget(name, f"docs/reference/{name}/changelog.mdx", tag_prefix, DOCS_STYLE)


TARGETS = {
    target.name: target
    for target in (
        docs_target("go", "sdk-go/v"),
        docs_target("java", "sdk-java/v"),
        docs_target("python", "sdk-python/v"),
        docs_target("rust", "sdk-rust/v"),
        docs_target("typescript", "sdk-typescript/v"),
        docs_target("web", "web/v"),
        docs_target("cli", "cli-v"),
        docs_target("server", "server-zd/v"),
        docs_target("agents", "agents/v"),
        ChangelogTarget("agents-packages", "agents/CHANGELOG.md", "agents/v", PACKAGES_STYLE),
    )
}


def assemble_release(
    target: ChangelogTarget,
    changelog: str,
    items: Sequence[PendingItem],
    version: str,
    release_date: str,
    is_empty_allowed: bool,
) -> str:
    """Returns the change log with version released from items and any legacy unreleased entry.

    A version whose entry already exists, such as a patch or a repeated prepare, folds into it.
    Older entries stay byte-identical. Raises ChangelogError when the release does not fit.
    """
    patch = version_parts(version)[2]
    key = release_key(target, version)
    intro, blocks = split_entries(changelog)
    pending = list(items)
    legacy_sections: list[tuple[str, list[str]]] = []
    if blocks and is_legacy_entry(parse_entry(blocks[0])):
        legacy = parse_entry(blocks[0])
        legacy_key = entry_key(target, legacy.heading)
        if legacy_key is not None and legacy_key != key:
            raise ChangelogError(unreleased_mismatch_message(target, legacy_key, version))
        legacy_sections = [(name, lines) for name, lines in legacy.sections if lines]
        blocks = blocks[1:]
    if not pending and not legacy_sections and not is_empty_allowed:
        raise ChangelogError(f"{target.output}: no fragments for {version}; pass --allow-empty to release without changes")

    newest_key = entry_key(target, parse_entry(blocks[0]).heading) if blocks else None
    if newest_key == key:
        pending = [PendingItem(name, tuple(lines)) for name, lines in legacy_sections] + pending
        entry = parse_entry(blocks[0])
        if target.style == DOCS_STYLE:
            entry.preamble = add_release_mention(target, entry.preamble, version, release_date)
            if patch > 0:
                pending = [with_since(item, version) for item in pending]
        if pending:
            entry.preamble = trim_blank_lines([line for line in entry.preamble if line != NO_CHANGES_TEXT])
        entry.sections = merge_sections(entry.sections, pending)
        rest = blocks[1:]
    elif newest_key is None or key_tuple(key) > key_tuple(newest_key):
        entry = new_entry(target, key, version, release_date, legacy_sections, pending)
        rest = blocks
    else:
        raise ChangelogError(f"{target.output}: release {version} is older than the newest entry {newest_key}")
    return intro + render_entry(entry, is_last=not rest) + "".join(rest)


def readiness_errors(target: ChangelogTarget, changelog: str, fragment_count: int, version: str) -> list[str]:
    """Returns why the change log is not ready for the tag of version, or no reasons."""
    errors = []
    if fragment_count:
        errors.append(f"{target.name}: {fragment_count} fragments are not in {target.output} yet")
    _, blocks = split_entries(changelog)
    if not blocks:
        return errors + [f"{target.output} has no entries"]
    entry = parse_entry(blocks[0])
    key = release_key(target, version)
    if is_legacy_entry(entry):
        legacy_key = entry_key(target, entry.heading)
        if legacy_key is not None and legacy_key != key:
            return errors + [unreleased_mismatch_message(target, legacy_key, version)]
        return errors + [f"{target.output}: the newest entry is still unreleased"]
    if entry_key(target, entry.heading) != key:
        return errors + [f"{target.output}: the newest entry is not {key}"]
    if target.style == PACKAGES_STYLE:
        heading = PACKAGES_HEADING.fullmatch(entry.heading)
        if heading is None or heading.group("key") != version or not is_calendar_date(heading.group("date")):
            errors.append(f"{target.output}: the newest entry must be '## {version} (YYYY-MM-DD)'")
        return errors
    return errors + released_line_errors(target, entry, key, version)


def released_line_errors(target: ChangelogTarget, entry: Entry, key: str, version: str) -> list[str]:
    """Returns why the entry's Released line does not record version with a valid date, tag, and URL."""
    lines = [line for line in entry.preamble if line.startswith("Released ")]
    if len(lines) != 1:
        return [f"{target.output}: the {key} entry needs exactly one Released line"]
    mentions = parse_released_line(lines[0])
    if mentions is None:
        return [
            f"{target.output}: the {key} Released line must read 'Released YYYY-MM-DD as [<tag>](<release URL>).', "
            "with each later patch as '; X.Y.Z released YYYY-MM-DD as [<tag>](<release URL>)'"
        ]
    tag = target.tag(version)
    for mention_version, release_date, mention_tag, url in mentions:
        if mention_tag != target.tag(mention_version) or url != release_url(mention_tag) or not is_calendar_date(release_date):
            return [f"{target.output}: the {key} Released line names {mention_tag} with date {release_date} and URL {url}"]
    if tag not in [mention_tag for _, _, mention_tag, _ in mentions]:
        return [f"{target.output}: the {key} entry does not name {tag} on its Released line"]
    return []


def parse_released_line(line: str) -> list[tuple[str, str, str, str]] | None:
    """Returns each recorded release as (version, date, tag, URL), or None when the line is malformed."""
    first = RELEASED_FIRST.fullmatch(line)
    if first is None:
        return None
    mentions = [(tag_version(first.group("tag")), first.group("date"), first.group("tag"), first.group("url"))]
    rest = first.group("rest")
    while rest:
        later = RELEASED_LATER.match(rest)
        if later is None:
            return None
        mentions.append((later.group("version"), later.group("date"), later.group("tag"), later.group("url")))
        rest = rest[later.end() :]
    return mentions


def tag_version(tag: str) -> str:
    return re.sub(r"^.*?v(?=\d)", "", tag, count=1)


def is_calendar_date(text: str) -> bool:
    if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", text):
        return False
    try:
        datetime.date.fromisoformat(text)
    except ValueError:
        return False
    return True


def unreleased_mismatch_message(target: ChangelogTarget, legacy_key: str, version: str) -> str:
    planned = legacy_key if target.style == PACKAGES_STYLE or legacy_key.count(".") == 2 else f"{legacy_key}.0"
    return (
        f"{target.output}: the newest entry is unreleased {legacy_key}, so main ships it as {planned}, not {version}. "
        f"Release {planned} instead, or move that entry's items into fragments first"
    )


def newest_release(target: ChangelogTarget, changelog: str) -> tuple[str, str] | None:
    """Returns the version and date of the latest release that the newest entry records, or None."""
    _, blocks = split_entries(changelog)
    if not blocks:
        return None
    entry = parse_entry(blocks[0])
    if target.style == PACKAGES_STYLE:
        heading = PACKAGES_HEADING.fullmatch(entry.heading)
        return (heading.group("key"), heading.group("date")) if heading else None
    mentions = [mention for line in entry.preamble for mention in RELEASED_MENTION.finditer(line)]
    if not mentions or not mentions[-1].group("tag").startswith(target.tag_prefix):
        return None
    return mentions[-1].group("tag")[len(target.tag_prefix) :], mentions[-1].group("date")


def is_legacy_entry_edit(target: ChangelogTarget, base: str, head: str) -> bool:
    """Returns whether head differs from base only in a newest entry that is still unreleased."""
    head_intro, head_blocks = split_entries(head)
    if not head_blocks or not is_legacy_entry(parse_entry(head_blocks[0])):
        return False
    head_key = entry_key(target, parse_entry(head_blocks[0]).heading)
    base_intro, base_blocks = split_entries(base)
    if base_blocks and is_legacy_entry(parse_entry(base_blocks[0])):
        base_key = entry_key(target, parse_entry(base_blocks[0]).heading)
        if base_key is not None and (head_key is None or key_tuple(head_key) < key_tuple(base_key)):
            return False
        base_blocks = base_blocks[1:]
    if base_blocks and head_key is not None:
        released_key = entry_key(target, parse_entry(base_blocks[0]).heading)
        if released_key is not None and key_tuple(head_key) <= key_tuple(released_key):
            return False
    return head_intro == base_intro and "".join(head_blocks[1:]) == "".join(base_blocks)


def new_entry(
    target: ChangelogTarget,
    key: str,
    version: str,
    release_date: str,
    legacy_sections: list[tuple[str, list[str]]],
    pending: Sequence[PendingItem],
) -> Entry:
    """Returns a new entry that keeps the legacy sections in their order and merges pending into them."""
    if target.style == PACKAGES_STYLE:
        entry = Entry(f"## {version} ({release_date})", [], [])
    else:
        anchor = key.replace(".", "-")
        entry = Entry(f"## {key} {{#v{anchor}}}", [released_line(target, version, release_date)], [])
    if not pending and not legacy_sections:
        entry.preamble += ["", NO_CHANGES_TEXT] if entry.preamble else [NO_CHANGES_TEXT]
    entry.sections = merge_sections(legacy_sections, pending)
    return entry


def add_release_mention(target: ChangelogTarget, preamble: list[str], version: str, release_date: str) -> list[str]:
    tag = target.tag(version)
    for index, line in enumerate(preamble):
        if not line.startswith("Released "):
            continue
        if f"[{tag}](" in line:
            return preamble
        mention = f"; {version} released {release_date} as [{tag}]({release_url(tag)})."
        return preamble[:index] + [line.rstrip().rstrip(".") + mention] + preamble[index + 1 :]
    raise ChangelogError(f"{target.output}: the {release_key(target, version)} entry has no Released line")


def released_line(target: ChangelogTarget, version: str, release_date: str) -> str:
    tag = target.tag(version)
    return f"Released {release_date} as [{tag}]({release_url(tag)})."


def release_url(tag: str) -> str:
    return RELEASES_URL + tag.replace("/", "%2F")


def with_since(item: PendingItem, version: str) -> PendingItem:
    """Returns item with "Since version." after the last prose line of each bullet's own text.

    Sub-bullets and code fence lines never carry the marker: it covers the whole parent bullet,
    and a closing fence cannot hold text.
    """
    lines = list(item.lines)
    last_prose_lines: list[int] = []
    is_in_fence = False
    is_in_sub_bullet = False
    for index, line in enumerate(lines):
        if line.startswith("- "):
            last_prose_lines.append(index)
            is_in_fence = is_in_sub_bullet = False
            continue
        stripped = line.lstrip()
        if not last_prose_lines or not stripped or is_in_sub_bullet:
            continue
        if stripped.startswith("```"):
            is_in_fence = not is_in_fence
        elif is_in_fence:
            continue
        elif line.startswith("  ") and stripped.startswith("- "):
            is_in_sub_bullet = True
        else:
            last_prose_lines[-1] = index
    for index in last_prose_lines:
        lines[index] = f"{lines[index].rstrip()} Since {version}."
    return PendingItem(item.section, tuple(lines))


def merge_sections(
    existing: list[tuple[str, list[str]]], pending: Sequence[PendingItem]
) -> list[tuple[str, list[str]]]:
    """Returns existing sections with pending lines appended; a new section goes to its standard position."""
    sections = [(name, list(lines)) for name, lines in existing]
    first_seen: dict[str, int] = {}
    for item in pending:
        first_seen.setdefault(item.section, len(first_seen))
    pending_names = sorted(first_seen, key=lambda name: (section_rank(name), first_seen[name]))
    for name in pending_names:
        lines = [line for item in pending if item.section == name for line in item.lines]
        match = next((index for index, (existing_name, _) in enumerate(sections) if existing_name == name), None)
        if match is not None:
            sections[match][1].extend(lines)
            continue
        position = next(
            (index for index, (existing_name, _) in enumerate(sections) if section_rank(existing_name) > section_rank(name)),
            len(sections),
        )
        sections.insert(position, (name, lines))
    return sections


def section_rank(name: str) -> int:
    """Returns the sort rank of a section: Breaking, free-form topics, then the other standard sections."""
    if name == STANDARD_SECTIONS[0]:
        return 0
    if name in STANDARD_SECTIONS:
        return STANDARD_SECTIONS.index(name) + 1
    return 1


def split_entries(changelog: str) -> tuple[str, list[str]]:
    """Returns the text before the first entry and each entry block, which ends with its trailing blank lines."""
    starts = [match.start() for match in ENTRY_START.finditer(changelog)]
    if not starts:
        return changelog, []
    bounds = [*starts, len(changelog)]
    return changelog[: starts[0]], [changelog[bounds[index] : bounds[index + 1]] for index in range(len(starts))]


def parse_entry(block: str) -> Entry:
    lines = block.rstrip("\n").split("\n")
    entry = Entry(lines[0], [], [])
    current: list[str] = entry.preamble
    for line in lines[1:]:
        heading = SECTION_HEADING.fullmatch(line)
        if heading:
            current = []
            entry.sections.append((heading.group("name"), current))
        else:
            current.append(line)
    entry.preamble[:] = trim_blank_lines(entry.preamble)
    entry.sections = [(name, trim_blank_lines(lines)) for name, lines in entry.sections]
    return entry


def render_entry(entry: Entry, is_last: bool) -> str:
    parts = [entry.heading]
    if entry.preamble:
        parts += ["", *entry.preamble]
    for name, lines in entry.sections:
        parts += ["", f"### {name}"]
        if lines:
            parts += ["", *lines]
    return "\n".join(parts) + ("\n" if is_last else "\n\n")


def trim_blank_lines(lines: list[str]) -> list[str]:
    start, end = 0, len(lines)
    while start < end and not lines[start].strip():
        start += 1
    while end > start and not lines[end - 1].strip():
        end -= 1
    return lines[start:end]


def is_legacy_entry(entry: Entry) -> bool:
    if UNRELEASED_HEADING.fullmatch(entry.heading) or PACKAGES_UNRELEASED_HEADING.fullmatch(entry.heading):
        return True
    return any(line.startswith(LEGACY_MARKER) for line in entry.preamble)


def entry_key(target: ChangelogTarget, heading: str) -> str | None:
    """Returns the release key that the entry heading names, or None for an unreleased heading."""
    if UNRELEASED_HEADING.fullmatch(heading):
        return None
    unreleased = PACKAGES_UNRELEASED_HEADING.fullmatch(heading)
    if unreleased and target.style == PACKAGES_STYLE:
        return unreleased.group("key")
    pattern = PACKAGES_HEADING if target.style == PACKAGES_STYLE else DOCS_HEADING
    match = pattern.fullmatch(heading)
    if match is None:
        raise ChangelogError(f"{target.output}: unrecognized entry heading {heading!r}")
    return match.group("key")


def release_key(target: ChangelogTarget, version: str) -> str:
    """Returns the entry key of version: each agents package release and server 0.x release has its own entry."""
    major, minor, _ = version_parts(version)
    if target.style == PACKAGES_STYLE or (target.name == "server" and major == 0):
        return version
    return f"{major}.{minor}"


def key_tuple(key: str) -> tuple[int, ...]:
    return tuple(int(part) for part in key.split("."))


def version_parts(version: str) -> tuple[int, int, int]:
    match = VERSION.fullmatch(version)
    if match is None:
        raise ChangelogError(f"release version must be stable SemVer: {version!r}")
    return int(match.group(1)), int(match.group(2)), int(match.group(3))
