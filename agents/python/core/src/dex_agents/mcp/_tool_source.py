# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""``McpToolSource``: the tools of configured MCP servers as deferred agent tools."""

from __future__ import annotations

import asyncio
import importlib.metadata
import json
import logging
from collections.abc import AsyncIterator, Awaitable, Callable, Mapping, Sequence
from contextlib import asynccontextmanager
from dataclasses import dataclass
from datetime import timedelta
from typing import Any, TypeAlias, cast

import httpx2
from mcp import Client, Implementation, MCPError, StdioServerParameters
from mcp.client import Transport
from mcp.client.streamable_http import streamable_http_client
from mcp_types import (
    CONNECTION_CLOSED,
    HEADER_MISMATCH,
    INTERNAL_ERROR,
    PARSE_ERROR,
    REQUEST_TIMEOUT,
    CallToolResult,
    InputRequiredResult,
    RequestParamsMeta,
    Result,
)
from mcp_types.version import MODERN_PROTOCOL_VERSIONS
from pydantic import BaseModel

from dex_agents.events import AgentEventWriter
from dex_agents.mcp._servers import (
    McpServerConfig,
    McpStdioServerConfig,
    McpStreamableHttpServerConfig,
)
from dex_agents.mcp._tasks import (
    TERMINAL_TASK_STATUSES,
    CancelMcpTaskRequest,
    CreatedMcpTask,
    GetMcpTaskRequest,
    McpTaskIdParams,
    McpTasksClientExtension,
    McpTaskSnapshot,
    PolledMcpTask,
)
from dex_agents.mcp._tools import McpTool, mcp_tool_from_listing, render_tool_result_text
from dex_agents.runner import (
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
)

#: ``_meta`` key that carries the session's idempotency key on every ``tools/call``.
IDEMPOTENCY_KEY_META_KEY = "io.superdurable.dex/idempotencyKey"

#: ``AgentEvents`` type for an MCP ``notifications/progress`` of a running tool call.
MCP_TOOL_PROGRESS_EVENT_TYPE = "mcp.tool_progress"

#: ``AgentEvents`` type for each observed status of an MCP task.
MCP_TASK_STATUS_EVENT_TYPE = "mcp.task_status"

#: Default bound on one MCP request, such as ``tools/call``.
DEFAULT_MCP_REQUEST_TIMEOUT = timedelta(minutes=10)

#: Default bound on polling one MCP task in one tool Step attempt.
DEFAULT_MCP_TASK_WAIT_TIMEOUT = timedelta(minutes=10)

#: Default bound on connecting to a server and each handshake request.
DEFAULT_MCP_CONNECTION_TIMEOUT = timedelta(seconds=30)

#: Default number of connection attempts before a request reports the server unreachable.
DEFAULT_MCP_CONNECTION_ATTEMPTS = 3

#: Handler that runs one deferred tool call, as ``execute_deferred_tool_call`` does.
DeferredToolHandler: TypeAlias = Callable[[DeferredToolExecution], Awaitable[ToolExecutionResult]]

_LOGGER = logging.getLogger(__name__)
_TOOL_LISTING_PAGE_LIMIT = 100
_STATE_ONLY_RETRY_LIMIT = 10
_STATE_ONLY_RETRY_DELAY_SECONDS = 0.1
_CONNECTION_RETRY_DELAY_SECONDS = 0.5
_DEFAULT_TASK_POLL_INTERVAL_SECONDS = 1.0
_MINIMUM_TASK_POLL_INTERVAL_SECONDS = 0.05
_MAXIMUM_TASK_POLL_INTERVAL_SECONDS = 30.0
_TASK_CANCEL_TIMEOUT_SECONDS = 5.0
# The SDK also reports HTTP error statuses and unreadable bodies with these codes, after the
# server may have run the call, so they leave the outcome unknown.
_NO_RESULT_ERROR_CODES = frozenset(
    {CONNECTION_CLOSED, REQUEST_TIMEOUT, INTERNAL_ERROR, PARSE_ERROR}
)


class McpToolCallInterruptedError(Exception):
    """Report a ``tools/call`` that reached an MCP server but returned no result.

    ``McpToolSource`` raises it when the response stream broke, the request
    timed out, the server process exited, the server or a gateway answered
    with an internal error, an HTTP error status, or a body that is not
    JSON-RPC, or an MCP task stopped being observable. The tool may or may not
    have taken effect. It is not a ``ToolExecutionError``, so the tool Step's
    effect class decides what happens next: ``CallTool`` re-issues a ``READ``
    or ``IDEMPOTENT_WRITE`` call as a new request with the same idempotency
    key, and ``CallSingleAttemptTool`` records ``outcome_unknown`` for the
    other effects and never sends the call again.

    Attributes:
        server_name: Configured name of the MCP server.
        mcp_tool_name: The tool's name on that server.
        detail: What went wrong.
    """

    def __init__(self, server_name: str, mcp_tool_name: str, detail: str) -> None:
        """Create the error.

        Args:
            server_name: Configured name of the MCP server.
            mcp_tool_name: The tool's name on that server.
            detail: What went wrong.
        """
        super().__init__(
            f"MCP tool {mcp_tool_name} of server {server_name!r} returned no result: {detail}"
        )
        self.server_name = server_name
        self.mcp_tool_name = mcp_tool_name
        self.detail = detail


