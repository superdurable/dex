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
import importlib
import importlib.metadata
import logging
import secrets
import signal
import sys
import webbrowser
from collections.abc import Callable, Coroutine, Mapping, Sequence
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
    ClientOptions,
    FlowNotFoundError,
    FlowServiceTlsOptions,
    FlowStatus,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)

from dex_agents import (
    AGENT_SESSION_LAST_ACTIVITY_AT,
    AGENT_SESSION_TITLE,
    AGENT_SESSION_TURN_COUNT,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSessionSummary,
    LocalDirectoryMediaStore,
    MediaStore,
    list_agent_sessions,
    session_flow_definition_document,
    sort_agent_sessions_by_last_activity,
)
from dex_agents.builtin_loop import ToolSource
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
from dex_agents_cli_core.environment import CommandEnvironment
from dex_agents_cli_core.harness import (
    SESSION_ACTION_PERMISSION,
    AgentHarness,
    HarnessContext,
    LocalToolGroup,
    SessionEnvironmentIdentity,
    TerminalInterface,
    current_session_environment_identity,
    namespace_session_flow_type,
    session_flow_type,
    session_start_environment,
)
from dex_agents_cli_core.local_tools import LocalToolSource
from dex_agents_cli_core.local_tools.shell import ShellProcessGroups
from dex_agents_cli_core.session_list import collect_sessions_for_list
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
from dex_agents_cli_core.terminal_ui.local_requests import RequestHandler
from dex_agents_cli_core.terminal_ui.process_groups import (
    ToolProcessGroupRegistry,
    stop_leftover_processes,
)
from dex_agents_cli_core.terminal_ui.worker_host import (
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
# How long the command waits for the session graph upload before it logs a failure.
_SESSION_FLOW_DEFINITION_UPLOAD_TIMEOUT_SECONDS = 10.0
_SESSION_START_TIMEOUT_SECONDS = 60.0
_SESSION_START_POLL_SECONDS = 0.1
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
        init writes no file.
    """
    raw_arguments = list(sys.argv[1:] if argv is None else argv)
    if raw_arguments[:1] == [WORKER_HOST_COMMAND]:
        return _run_worker_host(harness, raw_arguments[1:])
    arguments = _parse_arguments(harness, raw_arguments)
    try:
        environment = CommandEnvironment.from_environment()
    except ValueError as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2
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
    except (CommandError, ConfigFileError) as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2
    except (CommandFailure, DexServerError) as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
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

        inputs = _prepare_run(start.model, loaded, environment, report_ignored_settings)
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
            process_groups=process_groups,
        )
        try:
            session_id = await session_worker.open_session(start.resume_session_id)
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
    return _HostedCliRun(session_worker, session_id, hosted)


class _HostedCliRun:
    """The Worker host's started Worker, its session, and the requests it serves."""

    def __init__(
        self, session_worker: _SessionWorker, session_id: str, hosted: HostedChatSession
    ) -> None:
        self._session_worker = session_worker
        self._session_id = session_id
        self._request_handlers: dict[str, RequestHandler] = {
            PUT_IMAGE: hosted.put_image,
            START_SESSION: hosted.start_session,
            LIST_CONNECTORS: hosted.list_connectors,
        }

    @property
    def session_id(self) -> str:
        return self._session_id

    @property
    def flow_type(self) -> str:
        return self._session_worker.flow_type

    @property
    def capabilities(self) -> Mapping[str, Any]:
        return session_flow_capabilities(self._session_worker.flow)

    @property
    def request_handlers(self) -> Mapping[str, RequestHandler]:
        return self._request_handlers

    async def close(self) -> None:
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
    inputs = _prepare_run(arguments.model, loaded, environment, _report_ignored_settings)
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
        ),
        terminal_variables=environment.terminal_variables,
        display={
            "model": inputs.config.model,
            "workspace": _path_shown(inputs.workspace, environment.home),
            "config_path": None
            if loaded.path is None
            else _path_shown(loaded.path, environment.home),
        },
        log_path=log_path,
        log_path_shown=_path_shown(log_path, environment.home),
        run_directory=run_directory,
        is_browser_opening_enabled=environment.is_browser_opening_enabled,
        is_resumed=arguments.resume_session_id is not None,
        startup_notices=tuple(server.started_dexcli_dev_lines),
    )
    return await run_terminal_ui_chat(
        launch, print, restore_signal_handlers=server.install_termination_handlers
    )


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


