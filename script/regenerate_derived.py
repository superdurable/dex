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

DEFAULT_UPSTREAM = "origin/main"

CARGO_WORKSPACES = ("server-zd", "sdk-rust", "s3local", "examples/rust")
# Keep in sync with GENERATED_CODE_PATHS in the root Makefile.
GENERATED_CODE_PATHS = (
    "sdk-go/gen/dexpb",
    "sdk-java/src/main/java/io/superdurable/gen",
    "sdk-python/dex/dexpb",
    "sdk-typescript/src/gen",
)
FLOW_DEFINITIONS_PATH = "docs/src/data/flow-definitions"
FORMAT_COMPAT_TESTDATA = "server-zd/testdata"
FORMAT_COMPAT_PIN_FILE = "server-zd/crates/dexzd-engine/tests/format_compat/golden_replay.rs"
FORMAT_COMPAT_VERSION_PATTERN = re.compile(r"^server-zd/testdata/(v[0-9]+)/(objects/.+|state\.txt)$")
FORMAT_COMPAT_PIN_LINE = re.compile(r'^\s*\("v[0-9]+", "[0-9a-f]{32}"\),\s*$')
FORMAT_COMPAT_MOVED_DIGEST = re.compile(r'testdata/(v[0-9]+) changed[^\n]*\n\s*left: "([0-9a-f]{32})"')
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
    """A condition that stops the command before or while it changes files."""


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

    def __init__(self, root: Path, upstream: str, generator_commands: dict[str, str]) -> None:
        self.root = root
        self.upstream = upstream
        self.generator_commands = generator_commands

    def git(self, *arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ("git", *arguments),
            cwd=self.root,
            check=check,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
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

    def is_merge_or_rebase_in_progress(self) -> bool:
        markers = ("MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply")
        for marker in markers:
            path = Path(self.git("rev-parse", "--git-path", marker).stdout.strip())
            if (path if path.is_absolute() else self.root / path).exists():
                return True
        return False

    def unmerged_paths(self) -> list[str]:
        output = self.git("ls-files", "--unmerged", "-z").stdout
        paths = {entry.split("\t", 1)[1] for entry in output.split("\0") if entry}
        return sorted(paths)

    def paths_differing_from_upstream(self) -> list[str]:
        output = self.git("diff", "--name-only", "-z", self.upstream, "--").stdout
        return sorted(path for path in output.split("\0") if path)

    def exists_upstream(self, path: str) -> bool:
        return self.git("cat-file", "-e", f"{self.upstream}:{path}", check=False).returncode == 0

    def take_upstream_copy(self, path: str) -> None:
        if self.exists_upstream(path):
            self.git("checkout", self.upstream, "--", path)
            return
        self.git("rm", "--cached", "--quiet", "--ignore-unmatch", "--", path)
        (self.root / path).unlink(missing_ok=True)


def main(argv: list[str] | None = None) -> int:
    arguments = parse_arguments(argv)
    root = Path(
        subprocess.run(
            ("git", "rev-parse", "--show-toplevel"), check=True, text=True, stdout=subprocess.PIPE
        ).stdout.strip()
    )
    repository = Repository(root, arguments.upstream, parse_generator_commands(arguments.generator_command))
    try:
        return regenerate_derived(repository, is_dry_run=arguments.dry_run, should_run_all=arguments.all)
    except RegenerationError as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


def parse_arguments(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--upstream", default=DEFAULT_UPSTREAM, help="ref whose copy resolves a conflict")
    parser.add_argument("--dry-run", action="store_true", help="print the plan and change nothing")
    parser.add_argument("--all", action="store_true", help="run every generator")
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


def regenerate_derived(repository: Repository, is_dry_run: bool, should_run_all: bool) -> int:
    if repository.git("rev-parse", "--verify", "--quiet", f"{repository.upstream}^{{commit}}", check=False).returncode:
        raise RegenerationError(f"upstream ref {repository.upstream} does not exist; fetch it or pass --upstream")
    groups = derived_groups()
    unmerged = repository.unmerged_paths()
    unowned_conflicts = classify_paths(groups, unmerged, repository.paths_differing_from_upstream())
    blockers = find_blockers(repository, groups, unowned_conflicts)
    selected = [
        group
        for group in groups
        if not group.is_manual and (should_run_all or group.conflicted or group.changed)
    ]
    print_plan(repository, groups, selected, blockers)
    if blockers:
        return 1
    if is_dry_run:
        return 0
    for group in selected:
        for path in group.conflicted:
            repository.take_upstream_copy(path)
    is_operation_in_progress = repository.is_merge_or_rebase_in_progress()
    for group in selected:
        print(f"== regenerating {group.name}", flush=True)
        override = repository.generator_commands.get(group.name)
        if override is not None:
            repository.run_checked(override)
        else:
            group.regenerate(repository)
        existing_pathspecs = [path for path in group.pathspecs if (repository.root / path).exists()]
        if (group.conflicted or is_operation_in_progress) and existing_pathspecs:
            repository.git("add", "--all", "--", *existing_pathspecs)
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


def find_blockers(repository: Repository, groups: list[DerivedGroup], unowned_conflicts: list[str]) -> list[str]:
    blockers = []
    if unowned_conflicts:
        blockers.append(
            "resolve these conflicts by hand first; they are not derived files:\n"
            + "\n".join(f"    {path}" for path in unowned_conflicts)
        )
    for group in groups:
        if group.is_manual and group.conflicted:
            blockers.append(
                f"{group.name} cannot be regenerated; follow {SIMULATION_README_SECTION}:\n"
                + "\n".join(f"    {path}" for path in group.conflicted)
            )
    if FORMAT_COMPAT_PIN_FILE in repository.unmerged_paths() and not are_conflicts_only_in_pins(
        (repository.root / FORMAT_COMPAT_PIN_FILE).read_text(encoding="utf-8")
    ):
        blockers.append(
            f"{FORMAT_COMPAT_PIN_FILE} conflicts outside PINNED_VERSION_DIGESTS; resolve the other hunks by hand,"
            " keep either pin line, and run this command again"
        )
    return blockers


def are_conflicts_only_in_pins(text: str) -> bool:
    is_inside_conflict = False
    for line in text.splitlines():
        if line.startswith("<<<<<<<"):
            is_inside_conflict = True
        elif line.startswith(">>>>>>>"):
            is_inside_conflict = False
        elif is_inside_conflict and not (
            line.startswith("=======") or line.startswith("|||||||") or FORMAT_COMPAT_PIN_LINE.match(line)
        ):
            return False
    return True


def print_plan(
    repository: Repository, groups: list[DerivedGroup], selected: list[DerivedGroup], blockers: list[str]
) -> None:
    print(f"Upstream: {repository.upstream}")
    for group in groups:
        if group.is_manual:
            state = "manual" if group.conflicted or group.changed else "unchanged"
        else:
            state = "regenerate" if group in selected else "skip"
        print(f"  {group.name}: {state}")
        for path in group.conflicted:
            action = "conflicted, left as is" if group.is_manual else f"conflicted, take {repository.upstream} copy"
            print(f"      {action}: {path}")
        for path in group.changed[:5]:
            print(f"      differs from {repository.upstream}: {path}")
        if len(group.changed) > 5:
            print(f"      ... and {len(group.changed) - 5} more")
    for blocker in blockers:
        print(f"Blocked: {blocker}")


def report_result(repository: Repository, groups: list[DerivedGroup], selected: list[DerivedGroup]) -> int:
    pathspecs = [
        pathspec
        for group in selected
        for pathspec in group.pathspecs
        if (repository.root / pathspec).exists()
    ]
    if pathspecs:
        status = repository.git("status", "--short", "--", *pathspecs).stdout
        print("Derived files now differing from HEAD:" if status else "Derived files match HEAD.")
        if status:
            print(status, end="")
    manual = [group for group in groups if group.is_manual and group.changed]
    for group in manual:
        print(
            f"Note: {group.name} differ from {repository.upstream}. They cannot be regenerated."
            f" If a pinned digest moved, follow {SIMULATION_README_SECTION}."
        )
    if repository.unmerged_paths():
        print("Unmerged paths remain; resolve them before continuing.")
        return 1
    return 0


def derived_groups() -> list[DerivedGroup]:
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
    groups.append(
        DerivedGroup(
            name="format-compat",
            pathspecs=(FORMAT_COMPAT_TESTDATA, FORMAT_COMPAT_PIN_FILE),
            matches=lambda path: path == FORMAT_COMPAT_PIN_FILE or bool(FORMAT_COMPAT_VERSION_PATTERN.match(path)),
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
    # cargo metadata only completes the lock file; it never upgrades a locked dependency.
    return DerivedGroup(
        name=f"cargo-lock:{workspace}",
        pathspecs=(lock_path,),
        matches=lambda path: path == lock_path,
        regenerate=lambda repository: repository.run_checked(
            ["cargo", "metadata", "--format-version", "1"], workspace, stdout=subprocess.DEVNULL
        ),
    )


def regenerate_format_compat(repository: Repository) -> None:
    if repository.git("tag", "--list", "server-zd/v1.*").stdout.strip():
        raise RegenerationError(
            "server-zd 1.0 is released, so recorded format_compat versions are immutable;"
            " add a new version as server-zd/testdata/README.md describes"
        )
    version = newest_format_compat_version(repository.root)
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


def newest_format_compat_version(root: Path) -> str:
    versions = [
        int(path.name[1:])
        for path in (root / FORMAT_COMPAT_TESTDATA).iterdir()
        if path.is_dir() and re.fullmatch(r"v[0-9]+", path.name)
    ]
    if not versions:
        raise RegenerationError(f"no v<N> directory under {FORMAT_COMPAT_TESTDATA}")
    return f"v{max(versions)}"


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
    updated, count = re.subn(
        rf'\("{version}", "[0-9a-f]{{32}}"\)', f'("{version}", "{match.group(2)}")', text
    )
    if count != 1:
        raise RegenerationError(f"found {count} pin lines for {version} in {FORMAT_COMPAT_PIN_FILE}")
    pin_path.write_text(updated, encoding="utf-8")
    repository.run_checked(pin_test, "server-zd")


def environment_with(**variables: str) -> dict[str, str]:
    return {**os.environ, **variables}


def is_under(path: str, prefix: str) -> bool:
    return path == prefix or path.startswith(prefix + "/")


if __name__ == "__main__":
    sys.exit(main())