class McpToolListingError(Exception):
    """Report that no configured MCP server could be listed.

    ``McpToolSource.list_tools`` leaves out a server that it cannot list and
    logs a warning; it raises this error only when every configured server
    failed and none has an earlier listing. Raised from ``run_model_step``, it
    fails the ``RunModel`` attempt, which retries.

    Attributes:
        server_name: Configured name of the last MCP server that failed.
        detail: What went wrong with that server.
    """

    def __init__(self, server_name: str, detail: str) -> None:
        """Create the error.

        Args:
            server_name: Configured name of the MCP server.
            detail: What went wrong.
        """
        super().__init__(f"could not list the tools of MCP server {server_name!r}: {detail}")
        self.server_name = server_name
        self.detail = detail


@dataclass(frozen=True)
class _ToolCallSucceeded:
    output: str


@dataclass(frozen=True)
class _ToolCallFailed:
    message: str


@dataclass(frozen=True)
class _ToolCallInterrupted:
    detail: str


@dataclass(frozen=True)
class _ServerUnreachable:
    detail: str


@dataclass(frozen=True)
class _TaskLost:
    """A resumed task that the server no longer knows; the call may be sent again."""

    detail: str


_ToolCallOutcome: TypeAlias = (
    _ToolCallSucceeded | _ToolCallFailed | _ToolCallInterrupted | _ServerUnreachable | _TaskLost
)


@dataclass(frozen=True)
class _McpToolTarget:
    """Where one call goes: the configured server and the tool's name there."""

    server: McpServerConfig
    mcp_tool_name: str
    tool_name: str


