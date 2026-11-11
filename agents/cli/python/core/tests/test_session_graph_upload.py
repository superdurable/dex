# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A session graph upload that never answers only logs a warning."""

from __future__ import annotations

import asyncio
import logging
from pathlib import Path

import pytest
from dex import AsyncClient, BlobCacheConfig, ClientOptions, Registry, open_blob_cache

from dex_agents_cli_core import cli
from tests.scripted_harness import build_session_flow_without_tools

FLOW_TYPE = "Agent-abcdef"


async def test_a_stalled_upload_logs_a_warning_within_its_timeout(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    # The server accepts connections and reads requests but never answers one.
    release = asyncio.Event()
    writers: list[asyncio.StreamWriter] = []

    async def hold(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        writers.append(writer)
        await release.wait()

    server = await asyncio.start_server(hold, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    monkeypatch.setattr(cli, "_SESSION_FLOW_DEFINITION_UPLOAD_TIMEOUT_SECONDS", 0.5)
    blob_cache = open_blob_cache(BlobCacheConfig(str(tmp_path / "cache"), 64 * 1024 * 1024))
    client = AsyncClient(
        Registry([build_session_flow_without_tools(FLOW_TYPE)], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=f"127.0.0.1:{port}"),
    )
    loop = asyncio.get_running_loop()
    try:
        with caplog.at_level(logging.INFO, logger="dex_agents_cli_core.cli"):
            started = loop.time()
            await cli._upload_session_flow_definition(client, FLOW_TYPE)
            elapsed = loop.time() - started
    finally:
        release.set()
        await client.close()
        blob_cache.close()
        for writer in writers:
            writer.close()
        server.close()
        await server.wait_closed()
    assert writers, "the client never connected"
    assert elapsed < 5
    (record,) = [record for record in caplog.records if record.name == "dex_agents_cli_core.cli"]
    assert record.levelno == logging.WARNING
    assert record.getMessage().startswith("session Flow definition upload failed")
    assert f"flowType={FLOW_TYPE} TimeoutError" in record.getMessage()
