#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Pin smoke-test installs to the built agents wheels, and check installed files against them."""

from __future__ import annotations

import base64
import csv
import hashlib
import importlib.metadata
import io
import re
import sys
import zipfile
from email.parser import Parser
from pathlib import Path

AGENTS_REQUIREMENT_PATTERN = re.compile(r"^(dex-agents(?:-[a-z0-9-]+)?)(\[[^\]]*\])?")


def built_wheel(dist_dir: Path, distribution: str) -> Path:
    wheels = sorted(dist_dir.glob(f"{distribution.replace('-', '_')}-[0-9]*-py3-none-any.whl"))
    if len(wheels) != 1:
        raise SystemExit(f"expected one built wheel of {distribution} in {dist_dir}, found {len(wheels)}")
    return wheels[0]


def wheel_requirements(wheel: Path) -> list[str]:
    with zipfile.ZipFile(wheel) as archive:
        metadata_member = next(name for name in archive.namelist() if name.endswith(".dist-info/METADATA"))
        metadata = Parser().parsestr(archive.read(metadata_member).decode())
    return metadata.get_all("Requires-Dist") or []


def print_requirements(dist_dir: Path, requested: list[str]) -> None:
    """Print a file URL requirement for each requested distribution and the agents distributions it needs."""
    extras_by_distribution: dict[str, str] = {}
    pending: list[str] = []
    for requirement in requested:
        match = AGENTS_REQUIREMENT_PATTERN.match(requirement)
        if match is None or match.end() != len(requirement):
            raise SystemExit(f"not an agents distribution: {requirement}")
        extras_by_distribution[match.group(1)] = match.group(2) or ""
        pending.append(match.group(1))
    while pending:
        distribution = pending.pop()
        for requirement in wheel_requirements(built_wheel(dist_dir, distribution)):
            match = AGENTS_REQUIREMENT_PATTERN.match(requirement)
            if match is not None and match.group(1) not in extras_by_distribution:
                extras_by_distribution[match.group(1)] = ""
                pending.append(match.group(1))
    for distribution, extras in sorted(extras_by_distribution.items()):
        print(f"{distribution}{extras} @ {built_wheel(dist_dir, distribution).resolve().as_uri()}")


def check_installed(dist_dir: Path) -> None:
    """Fail unless every installed agents distribution has its built wheel's files and hashes."""
    problems: list[str] = []
    checked: list[str] = []
    for installed in importlib.metadata.distributions():
        name = installed.metadata["Name"]
        if AGENTS_REQUIREMENT_PATTERN.fullmatch(name) is None:
            continue
        wheel = built_wheel(dist_dir, name)
        with zipfile.ZipFile(wheel) as archive:
            record_member = next(member for member in archive.namelist() if member.endswith(".dist-info/RECORD"))
            records = list(csv.reader(io.StringIO(archive.read(record_member).decode())))
        for path, digest, _ in records:
            if ".dist-info/" in path or not digest:
                continue
            installed_file = Path(installed.locate_file(path))
            actual = "sha256=" + base64.urlsafe_b64encode(
                hashlib.sha256(installed_file.read_bytes()).digest()
            ).rstrip(b"=").decode()
            if actual != digest:
                problems.append(f"{name}: {path} differs from {wheel.name}")
        checked.append(f"{name} {installed.version}")
    for problem in problems:
        print(problem, file=sys.stderr)
    if problems:
        raise SystemExit(1)
    print(f"installed from the built wheels: {', '.join(sorted(checked))}")


def main() -> None:
    command, dist_dir, *requested = sys.argv[1:]
    if command == "requirements":
        print_requirements(Path(dist_dir), requested)
    elif command == "check":
        check_installed(Path(dist_dir))
    else:
        raise SystemExit(f"unknown command {command}")


if __name__ == "__main__":
    main()
