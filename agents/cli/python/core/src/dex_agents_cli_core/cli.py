# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command line of every harness command: ``init``, ``run``, ``connectors``, ``sessions``."""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import dataclasses
import importlib
import importlib.metadata
import logging
import os
import secrets
import signal
import socket
import sys
import webbrowser
from collections.abc import Awaitable, Callable, Coroutine, Mapping, Sequence
from dataclasses import dataclass, replace
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

import httpx2
from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCache,
    BlobCacheConfig,
    DexServiceError,
    Flow,
    FlowNotFoundError,
    PersistenceSchema,
    PullWorkerOptions,
    Registry,
    StartFlowOptions,
    open_blob_cache,
)

from dex_agents import (
    AGENT_SESSION_LAST_ACTIVITY_AT,
    AGENT_SESSION_TITLE,
    AGENT_SESSION_TURN_COUNT,
    SESSION_LAST_TERMINAL_ENVIRONMENT,
    AgentSessionFlow,
    AgentSessionSummary,
    AgentSessionSummaryPage,
    LocalDirectoryMediaStore,
    MediaStore,
    SessionTerminalLease,
    list_agent_sessions,
    session_flow_definition_document,
    sort_agent_sessions_by_last_activity,
)
from dex_agents.builtin_loop import ToolSource
from dex_agents.mcp import McpStreamableHttpServerConfig
from dex_agents_cli_core.chat import Terminal, TerminalChat
from dex_agents_cli_core.config import (
    AUTO,
    ConfigError,
    ConnectorEntry,
    EffectiveConfig,
    is_session_summary_indexed,
)
from dex_agents_cli_core.config_file import (
    ConfigFileError,
    LoadedConfig,
    ask_config_values,
    default_config_path,
    default_config_values,
    defaults_without_file,
    load_config_file,
    write_config_file,
)
from dex_agents_cli_core.connectors import (
    ConnectorCatalogTool,
    ConnectorsMcpAccessDeniedError,
    ConnectorsMcpListingError,
    ConnectorsMcpToolSource,
    ConnectorsMcpUnavailableError,
    connectors_access_denied_sentence,
    connectors_unavailable_line,
    read_connector_catalog,
)
from dex_agents_cli_core.dex_connection import (
    DexConnection,
    DexConnectionSettings,
    DexConnectionSettingsError,
    DexRefusalDetailsFilter,
    describe_dex_refusal_without_details,
    describe_rejected_dex_credential,
    resolve_dex_connection_settings,
)
from dex_agents_cli_core.dex_server import (
    DexServerEndpoint,
    DexServerError,
    StartedDexcliDevProcess,
    describe_lost_dexcli_dev,
    describe_unavailable_dex_server,
    resolve_dex_server,
)
from dex_agents_cli_core.dexcli_install import (
    DexcliInstallError,
    DexcliVariableError,
    download_dexcli,
    locate_dexcli,
)
from dex_agents_cli_core.environment import CommandEnvironment, remove_dex_secret_variables
from dex_agents_cli_core.harness import (
    SESSION_ACTION_PERMISSION,
    AgentHarness,
    HarnessContext,
    LocalToolGroup,
    SessionEnvironmentIdentity,
    TerminalInterface,
    current_session_environment_identity,
    declared_reasoning_efforts,
    declared_suggested_models,
    namespace_session_flow_type,
    session_start_environment,
)
from dex_agents_cli_core.local_tools import LocalToolSource
from dex_agents_cli_core.local_tools.shell import ShellProcessGroups
from dex_agents_cli_core.mcp_servers import (
    McpServerSettingsError,
    McpServersToolSource,
    mcp_server_secret_texts,
    resolve_mcp_servers,
)
from dex_agents_cli_core.session_lease import (
    HeldSessionLease,
    PullPermitExpiredError,
    SessionLeaseHolder,
    SessionLeaseWaitInterrupted,
    build_lease_lost_line,
    build_lease_unconfirmed_line,
    format_lease_holder,
    is_waiting_line_animated,
    terminal_columns,
    wait_for_session_lease,
)
from dex_agents_cli_core.session_list import collect_sessions_for_list
from dex_agents_cli_core.session_opener import (
    OpenableSession,
    SessionOpener,
    SessionQueueWorker,
    SessionWorkerStartError,
    TakenOverTurnRefusal,
    UnopenableSessionError,
)
from dex_agents_cli_core.session_task_queues import (
    cli_app_worker_id,
    describe_session_environment,
    is_named_task_queue_protocol_error,
    named_task_queue_server_error,
    session_task_queue,
)
from dex_agents_cli_core.terminal_ui.contract import LIST_CONNECTORS, PUT_IMAGE, START_SESSION
from dex_agents_cli_core.terminal_ui.hosted_session import (
    HostedChatSession,
    session_flow_capabilities,
)
from dex_agents_cli_core.terminal_ui.launcher import (
    PlainFallback,
    TerminalUiLaunch,
    describe_terminal_ui,
    run_terminal_ui_chat,
    worker_host_argv,
)
from dex_agents_cli_core.terminal_ui.local_requests import RequestHandler, RequestOutcome
from dex_agents_cli_core.terminal_ui.process_groups import (
    ToolProcessGroupRegistry,
    stop_leftover_processes,
)
from dex_agents_cli_core.terminal_ui.worker_host import (
    ACQUIRE_SESSION_LEASE,
    RELEASE_SESSION_LEASE,
    SESSION_LEASE_LOST,
    SESSION_LEASE_UNCONFIRMED,
    SESSION_UNAVAILABLE,
    WORKER_HOST_COMMAND,
    HostedRun,
    WorkerHostReports,
    WorkerHostStart,
    WorkerHostStartupError,
    run_worker_host,
)
from dex_agents_cli_core.turns import TurnConfigResolver, with_turn_config

#: The Dex Web page where connections are authorized.
CONNECTORS_WEB_PATH = "/v1/connectors"
#: The directory under ``~/.dex/agents`` that ``media.store: auto`` keeps images in.
MEDIA_DIRECTORY = "media"
_BLOB_CACHE_BYTES = 64 * 1024 * 1024
_SESSION_SUMMARY_ATTRIBUTE_NAMES = frozenset(
    definition.name
    for definition in (
        AGENT_SESSION_TITLE,
        AGENT_SESSION_LAST_ACTIVITY_AT,
        AGENT_SESSION_TURN_COUNT,
    )
)
_WORKER_REGISTRATION_TIMEOUT_SECONDS = 30.0
# A Flow ID no session uses; describing it proves the credential before the Worker starts.
_CREDENTIAL_PROBE_FLOW_ID = "dex-agents-cli-credential-probe"
# How long the command waits for the session graph upload before it logs a failure.
_SESSION_FLOW_DEFINITION_UPLOAD_TIMEOUT_SECONDS = 10.0
# How many sessions the sessions subcommand reads the last environment of at once.
_ENVIRONMENT_READ_CONCURRENCY = 8
# How long a Worker drains its running tasks when it stops.
_WORKER_STOP_DRAIN = timedelta(seconds=5)
# The shell's exit code for a command that Ctrl-C ended.
_INTERRUPTED_EXIT_CODE = 130
_LOGGER = logging.getLogger(__name__)
_TERMINATION_SIGNALS = tuple(
    getattr(signal, name) for name in ("SIGTERM", "SIGHUP") if hasattr(signal, name)
)


class CommandError(Exception):
    """Report a usage or configuration error; the command prints it and exits with code 2."""


class CommandFailure(Exception):
    """Report a failure at run time; the command prints it and exits with code 1."""


class _InitCancelled(Exception):
    """Ctrl-C or the end of input during the init questions; nothing was written."""

    def __init__(self, exit_code: int) -> None:
        super().__init__("the init questions were cancelled")
        self.exit_code = exit_code


@dataclass(frozen=True)
class _Arguments:
    command: str
    config_path: Path | None
    is_yes: bool
    resume_session_id: str | None
    model: str | None
    is_plain: bool = False


