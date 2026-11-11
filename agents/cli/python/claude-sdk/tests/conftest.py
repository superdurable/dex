# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fixtures: a shared private dexcli dev, the scripted Messages API, and command processes.

Each command runs ``python -m dex_agents_claude_sdk_cli`` in a pseudo-terminal
with a private home. The bundled Claude Code CLI it starts reaches only the
scripted Messages API: a refused proxy stops everything else, and no
``CLAUDE*`` or ``ANTHROPIC*`` variable of the host reaches it.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import signal
import subprocess
import sys
from collections.abc import AsyncIterator, Iterator, Mapping
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import pexpect
import pytest
from dex import AsyncClient, BlobCacheConfig, ClientOptions, Registry, open_blob_cache

from dex_agents import AgentSessionClient
from dex_agents.testing import (
    DEXCLI_ENVIRONMENT_VARIABLE,
    InvocationLog,
    PrivateDexEngine,
    find_free_ports,
)
from dex_agents_claude_agent_sdk import claude_agent_session_flow
from tests.fake_connectors_mcp import (
    INVOCATION_LOG_NAME,
    READY_LINE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
)
from tests.mock_messages_api import MockMessagesApi

PACKAGE_DIR = Path(__file__).resolve().parents[1]
MOCK_API_KEY = "sk-ant-mock-cli-key"
UNREACHABLE_PROXY = "http://127.0.0.1:9"
CLI_TIMEOUT_SECONDS = 120
_PROCESS_READY_SECONDS = 30.0


@dataclass
class CliHome:
    """A private home for command processes, pointed at one dexcli dev and the mock API.

    Attributes:
        home: The ``HOME`` of every command process.
        dex_port: The Dex port the command reuses.
        web_port: The Dex Web port.
        messages_api: The scripted Messages API the Claude Code CLI calls.
        claude_environment: The Claude variables every process gets: the mock
            URL and, unless a test removes it, the mock API key.
    """

    home: Path
    dex_port: int
    web_port: int
    messages_api: MockMessagesApi
    claude_environment: dict[str, str]
    children: list[pexpect.spawn] = field(default_factory=list)

    @property
    def config_path(self) -> Path:
        """Return ``~/.dex/agents/claude-sdk.yaml``."""
        return self.home / ".dex" / "agents" / "claude-sdk.yaml"

    @property
    def workspace(self) -> Path:
        """Return the default workspace, ``~/dex-agent-workspace``."""
        return self.home / "dex-agent-workspace"

    @property
    def address(self) -> str:
        """Return the Dex Server address."""
        return f"127.0.0.1:{self.dex_port}"

    def write_config(self, values: Mapping[str, Any]) -> None:
        """Write the command's configuration file; JSON is valid YAML."""
        self.config_path.parent.mkdir(parents=True, exist_ok=True)
        self.config_path.write_text(json.dumps(values, indent=2) + "\n", encoding="utf-8")

    def environment(self) -> dict[str, str]:
        """Return the environment of a command process."""
        environment = {
            name: value
            for name, value in os.environ.items()
            if name in ("PATH", "LANG", "LC_ALL", "TMPDIR", DEXCLI_ENVIRONMENT_VARIABLE)
        }
        environment.update(
            HOME=str(self.home),
            DEX_AGENTS_DEX_PORT=str(self.dex_port),
            DEX_AGENTS_WEB_PORT=str(self.web_port),
            CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK="1",
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
            DISABLE_AUTOUPDATER="1",
            DISABLE_TELEMETRY="1",
            DISABLE_ERROR_REPORTING="1",
            HTTPS_PROXY=UNREACHABLE_PROXY,
            HTTP_PROXY=UNREACHABLE_PROXY,
            NO_PROXY="127.0.0.1,localhost",
            PYTHONUNBUFFERED="1",
            TERM="dumb",
        )
        environment.update(self.claude_environment)
        return environment

    def spawn(self, *arguments: str) -> pexpect.spawn:
        """Start the command in a pseudo-terminal."""
        child = pexpect.spawn(
            sys.executable,
            ["-m", "dex_agents_claude_sdk_cli", *arguments],
            cwd=str(PACKAGE_DIR),
            env=self.environment(),
            encoding="utf-8",
            timeout=CLI_TIMEOUT_SECONDS,
            dimensions=(50, 400),
        )
        self.children.append(child)
        return child

    def run(self, *arguments: str, stdin: str = "") -> subprocess.CompletedProcess[str]:
        """Run the command without a terminal, feeding standard input."""
        return subprocess.run(
            [sys.executable, "-m", "dex_agents_claude_sdk_cli", *arguments],
            cwd=PACKAGE_DIR,
            env=self.environment(),
            input=stdin,
            capture_output=True,
            text=True,
            timeout=CLI_TIMEOUT_SECONDS * 2,
            check=False,
        )

    def model_requests(self) -> list[dict[str, Any]]:
        """Return the body of every Messages API request so far."""
        return [request.body for request in self.messages_api.requests if request.body]

    def close(self) -> None:
        """Kill every command process and a dexcli dev one of them started."""
        for child in self.children:
            child.close(force=True)
        state_path = self.home / ".dex" / "agents" / "dexcli-dev.json"
        if state_path.exists():
            with contextlib.suppress(ProcessLookupError, PermissionError):
                os.killpg(json.loads(state_path.read_text())["pid"], signal.SIGKILL)


