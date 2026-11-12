#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Select the agents packages whose CI a change can affect.

A package is a directory with an executable ci.sh. A changed file inside a
package selects that package and every package that depends on it, directly
or through other packages. A package depends on another when one of its tracked
files names the other's directory, by a relative path such as ../core or by its
path under agents/. So a change to the Python core selects the Python packages
and the Go core, but not the TypeScript packages.

Any other file under agents/, such as the conformance suite, a schema, or a
script, and any workflow selects every package. Markdown outside a package
selects nothing, because no package test reads it. Agents CI reads no file
outside agents/ and .github/workflows/; it downloads a released dexcli.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath

AGENTS_DIRECTORY = "agents"
WORKFLOW_DIRECTORY = ".github/workflows"
MARKDOWN_SUFFIX = ".md"

RELATIVE_PATH_PATTERN = re.compile(r"(?:\.\./)+[A-Za-z0-9_.\-/]*")
TEXT_SUFFIXES = frozenset(
    {
        ".cjs", ".go", ".js", ".json", ".mjs", ".mod", ".py", ".sh", ".toml", ".ts", ".tsx", ".txt",
        ".yaml", ".yml",
    }
)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repository-root", type=Path, default=Path.cwd())
    parser.add_argument(
        "--changed-files",
        type=Path,
        help="file with one changed repository path per line; omit to select every package",
    )
    arguments = parser.parse_args()

    repository_root = arguments.repository_root.resolve()
    tracked_files = list_tracked_agents_files(repository_root)
    package_paths = find_package_paths(repository_root, tracked_files)
    if arguments.changed_files is None:
        selected = package_paths
    else:
        changed_files = [
            line.strip()
            for line in arguments.changed_files.read_text(encoding="utf-8").splitlines()
            if line.strip()
        ]
        dependencies = find_package_dependencies(repository_root, tracked_files, package_paths)
        selected = select_packages(changed_files, package_paths, dependencies)
    json.dump(selected, sys.stdout)
    sys.stdout.write("\n")
    return 0


def list_tracked_agents_files(repository_root: Path) -> list[str]:
    result = subprocess.run(
        ["git", "ls-files", "-z", "--", AGENTS_DIRECTORY],
        cwd=repository_root,
        check=True,
        capture_output=True,
    )
    return [path for path in result.stdout.decode("utf-8").split("\0") if path]


def find_package_paths(repository_root: Path, tracked_files: list[str]) -> list[str]:
    package_paths = []
    for path in tracked_files:
        parts = PurePosixPath(path).parts
        is_package_script = parts[-1] == "ci.sh" and (
            len(parts) == 4 or (len(parts) == 5 and parts[1] == "cli")
        )
        if is_package_script and os.access(repository_root / path, os.X_OK):
            package_paths.append(str(PurePosixPath(path).parent))
    return sorted(package_paths)


def find_package_dependencies(
    repository_root: Path, tracked_files: list[str], package_paths: list[str]
) -> dict[str, set[str]]:
    """Returns, for each package path, the set of other package paths that it names."""
    dependencies: dict[str, set[str]] = {package: set() for package in package_paths}
    for path in tracked_files:
        package = find_owning_package(path, package_paths)
        if package is None or PurePosixPath(path).suffix not in TEXT_SUFFIXES:
            continue
        text = (repository_root / path).read_text(encoding="utf-8", errors="replace")
        for named_path in find_named_paths(path, text):
            named_package = find_owning_package(named_path, package_paths)
            if named_package is not None and named_package != package:
                dependencies[package].add(named_package)
        for other in package_paths:
            if other != package and other in text:
                dependencies[package].add(other)
    return dependencies


def find_named_paths(path: str, text: str) -> list[str]:
    directory = PurePosixPath(path).parent
    named_paths = []
    for match in RELATIVE_PATH_PATTERN.findall(text):
        resolved = os.path.normpath(str(directory / match))
        named_paths.append(resolved)
    return named_paths


def find_owning_package(path: str, package_paths: list[str]) -> str | None:
    for package in package_paths:
        if path == package or path.startswith(package + "/"):
            return package
    return None


def select_packages(
    changed_files: list[str], package_paths: list[str], dependencies: dict[str, set[str]]
) -> list[str]:
    changed_packages = set()
    for path in changed_files:
        if path.startswith(WORKFLOW_DIRECTORY + "/"):
            return package_paths
        if not path.startswith(AGENTS_DIRECTORY + "/"):
            continue
        package = find_owning_package(path, package_paths)
        if package is not None:
            changed_packages.add(package)
        elif PurePosixPath(path).suffix != MARKDOWN_SUFFIX:
            return package_paths
    selected = set(changed_packages)
    is_growing = True
    while is_growing:
        dependents = {
            package for package, named in dependencies.items() if named & selected
        } - selected
        selected |= dependents
        is_growing = bool(dependents)
    return sorted(selected)


if __name__ == "__main__":
    raise SystemExit(main())
