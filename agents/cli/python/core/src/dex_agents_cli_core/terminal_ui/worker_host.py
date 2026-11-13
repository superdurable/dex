# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Worker host: the hidden ``__worker-host`` entry that runs the Worker for a launcher.

The launcher starts it in its own session, with standard input from
``/dev/null`` and output to the command's log, so nothing it or its children
write reaches the terminal. It talks to the launcher over the control
channel, an internal protocol of one CLI core version:

- launcher to host: ``start``, ``request``, and ``shutdown``;
- host to launcher: ``worker_ready`` or ``startup_failed``, then
  ``response``, ``notice``, ``worker`` (``stopping``), and
  ``session_lease_lost``.

Besides the relayed local requests, the host serves the launcher's own
``acquire_session_lease`` and ``release_session_lease`` requests. The host holds
the session's terminal lease and renews it.

It stops in order when the channel ends, on ``shutdown``, on SIGTERM, or when
its watchdog finds the launcher gone, and the watchdog bounds that stop.
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import logging
import os
import signal
import socket
import sys
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Protocol, TypeVar

from dex_agents_cli_core.terminal_ui.channel import MessageChannel
from dex_agents_cli_core.terminal_ui.contract import (
    LIST_CONNECTORS,
    PUT_IMAGE,
    START_SESSION,
    ChannelProtocolError,
    cut_notice_text,
)
from dex_agents_cli_core.terminal_ui.local_requests import RequestHandler, RequestOutcome
from dex_agents_cli_core.terminal_ui.process_groups import (
    ToolProcessGroupRegistry,
    read_process_identity,
)
from dex_agents_cli_core.terminal_ui.watchdog import ParentDeathWatchdog, set_parent_death_signal

#: The hidden first argument that runs a command as a Worker host.
WORKER_HOST_COMMAND = "__worker-host"
#: The local request methods a chat Worker host serves; the launcher relays them.
HOSTED_METHODS = (PUT_IMAGE, START_SESSION, LIST_CONNECTORS)
#: The launcher's request for a session's terminal lease: ``{session_id}``.
ACQUIRE_SESSION_LEASE = "acquire_session_lease"
#: The launcher's request to release the lease of ``{session_id}``.
RELEASE_SESSION_LEASE = "release_session_lease"
#: The host's message that another terminal took the session it held.
SESSION_LEASE_LOST = "session_lease_lost"
#: The error of ``acquire_session_lease`` for a session that cannot be opened.
SESSION_UNAVAILABLE = "session_unavailable"
_SIGTERM_EXIT_CODE = 143
_LOGGER = logging.getLogger(__name__)
_Result = TypeVar("_Result")


@dataclass(frozen=True)
class WorkerHostStart:
    """The launcher's ``start`` message: what the Worker host serves.

    Attributes:
        config_path: The configuration file, or ``None`` to run on the defaults.
        model: The ``--model`` override, or ``None``.
        resume_session_id: The ``--resume`` session, or ``None`` to start one.
        server_address: The Dex server's ``host:port`` the launcher resolved.
        is_tls: Whether the Dex server uses TLS.
        web_url: Dex Web's URL, or ``None``.
        connectors_mcp_url: The ``dexcli dev`` connectors MCP endpoint, or ``None``.
        dex_connection: The resolved Dex connection settings, as
            ``DexConnectionSettings.to_json`` returns them. They hold no secret;
            the Worker host reads it from its own environment.
    """

    config_path: str | None
    model: str | None
    resume_session_id: str | None
    server_address: str
    is_tls: bool
    web_url: str | None
    connectors_mcp_url: str | None
    dex_connection: Mapping[str, Any]

    def to_message(self) -> dict[str, Any]:
        """Return the ``start`` message."""
        return {"type": "start", **self.__dict__}

    @classmethod
    def from_message(cls, message: Mapping[str, Any]) -> WorkerHostStart:
        """Read a ``start`` message.

        Raises:
            ChannelProtocolError: If it is not one.
        """
        if message.get("type") != "start":
            raise ChannelProtocolError("the launcher's first message is not start")
        try:
            return cls(**{name: message[name] for name in cls.__dataclass_fields__})
        except KeyError as error:
            raise ChannelProtocolError(f"the start message lacks {error}") from error


