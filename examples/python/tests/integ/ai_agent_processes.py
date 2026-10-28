# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Runs a private Dex engine and examples Worker that a test can hard-kill.

The shared test server already has an in-process Worker for every example Flow,
so these tests start their own server-zd through ``dexcli dev`` and run
``main.py`` as the only Worker. ``kill`` sends SIGKILL to the whole process
group, so neither process gets a chance to shut down cleanly.
"""

from __future__ import annotations

import asyncio
import os
import shutil
import signal
import socket
import subprocess
import sys
from pathlib import Path

from tests.integ.flow_smoke_helper import dexcli_path

EXAMPLES_DIR = Path(__file__).resolve().parents[2]
READY_TIMEOUT_SECONDS = 60
ENGINE_READY_LINE = "Dex development environment is ready"
WORKER_READY_LINE = "Dex Python examples listening on"


def server_zd_binary() -> str | None:
    """Returns the dex-server-zd executable, or None when it is unavailable."""
    return os.environ.get("DEX_SERVER_ZD_BINARY") or shutil.which("dex-server-zd")


class KillableProcess:
    def __init__(self, name: str, log_path: Path) -> None:
        self._name = name
        self._log_path = log_path
        self._process: subprocess.Popen[bytes] | None = None

    async def start(
        self,
        command: list[str],
        ready_line: str,
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
    ) -> None:
        if self._process is not None:
            raise RuntimeError(f"{self._name} is already running")
        log_offset = self._log_path.stat().st_size if self._log_path.exists() else 0
        with self._log_path.open("ab") as log:
            self._process = subprocess.Popen(
                command,
                cwd=cwd,
                env=env,
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        deadline = asyncio.get_running_loop().time() + READY_TIMEOUT_SECONDS
        while asyncio.get_running_loop().time() < deadline:
            if ready_line in self._read_log(log_offset):
                return
            if self._process.poll() is not None:
                raise RuntimeError(
                    f"{self._name} exited before it was ready:\n"
                    f"{self._read_log(log_offset)}"
                )
            await asyncio.sleep(0.1)
        raise RuntimeError(f"{self._name} was not ready:\n{self._read_log(log_offset)}")

    def kill(self) -> None:
        """Sends SIGKILL to the process and every child it started."""
        if self._process is None:
            return
        try:
            os.killpg(self._process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        self._process.wait()
        self._process = None

    def _read_log(self, offset: int) -> str:
        with self._log_path.open("rb") as log:
            log.seek(offset)
            return log.read().decode(errors="replace")


class PrivateDexEngine:
    """A ``dexcli dev`` server-zd instance whose state survives a hard kill."""

    def __init__(self, state_dir: Path) -> None:
        self._state_dir = state_dir
        self.dex_port = available_port()
        self.web_port = available_port()
        self._process = KillableProcess("dexcli dev", state_dir / "dexcli.log")

    @property
    def address(self) -> str:
        return f"127.0.0.1:{self.dex_port}"

    async def start(self) -> None:
        zd_binary = server_zd_binary()
        if zd_binary is None:
            raise RuntimeError("set DEX_SERVER_ZD_BINARY or put dex-server-zd on PATH")
        await self._process.start(
            [
                dexcli_path(),
                "dev",
                "-engine",
                "zd",
                "-zd-binary",
                zd_binary,
                "-zd-data-dir",
                str(self._state_dir / "zd"),
                "-connector-config-dir",
                str(self._state_dir / "connectors"),
                "-bind-address",
                "127.0.0.1",
                "-dex-port",
                str(self.dex_port),
                "-web-port",
                str(self.web_port),
                "-open=false",
            ],
            ENGINE_READY_LINE,
        )

    def kill(self) -> None:
        self._process.kill()


class ExamplesWorkerProcess:
    """Runs ``main.py``, which hosts the Worker and the HTTP routes."""

    def __init__(
        self,
        server_address: str,
        agent_mcp_config: Path,
        state_dir: Path,
        project_dir: Path = EXAMPLES_DIR,
        extra_env: dict[str, str] | None = None,
    ) -> None:
        self._server_address = server_address
        self._agent_mcp_config = agent_mcp_config
        self._state_dir = state_dir
        self._project_dir = project_dir
        self._extra_env = extra_env or {}
        self.http_port = available_port()
        self._process = KillableProcess("examples Worker", state_dir / "worker.log")

    async def start(self) -> None:
        await self._process.start(
            [sys.executable, "main.py"],
            WORKER_READY_LINE,
            cwd=self._project_dir,
            env={
                **os.environ,
                "DEX_FLOW_SERVICE_ADDRESS": self._server_address,
                "DEX_EXAMPLES_HTTP_ADDRESS": f"127.0.0.1:{self.http_port}",
                "DEX_AGENT_MCP_CONFIG": str(self._agent_mcp_config),
                "DEX_BLOB_CACHE_DIR": str(self._state_dir / "worker-blob-cache"),
                "PYTHONUNBUFFERED": "1",
                **self._extra_env,
            },
        )

    def kill(self) -> None:
        self._process.kill()


def write_agent_mcp_config(config_path: Path) -> Path:
    """Writes a Worker MCP configuration for the test MCP server."""
    server_path = Path(__file__).with_name("ai_agent_mcp_server.py")
    config_path.write_text(
        f"""
servers:
  - name: test
    transport: stdio
    command: {sys.executable}
    args: [{server_path}]
    trust_read_only_annotations: true
""".strip()
    )
    return config_path


def available_port() -> int:
    with socket.socket() as server_socket:
        server_socket.bind(("127.0.0.1", 0))
        return int(server_socket.getsockname()[1])