class McpToolSource:
    """Offer the tools of configured MCP servers to an agent, one Dex Step per call.

    Configure the servers on the Worker, never from Flow input, a user
    message, or a browser. ``list_tools`` lists each server's tools and caches
    them per server as ``McpTool`` definitions that a runner offers to the
    model. When the model calls one, the runner returns ``deferred_tool_call``,
    so the session runs the call in its own tool Step, and the Step calls
    ``execute_deferred_tool_call``. No MCP call runs inside ``RunModel``.
    ``deferred_tool_call`` stores the server and the tool's MCP name on the
    call's record, so the tool Step calls that server directly, whatever the
    other servers do.

    Each tool's effect comes from its annotations: ``readOnlyHint`` makes it
    ``READ``, ``idempotentHint`` makes it ``IDEMPOTENT_WRITE``, and any other
    tool gets the session's ``default_tool_effect``, ``UNCLASSIFIED`` by
    default. Annotations are trusted by default. A ``ToolPolicy`` in
    ``AgentSessionOptions.tool_policies`` keyed by ``McpTool.name`` wins over
    the annotations.

    Every request opens a new connection: a new process for a stdio server, a
    new HTTP connection for a Streamable HTTP server. The source speaks MCP
    2026-07-28, which has no protocol session, and falls back to the
    ``initialize`` handshake for older servers. Every ``tools/call`` carries
    the session's idempotency key in ``_meta`` under
    ``IDEMPOTENCY_KEY_META_KEY``, and the MCP SDK adds ``traceparent`` when
    OpenTelemetry has an active trace.

    Results map onto the session contract:

    * A result becomes the tool output: text blocks joined with newlines,
      other blocks and structured content as JSON.
    * An ``isError`` result, a JSON-RPC error response other than an internal
      error, a failed or cancelled task, and an ``input_required`` answer
      raise ``ToolExecutionError``, so the session records ``failed`` and the
      model sees the text.
    * No result after the request was sent raises
      ``McpToolCallInterruptedError``; the tool Step's effect decides whether
      it is re-issued.
    * A call that was never sent, because its server is not configured or
      cannot be reached, raises ``ToolExecutionError``.
    * A task handle from the ``io.modelcontextprotocol/tasks`` extension is
      polled with ``tasks/get`` inside the tool Step, with a heartbeat per
      poll, for at most ``task_wait_timeout`` per attempt. For a Streamable
      HTTP server the task ID is saved as the attempt's heartbeat checkpoint,
      so a retried attempt polls the same task instead of calling again.

    The source is safe to share between the parallel tool Steps of one
    Worker. Build one per Worker process at startup.

    Example:
        >>> tools = McpToolSource([
        ...     McpStdioServerConfig(name="files", command="mcp-server-files"),
        ...     McpStreamableHttpServerConfig(name="tickets", url=tickets_url),
        ... ])
        >>> class MyRunner:
        ...     async def run_model_step(self, turn):
        ...         offered = await tools.list_tools()  # name, description, input_schema
        ...         response = await call_my_model(turn.messages, offered)
        ...         return ToolCallsRequested(
        ...             framework_run_state=serialize(response),
        ...             tool_calls=[tools.deferred_tool_call(
        ...                 turn, tool_call_id=c.id, tool_name=c.name, arguments=c.arguments)
        ...                 for c in response.tool_calls])
        ...
        ...     async def execute_deferred_tool_call(self, call):
        ...         return await tools.execute_deferred_tool_call(call)
    """

    def __init__(
        self,
        servers: Sequence[McpServerConfig],
        *,
        request_timeout: timedelta = DEFAULT_MCP_REQUEST_TIMEOUT,
        task_wait_timeout: timedelta = DEFAULT_MCP_TASK_WAIT_TIMEOUT,
        connection_timeout: timedelta = DEFAULT_MCP_CONNECTION_TIMEOUT,
        connection_attempts: int = DEFAULT_MCP_CONNECTION_ATTEMPTS,
        client_name: str = "dex-agents",
    ) -> None:
        """Describe the servers without connecting to them.

        Args:
            servers: Server configurations with unique names.
            request_timeout: Bound on one ``tools/call`` or ``tasks/get``
                request. A ``tools/call`` that times out raises
                ``McpToolCallInterruptedError``. Defaults to 10 minutes. Keep
                it below the tool Step's two-hour method timeout.
            task_wait_timeout: Bound on polling one MCP task in one tool Step
                attempt. When it passes, the source raises
                ``McpToolCallInterruptedError`` and leaves the task running; a
                retried attempt resumes polling it. Defaults to 10 minutes.
            connection_timeout: Bound on connecting to a server and on each
                handshake and ``tools/list`` request. Defaults to 30 seconds.
            connection_attempts: Connection attempts per request before the
                server counts as unreachable. Defaults to 3.
            client_name: ``clientInfo.name`` sent to the servers.

        Raises:
            ValueError: If server names repeat, a timeout is not positive, or
                ``connection_attempts`` is less than 1.
        """
        self._servers: dict[str, McpServerConfig] = {}
        for server in servers:
            if server.name in self._servers:
                raise ValueError(f"MCP server name {server.name!r} is configured twice")
            self._servers[server.name] = server
        timeouts = (request_timeout, task_wait_timeout, connection_timeout)
        if any(timeout <= timedelta(0) for timeout in timeouts):
            raise ValueError(
                "request_timeout, task_wait_timeout, and connection_timeout must be positive"
            )
        if connection_attempts < 1:
            raise ValueError("connection_attempts must be at least 1")
        self._request_timeout_seconds = request_timeout.total_seconds()
        self._task_wait_timeout = task_wait_timeout
        self._connection_timeout_seconds = connection_timeout.total_seconds()
        self._connection_attempts = connection_attempts
        self._client_info = Implementation(name=client_name, version=_dex_agents_version())
        self._tools_by_server: dict[str, tuple[McpTool, ...]] = {}
        self._tools: dict[str, McpTool] | None = None
        self._listing_lock = asyncio.Lock()

    async def list_tools(self, *, refresh: bool = False) -> tuple[McpTool, ...]:
        """Return the tools of every reachable configured server.

        Each server is listed on first use and cached on its own; ``refresh``
        lists every server again. A server that cannot be listed is left out
        with a warning log and tried again on the next call; when a refresh
        fails, its earlier listing stays. When two tools map to the same agent
        tool name, the first one in server order wins and the other is left out
        with a warning log. Call it from ``run_model_step`` to offer the tools,
        or at Worker startup.

        Args:
            refresh: Whether to list every server again instead of using the
                cached definitions.

        Returns:
            Every offered tool, in server order and then in each server's order.

        Raises:
            McpToolListingError: If no server could be listed and none has an
                earlier listing.
        """
        async with self._listing_lock:
            failure: McpToolListingError | None = None
            has_changed = self._tools is None
            for server in self._servers.values():
                if not refresh and server.name in self._tools_by_server:
                    continue
                try:
                    self._tools_by_server[server.name] = tuple(
                        await self._list_server_tools(server)
                    )
                    has_changed = True
                except McpToolListingError as error:
                    failure = error
                    _LOGGER.warning(
                        "left MCP server %r out of the tool listing: %s", server.name, error
                    )
            if not self._tools_by_server and failure is not None:
                raise failure
            if has_changed or self._tools is None:
                self._tools = self._combine_server_tools()
            return tuple(self._tools.values())

    def tool(self, tool_name: str) -> McpTool:
        """Return one listed tool by its agent tool name.

        Args:
            tool_name: ``McpTool.name``.

        Returns:
            The cached definition.

        Raises:
            LookupError: If ``list_tools`` has not run or no listed server
                offers the tool.
        """
        if self._tools is None:
            raise LookupError("call list_tools before looking up an MCP tool")
        tool = self._tools.get(tool_name)
        if tool is None:
            raise LookupError(f"no listed MCP server offers the tool {tool_name!r}")
        return tool

    def tool_policies(self) -> dict[str, ToolPolicy]:
        """Return a ``ToolPolicy`` for every listed tool whose annotations declare an effect.

        Use it with a framework adapter that offers the MCP tools as its own
        tools: pass it in the adapter's ``tool_policies`` and put your own
        policies after it, so yours win.

        Example:
            >>> await tools.list_tools()
            >>> tool_policies = {**tools.tool_policies(), "files__delete": ToolPolicy(
            ...     ToolEffect.NON_IDEMPOTENT_WRITE)}

        Returns:
            Policies by ``McpTool.name``, with the effect's default approval.

        Raises:
            LookupError: If ``list_tools`` has not run.
        """
        if self._tools is None:
            raise LookupError("call list_tools before reading MCP tool policies")
        return {
            tool.name: ToolPolicy(tool.framework_effect)
            for tool in self._tools.values()
            if tool.framework_effect is not None
        }

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
    ) -> DeferredToolCall:
        """Build the ``DeferredToolCall`` for one model call of a listed MCP tool.

        The policy comes from ``turn.resolve_tool_policy`` with the tool's
        annotation effect, so an explicit ``ToolPolicy`` wins. The call's
        ``tool_source_reference`` names the server and the tool's MCP name.

        Args:
            turn: The ``RunModel`` context.
            tool_call_id: Framework tool call ID.
            tool_name: ``McpTool.name`` the model called.
            arguments: Tool arguments as JSON text.

        Returns:
            The call to put in ``ToolCallsRequested.tool_calls``.

        Raises:
            LookupError: If ``list_tools`` has not run or the tool is unknown.
        """
        tool = self.tool(tool_name)
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=turn.resolve_tool_policy(tool_name, tool.framework_effect),
            tool_source_reference=json.dumps(
                {"mcp_server": tool.server_name, "mcp_tool": tool.mcp_tool_name}
            ),
        )

    async def deferred_tool_handlers(self) -> dict[str, DeferredToolHandler]:
        """Return one deferred-call handler per listed MCP tool, keyed by agent tool name.

        Merge them with a runner's own handlers and dispatch
        ``execute_deferred_tool_call`` by ``call.tool_call.tool_name``. Build the
        map inside ``execute_deferred_tool_call``, not once at construction: a
        replacement Worker runs tool Steps before any ``RunModel``.

        Example:
            >>> async def execute_deferred_tool_call(self, call):
            ...     handlers = {**self._local_handlers, **await tools.deferred_tool_handlers()}
            ...     return await handlers[call.tool_call.tool_name](call)

        Returns:
            Handlers that each call ``execute_deferred_tool_call``.

        Raises:
            McpToolListingError: If no server could be listed.
        """
        return {tool.name: self.execute_deferred_tool_call for tool in await self.list_tools()}

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        """Run one deferred MCP tool call in its tool Step.

        Call it from ``AgentFrameworkRunner.execute_deferred_tool_call``. A
        call built by ``deferred_tool_call`` goes straight to the server named
        on its record, without a listing. It passes the call's idempotency key,
        events, heartbeat, and heartbeat checkpoint on.

        Args:
            call: The approved call from the tool Step.

        Returns:
            The tool output the model sees.

        Raises:
            ToolExecutionError: The arguments are not a JSON object, the tool
                failed, the server answered with an error or asked for input,
                or the call was never sent.
            McpToolCallInterruptedError: The call was sent and no result came back.
        """
        record = call.tool_call
        arguments = _parse_tool_arguments(record.arguments)
        target = self._target_from_reference(record.tool_name, record.tool_source_reference)
        if target is None:
            target = await self._target_from_listing(record.tool_name)
        return await self._call_target(
            target,
            arguments,
            idempotency_key=call.idempotency_key,
            tool_call_id=record.tool_call_id,
            events=call.events,
            heartbeat=call.heartbeat,
            heartbeat_checkpoint=call.heartbeat_checkpoint,
            save_heartbeat_checkpoint=call.save_heartbeat_checkpoint,
        )

    async def call_tool(
        self,
        tool_name: str,
        arguments: Mapping[str, Any],
        *,
        idempotency_key: str | None = None,
        tool_call_id: str | None = None,
        events: AgentEventWriter | None = None,
        heartbeat: Callable[[], Awaitable[None]] | None = None,
        heartbeat_checkpoint: str | None = None,
        save_heartbeat_checkpoint: Callable[[str], Awaitable[None]] | None = None,
    ) -> ToolExecutionResult:
        """Call one listed MCP tool once, as a tool Step does.

        Use it from a framework tool that wraps an MCP tool, when the adapter
        gives the tool only the idempotency key. It finds the tool's server
        through the per-server listing. Call it only inside a tool Step: the
        session's retry rules for the effect class assume that.

        Args:
            tool_name: ``McpTool.name``.
            arguments: Tool arguments.
            idempotency_key: Key sent in ``_meta`` under
                ``IDEMPOTENCY_KEY_META_KEY``, or ``None`` to send none.
            tool_call_id: Tool call ID stamped on progress events.
            events: Writer for ``mcp.tool_progress`` and ``mcp.task_status``
                events, or ``None``.
            heartbeat: Awaited between task polls, or ``None``.
            heartbeat_checkpoint: ``DeferredToolExecution.heartbeat_checkpoint``,
                so a retried attempt resumes the earlier attempt's MCP task.
            save_heartbeat_checkpoint:
                ``DeferredToolExecution.save_heartbeat_checkpoint``, so an MCP
                task survives a retry. ``None`` saves nothing.

        Returns:
            The tool output the model sees.

        Raises:
            ToolExecutionError: The tool failed, the server answered with an
                error or asked for input, or the call was never sent.
            McpToolCallInterruptedError: The call was sent and no result came back.
        """
        return await self._call_target(
            await self._target_from_listing(tool_name),
            dict(arguments),
            idempotency_key=idempotency_key,
            tool_call_id=tool_call_id,
            events=events,
            heartbeat=heartbeat,
            heartbeat_checkpoint=heartbeat_checkpoint,
            save_heartbeat_checkpoint=save_heartbeat_checkpoint,
        )

    async def _call_target(
        self,
        target: _McpToolTarget,
        arguments: dict[str, Any],
        *,
        idempotency_key: str | None,
        tool_call_id: str | None,
        events: AgentEventWriter | None,
        heartbeat: Callable[[], Awaitable[None]] | None,
        heartbeat_checkpoint: str | None,
        save_heartbeat_checkpoint: Callable[[str], Awaitable[None]] | None,
    ) -> ToolExecutionResult:
        call = _McpToolCall(
            source=self,
            target=target,
            arguments=arguments,
            meta={} if idempotency_key is None else {IDEMPOTENCY_KEY_META_KEY: idempotency_key},
            reporter=_McpToolCallReporter(target.tool_name, tool_call_id, events),
            heartbeat=heartbeat,
            save_heartbeat_checkpoint=save_heartbeat_checkpoint,
        )
        resumed_task_id = _task_id_from_checkpoint(heartbeat_checkpoint, target)
        outcome = await call.run(resumed_task_id)
        if isinstance(outcome, _ToolCallSucceeded):
            return ToolExecutionResult(output=outcome.output)
        if isinstance(outcome, _ToolCallFailed):
            raise ToolExecutionError(outcome.message)
        if isinstance(outcome, _ToolCallInterrupted | _TaskLost):
            raise McpToolCallInterruptedError(
                target.server.name, target.mcp_tool_name, outcome.detail
            )
        raise ToolExecutionError(
            f"could not connect to MCP server {target.server.name!r}, so "
            f"{target.mcp_tool_name} did not run: {outcome.detail}"
        )

    def _target_from_reference(
        self, tool_name: str, tool_source_reference: str | None
    ) -> _McpToolTarget | None:
        reference = _parse_tool_source_reference(tool_source_reference)
        if reference is None:
            return None
        server_name, mcp_tool_name = reference
        server = self._servers.get(server_name)
        if server is None:
            raise ToolExecutionError(
                f"MCP server {server_name!r} is no longer configured, so {mcp_tool_name} "
                "did not run"
            )
        return _McpToolTarget(server, mcp_tool_name, tool_name)

    async def _target_from_listing(self, tool_name: str) -> _McpToolTarget:
        try:
            tools = {tool.name: tool for tool in await self.list_tools()}
            if tool_name not in tools:
                tools = {tool.name: tool for tool in await self.list_tools(refresh=True)}
        except McpToolListingError as error:
            raise ToolExecutionError(f"{error}, so {tool_name} did not run") from error
        tool = tools.get(tool_name)
        if tool is None:
            raise ToolExecutionError(f"no listed MCP server offers the tool {tool_name!r}")
        return _McpToolTarget(self._servers[tool.server_name], tool.mcp_tool_name, tool.name)

    def _combine_server_tools(self) -> dict[str, McpTool]:
        tools: dict[str, McpTool] = {}
        for server_name in self._servers:
            for tool in self._tools_by_server.get(server_name, ()):
                existing = tools.get(tool.name)
                if existing is not None:
                    _LOGGER.warning(
                        "left MCP tool %s/%s out: %s/%s already has the agent tool name %r",
                        tool.server_name,
                        tool.mcp_tool_name,
                        existing.server_name,
                        existing.mcp_tool_name,
                        tool.name,
                    )
                    continue
                tools[tool.name] = tool
        return tools

    async def _list_server_tools(self, server: McpServerConfig) -> list[McpTool]:
        detail = "no listing attempt"
        for listing_attempt in range(1, self._connection_attempts + 1):
            try:
                async with self._new_client(server) as client:
                    listed = []
                    cursor: str | None = None
                    for _ in range(_TOOL_LISTING_PAGE_LIMIT):
                        page = await client.list_tools(cursor=cursor)
                        listed.extend(page.tools)
                        cursor = page.next_cursor
                        if not cursor:
                            break
                return [
                    mcp_tool_from_listing(server.name, tool, server.trusts_tool_annotations)
                    for tool in listed
                ]
            except Exception as error:
                detail = _describe_exception(error)
            if listing_attempt < self._connection_attempts:
                await asyncio.sleep(_CONNECTION_RETRY_DELAY_SECONDS * listing_attempt)
        raise McpToolListingError(server.name, detail)

    def _new_client(self, server: McpServerConfig) -> Client:
        transport: Transport | StdioServerParameters
        if isinstance(server, McpStdioServerConfig):
            transport = StdioServerParameters(
                command=server.command,
                args=list(server.args),
                env=dict(server.env) if server.env is not None else None,
                cwd=server.cwd,
            )
        else:
            transport = cast(
                Transport,
                _open_streamable_http_transport(
                    server, self._connection_timeout_seconds, self._request_timeout_seconds
                ),
            )
        # Handshake requests use this bound; tool calls and polls pass request_timeout.
        return Client(
            transport,
            mode="auto",
            client_info=self._client_info,
            read_timeout_seconds=self._connection_timeout_seconds,
            extensions=[McpTasksClientExtension()],
            cache=None,
        )


