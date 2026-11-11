# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Assembles change-log fragments into releases and checks the pull requests that touch them."""

from __future__ import annotations

import argparse
import datetime
import re
import secrets
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Mapping, Sequence

FRAGMENT_ROOT = "changelog.d"
FRAGMENT_README = f"{FRAGMENT_ROOT}/README.md"
FRAGMENT_PLACEHOLDER = ".gitkeep"
FRAGMENT_NAME = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*-[0-9a-f]{6}\.md$")
FRAGMENT_SUFFIX_BYTES = 3
FRAGMENT_NAME_ATTEMPTS = 16
SLUG_MAX_LENGTH = 40

SKILLS_TARGET = "skills"
SKILLS_VERSION = "skills/VERSION"
SKILLS_SDK_VERSION = "skills/dex-sdk/VERSION"
SKILLS_CHANGELOG = "skills/CHANGELOG.md"
SKILLS_MANIFESTS = (
    "skills/.claude-plugin/plugin.json",
    "skills/.claude-plugin/marketplace.json",
    "skills/.codex-plugin/plugin.json",
    "skills/.cursor-plugin/plugin.json",
    "skills/.cursor-plugin/marketplace.json",
)
SKILLS_OUTPUTS = (SKILLS_VERSION, SKILLS_SDK_VERSION, SKILLS_CHANGELOG, *SKILLS_MANIFESTS)
SKILLS_ARCHIVED_CHANGELOG = "skills/CHANGELOG-0.x.md"
# The mirror publishes the whole skills/ tree, so every file there except release output needs a fragment.
SKILLS_PUBLISHED_PREFIX = "skills/"
SKILLS_RELEASE_NOTE_EXEMPT = frozenset((*SKILLS_OUTPUTS, SKILLS_ARCHIVED_CHANGELOG))
TARGET_OUTPUTS: dict[str, tuple[str, ...]] = {SKILLS_TARGET: SKILLS_OUTPUTS}

BUMP_LEVELS = ("patch", "minor", "major")
SEMVER = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")
RELEASE_DATE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
SKILLS_ENTRY_HEADING = re.compile(r"^## (?P<version>\d+\.\d+\.\d+) - (?P<date>\d{4}-\d{2}-\d{2})\s*$", re.MULTILINE)
CHANGELOG_ENTRY_HEADING = re.compile(r"^## ", re.MULTILINE)
MANIFEST_VERSION = re.compile(r'("version"\s*:\s*")(\d+\.\d+\.\d+)(")')
FRONT_MATTER_LINE = re.compile(r"^(?P<key>[a-z_]+):[ \t]*(?P<value>.*?)\s*$")

RELEASE_PREP_LABEL = "release-prep"
# Pull requests up to this number were opened before fragments and may keep the old flow.
LEGACY_CHANGELOG_PR_MAX = 1608
CORRECTION_LINE = re.compile(r"^\s*(?:[-*]\s+)?changelog:\s*correction:[ \t]*\S", re.IGNORECASE | re.MULTILINE)
SKILLS_NO_BUMP_LINE = re.compile(
    r"^\s*(?:[-*]\s+)?skills-release:\s*no bump needed:[ \t]*\S", re.IGNORECASE | re.MULTILINE
)
# Dex material describes source-available Dex only; reject a downstream product name.
DOWNSTREAM_PRODUCT_NAME = re.compile(r"super[\s_-]*verse", re.IGNORECASE)


class FragmentError(Exception):
    """A fragment, a release file, or a release request is invalid."""


@dataclass(frozen=True)
class Fragment:
    path: str
    front_matter: dict[str, str]
    body: str


@dataclass(frozen=True)
class SkillsRelease:
    version: str
    files: dict[str, str]
    consumed_fragments: tuple[str, ...]


@dataclass(frozen=True)
class FileChange:
    status: str
    path: str


@dataclass(frozen=True)
class PullRequest:
    number: int | None
    body: str
    labels: frozenset[str]


