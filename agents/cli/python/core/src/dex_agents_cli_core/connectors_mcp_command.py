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
import os
import signal
import subprocess
import sys
import threading
import time
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from urllib.parse import urlsplit

from dex_agents_cli_core.environment import CommandEnvironment

#: The first ``dexcli`` release with ``dexcli connectors mcp``.
CONNECTORS_MCP_COMMAND_MINIMUM_DEXCLI_VERSION = "1.47.7"

# The first start may download the Connectors MCP server and retry five failed starts.
_READY_TIMEOUT_SECONDS = 120.0
# A child that closed its output without a line exits at once when it hit a usage error.
_EXIT_WAIT_AFTER_OUTPUT_END_SECONDS = 5.0
_STDIN_CLOSE_WAIT_SECONDS = 10.0
_GROUP_STOP_WAIT_SECONDS = 5.0
_EXIT_POLL_SECONDS = 0.02
# The same bound as dexcli's connectorsMCPMaxAnnouncementBytes.
_MAXIMUM_READY_LINE_BYTES = 1 << 20
_MAXIMUM_REASON_CHARACTERS = 500
# dexcli answers an unknown command with this usage exit code.
_USAGE_EXIT_CODE = 2
_LOOPBACK_HOSTS = ("127.0.0.1", "localhost", "::1")
# Windows has no SIGKILL; there _signal_group kills the process instead.
_FORCE_STOP_SIGNAL: signal.Signals = getattr(signal, "SIGKILL", signal.SIGTERM)


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
    except (OSError, UnicodeDecodeError):
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
    """A ``dexcli connectors mcp`` this command started, the leader of its own process group.

    Stopping it stops a server it started, never one it reused. This object
    is the only code that reaps the leader: every ``poll()`` runs under one
    lock, which a group signal also holds from its leader check through the
    signal. So the leader cannot be reaped in between, and a signal never
    reaches a process group ID that another group may reuse.
    """

    def __init__(self, process: subprocess.Popen[bytes]) -> None:
        """Own a spawned ``dexcli connectors mcp`` with a piped stdin."""
        self._process = process
        self._reaping_lock = threading.Lock()
        self._stop_lock = threading.Lock()
        self._is_stopped = False

    def wait_for_exit(self, timeout: float) -> int | None:
        """Wait up to ``timeout`` seconds for the leader to exit, reaping it.

        Returns:
            Its exit code, or ``None`` when it still runs after ``timeout``.
        """
        deadline = time.monotonic() + timeout
        while True:
            with self._reaping_lock:
                exit_code = self._process.poll()
            if exit_code is not None or time.monotonic() >= deadline:
                return exit_code
            time.sleep(_EXIT_POLL_SECONDS)

    def stop(self) -> None:
        """Stop it once: close its stdin, then signal its group with SIGTERM and SIGKILL.

        It signals the group only while the leader is not reaped, so the
        group ID is still this group's. A real dexcli stops its own server
        on stdin EOF, so nothing of the group outlives the leader. A later
        call does nothing.
        """
        with self._stop_lock:
            if self._is_stopped:
                return
            self._is_stopped = True
        if self._process.stdin is not None:
            # It may have exited already; closing a broken pipe changes nothing then.
            with contextlib.suppress(OSError):
                self._process.stdin.close()
        if self.wait_for_exit(_STDIN_CLOSE_WAIT_SECONDS) is not None:
            return
        self._signal_group_while_leader_runs(signal.SIGTERM)
        if self.wait_for_exit(_GROUP_STOP_WAIT_SECONDS) is not None:
            return
        self._signal_group_while_leader_runs(_FORCE_STOP_SIGNAL)
        self.wait_for_exit(_GROUP_STOP_WAIT_SECONDS)

    def _signal_group_while_leader_runs(self, signal_number: signal.Signals) -> None:
        with self._reaping_lock:
            # Only this object reaps, so an unreaped leader keeps its group ID under the lock.
            if self._process.poll() is not None:
                return
            if sys.platform == "win32":
                with contextlib.suppress(OSError):
                    self._process.kill()
                return
            # The leader may be a zombie already; its group ID is still this group's.
            with contextlib.suppress(ProcessLookupError, PermissionError):
                os.killpg(self._process.pid, signal_number)


