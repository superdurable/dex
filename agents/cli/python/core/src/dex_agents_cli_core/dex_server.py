# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Dex server a command uses: a URL, a running ``dexcli dev``, or one it starts."""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import secrets
import signal
import subprocess
import sys
import tempfile
import time
from collections.abc import Callable, Iterator
from dataclasses import asdict, dataclass, replace
from enum import Enum
from pathlib import Path
from types import FrameType
from typing import Any
from urllib.parse import urlsplit

import httpx2
from dex import (
    AsyncClient,
    BlobCacheConfig,
    ClientOptions,
    FlowNotFoundError,
    Registry,
    open_blob_cache,
)

from dex_agents_cli_core.config import AUTO
from dex_agents_cli_core.dexcli_install import DexcliInstallError, download_dexcli, locate_dexcli
from dex_agents_cli_core.environment import CommandEnvironment

#: The line ``dexcli dev`` prints once Dex Server and Dex Web are ready.
DEXCLI_DEV_READY_LINE = "Dex development environment is ready"
_READY_BLOCK_END_LINE = "Press Ctrl+C to stop."
_READY_FIELD_PREFIXES = {
    "web_url": "Dex Web:",
    "dex_address": "Dex Server:",
    "connectors_mcp_url": "Connectors MCP:",
}
_START_TIMEOUT_SECONDS = 120.0
_READINESS_WAIT_SECONDS = 60.0
_PROBE_TIMEOUT_SECONDS = 2.0
_FLOW_SERVICE_PROBE_TIMEOUT_SECONDS = 5.0
_POLL_INTERVAL_SECONDS = 0.2
_LOG_TAIL_LINES = 20
_PROBE_BLOB_CACHE_BYTES = 1024 * 1024
# Longer than the 10 seconds dexcli dev gives dex-server-zd to shut down.
_STOP_GRACE_SECONDS = 20.0
_KILL_WAIT_SECONDS = 5.0
# dexcli dev stops dex-server-zd before it exits; this only catches a member that outlives it.
_LEFTOVER_WAIT_SECONDS = 2.0
_STOPPED_LINE = "Stopped the dexcli dev this command started."
_SHARING_HINT = "To share one dexcli dev across sessions, run dexcli dev yourself first."


class DexServerError(Exception):
    """Report that no Dex server is available, with what to do."""


@dataclass(frozen=True)
class DexServerEndpoint:
    """The Dex server one command run talks to.

    Attributes:
        address: The gRPC target, ``host:port``.
        is_tls: Whether the target uses TLS (an ``https`` URL).
        web_url: Dex Web's URL, or ``None`` when unknown.
        connectors_mcp_url: The connectors MCP endpoint ``dexcli dev``
            serves, or ``None`` when unknown.
        started_pid: The ``dexcli dev`` process this run started, or ``None``.
    """

    address: str
    is_tls: bool = False
    web_url: str | None = None
    connectors_mcp_url: str | None = None
    started_pid: int | None = None


class StartedDexcliDevProcess:
    """The ``dexcli dev`` one command spawned; stopping it ends its process group."""

    def __init__(self, environment: CommandEnvironment) -> None:
        """Own no process yet.

        Args:
            environment: The machine settings, which locate the record.
        """
        self._environment = environment
        self._process: subprocess.Popen[bytes] | None = None

    def track(self, process: subprocess.Popen[bytes]) -> None:
        """Take ownership of a spawned ``dexcli dev``, the leader of its own process group.

        Args:
            process: The spawned process.
        """
        self._process = process

    def stop(self, report: Callable[[str], None]) -> None:
        """Stop the process group once, SIGTERM then SIGKILL, and remove the record.

        A signal during the stop kills the group at once and exits.

        Args:
            report: Prints the stopped line.
        """
        process, self._process = self._process, None
        if process is None:
            return
        _remove_record_of(self._environment, process.pid)
        # Once poll() reaps the leader, its group ID may be reused, so it is never signalled again.
        was_running = process.poll() is None
        if was_running:
            with _signals_kill_the_group(process):
                _terminate_process_group(process)
                if not _waits_for_exit(process, _STOP_GRACE_SECONDS):
                    _kill_process_group_while_leader_runs(process)
                    _waits_for_exit(process, _KILL_WAIT_SECONDS)
        # After SIGHUP the terminal may be gone; the server is stopped either way.
        with contextlib.suppress(OSError, ValueError):
            if _has_group_members_after_exit(process.pid):
                report(
                    f"dexcli dev (pid {process.pid}) exited, but processes of its process group "
                    f"may still run; if so, stop them with: kill -- -{process.pid}"
                )
            elif was_running:
                report(_STOPPED_LINE)