class TreeReader:
    """Reads files of one git tree, or of the working tree when ref is None."""

    def __init__(self, repository: Path, ref: str | None) -> None:
        self.repository = repository
        self.ref = ref

    def read(self, path: str) -> str | None:
        """Returns the file text, or None when the tree has no such file."""
        if self.ref is None:
            file_path = self.repository / path
            return file_path.read_text(encoding="utf-8") if file_path.is_file() else None
        result = run_git(self.repository, "show", f"{self.ref}:{path}", check=False)
        return result.stdout if result.returncode == 0 else None

    def fragment_paths(self, target: str) -> list[str]:
        """Returns the sorted fragment paths of target, excluding the placeholder file."""
        directory = f"{FRAGMENT_ROOT}/{target}"
        if self.ref is None:
            root = self.repository / directory
            names = [entry.name for entry in root.iterdir() if entry.is_file()] if root.is_dir() else []
        else:
            listing = run_git(self.repository, "ls-tree", "--name-only", f"{self.ref}:{directory}", check=False)
            names = listing.stdout.splitlines() if listing.returncode == 0 else []
        return sorted(f"{directory}/{name}" for name in names if name != FRAGMENT_PLACEHOLDER)

    def fragments(self, target: str) -> list[Fragment]:
        fragments = []
        for path in self.fragment_paths(target):
            text = self.read(path)
            if text is None:
                raise FragmentError(f"{path}: cannot read the fragment")
            fragments.append(parse_fragment(path, text))
        return fragments


def main(arguments: Sequence[str] | None = None) -> int:
    parser = build_parser()
    options = parser.parse_args(arguments)
    repository = repository_root()
    try:
        return options.handler(options, repository)
    except FragmentError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    commands = parser.add_subparsers(required=True)

    new = commands.add_parser("new", help="write a new fragment with a unique name")
    new.add_argument("target", choices=sorted(TARGET_OUTPUTS))
    new.add_argument("--bump", choices=BUMP_LEVELS, required=True, help="release level of a skills change")
    new.add_argument("--slug", help="name stem; defaults to the current branch name")
    new.set_defaults(handler=run_new)

    lint = commands.add_parser("lint", help="lint fragments; defaults to every fragment in the working tree")
    lint.add_argument("paths", nargs="*")
    lint.set_defaults(handler=run_lint)

    prepare = commands.add_parser("prepare", help="assemble the fragments of a target into a release")
    prepare.add_argument("target", choices=sorted(TARGET_OUTPUTS))
    prepare.add_argument("--date", required=True, help="release date, YYYY-MM-DD")
    prepare.set_defaults(handler=run_prepare)

    verify = commands.add_parser("verify-release-prep", help="verify a release-prep commit against its base")
    verify.add_argument("--base-ref", required=True)
    verify.add_argument("--head-ref", default="HEAD")
    verify.set_defaults(handler=run_verify_release_prep)

    check = commands.add_parser("check-pr", help="check the release files and fragments of a pull request")
    check.add_argument("--base-ref", required=True, help="the commit the pull request merges into")
    check.add_argument("--head-ref", default="HEAD")
    check.add_argument("--pr-body-file", required=True)
    check.add_argument("--pr-number", type=int)
    check.add_argument("--labels", default="", help="comma-separated pull request labels")
    check.set_defaults(handler=run_check_pr)
    return parser


def run_new(options: argparse.Namespace, repository: Path) -> int:
    stem = slugify(options.slug if options.slug else current_branch(repository))
    path = write_new_fragment(repository / FRAGMENT_ROOT / options.target, stem, options.bump)
    print(path.relative_to(repository).as_posix())
    return 0


def write_new_fragment(directory: Path, stem: str, bump: str) -> Path:
    """Returns the path of a new fragment skeleton, created exclusively so it never overwrites a file."""
    directory.mkdir(parents=True, exist_ok=True)
    for _ in range(FRAGMENT_NAME_ATTEMPTS):
        path = directory / f"{stem}-{secrets.token_hex(FRAGMENT_SUFFIX_BYTES)}.md"
        try:
            with path.open("x", encoding="utf-8") as fragment_file:
                fragment_file.write(f"---\nbump: {bump}\n---\n- \n")
        except FileExistsError:
            continue
        return path
    raise FragmentError(f"{directory}: found no free fragment name for {stem!r} in {FRAGMENT_NAME_ATTEMPTS} attempts")


def run_lint(options: argparse.Namespace, repository: Path) -> int:
    working_tree = TreeReader(repository, None)
    paths = list(options.paths) or [path for target in TARGET_OUTPUTS for path in working_tree.fragment_paths(target)]
    errors = []
    for path in paths:
        text = working_tree.read(path)
        if text is None:
            errors.append(f"{path}: no such file")
            continue
        errors.extend(lint_fragment(path, text))
    return report(errors, f"linted {len(paths)} fragments")


