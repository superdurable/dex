# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Resolve derived-file conflicts by taking the upstream copy and rerunning their generators."""

from __future__ import annotations

import argparse
import os
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable

FALLBACK_UPSTREAM = "origin/main"
REMOTE_TAG_TIMEOUT_SECONDS = 15

CARGO_WORKSPACES = ("server-zd", "sdk-rust", "s3local", "examples/rust")
# Keep in sync with GENERATED_CODE_PATHS in the root Makefile; a test compares them.
GENERATED_CODE_PATHS = (
    "sdk-go/gen/dexpb",
    "sdk-java/src/main/java/io/superdurable/gen",
    "sdk-python/dex/dexpb",
    "sdk-typescript/src/gen",
)
FLOW_DEFINITIONS_PATH = "docs/src/data/flow-definitions"
FORMAT_COMPAT_TESTDATA = "server-zd/testdata"
FORMAT_COMPAT_PIN_FILE = "server-zd/crates/dexzd-engine/tests/format_compat/golden_replay.rs"
FORMAT_COMPAT_VERSION_PATH = re.compile(r"^server-zd/testdata/(v[0-9]+)/(.+)$")
FORMAT_COMPAT_GENERATED_FILE = re.compile(r"^(objects/.+|state\.txt)$")
FORMAT_COMPAT_PIN_BLOCK = re.compile(r"^const PINNED_VERSION_DIGESTS\b[^\n]*\n(.*?)^\];", re.MULTILINE | re.DOTALL)
FORMAT_COMPAT_PIN_ENTRY = re.compile(r'\("(v[0-9]+)", "([0-9a-f]{32})"\)')
FORMAT_COMPAT_MOVED_DIGEST = re.compile(r'testdata/(v[0-9]+) changed[^\n]*\n\s*left: "([0-9a-f]{32})"')
SERVER_ZD_STABLE_TAG_PATTERN = "server-zd/v1.*"
SIMULATION_CRATE = "server-zd/crates/dexzd-simulation"
SIMULATION_PIN_PATHS = (
    f"{SIMULATION_CRATE}/regression-seeds.txt",
    f"{SIMULATION_CRATE}/engine-regression-seeds.txt",
    f"{SIMULATION_CRATE}/README.md",
    f"{SIMULATION_CRATE}/tests/apply_invariants.rs",
    f"{SIMULATION_CRATE}/tests/engine_simulation.rs",
)
SIMULATION_README_SECTION = f"{SIMULATION_CRATE}/README.md, section 'Pinned trajectories'"


class RegenerationError(Exception):
    """A condition that stops the command while it runs a generator."""


@dataclass
class DerivedGroup:
    """One set of derived paths that a single generator rewrites."""

    name: str
    pathspecs: tuple[str, ...]
    matches: Callable[[str], bool]
    regenerate: Callable[["Repository"], None] | None
    conflicted: list[str] = field(default_factory=list)
    changed: list[str] = field(default_factory=list)

    @property
    def is_manual(self) -> bool:
        return self.regenerate is None