async def describe_lost_dexcli_dev(web_url: str) -> str | None:
    """Return the short error for a reused ``dexcli dev`` that stopped; ``None`` while it answers.

    Args:
        web_url: Its Dex Web URL.

    Returns:
        The error text.
    """
    if await _read_readiness(web_url) is not _Readiness.ABSENT:
        return None
    return (
        f"the dexcli dev at {web_url} stopped. A dexcli dev that another command started stops "
        f"when that command exits. {_SHARING_HINT}"
    )


@dataclass(frozen=True)
class _DevServerState:
    pid: int
    dex_address: str
    web_url: str
    connectors_mcp_url: str | None


class _Readiness(Enum):
    ABSENT = "absent"
    READY = "ready"
    STARTING = "starting"
    UNKNOWN_SERVICE = "unknown_service"


async def resolve_dex_server(
    server: str,
    environment: CommandEnvironment,
    *,
    report: Callable[[str], None],
    started_process: StartedDexcliDevProcess | None = None,
) -> DexServerEndpoint:
    """Find the Dex server of a ``dex.server`` value, starting ``dexcli dev`` when needed.

    ``auto`` reuses a ``dexcli dev`` on the configured ports only when it
    identifies itself as one, even when ``~/.dex/agents/dexcli-dev.json``
    records it: Dex Web's ``/readyz`` answers with its readiness JSON and,
    within five seconds, the Dex port answers as FlowService, describing an
    unknown Flow as not found. Something else on those ports is an error, so
    no other process can stand in for Dex or the connectors MCP server. With nothing there, it
    starts ``dexcli dev`` in the background. With ``started_process``, that
    owns the started ``dexcli dev`` from its spawn on, so the command stops it
    when it exits; without it, the ``dexcli dev`` keeps running.

    Args:
        server: ``auto`` or a Dex server URL.
        environment: The machine settings.
        report: Prints one progress line for the user.
        started_process: Takes ownership of a ``dexcli dev`` this call starts.

    Returns:
        The endpoint.

    Raises:
        DexServerError: If another service holds the ports, or ``dexcli dev``
            cannot be found, downloaded, or started.
    """
    if server != AUTO:
        parts = urlsplit(server)
        port = parts.port or (443 if parts.scheme == "https" else 80)
        return DexServerEndpoint(address=f"{parts.hostname}:{port}", is_tls=parts.scheme == "https")
    web_url = f"http://127.0.0.1:{environment.dev_web_port}"
    address = f"127.0.0.1:{environment.dev_dex_port}"
    readiness = await _read_readiness(web_url)
    if readiness is _Readiness.ABSENT:
        return await _start_dev_server(environment, report, started_process)
    if readiness is _Readiness.STARTING:
        report(f"Waiting for the dexcli dev at {web_url} to become ready.")
        readiness = await _wait_for_readiness(web_url)
    started = _read_started_server(environment, web_url, address)
    is_identified = readiness is _Readiness.READY and await _is_dex_flow_service(address)
    if not is_identified:
        raise DexServerError(
            f"{web_url} answers, but it is not a dexcli dev with Dex at {address}. Stop that "
            "service, or choose other ports with DEX_AGENTS_DEX_PORT and DEX_AGENTS_WEB_PORT, "
            "or set dex.server to a Dex server URL."
        )
    report(f"Using the running dexcli dev at {address} (Dex Web {web_url}).")
    mcp_url = started.connectors_mcp_url if started is not None else None
    return DexServerEndpoint(
        address=address, web_url=web_url, connectors_mcp_url=mcp_url or f"{web_url}/mcp"
    )


