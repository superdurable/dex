# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The launcher: start the Worker host, hand the terminal to ``dexcli agents-tui``, relay.

The launcher only runs CLI core code. It never reads or writes the terminal
while ``dexcli`` owns it: its fds 1 and 2 point at the log file meanwhile. A
failure before ``ready`` restores the terminal and returns ``PlainFallback``,
so the command continues in the plain chat on the same session.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import os
import signal
import socket
import subprocess
import sys
from collections.abc import Callable, Iterator, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from dex_agents_cli_core.terminal_ui.channel import MessageChannel
from dex_agents_cli_core.terminal_ui.child_process import ChildProcess
from dex_agents_cli_core.terminal_ui.contract import (
    CHAT_METHODS,
    DESCRIBE_TIMEOUT_SECONDS,
    OPEN_URL,
    READY_TIMEOUT_SECONDS,
    ChannelProtocolError,
    DescribeOutputError,
    build_terminal_ui_environment,
    choose_contract,
    cut_notice_text,
    is_open_url_allowed,
    parse_describe_output,
)
from dex_agents_cli_core.terminal_ui.local_requests import (
    LocalRequestServer,
    RequestHandler,
    RequestOutcome,
)
from dex_agents_cli_core.terminal_ui.process_groups import stop_recorded_processes
from dex_agents_cli_core.terminal_ui.worker_host import (
    HOSTED_METHODS,
    WORKER_HOST_COMMAND,
    WorkerHostStart,
)

#: How long the Worker host may take to stop after ``shutdown``, before SIGTERM.
WORKER_HOST_STOP_SECONDS = 10.0
#: How long a process may take after SIGTERM, before SIGKILL.
TERMINATE_GRACE_SECONDS = 2.0
#: How long dexcli may take to exit by itself after its channel ended, before SIGTERM.
CHANNEL_END_EXIT_SECONDS = 1.0
#: How long dexcli may take to exit after a SIGINT reached the launcher, before it is forwarded.
SIGINT_EXIT_SECONDS = 1.0
#: How long the browser opener may take; one still running then counts as opened.
OPENER_WAIT_SECONDS = 4.0
# Leave the alternate screen, turn bracketed paste off, and show the cursor.
_TERMINAL_RESET = "\x1b[?1049l\x1b[?2004l\x1b[?25h"
_TERMINAL_UI_EXIT_CODES = frozenset({0, 1, 130, 143})
_HOST_STOPPED_MESSAGE = "The Worker host stopped."
_TUI_ONLY_TYPES = frozenset({"ready", "launch_rejected", "result"})
_LAUNCHER_ONLY_TYPES = frozenset({"launch", "response", "notice", "worker"})
_LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True)
class DescribeResult:
    """The outcome of ``dexcli agents-tui --describe``: a contract, or why to use plain text.

    Attributes:
        contract: The contract to hand off with, or ``None``.
        plain_line: The ``Plain chat: …`` line to print when ``contract`` is ``None``.
    """

    contract: int | None
    plain_line: str | None


@dataclass(frozen=True)
class PlainFallback:
    """The handoff failed before ``ready``: continue in the plain chat.

    Attributes:
        session_id: The session the Worker host opened, to resume, or ``None``
            when it opened none and the plain chat starts as the command would.
    """

    session_id: str | None


@dataclass(frozen=True)
class TerminalUiLaunch:
    """Everything one handoff needs.

    Attributes:
        command_name: The executable, such as ``dex-agents-openai-sdk``.
        harness_name: The harness, the prefix of its session IDs.
        version: The command package's version.
        dexcli: The ``dexcli`` to run.
        contract: The contract ``--describe`` chose.
        worker_host_argv: The command line that runs this command as a Worker host.
        start: The Worker host's ``start`` message.
        terminal_variables: The launcher variables the terminal UI may get.
        display: The launch document's ``display``.
        log_path: The command's log file.
        log_path_shown: The log file as the launch document shows it.
        run_directory: ``~/.dex/agents/run``.
        is_browser_opening_enabled: Whether the command may open a web browser.
        is_resumed: Whether the chat resumes a session.
    """

    command_name: str
    harness_name: str
    version: str
    dexcli: Path
    contract: int
    worker_host_argv: Sequence[str]
    start: WorkerHostStart
    terminal_variables: Mapping[str, str]
    display: Mapping[str, Any]
    log_path: Path
    log_path_shown: str
    run_directory: Path
    is_browser_opening_enabled: bool
    is_resumed: bool