def run_agent_cli(harness: AgentHarness, argv: Sequence[str] | None = None) -> int:
    """Run a harness command: parse arguments, then run its subcommand.

    Call it from a harness package's console script:

    Example:
        >>> def run() -> None:
        ...     raise SystemExit(run_agent_cli(ClaudeHarness()))

    A ``dexcli dev`` that the command started for ``dex.server: auto`` stops
    when the command exits: on ``/exit``, the end of input, the Ctrl-C that
    exits, a failure, SIGTERM, or SIGHUP. A signal makes it return 128 plus
    the signal number. A ``dexcli dev`` the command reused keeps running. To
    share one across sessions, run ``dexcli dev`` yourself first. When a
    reused ``dexcli dev`` stops during the command, the command fails with a
    short error that says so.

    .. versionchanged:: 0.6.0
       A started ``dexcli dev`` stops when the command exits instead of
       running on.

    Args:
        harness: The command's harness.
        argv: The arguments without the program name; ``None`` reads ``sys.argv``.

    Returns:
        The exit code: 0 on success, 1 for a failure at run time, such as a
        Dex server or an explicit connectors MCP server that is unavailable,
        2 for a usage or configuration error, 130 for Ctrl-C during the init
        questions, and 128 plus the signal number after SIGTERM or SIGHUP.
        Input that ends during the init questions exits with 1. A cancelled
        init writes no file. Ctrl-C while the command waits for a session
        that another terminal has open exits with 130.
    """
    # A bad declaration is the harness's programming error, so it raises in every subcommand.
    declared_reasoning_efforts(harness)
    declared_suggested_models(harness)
    raw_arguments = list(sys.argv[1:] if argv is None else argv)
    if raw_arguments[:1] == [WORKER_HOST_COMMAND]:
        return _run_worker_host(harness, raw_arguments[1:])
    arguments = _parse_arguments(harness, raw_arguments)
    try:
        environment = CommandEnvironment.from_environment()
    except ValueError as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2
    remove_dex_secret_variables(os.environ)
    log_path = _configure_logging(environment, harness)
    server = _CommandDexServer(environment)
    try:
        if arguments.command == "init":
            return _init(arguments, environment, harness)
        # Outside asyncio.run, because its SIGINT handler would keep Ctrl-C from ending input().
        loaded = _load_config(arguments, environment, harness)
        match arguments.command:
            case "sessions":
                return server.run(_list_sessions(loaded, environment, harness, server))
            case "connectors":
                return server.run(_list_connectors(loaded, environment, harness, server))
            case _:
                return server.run(_run(arguments, loaded, environment, harness, log_path, server))
    except _InitCancelled as cancelled:
        print("\nCancelled; no configuration written.", file=sys.stderr)
        return cancelled.exit_code
    except SessionLeaseWaitInterrupted:
        return _INTERRUPTED_EXIT_CODE
    except (CommandError, ConfigFileError) as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2
    except (CommandFailure, DexServerError) as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 1
    except Exception as error:
        refused = describe_dex_refusal_without_details(error)
        if refused is None and isinstance(error, DexServiceError):
            _LOGGER.error("the command failed", exc_info=error)
            refused = _describe_dex_service_error(error)
        if refused is None:
            raise
        print(f"{command_name(harness)}: {refused}", file=sys.stderr)
        return 1
    finally:
        server.stop_started_dexcli_dev()


class _CommandDexServer:
    """The Dex server one command uses, and a ``dexcli dev`` it started, which it stops on exit."""

    def __init__(self, environment: CommandEnvironment) -> None:
        self._environment = environment
        self._started_process = StartedDexcliDevProcess(environment)
        self._reused_web_url: str | None = None
        self.started_dexcli_dev_lines: list[str] = []
        self._command_task: asyncio.Task[Any] | None = None
        self._received_signals: list[int] = []

    def run(self, command: Coroutine[Any, Any, int]) -> int:
        """Run an async subcommand; SIGTERM and SIGHUP end it with 128 plus the signal number."""
        return asyncio.run(self._run_until_terminated(command))

    async def _run_until_terminated(self, command: Coroutine[Any, Any, int]) -> int:
        # Cancelling the task, rather than raising in a signal handler, unwinds every await cleanly.
        loop = asyncio.get_running_loop()
        self._command_task = asyncio.current_task()
        assert self._command_task is not None, "asyncio.run runs this coroutine in a task"
        handled_signals = self.install_termination_handlers()
        try:
            return await self._fail_short_on_a_lost_server(command)
        except asyncio.CancelledError:
            if not self._received_signals:
                raise
            return 128 + self._received_signals[0]
        finally:
            for signal_number in handled_signals:
                loop.remove_signal_handler(signal_number)

    def install_termination_handlers(self) -> list[int]:
        """Make SIGTERM and SIGHUP end the command; the terminal UI handoff calls it again after.

        Returns:
            The signals now handled; none on Windows, whose loops take no handlers.
        """
        loop = asyncio.get_running_loop()
        handled_signals: list[int] = []
        for signal_number in _TERMINATION_SIGNALS:
            try:
                loop.add_signal_handler(signal_number, self._cancel_on_signal, signal_number)
            except NotImplementedError:
                break  # Windows loops take no signal handlers; Ctrl-C stays KeyboardInterrupt.
            handled_signals.append(signal_number)
        return handled_signals

    def _cancel_on_signal(self, signal_number: int) -> None:
        self._received_signals.append(signal_number)
        if self._command_task is not None:
            self._command_task.cancel()

    async def resolve(self, config: EffectiveConfig) -> DexServerEndpoint:
        """Find or start the Dex server of ``dex.server``, printing its progress lines.

        When it starts a ``dexcli dev``, those lines stay in
        ``started_dexcli_dev_lines`` too, for the terminal UI to show again.
        """
        lines: list[str] = []

        def report(line: str) -> None:
            print(line)
            lines.append(line)

        endpoint = await resolve_dex_server(
            config.dex.server,
            self._environment,
            report=report,
            started_process=self._started_process,
        )
        if endpoint.started_pid is None and endpoint.web_url is not None:
            self._reused_web_url = endpoint.web_url
        if endpoint.started_pid is not None:
            self.started_dexcli_dev_lines = lines
        return endpoint

    def stop_started_dexcli_dev(self) -> None:
        """Stop the ``dexcli dev`` this command started, if any."""
        self._started_process.stop(report=print)

    async def _fail_short_on_a_lost_server(self, command: Coroutine[Any, Any, int]) -> int:
        try:
            return await command
        except (CommandError, ConfigFileError, CommandFailure, DexServerError):
            raise
        except Exception as error:
            lost = (
                None
                if self._reused_web_url is None
                else await describe_lost_dexcli_dev(self._reused_web_url)
            )
            if lost is None:
                raise
            raise CommandFailure(lost) from error


def _run_worker_host(harness: AgentHarness, argv: Sequence[str]) -> int:
    """Run the hidden ``__worker-host`` entry that a launcher starts."""
    try:
        environment = CommandEnvironment.from_environment()
    except ValueError as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2
    remove_dex_secret_variables(os.environ)
    log_path = _configure_logging(environment, harness)

    async def open_run(
        start: WorkerHostStart,
        reports: WorkerHostReports,
        process_groups: ToolProcessGroupRegistry,
    ) -> HostedRun:
        return await _open_hosted_run(
            environment, harness, start, reports, process_groups, log_path
        )

    return run_worker_host(argv, harness.name, environment.agents_directory / "run", open_run)