class _McpToolCall:
    """One tool call: connection attempts, the request, and task polling."""

    def __init__(
        self,
        *,
        source: McpToolSource,
        target: _McpToolTarget,
        arguments: dict[str, Any],
        meta: dict[str, str],
        reporter: _McpToolCallReporter,
        heartbeat: Callable[[], Awaitable[None]] | None,
        save_heartbeat_checkpoint: Callable[[str], Awaitable[None]] | None,
    ) -> None:
        self._source = source
        self._target = target
        self._arguments = arguments
        self._meta = meta
        self._reporter = reporter
        self._heartbeat = heartbeat
        self._save_heartbeat_checkpoint = save_heartbeat_checkpoint

    async def run(self, resumed_task_id: str | None) -> _ToolCallOutcome:
        if resumed_task_id is not None:
            outcome = await self._with_connection(
                lambda client: self._await_task(client, resumed_task_id, None)
            )
            if isinstance(outcome, _ServerUnreachable):
                # An earlier attempt started the task, so it may be running.
                return _ToolCallInterrupted(
                    f"could not reach the server to poll MCP task {resumed_task_id}: "
                    f"{outcome.detail}"
                )
            if not isinstance(outcome, _TaskLost):
                return outcome
            # Only CallTool retries an attempt, and it may send the call again.
            _LOGGER.warning(
                "calling MCP tool %s again: %s", self._target.mcp_tool_name, outcome.detail
            )
        return await self._with_connection(self._send_tool_call)

    async def _with_connection(
        self, operation: Callable[[Client], Awaitable[_ToolCallOutcome]]
    ) -> _ToolCallOutcome:
        outcome: _ToolCallOutcome = _ServerUnreachable("no connection attempt")
        for connection_attempt in range(1, self._source._connection_attempts + 1):
            outcome = await self._run_on_new_connection(operation)
            if not isinstance(outcome, _ServerUnreachable):
                return outcome
            if connection_attempt < self._source._connection_attempts:
                await asyncio.sleep(_CONNECTION_RETRY_DELAY_SECONDS * connection_attempt)
        return outcome

    async def _run_on_new_connection(
        self, operation: Callable[[Client], Awaitable[_ToolCallOutcome]]
    ) -> _ToolCallOutcome:
        is_request_sent = False
        outcome: _ToolCallOutcome | None = None
        try:
            async with self._source._new_client(self._target.server) as client:
                is_request_sent = True
                outcome = await operation(client)
        except Exception as error:
            if outcome is not None:
                # The result arrived; only closing the connection failed.
                return outcome
            detail = _describe_exception(error)
            if not is_request_sent:
                return _ServerUnreachable(detail)
            return _ToolCallInterrupted(f"the connection broke before a result arrived: {detail}")
        if outcome is None:
            raise RuntimeError(f"MCP tool {self._target.tool_name} finished without an outcome")
        return outcome

    async def _send_tool_call(self, client: Client) -> _ToolCallOutcome:
        request_state: str | None = None
        has_refreshed_listing = False
        for _ in range(_STATE_ONLY_RETRY_LIMIT + 1):
            try:
                result = await client.session.call_tool(
                    self._target.mcp_tool_name,
                    self._arguments,
                    read_timeout_seconds=self._source._request_timeout_seconds,
                    progress_callback=self._reporter.write_progress,
                    request_state=request_state,
                    meta=cast(RequestParamsMeta, dict(self._meta)) if self._meta else None,
                    allow_input_required=True,
                    allow_claimed=True,
                )
            except MCPError as error:
                is_modern = client.protocol_version in MODERN_PROTOCOL_VERSIONS
                if error.code == HEADER_MISMATCH and is_modern and not has_refreshed_listing:
                    # The server rejected the call unrun; a listing teaches the SDK its headers.
                    has_refreshed_listing = True
                    await _refresh_tool_listing(client, self._target.mcp_tool_name)
                    continue
                return _outcome_from_mcp_error(error)
            if isinstance(result, CallToolResult):
                return _outcome_from_tool_result(result)
            if isinstance(result, CreatedMcpTask):
                if self._save_heartbeat_checkpoint is not None and isinstance(
                    self._target.server, McpStreamableHttpServerConfig
                ):
                    # A stdio server's tasks end with its process, so only HTTP tasks resume.
                    await self._save_heartbeat_checkpoint(
                        _task_checkpoint(self._target, result.task_id)
                    )
                return await self._await_task(client, result.task_id, result)
            if isinstance(result, InputRequiredResult):
                if result.input_requests:
                    return _ToolCallFailed(_describe_input_requests(result.input_requests))
                # A state-only answer asks for the same request again; the server has not run it.
                request_state = result.request_state
                await asyncio.sleep(_STATE_ONLY_RETRY_DELAY_SECONDS)
                continue
            return _ToolCallInterrupted(
                f"the MCP server returned an unsupported {type(result).__name__}"
            )
        return _ToolCallInterrupted(
            "the MCP server kept asking to repeat tools/call without finishing it"
        )

    async def _await_task(
        self, client: Client, task_id: str, created: CreatedMcpTask | None
    ) -> _ToolCallOutcome:
        loop = asyncio.get_running_loop()
        task_wait_timeout = self._source._task_wait_timeout
        deadline = loop.time() + task_wait_timeout.total_seconds()
        snapshot: McpTaskSnapshot | None = created
        while True:
            if snapshot is not None:
                self._reporter.write_task_status(snapshot)
                if snapshot.status in TERMINAL_TASK_STATUSES:
                    return _outcome_from_terminal_task(snapshot)
                if snapshot.status == "input_required":
                    await _cancel_task_quietly(client, task_id)
                    input_requests = (
                        snapshot.input_requests if isinstance(snapshot, PolledMcpTask) else None
                    )
                    return _ToolCallFailed(_describe_input_requests(input_requests or {}))
                remaining = deadline - loop.time()
                if remaining <= 0:
                    return _ToolCallInterrupted(
                        f"MCP task {task_id} did not finish within {task_wait_timeout} in this "
                        "attempt; it keeps running, and a retried attempt resumes polling it"
                    )
                if self._heartbeat is not None:
                    await self._heartbeat()
                await asyncio.sleep(min(_task_poll_interval_seconds(snapshot), remaining))
            try:
                snapshot = await client.session.send_request(
                    GetMcpTaskRequest(params=McpTaskIdParams(task_id=task_id)),
                    PolledMcpTask,
                    request_read_timeout_seconds=self._source._request_timeout_seconds,
                )
            except MCPError as error:
                if created is None and error.code not in _NO_RESULT_ERROR_CODES:
                    return _TaskLost(f"tasks/get for MCP task {task_id} failed: {error}")
                # The task exists and may run on, so any polling failure leaves the outcome unknown.
                return _ToolCallInterrupted(f"tasks/get for MCP task {task_id} failed: {error}")