class WorkerHostStartupError(Exception):
    """The Worker host cannot serve: the launcher prints the message and exits with the code."""

    def __init__(self, exit_code: int, message: str) -> None:
        """Report a startup failure with the command's exit code and its message."""
        super().__init__(message)
        self.exit_code = exit_code
        self.message = message


@dataclass
class WorkerHostReports:
    """User-facing lines from the Worker: kept for the launcher until ready, then sent."""

    channel: MessageChannel
    startup_notices: list[dict[str, str]] = field(default_factory=list)
    connectors_page_url: str | None = None
    is_ready: bool = False

    def report(self, text: str) -> None:
        """Report one line, as a warning notice."""
        notice = {"level": "warning", "text": cut_notice_text(text)}
        if self.is_ready:
            self.channel.post({"type": "notice", **notice})
        else:
            self.startup_notices.append(notice)

    def open_connectors_page(self, url: str) -> None:
        """Ask the launcher to open the Connectors page before the handoff."""
        self.connectors_page_url = url


class HostedRun(Protocol):
    """A started Worker and its session, as the Worker host serves them."""

    @property
    def session_id(self) -> str:
        """Return the session the chat opens."""
        ...

    @property
    def flow_type(self) -> str:
        """Return the session Flow type."""
        ...

    @property
    def is_other_environment_session(self) -> bool:
        """Return whether the session started in another environment, whose Flow type it polls."""
        ...

    @property
    def capabilities(self) -> Mapping[str, Any]:
        """Return the launch document's ``capabilities``."""
        ...

    @property
    def request_handlers(self) -> Mapping[str, RequestHandler]:
        """Return the local request methods it serves."""
        ...

    async def close(self) -> None:
        """Stop the Worker and release everything."""
        ...


#: Builds the Worker and opens the session; raises ``WorkerHostStartupError``.
OpenHostedRun = Callable[
    [WorkerHostStart, WorkerHostReports, ToolProcessGroupRegistry], Awaitable[HostedRun]
]


def run_worker_host(
    argv: Sequence[str], harness_name: str, run_directory: Path, open_run: OpenHostedRun
) -> int:
    """Run the Worker host until the launcher is done with it.

    Args:
        argv: The arguments after ``__worker-host``: ``--control-fd N --launcher-pid P``.
        harness_name: The harness, which names the run record.
        run_directory: ``~/.dex/agents/run``.
        open_run: Builds the Worker and opens the session.

    Returns:
        2 for bad arguments, or 1 when the launcher is already gone. Once it
        serves, it never returns: it ends the process with ``os._exit`` and
        0 after an orderly stop, 143 after SIGTERM, or the startup failure's
        code.
    """
    parser = argparse.ArgumentParser(prog=WORKER_HOST_COMMAND, add_help=False)
    parser.add_argument("--control-fd", type=int, required=True)
    parser.add_argument("--launcher-pid", type=int, required=True)
    try:
        arguments = parser.parse_args(list(argv))
    except SystemExit:
        return 2
    try:
        os.set_inheritable(arguments.control_fd, False)
        control_socket = socket.socket(fileno=arguments.control_fd)
    except OSError as error:
        _LOGGER.error("--control-fd %d is not a socket: %s", arguments.control_fd, error)
        return 2
    set_parent_death_signal()
    if os.getppid() != arguments.launcher_pid:
        _LOGGER.warning("the launcher exited before the Worker host started")
        return 1
    host = _WorkerHost(
        control_socket, arguments.launcher_pid, harness_name, run_directory, open_run
    )
    exit_code = asyncio.run(host.serve())
    # Exit at once: a thread left by a library must not hold the process past the watchdog.
    logging.shutdown()
    with contextlib.suppress(OSError, ValueError):
        sys.stdout.flush()
        sys.stderr.flush()
    os._exit(exit_code)