async def _open_hosted_run(
    environment: CommandEnvironment,
    harness: AgentHarness,
    start: WorkerHostStart,
    reports: WorkerHostReports,
    process_groups: ToolProcessGroupRegistry,
    log_path: Path | None,
) -> HostedRun:
    """Build the Worker and open the session for a launcher, as ``run`` does in one process."""
    try:
        loaded = (
            load_config_file(Path(start.config_path), harness)
            if start.config_path is not None
            else defaults_without_file(harness, environment.home)
        )

        def report_ignored_settings(warnings: Sequence[str]) -> None:
            reports.report(f"Session settings keys ignored: {', '.join(warnings)}")

        inputs = _prepare_run(
            start.model,
            loaded,
            environment,
            harness,
            report_ignored_settings,
            DexConnectionSettings.from_json(start.dex_connection),
        )
        endpoint = DexServerEndpoint(
            address=start.server_address,
            is_tls=start.is_tls,
            web_url=start.web_url,
            connectors_mcp_url=start.connectors_mcp_url,
        )
        session_worker = await _SessionWorker.start(
            environment,
            harness,
            loaded,
            inputs,
            endpoint,
            reports=_SessionWorkerReports(
                report=reports.report,
                report_runtime=reports.report,
                open_connectors_page=reports.open_connectors_page,
                log_path=log_path,
            ),
            # The launcher is the Worker host's parent: its pid is the one a waiting terminal shows.
            holder=SessionLeaseHolder.for_this_process(
                environment.session_lease_ttl_seconds, process_id=os.getppid()
            ),
            process_groups=process_groups,
        )
        try:
            session = await session_worker.open_session(start.resume_session_id)
        except BaseException:
            await session_worker.close()
            raise
    except (CommandError, ConfigFileError, ConfigError) as error:
        raise WorkerHostStartupError(2, str(error)) from error
    except (CommandFailure, DexServerError) as error:
        raise WorkerHostStartupError(1, str(error)) from error
    hosted = HostedChatSession(
        harness_name=harness.name,
        client=session_worker.client,
        sessions=session_worker.sessions,
        media_store=inputs.media_store,
        start_environment=session_start_environment(session_worker.identity),
        connectors=inputs.config.connectors,
        connectors_mcp_url=_connectors_mcp_url(inputs.config, endpoint),
        web_url=endpoint.web_url,
    )
    lease = HeldSessionLease(session_worker.opener)
    return _HostedCliRun(
        session_worker,
        session,
        hosted,
        declared_reasoning_efforts(harness),
        _HostedSessionLease(session_worker, lease, reports),
    )


class _HostedSessionLease:
    """The Worker host's side of the session lease: the launcher's requests and lost leases."""

    def __init__(
        self,
        session_worker: _SessionWorker,
        lease: HeldSessionLease,
        reports: WorkerHostReports,
    ) -> None:
        self._session_worker = session_worker
        self._lease = lease
        self._reports = reports
        lease.lost.add_done_callback(self._report_lost)
        lease.unconfirmed.add_done_callback(self._report_unconfirmed)

    async def acquire(self, params: Mapping[str, Any], idempotency_key: str) -> RequestOutcome:
        """Try once for ``params.session_id``'s lease after checking that the session can open.

        An acquired lease answers with the session's Flow type and the lines
        about a takeover from another place.
        """
        session_id = params.get("session_id")
        if not isinstance(session_id, str) or not session_id:
            return RequestOutcome.failed(
                "invalid_params", "session_id must be a session ID.", is_remembered=False
            )
        opener = self._session_worker.opener
        try:
            session = await opener.describe_openable_session(session_id)
            result = await self._lease.try_acquire(session)
        except (UnopenableSessionError, SessionWorkerStartError) as error:
            return RequestOutcome.failed(SESSION_UNAVAILABLE, str(error), is_remembered=False)
        except (DexServiceError, TimeoutError, PullPermitExpiredError) as error:
            # Asked again later: an expired pull permit pulls and confirms again on the next try.
            return RequestOutcome.failed(
                "lease_call_failed", str(error) or type(error).__name__, is_remembered=False
            )
        lease = None if result.lease is None else dataclasses.asdict(result.lease)
        if not result.is_acquired:
            return RequestOutcome.succeeded({"is_acquired": False, "lease": lease})
        notices = await opener.complete_takeover(session_id)
        return RequestOutcome.succeeded(
            {
                "is_acquired": True,
                "lease": lease,
                "flow_type": session.flow_type,
                "notices": notices,
            }
        )

    async def release(self, params: Mapping[str, Any], idempotency_key: str) -> RequestOutcome:
        """Release the held lease when it is ``params.session_id``'s."""
        if params.get("session_id") == self._lease.session_id:
            await self._lease.release()
        return RequestOutcome.succeeded({})

    async def close(self) -> None:
        """Release the held lease, before the Worker stops."""
        await self._lease.release()

    def _report_lost(self, lost: asyncio.Future[tuple[str, SessionTerminalLease]]) -> None:
        if lost.cancelled():
            return
        session_id, holder = lost.result()
        self._reports.channel.post(
            {
                "type": SESSION_LEASE_LOST,
                "session_id": session_id,
                "lease": dataclasses.asdict(holder),
            }
        )

    def _report_unconfirmed(self, unconfirmed: asyncio.Future[str]) -> None:
        if not unconfirmed.cancelled():
            self._reports.channel.post(
                {"type": SESSION_LEASE_UNCONFIRMED, "session_id": unconfirmed.result()}
            )


class _HostedCliRun:
    """The Worker host's started Worker, its session, and the requests it serves."""

    def __init__(
        self,
        session_worker: _SessionWorker,
        session: OpenableSession,
        hosted: HostedChatSession,
        supported_reasoning_efforts: Sequence[str],
        session_lease: _HostedSessionLease,
    ) -> None:
        self._session_worker = session_worker
        self._session = session
        self._supported_reasoning_efforts = tuple(supported_reasoning_efforts)
        self._session_lease = session_lease
        self._request_handlers: dict[str, RequestHandler] = {
            PUT_IMAGE: hosted.put_image,
            START_SESSION: hosted.start_session,
            LIST_CONNECTORS: hosted.list_connectors,
            ACQUIRE_SESSION_LEASE: session_lease.acquire,
            RELEASE_SESSION_LEASE: session_lease.release,
        }

    @property
    def session_id(self) -> str:
        return self._session.session_id

    @property
    def flow_type(self) -> str:
        return self._session.flow_type

    @property
    def capabilities(self) -> Mapping[str, Any]:
        return session_flow_capabilities(
            self._session.sessions.flow,
            supported_reasoning_efforts=self._supported_reasoning_efforts,
        )

    @property
    def request_handlers(self) -> Mapping[str, RequestHandler]:
        return self._request_handlers

    async def close(self) -> None:
        await self._session_lease.close()
        await self._session_worker.close()


def command_name(harness: AgentHarness) -> str:
    """Return a harness's command name, ``dex-agents-<name>``.

    Args:
        harness: The harness.

    Returns:
        The console script name.
    """
    return f"dex-agents-{harness.name}"


def _parse_arguments(harness: AgentHarness, argv: Sequence[str] | None) -> _Arguments:
    parser = argparse.ArgumentParser(
        prog=command_name(harness),
        description="Chat with a durable Dex agent in the terminal.",
    )
    parser.add_argument(
        "command",
        nargs="?",
        default="run",
        choices=("run", "init", "connectors", "sessions"),
        help="run (the default) chats; init writes the configuration file; connectors lists "
        "the connector tools; sessions lists this agent's sessions",
    )
    parser.add_argument("-c", "--config", type=Path, default=None, help="configuration file")
    parser.add_argument(
        "-y", "--yes", action="store_true", help="write and use the defaults instead of asking"
    )
    parser.add_argument("-r", "--resume", metavar="SESSION_ID", help="continue a session")
    parser.add_argument("-m", "--model", help="override the configured model")
    parser.add_argument(
        "--plain",
        action="store_true",
        help="chat in plain text lines, without the dexcli terminal UI",
    )
    namespace = parser.parse_args(argv)
    return _Arguments(
        command=namespace.command,
        config_path=namespace.config,
        is_yes=namespace.yes,
        resume_session_id=namespace.resume,
        model=namespace.model,
        is_plain=namespace.plain,
    )


def _configure_logging(environment: CommandEnvironment, harness: AgentHarness) -> Path | None:
    """Send the ``logging`` records to the log file.

    Returns:
        The log file, or ``None`` when it cannot be written; logging is then off.
    """
    log_path = environment.agents_directory / f"{harness.name}.log"
    root = logging.getLogger()
    root.setLevel(logging.INFO)
    try:
        environment.agents_directory.mkdir(parents=True, exist_ok=True)
        handler: logging.Handler = logging.FileHandler(log_path, encoding="utf-8")
    except OSError as error:
        print(f"Logging is off: cannot write {log_path}: {error}", file=sys.stderr)
        # Without a handler, logging's last resort would print every warning to the terminal.
        root.handlers[:] = [logging.NullHandler()]
        return None
    handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(name)s: %(message)s"))
    handler.addFilter(DexRefusalDetailsFilter())
    root.handlers[:] = [handler]
    return log_path


def _is_interactive() -> bool:
    return sys.stdin.isatty() and sys.stdout.isatty()


def _init(arguments: _Arguments, environment: CommandEnvironment, harness: AgentHarness) -> int:
    path = arguments.config_path or default_config_path(environment, harness)
    if arguments.is_yes or not _is_interactive():
        values = default_config_values(harness)
    else:
        values = ask_config_values(harness, _ask)
    write_config_file(path, values, harness)
    print(f"Wrote {path}.")
    return 0