def _read_started_server(
    environment: CommandEnvironment, web_url: str, address: str
) -> _DevServerState | None:
    """Return the recorded ``dexcli dev`` when it still runs on exactly these ports."""
    try:
        recorded = json.loads(environment.dev_server_state_path.read_text(encoding="utf-8"))
        state = _DevServerState(**recorded)
    except (OSError, ValueError, TypeError):
        return None
    if state.web_url != web_url or state.dex_address != address:
        return None
    if not _is_http_url(state.connectors_mcp_url):
        # Earlier releases could record a later log line here instead of the URL.
        state = replace(state, connectors_mcp_url=None)
    return state if _is_process_alive(state.pid) else None


async def _start_dev_server(
    environment: CommandEnvironment,
    report: Callable[[str], None],
    started_process: StartedDexcliDevProcess | None,
) -> DexServerEndpoint:
    try:
        dexcli = locate_dexcli(environment)
        if dexcli is None:
            report(f"dexcli is not installed; downloading dexcli {environment.dexcli_version}.")
            dexcli = download_dexcli(environment)
    except DexcliInstallError as error:
        raise DexServerError(str(error)) from error
    arguments = [
        str(dexcli),
        "dev",
        "-open=false",
        f"-dex-port={environment.dev_dex_port}",
        f"-web-port={environment.dev_web_port}",
    ]
    environment.agents_directory.mkdir(parents=True, exist_ok=True)
    log_path = environment.agents_directory / "dexcli-dev.log"
    report(f"Starting dexcli dev in the background (log: {log_path}).")
    with log_path.open("w", encoding="utf-8") as log:
        try:
            process = subprocess.Popen(
                arguments,
                stdin=subprocess.DEVNULL,
                stdout=log,
                stderr=subprocess.STDOUT,
                # Its own session keeps it out of the terminal's Ctrl-C and SIGHUP.
                start_new_session=sys.platform != "win32",
                # On Windows its own process group can receive CTRL_BREAK_EVENT alone.
                creationflags=getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0),
            )
        except OSError as error:
            raise DexServerError(f"cannot run {dexcli}: {error.strerror}") from error
    if started_process is not None:
        started_process.track(process)
    ready = await _wait_for_ready_output(process, log_path)
    state = _DevServerState(
        pid=process.pid,
        dex_address=ready["dex_address"],
        web_url=ready["web_url"],
        connectors_mcp_url=ready.get("connectors_mcp_url"),
    )
    environment.dev_server_state_path.write_text(json.dumps(asdict(state)), encoding="utf-8")
    if started_process is None:
        report(f"dexcli dev is ready (pid {process.pid}) and keeps running.")
    else:
        report(
            f"dexcli dev is ready (pid {process.pid}); it stops when this command exits. "
            f"{_SHARING_HINT}"
        )
    return DexServerEndpoint(
        address=state.dex_address,
        web_url=state.web_url,
        connectors_mcp_url=state.connectors_mcp_url or f"{state.web_url}/mcp",
        started_pid=process.pid,
    )


async def _wait_for_ready_output(
    process: subprocess.Popen[bytes], log_path: Path
) -> dict[str, str]:
    loop = asyncio.get_running_loop()
    deadline = loop.time() + _START_TIMEOUT_SECONDS
    while loop.time() < deadline:
        text = log_path.read_text(encoding="utf-8", errors="replace")
        if DEXCLI_DEV_READY_LINE in text:
            fields = _parse_ready_fields(text)
            if "web_url" in fields and "dex_address" in fields:
                return fields
        if process.poll() is not None:
            raise DexServerError(
                f"dexcli dev exited with code {process.returncode}:\n{_tail(log_path)}"
            )
        await asyncio.sleep(_POLL_INTERVAL_SECONDS)
    _kill_process_group_while_leader_runs(process)
    raise DexServerError(
        f"dexcli dev was not ready in {_START_TIMEOUT_SECONDS:.0f}s:\n{_tail(log_path)}"
    )


