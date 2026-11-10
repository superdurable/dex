# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Dex server a command uses: a URL, a running ``dexcli dev``, or one it starts."""

from __future__ import annotations

import contextlib
import json
import os
import socket
import subprocess
import time
from collections.abc import Callable
from dataclasses import asdict, dataclass
from pathlib import Path
from urllib.parse import urlsplit

import httpx2

from dex_agents_cli.config import AUTO
from dex_agents_cli.dexcli_install import download_dexcli, locate_dexcli
from dex_agents_cli.environment import RuntimeEnvironment

#: The line ``dexcli dev`` prints once Dex Server and Dex Web are ready.
DEXCLI_DEV_READY_LINE = "Dex development environment is ready"
_READY_FIELD_PREFIXES = {
    "web_url": "Dex Web:",
    "dex_address": "Dex Server:",
    "connectors_mcp_url": "Connectors MCP:",
}
_START_TIMEOUT_SECONDS = 120.0
_PROBE_TIMEOUT_SECONDS = 2.0
_POLL_INTERVAL_SECONDS = 0.2
_LOG_TAIL_LINES = 20


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


def resolve_dex_server(
    server: str,
    environment: RuntimeEnvironment,
    *,
    is_connectors_mcp_needed: bool,
    report: Callable[[str], None],
) -> DexServerEndpoint:
    """Find the Dex server of a ``dex.server`` value, starting ``dexcli dev`` when needed.

    ``auto`` reuses the ``dexcli dev`` a command started earlier when it still
    answers, then one on the default ports, and otherwise starts one in the
    background. A started one keeps running after the command exits, so other
    commands and ``--resume`` reuse it.

    Args:
        server: ``auto`` or a Dex server URL.
        environment: The machine settings.
        is_connectors_mcp_needed: Whether ``connectors.mcp`` is ``auto``. When
            it is not, a started ``dexcli dev`` that supports
            ``--connectors-mcp`` gets ``--connectors-mcp=false``.
        report: Prints one progress line for the user.

    Returns:
        The endpoint.

    Raises:
        DexServerError: If ``dexcli dev`` cannot be found, downloaded, or started.
    """
    if server != AUTO:
        parts = urlsplit(server)
        port = parts.port or (443 if parts.scheme == "https" else 80)
        return DexServerEndpoint(address=f"{parts.hostname}:{port}", is_tls=parts.scheme == "https")
    reused = _reuse_started_server(environment) or _reuse_default_server(environment)
    if reused is not None:
        report(f"Using the running dexcli dev at {reused.address} (Dex Web {reused.web_url}).")
        return reused
    return _start_dev_server(environment, is_connectors_mcp_needed, report)


def _reuse_started_server(environment: RuntimeEnvironment) -> DexServerEndpoint | None:
    path = environment.dev_server_state_path
    try:
        state = _DevServerState(**json.loads(path.read_text(encoding="utf-8")))
    except (OSError, ValueError, TypeError):
        return None
    if not _is_process_alive(state.pid) or not _is_web_ready(state.web_url):
        return None
    return DexServerEndpoint(
        address=state.dex_address,
        web_url=state.web_url,
        connectors_mcp_url=state.connectors_mcp_url or f"{state.web_url}/mcp",
    )


def _reuse_default_server(environment: RuntimeEnvironment) -> DexServerEndpoint | None:
    web_url = f"http://127.0.0.1:{environment.dev_web_port}"
    address = f"127.0.0.1:{environment.dev_dex_port}"
    if not _is_port_open(environment.dev_dex_port) or not _is_web_ready(web_url):
        return None
    return DexServerEndpoint(address=address, web_url=web_url, connectors_mcp_url=f"{web_url}/mcp")


def _start_dev_server(
    environment: RuntimeEnvironment, is_connectors_mcp_needed: bool, report: Callable[[str], None]
) -> DexServerEndpoint:
    dexcli = locate_dexcli(environment)
    if dexcli is None:
        report(f"dexcli is not installed; downloading dexcli {environment.dexcli_version}.")
        try:
            dexcli = download_dexcli(environment)
        except Exception as error:
            raise DexServerError(str(error)) from error
    arguments = [str(dexcli), "dev", "-open=false"]
    if environment.has_custom_dev_ports:
        arguments += [f"-dex-port={environment.dev_dex_port}"]
        arguments += [f"-web-port={environment.dev_web_port}"]
    if not is_connectors_mcp_needed and _supports_connectors_mcp_flag(dexcli):
        arguments.append("-connectors-mcp=false")
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
    ready = _wait_for_ready_output(process, log_path)
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


def _wait_for_ready_output(process: subprocess.Popen[bytes], log_path: Path) -> dict[str, str]:
    deadline = time.monotonic() + _START_TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        text = log_path.read_text(encoding="utf-8", errors="replace")
        if DEXCLI_DEV_READY_LINE in text:
            fields = _parse_ready_fields(text)
            if "web_url" in fields and "dex_address" in fields:
                return fields
        if process.poll() is not None:
            raise DexServerError(
                f"dexcli dev exited with code {process.returncode}:\n{_tail(log_path)}"
            )
        time.sleep(_POLL_INTERVAL_SECONDS)
    process.kill()
    raise DexServerError(
        f"dexcli dev was not ready in {_START_TIMEOUT_SECONDS:.0f}s:\n{_tail(log_path)}"
    )


def _parse_ready_fields(text: str) -> dict[str, str]:
    fields: dict[str, str] = {}
    for line in text.splitlines():
        stripped = line.strip()
        for name, prefix in _READY_FIELD_PREFIXES.items():
            if stripped.startswith(prefix):
                fields[name] = stripped.removeprefix(prefix).strip()
    return fields


def _supports_connectors_mcp_flag(dexcli: Path) -> bool:
    try:
        completed = subprocess.run(
            [str(dexcli), "dev", "-h"], capture_output=True, text=True, timeout=30, check=False
        )
    except (OSError, subprocess.TimeoutExpired):
        return False
    return "connectors-mcp" in completed.stdout + completed.stderr


def _is_process_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def _is_port_open(port: int) -> bool:
    with (
        contextlib.suppress(OSError),
        socket.create_connection(("127.0.0.1", port), timeout=_PROBE_TIMEOUT_SECONDS),
    ):
        return True
    return False


def _is_web_ready(web_url: str) -> bool:
    try:
        response = httpx2.get(f"{web_url}/readyz", timeout=_PROBE_TIMEOUT_SECONDS)
    except httpx2.HTTPError:
        return False
    return response.status_code == 200


def _tail(log_path: Path) -> str:
    lines = log_path.read_text(encoding="utf-8", errors="replace").splitlines()
    return "\n".join(lines[-_LOG_TAIL_LINES:])