def _load_config(
    arguments: _Arguments, environment: CommandEnvironment, harness: AgentHarness
) -> LoadedConfig:
    """Load the file, or create it first as ``H`` in the plan describes."""
    path = arguments.config_path or default_config_path(environment, harness)
    if path.exists():
        return load_config_file(path, harness)
    if arguments.config_path is not None:
        raise CommandError(f"{path} does not exist")
    if arguments.is_yes:
        loaded = write_config_file(path, default_config_values(harness), harness)
        print(f"Wrote the default configuration to {path}.")
        return loaded
    if _is_interactive():
        print(f"No configuration at {path}; answer a few questions to create it.")
        loaded = write_config_file(path, ask_config_values(harness, _ask), harness)
        print(f"Wrote {path}.")
        return loaded
    print(
        f"No configuration at {path}; using the defaults. "
        f"Run `{command_name(harness)} init` to create one."
    )
    return defaults_without_file(harness, environment.home)


def _ask(question: str, default: str) -> str:
    shown = f" [{default}]" if default else ""
    try:
        answer = input(f"{question}{shown}: ")
    except KeyboardInterrupt:
        raise _InitCancelled(_INTERRUPTED_EXIT_CODE) from None
    except EOFError:
        raise _InitCancelled(1) from None
    return answer if answer.strip() else default


async def _run(
    arguments: _Arguments,
    loaded: LoadedConfig,
    environment: CommandEnvironment,
    harness: AgentHarness,
    log_path: Path | None,
    server: _CommandDexServer,
) -> int:
    connection = _resolve_connection(loaded, environment)
    inputs = _prepare_run(
        arguments.model, loaded, environment, harness, _report_ignored_settings, connection
    )
    endpoint = await server.resolve(inputs.config)
    if _is_terminal_ui_wanted(arguments, environment, harness):
        outcome = await _run_terminal_ui(
            arguments, environment, harness, loaded, inputs, endpoint, server
        )
        if isinstance(outcome, int):
            return outcome
        if isinstance(outcome, PlainFallback) and outcome.session_id is not None:
            arguments = replace(arguments, resume_session_id=outcome.session_id)
    return await _run_plain_chat(
        arguments, environment, harness, loaded, inputs, endpoint, log_path
    )


def _is_terminal_ui_wanted(
    arguments: _Arguments, environment: CommandEnvironment, harness: AgentHarness
) -> bool:
    """Report whether ``run`` tries the terminal UI; Windows is ruled out before any probe."""
    return (
        sys.platform != "win32"
        and not arguments.is_plain
        and _is_interactive()
        and environment.terminal_type not in (None, "dumb")
        and getattr(harness, "terminal_interface", TerminalInterface.DEXCLI)
        is not TerminalInterface.PLAIN
    )


async def _run_terminal_ui(
    arguments: _Arguments,
    environment: CommandEnvironment,
    harness: AgentHarness,
    loaded: LoadedConfig,
    inputs: _RunInputs,
    endpoint: DexServerEndpoint,
    server: _CommandDexServer,
) -> int | PlainFallback | None:
    """Chat in the terminal UI; ``None`` when ``dexcli`` cannot show it, after saying why."""
    run_directory = environment.agents_directory / "run"
    await asyncio.to_thread(stop_leftover_processes, run_directory, print)
    try:
        dexcli = locate_dexcli(environment)
        if dexcli is None:
            print(f"dexcli is not installed; downloading dexcli {environment.dexcli_version}.")
            dexcli = await asyncio.to_thread(download_dexcli, environment)
    except DexcliVariableError as error:
        raise CommandFailure(str(error)) from error
    except DexcliInstallError as error:
        print(f"Plain chat: {error}")
        return None
    described = await describe_terminal_ui(dexcli, environment.terminal_variables)
    if described.contract is None:
        print(described.plain_line)
        return None
    log_path = environment.agents_directory / f"{harness.name}.log"
    launch = TerminalUiLaunch(
        command_name=command_name(harness),
        harness_name=harness.name,
        version=importlib.metadata.version("dex-agents-cli-core"),
        dexcli=dexcli,
        contract=described.contract,
        worker_host_argv=worker_host_argv(),
        start=WorkerHostStart(
            config_path=None if loaded.path is None else str(loaded.path),
            model=arguments.model,
            resume_session_id=arguments.resume_session_id,
            server_address=endpoint.address,
            is_tls=endpoint.is_tls,
            web_url=endpoint.web_url,
            connectors_mcp_url=endpoint.connectors_mcp_url,
            dex_connection=inputs.connection.to_json(),
        ),
        terminal_variables={
            **environment.terminal_variables,
            **inputs.connection.terminal_ui_auth_variables(environment.dex_connection_variables),
        },
        display={
            "model": inputs.config.model,
            "workspace": _path_shown(inputs.workspace, environment.home),
            "config_path": None
            if loaded.path is None
            else _path_shown(loaded.path, environment.home),
            "models": _offered_models(inputs.config.model, harness),
        },
        log_path=log_path,
        log_path_shown=_path_shown(log_path, environment.home),
        run_directory=run_directory,
        is_browser_opening_enabled=environment.is_browser_opening_enabled,
        is_resumed=arguments.resume_session_id is not None,
        startup_notices=tuple(server.started_dexcli_dev_lines),
        worker_host_variables=inputs.connection.terminal_ui_auth_variables(
            environment.dex_connection_variables
        ),
        is_waiting_line_animated=is_waiting_line_animated(sys.stderr, environment.terminal_type),
    )
    return await run_terminal_ui_chat(
        launch, print, restore_signal_handlers=server.install_termination_handlers
    )


def _offered_models(configured_model: str, harness: AgentHarness) -> list[str]:
    """Return the ``/model`` choices: the configured model, the default, then the suggestions."""
    candidates = [configured_model, harness.default_model, *declared_suggested_models(harness)]
    return list(dict.fromkeys(model for model in candidates if model is not None))


def _path_shown(path: Path, home: Path) -> str:
    """Return a path for display, with the home directory written as ``~``."""
    with contextlib.suppress(ValueError):
        return str(Path("~") / path.relative_to(home))
    return str(path)


@dataclass(frozen=True)
class _RunInputs:
    """What a chat run reads before it builds the Worker."""

    config: EffectiveConfig
    media_store: MediaStore
    workspace: Path
    resolver: TurnConfigResolver
    connection: DexConnectionSettings
    mcp_servers: tuple[McpStreamableHttpServerConfig, ...] = ()
    mcp_server_secret_texts: frozenset[str] = frozenset()


def _resolve_connection(
    loaded: LoadedConfig, environment: CommandEnvironment
) -> DexConnectionSettings:
    """Resolve ``dex.auth`` and ``dex.tls`` with the environment; a missing setting exits 2."""
    try:
        return resolve_dex_connection_settings(
            loaded.config,
            environment.dex_connection_variables,
            config_directory=loaded.directory,
            home=environment.home,
            working_directory=Path.cwd(),
        )
    except DexConnectionSettingsError as error:
        raise CommandError(error.message) from error


def _open_connection(
    settings: DexConnectionSettings, environment: CommandEnvironment
) -> DexConnection:
    try:
        return DexConnection.open(settings, environment.dex_connection_variables)
    except DexConnectionSettingsError as error:
        raise CommandError(error.message) from error


def _describe_dex_failure(
    failure: BaseException, connection: DexConnection, server: str, address: str
) -> str | None:
    """Return the line for a rejected credential, else for an unavailable server, else ``None``."""
    return describe_rejected_dex_credential(
        failure, connection.settings, address
    ) or describe_unavailable_dex_server(failure, server, address)


def _prepare_run(
    model: str | None,
    loaded: LoadedConfig,
    environment: CommandEnvironment,
    harness: AgentHarness,
    report_ignored_settings: Callable[[Sequence[str]], None],
    connection: DexConnectionSettings,
) -> _RunInputs:
    config = loaded.config if model is None else replace(loaded.config, model=model)
    media_store = _build_media_store(config, environment)
    prompt_text = _read_system_prompt_file(config, loaded, environment)
    workspace = environment.expand_path(config.workspace, loaded.directory)
    workspace.mkdir(parents=True, exist_ok=True)
    resolver = TurnConfigResolver(
        config,
        config.system_prompt if config.system_prompt is not None else prompt_text,
        report_ignored_settings,
        supported_reasoning_efforts=declared_reasoning_efforts(harness),
    )
    try:
        mcp_servers = resolve_mcp_servers(config, os.environ)
        secret_texts = mcp_server_secret_texts(config, os.environ)
    except McpServerSettingsError as error:
        raise CommandError(error.message) from error
    return _RunInputs(
        config, media_store, workspace, resolver, connection, mcp_servers, secret_texts
    )


