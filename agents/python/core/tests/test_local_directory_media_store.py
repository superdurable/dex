# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""LocalDirectoryMediaStore: the shared location cases, and its files on a real file system."""

from __future__ import annotations

import asyncio
import base64
import json
import os
import stat
from dataclasses import replace
from pathlib import Path
from typing import Any

import pytest

from dex_agents import (
    ImageValidationError,
    LocalDirectoryMediaStore,
    MediaStoreError,
    build_image_reference,
    image_uri,
)

IMAGES_FILE = Path(__file__).resolve().parents[3] / "conformance" / "images.json"
SUITE: dict[str, Any] = json.loads(IMAGES_FILE.read_text(encoding="utf-8"))
LOCATIONS: dict[str, Any] = SUITE["store_locations"]
ERRORS: dict[str, str] = SUITE["local_directory_store_errors"]
IMAGES: dict[str, bytes] = {
    case["name"]: base64.b64decode(case["base64"]) for case in SUITE["images"]
}
PNG = IMAGES["png"]
JPEG = IMAGES["jpeg"]


def image_files(root: Path) -> list[str]:
    directory = root / "sha256"
    return sorted(path.name for path in directory.iterdir()) if directory.exists() else []


def mode_of(path: Path) -> int:
    return stat.S_IMODE(path.lstat().st_mode)


@pytest.mark.parametrize("case", LOCATIONS["local_directory"], ids=lambda case: case["name"])
def test_a_local_directory_store_names_images_by_content(case: dict[str, Any]) -> None:
    store = LocalDirectoryMediaStore(case["root"])

    assert store.store_uri == case["store_uri"]
    assert store.image_path(LOCATIONS["sha256"]) == case["image_path"]
    assert image_uri(store.store_uri, LOCATIONS["sha256"]) == case["image_uri"]


@pytest.mark.parametrize(
    "case", LOCATIONS["local_directory_rejected_roots"], ids=lambda case: case["name"]
)
def test_a_root_that_is_not_a_usable_absolute_path_is_refused(case: dict[str, Any]) -> None:
    with pytest.raises(ValueError) as raised:
        LocalDirectoryMediaStore(case["root"])
    assert str(raised.value) == case["error"]


@pytest.mark.parametrize(
    "case", LOCATIONS["local_directory_too_long"], ids=lambda case: case["name"]
)
def test_a_root_too_long_for_its_image_uris_is_refused(case: dict[str, Any]) -> None:
    with pytest.raises(ValueError) as raised:
        LocalDirectoryMediaStore(case["root"])
    assert str(raised.value) == case["store_error"]


def test_a_bytes_root_is_refused() -> None:
    with pytest.raises(TypeError, match="must be a str path"):
        LocalDirectoryMediaStore(b"/var/agent-media")  # type: ignore[arg-type]


async def test_put_copies_the_bytes_into_private_files_and_get_reads_them_back(
    tmp_path: Path,
) -> None:
    root = tmp_path / "parent" / "media"
    store = LocalDirectoryMediaStore(root)
    assert not root.exists()

    reference = await store.put_image(PNG)

    assert reference.uri == image_uri(store.store_uri, reference.sha256)
    assert reference.uri.startswith("file://")
    assert Path(store.image_path(reference.sha256)).read_bytes() == PNG
    assert image_files(root) == [reference.sha256]
    assert await store.get_image(reference) == PNG
    # A umask can only narrow these modes.
    assert mode_of(tmp_path / "parent") == 0o700
    assert mode_of(root) == 0o700
    assert mode_of(root / "sha256") == 0o700
    assert mode_of(Path(store.image_path(reference.sha256))) == 0o600
    with pytest.raises(ImageValidationError, match="not PNG, JPEG, GIF, or WebP"):
        await store.put_image(b"<svg/>")
    assert image_files(root) == [reference.sha256]


async def test_a_repeated_put_keeps_the_existing_file(tmp_path: Path) -> None:
    store = LocalDirectoryMediaStore(tmp_path / "media")
    first = await store.put_image(PNG)
    path = Path(store.image_path(first.sha256))
    inode = path.stat().st_ino

    second = await LocalDirectoryMediaStore(tmp_path / "media").put_image(PNG)

    assert second == first
    assert path.stat().st_ino == inode
    assert image_files(tmp_path / "media") == [first.sha256]


async def test_concurrent_puts_of_the_same_bytes_leave_one_file(tmp_path: Path) -> None:
    stores = [LocalDirectoryMediaStore(tmp_path / "media") for _ in range(2)]

    references = await asyncio.gather(*(stores[index % 2].put_image(JPEG) for index in range(16)))

    assert len(set(references)) == 1
    # No temporary file is left behind.
    assert image_files(tmp_path / "media") == [references[0].sha256]
    assert await stores[0].get_image(references[0]) == JPEG


async def test_a_tampered_file_is_detected_on_read_and_replaced_by_a_put(
    tmp_path: Path,
) -> None:
    store = LocalDirectoryMediaStore(tmp_path / "media")
    reference = await store.put_image(PNG)
    path = Path(store.image_path(reference.sha256))
    mismatch = f"image {reference.uri} does not match its sha256 and size"

    path.write_bytes(PNG + b"\x00")
    with pytest.raises(MediaStoreError) as longer:
        await store.get_image(reference)
    flipped = bytearray(PNG)
    flipped[40] ^= 0xFF
    path.write_bytes(bytes(flipped))
    with pytest.raises(MediaStoreError) as same_size:
        await store.get_image(reference)

    assert str(longer.value) == mismatch
    assert str(same_size.value) == mismatch
    assert await store.put_image(PNG) == reference
    assert await store.get_image(reference) == PNG


