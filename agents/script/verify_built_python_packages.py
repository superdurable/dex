#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Check the wheel and sdist of every Python agents package before anything installs them."""

from __future__ import annotations

import re
import sys
import tarfile
import zipfile
from email.parser import Parser
from pathlib import Path

FORBIDDEN_MEMBER_PATTERN = re.compile(r"(^|/)(tests?|__pycache__|\.venv|\.mypy_cache|\.ruff_cache)(/|$)|\.pyc$")
CORE_DISTRIBUTION = "dex-agents"


def canonical_version(version: str) -> str:
    """Return the PEP 440 spelling that build backends use in file names."""
    match = re.fullmatch(r"([0-9]+\.[0-9]+\.[0-9]+)(?:[.-]?(a|b|rc|alpha|beta)[.-]?([0-9]+))?", version)
    if match is None:
        return version
    core, label, number = match.groups()
    if label is None:
        return core
    label = {"alpha": "a", "beta": "b"}.get(label, label)
    return f"{core}{label}{number}"


def distribution_name(package: str) -> str:
    return CORE_DISTRIBUTION if package == "core" else f"{CORE_DISTRIBUTION}-{package}"


def module_name(package: str) -> str:
    return "dex_agents" if package == "core" else f"dex_agents_{package.replace('-', '_')}"


def verify_package(dist_dir: Path, package: str, version: str) -> list[str]:
    problems: list[str] = []
    file_stem = f"{distribution_name(package).replace('-', '_')}-{version}"
    wheel_path = dist_dir / f"{file_stem}-py3-none-any.whl"
    sdist_path = dist_dir / f"{file_stem}.tar.gz"
    for path in (wheel_path, sdist_path):
        if not path.is_file():
            return [f"missing {path.name}"]

    with zipfile.ZipFile(wheel_path) as wheel:
        members = wheel.namelist()
        metadata = Parser().parsestr(wheel.read(f"{file_stem}.dist-info/METADATA").decode())
    module = module_name(package)
    expected_members = (f"{module}/__init__.py", f"{module}/py.typed", f"{file_stem}.dist-info/licenses/LICENSE")
    problems += [f"{wheel_path.name} lacks {member}" for member in expected_members if member not in members]
    problems += [f"{wheel_path.name} contains {member}" for member in members if FORBIDDEN_MEMBER_PATTERN.search(member)]
    problems += [f"{wheel_path.name} has a file outside {module}" for member in members
                 if not member.startswith((f"{module}/", f"{file_stem}.dist-info/"))][:1]

    if metadata["Name"] != distribution_name(package) or metadata["Version"] != version:
        problems.append(f"{wheel_path.name} metadata is {metadata['Name']} {metadata['Version']}")
    if metadata["License-Expression"] != "LicenseRef-Sustainable-Use-1.0":
        problems.append(f"{wheel_path.name} License-Expression is {metadata['License-Expression']}")
    if metadata["Description-Content-Type"] != "text/markdown" or not metadata.get_payload().strip():
        problems.append(f"{wheel_path.name} has no Markdown README description")
    requirements = metadata.get_all("Requires-Dist") or []
    if any(" @ " in requirement or "file:" in requirement for requirement in requirements):
        problems.append(f"{wheel_path.name} has a direct or path requirement: {requirements}")
    if package != "core" and f"{CORE_DISTRIBUTION}=={version}" not in requirements:
        problems.append(f"{wheel_path.name} does not require {CORE_DISTRIBUTION}=={version}: {requirements}")

    with tarfile.open(sdist_path) as sdist:
        sdist_members = [member.removeprefix(f"{file_stem}/") for member in sdist.getnames()]
    for member in ("LICENSE", "README.md", "pyproject.toml", f"src/{module}/py.typed"):
        if member not in sdist_members:
            problems.append(f"{sdist_path.name} lacks {member}")
    problems += [f"{sdist_path.name} contains {member}" for member in sdist_members
                 if FORBIDDEN_MEMBER_PATTERN.search(member)]
    return problems


def main() -> int:
    dist_dir = Path(sys.argv[1])
    version = canonical_version(sys.argv[2])
    problems = [problem for package in sys.argv[3:] for problem in verify_package(dist_dir, package, version)]
    for problem in problems:
        print(problem, file=sys.stderr)
    if problems:
        return 1
    print(f"Verified {len(sys.argv) - 3} Python wheels and sdists at {version}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