async def _run_plain_chat(
    arguments: _Arguments,
    environment: CommandEnvironment,
    harness: AgentHarness,
    loaded: LoadedConfig,
    inputs: _RunInputs,
    endpoint: DexServerEndpoint,
    log_path: Path | None,
) -> int:
    session_worker = await _SessionWorker.start(
        environment,
        harness,
        loaded,
        inputs,
        endpoint,
        reports=_SessionWorkerReports(
            report=print,
            report_runtime=_report_on_standard_error,
            open_connectors_page=_open_connectors_page_if_interactive(environment),
            log_path=log_path,
        ),
        holder=SessionLeaseHolder.for_this_process(environment.session_lease_ttl_seconds),
    )
    terminal = Terminal()
    lease = HeldSessionLease(session_worker.opener)
    try:
        session = await session_worker.open_session(arguments.resume_session_id)
        session_id = session.session_id
        await _wait_for_session_lease(session, lease, environment)
        for line in await session_worker.opener.complete_takeover(session_id):
            print(line, file=sys.stderr)
        # The pull permit runs from the acquire on; it may end the lease during the takeover.
        if lease.is_ending:
            await lease.wait_until_ended()
            _report_ended_lease(lease)
            return 1
        print(f"Session {session_id}")
        if endpoint.web_url is not None:
            print(f"Dex Web: {endpoint.web_url}/v1/flows/{session_id}?view=session")
        terminal.start()
        chat = TerminalChat(
            session_worker.client,
            session.sessions,
            session_id,
            terminal,
            command_name(harness),
            media_store=inputs.media_store,
            home=environment.home,
        )
        is_failed = not await _chat_while_lease_held(chat, lease)
    finally:
        with contextlib.suppress(RuntimeError):
            terminal.stop()
        # Before the Worker stops: it may be the only one left to serve the release.
        await lease.release()
        await session_worker.close()
    return 1 if is_failed else 0


async def _wait_for_session_lease(
    session: OpenableSession, lease: HeldSessionLease, environment: CommandEnvironment
) -> None:
    """Wait until this command holds the session's lease, showing who has it meanwhile.

    Raises:
        CommandFailure: If the session Worker could not start pulling the session's task queue.
    """
    try:
        await wait_for_session_lease(
            session.session_id,
            lambda: lease.try_acquire(session),
            sys.stderr,
            is_animated=is_waiting_line_animated(sys.stderr, environment.terminal_type),
            columns=lambda: terminal_columns(sys.stderr),
        )
    except SessionWorkerStartError as error:
        raise CommandFailure(str(error)) from error


async def _chat_while_lease_held(chat: TerminalChat, lease: HeldSessionLease) -> bool:
    """Run the chat until it ends, another terminal takes the session, or its pull permit expires.

    Returns:
        The chat's result, or ``False`` when the lease was lost or unconfirmed.
    """
    chatting = asyncio.create_task(chat.run())
    try:
        waited: set[asyncio.Future[Any]] = {chatting, lease.ending}
        await asyncio.wait(waited, return_when=asyncio.FIRST_COMPLETED)
        # A chat whose lease ended may fail at the same time; the lease explains it.
        if not lease.is_ending:
            return chatting.result()
    finally:
        chatting.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await _end_cancelled_chat(chatting)
    # The newline ends the prompt line, which is on standard output.
    print(flush=True)
    await lease.wait_until_ended()
    _report_ended_lease(lease)
    return False


def _report_ended_lease(lease: HeldSessionLease) -> None:
    """Print the line of a lease that another terminal took or no renewal confirmed."""
    if lease.lost.done():
        session_id, holder = lease.lost.result()
        print(build_lease_lost_line(session_id, format_lease_holder(holder)), file=sys.stderr)
    else:
        print(build_lease_unconfirmed_line(lease.unconfirmed.result()), file=sys.stderr)


async def _end_cancelled_chat(chatting: asyncio.Task[bool]) -> None:
    """Wait for a chat the lease loss cancelled; a Dex call it had running may fail instead."""
    try:
        await chatting
    except DexServiceError as error:
        _LOGGER.warning("the stopped chat's last Dex call failed", exc_info=error)


@dataclass(frozen=True)
class _SessionWorkerReports:
    """Where the Worker half of a run reports to the user.

    Attributes:
        report: Prints one startup line, such as a connectors warning.
        report_runtime: Prints one line while the chat runs.
        open_connectors_page: Opens the Dex Web Connectors page, or does nothing.
        log_path: The log file a line about an error points to, or ``None``.
    """

    report: Callable[[str], None]
    report_runtime: Callable[[str], None]
    open_connectors_page: Callable[[str], None]
    log_path: Path | None


