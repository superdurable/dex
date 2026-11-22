#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Stamp one release version, and the internal dependency constraints, across every agents package.

The publishers stamp the PyPI and npm packages on the runner before they build, so their committed version
never changes. Go has no build step: `go get` reads the go.mod of the tagged commit and ignores its replace
directives. So --go stamps only the requires between the agents Go modules, and the release commits that
change before it pushes the tags agents/go/<module>/v<version>.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
import tomllib
from collections.abc import Iterator
from pathlib import Path, PurePosixPath

AGENTS_DIR = Path(__file__).resolve().parents[1]
# A "cli/<name>" package lives in agents/cli/<language>/<name>; the others in agents/<language>/<name>.
PYTHON_PACKAGES = (
    "core", "openai-agents", "pydantic-ai", "langgraph", "google-adk", "claude-agent-sdk",
    "cli/core", "cli/claude-sdk", "cli/openai-sdk", "cli/pydantic-ai", "cli/deepagents", "cli/google-adk",
    "cli/builtin",
    # Never published, but it pins the CLI core, so it carries the release version too.
    "cli/testing",
)
TYPESCRIPT_PACKAGES = (
    "core", "vercel-ai", "openai-agents", "mastra", "claude-agent-sdk", "pi",
    "cli/core", "cli/claude-sdk", "cli/openai-sdk", "cli/vercel-ai", "cli/mastra", "cli/builtin", "cli/pi",
)
PYTHON_CORE_DISTRIBUTION = "dex-agents"
TYPESCRIPT_CORE_PACKAGE = "@superdurable/dex-agents"
VERSION_PATTERN = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z]+)*$")
PYTHON_INTERNAL_CONSTRAINT_PATTERN = re.compile(r'"(dex-agents(?:-[a-z0-9-]+)?)(\[[a-z0-9,-]+\])?==[^"]*"')
TYPESCRIPT_LOCAL_PACKAGE_KEY_PATTERN = re.compile(r"^node_modules/@superdurable/dex-agents(-[a-z0-9-]+)?$")
GO_MODULES_DIR = AGENTS_DIR / "go"
GO_MODULE_PATH_PREFIX = "github.com/superdurable/dex/agents/go/"
# A version a go.mod may require: semantic version 2.0.0 with a "v" prefix and no build metadata.
GO_IDENTIFIER = r"(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
GO_VERSION_PATTERN = re.compile(
    rf"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-{GO_IDENTIFIER}(?:\.{GO_IDENTIFIER})*)?$"
)
# The pseudo-version forms of golang.org/x/mod/module: a commit, not a release.
GO_PSEUDO_VERSION_PATTERN = re.compile(r"^v[0-9]+\.(0\.0-|[0-9]+\.[0-9]+-([^+]*\.)?0\.)[0-9]{14}-[A-Za-z0-9]+$")
GO_MOD_TOKEN_PATTERN = re.compile(r'"(?:[^"\\]|\\.)*"|`[^`]*`|[()]|[^\s()"`]+')


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
            peers[name] = version


def write_or_compare(path: Path, original: str, updated: str, check_only: bool) -> None:
    if original == updated:
        return
    if check_only:
        raise StampMismatchError(f"{path}: not stamped with the requested version")
    path.write_text(updated, encoding="utf-8")


def go_release_version(version: str) -> str:
    go_version = f"v{version}"
    match = GO_VERSION_PATTERN.fullmatch(go_version)
    if match is None or GO_PSEUDO_VERSION_PATTERN.fullmatch(go_version):
        raise StampMismatchError(f"{go_version} is not a Go module release version")
    if int(match.group(1)) >= 2:
        raise StampMismatchError(
            f"{go_version}: the agents Go module paths have no /v{match.group(1)} suffix, "
            "so a release from 2.0.0 on needs new module paths first"
        )
    return go_version


def go_mod_directives(lines: list[str]) -> Iterator[tuple[int, str, list[str]]]:
    """Yields the line index, verb, and unquoted fields of each go.mod directive, one per block entry."""
    block_verb: str | None = None
    for index, line in enumerate(lines):
        fields = [
            token[1:-1] if token[:1] in ('"', "`") else token
            for token in GO_MOD_TOKEN_PATTERN.findall(line.split("//", 1)[0])
        ]
        if not fields:
            continue
        if block_verb is not None:
            if fields == [")"]:
                block_verb = None
            else:
                yield index, block_verb, fields
        elif fields[1:] == ["("]:
            block_verb = fields[0]
        elif fields[1:] != ["(", ")"]:
            yield index, fields[0], fields[1:]


