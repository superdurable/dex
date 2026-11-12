# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Dex Servers a test can crash while Worker children keep running."""

from __future__ import annotations

import asyncio
import contextlib
import os
import re
import signal
from collections.abc import Mapping, Sequence
from datetime import timedelta
from pathlib import Path
from typing import Any

from dex import Flow

from dex_agents.testing import (
    AgentTestStack,
    KillableProcess,
    PrivateDexEngine,
    find_free_port,
    find_free_ports,
    wait_until,
)
from dex_agents.testing.processes import resolve_dexcli_path, resolve_server_zd_path

# Short enough to keep a takeover near this long, long enough to survive a busy CI host.
_LEASE_DURATION = "3s"
_OBJECT_STORE_READY_LINE = "embedded S3 emulator serving"
_NODE_SERVING_LINE = "dex-server-zd serving"
_STANDBY_READY_LINE = "another process holds the node lease"
_HOT_TAKEOVER_LINE = "is_serving_hot_standby_store=true"
_ADDRESS_PATTERN = re.compile(r"address=(127\.0\.0\.1:\d+)")
_RELAY_CHUNK_BYTES = 65536


class CrashableServer(PrivateDexEngine):
    """A Dex Server whose failure the test controls."""

    async def crash(self) -> None:
        """Stop serving with SIGKILL. Running Workers lose their connections."""
        raise NotImplementedError

    async def recover(self) -> None:
        """Serve again from the state the crash left, at the same address."""
        raise NotImplementedError

    async def crash_for(self, outage: timedelta) -> None:
        """Crash, keep every node down for ``outage``, then recover."""
        await self.crash()
        await asyncio.sleep(outage.total_seconds())
        await self.recover()


class RestartedServer(CrashableServer):
    """A single node that restarts on its data directory after SIGKILL."""

    async def crash(self) -> None:
        self.kill()

    async def recover(self) -> None:
        await self.start()


class _PausableProcess(KillableProcess):
    """A process the test can freeze, so it cannot act until it is resumed."""

    def send_signal(self, signal_number: int) -> None:
        assert self._process is not None
        # A reaped leader's group ID may belong to another process group by now.
        if self._process.returncode is None:
            os.killpg(self._process.pid, signal_number)


