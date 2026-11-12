#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Read the pull request paths of the CI suites, the one source of what can affect each suite.

Commands:

* ``select-main-suites`` prints the Main CI (all) jobs whose suite paths match
  the changed files, so main runs a suite exactly when a pull request would.
* ``check-paths`` fails when a Check Gate CI path filter differs from the pull
  request paths of the workflow it waits for. A filter with extra paths waits
  for a run that never starts; a workflow with extra paths runs a check that the
  gate never enforces. It also fails when an SDK suite misses a cli/internal
  package that dexcli dev builds.

The workflows are read line by line, so this needs no YAML library on the runner.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

WORKFLOW_DIRECTORY = Path(".github/workflows")
MAIN_CI_WORKFLOW = "main-ci-all.yml"
GATE_WORKFLOW = "check-gate-ci.yml"
# A change to these files can change which suites Main CI (all) runs, so it selects every suite.
MAIN_CI_SELECTION_FILES = frozenset(
    {f".github/workflows/{MAIN_CI_WORKFLOW}", ".github/scripts/workflow_paths.py"}
)

# The SDK suites build dexcli and run dexcli dev, which never loads these cli/internal packages.
DEXCLI_PACKAGES_OUTSIDE_DEV = frozenset({"agentsterminal"})
SDK_SUITES_THAT_RUN_DEXCLI_DEV = (
    "sdk-go-ci.yml",
    "sdk-java-ci.yml",
    "sdk-python-ci.yml",
    "sdk-rust-ci.yml",
    "sdk-typescript-ci.yml",
)

USES_PATTERN =re.compile(r"^    uses: \./\.github/workflows/(?P<file>[A-Za-z0-9_.-]+\.ya?ml)\s*$")
JOB_PATTERN = re.compile(r"^  (?P<job>[A-Za-z0-9_-]+):\s*$")
NAME_PATTERN = re.compile(r"^name: (?P<name>.+?)\s*$")
LIST_ITEM_PATTERN = re.compile(r"^(?P<indent> *)- *(?P<value>.+?)\s*$")
SUPPORTED_GLOB_PATTERN = re.compile(r"^[A-Za-z0-9_./*-]+$")
GATE_FILTER_OUTPUT_PATTERN = re.compile(
    r"^\s+(?P<variable>FILTER_[A-Z_]+): \$\{\{ steps\.filter\.outputs\.(?P<filter>[a-z_]+) \}\}\s*$"
)
GATE_EXPECTED_PATTERN = re.compile(
    r'^\s+\[\[ "\$(?P<variable>FILTER_[A-Z_]+)" == "true" \]\] && expected\+=\("(?P<name>[^"]+)"\)\s*$'
)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--workflow-directory", type=Path, default=WORKFLOW_DIRECTORY)
    commands = parser.add_subparsers(dest="command", required=True)
    select = commands.add_parser("select-main-suites")
    select.add_argument(
        "--changed-files",
        type=Path,
        help="file with one changed repository path per line; omit to select every suite",
    )
    commands.add_parser("check-paths")
    arguments = parser.parse_args()

    if arguments.command == "select-main-suites":
        changed_files = None
        if arguments.changed_files is not None:
            changed_files = [
                line.strip()
                for line in arguments.changed_files.read_text(encoding="utf-8").splitlines()
                if line.strip()
            ]
        json.dump(select_main_suites(arguments.workflow_directory, changed_files), sys.stdout)
        sys.stdout.write("\n")
        return 0
    differences = check_gate_filters(arguments.workflow_directory)
    differences += check_dexcli_paths(arguments.workflow_directory, Path("cli/internal"))
    for difference in differences:
        print(difference, file=sys.stderr)
    return 1 if differences else 0


def select_main_suites(workflow_directory: Path, changed_files: list[str] | None) -> list[str]:
    """Returns the sorted Main CI (all) job IDs to run for these changed files, or every job for None."""
    suite_files = read_called_workflows(workflow_directory / MAIN_CI_WORKFLOW)
    if changed_files is None or MAIN_CI_SELECTION_FILES.intersection(changed_files):
        return sorted(suite_files)
    selected = []
    for job, suite_file in sorted(suite_files.items()):
        patterns = read_pull_request_paths(workflow_directory / suite_file)
        if patterns is None or any(matches_any(path, patterns) for path in changed_files):
            selected.append(job)
    return selected


def check_gate_filters(workflow_directory: Path) -> list[str]:
    """Returns one message per Check Gate filter whose paths differ from its workflow's."""
    gate_lines = (workflow_directory / GATE_WORKFLOW).read_text(encoding="utf-8").splitlines()
    filter_by_variable = {}
    name_by_variable = {}
    for line in gate_lines:
        output = GATE_FILTER_OUTPUT_PATTERN.match(line)
        if output:
            filter_by_variable[output.group("variable")] = output.group("filter")
        expected = GATE_EXPECTED_PATTERN.match(line)
        if expected:
            name_by_variable[expected.group("variable")] = expected.group("name")
    workflow_by_name = read_workflow_files_by_name(workflow_directory)
    gate_filters = read_gate_filters(gate_lines)
    differences = []
    for variable, name in sorted(name_by_variable.items()):
        filter_name = filter_by_variable.get(variable)
        if filter_name is None or filter_name not in gate_filters:
            differences.append(f"{GATE_WORKFLOW}: {variable} names no path filter")
            continue
        workflow_file = workflow_by_name.get(name)
        if workflow_file is None:
            differences.append(f"{GATE_WORKFLOW}: no workflow is named {name!r}")
            continue
        workflow_paths = read_pull_request_paths(workflow_directory / workflow_file, require_supported=False)
        gate_paths = gate_filters[filter_name]
        if workflow_paths is None or set(workflow_paths) != set(gate_paths):
            only_workflow = sorted(set(workflow_paths or []) - set(gate_paths))
            only_gate = sorted(set(gate_paths) - set(workflow_paths or []))
            differences.append(
                f"filter {filter_name} differs from {workflow_file} on.pull_request.paths: "
                f"only in the workflow {only_workflow}, only in the gate {only_gate}"
            )
    return differences


