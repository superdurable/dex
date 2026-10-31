#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Stamp one release version, and the adapters' core constraints, across every agents package."""

from __future__ import annotations

import argparse
import json
import re
import sys
import tomllib
from pathlib import Path

AGENTS_DIR = Path(__file__).resolve().parents[1]
PYTHON_PACKAGES = ("core", "openai-agents", "pydantic-ai", "langgraph", "google-adk", "claude-agent-sdk")
TYPESCRIPT_PACKAGES = ("core", "vercel-ai", "openai-agents", "mastra", "claude-agent-sdk")
PYTHON_CORE_DISTRIBUTION = "dex-agents"
TYPESCRIPT_CORE_PACKAGE = "@superdurable/dex-agents"
VERSION_PATTERN = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z]+)*$")
PYTHON_CORE_CONSTRAINT_PATTERN = re.compile(r'"dex-agents==[^"]*"')


class StampMismatchError(Exception):
    """A file does not carry the requested version."""


def stamp_pyproject(path: Path, version: str, is_core: bool, check_only: bool) -> None:
    text = path.read_text(encoding="utf-8")
    updated, version_count = re.subn(
        r'(?m)^version = "[^"]*"', f'version = "{version}"', text, count=1
    )
    if version_count != 1:
        raise StampMismatchError(f"{path}: no project.version line")
    if not is_core:
        updated, constraint_count = PYTHON_CORE_CONSTRAINT_PATTERN.subn(
            f'"{PYTHON_CORE_DISTRIBUTION}=={version}"', updated
        )
        if constraint_count != 1:
            raise StampMismatchError(f"{path}: expected one dex-agents==<version> dependency")
    write_or_compare(path, text, updated, check_only)
    project = tomllib.loads(updated)["project"]
    if project["version"] != version:
        raise StampMismatchError(f"{path}: project.version is {project['version']}")


def stamp_uv_lock(path: Path, version: str, check_only: bool) -> None:
    text = path.read_text(encoding="utf-8")
    updated = re.sub(
        r'(?m)^(name = "dex-agents(?:-[a-z-]+)?"\nversion = )"[^"]*"(\nsource = \{ editable = )',
        rf'\g<1>"{version}"\g<2>',
        text,
    )
    write_or_compare(path, text, updated, check_only)


def stamp_package_json(path: Path, version: str, is_core: bool, check_only: bool) -> None:
    text = path.read_text(encoding="utf-8")
    manifest = json.loads(text)
    manifest["version"] = version
    if not is_core:
        peers = manifest.get("peerDependencies", {})
        if TYPESCRIPT_CORE_PACKAGE not in peers:
            raise StampMismatchError(f"{path}: {TYPESCRIPT_CORE_PACKAGE} must be a peerDependency")
        peers[TYPESCRIPT_CORE_PACKAGE] = f"^{version}"
    write_or_compare(path, text, json.dumps(manifest, indent=2) + "\n", check_only)


def stamp_package_lock(path: Path, version: str, is_core: bool, check_only: bool) -> None:
    text = path.read_text(encoding="utf-8")
    lock = json.loads(text)
    lock["version"] = version
    root = lock["packages"][""]
    root["version"] = version
    if not is_core:
        root["peerDependencies"][TYPESCRIPT_CORE_PACKAGE] = f"^{version}"
        local_core = lock["packages"].get(f"node_modules/{TYPESCRIPT_CORE_PACKAGE}")
        if local_core is not None and local_core.get("resolved") == "file:../core":
            local_core["version"] = version
    write_or_compare(path, text, json.dumps(lock, indent=2) + "\n", check_only)


def write_or_compare(path: Path, original: str, updated: str, check_only: bool) -> None:
    if original == updated:
        return
    if check_only:
        raise StampMismatchError(f"{path}: not stamped with the requested version")
    path.write_text(updated, encoding="utf-8")


def stamp_all_packages(version: str, check_only: bool) -> None:
    for package in PYTHON_PACKAGES:
        package_dir = AGENTS_DIR / "python" / package
        stamp_pyproject(package_dir / "pyproject.toml", version, package == "core", check_only)
        stamp_uv_lock(package_dir / "uv.lock", version, check_only)
    for package in TYPESCRIPT_PACKAGES:
        package_dir = AGENTS_DIR / "typescript" / package
        is_core = package == "core"
        stamp_package_json(package_dir / "package.json", version, is_core, check_only)
        stamp_package_lock(package_dir / "package-lock.json", version, is_core, check_only)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version", help="Release version, such as 0.1.0 or 0.2.0-rc.1")
    parser.add_argument("--check", action="store_true", help="Fail instead of writing changes")
    arguments = parser.parse_args()
    if not VERSION_PATTERN.fullmatch(arguments.version):
        print(f"Invalid agents release version: {arguments.version}", file=sys.stderr)
        return 1
    try:
        stamp_all_packages(arguments.version, arguments.check)
    except StampMismatchError as error:
        print(error, file=sys.stderr)
        return 1
    print(arguments.version)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