def _parse_ready_fields(text: str) -> dict[str, str]:
    # Later log lines, such as "Connectors MCP: started server ...", reuse these prefixes.
    _, _, ready_block = text.rpartition(DEXCLI_DEV_READY_LINE)
    fields: dict[str, str] = {}
    for line in ready_block.splitlines():
        stripped = line.strip()
        if stripped == _READY_BLOCK_END_LINE:
            break
        for name, prefix in _READY_FIELD_PREFIXES.items():
            if name not in fields and stripped.startswith(prefix):
                fields[name] = stripped.removeprefix(prefix).strip()
    if not _is_http_url(fields.get("connectors_mcp_url")):
        fields.pop("connectors_mcp_url", None)
    return fields


def _is_http_url(value: str | None) -> bool:
    if not value or any(character.isspace() for character in value):
        return False
    try:
        parts = urlsplit(value)
    except ValueError:
        return False
    return parts.scheme in ("http", "https") and bool(parts.netloc)


async def _read_readiness(web_url: str) -> _Readiness:
    try:
        async with httpx2.AsyncClient(timeout=_PROBE_TIMEOUT_SECONDS) as http:
            response = await http.get(f"{web_url}/readyz")
    except httpx2.HTTPError:
        return _Readiness.ABSENT
    try:
        body = response.json()
    except ValueError:
        return _Readiness.UNKNOWN_SERVICE
    if response.status_code == 200:
        return _Readiness.READY if _is_dex_web_readiness(body) else _Readiness.UNKNOWN_SERVICE
    # Dex Web answers 503 with a coded error while Dex is starting.
    is_starting = response.status_code == 503 and isinstance(body, dict)
    return _Readiness.STARTING if is_starting else _Readiness.UNKNOWN_SERVICE


def _is_dex_web_readiness(body: object) -> bool:
    """Report whether a /readyz body is Dex Web's readiness JSON, by its values' types."""
    if not isinstance(body, dict) or body.get("status") != "ready":
        return False
    count = body.get("definitionCount")
    return (
        isinstance(body.get("definitionRevision"), str)
        and isinstance(body.get("source"), str)
        and isinstance(count, int)
        and not isinstance(count, bool)
    )


async def _wait_for_readiness(web_url: str) -> _Readiness:
    loop = asyncio.get_running_loop()
    deadline = loop.time() + _READINESS_WAIT_SECONDS
    readiness = _Readiness.STARTING
    while readiness is _Readiness.STARTING and loop.time() < deadline:
        await asyncio.sleep(_POLL_INTERVAL_SECONDS)
        readiness = await _read_readiness(web_url)
    return readiness


async def _is_dex_flow_service(address: str) -> bool:
    """Report whether a port answers as Dex's FlowService: an unknown Flow is not found."""
    with tempfile.TemporaryDirectory(prefix="dex-agents-probe-") as cache_directory:
        blob_cache = open_blob_cache(BlobCacheConfig(cache_directory, _PROBE_BLOB_CACHE_BYTES))
        client = AsyncClient(
            Registry([], allow_async_handlers=True),
            blob_cache,
            ClientOptions(server_address=address),
        )
        probe_flow_id = f"dex-agents-cli-probe-{secrets.token_hex(8)}"
        try:
            await asyncio.wait_for(
                client.describe_flow(probe_flow_id), _FLOW_SERVICE_PROBE_TIMEOUT_SECONDS
            )
        except FlowNotFoundError:
            return True
        except Exception:
            # Any other answer means the port is not a Dex server this command can trust.
            return False
        finally:
            await client.close()
            blob_cache.close()
        return False


def _terminate_process_group(process: subprocess.Popen[bytes]) -> None:
    """Ask the group of a running, unreaped leader to stop."""
    if sys.platform == "win32":
        # Its own process group gets CTRL_BREAK_EVENT, which dexcli handles as an interrupt.
        with contextlib.suppress(OSError):
            process.send_signal(signal.CTRL_BREAK_EVENT)
        return
    with contextlib.suppress(ProcessLookupError, PermissionError):
        os.killpg(process.pid, signal.SIGTERM)


