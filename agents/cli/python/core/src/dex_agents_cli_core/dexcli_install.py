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
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path, PurePosixPath
from urllib.parse import urlsplit

import httpx2

from dex_agents_cli_core.environment import CommandEnvironment

#: The executables a ``dexcli`` release archive holds; ``dexcli dev`` runs both.
DEXCLI_EXECUTABLES = ("dexcli", "dex-server-zd")
#: The file in an installed release's directory that records each executable's SHA-256.
INSTALL_RECORD_NAME = "install.json"
_DOWNLOAD_TIMEOUT_SECONDS = 300.0
_VERSION_TIMEOUT_SECONDS = 30.0
_MAX_EXECUTABLE_BYTES = 512 * 1024 * 1024
_MAX_REDIRECTS = 5
_LOOPBACK_HOSTS = frozenset({"127.0.0.1", "localhost", "::1"})
_VERSION = re.compile(r"v?(\d+)\.(\d+)\.(\d+)")


class DexcliInstallError(Exception):
    """Report that ``dexcli`` is not installed, cannot be downloaded, or was changed."""


class DexcliVariableError(DexcliInstallError):
    """Report that ``DEXCLI`` names no executable file; the command cannot fall back from it."""


def locate_dexcli(environment: CommandEnvironment) -> Path | None:
    """Return the ``dexcli`` to run without downloading one.

    Args:
        environment: The machine settings.

    Returns:
        ``DEXCLI``; else the downloaded release in ``~/.dex/bin/dexcli-<version>``,
        after checking both executables against their recorded SHA-256; else
        the first ``dexcli`` on ``PATH`` whose version is at least the pinned
        one; else ``None``.

    Raises:
        DexcliVariableError: If ``DEXCLI`` is set but is not an executable file.
        DexcliInstallError: If the downloaded release changed since it was installed.
    """
    if environment.dexcli is not None:
        if not (environment.dexcli.is_file() and os.access(environment.dexcli, os.X_OK)):
            raise DexcliVariableError(
                f"DEXCLI is not an executable file: {environment.dexcli}. Unset DEXCLI to use "
                "the dexcli on PATH or the downloaded release, or set it to the path of a dexcli "
                "executable."
            )
        return environment.dexcli
    installed = installed_release_directory(environment)
    if installed.exists():
        verify_installed_release(installed)
        return installed / "dexcli"
    minimum = _parse_version(environment.dexcli_version)
    for directory in environment.search_path:
        candidate = directory / "dexcli"
        if not (candidate.is_file() and os.access(candidate, os.X_OK)):
            continue
        version = _read_version(candidate)
        if minimum is not None and version is not None and version >= minimum:
            return candidate
    return None


def installed_release_directory(environment: CommandEnvironment) -> Path:
    """Return ``~/.dex/bin/dexcli-<version>``, where the pinned release is installed.

    Args:
        environment: The machine settings.

    Returns:
        The directory, which may not exist.
    """
    return environment.bin_directory / f"dexcli-{environment.dexcli_version}"


def verify_installed_release(directory: Path) -> None:
    """Check an installed release's executables against the SHA-256 recorded at install.

    Args:
        directory: The release's directory.

    Raises:
        DexcliInstallError: If the record is missing or an executable changed.
    """
    try:
        recorded = json.loads((directory / INSTALL_RECORD_NAME).read_text(encoding="utf-8"))
        digests = {name: str(recorded["sha256"][name]) for name in DEXCLI_EXECUTABLES}
    except (OSError, ValueError, KeyError, TypeError) as error:
        raise DexcliInstallError(
            f"{directory} has no valid {INSTALL_RECORD_NAME}; delete it to download dexcli again"
        ) from error
    for name, digest in digests.items():
        path = directory / name
        is_executable = path.is_file() and os.access(path, os.X_OK)
        if path.is_symlink() or not is_executable or _sha256(path) != digest:
            raise DexcliInstallError(
                f"{path} changed since it was installed; delete {directory} to download it again"
            )