async def test_a_missing_file_or_root_is_not_in_the_media_store(tmp_path: Path) -> None:
    store = LocalDirectoryMediaStore(tmp_path / "media")
    await store.put_image(JPEG)
    never_stored = build_image_reference(PNG, store.store_uri)
    absent_store = LocalDirectoryMediaStore(tmp_path / "absent")
    absent = build_image_reference(PNG, absent_store.store_uri)
    missing = ERRORS["missing"]

    with pytest.raises(MediaStoreError) as no_file:
        await store.get_image(never_stored)
    with pytest.raises(MediaStoreError) as no_root:
        await absent_store.get_image(absent)

    assert str(no_file.value) == missing.format(uri=never_stored.uri)
    assert str(no_root.value) == missing.format(uri=absent.uri)


async def test_a_reference_outside_the_root_is_refused(tmp_path: Path) -> None:
    store = LocalDirectoryMediaStore(tmp_path / "media")
    reference = await store.put_image(PNG)
    elsewhere = LocalDirectoryMediaStore(tmp_path / "elsewhere")
    await elsewhere.put_image(PNG)

    for uri in (
        image_uri(elsewhere.store_uri, reference.sha256),
        image_uri(f"file://{tmp_path}/media/..", reference.sha256),
        image_uri(f"s3://bucket{tmp_path}/media", reference.sha256),
    ):
        with pytest.raises(MediaStoreError) as raised:
            await store.get_image(replace(reference, uri=uri))
        assert str(raised.value) == f"image {uri} is not in the media store {store.store_uri}"


async def test_symbolic_links_under_the_root_are_refused(tmp_path: Path) -> None:
    outside = LocalDirectoryMediaStore(tmp_path / "outside")
    outside_reference = await outside.put_image(PNG)
    root = tmp_path / "media"
    store = LocalDirectoryMediaStore(root)
    await store.put_image(JPEG)
    reference = replace(outside_reference, uri=image_uri(store.store_uri, outside_reference.sha256))
    link = Path(store.image_path(reference.sha256))
    link.symlink_to(outside.image_path(reference.sha256))
    not_regular_file = ERRORS["not_regular_file"].format(uri=reference.uri)

    with pytest.raises(MediaStoreError) as on_read:
        await store.get_image(reference)
    with pytest.raises(MediaStoreError) as on_write:
        await store.put_image(PNG)

    assert str(on_read.value) == not_regular_file
    assert str(on_write.value) == not_regular_file
    assert link.is_symlink()

    linked_root = tmp_path / "linked-directory"
    linked_root.mkdir()
    (linked_root / "sha256").symlink_to(tmp_path / "outside" / "sha256")
    linked_store = LocalDirectoryMediaStore(linked_root)
    linked_reference = replace(
        outside_reference, uri=image_uri(linked_store.store_uri, outside_reference.sha256)
    )
    not_directory = ERRORS["not_directory"].format(path=f"{linked_root}/sha256")
    with pytest.raises(MediaStoreError) as directory_on_read:
        await linked_store.get_image(linked_reference)
    with pytest.raises(MediaStoreError) as directory_on_write:
        await linked_store.put_image(JPEG)
    assert str(directory_on_read.value) == not_directory
    assert str(directory_on_write.value) == not_directory
    assert image_files(tmp_path / "outside") == [outside_reference.sha256]


async def test_a_special_file_under_the_root_is_refused_without_blocking(tmp_path: Path) -> None:
    store = LocalDirectoryMediaStore(tmp_path / "media")
    await store.put_image(JPEG)
    reference = build_image_reference(PNG, store.store_uri)
    os.mkfifo(store.image_path(reference.sha256))

    with pytest.raises(MediaStoreError) as raised:
        await asyncio.wait_for(store.get_image(reference), timeout=10)

    assert str(raised.value) == ERRORS["not_regular_file"].format(uri=reference.uri)


async def test_the_root_itself_may_be_a_symbolic_link(tmp_path: Path) -> None:
    (tmp_path / "target").mkdir()
    (tmp_path / "link").symlink_to(tmp_path / "target")
    store = LocalDirectoryMediaStore(tmp_path / "link")

    reference = await store.put_image(PNG)

    assert store.root == str(tmp_path / "link")
    assert image_files(tmp_path / "target") == [reference.sha256]
    assert await store.get_image(reference) == PNG


async def test_a_file_with_another_hard_link_is_refused(tmp_path: Path) -> None:
    outside = tmp_path / "outside.png"
    outside.write_bytes(PNG)
    store = LocalDirectoryMediaStore(tmp_path / "media")
    await store.put_image(JPEG)
    reference = build_image_reference(PNG, store.store_uri)
    os.link(outside, store.image_path(reference.sha256))
    hard_link = ERRORS["hard_link"].format(uri=reference.uri)

    with pytest.raises(MediaStoreError) as on_read:
        await store.get_image(reference)
    with pytest.raises(MediaStoreError) as on_write:
        await store.put_image(PNG)

    assert str(on_read.value) == hard_link
    assert str(on_write.value) == hard_link
    assert outside.stat().st_nlink == 2


async def test_the_first_put_syncs_the_parent_of_every_directory_it_creates(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    synced: set[int] = set()
    fsync = os.fsync

    def recording_fsync(fd: int) -> None:
        synced.add(os.fstat(fd).st_ino)
        fsync(fd)

    monkeypatch.setattr(os, "fsync", recording_fsync)
    root = tmp_path / "parent" / "media"
    store = LocalDirectoryMediaStore(root)

    reference = await store.put_image(PNG)

    created = [tmp_path / "parent", root, root / "sha256"]
    assert {path.parent.stat().st_ino for path in created} <= synced
    # The sha256 directory after the rename, and the image file itself.
    assert (root / "sha256").stat().st_ino in synced
    assert Path(store.image_path(reference.sha256)).stat().st_ino in synced