class _McpToolCallReporter:
    def __init__(
        self, tool_name: str, tool_call_id: str | None, events: AgentEventWriter | None
    ) -> None:
        self._tool_name = tool_name
        self._tool_call_id = tool_call_id
        self._events = events

    async def write_progress(
        self, progress: float, total: float | None, message: str | None
    ) -> None:
        if self._events is None:
            return
        self._events.write_event(
            MCP_TOOL_PROGRESS_EVENT_TYPE,
            {
                "tool_call_id": self._tool_call_id,
                "tool_name": self._tool_name,
                "progress": progress,
                "total": total,
                "message": message,
            },
        )

    def write_task_status(self, task: McpTaskSnapshot) -> None:
        if self._events is None:
            return
        self._events.write_event(
            MCP_TASK_STATUS_EVENT_TYPE,
            {
                "tool_call_id": self._tool_call_id,
                "tool_name": self._tool_name,
                "task_id": task.task_id,
                "status": task.status,
                "status_message": task.status_message,
            },
        )


@asynccontextmanager
async def _open_streamable_http_transport(
    server: McpStreamableHttpServerConfig,
    connection_timeout_seconds: float,
    read_timeout_seconds: float,
) -> AsyncIterator[Any]:
    http_client = httpx2.AsyncClient(
        headers=dict(server.headers or {}),
        timeout=httpx2.Timeout(connection_timeout_seconds, read=read_timeout_seconds),
    )
    async with http_client, streamable_http_client(server.url, http_client=http_client) as streams:
        yield streams


