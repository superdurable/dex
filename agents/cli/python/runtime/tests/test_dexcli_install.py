# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Downloading dexcli from a release: the checksum decides whether it is installed."""

from __future__ import annotations

import hashlib
import io
import platform
import sys
import tarfile
import threading
from collections.abc import Iterator
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from dex_agents_cli.dexcli_install import (
    DexcliInstallError,
    download_dexcli,
    locate_dexcli,
    release_asset_name,
)
from dex_agents_cli.environment import RuntimeEnvironment

VERSION = "9.9.9"


def write_release(directory: Path, *, checksum_of: bytes | None = None) -> None:
    """Write a fake cli-v<version> release with an archive and checksums.txt."""
    release = directory / f"cli-v{VERSION}"
    release.mkdir(parents=True)
    asset = release_asset_name(VERSION, sys.platform, platform.machine())
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as tar:
        for name in ("dexcli", "dex-server-zd", "README.md"):
            content = f"#!/bin/sh\necho {name}\n".encode()
            member = tarfile.TarInfo(name)
            member.size = len(content)
            member.mode = 0o644
            tar.addfile(member, io.BytesIO(content))
    archive = buffer.getvalue()
    (release / asset).write_bytes(archive)
    digest = hashlib.sha256(checksum_of if checksum_of is not None else archive).hexdigest()
    (release / "checksums.txt").write_text(f"{'0' * 64}  other.tar.gz\n{digest}  {asset}\n")


@pytest.fixture
def release_server(tmp_path: Path) -> Iterator[tuple[str, Path]]:
    root = tmp_path / "releases"
    root.mkdir()

    class Releases(SimpleHTTPRequestHandler):
        def __init__(self, *args: object, **kwargs: object) -> None:
            super().__init__(*args, directory=str(root), **kwargs)  # type: ignore[arg-type]

        def log_message(self, format: str, *args: object) -> None:
            del format, args

    server = ThreadingHTTPServer(("127.0.0.1", 0), Releases)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}", root
    finally:
        server.shutdown()


def test_a_verified_release_is_installed_in_dex_bin(
    tmp_path: Path, release_server: tuple[str, Path]
) -> None:
    url, root = release_server
    write_release(root)
    environment = RuntimeEnvironment(
        home=tmp_path / "home", dexcli_version=VERSION, dexcli_release_base_url=url
    )
    installed = download_dexcli(environment)
    assert installed == tmp_path / "home" / ".dex" / "bin" / "dexcli"
    assert installed.read_text() == "#!/bin/sh\necho dexcli\n"
    assert (installed.parent / "dex-server-zd").stat().st_mode & 0o111
    assert sorted(path.name for path in installed.parent.iterdir()) == ["dex-server-zd", "dexcli"]


def test_a_checksum_mismatch_installs_nothing(
    tmp_path: Path, release_server: tuple[str, Path]
) -> None:
    url, root = release_server
    write_release(root, checksum_of=b"something else")
    environment = RuntimeEnvironment(
        home=tmp_path / "home", dexcli_version=VERSION, dexcli_release_base_url=url
    )
    with pytest.raises(DexcliInstallError, match="but checksums.txt says"):
        download_dexcli(environment)
    assert list((tmp_path / "home" / ".dex" / "bin").iterdir()) == []


def test_dexcli_from_the_environment_wins(tmp_path: Path) -> None:
    dexcli = tmp_path / "custom-dexcli"
    environment = RuntimeEnvironment(home=tmp_path, dexcli=dexcli)
    assert locate_dexcli(environment) == dexcli


def test_an_unsupported_platform_says_how_to_install(tmp_path: Path) -> None:
    with pytest.raises(DexcliInstallError, match="install dexcli and put it on PATH"):
        release_asset_name(VERSION, "win32", "x86_64")