class Repository:
    """Git and generator access rooted at one working tree."""

    def __init__(self, root: Path, generator_commands: dict[str, str]) -> None:
        self.root = root
        self.upstream = FALLBACK_UPSTREAM
        self.generator_commands = generator_commands

    def git(self, *arguments: str, check: bool = True, timeout: float | None = None) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ("git", *arguments),
            cwd=self.root,
            check=check,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout,
        )

    def run(self, command: list[str] | str, directory: str = ".", **options: object) -> subprocess.CompletedProcess[str]:
        print(f"+ ({directory}) {command if isinstance(command, str) else ' '.join(command)}", flush=True)
        return subprocess.run(
            command,
            cwd=self.root / directory,
            shell=isinstance(command, str),
            text=True,
            **options,
        )

    def run_checked(self, command: list[str] | str, directory: str = ".", **options: object) -> None:
        result = self.run(command, directory, **options)
        if result.returncode != 0:
            raise RegenerationError(f"command failed with exit code {result.returncode}: {command}")

    def git_path(self, name: str) -> Path:
        path = Path(self.git("rev-parse", "--git-path", name).stdout.strip())
        return path if path.is_absolute() else self.root / path

    def operation_upstream(self) -> tuple[str, str] | None:
        """Returns the ref whose copy wins for the operation in progress and its description, or None."""
        for directory in ("rebase-merge", "rebase-apply"):
            onto = self.git_path(f"{directory}/onto")
            if onto.exists():
                return onto.read_text(encoding="utf-8").strip(), "the rebase onto commit"
        merge_head = self.git_path("MERGE_HEAD")
        if merge_head.exists():
            return merge_head.read_text(encoding="utf-8").split()[0], "MERGE_HEAD"
        for marker in ("CHERRY_PICK_HEAD", "REVERT_HEAD"):
            if self.git_path(marker).exists():
                return "HEAD", f"HEAD, the target branch of the {marker.split('_')[0].lower()}"
        return None

    def unmerged_paths(self) -> list[str]:
        output = self.git("ls-files", "--unmerged", "-z").stdout
        paths = {entry.split("\t", 1)[1] for entry in output.split("\0") if entry}
        return sorted(paths)

    def paths_differing_from_upstream(self) -> list[str]:
        output = self.git("diff", "--name-only", "-z", self.upstream, "--").stdout
        return sorted(path for path in output.split("\0") if path)

    def dirty_paths(self, pathspecs: list[str]) -> set[str]:
        """Returns paths whose working-tree copy differs from the index, plus untracked files."""
        if not pathspecs:
            return set()
        modified = self.git("diff", "--name-only", "-z", "--", *pathspecs).stdout
        untracked = self.git("ls-files", "--others", "--exclude-standard", "-z", "--", *pathspecs).stdout
        return {path for path in (modified + untracked).split("\0") if path}

    def upstream_text(self, path: str) -> str | None:
        result = self.git("show", f"{self.upstream}:{path}", check=False)
        return result.stdout if result.returncode == 0 else None

    def stage_text(self, stage: int, path: str) -> str | None:
        result = self.git("show", f":{stage}:{path}", check=False)
        return result.stdout if result.returncode == 0 else None

    def take_upstream_copy(self, path: str) -> None:
        if self.git("cat-file", "-e", f"{self.upstream}:{path}", check=False).returncode == 0:
            self.git("checkout", self.upstream, "--", path)
            return
        self.git("rm", "--cached", "--quiet", "--ignore-unmatch", "--", path)
        (self.root / path).unlink(missing_ok=True)

    def stable_server_zd_tags(self) -> tuple[list[str], bool]:
        """Returns the server-zd 1.x tags and whether origin answered; without origin, only local tags."""
        try:
            remote = self.git(
                "ls-remote", "--tags", "origin", f"refs/tags/{SERVER_ZD_STABLE_TAG_PATTERN}",
                check=False, timeout=REMOTE_TAG_TIMEOUT_SECONDS,
            )
            if remote.returncode == 0:
                return [line.split("\t", 1)[1].removeprefix("refs/tags/") for line in remote.stdout.splitlines() if "\t" in line], True
        except subprocess.TimeoutExpired:
            pass
        local = self.git("tag", "--list", SERVER_ZD_STABLE_TAG_PATTERN).stdout.split()
        return local, False


def main(argv: list[str] | None = None) -> int:
    arguments = parse_arguments(argv)
    root = Path(
        subprocess.run(
            ("git", "rev-parse", "--show-toplevel"), check=True, text=True, stdout=subprocess.PIPE
        ).stdout.strip()
    )
    repository = Repository(root, parse_generator_commands(arguments.generator_command))
    repository.upstream, upstream_source = choose_upstream(repository, arguments.upstream)
    try:
        return regenerate_derived(
            repository,
            upstream_source,
            is_dry_run=arguments.dry_run,
            should_run_all=arguments.all,
            is_assumed_before_1_0=arguments.assume_before_1_0,
        )
    except RegenerationError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


