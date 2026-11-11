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
import json
import os
import secrets
import subprocess
import tempfile
from collections.abc import Callable
from dataclasses import asdict, dataclass, replace
from enum import Enum
from pathlib import Path
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

from dex_agents_cli.config import AUTO
from dex_agents_cli.dexcli_install import DexcliInstallError, download_dexcli, locate_dexcli
from dex_agents_cli.environment import RuntimeEnvironment

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
    environment: RuntimeEnvironment,
    *,
    report: Callable[[str], None],
) -> DexServerEndpoint:
    """Find the Dex server of a ``dex.server`` value, starting ``dexcli dev`` when needed.

    ``auto`` reuses a ``dexcli dev`` on the configured ports only when it
    identifies itself as one, even when ``~/.dex/agents/dexcli-dev.json``
    records it: Dex Web's ``/readyz`` answers with its readiness JSON and,
    within five seconds, the Dex port answers as FlowService, describing an
    unknown Flow as not found. Something else on those ports is an error, so
    no other process can stand in for Dex or the connectors MCP server. With nothing there, it
    starts ``dexcli dev`` in the background, which keeps running after the
    command exits.

    Args:
        server: ``auto`` or a Dex server URL.
        environment: The machine settings.
        report: Prints one progress line for the user.

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
        return await _start_dev_server(environment, report)
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
    environment: RuntimeEnvironment, web_url: str, address: str
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
    environment: RuntimeEnvironment, report: Callable[[str], None]
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
                # Its own session keeps it running, and out of Ctrl-C, after the command exits.
                start_new_session=True,
            )
        except OSError as error:
            raise DexServerError(f"cannot run {dexcli}: {error.strerror}") from error
    ready = await _wait_for_ready_output(process, log_path)
    state = _DevServerState(
        pid=process.pid,
        dex_address=ready["dex_address"],
        web_url=ready["web_url"],
        connectors_mcp_url=ready.get("connectors_mcp_url"),
    )
    environment.dev_server_state_path.write_text(json.dumps(asdict(state)), encoding="utf-8")
    report(
        f"dexcli dev is ready (pid {process.pid}); it keeps running after this command exits. "
        f"Stop it with: kill {process.pid}"
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
    process.kill()
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
    parts = urlsplit(value)
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


def _is_process_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def _tail(log_path: Path) -> str:
    lines = log_path.read_text(encoding="utf-8", errors="replace").splitlines()
    return "\n".join(lines[-_LOG_TAIL_LINES:])