async def describe_terminal_ui(
    dexcli: Path, terminal_variables: Mapping[str, str]
) -> DescribeResult:
    """Run ``dexcli agents-tui --describe`` and choose a contract for the chat mode.

    Args:
        dexcli: The ``dexcli`` executable.
        terminal_variables: The launcher variables the terminal UI may get.

    Returns:
        The contract, or the line that says why the chat uses plain text.
    """
    environment = build_terminal_ui_environment(terminal_variables, {"mode": "none"})
    try:
        process = await asyncio.create_subprocess_exec(
            str(dexcli),
            "agents-tui",
            "--describe",
            stdin=asyncio.subprocess.DEVNULL,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.DEVNULL,
            env=environment,
        )
    except OSError as error:
        return _describe_failed(dexcli, f"it cannot run: {error.strerror}")
    try:
        stdout, _ = await asyncio.wait_for(process.communicate(), DESCRIBE_TIMEOUT_SECONDS)
    except TimeoutError:
        process.kill()
        await process.wait()
        return _describe_failed(
            dexcli, f"agents-tui --describe took over {DESCRIBE_TIMEOUT_SECONDS:.0f} s"
        )
    if process.returncode != 0:
        return _describe_failed(
            dexcli, f"agents-tui --describe exited with code {process.returncode}"
        )
    try:
        describe = parse_describe_output(stdout)
    except DescribeOutputError as error:
        return _describe_failed(dexcli, f"agents-tui --describe printed no valid document: {error}")
    choice = choose_contract(describe, "chat")
    if choice.contract is None:
        version = describe.get("dexcli_version")
        reason = (
            "speaks no terminal UI contract this command knows"
            if choice.plain_reason == "no_common_contract"
            else "has no chat terminal UI"
        )
        return DescribeResult(None, f"Plain chat: dexcli {version} {reason}.")
    return DescribeResult(choice.contract, None)


def _describe_failed(dexcli: Path, detail: str) -> DescribeResult:
    return DescribeResult(
        None,
        f"Plain chat: {dexcli} has no terminal UI ({detail}); point DEXCLI at a dexcli with "
        "agents-tui.",
    )


def worker_host_argv() -> list[str]:
    """Return the command line that runs this same command as a Worker host.

    It is the interpreter with the options and script, or ``-m`` module,
    that started this process, followed by ``__worker-host``.
    """
    prefix_length = len(sys.orig_argv) - len(sys.argv) + 1
    return [sys.executable, *sys.orig_argv[1:prefix_length], WORKER_HOST_COMMAND]


async def run_terminal_ui_chat(
    launch: TerminalUiLaunch, report: Callable[[str], None]
) -> int | PlainFallback:
    """Run one chat with the terminal UI.

    Args:
        launch: What the handoff needs.
        report: Prints one line on the terminal, before or after the handoff only.

    Returns:
        The command's exit code, or ``PlainFallback`` when the chat continues in plain text.
    """
    worker_host = await WorkerHostProcess.spawn(launch)
    try:
        ready = await worker_host.start(launch.start)
        if isinstance(ready, int):
            return ready
        if ready is None:
            report("Plain chat: the Worker host closed its control channel before it was ready.")
            return PlainFallback(None)
        for notice in ready.notices:
            report(notice)
        if ready.connectors_page_url is not None and launch.is_browser_opening_enabled:
            await open_in_browser(ready.connectors_page_url)
        report(f"Session {ready.session_id}")
        if launch.start.web_url is not None:
            report(f"Dex Web: {launch.start.web_url}/v1/flows/{ready.session_id}?view=session")
        handoff = _Handoff(launch, worker_host, ready, report)
        return await handoff.run()
    finally:
        await worker_host.stop()