async def expect(child: pexpect.spawn, pattern: str | list[str], timeout: float = 90) -> int:
    """Wait for terminal output without blocking the event loop."""
    patterns = [pattern] if isinstance(pattern, str) else pattern
    return int(await asyncio.to_thread(child.expect_exact, patterns, timeout=timeout))


async def read_session_id(child: pexpect.spawn) -> str:
    """Wait for the ``Session <id>`` line and return the ID."""
    await asyncio.to_thread(child.expect, r"Session (\S+)\r?\n")
    match = child.match
    assert match is not None
    return str(match.group(1))


async def wait_for_exit(child: pexpect.spawn, timeout: float = 60) -> int:
    """Wait for the command to exit and return its exit code."""
    await asyncio.to_thread(child.expect, pexpect.EOF, timeout=timeout)
    child.close()
    return int(child.exitstatus if child.exitstatus is not None else -1)


@pytest.fixture(scope="session")
async def shared_engine(
    tmp_path_factory: pytest.TempPathFactory,
) -> AsyncIterator[PrivateDexEngine]:
    """Start one private dexcli dev that the commands find on its ports."""
    async with PrivateDexEngine(tmp_path_factory.mktemp("engine")) as engine:
        yield engine


@pytest.fixture
def messages_api() -> Iterator[MockMessagesApi]:
    """Start the scripted Messages API."""
    api = MockMessagesApi()
    api.start()
    try:
        yield api
    finally:
        api.stop()


@pytest.fixture
def cli_home(
    tmp_path: Path, shared_engine: PrivateDexEngine, messages_api: MockMessagesApi
) -> Iterator[CliHome]:
    """Give a test a private home whose commands reuse the shared dexcli dev."""
    home = CliHome(
        home=tmp_path / "home",
        dex_port=shared_engine.dex_port,
        web_port=shared_engine.web_port,
        messages_api=messages_api,
        claude_environment={
            "ANTHROPIC_API_KEY": MOCK_API_KEY,
            "ANTHROPIC_BASE_URL": messages_api.base_url,
        },
    )
    home.home.mkdir()
    yield home
    home.close()


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
            [sys.executable, "-m", "tests.fake_connectors_mcp", str(port)],
            cwd=PACKAGE_DIR,
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


@contextlib.asynccontextmanager
async def session_client(
    address: str, cache_dir: Path, flow_type: str
) -> AsyncIterator[AgentSessionClient]:
    """Open a test-side session client for one of the command's Flow types."""
    flow = claude_agent_session_flow(lambda _: _unused_options(), flow_type=flow_type)
    cache_dir.mkdir(parents=True, exist_ok=True)
    blob_cache = open_blob_cache(BlobCacheConfig(str(cache_dir), 64 * 1024 * 1024))
    client = AsyncClient(
        Registry([flow], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=address),
    )
    try:
        yield AgentSessionClient(client, flow)
    finally:
        await client.close()
        blob_cache.close()


def _unused_options() -> Any:
    raise AssertionError("the test-side client never runs a turn")
