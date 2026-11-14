# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run ``dexcli connectors mcp``, which serves this machine's Connector tools."""

from __future__ import annotations

import asyncio
import contextlib
import json
import subprocess
import sys
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from urllib.parse import urlsplit

from dex_agents_cli_core.environment import CommandEnvironment

#: The first ``dexcli`` release with ``dexcli connectors mcp``.
CONNECTORS_MCP_COMMAND_MINIMUM_DEXCLI_VERSION = "1.47.7"

# The first start may download the Connectors MCP server and retry five failed starts.
_READY_TIMEOUT_SECONDS = 120.0
_STDIN_CLOSE_WAIT_SECONDS = 10.0
_KILL_WAIT_SECONDS = 5.0
# dexcli answers an unknown command with this usage exit code.
_USAGE_EXIT_CODE = 2
_LOOPBACK_HOSTS = ("127.0.0.1", "localhost", "::1")


class LocalConnections(Enum):
    """Whether this machine's ``connections.json`` holds any connection."""

    MISSING = "missing"
    EMPTY = "empty"
    PRESENT = "present"


def read_local_connections(environment: CommandEnvironment) -> LocalConnections:
    """Read ``~/.dex/connectors/connections.json``, which the Dex Web Connectors page writes.

    Returns:
        ``MISSING`` without the file, ``EMPTY`` when it lists no connection,
        else ``PRESENT``, also for a file the Connectors MCP server must judge.
    """
    path = environment.dex_directory / "connectors" / "connections.json"
    try:
        content = path.read_text(encoding="utf-8")
    except OSError:
        return LocalConnections.MISSING
    try:
        parsed = json.loads(content)
    except ValueError:
        return LocalConnections.PRESENT
    if isinstance(parsed, dict) and parsed.get("connections") == []:
        return LocalConnections.EMPTY
    return LocalConnections.PRESENT


def connectors_mcp_command_log_path(environment: CommandEnvironment) -> Path:
    """Return ``~/.dex/agents/dexcli-connectors-mcp.log``, the last run's stderr."""
    return environment.agents_directory / "dexcli-connectors-mcp.log"


class StartedConnectorsMcpCommand:
    """A ``dexcli connectors mcp`` this command started.

    Stopping it stops a server it started, never one it reused.
    """

    def __init__(self, process: subprocess.Popen[bytes]) -> None:
        """Own a spawned ``dexcli connectors mcp`` with a piped stdin."""
        self._process: subprocess.Popen[bytes] | None = process

    def stop(self) -> None:
        """Stop it once: close its stdin, then SIGTERM, then SIGKILL."""
        process, self._process = self._process, None
        if process is None:
            return
        if process.stdin is not None:
            # It may have exited already; closing a broken pipe changes nothing then.
            with contextlib.suppress(OSError):
                process.stdin.close()
        if _waits_for_exit(process, _STDIN_CLOSE_WAIT_SECONDS):
            return
        process.terminate()
        if _waits_for_exit(process, _KILL_WAIT_SECONDS):
            return
        process.kill()
        _waits_for_exit(process, _KILL_WAIT_SECONDS)


@dataclass(frozen=True)
class ConnectorsMcpCommandStart:
    """How ``dexcli connectors mcp`` ended its startup.

    Attributes:
        url: The loopback ``/mcp`` URL when it is ready, else ``None``.
        process: The owned process when it is ready, else ``None``.
        is_unsupported: Whether this ``dexcli`` has no ``connectors mcp`` command.
        failure: Why it did not start, when it failed.
    """

    url: str | None = None
    process: StartedConnectorsMcpCommand | None = None
    is_unsupported: bool = False
    failure: str | None = None


async def start_connectors_mcp_command(
    dexcli: Path, environment: CommandEnvironment
) -> ConnectorsMcpCommandStart:
    """Run ``dexcli connectors mcp`` and wait for its one JSON line.

    The child runs in its own session, so a Ctrl-C at the terminal does not
    stop it. It stops when its stdin closes, which happens when this process
    exits for any reason. Its stderr goes to
    ``~/.dex/agents/dexcli-connectors-mcp.log``.

    Args:
        dexcli: The ``dexcli`` executable.
        environment: The machine settings.

    Returns:
        The URL and owned process when ready, ``is_unsupported`` for a
        ``dexcli`` without the command, or the failure otherwise.
    """
    environment.agents_directory.mkdir(parents=True, exist_ok=True)
    log_path = connectors_mcp_command_log_path(environment)
    with log_path.open("wb") as log:
        try:
            process = subprocess.Popen(
                [str(dexcli), "connectors", "mcp", "--exit-on-stdin-close"],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=log,
                start_new_session=sys.platform != "win32",
                creationflags=getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0),
            )
        except OSError as error:
            return ConnectorsMcpCommandStart(failure=f"cannot run {dexcli}: {error.strerror}")
    started = StartedConnectorsMcpCommand(process)
    assert process.stdout is not None, "stdout is piped"
    try:
        line = await asyncio.wait_for(
            asyncio.to_thread(process.stdout.readline), _READY_TIMEOUT_SECONDS
        )
    except TimeoutError:
        await asyncio.to_thread(started.stop)
        return ConnectorsMcpCommandStart(
            failure=f"dexcli connectors mcp printed no line within "
            f"{_READY_TIMEOUT_SECONDS:.0f} seconds; see {log_path}"
        )
    except BaseException:
        # Closing its stdin ends the child, so the readline thread returns and the loop can close.
        started.stop()
        raise
    if not line.strip():
        exit_code = await asyncio.to_thread(process.wait)
        if exit_code == _USAGE_EXIT_CODE:
            return ConnectorsMcpCommandStart(is_unsupported=True)
        return ConnectorsMcpCommandStart(
            failure=f"dexcli connectors mcp exited with {exit_code}; see {log_path}"
        )
    parsed = _parse_line(line)
    url = parsed.get("url")
    if parsed.get("type") == "ready" and isinstance(url, str) and _is_loopback_http_url(url):
        process.stdout.close()
        return ConnectorsMcpCommandStart(url=url, process=started)
    await asyncio.to_thread(started.stop)
    message = parsed.get("message")
    printed = line.decode(errors="replace").strip()
    reason = message if isinstance(message, str) else f"unexpected output: {printed}"
    return ConnectorsMcpCommandStart(failure=f"{reason}; see {log_path}")


def _parse_line(line: bytes) -> dict[str, object]:
    try:
        parsed = json.loads(line)
    except ValueError:
        return {}
    return parsed if isinstance(parsed, dict) else {}


def _is_loopback_http_url(value: str) -> bool:
    parts = urlsplit(value)
    return parts.scheme == "http" and parts.hostname in _LOOPBACK_HOSTS


def _waits_for_exit(process: subprocess.Popen[bytes], timeout: float) -> bool:
    try:
        process.wait(timeout)
    except subprocess.TimeoutExpired:
        return False
    return True
