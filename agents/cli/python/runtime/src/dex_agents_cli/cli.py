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
import logging
import secrets
import sys
import webbrowser
from collections.abc import Sequence
from dataclasses import dataclass, replace
from datetime import datetime, timedelta
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

from dex_agents import AgentSessionClient
from dex_agents.stepwise import ToolSource
from dex_agents_cli.chat import Terminal, TerminalChat
from dex_agents_cli.config import AUTO, ConnectorEntry, EffectiveConfig
from dex_agents_cli.config_file import (
    ConfigFileError,
    LoadedConfig,
    ask_config_values,
    default_config_path,
    default_config_values,
    defaults_without_file,
    load_config_file,
    write_config_file,
)
from dex_agents_cli.connectors import (
    ConnectorCatalogTool,
    ConnectorsMcpToolSource,
    ConnectorsMcpUnavailableError,
    read_connector_catalog,
)
from dex_agents_cli.dex_server import DexServerEndpoint, DexServerError, resolve_dex_server
from dex_agents_cli.environment import RuntimeEnvironment
from dex_agents_cli.harness import (
    AgentHarness,
    HarnessContext,
    LocalToolGroup,
    session_flow_type,
)
from dex_agents_cli.local_tools import LocalToolSource
from dex_agents_cli.turns import TurnConfigResolver, with_turn_config

#: The Dex Web page where connections are authorized.
CONNECTORS_WEB_PATH = "/v1/connectors"
_BLOB_CACHE_BYTES = 64 * 1024 * 1024
_WORKER_REGISTRATION_TIMEOUT_SECONDS = 30.0
_SESSION_LIST_PAGE_SIZE = 50
_SESSION_START_TIMEOUT_SECONDS = 60.0
_SESSION_START_POLL_SECONDS = 0.1
_LOGGER = logging.getLogger(__name__)


class CommandError(Exception):
    """Report a usage or configuration error; the command prints it and exits with code 2."""


class CommandFailure(Exception):
    """Report a failure at run time; the command prints it and exits with code 1."""


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
        and 2 for a usage or configuration error.
    """
    arguments = _parse_arguments(harness, argv)
    try:
        environment = RuntimeEnvironment.from_environment()
    except ValueError as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2
    _configure_logging(environment, harness)
    try:
        match arguments.command:
            case "init":
                return _init(arguments, environment, harness)
            case "sessions":
                return asyncio.run(_list_sessions(arguments, environment, harness))
            case "connectors":
                return asyncio.run(_list_connectors(arguments, environment, harness))
            case _:
                return asyncio.run(_run(arguments, environment, harness))
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


def _configure_logging(environment: RuntimeEnvironment, harness: AgentHarness) -> None:
    environment.agents_directory.mkdir(parents=True, exist_ok=True)
    log_path = environment.agents_directory / f"{harness.name}.log"
    handler = logging.FileHandler(log_path, encoding="utf-8")
    handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(name)s: %(message)s"))
    root = logging.getLogger()
    root.handlers[:] = [handler]
    root.setLevel(logging.INFO)


def _is_interactive() -> bool:
    return sys.stdin.isatty() and sys.stdout.isatty()


def _init(arguments: _Arguments, environment: RuntimeEnvironment, harness: AgentHarness) -> int:
    path = arguments.config_path or default_config_path(environment, harness)
    if arguments.is_yes or not _is_interactive():
        values = default_config_values(harness)
    else:
        values = ask_config_values(harness, _ask)
    write_config_file(path, values, harness)
    print(f"Wrote {path}.")
    return 0


def _load_config(
    arguments: _Arguments, environment: RuntimeEnvironment, harness: AgentHarness
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
    except EOFError:
        answer = ""
    return answer if answer.strip() else default


async def _run(
    arguments: _Arguments, environment: RuntimeEnvironment, harness: AgentHarness
) -> int:
    loaded = _load_config(arguments, environment, harness)
    config = _with_model_override(loaded.config, arguments)
    prompt_text = _read_system_prompt_file(config, loaded, environment)
    workspace = environment.expand_path(config.workspace, loaded.directory)
    workspace.mkdir(parents=True, exist_ok=True)
    resolver = TurnConfigResolver(
        config,
        config.system_prompt if config.system_prompt is not None else prompt_text,
        _report_ignored_settings,
    )
    endpoint = await _resolve_server(config, environment)
    connectors = await _connect_connectors(config, endpoint, resolver)
    local_tools = _local_tool_source(config, workspace, harness, resolver)
    tool_sources: list[ToolSource] = [
        source for source in (connectors, local_tools) if source is not None
    ]
    flow_type = session_flow_type(harness.flow_type, loaded.path, workspace)
    context = HarnessContext(
        flow_type=flow_type,
        config=config,
        workspace=workspace.resolve(),
        instructions=resolver.file_instructions,
        harness_options=config.harness_options,
        tool_sources=tuple(tool_sources),
        resolver=resolver,
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
        sessions = AgentSessionClient(client, flow)
        session_id = await _open_session(
            client, sessions, arguments.resume_session_id, harness, flow_type
        )
        print(f"Session {session_id}")
        if endpoint.web_url is not None:
            print(f"Dex Web: {endpoint.web_url}/v1/flows/{session_id}?view=session")
        terminal.start()
        chat = TerminalChat(client, sessions, session_id, terminal, command_name(harness))
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


def _read_system_prompt_file(
    config: EffectiveConfig, loaded: LoadedConfig, environment: RuntimeEnvironment
) -> str | None:
    if config.system_prompt_file is None:
        return None
    path = environment.expand_path(config.system_prompt_file, loaded.directory)
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as error:
        raise CommandError(f"cannot read system_prompt_file {path}: {error}") from error


async def _resolve_server(
    config: EffectiveConfig, environment: RuntimeEnvironment
) -> DexServerEndpoint:
    return await resolve_dex_server(config.dex.server, environment, report=print)


def _connectors_mcp_url(config: EffectiveConfig, endpoint: DexServerEndpoint) -> str | None:
    if config.connectors.mcp != AUTO:
        return config.connectors.mcp
    return endpoint.connectors_mcp_url


async def _connect_connectors(
    config: EffectiveConfig, endpoint: DexServerEndpoint, resolver: TurnConfigResolver
) -> ConnectorsMcpToolSource | None:
    """Check the connectors MCP server and return its source, or ``None`` for no connector tools.

    An unavailable ``auto`` server only warns: the source lists it again on
    every turn, so a server that is still starting comes alive. An explicit
    URL that is unavailable fails the command with exit code 1.
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
    try:
        catalog = await read_connector_catalog(url)
    except ConnectorsMcpUnavailableError as error:
        message = f"The connectors MCP server is unavailable: {error}"
        if config.connectors.mcp != AUTO:
            raise CommandFailure(message) from error
        print(f"{message}. Continuing without connector tools until it answers.")
        return ConnectorsMcpToolSource(
            url, resolver, is_available=False, report=_report_on_standard_error
        )
    _report_unauthorized(config, catalog, endpoint.web_url)
    source = ConnectorsMcpToolSource(url, resolver, report=_report_on_standard_error)
    await source.list_file_enabled_tools()
    return source