class TcpRelay:
    """Forward TCP connections to the serving node, as a load balancer does."""

    def __init__(self) -> None:
        self._server: asyncio.Server | None = None
        self._target = ""
        self._connections: set[asyncio.StreamWriter] = set()
        self.port = 0

    async def start(self, target: str) -> None:
        self._target = target
        self._server = await asyncio.start_server(self._accept, "127.0.0.1", 0)
        self.port = int(self._server.sockets[0].getsockname()[1])

    def switch_to(self, target: str) -> None:
        self._target = target
        self.drop_connections()

    def drop_connections(self) -> None:
        for writer in tuple(self._connections):
            writer.transport.abort()

    async def close(self) -> None:
        if self._server is not None:
            self._server.close()
        self.drop_connections()

    async def _accept(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        host, port = self._target.rsplit(":", 1)
        try:
            upstream_reader, upstream_writer = await asyncio.open_connection(host, int(port))
        except OSError:
            writer.transport.abort()
            return
        self._connections.update((writer, upstream_writer))
        await asyncio.gather(
            self._copy(reader, upstream_writer),
            self._copy(upstream_reader, writer),
        )

    async def _copy(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        try:
            while data := await reader.read(_RELAY_CHUNK_BYTES):
                writer.write(data)
                await writer.drain()
        except (ConnectionError, OSError):
            pass
        finally:
            self._connections.discard(writer)
            writer.transport.abort()


class TakenOverServer(CrashableServer):
    """An active node and a hot standby sharing one S3 emulator, behind a TCP relay."""

    def __init__(self, state_dir: Path) -> None:
        super().__init__(state_dir)
        self._directory = state_dir
        self._relay = TcpRelay()
        self._object_store = KillableProcess("object store", state_dir / "object-store.log")
        self._active = KillableProcess("active node", state_dir / "active.log")
        self._standby = _PausableProcess("standby node", state_dir / "standby.log")
        self._standby_port = 0

    @property
    def address(self) -> str:
        return f"127.0.0.1:{self._relay.port}"

    async def start(self, ready_timeout: timedelta = timedelta(seconds=60)) -> None:
        try:
            await self._start_nodes(ready_timeout)
        except BaseException:
            self.kill()
            raise

    async def _start_nodes(self, ready_timeout: timedelta) -> None:
        server_zd = str(resolve_server_zd_path(resolve_dexcli_path()))
        self._directory.mkdir(parents=True, exist_ok=True)
        store_config = self._directory / "object-store.yaml"
        store_config.write_text("dev:\n  durabilityMode: processCrashSafe\n")
        await self._object_store.start(
            [
                server_zd,
                "dev",
                "--data-dir",
                str(self._directory / "object-store"),
                "--listen",
                f"127.0.0.1:{find_free_port()}",
                "--config",
                str(store_config),
            ],
            _OBJECT_STORE_READY_LINE,
            ready_timeout=ready_timeout,
        )
        endpoint = self._object_store_address()
        active_port, self._standby_port = find_free_ports(2)
        await self._active.start(
            [
                server_zd,
                "serve",
                "--config",
                str(self._write_config("active", endpoint, active_port)),
            ],
            _NODE_SERVING_LINE,
            ready_timeout=ready_timeout,
        )
        await self._standby.start(
            [
                server_zd,
                "serve",
                "--config",
                str(self._write_config("standby", endpoint, self._standby_port)),
            ],
            _STANDBY_READY_LINE,
            ready_timeout=ready_timeout,
        )
        await self._relay.start(f"127.0.0.1:{active_port}")

    async def crash(self) -> None:
        self._active.kill()
        self._relay.drop_connections()

    async def recover(self) -> None:
        async def is_serving() -> bool:
            return _NODE_SERVING_LINE in self._standby.read_log()

        await wait_until("the hot standby to serve", is_serving, timeout=timedelta(seconds=30))
        assert _HOT_TAKEOVER_LINE in self._standby.read_log()
        self._relay.switch_to(f"127.0.0.1:{self._standby_port}")

    async def crash_for(self, outage: timedelta) -> None:
        # A frozen standby cannot take the lease, so no node serves during the outage.
        self._standby.send_signal(signal.SIGSTOP)
        await self.crash()
        await asyncio.sleep(outage.total_seconds())
        self._standby.send_signal(signal.SIGCONT)
        await self.recover()

    def kill(self) -> None:
        self._active.kill()
        self._standby.kill()
        self._object_store.kill()
        with contextlib.suppress(RuntimeError):
            asyncio.get_running_loop().create_task(self._relay.close())

    def _object_store_address(self) -> str:
        match = _ADDRESS_PATTERN.search(
            next(
                line
                for line in self._object_store.read_log().splitlines()
                if _OBJECT_STORE_READY_LINE in line
            )
        )
        assert match is not None
        return match.group(1)

    def _write_config(self, name: str, object_store: str, port: int) -> Path:
        path = self._directory / f"{name}.yaml"
        path.write_text(
            f"""\
cluster:
  nodeId: agent-test-node
  partitionCount: 4
objectStore:
  endpoint: http://{object_store}
  region: us-east-1
  bucket: dex-dev
  prefix: takeover
  forcePathStyle: true
  credentialsSource: anonymous
localStorage:
  directory: {self._directory / f"{name}-store"}
  blockCacheBytes: 16777216
lease:
  duration: {_LEASE_DURATION}
  failIfHeld: false
standby:
  mode: hot
api:
  listen: "127.0.0.1:{port}"
streamStore:
  backend: memory
search:
  backend: builtin
"""
        )
        return path


class CrashableServerStack(AgentTestStack):
    """An ``AgentTestStack`` on a crashable server instead of a private engine."""

    def __init__(
        self,
        state_dir: Path,
        server: CrashableServer,
        flows: Sequence[Flow[Any]],
        worker_module: str,
        *,
        worker_cwd: Path,
        worker_env: Mapping[str, str],
    ) -> None:
        super().__init__(
            state_dir, flows, worker_module, worker_cwd=worker_cwd, worker_env=worker_env
        )
        self._engine = server
