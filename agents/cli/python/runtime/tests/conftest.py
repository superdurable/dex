# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fixtures: a shared private dexcli dev, CLI processes with their own home, and fake servers."""

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
from tests.fake_connectors_mcp import (
    INVOCATION_LOG_NAME,
    READY_LINE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
)
from tests.scripted_harness import build_session_flow_without_tools

PACKAGE_DIR = Path(__file__).resolve().parents[1]
CLI_TIMEOUT_SECONDS = 90
_PROCESS_READY_SECONDS = 30.0


def playwright_browsers_path() -> str:
    """Return the shared Playwright browser cache, outside the tests' private homes."""
    configured = os.environ.get("PLAYWRIGHT_BROWSERS_PATH")
    if configured:
        return configured
    if sys.platform == "darwin":
        return str(Path.home() / "Library" / "Caches" / "ms-playwright")
    return str(Path.home() / ".cache" / "ms-playwright")


@dataclass
class CliHome:
    """A private home for CLI processes, pointed at one dexcli dev's ports.

    Attributes:
        home: The ``HOME`` of every CLI process.
        dex_port: The Dex port the CLI probes for and passes to ``dexcli dev``.
        web_port: The Dex Web port.
        opened_urls: The file the fake ``BROWSER`` appends each opened URL to.
    """

    home: Path
    dex_port: int
    web_port: int
    opened_urls: Path
    children: list[pexpect.spawn] = field(default_factory=list)

    @property
    def config_path(self) -> Path:
        """Return ``~/.dex/agents/scripted.yaml``."""
        return self.home / ".dex" / "agents" / "scripted.yaml"

    @property
    def address(self) -> str:
        """Return the Dex Server address."""
        return f"127.0.0.1:{self.dex_port}"

    def write_config(self, text: str) -> None:
        """Write the scripted command's configuration file."""
        self.config_path.parent.mkdir(parents=True, exist_ok=True)
        self.config_path.write_text(text, encoding="utf-8")

    def environment(self) -> dict[str, str]:
        """Return the environment of a CLI process."""
        environment = {
            name: value
            for name, value in os.environ.items()
            if name
            in ("PATH", "LANG", "LC_ALL", "TMPDIR", "SYSTEMROOT", DEXCLI_ENVIRONMENT_VARIABLE)
        }
        environment.update(
            HOME=str(self.home),
            DEX_AGENTS_DEV_DEX_PORT=str(self.dex_port),
            DEX_AGENTS_DEV_WEB_PORT=str(self.web_port),
            BROWSER=str(PACKAGE_DIR / "tests" / "record_browser_url.sh"),
            DEX_AGENTS_TEST_OPENED_URLS=str(self.opened_urls),
            PLAYWRIGHT_BROWSERS_PATH=playwright_browsers_path(),
            PYTHONUNBUFFERED="1",
            TERM="dumb",
        )
        return environment

    def spawn(self, *arguments: str) -> pexpect.spawn:
        """Start the scripted command in a pseudo-terminal."""
        child = pexpect.spawn(
            sys.executable,
            ["-m", "tests.scripted_cli", *arguments],
            cwd=str(PACKAGE_DIR),
            env=self.environment(),
            encoding="utf-8",
            timeout=CLI_TIMEOUT_SECONDS,
            dimensions=(50, 400),
        )
        self.children.append(child)
        return child

    def run(self, *arguments: str, stdin: str = "") -> subprocess.CompletedProcess[str]:
        """Run the scripted command without a terminal, feeding standard input."""
        return subprocess.run(
            [sys.executable, "-m", "tests.scripted_cli", *arguments],
            cwd=PACKAGE_DIR,
            env=self.environment(),
            input=stdin,
            capture_output=True,
            text=True,
            timeout=CLI_TIMEOUT_SECONDS * 2,
            check=False,
        )

    def stop_started_dev_server(self) -> None:
        """Kill the dexcli dev that a CLI process started, if any."""
        state_path = self.home / ".dex" / "agents" / "dexcli-dev.json"
        if not state_path.exists():
            return
        pid = json.loads(state_path.read_text())["pid"]
        with contextlib.suppress(ProcessLookupError, PermissionError):
            os.killpg(pid, signal.SIGKILL)

    def close(self) -> None:
        """Kill every CLI process and a dexcli dev they started."""
        for child in self.children:
            child.close(force=True)
        self.stop_started_dev_server()


async def expect(child: pexpect.spawn, pattern: str | list[str], timeout: float = 60) -> int:
    """Wait for terminal output without blocking the event loop.

    Args:
        child: The CLI process.
        pattern: A literal text or several.
        timeout: Seconds to wait.

    Returns:
        The index of the pattern that matched.
    """
    patterns = [pattern] if isinstance(pattern, str) else pattern
    return int(await asyncio.to_thread(child.expect_exact, patterns, timeout=timeout))


async def wait_for_exit(child: pexpect.spawn, timeout: float = 60) -> int:
    """Wait for the CLI process to exit and return its exit code."""
    await asyncio.to_thread(child.expect, pexpect.EOF, timeout=timeout)
    child.close()
    return int(child.exitstatus if child.exitstatus is not None else -1)


@pytest.fixture(scope="session")
async def shared_engine(
    tmp_path_factory: pytest.TempPathFactory,
) -> AsyncIterator[PrivateDexEngine]:
    """Start one private dexcli dev that the CLI processes find on its ports."""
    async with PrivateDexEngine(tmp_path_factory.mktemp("engine")) as engine:
        yield engine


@pytest.fixture
def cli_home(tmp_path: Path, shared_engine: PrivateDexEngine) -> Iterator[CliHome]:
    """Give a test a private home whose CLI processes reuse the shared dexcli dev."""
    home = CliHome(
        home=tmp_path / "home",
        dex_port=shared_engine.dex_port,
        web_port=shared_engine.web_port,
        opened_urls=tmp_path / "opened-urls.txt",
    )
    home.home.mkdir()
    yield home
    home.close()


@dataclass(frozen=True)
class FakeConnectorsMcp:
    """A running fake connectors MCP server.

    Attributes:
        url: Its MCP endpoint.
        invocations: The log of real tool runs.
    """

    url: str
    invocations: InvocationLog

    def runs(self, tool_name: str) -> int:
        """Return how many times a tool really ran."""
        return sum(1 for name in self.invocations.names() if name.split(" ")[0] == tool_name)


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
        yield FakeConnectorsMcp(
            url=f"http://127.0.0.1:{port}/mcp",
            invocations=InvocationLog(state_dir / INVOCATION_LOG_NAME),
        )
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
async def session_client(address: str, cache_dir: Path) -> AsyncIterator[AgentSessionClient]:
    """Open a test-side session client for the scripted harness's Flow type."""
    flow = build_session_flow_without_tools()
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


def config_yaml(values: Mapping[str, Any]) -> str:
    """Return configuration file text for some values; JSON is valid YAML."""
    return json.dumps(values, indent=2) + "\n"