class _WorkerHost:
    def __init__(
        self,
        control_socket: socket.socket,
        launcher_pid: int,
        harness_name: str,
        run_directory: Path,
        open_run: OpenHostedRun,
    ) -> None:
        self._control_socket = control_socket
        self._launcher_pid = launcher_pid
        self._open_run = open_run
        self._process_groups = ToolProcessGroupRegistry(
            run_directory / f"{harness_name}-{os.getpid()}.json",
            worker_host=read_process_identity(os.getpid()),
            launcher=read_process_identity(launcher_pid),
        )
        self._stop_requested = asyncio.Event()
        self._is_sigterm = False
        self._requests: set[asyncio.Task[None]] = set()

    async def serve(self) -> int:
        loop = asyncio.get_running_loop()
        self._process_groups.write_record()
        watchdog = ParentDeathWatchdog(
            self._launcher_pid, loop, self._stop_requested.set, self._process_groups
        )
        watchdog.start()
        loop.add_signal_handler(signal.SIGTERM, self._on_sigterm)
        channel = await MessageChannel.open(self._control_socket)
        reports = WorkerHostReports(channel)
        hosted: HostedRun | None = None
        exit_code = 0
        try:
            hosted = await self._start(channel, reports)
            if hosted is not None:
                reports.is_ready = True
                await self._serve_requests(channel, hosted)
        except WorkerHostStartupError as failure:
            await channel.send(
                {"type": "startup_failed", "exit_code": failure.exit_code, "message": str(failure)}
            )
            exit_code = failure.exit_code
        except ChannelProtocolError as error:
            _LOGGER.error("the control channel broke: %s", error)
            exit_code = 1
        finally:
            watchdog.begin_shutdown()
            await channel.send({"type": "worker", "state": "stopping"})
            for request in self._requests:
                request.cancel()
            await asyncio.to_thread(self._process_groups.stop_all)
            if hosted is not None:
                await hosted.close()
            self._process_groups.remove_record()
            await channel.close()
        return _SIGTERM_EXIT_CODE if self._is_sigterm else exit_code

    async def _start(self, channel: MessageChannel, reports: WorkerHostReports) -> HostedRun | None:
        message = await self._until_stop(channel.receive())
        if message is None:
            return None
        start = WorkerHostStart.from_message(message)
        hosted = await self._until_stop(self._open_run(start, reports, self._process_groups))
        if hosted is None:
            return None
        await channel.send(
            {
                "type": "worker_ready",
                "session_id": hosted.session_id,
                "flow_type": hosted.flow_type,
                "is_other_environment_session": hosted.is_other_environment_session,
                "capabilities": dict(hosted.capabilities),
                "notices": reports.startup_notices,
                "connectors_page_url": reports.connectors_page_url,
            }
        )
        return hosted

    async def _serve_requests(self, channel: MessageChannel, hosted: HostedRun) -> None:
        handlers = hosted.request_handlers
        while True:
            message = await self._until_stop(channel.receive())
            if message is None or message["type"] == "shutdown":
                return
            if message["type"] != "request":
                continue
            request = asyncio.create_task(self._answer(channel, handlers, message))
            self._requests.add(request)
            request.add_done_callback(self._requests.discard)

    async def _answer(
        self,
        channel: MessageChannel,
        handlers: Mapping[str, RequestHandler],
        message: Mapping[str, Any],
    ) -> None:
        method = str(message.get("method"))
        handler = handlers.get(method)
        if handler is None:
            outcome = RequestOutcome.failed(
                "unknown_method", f"This command does not serve {method}.", is_remembered=False
            )
        else:
            try:
                outcome = await handler(
                    message.get("params") or {}, str(message.get("idempotency_key"))
                )
            except Exception:  # A handler bug must still answer the launcher.
                _LOGGER.exception("local request failed: method=%s", method)
                outcome = RequestOutcome.failed(
                    "request_failed", f"{method} failed; see the log.", is_remembered=False
                )
        await channel.send(
            {
                "type": "response",
                "id": message.get("id"),
                "is_remembered": outcome.is_remembered,
                **outcome.response_fields(),
            }
        )

    async def _until_stop(self, awaitable: Awaitable[_Result]) -> _Result | None:
        """Return the awaitable's result, or ``None`` once a stop is requested first."""
        work = asyncio.ensure_future(awaitable)
        stop = asyncio.create_task(self._stop_requested.wait())
        await asyncio.wait({work, stop}, return_when=asyncio.FIRST_COMPLETED)
        stop.cancel()
        if work.done():
            return work.result()
        work.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await work
        return None

    def _on_sigterm(self) -> None:
        self._is_sigterm = True
        self._stop_requested.set()