@dataclass(frozen=True)
class WorkerReady:
    """The Worker host's ``worker_ready``."""

    session_id: str
    flow_type: str
    capabilities: Mapping[str, Any]
    notices: Sequence[str]
    connectors_page_url: str | None


class WorkerHostProcess:
    """The Worker host child: its control channel, its state, and its bounded stop.

    The control channel ending while the Worker host runs means the Worker
    host is no longer usable: the launcher stops it within the grace, and
    every waiting and later relayed request then gets ``host_stopped``.
    """

    def __init__(
        self,
        process: ChildProcess,
        channel: MessageChannel,
        launch: TerminalUiLaunch,
    ) -> None:
        """Wrap a spawned Worker host; use ``spawn``."""
        self._process = process
        self._channel = channel
        self._launch = launch
        loop = asyncio.get_running_loop()
        self._ready: asyncio.Future[WorkerReady | int | None] = loop.create_future()
        self._calls: dict[int, asyncio.Future[RequestOutcome]] = {}
        self._next_call_id = 1
        self._on_event: Callable[[Mapping[str, Any]], None] = lambda _: None
        self._exit_code: int | None = None
        self._is_stopping = False
        self._is_channel_ended = False
        self._stopped = asyncio.Event()
        self._reader = asyncio.create_task(self._read())
        self._watcher = asyncio.create_task(self._watch_exit())

    @classmethod
    async def spawn(cls, launch: TerminalUiLaunch) -> WorkerHostProcess:
        """Start the Worker host in its own session, with its output in the log file."""
        launcher_socket, host_socket = socket.socketpair(socket.AF_UNIX, socket.SOCK_STREAM)
        host_fd = host_socket.fileno()
        launch.log_path.parent.mkdir(parents=True, exist_ok=True)
        try:
            with launch.log_path.open("ab") as log:
                process = ChildProcess.spawn(
                    [
                        *launch.worker_host_argv,
                        "--control-fd",
                        str(host_fd),
                        "--launcher-pid",
                        str(os.getpid()),
                    ],
                    stdin=subprocess.DEVNULL,
                    stdout=log,
                    stderr=log,
                    pass_fds=(host_fd,),
                    start_new_session=True,
                )
        finally:
            host_socket.close()
        channel = await MessageChannel.open(launcher_socket)
        return cls(process, channel, launch)

    @property
    def is_stopped(self) -> bool:
        """Return whether the Worker host exited."""
        return self._exit_code is not None

    def subscribe(self, on_event: Callable[[Mapping[str, Any]], None]) -> None:
        """Receive its notices and state changes as terminal UI messages."""
        self._on_event = on_event

    async def start(self, start: WorkerHostStart) -> WorkerReady | int | None:
        """Send ``start`` and wait until the session is open.

        Returns:
            The session; the exit code to exit with after a startup failure,
            whose message was printed on standard error; or ``None`` when the
            control channel ended first.
        """
        await self._channel.send(start.to_message())
        return await self._ready

    async def call(
        self, method: str, params: Mapping[str, Any], idempotency_key: str
    ) -> RequestOutcome:
        """Relay one request and return its outcome, or ``host_stopped``."""
        if self._is_channel_ended and not self.is_stopped:
            await self._stopped.wait()
        if self.is_stopped:
            return _host_stopped()
        call_id = self._next_call_id
        self._next_call_id += 1
        future: asyncio.Future[RequestOutcome] = asyncio.get_running_loop().create_future()
        self._calls[call_id] = future
        await self._channel.send(
            {
                "type": "request",
                "id": call_id,
                "method": method,
                "params": dict(params),
                "idempotency_key": idempotency_key,
            }
        )
        try:
            return await future
        finally:
            self._calls.pop(call_id, None)

    async def stop(self) -> None:
        """Stop it in order, then with SIGTERM, then SIGKILL; then clean what it left."""
        self._is_stopping = True
        if not self.is_stopped:
            await self._channel.send({"type": "shutdown"})
            if not await self._wait_stopped(WORKER_HOST_STOP_SECONDS):
                self._process.signal_group(signal.SIGTERM)
                if not await self._wait_stopped(TERMINATE_GRACE_SECONDS):
                    self._process.signal_group(signal.SIGKILL)
                    await self._wait_stopped(None)
        await self._channel.close()
        self._reader.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await self._reader
        record = (
            self._launch.run_directory / f"{self._launch.harness_name}-{self._process.pid}.json"
        )
        if record.exists():
            await asyncio.to_thread(stop_recorded_processes, record, is_launcher_checked=False)

    async def _wait_stopped(self, timeout: float | None) -> bool:
        try:
            await asyncio.wait_for(self._stopped.wait(), timeout)
        except TimeoutError:
            return False
        return True

    async def _read(self) -> None:
        while True:
            try:
                message = await self._channel.receive()
            except ChannelProtocolError as error:
                _LOGGER.error("the Worker host's control channel broke: %s", error)
                break
            if message is None:
                break
            self._dispatch(message)
        self._end_channel()

    def _end_channel(self) -> None:
        """Stop a Worker host that can no longer answer, unless it is stopping anyway."""
        self._is_channel_ended = True
        is_startup_failed = self._ready.done() and isinstance(self._ready.result(), int)
        if self._is_stopping or self.is_stopped or is_startup_failed:
            return
        _LOGGER.error("the Worker host closed its control channel; stopping it")
        if not self._ready.done():
            self._ready.set_result(None)
        self._process.signal_group(signal.SIGTERM)
        asyncio.get_running_loop().call_later(
            TERMINATE_GRACE_SECONDS, self._process.signal_group, signal.SIGKILL
        )

    def _dispatch(self, message: Mapping[str, Any]) -> None:
        match message["type"]:
            case "worker_ready":
                if not self._ready.done():
                    self._ready.set_result(
                        WorkerReady(
                            session_id=str(message["session_id"]),
                            flow_type=str(message["flow_type"]),
                            capabilities=dict(message["capabilities"]),
                            notices=[str(notice["text"]) for notice in message["notices"]],
                            connectors_page_url=message.get("connectors_page_url"),
                        )
                    )
            case "startup_failed":
                print(f"{self._launch.command_name}: {message['message']}", file=sys.stderr)
                if not self._ready.done():
                    self._ready.set_result(int(message["exit_code"]))
            case "response":
                future = self._calls.get(int(message["id"]))
                if future is not None and not future.done():
                    future.set_result(_relayed_outcome(message))
            case "notice":
                self._on_event(
                    {
                        "type": "notice",
                        "level": message["level"],
                        "text": cut_notice_text(str(message["text"])),
                    }
                )
            case "worker":
                self._on_event({"type": "worker", "state": message["state"]})

    async def _watch_exit(self) -> None:
        returncode = await self._process.wait()
        self._exit_code = _exit_status(returncode)
        _LOGGER.info("the Worker host exited: exit=%d", self._exit_code)
        if not self._ready.done():
            print(
                f"{self._launch.command_name}: the Worker host exited with code "
                f"{self._exit_code} before the session opened; see {self._launch.log_path}",
                file=sys.stderr,
            )
            self._ready.set_result(1)
        self._on_event({"type": "worker", "state": "stopped", "exit_code": self._exit_code})
        for future in self._calls.values():
            if not future.done():
                future.set_result(_host_stopped())
        self._stopped.set()