@dataclass(frozen=True)
class ConnectorsMcpCommandStart:
    """How ``dexcli connectors mcp`` ended its startup.

    Attributes:
        url: The loopback ``/mcp`` URL when it is ready, else ``None``.
        process: The owned process when it is ready, else ``None``.
        is_unsupported: Whether this ``dexcli`` may lack the ``connectors mcp`` command.
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

    The child leads its own process group, so a Ctrl-C at the terminal does
    not stop it. It stops when its stdin closes, which happens when this
    process exits for any reason. Its stderr goes to
    ``~/.dex/agents/dexcli-connectors-mcp.log``. The whole startup has one
    deadline of 120 seconds. Every outcome but a valid ready line, a
    cancellation included, stops and reaps the child before it returns.

    Args:
        dexcli: The ``dexcli`` executable.
        environment: The machine settings.

    Returns:
        The URL and owned process when ready, ``is_unsupported`` for a
        ``dexcli`` that may lack the command, or the failure otherwise.
    """
    log_path = connectors_mcp_command_log_path(environment)
    try:
        environment.agents_directory.mkdir(parents=True, exist_ok=True)
        with log_path.open("wb") as log:
            process = subprocess.Popen(
                [str(dexcli), "connectors", "mcp", "--exit-on-stdin-close"],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=log,
                start_new_session=sys.platform != "win32",
                creationflags=getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0),
            )
    except OSError as error:
        return ConnectorsMcpCommandStart(failure=_bounded(f"cannot run {dexcli}: {error}"))
    started = StartedConnectorsMcpCommand(process)
    try:
        start = await asyncio.wait_for(
            asyncio.to_thread(_await_startup, process, started), _READY_TIMEOUT_SECONDS
        )
    except TimeoutError:
        start = ConnectorsMcpCommandStart(
            failure=f"dexcli connectors mcp printed no line within "
            f"{_READY_TIMEOUT_SECONDS:.0f} seconds"
        )
    except Exception as error:
        start = ConnectorsMcpCommandStart(failure=_bounded(str(error)))
    except BaseException:
        # Stopping the child also ends the reading thread, so the event loop can close.
        started.stop()
        raise
    if start.url is not None:
        if process.stdout is not None:
            process.stdout.close()
        return ConnectorsMcpCommandStart(url=start.url, process=started)
    await asyncio.to_thread(started.stop)
    if start.failure is not None:
        return ConnectorsMcpCommandStart(failure=f"{start.failure}; see {log_path}")
    return start


def _await_startup(
    process: subprocess.Popen[bytes], started: StartedConnectorsMcpCommand
) -> ConnectorsMcpCommandStart:
    """Judge the first stdout line, at most 1 MiB; without one, wait briefly for the exit code.

    It reaps only through ``started``, the one owner of the leader.
    """
    assert process.stdout is not None, "stdout is piped"
    line = process.stdout.readline(_MAXIMUM_READY_LINE_BYTES + 1)
    if len(line) > _MAXIMUM_READY_LINE_BYTES:
        return ConnectorsMcpCommandStart(
            failure=f"dexcli connectors mcp printed more than {_MAXIMUM_READY_LINE_BYTES} "
            "bytes without a newline"
        )
    if line:
        return _judge_ready_line(line)
    exit_code = started.wait_for_exit(_EXIT_WAIT_AFTER_OUTPUT_END_SECONDS)
    if exit_code is None:
        return ConnectorsMcpCommandStart(
            failure="dexcli connectors mcp closed its output without a line"
        )
    if exit_code == _USAGE_EXIT_CODE:
        return ConnectorsMcpCommandStart(is_unsupported=True)
    return ConnectorsMcpCommandStart(failure=f"dexcli connectors mcp exited with {exit_code}")


def _judge_ready_line(line: bytes) -> ConnectorsMcpCommandStart:
    printed = line.decode(errors="replace").strip()
    if not printed:
        return ConnectorsMcpCommandStart(failure="dexcli connectors mcp printed an empty line")
    try:
        parsed = json.loads(printed)
    except ValueError:
        parsed = None
    if isinstance(parsed, dict):
        url = parsed.get("url")
        message = parsed.get("message")
        if parsed.get("type") == "ready" and isinstance(url, str) and _is_loopback_http_url(url):
            return ConnectorsMcpCommandStart(url=url)
        if parsed.get("type") == "error" and isinstance(message, str):
            return ConnectorsMcpCommandStart(failure=_bounded(message))
    return ConnectorsMcpCommandStart(failure=f"unexpected output: {_bounded(printed)}")


def _bounded(text: str) -> str:
    if len(text) <= _MAXIMUM_REASON_CHARACTERS:
        return text
    return f"{text[:_MAXIMUM_REASON_CHARACTERS]}..."


def _is_loopback_http_url(value: str) -> bool:
    try:
        parts = urlsplit(value)
        return parts.scheme == "http" and parts.hostname in _LOOPBACK_HOSTS
    except ValueError:
        return False
