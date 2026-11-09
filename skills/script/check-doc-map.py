#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

"""Check skills/doc-map.yaml and keep the two copies of the stable layer in step.

Without --base-ref, it validates the map: every mapping names existing files, the
map is not empty, and every skills Markdown file and every docs page in the
doc-map folders is either mapped or listed as unmapped. With --base-ref, it also
fails when a change touches a mapped file on one side but none of its
counterparts, unless the pull request description contains a line
'doc-map: no change needed: <reason>'. Mappings from the base map still count,
so removing an entry does not remove its check in the same change. It runs only
in superdurable/dex, because the skills mirror has no docs.
"""

from __future__ import annotations

import argparse
import fnmatch
import re
import subprocess
from dataclasses import dataclass, field
from pathlib import Path


SKILLS_ROOT = Path(__file__).resolve().parents[1]
REPO_ROOT = SKILLS_ROOT.parent
DOC_MAP = SKILLS_ROOT / "doc-map.yaml"
DOC_MAP_PATH = "skills/doc-map.yaml"
SKILL_DIRECTORIES = ("dex-app-builder/", "dex-sdk/", "dex-connector-contributor/")
# Skills CI triggers on these folders; keep them in step with .github/workflows/skills-ci.yml.
DOCS_DIRECTORIES = ("docs/content/concepts/", "docs/content/components/", "docs/content/design-patterns/")
OPT_OUT = re.compile(r"^\s*(?:[-*]\s+)?doc-map:\s*no change needed:[ \t]*\S", re.IGNORECASE | re.MULTILINE)
SECTION_LINE = re.compile(r"^(mappings|unmapped):\s*$")
SKILL_LINE = re.compile(r"^  - skill:\s*(\S+)\s*$")
DOCS_LINE = re.compile(r"^    docs:\s*$")
DOC_LINE = re.compile(r"^      - (\S+)\s*$")
UNMAPPED_LINE = re.compile(r"^  - (\S+)\s*$")


@dataclass
class DocMapEntry:
    skill: str
    docs: list[str] = field(default_factory=list)
    line_number: int = 0


@dataclass
class DocMap:
    entries: list[DocMapEntry] = field(default_factory=list)
    # Repository paths or fnmatch patterns of files that have no counterpart.
    unmapped: list[str] = field(default_factory=list)


class DocMapFormatError(ValueError):
    """Reports a doc-map line outside the fixed format."""


def fail(message: str) -> None:
    raise SystemExit(message)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-ref", help="compare the changes since this ref against the map")
    parser.add_argument("--pr-body", default="", help="pull request description, for the opt-out marker")
    parser.add_argument("--pr-body-file", help="file holding the pull request description; overrides --pr-body")
    arguments = parser.parse_args()
    pr_body = read_pr_body(arguments.pr_body, arguments.pr_body_file)

    if not (REPO_ROOT / "docs" / "content").is_dir():
        fail("check-doc-map.py runs in superdurable/dex, where docs/content exists")
    doc_map = parse_doc_map_or_fail(DOC_MAP.read_text())
    problems = validate_entries(doc_map.entries, REPO_ROOT)
    problems += find_unclassified_files(doc_map, list_stable_files(REPO_ROOT), REPO_ROOT)
    if problems:
        fail("skills/doc-map.yaml problems:\n" + "\n".join(f"  {problem}" for problem in problems))
    if not arguments.base_ref:
        print(f"validated skills/doc-map.yaml with {len(doc_map.entries)} mappings")
        return

    changed = set(git_output("diff", "--name-only", f"{arguments.base_ref}...HEAD").splitlines())
    base_entries = read_base_entries(arguments.base_ref)
    unpaired = find_unpaired_changes(doc_map.entries + base_entries, changed)
    if not unpaired:
        print("doc-map: every changed mapped file has a changed counterpart")
        return
    report = "\n".join(f"  {path} changed, but none of: {', '.join(counterparts)}" for path, counterparts in unpaired)
    if OPT_OUT.search(pr_body):
        print(f"doc-map: the pull request states 'doc-map: no change needed' for:\n{report}")
        return
    fail(
        "The skills and docs copies of the stable layer must change together.\n"
        f"{report}\n"
        "Update the other side, or add a line 'doc-map: no change needed: <reason>' to the pull request description."
    )


def read_pr_body(pr_body: str, pr_body_file: str | None) -> str:
    """Returns the pull request description, preferring the file that Check Gate fetched at gate time."""
    if pr_body_file is None:
        return pr_body
    return Path(pr_body_file).read_text()


def parse_doc_map_or_fail(text: str) -> DocMap:
    try:
        return parse_doc_map(text)
    except DocMapFormatError as error:
        fail(f"skills/doc-map.yaml: {error}")
        raise


