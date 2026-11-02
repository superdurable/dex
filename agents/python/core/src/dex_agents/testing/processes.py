# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Child processes that integration tests start and hard-kill."""

from __future__ import annotations

import asyncio
import contextlib
import os
import shutil
import signal
import socket
import subprocess
import sys
import uuid
from collections.abc import Mapping, Sequence
from datetime import timedelta
from pathlib import Path
from types import TracebackType

from dex_agents.testing.worker import (
    BLOB_CACHE_DIR_ENVIRONMENT_VARIABLE,
    SERVER_ADDRESS_ENVIRONMENT_VARIABLE,
    WORKER_READY_LINE,
)

#: Environment variable naming the ``dexcli`` executable.
DEXCLI_ENVIRONMENT_VARIABLE = "DEXCLI"
#: Environment variable naming the ``dex-server-zd`` executable.
DEX_SERVER_ZD_ENVIRONMENT_VARIABLE = "DEX_SERVER_ZD_BINARY"
#: Line ``dexcli dev`` prints once its Dex Server accepts calls.
ENGINE_READY_LINE = "Dex development environment is ready"
DEFAULT_READY_TIMEOUT = timedelta(seconds=60)
_READY_POLL_INTERVAL_SECONDS = 0.05


def find_free_port() -> int:
    """Return a TCP port on 127.0.0.1 that was free a moment ago.

    Tests on a shared machine must never use fixed ports. A port can still be
    taken between this call and its use, which is rare enough for tests. Use
    ``find_free_ports`` when one process needs several ports.

    Returns:
        A port number.
    """
    return find_free_ports(1)[0]


def find_free_ports(count: int) -> list[int]:
    """Return distinct TCP ports on 127.0.0.1 that were free a moment ago.

    Every probe socket stays open until all ports are chosen, so the ports
    are distinct. A port can still be taken after this call returns.

    Args:
        count: Number of ports, at least one.

    Returns:
        ``count`` distinct port numbers.

    Raises:
        ValueError: If ``count`` is less than one.
    """
    if count < 1:
        raise ValueError("count must be at least one")
    with contextlib.ExitStack() as probes:
        ports: list[int] = []
        for _ in range(count):
            probe = probes.enter_context(socket.socket())
            probe.bind(("127.0.0.1", 0))
            ports.append(int(probe.getsockname()[1]))
        return ports


def resolve_dexcli_path() -> Path:
    """Return the ``dexcli`` executable for private test engines.

    Returns:
        ``$DEXCLI`` when set, otherwise ``dexcli`` from ``PATH``.

    Raises:
        FileNotFoundError: If neither names an existing file.
    """
    configured = os.environ.get(DEXCLI_ENVIRONMENT_VARIABLE)
    candidate = configured or shutil.which("dexcli")
    if candidate is None or not Path(candidate).is_file():
        raise FileNotFoundError(f"set {DEXCLI_ENVIRONMENT_VARIABLE} to the dexcli executable")
    return Path(candidate).resolve()


def resolve_server_zd_path(dexcli: Path) -> Path:
    """Return the ``dex-server-zd`` executable that ``dexcli dev`` should run.

    Args:
        dexcli: The resolved ``dexcli`` executable.

    Returns:
        ``$DEX_SERVER_ZD_BINARY`` when set, otherwise ``dex-server-zd`` in the
        same directory as ``dexcli``, otherwise ``dex-server-zd`` from ``PATH``.

    Raises:
        FileNotFoundError: If no candidate exists.
    """
    configured = os.environ.get(DEX_SERVER_ZD_ENVIRONMENT_VARIABLE)
    sibling = dexcli.with_name("dex-server-zd")
    for candidate in (configured, str(sibling), shutil.which("dex-server-zd")):
        if candidate and Path(candidate).is_file():
            return Path(candidate).resolve()
    raise FileNotFoundError(
        f"put dex-server-zd next to dexcli or set {DEX_SERVER_ZD_ENVIRONMENT_VARIABLE}"
    )