async def _refresh_tool_listing(client: Client, mcp_tool_name: str) -> None:
    cursor: str | None = None
    for _ in range(_TOOL_LISTING_PAGE_LIMIT):
        page = await client.list_tools(cursor=cursor)
        cursor = page.next_cursor
        if not cursor or any(listed.name == mcp_tool_name for listed in page.tools):
            return


async def _cancel_task_quietly(client: Client, task_id: str) -> None:
    try:
        async with asyncio.timeout(_TASK_CANCEL_TIMEOUT_SECONDS):
            await client.session.send_request(
                CancelMcpTaskRequest(params=McpTaskIdParams(task_id=task_id)), Result
            )
    except Exception as error:
        # Cancellation is cooperative; the tool's outcome is already reported either way.
        _LOGGER.warning("tasks/cancel for MCP task %s failed: %s", task_id, error)


def _parse_tool_arguments(arguments_json: str) -> dict[str, Any]:
    try:
        arguments = json.loads(arguments_json)
    except json.JSONDecodeError as error:
        raise ToolExecutionError(f"the tool arguments are not valid JSON: {error}") from error
    if not isinstance(arguments, dict):
        raise ToolExecutionError("the tool arguments must be a JSON object")
    return arguments


def _parse_tool_source_reference(tool_source_reference: str | None) -> tuple[str, str] | None:
    if tool_source_reference is None:
        return None
    try:
        reference = json.loads(tool_source_reference)
    except json.JSONDecodeError:
        return None
    if not isinstance(reference, dict):
        return None
    server_name = reference.get("mcp_server")
    mcp_tool_name = reference.get("mcp_tool")
    if not isinstance(server_name, str) or not isinstance(mcp_tool_name, str):
        return None
    return server_name, mcp_tool_name