def parse_arguments(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--upstream",
        help="ref whose copy resolves a conflict; defaults to the rebase onto commit, MERGE_HEAD,"
        f" HEAD for a cherry-pick or revert, and {FALLBACK_UPSTREAM} otherwise",
    )
    parser.add_argument("--dry-run", action="store_true", help="print the plan and change nothing")
    parser.add_argument("--all", action="store_true", help="run every generator")
    parser.add_argument(
        "--assume-before-1-0",
        action="store_true",
        help="regenerate format_compat testdata even when origin cannot confirm that no server-zd 1.x tag exists",
    )
    parser.add_argument(
        "--generator-command",
        action="append",
        default=[],
        metavar="GROUP=COMMAND",
        help=argparse.SUPPRESS,
    )
    return parser.parse_args(argv)


def parse_generator_commands(values: list[str]) -> dict[str, str]:
    commands = {}
    for value in values:
        name, separator, command = value.partition("=")
        if not separator:
            raise SystemExit(f"--generator-command needs GROUP=COMMAND, got {value!r}")
        commands[name] = command
    return commands


def choose_upstream(repository: Repository, explicit_upstream: str | None) -> tuple[str, str]:
    if explicit_upstream:
        return explicit_upstream, "--upstream"
    operation = repository.operation_upstream()
    if operation is not None:
        return operation
    return FALLBACK_UPSTREAM, "the default; no merge or rebase is in progress, pass --upstream to change it"


def regenerate_derived(
    repository: Repository,
    upstream_source: str,
    is_dry_run: bool,
    should_run_all: bool,
    is_assumed_before_1_0: bool,
) -> int:
    if repository.git("rev-parse", "--verify", "--quiet", f"{repository.upstream}^{{commit}}", check=False).returncode:
        print(f"Blocked: upstream ref {repository.upstream} does not exist; fetch it or pass --upstream")
        return 1
    newest_version = newest_format_compat_version(repository.root)
    groups = derived_groups(newest_version)
    unowned_conflicts = classify_paths(groups, repository.unmerged_paths(), repository.paths_differing_from_upstream())
    selected = [
        group
        for group in groups
        if not group.is_manual and (should_run_all or group.conflicted or group.changed)
    ]
    blockers = find_blockers(repository, groups, selected, unowned_conflicts, newest_version, is_assumed_before_1_0)
    print_plan(repository, upstream_source, groups, selected, blockers)
    if blockers:
        return 1
    if is_dry_run:
        return 0
    should_stage = repository.operation_upstream() is not None
    for group in selected:
        for path in group.conflicted:
            repository.take_upstream_copy(path)
    for group in selected:
        print(f"== regenerating {group.name}", flush=True)
        override = repository.generator_commands.get(group.name)
        if override is not None:
            repository.run_checked(override)
        else:
            group.regenerate(repository)
        produced = repository.dirty_paths(existing_pathspecs(repository, group))
        if group.name.startswith("cargo-lock:") and produced:
            print(f"Review the lock file diff: git diff -- {group.pathspecs[0]}")
        to_stage = sorted(produced | set(group.conflicted)) if (group.conflicted or should_stage) else []
        if to_stage:
            repository.git("add", "--all", "--", *to_stage)
    return report_result(repository, groups, selected)


def classify_paths(groups: list[DerivedGroup], unmerged: list[str], changed: list[str]) -> list[str]:
    unowned_conflicts = []
    unmerged_set = set(unmerged)
    for path in unmerged:
        group = owning_group(groups, path)
        if group is None:
            unowned_conflicts.append(path)
        else:
            group.conflicted.append(path)
    for path in changed:
        group = owning_group(groups, path)
        if group is not None and path not in unmerged_set:
            group.changed.append(path)
    return unowned_conflicts


def owning_group(groups: list[DerivedGroup], path: str) -> DerivedGroup | None:
    return next((group for group in groups if group.matches(path)), None)