class _SessionWorker:
    """The Worker half of a chat run: tools, the session Flows, the Client, and their Workers.

    A new session has the harness's Flow type and its own task queue. The
    session Worker pulls that queue only once ``SessionOpener.attempt`` finds
    the session free. The other Worker always pulls the default task queue for
    this execution environment's environment-hashed sessions from before 0.6.8.
    """

    def __init__(
        self,
        harness: AgentHarness,
        flow: AgentSessionFlow,
        identity: SessionEnvironmentIdentity,
        client: AsyncClient,
        worker: AsyncWorker,
        session_queue_worker: SessionQueueWorker,
        opener: SessionOpener,
        blob_cache: BlobCache,
        local_tools: LocalToolSource | None,
        connection: DexConnection,
        address: str,
    ) -> None:
        self.harness = harness
        self.flow = flow
        self.identity = identity
        self.client = client
        self.opener = opener
        self.sessions = opener.sessions
        self._worker = worker
        self._session_queue_worker = session_queue_worker
        self._blob_cache = blob_cache
        self._local_tools = local_tools
        self._connection = connection
        self._address = address
        self._serving = asyncio.create_task(worker.start())

    @classmethod
    async def start(
        cls,
        environment: CommandEnvironment,
        harness: AgentHarness,
        loaded: LoadedConfig,
        inputs: _RunInputs,
        endpoint: DexServerEndpoint,
        *,
        reports: _SessionWorkerReports,
        holder: SessionLeaseHolder,
        process_groups: ShellProcessGroups | None = None,
    ) -> _SessionWorker:
        """Build both session Flows, start the default task queue's Worker, and upload the graphs.

        Args:
            environment: The command's environment.
            harness: The command's harness.
            loaded: The configuration file.
            inputs: What the run read before it builds the Worker.
            endpoint: The Dex server.
            reports: Where the Worker half reports to the user.
            holder: This process's lease holder.
            process_groups: The Worker host's shell process groups, or ``None``.
        """
        config = inputs.config
        connectors = await _connect_connectors(
            config,
            endpoint,
            inputs.resolver,
            harness,
            inputs.media_store,
            reports.log_path,
            reports=reports,
        )
        local_tools = _local_tool_source(
            config, inputs.workspace, harness, inputs.resolver, process_groups
        )
        mcp_servers = await _connect_mcp_servers(inputs, reports)
        tool_sources: list[ToolSource] = [
            source for source in (connectors, mcp_servers, local_tools) if source is not None
        ]
        identity = current_session_environment_identity(loaded.path, inputs.workspace)
        legacy_flow_type = namespace_session_flow_type(harness.flow_type, identity)

        def build_session_flow(flow_type: str) -> AgentSessionFlow:
            context = HarnessContext(
                flow_type=flow_type,
                config=config,
                workspace=inputs.workspace.resolve(),
                instructions=inputs.resolver.file_instructions,
                harness_options=config.harness_options,
                tool_sources=tuple(tool_sources),
                resolver=inputs.resolver,
                media_store=inputs.media_store,
                is_session_summary_indexed=is_session_summary_indexed(config),
            )
            harness_flow = harness.create_session_flow(context)
            _require_session_summary_setting(harness_flow, context.is_session_summary_indexed)
            flow = with_turn_config(harness_flow, inputs.resolver)
            _require_session_summary_setting(flow, context.is_session_summary_indexed)
            if flow.get_flow_type() != flow_type:
                raise CommandError(
                    f"the harness built Flow type {flow.get_flow_type()}, not {flow_type}"
                )
            return flow

        turn_refusal = TakenOverTurnRefusal()
        flow = turn_refusal.wrap_flow(build_session_flow(harness.flow_type))
        legacy_flow = build_session_flow(legacy_flow_type)
        cache_directory = environment.agents_directory / "cache" / harness.name
        cache_directory.mkdir(parents=True, exist_ok=True)
        blob_cache = open_blob_cache(BlobCacheConfig(str(cache_directory), _BLOB_CACHE_BYTES))
        connection = _open_connection(inputs.connection, environment)
        try:
            client = AsyncClient(
                Registry([flow, legacy_flow], allow_async_handlers=True),
                blob_cache,
                connection.client_options(endpoint.address),
            )
        except ValueError as error:
            connection.close()
            blob_cache.close()
            raise CommandError(connection.describe_setup_error(error)) from error
        if connection.settings.mode != "none":
            try:
                await _check_dex_credential(client, connection, config.dex.server, endpoint.address)
            except BaseException:
                await client.close()
                connection.close()
                blob_cache.close()
                raise
        host_name = socket.gethostname()

        def create_worker(
            session_flow: AgentSessionFlow, app_worker_id: str, task_queue: str = ""
        ) -> AsyncWorker:
            pull = PullWorkerOptions(
                stop_drain_timeout=_WORKER_STOP_DRAIN,
                app_worker_id=app_worker_id,
                task_queue=task_queue,
            )
            return AsyncWorker(
                Registry([session_flow], allow_async_handlers=True),
                blob_cache,
                connection.worker_options(endpoint.address, pull),
            )

        def describe_start_failure(failure: BaseException) -> str:
            if is_named_task_queue_protocol_error(failure):
                return named_task_queue_server_error(command_name(harness))
            described = _describe_dex_failure(
                failure, connection, config.dex.server, endpoint.address
            )
            return described or f"the session Worker did not start: {failure}"

        worker = create_worker(legacy_flow, cli_app_worker_id(harness.name, host_name, os.getpid()))
        session_queue_worker = SessionQueueWorker(
            lambda task_queue, app_worker_id: create_worker(flow, app_worker_id, task_queue),
            lambda incarnation: cli_app_worker_id(
                harness.name, host_name, os.getpid(), str(incarnation)
            ),
            describe_start_failure,
        )
        opener = SessionOpener(
            client=client,
            harness_flow_type=harness.flow_type,
            session_flow=flow,
            legacy_session_flow=legacy_flow,
            command_name=command_name(harness),
            holder=holder,
            environment=session_start_environment(identity),
            session_worker=session_queue_worker,
            turn_refusal=turn_refusal,
        )
        session_worker = cls(
            harness,
            flow,
            identity,
            client,
            worker,
            session_queue_worker,
            opener,
            blob_cache,
            local_tools,
            connection,
            endpoint.address,
        )
        try:
            await worker.wait_pull_registered(timeout=_WORKER_REGISTRATION_TIMEOUT_SECONDS)
            await _upload_session_flow_definitions(client, [harness.flow_type, legacy_flow_type])
        except BaseException as failure:
            await session_worker.close(startup_failure=failure)
            described = _describe_dex_failure(
                failure, connection, config.dex.server, endpoint.address
            )
            if described is None:
                raise
            raise DexServerError(described) from failure
        return session_worker

    async def open_session(self, resume_session_id: str | None) -> OpenableSession:
        """Check and return the resumed session, or start a new one and return it.

        A new session has the harness's Flow type and its own task queue; no
        Worker pulls that queue until its lease is taken.

        Raises:
            CommandError: If the session to resume does not exist, does not
                run, or belongs to another harness or to another execution
                environment's environment-hashed Flow type.
            DexServerError: If the server refused the credential or has no
                named task queues.
        """
        try:
            if resume_session_id is None:
                session_id = (
                    f"{self.harness.name}-{datetime.now(UTC):%Y%m%d-%H%M%S}-{secrets.token_hex(3)}"
                )
                await self.sessions.start_session(
                    session_id,
                    options=StartFlowOptions(task_queue=session_task_queue(session_id)),
                    start_environment=session_start_environment(self.identity),
                )
                return self.opener.started_session(session_id)
            return await self.opener.describe_openable_session(resume_session_id)
        except UnopenableSessionError as error:
            raise CommandError(str(error)) from error
        except (CommandError, asyncio.CancelledError):
            raise
        except Exception as failure:
            if is_named_task_queue_protocol_error(failure):
                raise DexServerError(
                    named_task_queue_server_error(command_name(self.harness))
                ) from failure
            rejected = describe_rejected_dex_credential(
                failure, self._connection.settings, self._address
            )
            if rejected is None:
                raise
            raise DexServerError(rejected) from failure

    async def close(self, *, startup_failure: BaseException | None = None) -> None:
        """Stop both Workers, close the browser pages, the Client, and the blob cache.

        Args:
            startup_failure: The error that ended ``start``. The Worker's own
                task raises the same error again, and it is not raised twice.
        """
        await asyncio.gather(self._worker.stop(), self._session_queue_worker.close())
        try:
            await self._serving
        except asyncio.CancelledError:
            pass
        except Exception as error:
            if error is not startup_failure:
                raise
        if self._local_tools is not None:
            await self._local_tools.browser.close()
        await self.client.close()
        self._connection.close()
        self._blob_cache.close()


def _describe_dex_service_error(error: DexServiceError) -> str:
    """Return one line for a Dex service failure that nothing else explains."""
    target = f" for session {error.flow_id}" if error.flow_id else ""
    return f"Dex {error.operation}{target} failed: {error.detail or error.code.name}"


def _report_on_standard_error(line: str) -> None:
    print(line, file=sys.stderr)


def _report_ignored_settings(warnings: Sequence[str]) -> None:
    print(f"Session settings keys ignored: {', '.join(warnings)}", file=sys.stderr)


def _build_media_store(config: EffectiveConfig, environment: CommandEnvironment) -> MediaStore:
    """Build the store of ``media.store``: ``~/.dex/agents/media`` for auto, else the S3 bucket."""
    media = config.media
    try:
        if media.store == AUTO:
            return LocalDirectoryMediaStore(
                (environment.agents_directory / MEDIA_DIRECTORY).absolute()
            )
        try:
            # Imported only for an s3 store, so the default store needs no boto3.
            s3 = importlib.import_module("dex_agents.s3")
        except ImportError as error:
            raise CommandError(
                f"media.store {media.store} needs the S3 support of dex-agents; install it "
                "into this command's environment with: pip install 'dex-agents[s3]'"
            ) from error
        store: MediaStore = s3.S3MediaStore.from_store_uri(
            media.store,
            endpoint_url=media.endpoint,
            force_path_style=media.endpoint is not None,
        )
        return store
    except ValueError as error:
        raise CommandError(f"media.store {media.store}: {error}") from error


def _read_system_prompt_file(
    config: EffectiveConfig, loaded: LoadedConfig, environment: CommandEnvironment
) -> str | None:
    if config.system_prompt_file is None:
        return None
    path = environment.expand_path(config.system_prompt_file, loaded.directory)
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as error:
        raise CommandError(f"cannot read system_prompt_file {path}: {error}") from error


def _connectors_mcp_url(config: EffectiveConfig, endpoint: DexServerEndpoint) -> str | None:
    if config.connectors.mcp != AUTO:
        return config.connectors.mcp
    return endpoint.connectors_mcp_url


