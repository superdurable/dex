#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

"""Check skills/doc-map.yaml and keep the two copies of the stable layer in step.

Without --base-ref, it only validates the map. With --base-ref, it also fails
when a change touches a mapped file on one side but none of its counterparts,
unless the pull request description contains a line
'doc-map: no change needed: <reason>'. It runs only in superdurable/dex,
because the skills mirror has no docs.
"""

from __future__ import annotations

import argparse
import re
import subprocess
from dataclasses import dataclass, field
from pathlib import Path


SKILLS_ROOT = Path(__file__).resolve().parents[1]
REPO_ROOT = SKILLS_ROOT.parent
DOC_MAP = SKILLS_ROOT / "doc-map.yaml"
SKILL_DIRECTORIES = ("dex-app-builder/", "dex-sdk/", "dex-connector-contributor/")
OPT_OUT = re.compile(r"^\s*doc-map:\s*no change needed:[ \t]*\S", re.IGNORECASE | re.MULTILINE)
SKILL_LINE = re.compile(r"^- skill:\s*(\S+)\s*$")
DOCS_LINE = re.compile(r"^  docs:\s*$")
DOC_LINE = re.compile(r"^    - (\S+)\s*$")


@dataclass
class DocMapEntry:
    skill: str
    docs: list[str] = field(default_factory=list)
    line_number: int = 0


def fail(message: str) -> None:
    raise SystemExit(message)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-ref", help="compare the changes since this ref against the map")
    parser.add_argument("--pr-body", default="", help="pull request description, for the opt-out marker")
    arguments = parser.parse_args()

    if not (REPO_ROOT / "docs" / "content").is_dir():
        fail("check-doc-map.py runs in superdurable/dex, where docs/content exists")
    entries = parse_doc_map(DOC_MAP.read_text())
    problems = validate_entries(entries, REPO_ROOT)
    if problems:
        fail("skills/doc-map.yaml problems:\n" + "\n".join(f"  {problem}" for problem in problems))
    if not arguments.base_ref:
        print(f"validated skills/doc-map.yaml with {len(entries)} entries")
        return

    changed = set(
        subprocess.run(
            ["git", "diff", "--name-only", f"{arguments.base_ref}...HEAD"],
            cwd=REPO_ROOT,
            check=True,
            capture_output=True,
            text=True,
        ).stdout.splitlines()
    )
    unpaired = find_unpaired_changes(entries, changed)
    if not unpaired:
        print("doc-map: every changed mapped file has a changed counterpart")
        return
    report = "\n".join(f"  {path} changed, but none of: {', '.join(counterparts)}" for path, counterparts in unpaired)
    if OPT_OUT.search(arguments.pr_body):
        print(f"doc-map: the pull request states 'doc-map: no change needed' for:\n{report}")
        return
    fail(
        "The skills and docs copies of the stable layer must change together.\n"
        f"{report}\n"
        "Update the other side, or add a line 'doc-map: no change needed: <reason>' to the pull request description."
    )


def parse_doc_map(text: str) -> list[DocMapEntry]:
    """Parses the fixed doc-map format: a list of {skill: path, docs: [paths]} in block style."""
    entries: list[DocMapEntry] = []
    is_in_docs = False
    for line_number, line in enumerate(text.splitlines(), start=1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if match := SKILL_LINE.match(line):
            entries.append(DocMapEntry(skill=match.group(1), line_number=line_number))
            is_in_docs = False
        elif DOCS_LINE.match(line) and entries:
            is_in_docs = True
        elif (match := DOC_LINE.match(line)) and is_in_docs:
            entries[-1].docs.append(match.group(1))
        else:
            fail(f"skills/doc-map.yaml:{line_number}: expected '- skill: <path>', '  docs:', or '    - <path>'")
    return entries


def validate_entries(entries: list[DocMapEntry], repo_root: Path) -> list[str]:
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
            if not doc.startswith("docs/content/") or not (repo_root / doc).is_file():
                problems.append(f"{location}: docs page must be an existing file under docs/content/: {doc}")
    return problems


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


if __name__ == "__main__":
    main()