def go_module_path(lines: list[str]) -> str | None:
    return next((fields[0] for _, verb, fields in go_mod_directives(lines) if verb == "module" and fields), None)


def go_mods_missing_from_checkout() -> list[str]:
    """Returns each agents/go/<module>/go.mod that git tracks but the checkout lacks, or none outside git."""
    repository = AGENTS_DIR.parent
    toplevel = subprocess.run(
        ("git", "-C", str(repository), "rev-parse", "--show-toplevel"), capture_output=True, text=True
    )
    if toplevel.returncode != 0 or Path(toplevel.stdout.strip()).resolve() != repository.resolve():
        return []
    tracked = subprocess.run(
        ("git", "-C", str(repository), "ls-files", "-z", "--", "agents/go"), check=True, capture_output=True, text=True
    ).stdout.split("\0")
    return [
        path for path in tracked
        if len(PurePosixPath(path).parts) == 4 and path.endswith("/go.mod") and not (repository / path).is_file()
    ]


def agents_go_modules() -> dict[str, Path]:
    """Maps each agents Go module path to its go.mod; the tag agents/go/<directory>/v<version> resolves it."""
    missing = go_mods_missing_from_checkout()
    if missing:
        raise StampMismatchError(
            "\n".join(
                f"{path}: git tracks it, but this checkout does not have it, as in a sparse checkout; "
                "stamp a checkout with all of agents/go, for example after git sparse-checkout add agents/go"
                for path in missing
            )
        )
    modules: dict[str, Path] = {}
    for go_mod in sorted(GO_MODULES_DIR.glob("*/go.mod")):
        expected = GO_MODULE_PATH_PREFIX + go_mod.parent.name
        declared = go_module_path(go_mod.read_text(encoding="utf-8").split("\n"))
        if declared != expected:
            raise StampMismatchError(f"{go_mod}: module is {declared}, not {expected}")
        modules[expected] = go_mod
    return modules


def stamped_go_mod(path: Path, go_version: str, modules: dict[str, Path]) -> tuple[str, str]:
    """Returns the go.mod text and that text with every agents Go module required at go_version."""
    text = path.read_text(encoding="utf-8")
    lines = text.split("\n")
    directives = list(go_mod_directives(lines))
    replacements: dict[str, list[list[str]]] = {}
    for _, verb, fields in directives:
        if verb == "replace" and fields:
            replacements.setdefault(fields[0], []).append(fields[1:])
    for index, verb, fields in directives:
        if verb != "require" or not fields or not fields[0].startswith(GO_MODULE_PATH_PREFIX):
            continue
        module = fields[0]
        if module not in modules:
            raise StampMismatchError(f"{path}: requires {module}, which is not a module in {GO_MODULES_DIR}")
        local_directory = f"../{modules[module].parent.name}"
        if replacements.get(module) != [["=>", local_directory]]:
            raise StampMismatchError(
                f"{path}: requires {module} without the one replace {module} => {local_directory} "
                "that builds the local module in this repository"
            )
        lines[index], count = re.subn(
            rf'^(\s*(?:require\s+)?["`]?{re.escape(module)}["`]?\s+)\S+', rf"\g<1>{go_version}", lines[index], count=1
        )
        if count != 1:
            raise StampMismatchError(f"{path}:{index + 1}: cannot find the version of {module}")
    return text, "\n".join(lines)


def stamp_go_modules(version: str, check_only: bool) -> None:
    go_version = go_release_version(version)
    modules = agents_go_modules()
    # Every go.mod is checked before any is written, so a refused stamp changes nothing.
    stamped = [(go_mod, *stamped_go_mod(go_mod, go_version, modules)) for go_mod in modules.values()]
    for go_mod, original, updated in stamped:
        write_or_compare(go_mod, original, updated, check_only)


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
    parser.add_argument(
        "--go",
        action="store_true",
        help="Stamp only the requires between the agents Go modules, as v<version>, which the release commits",
    )
    arguments = parser.parse_args()
    if not VERSION_PATTERN.fullmatch(arguments.version):
        print(f"Invalid agents release version: {arguments.version}", file=sys.stderr)
        return 1
    try:
        if arguments.go:
            stamp_go_modules(arguments.version, arguments.check)
        else:
            stamp_all_packages(arguments.version, arguments.check)
    except StampMismatchError as error:
        print(error, file=sys.stderr)
        return 1
    print(arguments.version)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
