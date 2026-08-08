#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

"""Stamp sdk-python/pyproject.toml project.version for a release build."""

from __future__ import annotations

import argparse
import re
import sys
import tomllib
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("version")
    parser.add_argument(
        "--pyproject",
        type=Path,
        default=Path("pyproject.toml"),
    )
    arguments = parser.parse_args()
    text = arguments.pyproject.read_text()
    updated, count = re.subn(
        r'(?m)^version = "[^"]*"',
        f'version = "{arguments.version}"',
        text,
        count=1,
    )
    if count != 1:
        print("failed to stamp project.version in pyproject.toml", file=sys.stderr)
        return 1
    arguments.pyproject.write_text(updated)
    project_version = tomllib.loads(arguments.pyproject.read_text())["project"][
        "version"
    ]
    if project_version != arguments.version:
        print(
            f"pyproject.toml version {project_version} does not match {arguments.version}",
            file=sys.stderr,
        )
        return 1
    print(project_version)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
