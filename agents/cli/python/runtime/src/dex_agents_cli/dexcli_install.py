# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Find ``dexcli``, or download a release with its checksum into ``~/.dex/bin``."""

from __future__ import annotations

import hashlib
import os
import platform
import re
import shutil
import sys
import tarfile
import tempfile
from pathlib import Path

import httpx2

from dex_agents_cli.environment import RuntimeEnvironment

#: The executables a ``dexcli`` release archive holds; ``dexcli dev`` runs both.
DEXCLI_EXECUTABLES = ("dexcli", "dex-server-zd")
_DOWNLOAD_TIMEOUT_SECONDS = 300.0
_MAX_EXECUTABLE_BYTES = 512 * 1024 * 1024


class DexcliInstallError(Exception):
    """Report that ``dexcli`` is not installed and cannot be downloaded."""


def locate_dexcli(environment: RuntimeEnvironment) -> Path | None:
    """Return the ``dexcli`` to run without downloading one.

    Args:
        environment: The machine settings.

    Returns:
        ``DEXCLI``, else ``dexcli`` on ``PATH``, else ``~/.dex/bin/dexcli``,
        or ``None`` when there is none.
    """
    if environment.dexcli is not None:
        return environment.dexcli
    on_path = shutil.which("dexcli")
    if on_path is not None:
        return Path(on_path)
    downloaded = environment.bin_directory / "dexcli"
    return downloaded if downloaded.is_file() and os.access(downloaded, os.X_OK) else None


def release_asset_name(version: str, system: str, machine: str) -> str:
    """Return the release archive name for a platform.

    Args:
        version: The ``dexcli`` version, such as ``1.42.0``.
        system: ``sys.platform``, such as ``darwin`` or ``linux``.
        machine: ``platform.machine()``, such as ``arm64`` or ``x86_64``.

    Returns:
        Such as ``dexcli_v1.42.0_darwin_arm64.tar.gz``.

    Raises:
        DexcliInstallError: If no release is built for the platform.
    """
    operating_system = {"darwin": "darwin", "linux": "linux"}.get(system)
    architecture = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64", "amd64": "amd64"}.get(
        machine.lower()
    )
    if operating_system is None or architecture is None:
        raise DexcliInstallError(
            f"no dexcli release is built for {system}/{machine}; install dexcli and put it on PATH"
        )
    return f"dexcli_v{version}_{operating_system}_{architecture}.tar.gz"


def download_dexcli(environment: RuntimeEnvironment) -> Path:
    """Download the pinned ``dexcli`` release, check its SHA-256, and install it in ``~/.dex/bin``.

    The archive's checksum must match its line in the release's
    ``checksums.txt``. Only ``dexcli`` and ``dex-server-zd`` are extracted,
    and each is moved into place atomically.

    Args:
        environment: The machine settings.

    Returns:
        The installed ``dexcli``.

    Raises:
        DexcliInstallError: If the download fails or the checksum does not match.
    """
    version = environment.dexcli_version
    asset = release_asset_name(version, sys.platform, platform.machine())
    base = f"{environment.dexcli_release_base_url.rstrip('/')}/cli-v{version}"
    target = environment.bin_directory
    target.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".dexcli-download-", dir=target) as scratch:
        scratch_path = Path(scratch)
        checksums = _download(f"{base}/checksums.txt", scratch_path / "checksums.txt")
        expected = _expected_sha256(checksums.read_text(encoding="utf-8"), asset)
        archive = _download(f"{base}/{asset}", scratch_path / asset)
        actual = hashlib.sha256(archive.read_bytes()).hexdigest()
        if actual != expected:
            raise DexcliInstallError(
                f"{asset} has SHA-256 {actual}, but checksums.txt says {expected}; not installed"
            )
        extracted = _extract_executables(archive, scratch_path / "extracted")
        for name in DEXCLI_EXECUTABLES:
            os.replace(extracted / name, target / name)
    return target / "dexcli"


def _download(url: str, destination: Path) -> Path:
    try:
        with (
            httpx2.Client(follow_redirects=True, timeout=_DOWNLOAD_TIMEOUT_SECONDS) as client,
            client.stream("GET", url) as response,
        ):
            if response.status_code != 200:
                raise DexcliInstallError(f"cannot download {url}: HTTP {response.status_code}")
            with destination.open("wb") as file:
                for chunk in response.iter_bytes():
                    file.write(chunk)
    except httpx2.HTTPError as error:
        raise DexcliInstallError(f"cannot download {url}: {error}") from error
    return destination


def _expected_sha256(checksums: str, asset: str) -> str:
    pattern = re.compile(rf"^([0-9a-f]{{64}})  (?:\./)?{re.escape(asset)}$", re.MULTILINE)
    match = pattern.search(checksums)
    if match is None:
        raise DexcliInstallError(f"checksums.txt has no entry for {asset}")
    return match.group(1)


def _extract_executables(archive: Path, destination: Path) -> Path:
    destination.mkdir()
    with tarfile.open(archive, "r:gz") as tar:
        members = {member.name.removeprefix("./"): member for member in tar.getmembers()}
        for name in DEXCLI_EXECUTABLES:
            member = members.get(name)
            if member is None or not member.isfile() or member.size > _MAX_EXECUTABLE_BYTES:
                raise DexcliInstallError(f"the release archive has no regular file {name}")
            source = tar.extractfile(member)
            assert source is not None, "a regular tar member always has content"
            output = destination / name
            with source, output.open("wb") as file:
                shutil.copyfileobj(source, file)
            output.chmod(0o755)
    return destination
