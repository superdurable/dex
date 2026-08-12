#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

"""Stamp Rust workspace package metadata for a release build."""

from __future__ import annotations

import argparse
import re
import sys
import tomllib
from pathlib import Path


PUBLISHED_DEPENDENCIES = ("dex-blob-cache", "dex-protocol")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("version")
    parser.add_argument(
        "--workspace",
        type=Path,
        default=Path.cwd(),
    )
    parser.add_argument("--stage-protocol", action="store_true")
    arguments = parser.parse_args()
    manifest_path = arguments.workspace / "Cargo.toml"
    lock_path = arguments.workspace / "Cargo.lock"

    manifest_text = manifest_path.read_text()
    manifest_text = replace_workspace_version(manifest_text, arguments.version)
    for dependency in PUBLISHED_DEPENDENCIES:
        manifest_text = replace_dependency_version(
            manifest_text, dependency, arguments.version
        )
    manifest_path.write_text(manifest_text)

    workspace_packages = read_workspace_packages(arguments.workspace, manifest_text)
    lock_text = lock_path.read_text()
    for package in workspace_packages:
        lock_text = replace_lock_version(lock_text, package, arguments.version)
    lock_path.write_text(lock_text)

    if arguments.stage_protocol:
        stage_protocol(arguments.workspace)

    return validate_versions(
        manifest_path,
        lock_path,
        workspace_packages,
        arguments.version,
    )


def replace_workspace_version(text: str, version: str) -> str:
    pattern = r'(?ms)(\[workspace\.package\].*?^version = ")[^"]+("$)'
    return replace_once(text, pattern, rf"\g<1>{version}\g<2>", "workspace version")


def replace_dependency_version(text: str, dependency: str, version: str) -> str:
    pattern = (
        rf'(?m)^({re.escape(dependency)} = \{{[^\n]*version = ")'
        r'[^"]+("[^\n]*\})$'
    )
    return replace_once(
        text,
        pattern,
        rf"\g<1>{version}\g<2>",
        f"{dependency} dependency version",
    )


def replace_lock_version(text: str, package: str, version: str) -> str:
    pattern = rf'(?m)(^name = "{re.escape(package)}"\nversion = ")[^"]+("$)'
    return replace_once(
        text,
        pattern,
        rf"\g<1>{version}\g<2>",
        f"{package} lock version",
    )


def replace_once(text: str, pattern: str, replacement: str, field: str) -> str:
    updated, count = re.subn(pattern, replacement, text, count=1)
    if count != 1:
        raise ValueError(f"failed to stamp {field}")
    return updated


def read_workspace_packages(workspace: Path, manifest_text: str) -> tuple[str, ...]:
    manifest = tomllib.loads(manifest_text)
    packages = []
    for member in manifest["workspace"]["members"]:
        member_manifest = tomllib.loads((workspace / member / "Cargo.toml").read_text())
        if member_manifest["package"]["version"].get("workspace") is True:
            packages.append(member_manifest["package"]["name"])
    return tuple(packages)


def stage_protocol(workspace: Path) -> None:
    source = workspace.parent / "protos" / "dex.proto"
    destination = workspace / "crates" / "dex-protocol" / "proto" / "dex.proto"
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(source.read_bytes())


def validate_versions(
    manifest_path: Path,
    lock_path: Path,
    workspace_packages: tuple[str, ...],
    expected: str,
) -> int:
    manifest = tomllib.loads(manifest_path.read_text())
    actual_versions = {"workspace": manifest["workspace"]["package"]["version"]}
    for dependency in PUBLISHED_DEPENDENCIES:
        actual_versions[dependency] = manifest["workspace"]["dependencies"][dependency][
            "version"
        ]

    lock = tomllib.loads(lock_path.read_text())
    lock_versions = {
        package["name"]: package["version"]
        for package in lock["package"]
        if package["name"] in workspace_packages
    }
    actual_versions.update(lock_versions)

    mismatches = {
        name: version for name, version in actual_versions.items() if version != expected
    }
    missing = set(workspace_packages) - lock_versions.keys()
    if mismatches or missing:
        print(
            f"release version validation failed: mismatches={mismatches}, missing={missing}",
            file=sys.stderr,
        )
        return 1
    print(expected)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (KeyError, OSError, TypeError, ValueError, tomllib.TOMLDecodeError) as error:
        print(error, file=sys.stderr)
        raise SystemExit(1) from error
