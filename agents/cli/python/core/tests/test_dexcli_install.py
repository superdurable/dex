# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Downloading dexcli: the checksum, the archive's entries, and the installed copy decide."""

from __future__ import annotations

import hashlib
import io
import platform
import sys
import tarfile
import threading
from collections.abc import Callable, Iterator
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from dex_agents_cli_core.dexcli_install import (
    DexcliInstallError,
    download_dexcli,
    locate_dexcli,
    release_asset_name,
)
from dex_agents_cli_core.environment import CommandEnvironment

VERSION = "9.9.9"
ArchiveEntries = Callable[[tarfile.TarFile], None]


def add_file(tar: tarfile.TarFile, name: str, content: bytes) -> None:
    member = tarfile.TarInfo(name)
    member.size = len(content)
    member.mode = 0o644
    tar.addfile(member, io.BytesIO(content))


def release_entries(tar: tarfile.TarFile) -> None:
    for name in ("dexcli", "dex-server-zd", "README.md"):
        add_file(tar, name, f"#!/bin/sh\necho {name}\n".encode())


def write_release(
    directory: Path, entries: ArchiveEntries = release_entries, checksum_of: bytes | None = None
) -> None:
    """Write a fake cli-v<version> release with an archive and checksums.txt."""
    release = directory / f"cli-v{VERSION}"
    release.mkdir(parents=True)
    asset = release_asset_name(VERSION, sys.platform, platform.machine())
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as tar:
        entries(tar)
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

        def do_GET(self) -> None:
            served = root / self.path.lstrip("/")
            if served.is_file() and served.read_bytes() == b"redirect":
                self.send_response(302)
                self.send_header("Location", "http://mirror.invalid/checksums.txt")
                self.end_headers()
                return
            super().do_GET()

        def log_message(self, format: str, *args: object) -> None:
            del format, args

    server = ThreadingHTTPServer(("127.0.0.1", 0), Releases)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}", root
    finally:
        server.shutdown()


def environment_for(
    tmp_path: Path, url: str, search_path: tuple[Path, ...] = ()
) -> CommandEnvironment:
    return CommandEnvironment(
        home=tmp_path / "home",
        dexcli_version=VERSION,
        dexcli_release_base_url=url,
        search_path=search_path,
    )


def test_a_verified_release_is_installed_and_found_again(
    tmp_path: Path, release_server: tuple[str, Path]
) -> None:
    url, root = release_server
    write_release(root)
    environment = environment_for(tmp_path, url)
    installed = download_dexcli(environment)
    directory = tmp_path / "home" / ".dex" / "bin" / f"dexcli-{VERSION}"
    assert installed == directory / "dexcli"
    assert installed.read_text() == "#!/bin/sh\necho dexcli\n"
    assert (directory / "dex-server-zd").stat().st_mode & 0o111
    assert sorted(path.name for path in directory.iterdir()) == [
        "dex-server-zd",
        "dexcli",
        "install.json",
    ]
    assert sorted(path.name for path in directory.parent.iterdir()) == [f"dexcli-{VERSION}"]
    assert locate_dexcli(environment) == installed


def test_a_changed_installed_copy_is_refused(
    tmp_path: Path, release_server: tuple[str, Path]
) -> None:
    url, root = release_server
    write_release(root)
    environment = environment_for(tmp_path, url)
    installed = download_dexcli(environment)
    installed.write_text("#!/bin/sh\necho tampered\n")
    with pytest.raises(DexcliInstallError, match="changed since it was installed"):
        locate_dexcli(environment)


def test_an_installed_copy_that_lost_its_execute_bit_is_refused(
    tmp_path: Path, release_server: tuple[str, Path]
) -> None:
    url, root = release_server
    write_release(root)
    environment = environment_for(tmp_path, url)
    installed = download_dexcli(environment)
    installed.chmod(0o644)
    with pytest.raises(DexcliInstallError, match="changed since it was installed"):
        locate_dexcli(environment)


def test_a_redirect_to_a_remote_http_url_is_refused(
    tmp_path: Path, release_server: tuple[str, Path]
) -> None:
    url, root = release_server
    release = root / f"cli-v{VERSION}"
    release.mkdir(parents=True)
    # The test server answers this path with a redirect to plain http on another host.
    (release / "checksums.txt").write_text("redirect")
    environment = environment_for(tmp_path, url)
    with pytest.raises(DexcliInstallError, match="is not an https URL"):
        download_dexcli(environment)
    assert list((tmp_path / "home" / ".dex" / "bin").iterdir()) == []