def release_asset_name(version: str, system: str, machine: str) -> str:
    """Return the release archive name for a platform.

    Args:
        version: The ``dexcli`` version, such as ``1.45.0``.
        system: ``sys.platform``, such as ``darwin`` or ``linux``.
        machine: ``platform.machine()``, such as ``arm64`` or ``x86_64``.

    Returns:
        Such as ``dexcli_v1.45.0_darwin_arm64.tar.gz``.

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


def download_dexcli(environment: CommandEnvironment) -> Path:
    """Download the pinned ``dexcli`` release, check it, and install it in ``~/.dex/bin``.

    The archive's SHA-256 must match its line in the release's
    ``checksums.txt``. Every archive entry must be a plain relative path
    without ``..``, a backslash, or a NUL, and every entry must be a regular
    file or a directory;
    otherwise nothing is installed. Only ``dexcli`` and ``dex-server-zd`` are
    extracted, into a private staging directory with their SHA-256 recorded,
    which then becomes ``~/.dex/bin/dexcli-<version>`` in one rename.

    Args:
        environment: The machine settings.

    Returns:
        The installed ``dexcli``.

    Raises:
        DexcliInstallError: If the download fails or the archive is not trusted.
    """
    version = environment.dexcli_version
    asset = release_asset_name(version, sys.platform, platform.machine())
    base = f"{environment.dexcli_release_base_url.rstrip('/')}/cli-v{version}"
    target = installed_release_directory(environment)
    environment.bin_directory.mkdir(parents=True, exist_ok=True)
    # mkdtemp creates the staging directory readable only by this user.
    staging = Path(tempfile.mkdtemp(prefix=".dexcli-staging-", dir=environment.bin_directory))
    try:
        checksums = _download(f"{base}/checksums.txt", staging / "checksums.txt")
        expected = _expected_sha256(checksums.read_text(encoding="utf-8"), asset)
        archive = _download(f"{base}/{asset}", staging / asset)
        actual = _sha256(archive)
        if actual != expected:
            raise DexcliInstallError(
                f"{asset} has SHA-256 {actual}, but checksums.txt says {expected}; not installed"
            )
        release = staging / "release"
        release.mkdir(mode=0o700)
        digests = _extract_executables(archive, release)
        record = {
            "version": version,
            "archive": asset,
            "archiveSha256": actual,
            "sha256": digests,
        }
        (release / INSTALL_RECORD_NAME).write_text(json.dumps(record, indent=2), encoding="utf-8")
        release.chmod(0o755)
        if target.exists():
            shutil.rmtree(target)
        os.replace(release, target)
    finally:
        shutil.rmtree(staging, ignore_errors=True)
    return target / "dexcli"


def _download(url: str, destination: Path) -> Path:
    """Download a URL, checking it and every redirect: https, or http on a loopback host."""
    current = url
    try:
        with httpx2.Client(follow_redirects=False, timeout=_DOWNLOAD_TIMEOUT_SECONDS) as client:
            for _ in range(_MAX_REDIRECTS + 1):
                check_download_url(current)
                with client.stream("GET", current) as response:
                    if response.is_redirect:
                        location = response.headers.get("location", "")
                        current = str(httpx2.URL(current).join(location))
                        continue
                    if response.status_code != 200:
                        raise DexcliInstallError(
                            f"cannot download {current}: HTTP {response.status_code}"
                        )
                    with destination.open("wb") as file:
                        for chunk in response.iter_bytes():
                            file.write(chunk)
                    return destination
    except httpx2.HTTPError as error:
        raise DexcliInstallError(f"cannot download {current}: {error}") from error
    raise DexcliInstallError(f"cannot download {url}: more than {_MAX_REDIRECTS} redirects")


def check_download_url(url: str) -> None:
    """Refuse a download URL that is neither https nor http on a loopback host.

    Args:
        url: The URL, or a redirect's target.

    Raises:
        DexcliInstallError: If the URL could be served or changed by another host.
    """
    parts = urlsplit(url)
    is_loopback_http = parts.scheme == "http" and parts.hostname in _LOOPBACK_HOSTS
    if not parts.hostname or not (parts.scheme == "https" or is_loopback_http):
        raise DexcliInstallError(
            f"{url} is not an https URL, or http on 127.0.0.1, localhost, or [::1]; "
            "dexcli is downloaded only over https"
        )


def _expected_sha256(checksums: str, asset: str) -> str:
    pattern = re.compile(rf"^([0-9a-f]{{64}})  (?:\./)?{re.escape(asset)}$", re.MULTILINE)
    match = pattern.search(checksums)
    if match is None:
        raise DexcliInstallError(f"checksums.txt has no entry for {asset}")
    return match.group(1)


def _extract_executables(archive: Path, destination: Path) -> dict[str, str]:
    """Extract the executables after checking every entry; return their SHA-256 by name."""
    digests: dict[str, str] = {}
    try:
        with tarfile.open(archive, "r:gz") as tar:
            members = tar.getmembers()
            for member in members:
                _check_archive_entry(member)
            by_name = {PurePosixPath(member.name).as_posix(): member for member in members}
            for name in DEXCLI_EXECUTABLES:
                executable = by_name.get(name)
                if (
                    executable is None
                    or not executable.isfile()
                    or executable.size > _MAX_EXECUTABLE_BYTES
                ):
                    raise DexcliInstallError(f"the release archive has no regular file {name}")
                source = tar.extractfile(executable)
                assert source is not None, "a regular tar member always has content"
                output = destination / name
                fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o755)
                with source, os.fdopen(fd, "wb") as file:
                    shutil.copyfileobj(source, file)
                digests[name] = _sha256(output)
    except tarfile.TarError as error:
        raise DexcliInstallError(f"the release archive cannot be read: {error}") from error
    return digests


def _check_archive_entry(member: tarfile.TarInfo) -> None:
    path = PurePosixPath(member.name)
    is_unsafe_text = "\\" in member.name or "\x00" in member.name
    if is_unsafe_text or path.is_absolute() or ".." in path.parts:
        raise DexcliInstallError(f"the release archive has an unsafe path {member.name!r}")
    if not (member.isfile() or member.isdir()):
        raise DexcliInstallError(
            f"the release archive entry {member.name!r} is a link or a special file"
        )


def _read_version(executable: Path) -> tuple[int, int, int] | None:
    try:
        completed = subprocess.run(
            [str(executable), "--version"],
            capture_output=True,
            text=True,
            timeout=_VERSION_TIMEOUT_SECONDS,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        return None
    if completed.returncode != 0:
        return None
    return _parse_version(completed.stdout + completed.stderr)


def _parse_version(text: str) -> tuple[int, int, int] | None:
    match = _VERSION.search(text)
    if match is None:
        return None
    return int(match.group(1)), int(match.group(2)), int(match.group(3))


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as file:
        for chunk in iter(lambda: file.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()
