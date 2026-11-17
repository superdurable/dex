#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fail when a tracked file starts dexcli dev without -open=false."""

from __future__ import annotations

import argparse
from pathlib import Path
import re
import subprocess
import sys

SOURCE_SUFFIXES = frozenset(
    {".go", ".java", ".js", ".kt", ".mjs", ".mts", ".py", ".rs", ".sh", ".ts", ".tsx", ".bash"}
)
SHELL_SUFFIXES = frozenset({".sh", ".bash"})
EXCLUDED_PREFIXES = (
    # Its in-process tests disable the browser through testConfig or --open=false.
    "cli/internal/dev/",
    # server-zd names dexcli dev flags in messages but never starts dexcli.
    "server-zd/",
    "docs/",
    "changelog.d/",
    "script/check_dexcli_dev_open_flag",
)
SHELL_NON_COMMAND_PREFIXES = ("#", "echo ", "printf ")
SHELL_DEXCLI_DEV = re.compile(r"dexcli\S*\s+dev(\s|$)", re.IGNORECASE)
# dex-server-zd also takes "dev", so other sources also need a flag only dexcli dev takes.
QUOTED_DEV_ARGUMENT = re.compile(r"""(["'])dev\1""")
DEXCLI_DEV_ONLY_FLAG = re.compile(
    r"""(?<![\w-])--?(web-port|dex-port|zd-data-dir|zd-binary|zd-config|connector-config-dir|flow-rendering-dir)\b"""
)
OPEN_FALSE = re.compile(r"(?<![\w-])--?open=false\b")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    arguments = parser.parse_args()
    violations = find_violations(arguments.root, list_tracked_files(arguments.root))
    for path in violations:
        print(f"{path}: starts dexcli dev without -open=false; tests and scripts must not open a browser.")
    if violations:
        return 1
    print("Every dexcli dev start in tests and scripts passes -open=false.")
    return 0


def list_tracked_files(root: Path) -> list[str]:
    """Returns the repository-relative paths that git tracks under root."""
    output = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z"], check=True, capture_output=True
    ).stdout
    return [path for path in output.decode("utf-8").split("\0") if path]


def find_violations(root: Path, paths: list[str]) -> list[str]:
    """Returns the scanned paths that start dexcli dev without -open=false, in input order."""
    violations = []
    for path in paths:
        if not is_scanned(path):
            continue
        try:
            text = (root / path).read_text(encoding="utf-8")
        except (UnicodeDecodeError, FileNotFoundError):
            # Binary files and paths deleted from the worktree start nothing.
            continue
        if starts_dexcli_dev(path, text) and not OPEN_FALSE.search(text):
            violations.append(path)
    return violations


def is_scanned(path: str) -> bool:
    if path.startswith(EXCLUDED_PREFIXES):
        return False
    name = Path(path).name
    return name == "Makefile" or Path(path).suffix in SOURCE_SUFFIXES


def starts_dexcli_dev(path: str, text: str) -> bool:
    if Path(path).suffix in SHELL_SUFFIXES or Path(path).name == "Makefile":
        for line in text.splitlines():
            stripped = line.lstrip()
            if stripped.startswith(SHELL_NON_COMMAND_PREFIXES) or "--help" in line:
                continue
            if SHELL_DEXCLI_DEV.search(line):
                return True
    return bool(QUOTED_DEV_ARGUMENT.search(text) and DEXCLI_DEV_ONLY_FLAG.search(text))


if __name__ == "__main__":
    sys.exit(main())
