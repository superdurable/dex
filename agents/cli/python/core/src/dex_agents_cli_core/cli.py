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
import logging
import secrets
import sys
import webbrowser
from collections.abc import Sequence
from dataclasses import dataclass, replace
from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx2
from dex import (
    AsyncClient,
    AsyncWorker,
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
    AgentSessionClient,
    AgentSessionSummary,
    LocalDirectoryMediaStore,
    MediaStore,
    list_agent_sessions,
    session_flow_definition_document,
    sort_agent_sessions_by_last_activity,
)
from dex_agents.builtin_loop import ToolSource
from dex_agents_cli_core.chat import Terminal, TerminalChat
from dex_agents_cli_core.config import AUTO, ConnectorEntry, EffectiveConfig
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
    ConnectorsMcpToolSource,
    ConnectorsMcpUnavailableError,
    read_connector_catalog,
)
from dex_agents_cli_core.dex_server import DexServerEndpoint, DexServerError, resolve_dex_server
from dex_agents_cli_core.environment import CommandEnvironment
from dex_agents_cli_core.harness import (
    SESSION_ACTION_PERMISSION,
    AgentHarness,
    HarnessContext,
    LocalToolGroup,
    SessionEnvironmentIdentity,
    current_session_environment_identity,
    namespace_session_flow_type,
    session_flow_type,
    session_start_environment,
)
from dex_agents_cli_core.local_tools import LocalToolSource
from dex_agents_cli_core.turns import TurnConfigResolver, with_turn_config

#: The Dex Web page where connections are authorized.
CONNECTORS_WEB_PATH = "/v1/connectors"
#: The directory under ``~/.dex/agents`` that ``media.store: auto`` keeps images in.
MEDIA_DIRECTORY = "media"
_BLOB_CACHE_BYTES = 64 * 1024 * 1024
#: Most pages of 50 sessions that ``sessions`` loads before it sorts them.
SESSION_LIST_MAX_PAGES = 5
_WORKER_REGISTRATION_TIMEOUT_SECONDS = 30.0
# How long the command waits for the session graph upload before it logs a failure.
_SESSION_FLOW_DEFINITION_UPLOAD_TIMEOUT_SECONDS = 10.0
_SESSION_START_TIMEOUT_SECONDS = 60.0
_SESSION_START_POLL_SECONDS = 0.1
# The shell's exit code for a command that Ctrl-C ended.
_INTERRUPTED_EXIT_CODE = 130
_LOGGER = logging.getLogger(__name__)


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


def run_agent_cli(harness: AgentHarness, argv: Sequence[str] | None = None) -> int:
    """Run a harness command: parse arguments, then run its subcommand.

    Call it from a harness package's console script:

    Example:
        >>> def run() -> None:
        ...     raise SystemExit(run_agent_cli(ClaudeHarness()))

    Args:
        harness: The command's harness.
        argv: The arguments without the program name; ``None`` reads ``sys.argv``.

    Returns:
        The exit code: 0 on success, 1 for a failure at run time, such as a
        Dex server or an explicit connectors MCP server that is unavailable,
        2 for a usage or configuration error, and 130 for Ctrl-C during the init
        questions. Input that ends during the init questions exits with 1. A
        cancelled init writes no file.
    """
    arguments = _parse_arguments(harness, argv)
    try:
        environment = CommandEnvironment.from_environment()
    except ValueError as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2
    _configure_logging(environment, harness)
    try:
        if arguments.command == "init":
            return _init(arguments, environment, harness)
        # Outside asyncio.run, because its SIGINT handler would keep Ctrl-C from ending input().
        loaded = _load_config(arguments, environment, harness)
        match arguments.command:
            case "sessions":
                return asyncio.run(_list_sessions(loaded, environment, harness))
            case "connectors":
                return asyncio.run(_list_connectors(loaded, environment, harness))
            case _:
                return asyncio.run(_run(arguments, loaded, environment, harness))
    except _InitCancelled as cancelled:
        print("\nCancelled; no configuration written.", file=sys.stderr)
        return cancelled.exit_code
    except (CommandError, ConfigFileError) as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2
    except (CommandFailure, DexServerError) as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 1


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
    namespace = parser.parse_args(argv)
    return _Arguments(
        command=namespace.command,
        config_path=namespace.config,
        is_yes=namespace.yes,
        resume_session_id=namespace.resume,
        model=namespace.model,
    )