class _Handoff:
    """One handoff of the terminal to ``dexcli agents-tui``, from spawn to exit."""

    def __init__(
        self,
        launch: TerminalUiLaunch,
        worker_host: WorkerHostProcess,
        ready: WorkerReady,
        report: Callable[[str], None],
    ) -> None:
        self._launch = launch
        self._worker_host = worker_host
        self._ready = ready
        self._report = report
        self._terminal_fd = sys.stdin.fileno()
        self._outbox: list[Mapping[str, Any]] = []
        self._channel: MessageChannel | None = None
        self._is_terminal_ui_ready = False
        self._is_terminated = False
        self._is_interrupted = False
        self._is_hung_up = False
        self._channel_end_reason: str | None = None
        self._process: ChildProcess | None = None
        self._plain_reason = ""

    async def run(self) -> int | PlainFallback:
        # POSIX only, so imported here: Windows never reaches the terminal UI path.
        import termios

        saved_mode = termios.tcgetattr(self._terminal_fd)
        self._worker_host.subscribe(self._forward_worker_event)
        loop = asyncio.get_running_loop()
        handled = (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)
        # Handlers, never SIG_IGN, so dexcli starts with the default dispositions.
        loop.add_signal_handler(signal.SIGINT, self._on_sigint)
        loop.add_signal_handler(signal.SIGTERM, self._on_sigterm)
        loop.add_signal_handler(signal.SIGHUP, self._on_sighup)
        try:
            with _output_to_log(self._launch.log_path) as terminal_output:
                outcome = await self._hand_off(terminal_output)
        finally:
            for signal_number in handled:
                loop.remove_signal_handler(signal_number)
            self._worker_host.subscribe(lambda _: None)
        if self._is_hung_up:
            return 129
        if isinstance(outcome, str) and (self._is_terminated or self._is_interrupted):
            self._restore_terminal(saved_mode, is_reset_written=True)
            self._report_resume()
            return 143 if self._is_terminated else 130
        if isinstance(outcome, str):
            self._restore_terminal(saved_mode, is_reset_written=outcome != "rejected")
            self._report(f"Plain chat: {self._plain_reason}")
            return PlainFallback(self._ready.session_id)
        return self._finish(outcome, saved_mode)

    async def _hand_off(self, terminal_output: tuple[int, int]) -> int | str:
        """Return dexcli's exit status after ``ready``, or a fallback kind before it."""
        channel_socket, terminal_ui_socket = socket.socketpair(socket.AF_UNIX, socket.SOCK_STREAM)
        child_fd = terminal_ui_socket.fileno()
        auth = {"mode": "none"}
        environment = build_terminal_ui_environment(self._launch.terminal_variables, auth)
        standard_output, standard_error = terminal_output
        try:
            self._process = ChildProcess.spawn(
                [str(self._launch.dexcli), "agents-tui", "--channel-fd", str(child_fd)],
                env=environment,
                pass_fds=(child_fd,),
                stdout=standard_output,
                stderr=standard_error,
            )
        except OSError as error:
            channel_socket.close()
            self._plain_reason = f"{self._launch.dexcli} cannot run: {error.strerror}."
            return "failed"
        finally:
            terminal_ui_socket.close()
        loop = asyncio.get_running_loop()
        ready_deadline = loop.time() + READY_TIMEOUT_SECONDS
        channel = await MessageChannel.open(channel_socket)
        self._channel = channel
        exit_task = asyncio.ensure_future(self._process.wait())
        try:
            await channel.send(self._launch_document())
            fallback = await self._wait_for_ready(channel, exit_task, ready_deadline)
            if fallback is not None:
                exit_grace = 0.0 if fallback == "timeout" else CHANNEL_END_EXIT_SECONDS
                await self._stop_terminal_ui(exit_task, exit_grace)
                return fallback
            self._is_terminal_ui_ready = True
            for message in self._outbox:
                await channel.send(message)
            self._outbox.clear()
            await self._relay(channel, exit_task)
            await self._stop_terminal_ui(exit_task, CHANNEL_END_EXIT_SECONDS)
            return await exit_task
        finally:
            await channel.close()
            if not exit_task.done():
                await self._stop_terminal_ui(exit_task, 0.0)

    async def _wait_for_ready(
        self,
        channel: MessageChannel,
        exit_task: asyncio.Future[int],
        deadline: float,
    ) -> str | None:
        loop = asyncio.get_running_loop()
        while True:
            receive = asyncio.create_task(channel.receive())
            remaining = max(0.0, deadline - loop.time())
            waited: set[asyncio.Future[Any]] = {receive, exit_task}
            done, _ = await asyncio.wait(
                waited, timeout=remaining, return_when=asyncio.FIRST_COMPLETED
            )
            if receive not in done:
                receive.cancel()
                with contextlib.suppress(asyncio.CancelledError):
                    await receive
                if exit_task in done:
                    self._plain_reason = (
                        f"dexcli exited with code {_exit_status(exit_task.result())} before the "
                        "terminal UI was ready."
                    )
                    return "failed"
                self._plain_reason = (
                    f"the terminal UI was not ready in {READY_TIMEOUT_SECONDS:.0f} s."
                )
                return "timeout"
            try:
                message = receive.result()
            except ChannelProtocolError:
                self._plain_reason = "dexcli sent a malformed line before it was ready."
                return "failed"
            if message is None:
                self._plain_reason = "dexcli closed the channel before it was ready."
                return "failed"
            if message["type"] == "ready":
                return None
            if message["type"] == "launch_rejected":
                text = " ".join(str(message.get("message", "")).split())
                self._plain_reason = cut_notice_text(text) or "dexcli rejected the launch."
                return "rejected"
            if message["type"] in _TUI_ONLY_TYPES | _LAUNCHER_ONLY_TYPES:
                self._plain_reason = "dexcli sent another message before ready."
                return "failed"

    async def _relay(self, channel: MessageChannel, exit_task: asyncio.Future[int]) -> None:
        handlers: dict[str, RequestHandler] = {
            method: self._relayed_handler(method) for method in HOSTED_METHODS
        }
        handlers[OPEN_URL] = self._open_url
        server = LocalRequestServer(handlers, channel.send)
        reading = asyncio.create_task(self._read_requests(channel, server))
        try:
            waited: set[asyncio.Future[Any]] = {reading, exit_task}
            await asyncio.wait(waited, return_when=asyncio.FIRST_COMPLETED)
        finally:
            if not reading.done():
                reading.cancel()
                with contextlib.suppress(asyncio.CancelledError):
                    await reading
            await server.close()

    async def _read_requests(self, channel: MessageChannel, server: LocalRequestServer) -> None:
        while True:
            try:
                message = await channel.receive()
            except ChannelProtocolError as error:
                _LOGGER.error("the terminal UI channel broke: %s", error)
                self._channel_end_reason = "dexcli sent a malformed line"
                await channel.close()
                return
            if message is None:
                self._channel_end_reason = "dexcli closed the channel"
                return
            kind = message["type"]
            if kind in ("request", "cancel"):
                await server.handle_message(message)
            elif kind in _LAUNCHER_ONLY_TYPES:
                # A known type from the wrong side is malformed: dropped, the channel open.
                _LOGGER.warning("dropped a %s message from dexcli", kind)

    def _relayed_handler(self, method: str) -> RequestHandler:
        async def relay(params: Mapping[str, Any], idempotency_key: str) -> RequestOutcome:
            return await self._worker_host.call(method, params, idempotency_key)

        return relay

    async def _open_url(self, params: Mapping[str, Any], idempotency_key: str) -> RequestOutcome:
        url = params.get("url")
        if not isinstance(url, str) or not is_open_url_allowed(self._launch.start.web_url, url):
            return RequestOutcome.failed("refused", "open_url only opens Dex Web pages.")
        if not self._launch.is_browser_opening_enabled:
            return RequestOutcome.failed("browser_off", "Opening a browser is turned off.")
        if not await open_in_browser(url):
            return RequestOutcome.failed("open_failed", "No web browser opened the page.")
        return RequestOutcome.succeeded({})

    def _launch_document(self) -> dict[str, Any]:
        launch = self._launch
        return {
            "type": "launch",
            "contract": launch.contract,
            "mode": "chat",
            "command": {
                "name": launch.command_name,
                "harness": launch.harness_name,
                "language": "python",
                "version": launch.version,
            },
            "dex": {
                "server_address": launch.start.server_address,
                "tls": {"enabled": launch.start.is_tls, "ca_file": None, "server_name": None},
                "auth": {"mode": "none"},
                "web_url": launch.start.web_url,
            },
            "session": {
                "flow_type": self._ready.flow_type,
                "session_id": self._ready.session_id,
                "is_resumed": launch.is_resumed,
            },
            "display": dict(launch.display),
            "methods": list(CHAT_METHODS),
            "capabilities": dict(self._ready.capabilities),
            "notices": [],
            "log_path": launch.log_path_shown,
        }

    def _forward_worker_event(self, message: Mapping[str, Any]) -> None:
        if self._channel is None or not self._is_terminal_ui_ready:
            self._outbox.append(message)
        else:
            self._channel.post(message)

    async def _stop_terminal_ui(self, exit_task: asyncio.Future[int], exit_grace: float) -> None:
        """Give dexcli ``exit_grace`` seconds to exit, then SIGTERM, and SIGKILL 2 s later."""
        process = self._process
        if process is None or exit_task.done():
            return
        if exit_grace > 0 and await _waits_within(exit_task, exit_grace):
            return
        process.send_signal(signal.SIGTERM)
        if not await _waits_within(exit_task, TERMINATE_GRACE_SECONDS):
            process.send_signal(signal.SIGKILL)
            await exit_task

    def _finish(self, returncode: int, saved_mode: list[Any]) -> int:
        status = _exit_status(returncode)
        is_unexpected = returncode < 0 or returncode not in _TERMINAL_UI_EXIT_CODES
        is_signaled = self._is_terminated or self._is_interrupted
        self._restore_terminal(saved_mode, is_reset_written=is_unexpected or is_signaled)
        if is_unexpected and not is_signaled:
            self._report(f"The chat closed unexpectedly (dexcli exit {status}).")
        elif returncode != 0 and self._channel_end_reason is not None and not is_signaled:
            self._report(f"The chat closed: {self._channel_end_reason}.")
        self._report_resume()
        if self._is_terminated:
            return 143
        if self._is_interrupted or returncode == 130:
            return 130
        if is_unexpected or returncode in (1, 143) or self._worker_host.is_stopped:
            return 1
        return 0

    def _report_resume(self) -> None:
        session_id = self._ready.session_id
        self._report(
            f"\nSession {session_id} keeps running. Resume it with: "
            f"{self._launch.command_name} --resume {session_id}"
        )

    def _restore_terminal(self, saved_mode: list[Any], *, is_reset_written: bool) -> None:
        import termios

        with contextlib.suppress(termios.error, OSError):
            termios.tcsetattr(self._terminal_fd, termios.TCSADRAIN, saved_mode)
        if is_reset_written:
            with contextlib.suppress(OSError):
                sys.stdout.write(_TERMINAL_RESET)
                sys.stdout.flush()

    def _on_sigterm(self) -> None:
        self._is_terminated = True
        process = self._process
        if process is not None:
            process.send_signal(signal.SIGTERM)
            asyncio.get_running_loop().call_later(
                TERMINATE_GRACE_SECONDS, process.send_signal, signal.SIGKILL
            )

    def _on_sigint(self) -> None:
        """A SIGINT that reached the launcher: let dexcli exit, else forward it, then SIGTERM."""
        self._is_interrupted = True
        process = self._process
        if process is None:
            return
        loop = asyncio.get_running_loop()
        loop.call_later(SIGINT_EXIT_SECONDS, process.send_signal, signal.SIGINT)
        loop.call_later(
            SIGINT_EXIT_SECONDS + TERMINATE_GRACE_SECONDS, process.send_signal, signal.SIGTERM
        )
        loop.call_later(
            SIGINT_EXIT_SECONDS + 2 * TERMINATE_GRACE_SECONDS, process.send_signal, signal.SIGKILL
        )

    def _on_sighup(self) -> None:
        self._is_hung_up = True
        process = self._process
        if process is not None:
            asyncio.get_running_loop().call_later(
                TERMINATE_GRACE_SECONDS, process.send_signal, signal.SIGKILL
            )