def check_dexcli_paths(workflow_directory: Path, cli_internal_directory: Path) -> list[str]:
    """Returns one message per SDK suite that misses a dexcli package its dexcli dev run builds.

    The SDK suites list the cli/internal packages one by one, leaving out only
    the agents terminal UI, so a new package must be added to each of them.
    """
    package_names = sorted(
        path.name
        for path in cli_internal_directory.iterdir()
        if path.is_dir() and path.name not in DEXCLI_PACKAGES_OUTSIDE_DEV
    )
    differences = []
    for suite_file in SDK_SUITES_THAT_RUN_DEXCLI_DEV:
        patterns = read_pull_request_paths(workflow_directory / suite_file) or []
        for package_name in package_names:
            sample_path = f"cli/internal/{package_name}/package.go"
            if not matches_any(sample_path, patterns):
                differences.append(f"{suite_file}: on.pull_request.paths misses cli/internal/{package_name}/**")
    return differences


def read_called_workflows(caller_path: Path) -> dict[str, str]:
    """Returns each job of the caller workflow that calls a local workflow, mapped to that file."""
    called = {}
    current_job = None
    in_jobs = False
    for line in caller_path.read_text(encoding="utf-8").splitlines():
        if line.startswith("jobs:"):
            in_jobs = True
            continue
        if not in_jobs:
            continue
        job_match = JOB_PATTERN.match(line)
        if job_match:
            current_job = job_match.group("job")
            continue
        uses_match = USES_PATTERN.match(line)
        if uses_match and current_job is not None:
            called[current_job] = uses_match.group("file")
    if not called:
        raise SystemExit(f"{caller_path} calls no local workflow")
    return called


def read_workflow_files_by_name(workflow_directory: Path) -> dict[str, str]:
    names = {}
    for path in sorted(workflow_directory.glob("*.yml")):
        for line in path.read_text(encoding="utf-8").splitlines():
            match = NAME_PATTERN.match(line)
            if match:
                names[match.group("name")] = path.name
                break
    return names


def read_pull_request_paths(workflow_path: Path, require_supported: bool = True) -> list[str] | None:
    """Returns the on.pull_request.paths patterns, or None when every change must select the suite.

    None means the workflow has no pull request paths or, when require_supported
    is true, a pattern uses glob syntax that translate_glob does not handle.
    """
    lines = workflow_path.read_text(encoding="utf-8").splitlines()
    try:
        pull_request_index = lines.index("  pull_request:", lines.index("on:"))
    except ValueError:
        return None
    patterns = read_list_after_key(lines, pull_request_index, "paths:", block_indent=2)
    if not patterns:
        return None
    if require_supported and not all(SUPPORTED_GLOB_PATTERN.match(pattern) for pattern in patterns):
        return None
    return patterns


def read_gate_filters(gate_lines: list[str]) -> dict[str, list[str]]:
    """Returns the paths of each dorny/paths-filter filter in the Check Gate filter step."""
    filters: dict[str, list[str]] = {}
    current = None
    filter_indent = None
    for line in gate_lines:
        if line.strip() == "filters: |":
            filter_indent = len(line) - len(line.lstrip(" ")) + 2
            continue
        if filter_indent is None:
            continue
        indent = len(line) - len(line.lstrip(" "))
        if line.strip() and indent < filter_indent:
            break
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if indent == filter_indent and line.strip().endswith(":"):
            current = line.strip()[:-1]
            filters[current] = []
            continue
        item = LIST_ITEM_PATTERN.match(line)
        if item and current is not None:
            filters[current].append(item.group("value").strip("\"'"))
    return filters


def read_list_after_key(lines: list[str], block_index: int, key: str, block_indent: int) -> list[str]:
    values: list[str] = []
    in_list = False
    list_indent = 0
    for line in lines[block_index + 1 :]:
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip(" "))
        if indent <= block_indent:
            break
        if line.strip() == key:
            in_list = True
            list_indent = indent
            continue
        if in_list:
            item = LIST_ITEM_PATTERN.match(line)
            if item is None or indent < list_indent:
                in_list = False
                continue
            values.append(item.group("value").strip("\"'"))
    return values


def matches_any(path: str, patterns: list[str]) -> bool:
    return any(translate_glob(pattern).fullmatch(path) for pattern in patterns)


def translate_glob(pattern: str) -> re.Pattern[str]:
    """Translates a GitHub paths glob made of *, **, and literal characters to a regular expression."""
    expression = ""
    index = 0
    while index < len(pattern):
        if pattern.startswith("**/", index):
            expression += "(?:[^/]*/)*"
            index += 3
        elif pattern.startswith("**", index):
            expression += ".*"
            index += 2
        elif pattern[index] == "*":
            expression += "[^/]*"
            index += 1
        else:
            expression += re.escape(pattern[index])
            index += 1
    return re.compile(expression)


if __name__ == "__main__":
    raise SystemExit(main())