def run_prepare(options: argparse.Namespace, repository: Path) -> int:
    working_tree = TreeReader(repository, None)
    release = assemble_skills_release(read_files(working_tree, SKILLS_OUTPUTS), working_tree.fragments(SKILLS_TARGET), options.date)
    for path, text in release.files.items():
        (repository / path).write_text(text, encoding="utf-8")
    for path in release.consumed_fragments:
        (repository / path).unlink()
    print(f"prepared skills {release.version} from {len(release.consumed_fragments)} fragments")
    return 0


def run_verify_release_prep(options: argparse.Namespace, repository: Path) -> int:
    base = TreeReader(repository, options.base_ref)
    head = TreeReader(repository, options.head_ref)
    changes = changed_files(repository, options.base_ref, options.head_ref)
    return report(verify_release_prep(base, head, changes), "verified the release-prep changes")


def run_check_pr(options: argparse.Namespace, repository: Path) -> int:
    pull_request = PullRequest(
        number=options.pr_number,
        body=Path(options.pr_body_file).read_text(encoding="utf-8"),
        labels=frozenset(label.strip() for label in options.labels.split(",") if label.strip()),
    )
    base = TreeReader(repository, options.base_ref)
    head = TreeReader(repository, options.head_ref)
    changes = changed_files(repository, options.base_ref, options.head_ref)
    return report(check_pull_request(pull_request, base, head, changes), "change-log fragments and release files are valid")


def check_pull_request(
    pull_request: PullRequest, base: TreeReader, head: TreeReader, changes: Sequence[FileChange]
) -> list[str]:
    """Returns every reason the pull request breaks the change-log rules, or no reasons."""
    errors = check_fragment_changes(head, changes)
    if RELEASE_PREP_LABEL in pull_request.labels:
        return errors + verify_release_prep(base, head, changes)

    is_correction = CORRECTION_LINE.search(pull_request.body) is not None
    is_legacy = pull_request.number is not None and pull_request.number <= LEGACY_CHANGELOG_PR_MAX
    output_paths = {path for outputs in TARGET_OUTPUTS.values() for path in outputs}
    changed_outputs = sorted(change.path for change in changes if change.path in output_paths)
    if changed_outputs and not is_correction:
        if is_legacy:
            errors.extend(legacy_skills_bump_problems(base, head, changes))
        else:
            errors.append(
                "only a release-prep pull request changes these release files: "
                + ", ".join(changed_outputs)
                + ". Add a fragment with `python3 script/release/changelog_fragments.py new skills --bump <level>`"
                " instead, or add a line 'changelog: correction: <reason>' to the pull request description"
            )
    changed_base_fragments = sorted(
        change.path for change in changes if change.status in ("M", "D") and is_fragment_path(change.path)
    )
    if changed_base_fragments and not is_correction:
        errors.append(
            "fragments already on the base change only in a release-prep pull request: "
            + ", ".join(changed_base_fragments)
            + ". Add a line 'changelog: correction: <reason>' to the pull request description to fix one"
        )
    errors.extend(check_skills_release_note(pull_request, base, head, changes, is_legacy))
    return errors


def check_fragment_changes(head: TreeReader, changes: Sequence[FileChange]) -> list[str]:
    errors = []
    for change in changes:
        if not change.path.startswith(f"{FRAGMENT_ROOT}/") or change.path == FRAGMENT_README:
            continue
        parts = change.path.split("/")
        if len(parts) != 3 or parts[1] not in TARGET_OUTPUTS:
            errors.append(f"{change.path}: fragments live in {FRAGMENT_ROOT}/<target>/, with target one of {sorted(TARGET_OUTPUTS)}")
            continue
        if parts[2] == FRAGMENT_PLACEHOLDER or change.status == "D":
            continue
        text = head.read(change.path)
        if text is None:
            errors.append(f"{change.path}: cannot read the fragment")
            continue
        errors.extend(lint_fragment(change.path, text))
    return errors