def _report_unauthorized(
    config: EffectiveConfig, catalog: Sequence[ConnectorCatalogTool], web_url: str | None
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
    if web_url is not None and _is_interactive():
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


async def _open_session(
    client: AsyncClient,
    sessions: AgentSessionClient,
    resume_session_id: str | None,
    harness: AgentHarness,
    flow_type: str,
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
    session_id = f"{harness.name}-{datetime.now():%Y%m%d-%H%M%S}-{secrets.token_hex(3)}"
    await sessions.start_session(session_id)
    loop = asyncio.get_running_loop()
    deadline = loop.time() + _SESSION_START_TIMEOUT_SECONDS
    while (await sessions.get_agent_snapshot(session_id)).state is None:
        if loop.time() > deadline:
            raise CommandError(f"session {session_id} did not start; is the Worker running?")
        await asyncio.sleep(_SESSION_START_POLL_SECONDS)
    return session_id


async def _list_sessions(
    arguments: _Arguments, environment: RuntimeEnvironment, harness: AgentHarness
) -> int:
    loaded = _load_config(arguments, environment, harness)
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
    try:
        page = await client.search_flows(f"FlowType = {_quote(flow_type)}", _SESSION_LIST_PAGE_SIZE)
    finally:
        await client.close()
        blob_cache.close()
    if not page.flows:
        print("No sessions yet.")
        return 0
    for entry in page.flows:
        started = entry.started_at.astimezone().strftime("%Y-%m-%d %H:%M")
        print(f"{entry.flow_id}  {entry.status.name.lower()}  started {started}")
    print(f"Resume one with: {command_name(harness)} --resume <session-id>")
    return 0


async def _list_connectors(
    arguments: _Arguments, environment: RuntimeEnvironment, harness: AgentHarness
) -> int:
    loaded = _load_config(arguments, environment, harness)
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


def _quote(text: str) -> str:
    return '"' + text.replace("\\", "\\\\").replace('"', '\\"') + '"'