def _task_checkpoint(target: _McpToolTarget, task_id: str) -> str:
    return json.dumps(
        {"mcp_server": target.server.name, "mcp_tool": target.mcp_tool_name, "mcp_task_id": task_id}
    )


def _task_id_from_checkpoint(checkpoint: str | None, target: _McpToolTarget) -> str | None:
    if checkpoint is None:
        return None
    try:
        saved = json.loads(checkpoint)
    except json.JSONDecodeError:
        return None
    if (
        not isinstance(saved, dict)
        or saved.get("mcp_server") != target.server.name
        or saved.get("mcp_tool") != target.mcp_tool_name
    ):
        return None
    task_id = saved.get("mcp_task_id")
    return task_id if isinstance(task_id, str) else None


def _outcome_from_mcp_error(error: MCPError) -> _ToolCallOutcome:
    if error.code in _NO_RESULT_ERROR_CODES:
        return _ToolCallInterrupted(f"MCP error {error.code}: {error.message}")
    return _ToolCallFailed(f"MCP error {error.code}: {error.message}")


def _outcome_from_tool_result(result: CallToolResult) -> _ToolCallOutcome:
    text = render_tool_result_text(result)
    if result.is_error:
        return _ToolCallFailed(text or "the MCP tool reported an error")
    return _ToolCallSucceeded(text)