def _configure_logging(environment: CommandEnvironment, harness: AgentHarness) -> None:
    environment.agents_directory.mkdir(parents=True, exist_ok=True)
    log_path = environment.agents_directory / f"{harness.name}.log"
    handler = logging.FileHandler(log_path, encoding="utf-8")
    handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(name)s: %(message)s"))
    root = logging.getLogger()
    root.handlers[:] = [handler]
    root.setLevel(logging.INFO)


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
) -> int:
    config = _with_model_override(loaded.config, arguments)
    media_store = _build_media_store(config, environment)
    prompt_text = _read_system_prompt_file(config, loaded, environment)
    workspace = environment.expand_path(config.workspace, loaded.directory)
    workspace.mkdir(parents=True, exist_ok=True)
    resolver = TurnConfigResolver(
        config,
        config.system_prompt if config.system_prompt is not None else prompt_text,
        _report_ignored_settings,
    )
    endpoint = await _resolve_server(config, environment)
    connectors = await _connect_connectors(
        config,
        endpoint,
        resolver,
        harness,
        media_store,
        is_browser_opening_enabled=environment.is_browser_opening_enabled,
    )
    local_tools = _local_tool_source(config, workspace, harness, resolver)
    tool_sources: list[ToolSource] = [
        source for source in (connectors, local_tools) if source is not None
    ]
    identity = current_session_environment_identity(loaded.path, workspace)
    flow_type = namespace_session_flow_type(harness.flow_type, identity)
    context = HarnessContext(
        flow_type=flow_type,
        config=config,
        workspace=workspace.resolve(),
        instructions=resolver.file_instructions,
        harness_options=config.harness_options,
        tool_sources=tuple(tool_sources),
        resolver=resolver,
        media_store=media_store,
    )
    flow = with_turn_config(harness.create_session_flow(context), resolver)
    if flow.get_flow_type() != flow_type:
        raise CommandError(f"the harness built Flow type {flow.get_flow_type()}, not {flow_type}")
    registry = Registry([flow], allow_async_handlers=True)
    cache_directory = environment.agents_directory / "cache" / harness.name
    cache_directory.mkdir(parents=True, exist_ok=True)
    blob_cache = open_blob_cache(BlobCacheConfig(str(cache_directory), _BLOB_CACHE_BYTES))
    tls = FlowServiceTlsOptions() if endpoint.is_tls else None
    client = AsyncClient(
        registry, blob_cache, ClientOptions(server_address=endpoint.address, flow_service_tls=tls)
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
    serving = asyncio.create_task(worker.start())
    terminal = Terminal()
    try:
        await worker.wait_pull_registered(timeout=_WORKER_REGISTRATION_TIMEOUT_SECONDS)
        await _upload_session_flow_definition(client, flow_type)
        sessions = AgentSessionClient(client, flow)
        session_id = await _open_session(
            client, sessions, arguments.resume_session_id, harness, flow_type, identity
        )
        print(f"Session {session_id}")
        if endpoint.web_url is not None:
            print(f"Dex Web: {endpoint.web_url}/v1/flows/{session_id}?view=session")
        terminal.start()
        chat = TerminalChat(
            client,
            sessions,
            session_id,
            terminal,
            command_name(harness),
            media_store=media_store,
            home=environment.home,
        )
        is_failed = not await chat.run()
    finally:
        with contextlib.suppress(RuntimeError):
            terminal.stop()
        await worker.stop()
        with contextlib.suppress(asyncio.CancelledError):
            await serving
        if local_tools is not None:
            await local_tools.browser.close()
        await client.close()
        blob_cache.close()
    return 1 if is_failed else 0


def _with_model_override(config: EffectiveConfig, arguments: _Arguments) -> EffectiveConfig:
    return config if arguments.model is None else replace(config, model=arguments.model)


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


async def _resolve_server(
    config: EffectiveConfig, environment: CommandEnvironment
) -> DexServerEndpoint:
    return await resolve_dex_server(config.dex.server, environment, report=print)


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
    *,
    is_browser_opening_enabled: bool,
) -> ConnectorsMcpToolSource | None:
    """Check the connectors MCP server and return its source, or ``None`` for no connector tools.

    An unavailable ``auto`` server only warns: the source lists it again on
    every turn, so a server that is still starting comes alive. A harness
    whose tool set is fixed at build time gets no connector tools in this run
    then, and the warning says to restart it. An explicit URL that is
    unavailable fails the command with exit code 1.
    """
    if config.connectors.enabled == ():
        return None
    url = _connectors_mcp_url(config, endpoint)
    if url is None:
        print(
            "Connector tools are off: connectors.mcp is auto, which needs dex.server: auto. "
            "Set connectors.mcp to the MCP server's URL."
        )
        return None
    is_tool_set_fixed = _is_tool_set_fixed_at_build(harness)
    try:
        catalog = await read_connector_catalog(url)
    except ConnectorsMcpUnavailableError as error:
        message = f"The connectors MCP server is unavailable: {error}"
        if config.connectors.mcp != AUTO:
            raise CommandFailure(message) from error
        if is_tool_set_fixed:
            print(f"{message}. {_restart_for_connector_tools(harness)}")
        else:
            print(f"{message}. Continuing without connector tools until it answers.")
        return ConnectorsMcpToolSource(
            url,
            resolver,
            is_available=False,
            report=_report_on_standard_error,
            media_store=media_store,
        )
    _report_unauthorized(config, catalog, endpoint.web_url, is_browser_opening_enabled)
    source = ConnectorsMcpToolSource(
        url, resolver, report=_report_on_standard_error, media_store=media_store
    )
    await source.list_file_enabled_tools()
    if is_tool_set_fixed and not source.is_available:
        restart = _restart_for_connector_tools(harness)
        print(f"The connectors MCP server could not be listed. {restart}")
    return source


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
    is_browser_opening_enabled: bool,
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
    print(f"No authorized connection for {names}. Authorize it on {page}.")
    if web_url is not None and is_browser_opening_enabled and _is_interactive():
        webbrowser.open(f"{web_url}{CONNECTORS_WEB_PATH}")


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
    return LocalToolSource(workspace, groups, resolver) if groups else None


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
    loaded: LoadedConfig, environment: CommandEnvironment, harness: AgentHarness
) -> int:
    endpoint = await _resolve_server(loaded.config, environment)
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
    sessions: list[AgentSessionSummary] = []
    next_page_token = ""
    try:
        for _ in range(SESSION_LIST_MAX_PAGES):
            page = await list_agent_sessions(client, flow_type, next_page_token=next_page_token)
            sessions.extend(page.sessions)
            next_page_token = page.next_page_token
            if not next_page_token:
                break
    finally:
        await client.close()
        blob_cache.close()
    if not sessions:
        print("No sessions yet.")
        return 0
    for session in sort_agent_sessions_by_last_activity(sessions):
        print(_session_line(session))
    if next_page_token:
        print(
            f"More sessions exist; this list sorts the first {len(sessions)} that Dex returns: "
            "running sessions first, then the most recently closed and started."
        )
    print(f"Resume one with: {command_name(harness)} --resume <session-id>")
    return 0


async def _list_connectors(
    loaded: LoadedConfig, environment: CommandEnvironment, harness: AgentHarness
) -> int:
    config = loaded.config
    endpoint = await _resolve_server(config, environment)
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