class KillableProcess:
    """Run one child process in its own process group and kill it with SIGKILL.

    The child writes stdout and stderr to a log file. ``start`` returns once a
    ready line appears in the output written by this start. ``kill`` sends
    SIGKILL to the whole process group, so neither the child nor anything it
    started can shut down cleanly. A killed process can be started again.

    Example:
        >>> process = KillableProcess("worker", tmp_path / "worker.log")
        >>> await process.start([sys.executable, "-m", "my_worker"], "ready")
        >>> process.kill()
    """

    def __init__(self, name: str, log_path: Path) -> None:
        """Create a stopped process.

        Args:
            name: Name used in error messages.
            log_path: File that receives the child's output, appended per start.
        """
        self._name = name
        self._log_path = log_path
        self._process: subprocess.Popen[bytes] | None = None
        self._log_offset = 0

    @property
    def is_running(self) -> bool:
        """Report whether the child was started and has not exited.

        Returns:
            ``True`` while the child runs.
        """
        return self._process is not None and self._process.poll() is None

    @property
    def log_path(self) -> Path:
        """Return the file that receives the child's output.

        Returns:
            The log path passed to the constructor.
        """
        return self._log_path

    async def start(
        self,
        command: Sequence[str],
        ready_line: str,
        *,
        cwd: Path | None = None,
        env: Mapping[str, str] | None = None,
        ready_timeout: timedelta = DEFAULT_READY_TIMEOUT,
    ) -> None:
        """Start the child and wait until its output contains ``ready_line``.

        Args:
            command: Executable and arguments.
            ready_line: Text that marks the child as ready.
            cwd: Working directory, or ``None`` for the current one.
            env: Complete environment, or ``None`` to inherit this process's.
            ready_timeout: Maximum wait for the ready line.

        Raises:
            RuntimeError: If the child is already running, exits before it is
                ready, or misses the timeout. The child is killed on timeout.
        """
        if self._process is not None:
            raise RuntimeError(f"{self._name} is already running")
        self._log_path.parent.mkdir(parents=True, exist_ok=True)
        self._log_offset = self._log_path.stat().st_size if self._log_path.exists() else 0
        with self._log_path.open("ab") as log:
            self._process = subprocess.Popen(
                list(command),
                cwd=cwd,
                env=dict(env) if env is not None else None,
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        loop = asyncio.get_running_loop()
        deadline = loop.time() + ready_timeout.total_seconds()
        while loop.time() < deadline:
            if ready_line in self.read_log():
                return
            if self._process.poll() is not None:
                output = self.read_log()
                self._process = None
                raise RuntimeError(f"{self._name} exited before it was ready:\n{output}")
            await asyncio.sleep(_READY_POLL_INTERVAL_SECONDS)
        output = self.read_log()
        self.kill()
        raise RuntimeError(f"{self._name} was not ready in {ready_timeout}:\n{output}")

    def kill(self) -> None:
        """Send SIGKILL to the child's process group and reap the child.

        Calling it on a stopped process does nothing.
        """
        if self._process is None:
            return
        try:
            os.killpg(self._process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        self._process.wait()
        self._process = None

    async def terminate(self, timeout: timedelta = timedelta(seconds=30)) -> None:
        """Ask the child to stop with SIGTERM, then SIGKILL it after ``timeout``.

        Args:
            timeout: Maximum wait for a graceful exit.
        """
        if self._process is None:
            return
        try:
            os.killpg(self._process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        process = self._process
        try:
            await asyncio.wait_for(asyncio.to_thread(process.wait), timeout.total_seconds())
        except TimeoutError:
            self.kill()
            return
        self._process = None

    def read_log(self) -> str:
        """Return the output written since the latest start.

        Returns:
            Decoded output; invalid bytes are replaced.
        """
        if not self._log_path.exists():
            return ""
        with self._log_path.open("rb") as log:
            log.seek(self._log_offset)
            return log.read().decode(errors="replace")


class PrivateDexEngine:
    """Run a private ``dexcli dev --engine zd`` Dex Server on free ports.

    Every test that needs a clean server, or kills the server, gets its own
    engine. State lives under ``state_dir``, so a killed engine started again
    resumes every Flow. Use it as an async context manager to kill it on exit.

    Example:
        >>> async with PrivateDexEngine(tmp_path / "engine") as engine:
        ...     client = AsyncClient(registry, cache, ClientOptions(engine.address))
    """

    def __init__(
        self,
        state_dir: Path,
        *,
        dexcli: Path | None = None,
        server_zd: Path | None = None,
        server_zd_config: Path | None = None,
    ) -> None:
        """Choose ports and executables without starting the engine.

        Args:
            state_dir: Directory for server state and the engine log.
            dexcli: ``dexcli`` executable; ``None`` uses ``resolve_dexcli_path()``.
            server_zd: ``dex-server-zd`` executable; ``None`` uses
                ``resolve_server_zd_path``.
            server_zd_config: ``dex-server-zd`` YAML file with configuration
                overrides, passed as ``dexcli dev -zd-config``, such as
                ``streamStore: {backend: disabled}``. ``None`` uses none.

        Raises:
            FileNotFoundError: If an executable cannot be found.
        """
        self._state_dir = state_dir
        self._dexcli = dexcli or resolve_dexcli_path()
        self._server_zd = server_zd or resolve_server_zd_path(self._dexcli)
        self._server_zd_config = server_zd_config
        self.dex_port, self.web_port = find_free_ports(2)
        self._process = KillableProcess("dexcli dev", state_dir / "dexcli.log")

    @property
    def address(self) -> str:
        """Return the Dex Server address for Client and Worker options.

        Returns:
            ``"127.0.0.1:<dex_port>"``.
        """
        return f"127.0.0.1:{self.dex_port}"

    @property
    def process(self) -> KillableProcess:
        """Return the engine process, for its log.

        Returns:
            The ``dexcli dev`` process.
        """
        return self._process

    async def start(self, ready_timeout: timedelta = DEFAULT_READY_TIMEOUT) -> None:
        """Start the engine and wait until it accepts calls.

        Args:
            ready_timeout: Maximum wait for readiness.

        Raises:
            RuntimeError: If the engine exits or misses the timeout.
        """
        self._state_dir.mkdir(parents=True, exist_ok=True)
        config_arguments = (
            [] if self._server_zd_config is None else ["-zd-config", str(self._server_zd_config)]
        )
        await self._process.start(
            [
                str(self._dexcli),
                "dev",
                "-engine",
                "zd",
                "-zd-binary",
                str(self._server_zd),
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
                *config_arguments,
            ],
            ENGINE_READY_LINE,
            ready_timeout=ready_timeout,
        )

    def kill(self) -> None:
        """Hard-kill the engine and its server."""
        self._process.kill()

    async def __aenter__(self) -> PrivateDexEngine:
        await self.start()
        return self

    async def __aexit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self.kill()


class AgentWorkerProcess:
    """Run a Worker module as a child process that a test can SIGKILL.

    The module must call ``dex_agents.testing.run_agent_worker`` with its
    Flows. The process receives the server address and a fresh BlobCache
    directory through environment variables, and prints
    ``WORKER_READY_LINE`` once every pull queue registered. Start it again
    after ``kill`` to model a Worker replacement.

    Example:
        >>> worker = AgentWorkerProcess(engine.address, "tests.my_worker", tmp_path)
        >>> await worker.start()
        >>> worker.kill()
        >>> await worker.start()
    """

    def __init__(
        self,
        server_address: str,
        module: str,
        state_dir: Path,
        *,
        name: str = "agent Worker",
        cwd: Path | None = None,
        extra_env: Mapping[str, str] | None = None,
        python_executable: str = sys.executable,
    ) -> None:
        """Describe the Worker child without starting it.

        Args:
            server_address: Dex Server address passed to the child.
            module: Module run with ``python -m``.
            state_dir: Directory for the log and BlobCache directories.
            name: Name used in the log file name and error messages.
            cwd: Working directory, which must make ``module`` importable.
                ``None`` uses the current directory.
            extra_env: Additional environment variables for the child.
            python_executable: Interpreter, defaulting to this process's.
        """
        self._server_address = server_address
        self._module = module
        self._state_dir = state_dir
        self._cwd = cwd
        self._extra_env = dict(extra_env or {})
        self._python_executable = python_executable
        file_name = name.replace(" ", "-").lower()
        self._process = KillableProcess(name, state_dir / f"{file_name}.log")

    @property
    def process(self) -> KillableProcess:
        """Return the Worker process, for its log.

        Returns:
            The child process.
        """
        return self._process

    async def start(self, ready_timeout: timedelta = DEFAULT_READY_TIMEOUT) -> None:
        """Start the Worker and wait until its pull queues registered.

        Args:
            ready_timeout: Maximum wait for readiness.

        Raises:
            RuntimeError: If the Worker exits or misses the timeout.
        """
        blob_cache_dir = self._state_dir / f"worker-blob-cache-{uuid.uuid4().hex}"
        await self._process.start(
            [self._python_executable, "-m", self._module],
            WORKER_READY_LINE,
            cwd=self._cwd,
            env={
                **os.environ,
                SERVER_ADDRESS_ENVIRONMENT_VARIABLE: self._server_address,
                BLOB_CACHE_DIR_ENVIRONMENT_VARIABLE: str(blob_cache_dir),
                "PYTHONUNBUFFERED": "1",
                **self._extra_env,
            },
            ready_timeout=ready_timeout,
        )

    def kill(self) -> None:
        """Hard-kill the Worker with SIGKILL; in-flight Step attempts are lost."""
        self._process.kill()

    async def terminate(self, timeout: timedelta = timedelta(seconds=30)) -> None:
        """Stop the Worker gracefully with SIGTERM.

        Args:
            timeout: Maximum wait before SIGKILL.
        """
        await self._process.terminate(timeout)