def _kill_process_group_while_leader_runs(process: subprocess.Popen[bytes]) -> None:
    """Kill the group only while the leader is unreaped, so its group ID is still ours."""
    if process.returncode is not None:
        return
    if sys.platform == "win32":
        # process.kill() would end dexcli dev alone and leave dex-server-zd running.
        try:
            subprocess.run(
                ["taskkill", "/PID", str(process.pid), "/T", "/F"],
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                check=False,
            )
        except OSError:
            with contextlib.suppress(OSError):  # Without taskkill, end dexcli dev alone.
                process.kill()
        return
    with contextlib.suppress(ProcessLookupError, PermissionError):
        os.killpg(process.pid, signal.SIGKILL)


def _waits_for_exit(process: subprocess.Popen[bytes], timeout: float) -> bool:
    try:
        process.wait(timeout)
    except subprocess.TimeoutExpired:
        return False
    return True


@contextlib.contextmanager
def _signals_kill_the_group(process: subprocess.Popen[bytes]) -> Iterator[None]:
    """While the command waits for the group, a termination signal kills it at once and exits."""

    def kill_and_exit(signal_number: int, frame: FrameType | None) -> None:
        _kill_process_group_while_leader_runs(process)
        os._exit(128 + signal_number)

    previous_handlers: dict[int, Any] = {}
    names = ("SIGTERM", "SIGHUP", "SIGINT")
    try:
        for number in (getattr(signal, name) for name in names if hasattr(signal, name)):
            previous_handlers[number] = signal.signal(number, kill_and_exit)
    except ValueError:
        pass  # Only the main thread can set handlers; the stop then just waits.
    try:
        yield
    finally:
        for number, handler in previous_handlers.items():
            signal.signal(number, handler)


def _has_group_members_after_exit(pid: int) -> bool:
    """Report whether members of an exited leader's group still run after a short wait.

    Signal 0 delivers nothing, so asking about a group ID that may be reused is harmless.
    """
    if sys.platform == "win32":
        return False
    deadline = time.monotonic() + _LEFTOVER_WAIT_SECONDS
    while _is_process_group_alive(pid):
        if time.monotonic() >= deadline:
            return True
        time.sleep(_POLL_INTERVAL_SECONDS)
    return False


def _is_process_group_alive(pid: int) -> bool:
    try:
        os.killpg(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True  # A member still runs, under another user.
    return True


def _remove_record_of(environment: CommandEnvironment, pid: int) -> None:
    """Remove the record only when it names ``pid``; renaming it aside keeps a newer one."""
    path = environment.dev_server_state_path
    claimed = path.with_name(f"{path.name}.{os.getpid()}-{secrets.token_hex(4)}.removing")
    try:
        path.rename(claimed)
    except OSError:
        return  # No record.
    try:
        recorded = json.loads(claimed.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        recorded = None
    if not (isinstance(recorded, dict) and recorded.get("pid") == pid):
        try:
            os.link(claimed, path)
        except FileExistsError:
            pass  # A newer record took its place; that one stays.
        except OSError:
            # Without hard links, rename it back while the path is still empty.
            if not path.exists():
                with contextlib.suppress(OSError):
                    claimed.rename(path)
                    return
    claimed.unlink(missing_ok=True)


def _is_process_alive(pid: int) -> bool:
    if sys.platform == "win32":
        return _is_windows_process_alive(pid)
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


if sys.platform == "win32":

    def _is_windows_process_alive(pid: int) -> bool:
        # os.kill(pid, 0) would end the process on Windows.
        import ctypes

        synchronize = 0x00100000
        error_access_denied = 5
        wait_timeout = 0x00000102
        kernel32 = ctypes.windll.kernel32
        handle = kernel32.OpenProcess(synchronize, False, pid)
        if not handle:
            return bool(ctypes.GetLastError() == error_access_denied)
        try:
            # A process handle is signalled once the process exits, whatever its exit code.
            return bool(kernel32.WaitForSingleObject(handle, 0) == wait_timeout)
        finally:
            kernel32.CloseHandle(handle)


def _tail(log_path: Path) -> str:
    lines = log_path.read_text(encoding="utf-8", errors="replace").splitlines()
    return "\n".join(lines[-_LOG_TAIL_LINES:])
