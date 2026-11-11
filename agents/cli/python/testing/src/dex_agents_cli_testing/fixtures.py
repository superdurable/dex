# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Pytest fixtures: a shared private dexcli dev, model API stubs, and the fake connectors MCP.

Load them with ``pytest_plugins = ["dex_agents_cli_testing.fixtures"]`` in a
harness package's ``tests/conftest.py``.
"""

from __future__ import annotations

import asyncio
import os
import subprocess
import sys
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass
from pathlib import Path

import pytest

from dex_agents.testing import InvocationLog, PrivateDexEngine, find_free_ports
from dex_agents_cli_testing.fake_connectors_mcp import (
    INVOCATION_LOG_NAME,
    READY_LINE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
)
from dex_agents_cli_testing.gemini_api import GeminiApiStub
from dex_agents_cli_testing.responses_api import ResponsesApiStub

_PROCESS_READY_SECONDS = 30.0


@pytest.fixture(scope="session")
async def shared_engine(
    tmp_path_factory: pytest.TempPathFactory,
) -> AsyncIterator[PrivateDexEngine]:
    """Start one private dexcli dev that the commands find on its ports."""
    async with PrivateDexEngine(tmp_path_factory.mktemp("engine")) as engine:
        yield engine


@pytest.fixture
def responses_api() -> Iterator[ResponsesApiStub]:
    """Start the scripted OpenAI Responses API."""
    stub = ResponsesApiStub()
    stub.start()
    try:
        yield stub
    finally:
        stub.stop()


@pytest.fixture
def gemini_api() -> Iterator[GeminiApiStub]:
    """Start the scripted Gemini API."""
    stub = GeminiApiStub()
    stub.start()
    try:
        yield stub
    finally:
        stub.stop()


@dataclass
class FakeConnectorsMcp:
    """A running fake connectors MCP server.

    Attributes:
        port: Its port.
        state_dir: Its invocation log.
    """

    port: int
    state_dir: Path
    process: subprocess.Popen[bytes]

    @property
    def url(self) -> str:
        """Return its MCP endpoint."""
        return f"http://127.0.0.1:{self.port}/mcp"

    def runs(self, tool_name: str) -> int:
        """Return how many times a tool really ran."""
        return InvocationLog(self.state_dir / INVOCATION_LOG_NAME).count(tool_name)


@pytest.fixture
async def fake_mcp(tmp_path: Path) -> AsyncIterator[FakeConnectorsMcp]:
    """Start the fake connectors MCP server on a free port."""
    (port,) = find_free_ports(1)
    state_dir = tmp_path / "fake-mcp"
    state_dir.mkdir()
    log_path = tmp_path / "fake-mcp.log"
    with log_path.open("w") as log:
        process = subprocess.Popen(
            [sys.executable, "-m", "dex_agents_cli_testing.fake_connectors_mcp", str(port)],
            env={**os.environ, STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
            stdout=log,
            stderr=subprocess.STDOUT,
        )
    try:
        await wait_for_text(log_path, READY_LINE)
        yield FakeConnectorsMcp(port=port, state_dir=state_dir, process=process)
    finally:
        process.kill()
        process.wait()


async def wait_for_text(path: Path, text: str, timeout: float = _PROCESS_READY_SECONDS) -> None:
    """Wait until a file contains a text."""
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    while loop.time() < deadline:
        if path.exists() and text in path.read_text(errors="replace"):
            return
        await asyncio.sleep(0.1)
    raise AssertionError(f"{path} never contained {text!r}:\n{path.read_text(errors='replace')}")