def _outcome_from_terminal_task(task: McpTaskSnapshot) -> _ToolCallOutcome:
    if task.status == "completed":
        if task.result is None:
            return _ToolCallInterrupted(f"MCP task {task.task_id} completed without a result")
        return _outcome_from_tool_result(CallToolResult.model_validate(task.result))
    if task.status == "failed":
        error = task.error or {}
        message = error.get("message") or task.status_message or "no error detail"
        return _ToolCallFailed(f"MCP task {task.task_id} failed: {message}")
    reason = f": {task.status_message}" if task.status_message else ""
    return _ToolCallFailed(f"MCP task {task.task_id} was cancelled{reason}")


def _task_poll_interval_seconds(task: McpTaskSnapshot) -> float:
    if task.poll_interval_ms is None:
        return _DEFAULT_TASK_POLL_INTERVAL_SECONDS
    return min(
        max(task.poll_interval_ms / 1000, _MINIMUM_TASK_POLL_INTERVAL_SECONDS),
        _MAXIMUM_TASK_POLL_INTERVAL_SECONDS,
    )


def _describe_input_requests(input_requests: Mapping[str, Any]) -> str:
    descriptions: list[str] = []
    for request in input_requests.values():
        payload = (
            request.model_dump(mode="json", by_alias=True)
            if isinstance(request, BaseModel)
            else request
        )
        method = payload.get("method") if isinstance(payload, Mapping) else None
        params = (payload.get("params") if isinstance(payload, Mapping) else None) or {}
        if method == "elicitation/create":
            descriptions.append(f"an answer to {params.get('message')!r}")
        elif method == "sampling/createMessage":
            descriptions.append("a model completion")
        elif method == "roots/list":
            descriptions.append("the client's root directories")
        else:
            descriptions.append(f"a {method or 'server'} request")
    requested = "; ".join(descriptions) or "more input"
    return (
        f"the MCP server asked for input that this agent session cannot provide yet "
        f"({requested}), so the tool did not finish"
    )


def _describe_exception(error: BaseException) -> str:
    if isinstance(error, BaseExceptionGroup):
        return "; ".join(_describe_exception(inner) for inner in error.exceptions)
    text = str(error)
    return f"{type(error).__name__}: {text}" if text else type(error).__name__


def _dex_agents_version() -> str:
    try:
        return importlib.metadata.version("dex-agents")
    except importlib.metadata.PackageNotFoundError:
        return "0"