def check_skills_release_note(
    pull_request: PullRequest, base: TreeReader, head: TreeReader, changes: Sequence[FileChange], is_legacy: bool
) -> list[str]:
    is_content_change = any(
        change.path.startswith(SKILLS_PUBLISHED_PREFIX) and change.path not in SKILLS_RELEASE_NOTE_EXEMPT
        for change in changes
    )
    if not is_content_change:
        return []
    if SKILLS_NO_BUMP_LINE.search(pull_request.body):
        return []
    if any(change.status == "A" and is_fragment_path(change.path, SKILLS_TARGET) for change in changes):
        return []
    if is_legacy and not legacy_skills_bump_problems(base, head, changes):
        return []
    return [
        "files under skills/ changed without a skills fragment. Add one with "
        "`python3 script/release/changelog_fragments.py new skills --bump <level>`, or add a line "
        "'skills-release: no bump needed: <reason>' to the pull request description"
    ]


def legacy_skills_bump_problems(base: TreeReader, head: TreeReader, changes: Sequence[FileChange]) -> list[str]:
    """Returns why the pull request does not raise skills VERSION and add its CHANGELOG.md entry, or no reasons."""
    # A strictly higher version keeps a stale legacy pull request from reverting a merged release.
    prefix = f"a pull request numbered up to {LEGACY_CHANGELOG_PR_MAX} that edits skills release files"
    changed_paths = {change.path for change in changes}
    if not {SKILLS_VERSION, SKILLS_CHANGELOG} <= changed_paths:
        return [f"{prefix} must change both {SKILLS_VERSION} and {SKILLS_CHANGELOG}"]
    # The mirror refuses to sync while fragments are pending, so this bump could never publish.
    pending_fragments = sorted(
        {fragment.path for tree in (base, head) for fragment in tree.fragments(SKILLS_TARGET)}
    )
    if pending_fragments:
        return [
            f"{prefix} cannot bump while unreleased skills fragments exist: "
            + ", ".join(pending_fragments)
            + ". Revert the VERSION, manifest, and CHANGELOG.md edits and add a fragment instead"
        ]
    base_text, head_text = base.read(SKILLS_VERSION), head.read(SKILLS_VERSION)
    if base_text is None or head_text is None:
        return [f"{prefix} needs {SKILLS_VERSION} on both the base and the head"]
    try:
        base_version, head_version = base_text.strip(), head_text.strip()
        is_raised = version_tuple(head_version) > version_tuple(base_version)
    except FragmentError as error:
        return [f"{prefix}: {error}"]
    if not is_raised:
        return [
            f"{prefix} must raise {SKILLS_VERSION} above the base's {base_version}, not set {head_version}. "
            "Rebase onto main and add a fragment instead"
        ]
    heading = SKILLS_ENTRY_HEADING.search(head.read(SKILLS_CHANGELOG) or "")
    base_versions = {match.group("version") for match in SKILLS_ENTRY_HEADING.finditer(base.read(SKILLS_CHANGELOG) or "")}
    if heading is None or heading.group("version") != head_version or head_version in base_versions:
        return [f"{prefix} must add a new newest '## {head_version} - YYYY-MM-DD' entry to {SKILLS_CHANGELOG}"]
    return []


def verify_release_prep(base: TreeReader, head: TreeReader, changes: Sequence[FileChange]) -> list[str]:
    """Returns why the changes differ from `prepare` on the base, or no reasons."""
    changed_paths = {change.path for change in changes}
    if not changed_paths & set(SKILLS_OUTPUTS) and not any(is_fragment_path(path) for path in changed_paths):
        return ["a release-prep pull request must change release files; this one changes none"]
    head_changelog = head.read(SKILLS_CHANGELOG)
    heading = SKILLS_ENTRY_HEADING.search(head_changelog or "")
    if heading is None:
        return [f"{SKILLS_CHANGELOG} has no '## X.Y.Z - YYYY-MM-DD' entry"]
    release_date = heading.group("date")
    reprepare = (
        "Prepare it again on a fresh branch from origin/main: "
        f"python3 script/release/changelog_fragments.py prepare skills --date {release_date}"
    )
    try:
        expected = assemble_skills_release(read_files(base, SKILLS_OUTPUTS), base.fragments(SKILLS_TARGET), release_date)
    except FragmentError as error:
        return [f"the base cannot be prepared: {error}"]

    errors = []
    if heading.group("version") != expected.version:
        errors.append(f"the new entry is {heading.group('version')}, but the base fragments release {expected.version}")
    deleted_fragments = {change.path for change in changes if change.status == "D" and is_fragment_path(change.path)}
    missing_deletions = sorted(set(expected.consumed_fragments) - deleted_fragments)
    if missing_deletions:
        errors.append("the base has fragments this release does not consume: " + ", ".join(missing_deletions))
    for path in sorted(expected.files):
        if head.read(path) != expected.files[path]:
            errors.append(f"{path} differs from the prepare output")
    allowed_paths = set(expected.files) | set(expected.consumed_fragments)
    unexpected_paths = sorted(changed_paths - allowed_paths)
    if unexpected_paths:
        errors.append("a release-prep pull request changes only release files and fragments, not: " + ", ".join(unexpected_paths))
    if errors:
        errors.append(reprepare)
    return errors