_BROWSER_OPENERS: set[asyncio.Task[None]] = set()


async def open_in_browser(url: str) -> bool:
    """Open a URL with Python's ``webbrowser`` in a child whose output goes nowhere.

    The child honors ``BROWSER``, and never writes to the terminal the
    terminal UI owns. An opener that still runs after 4 s counts as opened;
    it runs on in its own session, and the launcher still reaps it.

    Returns:
        Whether a browser took the URL: ``False`` when the opener could not
        start or exited non-zero within 4 s.
    """
    try:
        process = await asyncio.create_subprocess_exec(
            sys.executable,
            "-c",
            "import sys, webbrowser; sys.exit(0 if webbrowser.open(sys.argv[1]) else 1)",
            url,
            stdin=asyncio.subprocess.DEVNULL,
            stdout=asyncio.subprocess.DEVNULL,
            stderr=asyncio.subprocess.DEVNULL,
            start_new_session=True,
        )
    except OSError as error:
        _LOGGER.warning("cannot start the browser opener: %s", error)
        return False
    waiting = asyncio.ensure_future(process.wait())
    if await _waits_within(waiting, OPENER_WAIT_SECONDS):
        return waiting.result() == 0
    _LOGGER.info("the browser opener still runs after %.0f s", OPENER_WAIT_SECONDS)
    reaping = asyncio.ensure_future(_reap_opener(waiting))
    _BROWSER_OPENERS.add(reaping)
    reaping.add_done_callback(_BROWSER_OPENERS.discard)
    return True