def find_blockers(
    repository: Repository,
    groups: list[DerivedGroup],
    selected: list[DerivedGroup],
    unowned_conflicts: list[str],
    newest_version: str | None,
    is_assumed_before_1_0: bool,
) -> list[str]:
    """Returns every reason to refuse; all of them are checked before any file or index change."""
    blockers = []
    if unowned_conflicts:
        blockers.append(
            "resolve these conflicts by hand first; they are not derived files:\n" + indented(unowned_conflicts)
        )
    for group in groups:
        if group.is_manual and group.conflicted:
            blockers.append(
                f"{group.name} cannot be regenerated; follow {SIMULATION_README_SECTION}:\n"
                + indented(group.conflicted)
            )
    format_compat = next(group for group in groups if group.name == "format-compat")
    blockers.extend(format_compat_blockers(repository, format_compat, newest_version))
    for group in selected:
        unrelated = repository.dirty_paths(existing_pathspecs(repository, group)) - set(group.conflicted)
        if unrelated:
            blockers.append(
                f"{group.name} has uncommitted changes that are not conflicts; commit or discard them first:\n"
                + indented(sorted(unrelated))
            )
    if format_compat in selected:
        tags, has_origin_answered = repository.stable_server_zd_tags()
        if tags:
            blockers.append(
                f"server-zd 1.0 is released ({tags[0]}), so recorded format_compat"
                " versions are immutable; add a new version as server-zd/testdata/README.md describes"
            )
        elif not has_origin_answered and not is_assumed_before_1_0:
            blockers.append(
                f"cannot list {SERVER_ZD_STABLE_TAG_PATTERN} tags on origin, so it is unknown whether server-zd 1.0"
                " froze the format_compat testdata; retry online, or pass --assume-before-1-0 if you know it is not"
                " released"
            )
        if newest_version is None and "format-compat" not in repository.generator_commands:
            blockers.append(f"no v<N> directory under {FORMAT_COMPAT_TESTDATA}")
    return blockers


def format_compat_blockers(repository: Repository, group: DerivedGroup, newest_version: str | None) -> list[str]:
    frozen = [
        path
        for path in group.conflicted + group.changed
        if path != FORMAT_COMPAT_PIN_FILE and not is_newest_generated_file(path, newest_version)
    ]
    blockers = []
    if frozen:
        blockers.append(
            f"only the newest format_compat version ({newest_version}) is regenerated; older versions are"
            f" frozen, so restore these paths from the upstream ({repository.upstream}):\n" + indented(sorted(frozen))
        )
    if FORMAT_COMPAT_PIN_FILE in group.conflicted:
        texts = [repository.upstream_text(FORMAT_COMPAT_PIN_FILE)] + [
            repository.stage_text(stage, FORMAT_COMPAT_PIN_FILE) for stage in (2, 3)
        ]
        masked = [masked_newest_pin(text, newest_version) for text in texts]
        if any(text is not None and masked_text is None for text, masked_text in zip(texts, masked)):
            blockers.append(
                f"{FORMAT_COMPAT_PIN_FILE} must hold exactly one {newest_version} entry in PINNED_VERSION_DIGESTS"
                " on both sides and in the upstream; resolve it by hand"
            )
        elif any(masked_text != masked[0] for masked_text in masked[1:]):
            blockers.append(
                f"{FORMAT_COMPAT_PIN_FILE} differs from the upstream ({repository.upstream}) outside the"
                f" {newest_version} pin; taking the upstream copy would drop those changes. Resolve it by hand,"
                " keep either newest pin line, and run this command again"
            )
    elif FORMAT_COMPAT_PIN_FILE in group.changed:
        upstream_text = repository.upstream_text(FORMAT_COMPAT_PIN_FILE) or ""
        working_text = (repository.root / FORMAT_COMPAT_PIN_FILE).read_text(encoding="utf-8")
        if older_pins(working_text, newest_version) != older_pins(upstream_text, newest_version):
            blockers.append(
                f"{FORMAT_COMPAT_PIN_FILE} changes the pin of an older, frozen format_compat version;"
                f" restore it from the upstream ({repository.upstream})"
            )
    return blockers


def is_newest_generated_file(path: str, newest_version: str | None) -> bool:
    match = FORMAT_COMPAT_VERSION_PATH.match(path)
    return bool(match and match.group(1) == newest_version and FORMAT_COMPAT_GENERATED_FILE.match(match.group(2)))