def assemble_skills_release(files: Mapping[str, str], fragments: Sequence[Fragment], release_date: str) -> SkillsRelease:
    """Returns the skills release files and consumed fragments; the version bumps by the largest fragment bump."""
    validate_release_date(release_date)
    if not fragments:
        raise FragmentError(f"no fragments in {FRAGMENT_ROOT}/{SKILLS_TARGET}; nothing to release")
    ordered = sorted(fragments, key=lambda fragment: fragment.path)
    for fragment in ordered:
        problems = fragment_problems(fragment)
        if problems:
            raise FragmentError("; ".join(problems))
    bump = max((fragment.front_matter["bump"] for fragment in ordered), key=BUMP_LEVELS.index)
    version = bump_version(files[SKILLS_VERSION].strip(), bump)

    entry = f"## {version} - {release_date}\n\n" + "\n".join(fragment.body for fragment in ordered) + "\n"
    release_files = {
        SKILLS_VERSION: f"{version}\n",
        SKILLS_SDK_VERSION: f"{version}\n",
        SKILLS_CHANGELOG: insert_changelog_entry(files[SKILLS_CHANGELOG], entry),
    }
    for path in SKILLS_MANIFESTS:
        release_files[path] = replace_manifest_version(path, files[path], version)
    return SkillsRelease(version, release_files, tuple(fragment.path for fragment in ordered))


def insert_changelog_entry(changelog: str, entry: str) -> str:
    """Returns the change log with entry above its newest entry, or appended when it has none."""
    newest_entry = CHANGELOG_ENTRY_HEADING.search(changelog)
    if newest_entry is None:
        return changelog.rstrip("\n") + "\n\n" + entry
    return changelog[: newest_entry.start()] + entry + "\n" + changelog[newest_entry.start() :]


def replace_manifest_version(path: str, manifest: str, version: str) -> str:
    matches = MANIFEST_VERSION.findall(manifest)
    if len(matches) != 1:
        raise FragmentError(f"{path} must have exactly one version field, found {len(matches)}")
    return MANIFEST_VERSION.sub(lambda match: f"{match.group(1)}{version}{match.group(3)}", manifest)


def lint_fragment(path: str, text: str) -> list[str]:
    """Returns every problem of the fragment at path, or no problems."""
    try:
        fragment = parse_fragment(path, text)
    except FragmentError as error:
        return [str(error)]
    return fragment_problems(fragment)


def fragment_problems(fragment: Fragment) -> list[str]:
    problems = []
    parts = fragment.path.split("/")
    if len(parts) != 3 or parts[0] != FRAGMENT_ROOT or parts[1] not in TARGET_OUTPUTS:
        return [f"{fragment.path}: fragments live in {FRAGMENT_ROOT}/<target>/, with target one of {sorted(TARGET_OUTPUTS)}"]
    if not FRAGMENT_NAME.fullmatch(parts[2]):
        problems.append(
            f"{fragment.path}: name it <slug>-<6 lowercase hex digits>.md; "
            "`python3 script/release/changelog_fragments.py new` picks a unique name"
        )
    keys = set(fragment.front_matter)
    if keys != {"bump"}:
        problems.append(f"{fragment.path}: a skills fragment has exactly the front matter key 'bump', found {sorted(keys)}")
    elif fragment.front_matter["bump"] not in BUMP_LEVELS:
        problems.append(f"{fragment.path}: bump must be one of {', '.join(BUMP_LEVELS)}")
    problems.extend(body_problems(fragment))
    return problems