async def _reap_opener(waiting: asyncio.Future[int]) -> None:
    returncode = await waiting
    _LOGGER.info("the browser opener exited: exit=%d", returncode)


async def _waits_within(future: asyncio.Future[int], seconds: float) -> bool:
    """Return whether ``future`` finished within ``seconds``, leaving it running otherwise."""
    try:
        await asyncio.wait_for(asyncio.shield(future), seconds)
    except TimeoutError:
        return False
    return True


@contextlib.contextmanager
def _output_to_log(log_path: Path) -> Iterator[tuple[int, int]]:
    """Point fds 1 and 2 at the log while dexcli owns the terminal.

    Yields:
        Copies of the terminal's fds 1 and 2, for dexcli.
    """
    sys.stdout.flush()
    sys.stderr.flush()
    terminal_output = (os.dup(1), os.dup(2))
    log_fd = os.open(log_path, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o600)
    try:
        os.dup2(log_fd, 1)
        os.dup2(log_fd, 2)
        yield terminal_output
    finally:
        with contextlib.suppress(OSError, ValueError):
            sys.stdout.flush()
            sys.stderr.flush()
        os.dup2(terminal_output[0], 1)
        os.dup2(terminal_output[1], 2)
        for fd in (*terminal_output, log_fd):
            os.close(fd)


def _relayed_outcome(message: Mapping[str, Any]) -> RequestOutcome:
    is_remembered = bool(message.get("is_remembered", True))
    error = message.get("error")
    if isinstance(error, dict):
        return RequestOutcome.failed(
            str(error.get("code")), str(error.get("message")), is_remembered=is_remembered
        )
    return RequestOutcome(result=dict(message.get("result") or {}), is_remembered=is_remembered)


def _host_stopped() -> RequestOutcome:
    return RequestOutcome.failed("host_stopped", _HOST_STOPPED_MESSAGE, is_remembered=False)


def _exit_status(returncode: int) -> int:
    return 128 - returncode if returncode < 0 else returncode