def masked_newest_pin(text: str | None, newest_version: str | None) -> str | None:
    """Returns text with only the newest PINNED_VERSION_DIGESTS digest replaced, or None when it has no such entry."""
    span = newest_pin_digest_span(text, newest_version) if text is not None else None
    if span is None:
        return None
    return text[: span[0]] + "<pin>" + text[span[1] :]


def newest_pin_digest_span(text: str, newest_version: str | None) -> tuple[int, int] | None:
    """Returns the digest's span of the single newest entry inside PINNED_VERSION_DIGESTS, or None."""
    block = FORMAT_COMPAT_PIN_BLOCK.search(text)
    if block is None or newest_version is None:
        return None
    entries = [
        entry
        for entry in FORMAT_COMPAT_PIN_ENTRY.finditer(text, block.start(1), block.end(1))
        if entry.group(1) == newest_version
    ]
    return entries[0].span(2) if len(entries) == 1 else None


def older_pins(text: str, newest_version: str | None) -> dict[str, str]:
    block = FORMAT_COMPAT_PIN_BLOCK.search(text)
    entries = FORMAT_COMPAT_PIN_ENTRY.findall(block.group(1)) if block else []
    return {version: digest for version, digest in entries if version != newest_version}


def print_plan(
    repository: Repository,
    upstream_source: str,
    groups: list[DerivedGroup],
    selected: list[DerivedGroup],
    blockers: list[str],
) -> None:
    print(f"Upstream: {repository.upstream} ({upstream_source})")
    for group in groups:
        if group.is_manual:
            state = "manual" if group.conflicted or group.changed else "unchanged"
        else:
            state = "regenerate" if group in selected else "skip"
        print(f"  {group.name}: {state}")
        for path in group.conflicted:
            action = "conflicted, left as is" if group.is_manual else "conflicted, take upstream copy"
            print(f"      {action}: {path}")
        for path in group.changed[:5]:
            print(f"      differs from upstream: {path}")
        if len(group.changed) > 5:
            print(f"      ... and {len(group.changed) - 5} more")
    for blocker in blockers:
        print(f"Blocked: {blocker}")


def report_result(repository: Repository, groups: list[DerivedGroup], selected: list[DerivedGroup]) -> int:
    pathspecs = [pathspec for group in selected for pathspec in existing_pathspecs(repository, group)]
    if pathspecs:
        status = repository.git("status", "--short", "--", *pathspecs).stdout
        print("Derived files now differing from HEAD:" if status else "Derived files match HEAD.")
        if status:
            print(status, end="")
    for group in groups:
        if group.is_manual and group.changed:
            print(
                f"Note: {group.name} differ from upstream. They cannot be regenerated."
                f" If a pinned digest moved, follow {SIMULATION_README_SECTION}."
            )
    if repository.unmerged_paths():
        print("Unmerged paths remain; resolve them before continuing.")
        return 1
    return 0


def derived_groups(newest_version: str | None) -> list[DerivedGroup]:
    groups = [cargo_lock_group(workspace) for workspace in CARGO_WORKSPACES]
    groups.append(
        DerivedGroup(
            name="generated-code",
            pathspecs=GENERATED_CODE_PATHS,
            matches=lambda path: any(is_under(path, prefix) for prefix in GENERATED_CODE_PATHS),
            regenerate=lambda repository: repository.run_checked(["make", "generated-code"]),
        )
    )
    groups.append(
        DerivedGroup(
            name="flow-definitions",
            pathspecs=(FLOW_DEFINITIONS_PATH,),
            matches=lambda path: is_under(path, FLOW_DEFINITIONS_PATH) and path.endswith(".json"),
            regenerate=lambda repository: repository.run_checked(
                ["npm", "run", "generate:flow-definitions"], "docs"
            ),
        )
    )
    newest_pathspecs = (
        (f"{FORMAT_COMPAT_TESTDATA}/{newest_version}/objects", f"{FORMAT_COMPAT_TESTDATA}/{newest_version}/state.txt")
        if newest_version
        else ()
    )
    groups.append(
        DerivedGroup(
            name="format-compat",
            pathspecs=(*newest_pathspecs, FORMAT_COMPAT_PIN_FILE),
            matches=lambda path: path == FORMAT_COMPAT_PIN_FILE or bool(FORMAT_COMPAT_VERSION_PATH.match(path)),
            regenerate=regenerate_format_compat,
        )
    )
    groups.append(
        DerivedGroup(
            name="simulation-pins",
            pathspecs=SIMULATION_PIN_PATHS,
            matches=lambda path: path in SIMULATION_PIN_PATHS,
            regenerate=None,
        )
    )
    return groups