def _prepare_run(
    model: str | None,
    loaded: LoadedConfig,
    environment: CommandEnvironment,
    report_ignored_settings: Callable[[Sequence[str]], None],
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
    )
    return _RunInputs(config, media_store, workspace, resolver)


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
    )
    terminal = Terminal()
    try:
        session_id = await session_worker.open_session(arguments.resume_session_id)
        print(f"Session {session_id}")
        if endpoint.web_url is not None:
            print(f"Dex Web: {endpoint.web_url}/v1/flows/{session_id}?view=session")
        terminal.start()
        chat = TerminalChat(
            session_worker.client,
            session_worker.sessions,
            session_id,
            terminal,
            command_name(harness),
            media_store=inputs.media_store,
            home=environment.home,
        )
        is_failed = not await chat.run()
    finally:
        with contextlib.suppress(RuntimeError):
            terminal.stop()
        await session_worker.close()
    return 1 if is_failed else 0


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
    """The Worker half of a chat run: tools, the session Flow, the Client, and the Worker."""

    def __init__(
        self,
        harness: AgentHarness,
        flow: AgentSessionFlow,
        identity: SessionEnvironmentIdentity,
        client: AsyncClient,
        worker: AsyncWorker,
        blob_cache: BlobCache,
        local_tools: LocalToolSource | None,
    ) -> None:
        self.harness = harness
        self.flow = flow
        self.identity = identity
        self.client = client
        self.sessions = AgentSessionClient(client, flow)
        self._worker = worker
        self._blob_cache = blob_cache
        self._local_tools = local_tools
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
        process_groups: ShellProcessGroups | None = None,
    ) -> _SessionWorker:
        """Build the session Flow, start the Worker, and upload the session graph."""
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
        tool_sources: list[ToolSource] = [
            source for source in (connectors, local_tools) if source is not None
        ]
        identity = current_session_environment_identity(loaded.path, inputs.workspace)
        flow_type = namespace_session_flow_type(harness.flow_type, identity)
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
        registry = Registry([flow], allow_async_handlers=True)
        cache_directory = environment.agents_directory / "cache" / harness.name
        cache_directory.mkdir(parents=True, exist_ok=True)
        blob_cache = open_blob_cache(BlobCacheConfig(str(cache_directory), _BLOB_CACHE_BYTES))
        tls = FlowServiceTlsOptions() if endpoint.is_tls else None
        client = AsyncClient(
            registry,
            blob_cache,
            ClientOptions(server_address=endpoint.address, flow_service_tls=tls),
        )
        worker = AsyncWorker(
            registry,
            blob_cache,
            WorkerOptions(
                server_address=endpoint.address,
                flow_service_tls=tls,
                pull=PullWorkerOptions(stop_drain_timeout=timedelta(seconds=5)),
            ),
        )
        session_worker = cls(harness, flow, identity, client, worker, blob_cache, local_tools)
        try:
            await worker.wait_pull_registered(timeout=_WORKER_REGISTRATION_TIMEOUT_SECONDS)
            await _upload_session_flow_definition(client, flow_type)
        except BaseException as failure:
            await session_worker.close(startup_failure=failure)
            unavailable = describe_unavailable_dex_server(
                failure, config.dex.server, endpoint.address
            )
            if unavailable is None:
                raise
            raise DexServerError(unavailable) from failure
        return session_worker

    @property
    def flow_type(self) -> str:
        """Return the session Flow type."""
        return self.flow.get_flow_type()

    async def open_session(self, resume_session_id: str | None) -> str:
        """Check and return the resumed session, or start a new one and return its ID."""
        return await _open_session(
            self.client,
            self.sessions,
            resume_session_id,
            self.harness,
            self.flow_type,
            self.identity,
        )

    async def close(self, *, startup_failure: BaseException | None = None) -> None:
        """Stop the Worker, close the browser pages, the Client, and the blob cache.

        Args:
            startup_failure: The error that ended ``start``. The Worker's own
                task raises the same error again, and it is not raised twice.
        """
        await self._worker.stop()
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
        self._blob_cache.close()


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


async def _upload_session_flow_definition(client: AsyncClient, flow_type: str) -> None:
    """Upload the session graph so Dex Web shows the session without ``--flow-rendering-dir``.

    The Client uploads it after the Worker registered, not ``WorkerOptions.flow_definitions``,
    because the Worker's upload runs inside ``start`` with a two-minute deadline. The outcome
    and any failure go to the log file only.
    """
    document = session_flow_definition_document(flow_type, SESSION_ACTION_PERMISSION)
    try:
        (result,) = await asyncio.wait_for(
            client.upload_flow_definitions([document]),
            _SESSION_FLOW_DEFINITION_UPLOAD_TIMEOUT_SECONDS,
        )
    except Exception as error:  # Any failure only loses the Dex Web graph of this session.
        _LOGGER.warning(
            "session Flow definition upload failed; Dex Web shows this session only with "
            "--flow-rendering-dir: flowType=%s %s",
            flow_type,
            _describe_upload_error(error),
        )
        return
    _LOGGER.info(
        "session Flow definition %s: flowType=%s storedDigest=%s",
        result.outcome.value,
        flow_type,
        result.stored_digest,
    )