async def _connect_connectors(
    config: EffectiveConfig,
    endpoint: DexServerEndpoint,
    resolver: TurnConfigResolver,
    harness: AgentHarness,
    media_store: MediaStore,
    log_path: Path | None,
    *,
    reports: _SessionWorkerReports,
) -> ConnectorsMcpToolSource | None:
    """Check the connectors MCP server and return its source, or ``None`` for no connector tools.

    An unavailable ``auto`` server only warns: the source lists it again on
    every turn, so a server that is still starting comes alive. A harness
    whose tool set is fixed at build time gets no connector tools in this run
    then, and the warning says to restart it. An explicit URL that does not
    answer ``initialize`` fails the command with exit code 1; one that answers
    but cannot be listed only warns, as ``auto`` does. Each availability
    change, the startup one included, prints exactly one line.
    """
    if config.connectors.enabled == ():
        return None
    url = _connectors_mcp_url(config, endpoint)
    if url is None:
        reports.report(
            "Connector tools are off: connectors.mcp is auto, which needs dex.server: auto. "
            "Set connectors.mcp to the MCP server's URL."
        )
        return None
    hint = _connectors_unavailable_hint(log_path, endpoint.web_url)
    if _is_tool_set_fixed_at_build(harness):
        restart = _restart_for_connector_tools(harness)

        def describe_unavailable(error: ConnectorsMcpUnavailableError) -> str:
            if isinstance(error, ConnectorsMcpAccessDeniedError):
                return f"{connectors_access_denied_sentence(error)} {hint} {restart}"
            return f"The connectors MCP server is unavailable. {hint} {restart}"

    else:

        def describe_unavailable(error: ConnectorsMcpUnavailableError) -> str:
            return connectors_unavailable_line(error, hint)

    try:
        catalog = await read_connector_catalog(url)
    except ConnectorsMcpUnavailableError as error:
        # An explicit server that answers initialize stays configured; each turn lists it again.
        if config.connectors.mcp != AUTO and not isinstance(error, ConnectorsMcpListingError):
            raise CommandFailure(f"The connectors MCP server is unavailable: {error}") from error
        _LOGGER.warning("connectors MCP server %s could not be listed", url, exc_info=error)
        reports.report(describe_unavailable(error))
        return ConnectorsMcpToolSource(
            url,
            resolver,
            report=reports.report_runtime,
            media_store=media_store,
            describe_unavailable=describe_unavailable,
            startup_error=error,
        )
    _report_unauthorized(config, catalog, endpoint.web_url, reports)
    source = ConnectorsMcpToolSource(
        url,
        resolver,
        report=reports.report_runtime,
        media_store=media_store,
        describe_unavailable=describe_unavailable,
    )
    # A failed listing prints the one line through describe_unavailable.
    await source.list_file_enabled_tools()
    return source


async def _connect_mcp_servers(
    inputs: _RunInputs, reports: _SessionWorkerReports
) -> McpServersToolSource:
    """Return the remote MCP server source, listed once so fixed tool sets get its tools.

    It is returned even without servers, so it refuses stored calls of servers
    another Worker has.
    """
    log_path = reports.log_path
    unavailable_line = "MCP server tools are unavailable for now; each turn tries again."
    if log_path is not None:
        unavailable_line = f"{unavailable_line} See {log_path} for the error."
    source = McpServersToolSource(
        inputs.mcp_servers,
        inputs.resolver,
        report=reports.report_runtime,
        report_at_startup=reports.report,
        unavailable_line=unavailable_line,
        media_store=inputs.media_store,
        secret_texts=inputs.mcp_server_secret_texts,
    )
    if inputs.mcp_servers:
        # Every record of this process may quote a server's error, which may echo a header.
        for handler in logging.getLogger().handlers:
            handler.addFilter(source.secret_redacting_log_filter)
        await source.list_file_enabled_tools()
    return source


def _connectors_unavailable_hint(log_path: Path | None, web_url: str | None) -> str:
    """Say where the error and the connections are, for a line about an unavailable server."""
    page = f"{web_url}{CONNECTORS_WEB_PATH}" if web_url else "the Dex Web Connectors page"
    if log_path is None:
        return f"See {page} for connections."
    return f"See {log_path} for the error and {page} for connections."


def _is_tool_set_fixed_at_build(harness: AgentHarness) -> bool:
    return getattr(harness, "is_tool_set_fixed_at_build", False) is True


def _restart_for_connector_tools(harness: AgentHarness) -> str:
    return (
        f"{command_name(harness)} fixes its tools when it starts, so this run has no connector "
        "tools. Restart it once the connectors MCP server answers."
    )


def _report_unauthorized(
    config: EffectiveConfig,
    catalog: Sequence[ConnectorCatalogTool],
    web_url: str | None,
    reports: _SessionWorkerReports,
) -> None:
    missing = [
        entry
        for entry in config.connectors.enabled or ()
        if not any(_entry_has_tool(entry, tool) for tool in catalog)
    ]
    if not missing:
        return
    page = f"{web_url}{CONNECTORS_WEB_PATH}" if web_url else "the Dex Web Connectors page"
    names = ", ".join(_entry_text(entry) for entry in missing)
    reports.report(f"No authorized connection for {names}. Authorize it on {page}.")
    if web_url is not None:
        reports.open_connectors_page(f"{web_url}{CONNECTORS_WEB_PATH}")


def _open_connectors_page_if_interactive(
    environment: CommandEnvironment,
) -> Callable[[str], None]:
    def open_page(url: str) -> None:
        if environment.is_browser_opening_enabled and _is_interactive():
            webbrowser.open(url)

    return open_page


def _entry_has_tool(entry: ConnectorEntry, tool: ConnectorCatalogTool) -> bool:
    identity = tool.identity
    return identity is not None and entry.covers_connection(identity.connector, identity.connection)


def _entry_text(entry: ConnectorEntry) -> str:
    return entry.connector if entry.connection is None else f"{entry.connector}/{entry.connection}"


def _local_tool_source(
    config: EffectiveConfig,
    workspace: Path,
    harness: AgentHarness,
    resolver: TurnConfigResolver,
    process_groups: ShellProcessGroups | None = None,
) -> LocalToolSource | None:
    switches = {
        LocalToolGroup.FILES: config.local_tools.files,
        LocalToolGroup.SHELL: config.local_tools.shell.enabled,
        LocalToolGroup.WEB_FETCH: config.local_tools.web_fetch,
        LocalToolGroup.BROWSER: config.local_tools.browser,
    }
    groups = frozenset(
        group
        for group, is_enabled in switches.items()
        if is_enabled and group not in harness.replaced_local_tools
    )
    if not groups:
        return None
    return LocalToolSource(workspace, groups, resolver, process_groups=process_groups)


async def _check_dex_credential(
    client: AsyncClient, connection: DexConnection, server: str, address: str
) -> None:
    """Make one Client call, so a rejected credential fails before the Worker retries it."""
    try:
        await client.describe_flow(_CREDENTIAL_PROBE_FLOW_ID)
    except FlowNotFoundError:
        return
    except Exception as failure:
        described = _describe_dex_failure(failure, connection, server, address)
        if described is None:
            raise
        raise DexServerError(described) from failure


async def _upload_session_flow_definitions(client: AsyncClient, flow_types: Sequence[str]) -> None:
    """Upload the session graphs so Dex Web shows the sessions without ``--flow-rendering-dir``.

    The Client uploads them after the Worker registered, not
    ``WorkerOptions.flow_definitions``, because the Worker's upload runs inside
    ``start`` with a two-minute deadline. The outcome and any failure go to the
    log file only.

    Args:
        client: The command's Client.
        flow_types: The harness's Flow type and this execution environment's
            environment-hashed one.
    """
    documents = [
        session_flow_definition_document(flow_type, SESSION_ACTION_PERMISSION)
        for flow_type in flow_types
    ]
    try:
        results = await asyncio.wait_for(
            client.upload_flow_definitions(documents),
            _SESSION_FLOW_DEFINITION_UPLOAD_TIMEOUT_SECONDS,
        )
    except Exception as error:  # Any failure only loses the Dex Web graph of these sessions.
        for flow_type in flow_types:
            _LOGGER.warning(
                "session Flow definition upload failed; Dex Web shows this session only with "
                "--flow-rendering-dir: flowType=%s %s",
                flow_type,
                _describe_upload_error(error),
            )
        return
    for flow_type, result in zip(flow_types, results, strict=False):
        _LOGGER.info(
            "session Flow definition %s: flowType=%s storedDigest=%s",
            result.outcome.value,
            flow_type,
            result.stored_digest,
        )


def _describe_upload_error(error: BaseException) -> str:
    """Return the error's type and text, or its type alone; a Dex refusal leaves out its details."""
    refused = describe_dex_refusal_without_details(error)
    if refused is not None:
        return f"{type(error).__name__}: {refused}"
    text = str(error)
    return f"{type(error).__name__}: {text}" if text else type(error).__name__