def cargo_lock_group(workspace: str) -> DerivedGroup:
    lock_path = f"{workspace}/Cargo.lock"
    # Keeps locked versions that satisfy the manifests; updates a package only when a constraint requires it.
    return DerivedGroup(
        name=f"cargo-lock:{workspace}",
        pathspecs=(lock_path,),
        matches=lambda path: path == lock_path,
        regenerate=lambda repository: repository.run_checked(
            ["cargo", "metadata", "--format-version", "1"], workspace, stdout=subprocess.DEVNULL
        ),
    )


def regenerate_format_compat(repository: Repository) -> None:
    version = newest_format_compat_version(repository.root)
    if version is None:
        raise RegenerationError(f"no v<N> directory under {FORMAT_COMPAT_TESTDATA}")
    version_directory = repository.root / FORMAT_COMPAT_TESTDATA / version
    with tempfile.TemporaryDirectory() as temporary:
        output_directory = Path(temporary) / version
        repository.run_checked(
            [
                "cargo", "test", "-p", "dexzd-engine", "--test", "format_compat",
                "generate_format_compat_testdata", "--", "--ignored",
            ],
            "server-zd",
            env=environment_with(DEXZD_FORMAT_COMPAT_OUTPUT=str(output_directory)),
        )
        shutil.rmtree(version_directory / "objects", ignore_errors=True)
        shutil.copytree(output_directory / "objects", version_directory / "objects")
        shutil.copyfile(output_directory / "state.txt", version_directory / "state.txt")
    update_format_compat_pin(repository, version)


def newest_format_compat_version(root: Path) -> str | None:
    testdata = root / FORMAT_COMPAT_TESTDATA
    if not testdata.is_dir():
        return None
    versions = [
        int(path.name[1:]) for path in testdata.iterdir() if path.is_dir() and re.fullmatch(r"v[0-9]+", path.name)
    ]
    return f"v{max(versions)}" if versions else None


def update_format_compat_pin(repository: Repository, version: str) -> None:
    pin_test = [
        "cargo", "test", "-p", "dexzd-engine", "--test", "format_compat",
        "every_version_keeps_its_pinned_bytes",
    ]
    result = repository.run(pin_test, "server-zd", stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if result.returncode == 0:
        return
    match = FORMAT_COMPAT_MOVED_DIGEST.search(result.stdout)
    if match is None or match.group(1) != version:
        print(result.stdout)
        raise RegenerationError("the format_compat pin test failed without reporting a new digest")
    pin_path = repository.root / FORMAT_COMPAT_PIN_FILE
    text = pin_path.read_text(encoding="utf-8")
    span = newest_pin_digest_span(text, version)
    if span is None:
        raise RegenerationError(f"PINNED_VERSION_DIGESTS in {FORMAT_COMPAT_PIN_FILE} needs exactly one {version} entry")
    pin_path.write_text(text[: span[0]] + match.group(2) + text[span[1] :], encoding="utf-8")
    repository.run_checked(pin_test, "server-zd")


def existing_pathspecs(repository: Repository, group: DerivedGroup) -> list[str]:
    return [path for path in group.pathspecs if (repository.root / path).exists()]


def environment_with(**variables: str) -> dict[str, str]:
    return {**os.environ, **variables}


def indented(paths: list[str]) -> str:
    return "\n".join(f"    {path}" for path in paths)


def is_under(path: str, prefix: str) -> bool:
    return path == prefix or path.startswith(prefix + "/")


if __name__ == "__main__":
    sys.exit(main())
