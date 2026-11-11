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

from component_changelogs import (
    DOCS_STYLE,
    STANDARD_SECTIONS,
    TARGETS as COMPONENT_CHANGELOGS,
    ChangelogError,
    PendingItem,
    assemble_release,
    is_legacy_entry_edit,
    newest_release,
    readiness_errors,
)

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
TARGET_OUTPUTS: dict[str, tuple[str, ...]] = {
    SKILLS_TARGET: SKILLS_OUTPUTS,
    **{name: (target.output,) for name, target in COMPONENT_CHANGELOGS.items()},
}
# A release component is what prepare and check-ready name; agents writes two change logs.
RELEASE_COMPONENTS: dict[str, tuple[str, ...]] = {
    **{name: (name,) for name in COMPONENT_CHANGELOGS if name != "agents-packages"},
    "agents": ("agents", "agents-packages"),
    SKILLS_TARGET: (SKILLS_TARGET,),
}

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
# Docs prose uses bold for identifiers, and MDX treats braces and angle brackets as code.
INLINE_BACKTICK = re.compile(r"`[^`\n]+`")
MDX_UNSAFE = re.compile(r"[{}<]")


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
    except (FragmentError, ChangelogError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    commands = parser.add_subparsers(required=True)

    new = commands.add_parser("new", help="write a new fragment with a unique name")
    new.add_argument("target", choices=sorted(TARGET_OUTPUTS))
    new.add_argument("--bump", choices=BUMP_LEVELS, help="release level of a skills change")
    new.add_argument("--section", help=f"change-log section of a component change: one of {', '.join(STANDARD_SECTIONS)}")
    new.add_argument("--slug", help="name stem; defaults to the current branch name")
    new.set_defaults(handler=run_new)

    lint = commands.add_parser("lint", help="lint fragments; defaults to every fragment in the working tree")
    lint.add_argument("paths", nargs="*")
    lint.set_defaults(handler=run_lint)

    prepare = commands.add_parser("prepare", help="assemble the fragments of release components into a release")
    prepare.add_argument("components", help=f"comma-separated, from {', '.join(sorted(RELEASE_COMPONENTS))}")
    prepare.add_argument("--version", help="release version of the components; skills computes its own")
    prepare.add_argument("--date", required=True, help="release date, YYYY-MM-DD")
    prepare.add_argument("--allow-empty", action="store_true", help="release a component that has no changes")
    prepare.set_defaults(handler=run_prepare)

    ready = commands.add_parser("check-ready", help="fail unless the change logs of a component record its release")
    ready.add_argument("component", choices=sorted(set(RELEASE_COMPONENTS) - {SKILLS_TARGET}))
    ready.add_argument("version")
    ready.set_defaults(handler=run_check_ready)

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
    if options.target == SKILLS_TARGET:
        if options.bump is None or options.section is not None:
            raise FragmentError("a skills fragment takes --bump and no --section")
        front_matter = f"bump: {options.bump}"
    else:
        if options.section is None or options.bump is not None:
            raise FragmentError(f"a {options.target} fragment takes --section and no --bump")
        front_matter = f"section: {options.section}"
    stem = slugify(options.slug if options.slug else current_branch(repository))
    path = write_new_fragment(repository / FRAGMENT_ROOT / options.target, stem, front_matter)
    print(path.relative_to(repository).as_posix())
    return 0


def write_new_fragment(directory: Path, stem: str, front_matter: str) -> Path:
    """Returns the path of a new fragment skeleton, created exclusively so it never overwrites a file."""
    directory.mkdir(parents=True, exist_ok=True)
    for _ in range(FRAGMENT_NAME_ATTEMPTS):
        path = directory / f"{stem}-{secrets.token_hex(FRAGMENT_SUFFIX_BYTES)}.md"
        try:
            with path.open("x", encoding="utf-8") as fragment_file:
                fragment_file.write(f"---\n{front_matter}\n---\n- \n")
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
    components = [component.strip() for component in options.components.split(",") if component.strip()]
    unknown = sorted(set(components) - set(RELEASE_COMPONENTS))
    if unknown or not components:
        raise FragmentError(f"unknown components {unknown}; choose from {', '.join(sorted(RELEASE_COMPONENTS))}")
    files: dict[str, str] = {}
    consumed: list[str] = []
    for component in components:
        if component == SKILLS_TARGET:
            if options.version is not None or options.allow_empty:
                raise FragmentError("skills computes its version from its fragments; pass neither --version nor --allow-empty")
            release = assemble_skills_release(
                read_files(working_tree, SKILLS_OUTPUTS), working_tree.fragments(SKILLS_TARGET), options.date
            )
            files.update(release.files)
            consumed.extend(release.consumed_fragments)
            print(f"prepared skills {release.version} from {len(release.consumed_fragments)} fragments")
            continue
        if options.version is None:
            raise FragmentError(f"{component} needs --version")
        for target in RELEASE_COMPONENTS[component]:
            text, fragments = prepare_component_target(working_tree, target, options.version, options.date, options.allow_empty)
            files[COMPONENT_CHANGELOGS[target].output] = text
            consumed.extend(fragments)
        print(f"prepared {component} {options.version}")
    for path, text in files.items():
        (repository / path).write_text(text, encoding="utf-8")
    for path in consumed:
        (repository / path).unlink()
    return 0


def run_check_ready(options: argparse.Namespace, repository: Path) -> int:
    errors = release_readiness_errors(TreeReader(repository, None), options.component, options.version)
    return report(errors, f"{options.component} {options.version} is recorded in its change logs")


def release_readiness_errors(tree: TreeReader, component: str, version: str) -> list[str]:
    """Returns why the change logs of component do not record version yet, with the prepare command, or no reasons."""
    errors = []
    for target_name in RELEASE_COMPONENTS[component]:
        target = COMPONENT_CHANGELOGS[target_name]
        changelog = tree.read(target.output)
        if changelog is None:
            errors.append(f"{target.output} is missing")
            continue
        errors.extend(readiness_errors(target, changelog, len(tree.fragment_paths(target_name)), version))
    if errors:
        errors.append(
            "Merge a release-prep pull request before the tag. From a fresh branch of origin/main run: "
            f"python3 script/release/changelog_fragments.py prepare {component} --version {version} --date <YYYY-MM-DD>"
        )
    return errors


def prepare_component_target(
    tree: TreeReader, target_name: str, version: str, release_date: str, is_empty_allowed: bool
) -> tuple[str, tuple[str, ...]]:
    """Returns the prepared change log of target_name and the fragment paths it consumes."""
    validate_release_date(release_date)
    target = COMPONENT_CHANGELOGS[target_name]
    changelog = tree.read(target.output)
    if changelog is None:
        raise FragmentError(f"{target.output} is missing")
    fragments = sorted(tree.fragments(target_name), key=lambda fragment: fragment.path)
    for fragment in fragments:
        problems = fragment_problems(fragment)
        if problems:
            raise FragmentError("; ".join(problems))
    items = [PendingItem(fragment.front_matter["section"], tuple(fragment.body.splitlines())) for fragment in fragments]
    text = assemble_release(target, changelog, items, version, release_date, is_empty_allowed)
    return text, tuple(fragment.path for fragment in fragments)


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
    changed_paths = {change.path for change in changes}
    changed_skills_outputs = sorted(changed_paths & set(SKILLS_OUTPUTS))
    if changed_skills_outputs and not is_correction:
        if is_legacy:
            errors.extend(legacy_skills_bump_problems(base, head, changes))
        else:
            errors.append(
                "only a release-prep pull request changes these release files: "
                + ", ".join(changed_skills_outputs)
                + ". Add a fragment with `python3 script/release/changelog_fragments.py new skills --bump <level>`"
                " instead, or add a line 'changelog: correction: <reason>' to the pull request description"
            )
    if not is_correction:
        errors.extend(component_output_errors(base, head, changed_paths, is_legacy))
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


def component_output_errors(base: TreeReader, head: TreeReader, changed_paths: set[str], is_legacy: bool) -> list[str]:
    """Returns why component change-log edits need a release-prep pull request; grace allows unreleased-entry edits."""
    errors = []
    for name, target in sorted(COMPONENT_CHANGELOGS.items()):
        if target.output not in changed_paths:
            continue
        base_text, head_text = base.read(target.output), head.read(target.output)
        # Once fragments exist for a target, its unreleased entry is frozen so prepare has one source.
        has_fragments = bool(base.fragment_paths(name) or head.fragment_paths(name))
        if (
            is_legacy
            and not has_fragments
            and base_text is not None
            and head_text is not None
            and is_legacy_entry_edit(target, base_text, head_text)
        ):
            continue
        hint = "--section <section>" if target.style == DOCS_STYLE else "--section <topic>"
        errors.append(
            f"only a release-prep pull request changes {target.output}. Add a fragment with "
            f"`python3 script/release/changelog_fragments.py new {name} {hint}` instead, or add a line "
            "'changelog: correction: <reason>' to the pull request description"
            + (
                ". A pull request from before fragments may edit only the newest unreleased entry, "
                "and only while the target has no fragments"
                if is_legacy
                else ""
            )
        )
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
    deleted_paths = {change.path for change in changes if change.status == "D"}
    targets = sorted(
        target
        for target, outputs in TARGET_OUTPUTS.items()
        if changed_paths & set(outputs) or any(is_fragment_path(path, target) for path in deleted_paths)
    )
    if not targets:
        return ["a release-prep pull request must change release files; this one changes none"]
    errors: list[str] = []
    allowed_paths: set[str] = set()
    for target in targets:
        verify = verify_skills_release_prep if target == SKILLS_TARGET else verify_component_release_prep
        target_errors, target_paths = verify(target, base, head, deleted_paths)
        errors.extend(target_errors)
        allowed_paths |= target_paths
    unexpected_paths = sorted(changed_paths - allowed_paths)
    if unexpected_paths:
        errors.append("a release-prep pull request changes only release files and fragments, not: " + ", ".join(unexpected_paths))
    return errors


def verify_skills_release_prep(
    target: str, base: TreeReader, head: TreeReader, deleted_paths: set[str]
) -> tuple[list[str], set[str]]:
    """Returns why the skills release files differ from `prepare` on the base, and the paths it may change."""
    head_changelog = head.read(SKILLS_CHANGELOG)
    heading = SKILLS_ENTRY_HEADING.search(head_changelog or "")
    if heading is None:
        return [f"{SKILLS_CHANGELOG} has no '## X.Y.Z - YYYY-MM-DD' entry"], set(SKILLS_OUTPUTS)
    release_date = heading.group("date")
    reprepare = (
        "Prepare it again on a fresh branch from origin/main: "
        f"python3 script/release/changelog_fragments.py prepare skills --date {release_date}"
    )
    try:
        expected = assemble_skills_release(read_files(base, SKILLS_OUTPUTS), base.fragments(SKILLS_TARGET), release_date)
    except FragmentError as error:
        return [f"the base cannot be prepared: {error}"], set(SKILLS_OUTPUTS)

    errors = []
    if heading.group("version") != expected.version:
        errors.append(f"the new entry is {heading.group('version')}, but the base fragments release {expected.version}")
    errors.extend(consumption_errors(expected.consumed_fragments, deleted_paths, target))
    for path in sorted(expected.files):
        if head.read(path) != expected.files[path]:
            errors.append(f"{path} differs from the prepare output")
    if errors:
        errors.append(reprepare)
    return errors, set(expected.files) | set(expected.consumed_fragments)


def verify_component_release_prep(
    target_name: str, base: TreeReader, head: TreeReader, deleted_paths: set[str]
) -> tuple[list[str], set[str]]:
    """Returns why a component change log differs from `prepare` on the base, and the paths it may change."""
    target = COMPONENT_CHANGELOGS[target_name]
    component = next(name for name, targets in RELEASE_COMPONENTS.items() if target_name in targets)
    head_changelog = head.read(target.output)
    release = newest_release(target, head_changelog or "")
    if release is None:
        return [f"{target.output}: the newest entry records no {target.tag_prefix} release"], {target.output}
    version, release_date = release
    reprepare = (
        "Prepare it again on a fresh branch from origin/main: "
        f"python3 script/release/changelog_fragments.py prepare {component} --version {version} --date {release_date}"
    )
    try:
        expected_text, consumed = prepare_component_target(base, target_name, version, release_date, is_empty_allowed=True)
    except (FragmentError, ChangelogError) as error:
        return [f"the base cannot be prepared: {error}", reprepare], {target.output}
    errors = consumption_errors(consumed, deleted_paths, target_name)
    if head_changelog != expected_text:
        errors.append(f"{target.output} differs from the prepare output")
    if errors:
        errors.append(reprepare)
    return errors, {target.output, *consumed}


def consumption_errors(consumed: Sequence[str], deleted_paths: set[str], target: str) -> list[str]:
    missing = sorted(set(consumed) - deleted_paths)
    if not missing:
        return []
    return [f"the base has {target} fragments this release does not consume: " + ", ".join(missing)]


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
    problems.extend(front_matter_problems(fragment, parts[1]))
    problems.extend(body_problems(fragment))
    if parts[1] in COMPONENT_CHANGELOGS and COMPONENT_CHANGELOGS[parts[1]].style == DOCS_STYLE:
        problems.extend(docs_prose_problems(fragment))
    return problems


def front_matter_problems(fragment: Fragment, target: str) -> list[str]:
    keys = set(fragment.front_matter)
    if target == SKILLS_TARGET:
        if keys != {"bump"}:
            return [f"{fragment.path}: a skills fragment has exactly the front matter key 'bump', found {sorted(keys)}"]
        if fragment.front_matter["bump"] not in BUMP_LEVELS:
            return [f"{fragment.path}: bump must be one of {', '.join(BUMP_LEVELS)}"]
        return []
    if keys != {"section"}:
        return [f"{fragment.path}: a {target} fragment has exactly the front matter key 'section', found {sorted(keys)}"]
    section = fragment.front_matter["section"]
    if COMPONENT_CHANGELOGS[target].style == DOCS_STYLE and section not in STANDARD_SECTIONS:
        return [f"{fragment.path}: section must be one of {', '.join(STANDARD_SECTIONS)}"]
    if not section or "#" in section:
        return [f"{fragment.path}: section must be a topic or one of {', '.join(STANDARD_SECTIONS)}"]
    return []


def docs_prose_problems(fragment: Fragment) -> list[str]:
    """Returns the docs prose rules a Developer reference fragment breaks: bold, not backticks, and MDX-safe text."""
    problems = []
    if INLINE_BACKTICK.search(fragment.body):
        problems.append(f"{fragment.path}: docs prose uses **bold** for identifiers, not inline backticks")
    if MDX_UNSAFE.search(fragment.body):
        problems.append(f"{fragment.path}: docs prose must not contain braces or '<', which MDX parses as code")
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