async def _list_sessions(
    loaded: LoadedConfig,
    environment: CommandEnvironment,
    harness: AgentHarness,
    server: _CommandDexServer,
) -> int:
    settings = _resolve_connection(loaded, environment)
    endpoint = await server.resolve(loaded.config)
    workspace = environment.expand_path(loaded.config.workspace, loaded.directory)
    identity = current_session_environment_identity(loaded.path, workspace)
    legacy_flow_type = namespace_session_flow_type(harness.flow_type, identity)
    blob_cache = open_blob_cache(
        BlobCacheConfig(str(environment.agents_directory / "cache" / "sessions"), _BLOB_CACHE_BYTES)
    )
    try:
        connection = _open_connection(settings, environment)
    except CommandError:
        blob_cache.close()
        raise
    environment_reading = _SessionEnvironmentReadingFlow()
    try:
        client = AsyncClient(
            Registry([environment_reading], allow_async_handlers=True),
            blob_cache,
            connection.client_options(endpoint.address),
        )
    except ValueError as error:
        connection.close()
        blob_cache.close()
        raise CommandError(connection.describe_setup_error(error)) from error

    def pages_of(flow_type: str) -> Callable[[str], Awaitable[AgentSessionSummaryPage]]:
        def load_page(next_page_token: str) -> Awaitable[AgentSessionSummaryPage]:
            return list_agent_sessions(client, flow_type, next_page_token=next_page_token)

        return load_page

    try:
        current, legacy = await asyncio.gather(
            collect_sessions_for_list(pages_of(harness.flow_type)),
            collect_sessions_for_list(pages_of(legacy_flow_type)),
        )
        environments = await environment_reading.read_all(client, current.sessions)
    except Exception as failure:
        described = _describe_dex_failure(
            failure, connection, loaded.config.dex.server, endpoint.address
        )
        if described is None:
            raise
        raise DexServerError(described) from failure
    finally:
        await client.close()
        connection.close()
        blob_cache.close()
    sessions = [*current.sessions, *legacy.sessions]
    has_more_sessions = current.has_more_sessions or legacy.has_more_sessions
    if not sessions and not has_more_sessions:
        print("No sessions yet.")
        return 0
    # An environment-hashed session runs only in this execution environment, so it was open here.
    this_environment = describe_session_environment(session_start_environment(identity))
    legacy_session_ids = {session.session_id for session in legacy.sessions}
    for session in sort_agent_sessions_by_last_activity(sessions):
        where = (
            this_environment
            if session.session_id in legacy_session_ids
            else describe_session_environment(environments.get(session.session_id))
        )
        print(_session_line(session, where))
    if has_more_sessions:
        print(
            f"More sessions exist; this list sorts the first {len(sessions)} that Dex returns: "
            "running sessions first, then the most recently closed and started."
        )
    print(f"Resume one with: {command_name(harness)} --resume <session-id>")
    return 0


class _SessionEnvironmentReadingFlow(Flow[None]):
    """A Flow that only declares ``SessionLastTerminalEnvironment``.

    The ``sessions`` subcommand's Client may read only registered definitions,
    and this Flow lets it do so without building the harness's session Flow,
    which can need a provider API key. It has no Steps and never runs.
    """

    def get_flow_type(self) -> str:
        return "DexAgentsCliSessionEnvironmentReading"

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(SESSION_LAST_TERMINAL_ENVIRONMENT)

    async def read_all(
        self, client: AsyncClient, sessions: Sequence[AgentSessionSummary]
    ) -> dict[str, dict[str, str]]:
        """Return where each session was last open; a failed read or none leaves it out.

        Args:
            client: A Client whose Registry has this Flow.
            sessions: The sessions.
        """
        environments: dict[str, dict[str, str]] = {}
        limit = asyncio.Semaphore(_ENVIRONMENT_READ_CONCURRENCY)

        async def read(session_id: str) -> None:
            async with limit:
                try:
                    stored = await client.get_attributes(
                        session_id, SESSION_LAST_TERMINAL_ENVIRONMENT
                    )
                except DexServiceError as error:
                    # The row then shows no environment; the list itself still prints.
                    _LOGGER.warning("cannot read where session %s was open: %s", session_id, error)
                    return
            found = stored.get(SESSION_LAST_TERMINAL_ENVIRONMENT)
            if found is not None:
                environments[session_id] = dict(found)

        await asyncio.gather(*(read(session.session_id) for session in sessions))
        return environments


async def _list_connectors(
    loaded: LoadedConfig,
    environment: CommandEnvironment,
    harness: AgentHarness,
    server: _CommandDexServer,
) -> int:
    config = loaded.config
    # Validated as for the other subcommands, though listing connectors makes no Dex call.
    _resolve_connection(loaded, environment)
    endpoint = await server.resolve(config)
    url = _connectors_mcp_url(config, endpoint)
    if url is None:
        raise CommandError("connectors.mcp is auto, which needs dex.server: auto")
    try:
        catalog = await read_connector_catalog(url)
    except ConnectorsMcpUnavailableError as error:
        print(f"The connectors MCP server is unavailable: {error}")
        return 1
    print(f"Connector tools at {url}:")
    if not catalog:
        print("  none; authorize a connection in Dex Web first")
    for tool in catalog:
        identity = tool.identity
        where = (
            f"{identity.connector}/{identity.connection} {identity.operation or '(no operation)'}"
            if identity
            else "unknown connector"
        )
        is_enabled = config.connectors.enabled is None or (
            identity is not None and identity.is_enabled_by(config.connectors)
        )
        print(f"  {tool.name}  ({where})  {'enabled' if is_enabled else 'not enabled'}")
    if endpoint.web_url is not None:
        await _print_connections(endpoint.web_url)
    return 0


async def _print_connections(web_url: str) -> None:
    try:
        async with httpx2.AsyncClient(timeout=10.0) as http:
            response = await http.get(f"{web_url}/api/v2/connector-connections")
        connections = response.json().get("connections", []) if response.is_success else []
    except (httpx2.HTTPError, ValueError) as error:
        _LOGGER.warning("cannot list the Dex Web connections: %s", error)
        return
    print(f"Connections in Dex Web ({web_url}{CONNECTORS_WEB_PATH}):")
    if not connections:
        print("  none")
    for connection in connections:
        print(
            f"  {connection.get('connectorId')}/{connection.get('connectionName')}  "
            f"{connection.get('status')}"
        )


def _require_session_summary_setting(flow: AgentSessionFlow, is_expected: bool) -> None:
    """Refuse a Flow whose declared indexed session summary differs from ``sessions.summary``."""
    # The persistence schema decides what the Worker registers; the option must agree with it.
    declared = {
        definition.name
        for definition in flow.get_persistence_schema().attributes
        if definition.name in _SESSION_SUMMARY_ATTRIBUTE_NAMES
        and getattr(definition, "index", None) is not None
    }
    expected_names = _SESSION_SUMMARY_ATTRIBUTE_NAMES if is_expected else frozenset()
    if declared == expected_names and flow.options.summary_attributes_indexed == is_expected:
        return
    expected = "keep" if is_expected else "leave out"
    raise CommandError(
        f"sessions.summary says the session Flow must {expected} the indexed session summary, "
        f"but the harness built it declaring {len(declared)} of its 3 indexed Attributes with "
        f"summary_attributes_indexed={flow.options.summary_attributes_indexed}; the harness "
        "must pass HarnessContext.is_session_summary_indexed"
    )


def _session_line(session: AgentSessionSummary, where: str) -> str:
    """Return one ``sessions`` line; an earlier release's session lacks some parts.

    Args:
        session: The session.
        where: Where the session was last open, ``<host>:<workspace>``, or
            ``—`` when unknown, which leaves that part out.
    """
    line = (
        f"{session.session_id}  {session.status.name.lower()}  "
        f"started {_local_minute(session.started_at)}"
    )
    if session.last_activity_at is not None:
        line += f"  updated {_local_minute(session.last_activity_at)}"
    if session.turn_count is not None:
        line += f"  {session.turn_count} {'turn' if session.turn_count == 1 else 'turns'}"
    if where != "—":
        line += f"  on {where}"
    if session.title is not None:
        line += f"  {session.title}"
    return line


def _local_minute(moment: datetime) -> str:
    return moment.astimezone().strftime("%Y-%m-%d %H:%M")