def parse_doc_map(text: str) -> DocMap:
    """Parses the fixed doc-map format: a 'mappings' list of {skill, docs} and an 'unmapped' list."""
    doc_map = DocMap()
    section = ""
    is_in_docs = False
    for line_number, line in enumerate(text.splitlines(), start=1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if match := SECTION_LINE.match(line):
            section = match.group(1)
            is_in_docs = False
        elif section == "mappings" and (match := SKILL_LINE.match(line)):
            doc_map.entries.append(DocMapEntry(skill=match.group(1), line_number=line_number))
            is_in_docs = False
        elif section == "mappings" and DOCS_LINE.match(line) and doc_map.entries:
            is_in_docs = True
        elif section == "mappings" and is_in_docs and (match := DOC_LINE.match(line)):
            doc_map.entries[-1].docs.append(match.group(1))
        elif section == "unmapped" and (match := UNMAPPED_LINE.match(line)):
            doc_map.unmapped.append(match.group(1))
        else:
            raise DocMapFormatError(
                f"line {line_number}: expected 'mappings:', '  - skill: <path>', '    docs:', "
                "'      - <path>', 'unmapped:', or '  - <path>'"
            )
    return doc_map


def validate_entries(entries: list[DocMapEntry], repo_root: Path) -> list[str]:
    if not entries:
        return ["the map has no mappings"]
    problems = []
    seen_skills: set[str] = set()
    for entry in entries:
        location = f"line {entry.line_number} ({entry.skill})"
        if entry.skill in seen_skills:
            problems.append(f"{location}: duplicate skill entry")
        seen_skills.add(entry.skill)
        if not entry.skill.startswith(SKILL_DIRECTORIES) or not (repo_root / "skills" / entry.skill).is_file():
            problems.append(f"{location}: skill must be an existing file under one of {', '.join(SKILL_DIRECTORIES)}")
        if not entry.docs:
            problems.append(f"{location}: needs at least one docs page")
        if len(set(entry.docs)) != len(entry.docs):
            problems.append(f"{location}: lists a docs page twice")
        for doc in entry.docs:
            if not doc.startswith(DOCS_DIRECTORIES) or not (repo_root / doc).is_file():
                problems.append(f"{location}: docs page must be an existing file under {' or '.join(DOCS_DIRECTORIES)}: {doc}")
    return problems


def list_stable_files(repo_root: Path) -> list[str]:
    """Returns the repository paths of every skills Markdown file and every docs page in the doc-map folders."""
    paths = []
    for directory in SKILL_DIRECTORIES:
        paths += [path.relative_to(repo_root).as_posix() for path in (repo_root / "skills" / directory).rglob("*.md")]
    for directory in DOCS_DIRECTORIES:
        paths += [path.relative_to(repo_root).as_posix() for path in (repo_root / directory).rglob("*.md*")]
    return sorted(paths)


def find_unclassified_files(doc_map: DocMap, stable_files: list[str], repo_root: Path) -> list[str]:
    """Returns a problem for each stable file that is neither mapped nor unmapped, and for stale unmapped lines."""
    mapped = {f"skills/{entry.skill}" for entry in doc_map.entries}
    for entry in doc_map.entries:
        mapped.update(entry.docs)
    problems = []
    for path in stable_files:
        is_unmapped = any(fnmatch.fnmatchcase(path, pattern) for pattern in doc_map.unmapped)
        if path in mapped and is_unmapped:
            problems.append(f"{path} is both mapped and unmapped")
        elif path not in mapped and not is_unmapped:
            problems.append(f"{path} needs a mapping, or an 'unmapped' line when it has no counterpart")
    for pattern in doc_map.unmapped:
        if not any(fnmatch.fnmatchcase(path, pattern) for path in stable_files):
            problems.append(f"unmapped '{pattern}' matches no skills file or doc-map docs page")
    return problems


def read_base_entries(base_ref: str) -> list[DocMapEntry]:
    """Returns the base map's mappings, so a removed entry still pairs changes to its files."""
    try:
        text = git_output("show", f"{base_ref}:{DOC_MAP_PATH}")
    except subprocess.CalledProcessError:
        print(f"no {DOC_MAP_PATH} at the base; pairing changes with the current map only")
        return []
    try:
        return parse_doc_map(text).entries
    except DocMapFormatError as error:
        print(f"the base {DOC_MAP_PATH} has another format ({error}); pairing changes with the current map only")
        return []


def find_unpaired_changes(entries: list[DocMapEntry], changed: set[str]) -> list[tuple[str, list[str]]]:
    """Returns each changed mapped file, by repository path, whose counterparts are all unchanged."""
    counterparts: dict[str, set[str]] = {}
    for entry in entries:
        skill = f"skills/{entry.skill}"
        counterparts.setdefault(skill, set()).update(entry.docs)
        for doc in entry.docs:
            counterparts.setdefault(doc, set()).add(skill)
    return [
        (path, sorted(counterparts[path]))
        for path in sorted(changed & counterparts.keys())
        if not counterparts[path] & changed
    ]


def git_output(*arguments: str) -> str:
    return subprocess.run(["git", *arguments], cwd=REPO_ROOT, check=True, capture_output=True, text=True).stdout


if __name__ == "__main__":
    main()
