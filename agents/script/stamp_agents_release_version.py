#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Stamp one release version, and the internal dependency constraints, across every agents package."""

from __future__ import annotations

import argparse
import json
import re
import sys
import tomllib
from pathlib import Path

AGENTS_DIR = Path(__file__).resolve().parents[1]
# A "cli/<name>" package lives in agents/cli/<language>/<name>; the others in agents/<language>/<name>.
PYTHON_PACKAGES = (
    "core", "openai-agents", "pydantic-ai", "langgraph", "google-adk", "claude-agent-sdk",
    "cli/runtime", "cli/claude-sdk", "cli/openai-sdk", "cli/pydantic-ai", "cli/deepagents", "cli/google-adk",
    "cli/builtin",
    # Never published, but it pins the runtime, so it carries the release version too.
    "cli/testing",
)
TYPESCRIPT_PACKAGES = (
    "core", "vercel-ai", "openai-agents", "mastra", "claude-agent-sdk", "pi",
    "cli/runtime", "cli/claude-sdk", "cli/openai-sdk", "cli/vercel-ai", "cli/mastra", "cli/builtin",
)
PYTHON_CORE_DISTRIBUTION = "dex-agents"
TYPESCRIPT_CORE_PACKAGE = "@superdurable/dex-agents"
VERSION_PATTERN = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z]+)*$")
PYTHON_INTERNAL_CONSTRAINT_PATTERN = re.compile(r'"(dex-agents(?:-[a-z0-9-]+)?)(\[[a-z0-9,-]+\])?==[^"]*"')
TYPESCRIPT_LOCAL_PACKAGE_KEY_PATTERN = re.compile(r"^node_modules/@superdurable/dex-agents(-[a-z0-9-]+)?$")


class StampMismatchError(Exception):
    """A file does not carry the requested version."""


def package_directory(language: str, package: str) -> Path:
    if package.startswith("cli/"):
        return AGENTS_DIR / "cli" / language / package.removeprefix("cli/")
    return AGENTS_DIR / language / package


def stamp_pyproject(path: Path, version: str, is_core: bool, check_only: bool) -> None:
    text = path.read_text(encoding="utf-8")
    updated, version_count = re.subn(
        r'(?m)^version = "[^"]*"', f'version = "{version}"', text, count=1
    )
    if version_count != 1:
        raise StampMismatchError(f"{path}: no project.version line")
    if not is_core:
        core_constraint_count = sum(
            1 for match in PYTHON_INTERNAL_CONSTRAINT_PATTERN.finditer(updated)
            if match.group(1) == PYTHON_CORE_DISTRIBUTION
        )
        if core_constraint_count != 1:
            raise StampMismatchError(f"{path}: expected one dex-agents==<version> dependency")
        updated = PYTHON_INTERNAL_CONSTRAINT_PATTERN.sub(
            lambda match: f'"{match.group(1)}{match.group(2) or ""}=={version}"', updated
        )
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
        stamp_internal_peers(peers, version)
    write_or_compare(path, text, json.dumps(manifest, indent=2) + "\n", check_only)


def stamp_package_lock(path: Path, version: str, is_core: bool, check_only: bool) -> None:
    text = path.read_text(encoding="utf-8")
    lock = json.loads(text)
    lock["version"] = version
    root = lock["packages"][""]
    root["version"] = version
    if not is_core:
        stamp_internal_peers(root["peerDependencies"], version)
        # The local agents packages this one installs from file: paths carry the release version too.
        for key, locked in lock["packages"].items():
            if TYPESCRIPT_LOCAL_PACKAGE_KEY_PATTERN.match(key) and locked.get("resolved", "").startswith("file:"):
                locked["version"] = version
                stamp_internal_peers(locked.get("peerDependencies", {}), version)
    write_or_compare(path, text, json.dumps(lock, indent=2) + "\n", check_only)


def stamp_internal_peers(peers: dict[str, str], version: str) -> None:
    for name in peers:
        if name == TYPESCRIPT_CORE_PACKAGE or name.startswith(f"{TYPESCRIPT_CORE_PACKAGE}-"):
            peers[name] = f"^{version}"


def write_or_compare(path: Path, original: str, updated: str, check_only: bool) -> None:
    if original == updated:
        return
    if check_only:
        raise StampMismatchError(f"{path}: not stamped with the requested version")
    path.write_text(updated, encoding="utf-8")


def stamp_all_packages(version: str, check_only: bool) -> None:
    for package in PYTHON_PACKAGES:
        package_dir = package_directory("python", package)
        stamp_pyproject(package_dir / "pyproject.toml", version, package == "core", check_only)
        stamp_uv_lock(package_dir / "uv.lock", version, check_only)
    for package in TYPESCRIPT_PACKAGES:
        package_dir = package_directory("typescript", package)
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
