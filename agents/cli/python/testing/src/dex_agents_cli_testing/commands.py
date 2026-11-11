# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Harness command processes with a private home, and terminal helpers."""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import signal
import subprocess
import sys
from collections.abc import AsyncIterator, Mapping
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import pexpect
from dex import AsyncClient, BlobCacheConfig, ClientOptions, Registry, open_blob_cache

from dex_agents import AgentSessionClient
from dex_agents.builtin_loop import (
    AgentDefinition,
    ScriptedModelClient,
    ScriptedModelReply,
    builtin_agent_session_flow,
)
from dex_agents.testing import DEXCLI_ENVIRONMENT_VARIABLE
from dex_agents_cli_testing.model_api import ModelApiStub

UNREACHABLE_PROXY = "http://127.0.0.1:9"
COMMAND_TIMEOUT_SECONDS = 120
APPROVE_PROMPT = "Approve? [y/N, or n <reason>]"


@dataclass
class CommandHome:
    """A private home for one harness command's processes.

    Attributes:
        home: The ``HOME`` of every command process.
        command_module: The module ``python -m`` runs, such as ``dex_agents_builtin_cli``.
        harness_name: The harness name, which names ``~/.dex/agents/<name>.yaml``.
        dex_port: The Dex port the command reuses.
        web_port: The Dex Web port.
        model_api: The model API stub the command's framework calls.
        model_environment: The provider variables every process gets, such as
            the stub's base URL and a fake key.
    """

    home: Path
    command_module: str
    harness_name: str
    dex_port: int
    web_port: int
    model_api: ModelApiStub
    model_environment: dict[str, str]
    children: list[pexpect.spawn] = field(default_factory=list)

    @property
    def config_path(self) -> Path:
        """Return ``~/.dex/agents/<harness name>.yaml``."""
        return self.home / ".dex" / "agents" / f"{self.harness_name}.yaml"

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
        """Return a command process's environment: an allow list, never the host's provider keys.

        A refused proxy stops every request that is not to 127.0.0.1.
        """
        environment = {
            name: value
            for name, value in os.environ.items()
            if name in ("PATH", "LANG", "LC_ALL", "TMPDIR", DEXCLI_ENVIRONMENT_VARIABLE)
        }
        environment.update(
            HOME=str(self.home),
            # A dexcli dev the command starts runs no published Connectors MCP release.
            DEX_CONNECTORS_MCP_BINARY=str(self.home / "dex-connectors-mcp-not-installed"),
            DEX_AGENTS_DEX_PORT=str(self.dex_port),
            DEX_AGENTS_WEB_PORT=str(self.web_port),
            HTTPS_PROXY=UNREACHABLE_PROXY,
            HTTP_PROXY=UNREACHABLE_PROXY,
            NO_PROXY="127.0.0.1,localhost",
            PYTHONUNBUFFERED="1",
            TERM="dumb",
        )
        environment.update(self.model_environment)
        return environment

    def spawn(self, *arguments: str) -> pexpect.spawn:
        """Start the command in a pseudo-terminal."""
        child = pexpect.spawn(
            sys.executable,
            ["-m", self.command_module, *arguments],
            env=self.environment(),
            encoding="utf-8",
            timeout=COMMAND_TIMEOUT_SECONDS,
            dimensions=(50, 400),
        )
        self.children.append(child)
        return child

    def run(self, *arguments: str, stdin: str = "") -> subprocess.CompletedProcess[str]:
        """Run the command without a terminal, feeding standard input."""
        return subprocess.run(
            [sys.executable, "-m", self.command_module, *arguments],
            env=self.environment(),
            input=stdin,
            capture_output=True,
            text=True,
            timeout=COMMAND_TIMEOUT_SECONDS * 2,
            check=False,
        )

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


def call(tool_name: str, arguments: Mapping[str, Any]) -> str:
    """Return the scripted model's command to call one tool."""
    return f"call {tool_name} {json.dumps(arguments)}"


@contextlib.asynccontextmanager
async def session_client(
    address: str, cache_dir: Path, flow_type: str
) -> AsyncIterator[AgentSessionClient]:
    """Open a test-side session client for one of the command's Flow types.

    The client only starts sessions and calls their RPCs, so any agent
    session Flow with the same type serves.
    """
    definition = AgentDefinition(
        name="test-client",
        instructions="",
        model=ScriptedModelClient(lambda _: ScriptedModelReply(text="unused")),
    )
    flow = builtin_agent_session_flow(definition, flow_type=flow_type)
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