def _describe_upload_error(error: BaseException) -> str:
    """Return the error's type and text, or its type alone when the text is empty."""
    text = str(error)
    return f"{type(error).__name__}: {text}" if text else type(error).__name__


async def _open_session(
    client: AsyncClient,
    sessions: AgentSessionClient,
    resume_session_id: str | None,
    harness: AgentHarness,
    flow_type: str,
    identity: SessionEnvironmentIdentity,
) -> str:
    if resume_session_id is not None:
        try:
            info = await client.describe_flow(resume_session_id)
        except FlowNotFoundError as error:
            raise CommandError(
                f"session {resume_session_id} does not exist; list sessions with "
                f"{command_name(harness)} sessions"
            ) from error
        if info.flow_type != flow_type:
            raise CommandError(
                f"session {resume_session_id} is a {info.flow_type} session, not {flow_type}; "
                "resume it as the user, with the configuration file and workspace, that started it"
            )
        if info.status is not FlowStatus.RUNNING:
            raise CommandError(
                f"session {resume_session_id} is {info.status.value}, so it cannot continue"
            )
        return resume_session_id
    session_id = f"{harness.name}-{datetime.now(UTC):%Y%m%d-%H%M%S}-{secrets.token_hex(3)}"
    await sessions.start_session(session_id, start_environment=session_start_environment(identity))
    loop = asyncio.get_running_loop()
    deadline = loop.time() + _SESSION_START_TIMEOUT_SECONDS
    while (await sessions.get_agent_snapshot(session_id)).state is None:
        if loop.time() > deadline:
            raise CommandError(f"session {session_id} did not start; is the Worker running?")
        await asyncio.sleep(_SESSION_START_POLL_SECONDS)
    return session_id


async def _list_sessions(
    loaded: LoadedConfig,
    environment: CommandEnvironment,
    harness: AgentHarness,
    server: _CommandDexServer,
) -> int:
    endpoint = await server.resolve(loaded.config)
    workspace = environment.expand_path(loaded.config.workspace, loaded.directory)
    flow_type = session_flow_type(harness.flow_type, loaded.path, workspace)
    blob_cache = open_blob_cache(
        BlobCacheConfig(str(environment.agents_directory / "cache" / "sessions"), _BLOB_CACHE_BYTES)
    )
    tls = FlowServiceTlsOptions() if endpoint.is_tls else None
    client = AsyncClient(
        Registry([], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=endpoint.address, flow_service_tls=tls),
    )
    try:
        collected = await collect_sessions_for_list(
            lambda next_page_token: list_agent_sessions(
                client, flow_type, next_page_token=next_page_token
            )
        )
    finally:
        await client.close()
        blob_cache.close()
    sessions = collected.sessions
    if not sessions and not collected.has_more_sessions:
        print("No sessions yet.")
        return 0
    for session in sort_agent_sessions_by_last_activity(sessions):
        print(_session_line(session))
    if collected.has_more_sessions:
        print(
            f"More sessions exist; this list sorts the first {len(sessions)} that Dex returns: "
            "running sessions first, then the most recently closed and started."
        )
    print(f"Resume one with: {command_name(harness)} --resume <session-id>")
    return 0


async def _list_connectors(
    loaded: LoadedConfig,
    environment: CommandEnvironment,
    harness: AgentHarness,
    server: _CommandDexServer,
) -> int:
    config = loaded.config
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


def _session_line(session: AgentSessionSummary) -> str:
    """Return one ``sessions`` line; an earlier release's session lacks the last three parts."""
    line = (
        f"{session.session_id}  {session.status.name.lower()}  "
        f"started {_local_minute(session.started_at)}"
    )
    if session.last_activity_at is not None:
        line += f"  updated {_local_minute(session.last_activity_at)}"
    if session.turn_count is not None:
        line += f"  {session.turn_count} {'turn' if session.turn_count == 1 else 'turns'}"
    if session.title is not None:
        line += f"  {session.title}"
    return line


def _local_minute(moment: datetime) -> str:
    return moment.astimezone().strftime("%Y-%m-%d %H:%M")