def test_a_checksum_mismatch_installs_nothing(
    tmp_path: Path, release_server: tuple[str, Path]
) -> None:
    url, root = release_server
    write_release(root, checksum_of=b"something else")
    environment = environment_for(tmp_path, url)
    with pytest.raises(DexcliInstallError, match="but checksums.txt says"):
        download_dexcli(environment)
    assert list((tmp_path / "home" / ".dex" / "bin").iterdir()) == []


def absolute_entry(tar: tarfile.TarFile) -> None:
    release_entries(tar)
    add_file(tar, "/tmp/dexcli-planted", b"planted")


def parent_entry(tar: tarfile.TarFile) -> None:
    release_entries(tar)
    add_file(tar, "../planted", b"planted")


def symbolic_link_entry(tar: tarfile.TarFile) -> None:
    add_file(tar, "dex-server-zd", b"#!/bin/sh\n")
    link = tarfile.TarInfo("dexcli")
    link.type = tarfile.SYMTYPE
    link.linkname = "/bin/sh"
    tar.addfile(link)


def hard_link_entry(tar: tarfile.TarFile) -> None:
    release_entries(tar)
    link = tarfile.TarInfo("extra")
    link.type = tarfile.LNKTYPE
    link.linkname = "dexcli"
    tar.addfile(link)


@pytest.mark.parametrize(
    ("entries", "message"),
    [
        (absolute_entry, "unsafe path"),
        (parent_entry, "unsafe path"),
        (symbolic_link_entry, "is a link or a special file"),
        (hard_link_entry, "is a link or a special file"),
    ],
    ids=["absolute-path", "parent-path", "symbolic-link", "hard-link"],
)
def test_an_archive_with_an_unsafe_entry_installs_nothing(
    tmp_path: Path, release_server: tuple[str, Path], entries: ArchiveEntries, message: str
) -> None:
    url, root = release_server
    write_release(root, entries)
    environment = environment_for(tmp_path, url)
    with pytest.raises(DexcliInstallError, match=message):
        download_dexcli(environment)
    assert list((tmp_path / "home" / ".dex" / "bin").iterdir()) == []
    assert not (tmp_path / "home" / ".dex" / "planted").exists()


def fake_dexcli(directory: Path, version: str) -> Path:
    directory.mkdir(parents=True)
    executable = directory / "dexcli"
    executable.write_text(f'#!/bin/sh\necho "dexcli v{version} (commit test)"\n')
    executable.chmod(0o755)
    return executable


def test_the_path_search_skips_an_old_dexcli(tmp_path: Path) -> None:
    fake_dexcli(tmp_path / "old", "9.8.0")
    current = fake_dexcli(tmp_path / "new", "9.10.0")
    environment = environment_for(
        tmp_path, "http://127.0.0.1:1", (tmp_path / "old", tmp_path / "new")
    )
    assert locate_dexcli(environment) == current


def test_a_dexcli_whose_version_command_fails_is_skipped(tmp_path: Path) -> None:
    broken = tmp_path / "broken"
    broken.mkdir()
    (broken / "dexcli").write_text('#!/bin/sh\necho "dexcli v9.10.0"\nexit 3\n')
    (broken / "dexcli").chmod(0o755)
    environment = environment_for(tmp_path, "http://127.0.0.1:1", (broken,))
    assert locate_dexcli(environment) is None


def test_only_an_old_dexcli_on_the_path_means_a_download(tmp_path: Path) -> None:
    fake_dexcli(tmp_path / "old", "9.9.8")
    environment = environment_for(tmp_path, "http://127.0.0.1:1", (tmp_path / "old",))
    assert locate_dexcli(environment) is None


def test_dexcli_from_the_environment_wins(tmp_path: Path) -> None:
    dexcli = tmp_path / "custom-dexcli"
    environment = CommandEnvironment(home=tmp_path, dexcli=dexcli)
    assert locate_dexcli(environment) == dexcli


def test_an_unsupported_platform_says_how_to_install(tmp_path: Path) -> None:
    with pytest.raises(DexcliInstallError, match="install dexcli and put it on PATH"):
        release_asset_name(VERSION, "win32", "x86_64")