def body_problems(fragment: Fragment) -> list[str]:
    lines = fragment.body.splitlines()
    if not lines or not lines[0].startswith("- ") or not lines[0][2:].strip():
        return [f"{fragment.path}: the body starts with a non-empty '- ' bullet"]
    problems = []
    for number, line in enumerate(lines, start=1):
        if line.startswith("- "):
            if not line[2:].strip():
                problems.append(f"{fragment.path}: body line {number} is an empty bullet")
        elif not line.startswith("  ") or not line.strip():
            problems.append(f"{fragment.path}: body line {number} must be a '- ' bullet or an indented continuation")
        if line.lstrip().startswith("#"):
            problems.append(f"{fragment.path}: body line {number} is a heading; the release entry adds headings")
    if DOWNSTREAM_PRODUCT_NAME.search(fragment.body):
        problems.append(f"{fragment.path}: Dex material must not describe a product built on Dex")
    return problems


def parse_fragment(path: str, text: str) -> Fragment:
    """Returns the fragment, or raises FragmentError when its front matter is malformed."""
    lines = text.splitlines()
    if not lines or lines[0] != "---":
        raise FragmentError(f"{path}: a fragment starts with a '---' front matter line")
    try:
        end = lines.index("---", 1)
    except ValueError as error:
        raise FragmentError(f"{path}: the front matter has no closing '---' line") from error
    front_matter = {}
    for line in lines[1:end]:
        match = FRONT_MATTER_LINE.fullmatch(line)
        if match is None or match.group("key") in front_matter:
            raise FragmentError(f"{path}: front matter line {line!r} must be a unique 'key: value'")
        front_matter[match.group("key")] = match.group("value")
    body = "\n".join(lines[end + 1 :]).strip("\n")
    return Fragment(path, front_matter, body)


def is_fragment_path(path: str, target: str | None = None) -> bool:
    parts = path.split("/")
    return (
        len(parts) == 3
        and parts[0] == FRAGMENT_ROOT
        and parts[1] in TARGET_OUTPUTS
        and (target is None or parts[1] == target)
        and parts[2] != FRAGMENT_PLACEHOLDER
    )


def bump_version(version: str, bump: str) -> str:
    major, minor, patch = version_tuple(version)
    if bump == "major":
        return f"{major + 1}.0.0"
    if bump == "minor":
        return f"{major}.{minor + 1}.0"
    return f"{major}.{minor}.{patch + 1}"


def version_tuple(version: str) -> tuple[int, int, int]:
    match = SEMVER.fullmatch(version)
    if match is None:
        raise FragmentError(f"version must be stable SemVer: {version!r}")
    return int(match.group(1)), int(match.group(2)), int(match.group(3))


def validate_release_date(release_date: str) -> None:
    if not RELEASE_DATE.fullmatch(release_date):
        raise FragmentError(f"release date must be YYYY-MM-DD: {release_date!r}")
    try:
        datetime.date.fromisoformat(release_date)
    except ValueError as error:
        raise FragmentError(f"release date is not a calendar date: {release_date!r}") from error


def slugify(name: str) -> str:
    stem = re.sub(r"[^a-z0-9]+", "-", name.rsplit("/", 1)[-1].lower()).strip("-")
    stem = stem[:SLUG_MAX_LENGTH].strip("-")
    if not stem:
        raise FragmentError(f"cannot derive a fragment name from {name!r}; pass --slug")
    return stem


def read_files(tree: TreeReader, paths: Sequence[str]) -> dict[str, str]:
    files = {}
    for path in paths:
        text = tree.read(path)
        if text is None:
            raise FragmentError(f"{path} is missing")
        files[path] = text
    return files


def changed_files(repository: Path, base_ref: str, head_ref: str) -> list[FileChange]:
    """Returns the files that differ between the two commits; a type change counts as a modification."""
    output = run_git(repository, "diff", "--name-status", "--no-renames", base_ref, head_ref).stdout
    changes = []
    for line in output.splitlines():
        status, path = line.split("\t", 1)
        changes.append(FileChange("M" if status[0] == "T" else status[0], path))
    return changes


def current_branch(repository: Path) -> str:
    return run_git(repository, "rev-parse", "--abbrev-ref", "HEAD").stdout.strip()


def repository_root() -> Path:
    return Path(run_git(Path.cwd(), "rev-parse", "--show-toplevel").stdout.strip())


def run_git(repository: Path, *arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        ("git", *arguments), cwd=repository, text=True, encoding="utf-8", stdout=subprocess.PIPE, stderr=subprocess.PIPE
    )
    if check and result.returncode != 0:
        raise FragmentError(f"git {' '.join(arguments)} failed: {result.stderr.strip()}")
    return result


def report(errors: Sequence[str], success: str) -> int:
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        return 1
    print(success)
    return 0


if __name__ == "__main__":
    sys.exit(main())
