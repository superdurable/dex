# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Images in the terminal chat: ``/image <path>``, the media store, and refusals."""

from __future__ import annotations

import asyncio
import hashlib
import struct
import zlib
from pathlib import Path
from typing import Any

from dex_agents import MAX_IMAGE_BYTES, MessageRole
from tests.conftest import CliHome, config_yaml, expect, session_client, wait_for_exit
from tests.scripted_harness import SCRIPTED_FLOW_TYPE, WITHOUT_IMAGES_ENVIRONMENT_VARIABLE
from tests.test_cli_chat import read_session_id

NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}, "local_tools": {"browser": False}}


def png_bytes() -> bytes:
    """Return a 1x1 PNG."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        checksum = struct.pack(">I", zlib.crc32(kind + data))
        return struct.pack(">I", len(data)) + kind + data + checksum

    header = struct.pack(">IIBBBBB", 1, 1, 8, 2, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", header)
        + chunk(b"IDAT", zlib.compress(b"\x00\xff\x00\x00", 9))
        + chunk(b"IEND", b"")
    )


def jpeg_bytes() -> bytes:
    """Return bytes that pass the structural JPEG check: SOI, a marker, and EOI."""
    return b"\xff\xd8\xff\xdb" + bytes(16) + b"\xff\xd9"


def sha256_of(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


async def test_an_attached_image_reaches_the_model_from_the_local_media_store(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    png = png_bytes()
    jpeg = jpeg_bytes()
    (cli_home.home / "chart.png").write_bytes(png)
    photo = tmp_path / "my photos" / "photo.jpg"
    photo.parent.mkdir()
    photo.write_bytes(jpeg)
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "/image <path> attaches an image to the next one")
    await expect(child, "> ")
    child.sendline("/image ~/chart.png")
    await expect(child, f"[image attached] chart.png (image/png, {len(png)} bytes)")
    await expect(child, "(1 image attached)")
    child.sendline(f'/image "{photo}"')
    await expect(child, f"[image attached] photo.jpg (image/jpeg, {len(jpeg)} bytes)")
    await expect(child, "(2 images attached)")
    child.sendline("images")
    await expect(
        child,
        f"saw image {sha256_of(png)} {len(png)}; saw image {sha256_of(jpeg)} {len(jpeg)}",
    )
    await expect(child, "> ")
    # The next message carries no images: they went with the previous one.
    child.sendline("thanks")
    await expect(child, "echo: thanks")
    await expect(child, "> ")
    # The command's Worker answers the snapshot RPC, so read it before the command exits.
    flow_type = SCRIPTED_FLOW_TYPE
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        snapshot = await sessions.get_agent_snapshot(session_id)
    child.sendeof()
    assert await wait_for_exit(child) == 0

    for data in (png, jpeg):
        stored = cli_home.media_directory / "sha256" / sha256_of(data)
        assert stored.read_bytes() == data
    # The store holds a copy: removing the original keeps the image.
    (cli_home.home / "chart.png").unlink()
    assert (cli_home.media_directory / "sha256" / sha256_of(png)).exists()

    user_messages = [message for message in snapshot.messages if message.role is MessageRole.USER]
    assert [message.text for message in user_messages] == ["images", "thanks"]
    first, second = user_messages
    assert [image.sha256 for image in first.images] == [sha256_of(png), sha256_of(jpeg)]
    assert [image.mime_type for image in first.images] == ["image/png", "image/jpeg"]
    media_uri = (cli_home.media_directory / "sha256").as_uri()
    assert all(image.uri.startswith(media_uri) for image in first.images)
    assert list(second.images) == []


async def test_an_invalid_image_file_attaches_nothing(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    (cli_home.home / "notes.txt").write_text("not an image")
    oversized = cli_home.home / "huge.png"
    with oversized.open("wb") as file:
        # A sparse file: the size check must refuse it before reading it.
        file.write(png_bytes())
        file.truncate(MAX_IMAGE_BYTES + 1)
    child = cli_home.spawn()
    await read_session_id(child)
    await expect(child, "> ")
    child.sendline("/image")
    await expect(child, "[image not attached] usage: /image <path>")
    child.sendline("/image ~/missing.png")
    await expect(child, f"[image not attached] {cli_home.home / 'missing.png'} does not exist")
    child.sendline("/image ~/notes.txt")
    await expect(
        child,
        f"[image not attached] {cli_home.home / 'notes.txt'}: image is not PNG, JPEG, GIF, or WebP",
    )
    child.sendline("/image ~/huge.png")
    await expect(
        child,
        f"[image not attached] {oversized} has 3,750,001 bytes; an image may have at most "
        "3,750,000",
    )
    child.sendline("/image ~/")
    await expect(child, f"[image not attached] {cli_home.home} is not a regular file")
    child.sendline("images")
    await expect(child, "saw no images")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    stored = cli_home.media_directory / "sha256"
    assert not stored.exists() or list(stored.iterdir()) == []


async def test_a_harness_without_image_support_refuses_the_message_and_says_so(
    cli_home: CliHome,
) -> None:
    cli_home.extra_environment[WITHOUT_IMAGES_ENVIRONMENT_VARIABLE] = "1"
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    (cli_home.home / "chart.png").write_bytes(png_bytes())
    child = cli_home.spawn()
    await read_session_id(child)
    await expect(child, "> ")
    child.sendline("/image ~/chart.png")
    await expect(child, "[image attached] chart.png")
    child.sendline("what is this?")
    await expect(
        child,
        "[not sent] this agent does not take images. The attached images (1 image) were "
        "removed; send the message again without them.",
    )
    await expect(child, "> ")
    child.sendline("what is this?")
    await expect(child, "echo: what is this?")
    child.sendeof()
    assert await wait_for_exit(child) == 0


async def test_an_s3_media_store_needs_the_s3_extra(cli_home: CliHome) -> None:
    # This environment installs dex-agents without its s3 extra, so boto3 is missing.
    cli_home.write_config(config_yaml({**NO_CONNECTORS, "media": {"store": "s3://agent-media"}}))
    completed = await asyncio.to_thread(cli_home.run)
    assert completed.returncode == 2, completed.stdout + completed.stderr
    assert "media.store s3://agent-media needs the S3 support of dex-agents" in completed.stderr
    assert "pip install 'dex-agents[s3]'" in completed.stderr


async def test_an_endpoint_without_an_s3_store_is_a_configuration_error(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml({"media": {"endpoint": "http://127.0.0.1:9000"}}))
    completed = await asyncio.to_thread(cli_home.run)
    assert completed.returncode == 2
    assert 'schema at "/media" (schema keyword not)' in completed.stderr
