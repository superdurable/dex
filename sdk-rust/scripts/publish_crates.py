#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
"""Publish Rust crates in dependency order, skipping versions crates.io already has.

After each upload, wait until the crates.io sparse index lists the version, so
the next crate can resolve it. A rerun after a partial publish therefore
uploads only the crates that are still missing.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
import urllib.error
import urllib.request

USER_AGENT = "superdurable-dex-release (https://github.com/superdurable/dex)"
INDEX_WAIT_SECONDS = 600
INDEX_POLL_SECONDS = 10


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("version")
    parser.add_argument("crates", nargs="+", help="crate names in dependency order")
    arguments = parser.parse_args()
    for crate in arguments.crates:
        if is_version_indexed(crate, arguments.version):
            print(f"{crate} {arguments.version} is already on crates.io; skipping")
            continue
        subprocess.run(["cargo", "publish", "--locked", "--allow-dirty", "-p", crate], check=True)
        wait_until_indexed(crate, arguments.version)
    return 0


def wait_until_indexed(crate: str, version: str) -> None:
    deadline = time.monotonic() + INDEX_WAIT_SECONDS
    while not is_version_indexed(crate, version):
        if time.monotonic() > deadline:
            raise SystemExit(f"{crate} {version} did not appear in the crates.io index within {INDEX_WAIT_SECONDS}s")
        print(f"waiting for {crate} {version} in the crates.io index")
        time.sleep(INDEX_POLL_SECONDS)
    print(f"{crate} {version} is in the crates.io index")


def is_version_indexed(crate: str, version: str) -> bool:
    request = urllib.request.Request(
        f"https://index.crates.io/{sparse_index_path(crate)}",
        headers={"User-Agent": USER_AGENT, "Cache-Control": "no-cache"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            lines = response.read().decode("utf-8").splitlines()
    except urllib.error.HTTPError as error:
        # The sparse index answers 404 for a crate that has never been published.
        if error.code == 404:
            return False
        raise
    return any(json.loads(line).get("vers") == version for line in lines if line.strip())


def sparse_index_path(crate: str) -> str:
    name = crate.lower()
    if len(name) <= 2:
        return f"{len(name)}/{name}"
    if len(name) == 3:
        return f"3/{name[0]}/{name}"
    return f"{name[0:2]}/{name[2:4]}/{name}"


if __name__ == "__main__":
    sys.exit(main())
