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
import hashlib
import importlib.metadata
import json
import logging
import math
import time
from collections.abc import AsyncIterator, Awaitable, Callable, Mapping, Sequence
from contextlib import asynccontextmanager
from dataclasses import dataclass, replace
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
from dex_agents.mcp._dex_conventions import (
    REJECTED_OUTCOME_FALLBACK_MESSAGE,
    UNKNOWN_OUTCOME_FALLBACK_MESSAGE,
    read_dex_error_outcome,
    read_dex_tool_outcome,
)
from dex_agents.mcp._resource_and_prompt_tools import (
    RESOURCE_AND_PROMPT_TOOL_NAMES,
    build_resource_and_prompt_tools,
    read_server_name,
    run_resource_and_prompt_tool,
)
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
    McpTaskUpdateParams,
    PolledMcpTask,
    UpdateMcpTaskRequest,
)
from dex_agents.mcp._tools import McpTool, mcp_tool_from_listing, render_tool_result_text
from dex_agents.runner import (
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    ToolExecutionError,
    ToolExecutionResult,
    ToolInputRequested,
    ToolOutcomeUnknownError,
    ToolPolicy,
    ToolTaskCancellation,
    ToolTaskPending,
    ToolTaskPoll,
    ToolTaskPollOutcome,
    ToolTaskStarted,
)
from dex_agents.stepwise.tool_sources import SourceToolDefinition
from dex_agents.values import AgentToolCall, QuestionAnswer, QuestionResolution, ToolEffect

#: ``_meta`` key that carries the session's idempotency key on every ``tools/call``.
#:
#: The key is ``"<flow_id>/<turn_id>/<tool_call_id>"``: the same on every
#: attempt of one tool call, and unique per call. A server that follows the Dex
#: MCP conventions derives its own call ID from it, so a retried attempt is
#: recognized as the same call.
IDEMPOTENCY_KEY_META_KEY = "io.superdurable.dex/idempotencyKey"

#: ``AgentEvents`` type for an MCP ``notifications/progress`` of a running tool call.
MCP_TOOL_PROGRESS_EVENT_TYPE = "mcp.tool_progress"

#: ``AgentEvents`` type for each observed status of an MCP task.
MCP_TASK_STATUS_EVENT_TYPE = "mcp.task_status"

#: Default bound on one MCP request, such as ``tools/call``.
DEFAULT_MCP_REQUEST_TIMEOUT = timedelta(minutes=10)

#: Default bound on polling one stdio server's MCP task in one tool Step attempt.
DEFAULT_MCP_TASK_WAIT_TIMEOUT = timedelta(minutes=10)

#: Default bound on connecting to a server and each handshake request.
DEFAULT_MCP_CONNECTION_TIMEOUT = timedelta(seconds=30)

#: Default number of connection attempts before a request reports the server unreachable.
DEFAULT_MCP_CONNECTION_ATTEMPTS = 3

#: Default ``McpToolSource.source_name``, which stored Stepwise calls are routed by.
DEFAULT_MCP_SOURCE_NAME = "mcp"

#: Default age after which ``list_tools`` lists a server again, in the background.
DEFAULT_MCP_LISTING_TTL = timedelta(minutes=5)

#: Default wait after a failed listing before ``list_tools`` tries that server again.
DEFAULT_MCP_LISTING_FAILURE_BACKOFF = timedelta(seconds=30)

#: Handler that runs one deferred tool call, as ``execute_deferred_tool_call`` does.
DeferredToolHandler: TypeAlias = Callable[
    [DeferredToolExecution], Awaitable[ToolExecutionResult | ToolTaskStarted]
]

_LOGGER = logging.getLogger(__name__)
_TOOL_LISTING_PAGE_LIMIT = 100
_STATE_ONLY_RETRY_LIMIT = 10
_STATE_ONLY_RETRY_DELAY_SECONDS = 0.1
_CONNECTION_RETRY_DELAY_SECONDS = 0.5
_DEFAULT_TASK_POLL_INTERVAL_SECONDS = 1.0
_MINIMUM_TASK_POLL_INTERVAL_SECONDS = 0.05
_MAXIMUM_TASK_POLL_INTERVAL_SECONDS = 30.0
_TASK_CANCEL_TIMEOUT_SECONDS = 5.0
# A durable poll is a Step: backing off to a minute bounds a long task's history.
_DURABLE_TASK_POLL_BACKOFF_CAP_SECONDS = 60
_MAXIMUM_DURABLE_TASK_POLL_INTERVAL_SECONDS = 3600
# After tasks/update, the answered request may stay listed until the server takes it in. Polls
# within this many seconds of the delivery, one second apart, wait for that; later, a listed
# identical request counts as a new one, and the user is asked again.
_TASK_ANSWER_GRACE_SECONDS = 10
_YES_NO_CHOICES = ("yes", "no")
# UNCLASSIFIED ranks highest: nothing is known, and it runs like a non-idempotent write.
_EFFECT_CONSERVATISM = {
    ToolEffect.READ: 0,
    ToolEffect.IDEMPOTENT_WRITE: 1,
    ToolEffect.NON_IDEMPOTENT_WRITE: 2,
    ToolEffect.UNCLASSIFIED: 3,
}
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
class _ToolCallOutcomeUnknown:
    """The server reported that the call may or may not have taken effect."""

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


@dataclass(frozen=True)
class _TaskStarted:
    """A task that ``PollToolTask`` polls; the tool Step ends."""

    task_id: str
    poll_interval_seconds: int


@dataclass(frozen=True)
class _TaskPending:
    poll_interval_seconds: int
    continuation: str | None = None


@dataclass(frozen=True)
class _TaskAsked:
    question: ToolInputRequested


_ToolCallOutcome: TypeAlias = (
    _ToolCallSucceeded
    | _ToolCallFailed
    | _ToolCallOutcomeUnknown
    | _ToolCallInterrupted
    | _ServerUnreachable
    | _TaskLost
    | _TaskStarted
    | _TaskPending
    | _TaskAsked
)


@dataclass
class _ServerListing:
    """One server's listing state: the last good tools, the last failure, and a running listing."""

    tools: tuple[McpTool, ...] | None = None
    listed_at: float = 0.0
    failure: McpToolListingError | None = None
    failed_until: float = 0.0
    task: asyncio.Task[None] | None = None


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

    A server that follows the Dex MCP conventions declares each tool's effect
    and approval in its definition's ``_meta`` instead, under
    ``TOOL_EFFECT_META_KEY`` and ``TOOL_APPROVAL_META_KEY``, as a connector
    manifest does for ``ConnectorToolSource``. A session ``ToolPolicy`` for
    such a tool may keep or raise its safety but never lowers it: the call
    gets the more conservative of the two effects, and its writes run in the
    model's order. A result or JSON-RPC
    error whose ``_meta`` reports ``TOOL_OUTCOME_UNKNOWN`` records
    ``outcome_unknown`` without a retry, and ``TOOL_OUTCOME_REJECTED``
    records ``failed``. See the package README, "Dex MCP conventions".

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
    * A result or JSON-RPC error whose ``_meta`` reports the Dex outcome
      ``unknown`` raises ``ToolOutcomeUnknownError``, so the session records
      ``outcome_unknown`` and never sends the call again. The outcome
      ``rejected`` raises ``ToolExecutionError``. Either wins over
      ``isError`` and the error code.
    * No result after the request was sent raises
      ``McpToolCallInterruptedError``; the tool Step's effect decides whether
      it is re-issued.
    * A call that was never sent, because its server is not configured or
      cannot be reached, raises ``ToolExecutionError``.
    * A Streamable HTTP server's task handle from the
      ``io.modelcontextprotocol/tasks`` extension becomes ``ToolTaskStarted``:
      the tool Step ends, and ``PollToolTask`` calls ``poll_tool_task`` on a
      Timer, so no Step or connection is held while the task runs. The task ID
      is saved as the attempt's heartbeat checkpoint first, so once that
      checkpoint reaches Dex, a retried attempt resumes the same task instead
      of calling again. A task that needs one answer from the user becomes a
      session question, and its answer, decline, or passed deadline goes back
      with ``tasks/update``. ``cancel_turn`` abandons the task, and
      ``cancel_tool_task`` sends ``tasks/cancel``.
    * A stdio server's task lives in its process, and a runner without
      ``poll_tool_task`` cannot poll one, so then the tool Step polls the task
      with ``tasks/get`` and a heartbeat per poll, for at most
      ``task_wait_timeout`` per attempt.

    With ``resource_and_prompt_tools_enabled``, the listing also offers three
    ``READ`` tools for the servers' resources and prompts:
    ``mcp_list_resources_and_prompts`` lists a server's resources, resource
    templates, and prompts; ``mcp_read_resource`` reads one resource by URI;
    and ``mcp_get_prompt`` gets one prompt with its arguments. Each takes the
    configured server's name as its ``server`` argument and opens a new
    connection to that server with the same transport and headers as a tool
    call. Its output is the server's answer as JSON with sorted keys:
    ``{"resources", "resource_templates", "prompts"}``, ``{"contents"}``, or
    ``{"messages", "description"}``. A server that does not offer resources
    or prompts lists none of them. An MCP error answer raises
    ``ToolExecutionError``.

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
        ...
        ...     async def poll_tool_task(self, poll):
        ...         return await tools.poll_tool_task(poll)
        ...
        ...     async def cancel_tool_task(self, cancellation):
        ...         await tools.cancel_tool_task(cancellation)
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
        resource_and_prompt_tools_enabled: bool = False,
        listing_ttl: timedelta = DEFAULT_MCP_LISTING_TTL,
        listing_failure_backoff: timedelta = DEFAULT_MCP_LISTING_FAILURE_BACKOFF,
        source_name: str | None = None,
    ) -> None:
        """Describe the servers without connecting to them.

        Args:
            servers: Server configurations with unique names.
            request_timeout: Bound on one ``tools/call`` or ``tasks/get``
                request. A ``tools/call`` that times out raises
                ``McpToolCallInterruptedError``. Defaults to 10 minutes. Keep
                it below the tool Step's two-hour method timeout.
            task_wait_timeout: Bound on polling one stdio server's MCP task
                in one tool Step attempt, and any task that ``call_tool``
                polls. When it passes, the source raises
                ``McpToolCallInterruptedError`` and leaves the task running.
                Defaults to 10 minutes. ``PollToolTask`` polls a Streamable
                HTTP server's task without this bound.
            connection_timeout: Bound on connecting to a server and on each
                handshake and ``tools/list`` request. Defaults to 30 seconds.
            connection_attempts: Connection attempts per request before the
                server counts as unreachable. Defaults to 3.
            client_name: ``clientInfo.name`` sent to the servers.
            resource_and_prompt_tools_enabled: Whether ``list_tools`` also
                offers ``mcp_list_resources_and_prompts``,
                ``mcp_read_resource``, and ``mcp_get_prompt``, after the
                servers' tools, when at least one server is configured. Their
                requests are bounded by ``connection_timeout``. Defaults to
                ``False``. Enable it on one source of an agent only: two
                sources with it list the same names, which
                ``AgentDefinition`` refuses.
            listing_ttl: Age after which ``list_tools`` lists a server again.
                The refresh runs in the background, and the earlier listing
                is returned meanwhile. Defaults to 5 minutes.
            listing_failure_backoff: How long ``list_tools`` leaves a server
                that failed to list alone before it tries again, in the
                background. Defaults to 30 seconds.
            source_name: The source's stable name in
                ``AgentDefinition.tool_sources``, which stored calls are
                routed by. ``None``, the default, uses ``"mcp"``, so adding or
                removing a server keeps it. An agent with two ``McpToolSource``
                instances must name at least one, because names must be unique.

        Raises:
            ValueError: If server names repeat, a timeout, ``listing_ttl``, or
                ``listing_failure_backoff`` is not positive, or
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
        if listing_ttl <= timedelta(0) or listing_failure_backoff <= timedelta(0):
            raise ValueError("listing_ttl and listing_failure_backoff must be positive")
        self._listing_ttl_seconds = listing_ttl.total_seconds()
        self._listing_failure_backoff_seconds = listing_failure_backoff.total_seconds()
        self._request_timeout_seconds = request_timeout.total_seconds()
        self._task_wait_timeout = task_wait_timeout
        self._connection_timeout_seconds = connection_timeout.total_seconds()
        self._connection_attempts = connection_attempts
        self._client_info = Implementation(name=client_name, version=_dex_agents_version())
        self._listings = {name: _ServerListing() for name in self._servers}
        self._source_name = source_name if source_name is not None else DEFAULT_MCP_SOURCE_NAME
        self._tools: dict[str, McpTool] | None = None
        self._resource_and_prompt_tools = (
            build_resource_and_prompt_tools(list(self._servers))
            if resource_and_prompt_tools_enabled and self._servers
            else ()
        )

    async def list_tools(self, *, refresh: bool = False) -> tuple[McpTool, ...]:
        """Return the tools of every reachable configured server.

        Each server is listed on its own and cached per Worker process. The
        first listing of a server is awaited. After that, a listing older
        than ``listing_ttl`` is refreshed in the background while the earlier
        one is returned. A server that cannot be listed is left out with a
        warning log and is not tried again until ``listing_failure_backoff``
        has passed; then it is listed in the background. One listing per
        server runs at a time, however many sessions call this. ``refresh``
        lists every server again now and waits for it. When two tools map to
        the same agent tool name, the first one in server order wins and the
        other is left out with a warning log. Call it from ``run_model_step``
        to offer the tools, or at Worker startup to warm the cache.

        Args:
            refresh: Whether to list every server again now, ignoring the
                cache and the failure backoff.

        Returns:
            Every offered tool, in server order and then in each server's order,
            then the resource and prompt tools when enabled.

        Raises:
            McpToolListingError: If servers are configured and none has a
                listing, because every one failed.
        """
        now = time.monotonic()
        running_loop = asyncio.get_running_loop()
        waits: list[asyncio.Task[None]] = []
        for server in self._servers.values():
            listing = self._listings[server.name]
            if listing.task is not None and listing.task.get_loop() is not running_loop:
                listing.task = None
            if listing.task is None:
                is_stale = (
                    listing.tools is None or now - listing.listed_at >= self._listing_ttl_seconds
                )
                if refresh or (is_stale and now >= listing.failed_until):
                    listing.task = running_loop.create_task(self._refresh_listing(server, listing))
            is_first_listing = listing.tools is None and listing.failure is None
            if listing.task is not None and (refresh or is_first_listing):
                waits.append(listing.task)
        if waits:
            await asyncio.wait(waits)
        if self._servers and all(listing.tools is None for listing in self._listings.values()):
            failures = [listing.failure for listing in self._listings.values() if listing.failure]
            if failures:
                raise failures[-1]
            raise McpToolListingError(next(iter(self._servers)), "no listing finished yet")
        if self._tools is None:
            self._tools = self._combine_server_tools()
        return tuple(self._tools.values())

    @property
    def source_name(self) -> str:
        """Return the name ``AgentDefinition.tool_sources`` routes stored calls by.

        Returns:
            ``source_name`` as passed, or ``DEFAULT_MCP_SOURCE_NAME``, ``"mcp"``.
        """
        return self._source_name

    def cached_tools(self) -> tuple[McpTool, ...]:
        """Return the tools of the last listings without listing, for estimates.

        Returns:
            The cached tools, as ``list_tools`` would return them, or none
            before any listing finished.
        """
        return () if self._tools is None else tuple(self._tools.values())

    def declared_tool_names(self) -> frozenset[str]:
        """Return the tool names this source lists whatever its servers offer.

        ``AgentDefinition`` reads it to refuse a name collision when it is
        built, instead of on every model call.

        Returns:
            The resource and prompt tool names when they are enabled, else none.
        """
        return frozenset(tool.name for tool in self._resource_and_prompt_tools)

    async def _refresh_listing(self, server: McpServerConfig, listing: _ServerListing) -> None:
        try:
            tools = tuple(await self._list_server_tools(server))
        except Exception as error:
            # A background listing must never fail: nobody may be waiting for it.
            failure = (
                error
                if isinstance(error, McpToolListingError)
                else McpToolListingError(server.name, _describe_exception(error))
            )
            listing.failure = failure
            listing.failed_until = time.monotonic() + self._listing_failure_backoff_seconds
            _LOGGER.warning("left MCP server %r out of the tool listing: %s", server.name, failure)
        else:
            listing.tools = tools
            listing.listed_at = time.monotonic()
            listing.failure = None
            listing.failed_until = 0.0
            self._tools = self._combine_server_tools()
        finally:
            listing.task = None

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
        """Return a ``ToolPolicy`` for every listed tool whose server declares an effect.

        Use it with a framework adapter that offers the MCP tools as its own
        tools: pass it in the adapter's ``tool_policies`` and put your own
        policies after it, so yours win. A tool with a ``dex_effect`` gets that
        effect and requires approval exactly when ``is_approval_recommended``;
        a policy you put after it replaces both. Unlike in
        ``deferred_tool_call``, such a policy can lower the declared effect,
        so keep yours at least as conservative as ``dex_effect``.

        Example:
            >>> await tools.list_tools()
            >>> tool_policies = {**tools.tool_policies(), "files__delete": ToolPolicy(
            ...     ToolEffect.NON_IDEMPOTENT_WRITE)}

        Returns:
            Policies by ``McpTool.name``: the declared approval for a tool
            with a ``dex_effect``, else the effect's default approval.

        Raises:
            LookupError: If ``list_tools`` has not run.
        """
        if self._tools is None:
            raise LookupError("call list_tools before reading MCP tool policies")
        policies: dict[str, ToolPolicy] = {}
        for tool in self._tools.values():
            if tool.dex_effect is not None:
                policies[tool.name] = ToolPolicy(tool.dex_effect, tool.is_approval_recommended)
            elif tool.framework_effect is not None:
                policies[tool.name] = ToolPolicy(tool.framework_effect)
        return policies

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition | None = None,
    ) -> DeferredToolCall:
        """Build the ``DeferredToolCall`` for one model call of a listed MCP tool.

        The policy comes from ``turn.resolve_tool_policy`` with the tool's
        annotation effect, so an explicit ``ToolPolicy`` wins. The call's
        ``tool_source_reference`` names the server and the tool's MCP name.

        For a tool with a ``dex_effect``, the session's ``ToolPolicy`` for
        the tool may keep or raise its safety but never lowers it. The call
        gets the more conservative of ``dex_effect`` and the policy's effect,
        ranked ``READ``, ``IDEMPOTENT_WRITE``, ``NON_IDEMPOTENT_WRITE``, then
        ``UNCLASSIFIED``. It waits for approval when the policy sets
        ``requires_approval``. When the policy leaves ``requires_approval``
        ``None``, the call waits when ``is_approval_recommended`` is ``True``
        or the policy's effect requires approval by default. Without a
        policy, it waits exactly when ``is_approval_recommended`` is
        ``True``. A write runs in the model's order after the turn's earlier
        writes (``DeferredToolCall.ordered``).

        .. versionchanged:: 0.5.0
            For a tool with a ``dex_effect``, a ``ToolPolicy`` may raise the
            effect or the approval but never lowers the effect.

        Args:
            turn: The ``RunModel`` context.
            tool_call_id: Framework tool call ID.
            tool_name: ``McpTool.name`` the model called.
            arguments: Tool arguments as JSON text.
            listed_tool: The ``McpTool`` that the listing offered for this
                model call, as the Stepwise runner passes it, so a refresh
                that finished meanwhile cannot change or remove it. ``None``,
                the default, looks the tool up in the current listing.

        Returns:
            The call to put in ``ToolCallsRequested.tool_calls``.

        Raises:
            LookupError: If no ``listed_tool`` is given and ``list_tools`` has
                not run or the tool is unknown.
        """
        tool = (
            listed_tool
            if isinstance(listed_tool, McpTool) and listed_tool.name == tool_name
            else self.tool(tool_name)
        )
        tool_source_reference = (
            None
            if self._is_resource_and_prompt_tool(tool_name)
            else json.dumps({"mcp_server": tool.server_name, "mcp_tool": tool.mcp_tool_name})
        )
        if tool.dex_effect is None:
            return DeferredToolCall(
                tool_call_id=tool_call_id,
                tool_name=tool_name,
                arguments=arguments,
                policy=turn.resolve_tool_policy(tool_name, tool.framework_effect),
                tool_source_reference=tool_source_reference,
            )
        policy = _combine_declared_tool_policy(
            tool.dex_effect, tool.is_approval_recommended, turn.tool_policies.get(tool_name)
        )
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=policy,
            tool_source_reference=tool_source_reference,
            ordered=policy.effect is not ToolEffect.READ,
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

    async def execute_deferred_tool_call(
        self, call: DeferredToolExecution
    ) -> ToolExecutionResult | ToolTaskStarted:
        """Run one deferred MCP tool call in its tool Step.

        Call it from ``AgentFrameworkRunner.execute_deferred_tool_call``, and
        implement ``ToolTaskPoller`` with ``poll_tool_task``. A call built by
        ``deferred_tool_call`` goes straight to the server named on its
        record, without a listing. It passes the call's idempotency key,
        events, heartbeat, and heartbeat checkpoint on.

        A Streamable HTTP server that answers with a task handle gets
        ``ToolTaskStarted``. The source saves the task ID as the heartbeat
        checkpoint first; a retried attempt that reads it returns the same
        task without calling the tool again.

        Args:
            call: The approved call from the tool Step.

        Returns:
            The tool output the model sees, or ``ToolTaskStarted`` for a
            Streamable HTTP server's task.

        Raises:
            ToolExecutionError: The arguments are not a JSON object, the tool
                failed, the server answered with an error or asked for input,
                or the call was never sent.
            ToolOutcomeUnknownError: The server reported the Dex outcome
                ``unknown``. The session records ``outcome_unknown``.
            McpToolCallInterruptedError: The call was sent and no result came back.
        """
        record = call.tool_call
        arguments = _parse_tool_arguments(record.arguments)
        if self._is_resource_and_prompt_tool(record.tool_name):
            return await self._run_resource_and_prompt_tool(record.tool_name, arguments)
        target = await self._resolve_target(record)
        # Without a poller, a task runs to its end inside the call, as for call_tool.
        is_task_polled_by_session = call.supports_tool_tasks and isinstance(
            target.server, McpStreamableHttpServerConfig
        )
        resumed_task_id = _task_id_from_checkpoint(call.heartbeat_checkpoint, target)
        if is_task_polled_by_session and resumed_task_id is not None:
            # An earlier attempt started this task; PollToolTask finds out how it went.
            return ToolTaskStarted(task_id=resumed_task_id, poll_interval_seconds=1)
        return await self._call_target(
            target,
            arguments,
            idempotency_key=call.idempotency_key,
            tool_call_id=record.tool_call_id,
            events=call.events,
            heartbeat=call.heartbeat,
            heartbeat_checkpoint=call.heartbeat_checkpoint,
            save_heartbeat_checkpoint=call.save_heartbeat_checkpoint,
            is_task_polled_by_session=is_task_polled_by_session,
        )

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        """Poll one MCP task that ``execute_deferred_tool_call`` started, once.

        Call it from ``ToolTaskPoller.poll_tool_task``. It reads the task with
        ``tasks/get`` and writes an ``mcp.task_status`` event when the status
        changed. A completed task is the tool's result. A task in
        ``input_required`` that asks one form elicitation with a single
        string, ``enum``, or boolean field becomes ``ToolInputRequested``,
        whose continuation names the request by key and fingerprint. A boolean
        field offers ``yes`` and ``no``. The poll that gets
        ``poll.pending_answer`` sends it with ``tasks/update`` on its first
        attempt, after a heartbeat, only while the task still lists that
        request: ``accept`` with the answer, ``decline``, or ``cancel`` for a
        passed deadline. A retried attempt cannot tell whether an earlier one
        sent it, so it asks the user again instead. For 10 seconds after the
        send, polls a second apart read a still-listed answered request as
        working; after that, or once the task left ``input_required``, an
        identical request is a new question. A task that asks for anything
        else is cancelled and fails.

        Args:
            poll: The call's record from ``PollToolTask``.

        Returns:
            The tool output; ``ToolTaskPending`` with the next poll interval,
            which backs off from the server's ``pollIntervalMs`` to at most a
            minute; or the task's question.

        Raises:
            ToolExecutionError: The task failed, was cancelled, returned an
                ``isError`` result, or asked for input the session cannot give.
            ToolOutcomeUnknownError: The task's result reported the Dex
                outcome ``unknown``. The session records ``outcome_unknown``.
            McpToolCallInterruptedError: The server could not be reached, no
                longer knows the task, or answered a poll with an error. The
                task may still run, so ``PollToolTask`` retries and then
                records the call by its effect.
        """
        record = poll.tool_call
        if record.task_id is None:
            raise ValueError(f"tool call {record.tool_call_id} has no MCP task")
        task_id = record.task_id
        target = await self._resolve_target(record)
        call = _McpToolCall(
            source=self,
            target=target,
            arguments={},
            meta={},
            reporter=_McpToolCallReporter(target.tool_name, record.tool_call_id, poll.events),
            heartbeat=poll.heartbeat,
            save_heartbeat_checkpoint=None,
        )
        outcome = await call.with_connection(
            lambda client: call.poll_task_once(client, task_id, record, poll)
        )
        if isinstance(outcome, _ToolCallSucceeded):
            return ToolExecutionResult(output=outcome.output)
        if isinstance(outcome, _ToolCallFailed):
            raise ToolExecutionError(outcome.message)
        if isinstance(outcome, _ToolCallOutcomeUnknown):
            raise ToolOutcomeUnknownError(outcome.message)
        if isinstance(outcome, _TaskPending):
            return ToolTaskPending(
                poll_interval_seconds=outcome.poll_interval_seconds,
                continuation=outcome.continuation,
            )
        if isinstance(outcome, _TaskAsked):
            return outcome.question
        detail = getattr(outcome, "detail", f"unexpected {type(outcome).__name__}")
        raise McpToolCallInterruptedError(
            target.server.name, target.mcp_tool_name, f"polling MCP task {task_id}: {detail}"
        )

    async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        """Send ``tasks/cancel`` for one MCP task that ``cancel_turn`` abandoned.

        Call it from ``ToolTaskCanceller.cancel_tool_task``. Cancellation is
        best-effort: the server may finish the task anyway, and the session
        drops a result that arrives later. Any answer from the server ends the
        cancellation, including an error for a task that already finished or
        that the server no longer knows, so a repeated call is harmless.

        Args:
            cancellation: The abandoned call's record from ``CancelToolTask``.

        Raises:
            McpToolCallInterruptedError: The server could not be reached or did
                not answer. ``CancelToolTask`` retries, then records the call
                ``outcome_unknown`` anyway.
        """
        record = cancellation.tool_call
        if record.task_id is None:
            raise ValueError(f"tool call {record.tool_call_id} has no MCP task")
        task_id = record.task_id
        target = await self._resolve_target(record)
        call = _McpToolCall(
            source=self,
            target=target,
            arguments={},
            meta={},
            reporter=_McpToolCallReporter(target.tool_name, record.tool_call_id, None),
            heartbeat=cancellation.heartbeat,
            save_heartbeat_checkpoint=None,
        )
        outcome = await call.with_connection(lambda client: _cancel_task(client, task_id))
        if not isinstance(outcome, _ToolCallSucceeded):
            detail = getattr(outcome, "detail", f"unexpected {type(outcome).__name__}")
            raise McpToolCallInterruptedError(
                target.server.name, target.mcp_tool_name, f"cancelling MCP task {task_id}: {detail}"
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
            The tool output the model sees. A task is polled inside the call,
            for at most ``task_wait_timeout``.

        Raises:
            ToolExecutionError: The tool failed, the server answered with an
                error or asked for input, or the call was never sent.
            ToolOutcomeUnknownError: The server reported the Dex outcome
                ``unknown``.
            McpToolCallInterruptedError: The call was sent and no result came back.
        """
        if self._is_resource_and_prompt_tool(tool_name):
            return await self._run_resource_and_prompt_tool(tool_name, arguments)
        result = await self._call_target(
            await self._target_from_listing(tool_name),
            dict(arguments),
            idempotency_key=idempotency_key,
            tool_call_id=tool_call_id,
            events=events,
            heartbeat=heartbeat,
            heartbeat_checkpoint=heartbeat_checkpoint,
            save_heartbeat_checkpoint=save_heartbeat_checkpoint,
            is_task_polled_by_session=False,
        )
        if not isinstance(result, ToolExecutionResult):
            raise RuntimeError(f"MCP tool {tool_name} returned a task to call_tool")
        return result

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
        is_task_polled_by_session: bool,
    ) -> ToolExecutionResult | ToolTaskStarted:
        call = _McpToolCall(
            source=self,
            target=target,
            arguments=arguments,
            meta={} if idempotency_key is None else {IDEMPOTENCY_KEY_META_KEY: idempotency_key},
            reporter=_McpToolCallReporter(target.tool_name, tool_call_id, events),
            heartbeat=heartbeat,
            save_heartbeat_checkpoint=save_heartbeat_checkpoint,
            is_task_polled_by_session=is_task_polled_by_session,
        )
        resumed_task_id = _task_id_from_checkpoint(heartbeat_checkpoint, target)
        outcome = await call.run(resumed_task_id)
        if isinstance(outcome, _ToolCallSucceeded):
            return ToolExecutionResult(output=outcome.output)
        if isinstance(outcome, _TaskStarted):
            return ToolTaskStarted(
                task_id=outcome.task_id, poll_interval_seconds=outcome.poll_interval_seconds
            )
        if isinstance(outcome, _ToolCallFailed):
            raise ToolExecutionError(outcome.message)
        if isinstance(outcome, _ToolCallOutcomeUnknown):
            raise ToolOutcomeUnknownError(outcome.message)
        if isinstance(outcome, _ToolCallInterrupted | _TaskLost):
            raise McpToolCallInterruptedError(
                target.server.name, target.mcp_tool_name, outcome.detail
            )
        if isinstance(outcome, _ServerUnreachable):
            raise ToolExecutionError(
                f"could not connect to MCP server {target.server.name!r}, so "
                f"{target.mcp_tool_name} did not run: {outcome.detail}"
            )
        raise McpToolCallInterruptedError(
            target.server.name,
            target.mcp_tool_name,
            f"the tool call ended with an unexpected {type(outcome).__name__}",
        )

    def _is_resource_and_prompt_tool(self, tool_name: str) -> bool:
        return bool(self._resource_and_prompt_tools) and tool_name in RESOURCE_AND_PROMPT_TOOL_NAMES

    async def _run_resource_and_prompt_tool(
        self, tool_name: str, arguments: Mapping[str, Any]
    ) -> ToolExecutionResult:
        server_name = read_server_name(arguments)
        server = self._servers.get(server_name)
        if server is None:
            raise ToolExecutionError(f"MCP server {server_name} is not configured")
        detail = "no connection attempt"
        for connection_attempt in range(1, self._connection_attempts + 1):
            outcome: str | ToolExecutionError | None = None
            try:
                async with self._new_client(server) as client:
                    try:
                        outcome = await run_resource_and_prompt_tool(client, tool_name, arguments)
                    except ToolExecutionError as error:
                        outcome = error
            except Exception as error:
                # Only closing the connection failed when the outcome is already known.
                if outcome is None:
                    detail = _describe_exception(error)
            if isinstance(outcome, ToolExecutionError):
                raise outcome
            if outcome is not None:
                return ToolExecutionResult(output=outcome)
            if connection_attempt < self._connection_attempts:
                await asyncio.sleep(_CONNECTION_RETRY_DELAY_SECONDS * connection_attempt)
        raise ToolExecutionError(
            f"could not read from MCP server {server_name}, so {tool_name} did not finish: "
            f"{detail}"
        )

    async def _resolve_target(self, record: AgentToolCall) -> _McpToolTarget:
        target = self._target_from_reference(record.tool_name, record.tool_source_reference)
        if target is None:
            target = await self._target_from_listing(record.tool_name)
        return target

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
            for tool in self._listings[server_name].tools or ():
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
        for tool in self._resource_and_prompt_tools:
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
        is_task_polled_by_session: bool = False,
    ) -> None:
        self._source = source
        self._target = target
        self._arguments = arguments
        self._meta = meta
        self._reporter = reporter
        self._heartbeat = heartbeat
        self._save_heartbeat_checkpoint = save_heartbeat_checkpoint
        self._is_task_polled_by_session = is_task_polled_by_session

    async def run(self, resumed_task_id: str | None) -> _ToolCallOutcome:
        if resumed_task_id is not None:
            outcome = await self.with_connection(
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
        return await self.with_connection(self._send_tool_call)

    async def with_connection(
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
                if self._is_task_polled_by_session:
                    return self._start_session_polling(result)
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

    def _start_session_polling(self, created: CreatedMcpTask) -> _ToolCallOutcome:
        self._reporter.write_task_status(created)
        if created.status in TERMINAL_TASK_STATUSES:
            return _outcome_from_terminal_task(created)
        return _TaskStarted(created.task_id, _durable_task_poll_interval_seconds(created, None))

    async def poll_task_once(
        self, client: Client, task_id: str, record: AgentToolCall, poll: ToolTaskPoll
    ) -> _ToolCallOutcome:
        snapshot = await self._get_task(client, task_id)
        if isinstance(snapshot, _ToolCallInterrupted):
            return snapshot
        answered_request = _answered_task_request(record.input_continuation, task_id)
        if poll.pending_answer is not None and answered_request is not None:
            # A retried attempt cannot tell whether an earlier one delivered the answer, and the
            # task may have asked an identical request since; asking again never misdelivers.
            if poll.attempt == 1 and _lists_request(snapshot, answered_request):
                failure = await self._deliver_resolution(
                    client, task_id, answered_request, poll.pending_answer
                )
                if failure is not None:
                    return failure
                answered_request = replace(answered_request, delivered_at=time.time())
                snapshot = await self._get_task(client, task_id)
                if isinstance(snapshot, _ToolCallInterrupted):
                    return snapshot
            else:
                answered_request = None
        # The task was input_required when this poll took the pending answer.
        if poll.pending_answer is not None or snapshot.status != "working":
            self._reporter.write_task_status(snapshot)
        if (
            poll.pending_answer is not None
            and poll.pending_answer.resolution is not QuestionResolution.ANSWERED
            and snapshot.status == "cancelled"
        ):
            # A cancelled task may have taken effect before it asked; record the call by effect.
            return _ToolCallInterrupted(
                f"MCP task {task_id} was cancelled after its question was declined"
            )
        if snapshot.status in TERMINAL_TASK_STATUSES:
            return _outcome_from_terminal_task(snapshot)
        interval = _durable_task_poll_interval_seconds(snapshot, record.task_poll_interval_seconds)
        if snapshot.status != "input_required":
            # The task left input_required, so a later identical request is a new question.
            return _TaskPending(interval)
        input_requests = snapshot.input_requests or {}
        if answered_request is not None and answered_request.is_still_being_taken(input_requests):
            # The server has not taken the answer in yet; asking now would repeat the question.
            return _TaskPending(1, answered_request.continuation(task_id))
        question = _task_question(task_id, input_requests)
        if question is None:
            await _cancel_task_quietly(client, task_id)
            return _ToolCallFailed(_describe_input_requests(input_requests))
        return _TaskAsked(question)

    async def _get_task(self, client: Client, task_id: str) -> PolledMcpTask | _ToolCallInterrupted:
        try:
            return await client.session.send_request(
                GetMcpTaskRequest(params=McpTaskIdParams(task_id=task_id)),
                PolledMcpTask,
                request_read_timeout_seconds=self._source._request_timeout_seconds,
            )
        except MCPError as error:
            # The task may still run, so a failed poll leaves the outcome unknown.
            return _ToolCallInterrupted(f"tasks/get for MCP task {task_id} failed: {error}")

    async def _deliver_resolution(
        self,
        client: Client,
        task_id: str,
        answered_request: _AnsweredTaskRequest,
        resolution: QuestionAnswer,
    ) -> _ToolCallOutcome | None:
        """Send a question's resolution with ``tasks/update``; return an outcome only on failure."""
        if self._heartbeat is not None:
            # Output before the send, so a later half-close fails the attempt instead of
            # returning it as not run.
            await self._heartbeat()
        try:
            await client.session.send_request(
                UpdateMcpTaskRequest(
                    params=McpTaskUpdateParams(
                        task_id=task_id,
                        input_responses={
                            answered_request.input_key: answered_request.input_response(resolution)
                        },
                    )
                ),
                Result,
                request_read_timeout_seconds=self._source._request_timeout_seconds,
            )
        except MCPError as error:
            if resolution.resolution is not QuestionResolution.ANSWERED:
                # The task cannot take the decline; stop it, and let the call be recorded by effect.
                await _cancel_task_quietly(client, task_id)
            return _ToolCallInterrupted(f"tasks/update for MCP task {task_id} failed: {error}")
        return None

    async def _await_task(
        self, client: Client, task_id: str, created: CreatedMcpTask | None
    ) -> _ToolCallOutcome:
        loop = asyncio.get_running_loop()
        task_wait_timeout = self._source._task_wait_timeout
        deadline = loop.time() + task_wait_timeout.total_seconds()
        snapshot: McpTaskSnapshot | None = created
        reported_status: str | None = None
        while True:
            if snapshot is not None:
                if snapshot.status != reported_status:
                    self._reporter.write_task_status(snapshot)
                    reported_status = snapshot.status
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


async def _cancel_task(client: Client, task_id: str) -> _ToolCallOutcome:
    try:
        async with asyncio.timeout(_TASK_CANCEL_TIMEOUT_SECONDS):
            await client.session.send_request(
                CancelMcpTaskRequest(params=McpTaskIdParams(task_id=task_id)), Result
            )
    except MCPError as error:
        if error.code in _NO_RESULT_ERROR_CODES:
            return _ToolCallInterrupted(f"tasks/cancel got no answer: {_describe_exception(error)}")
        # The server answered, as for a finished or unknown task; asking again cannot help.
        _LOGGER.info("tasks/cancel for MCP task %s answered %s", task_id, error)
    except TimeoutError:
        return _ToolCallInterrupted(
            f"tasks/cancel got no answer in {_TASK_CANCEL_TIMEOUT_SECONDS}s"
        )
    return _ToolCallSucceeded("")


async def _cancel_task_quietly(client: Client, task_id: str) -> None:
    try:
        async with asyncio.timeout(_TASK_CANCEL_TIMEOUT_SECONDS):
            await client.session.send_request(
                CancelMcpTaskRequest(params=McpTaskIdParams(task_id=task_id)), Result
            )
    except Exception as error:
        # Cancellation is cooperative; the tool's outcome is already reported either way.
        _LOGGER.warning("tasks/cancel for MCP task %s failed: %s", task_id, error)


def _combine_declared_tool_policy(
    declared_effect: ToolEffect, is_approval_recommended: bool, session_policy: ToolPolicy | None
) -> ToolPolicy:
    """Return a declared tool's policy; a session policy may raise its safety, never lower it."""
    if session_policy is None:
        return ToolPolicy(declared_effect, is_approval_recommended)
    effect = max(declared_effect, session_policy.effect, key=_EFFECT_CONSERVATISM.__getitem__)
    requires_approval = (
        session_policy.requires_approval
        if session_policy.requires_approval is not None
        else is_approval_recommended or session_policy.is_approval_required
    )
    return ToolPolicy(effect, requires_approval)


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
    message = f"MCP error {error.code}: {error.message}"
    # A Dex-aware server's outcome is known, even for an error code that usually leaves it open.
    dex_outcome = read_dex_error_outcome(error.data)
    if dex_outcome == "unknown":
        return _ToolCallOutcomeUnknown(message)
    if dex_outcome == "rejected":
        return _ToolCallFailed(message)
    if error.code in _NO_RESULT_ERROR_CODES:
        return _ToolCallInterrupted(message)
    return _ToolCallFailed(message)


def _outcome_from_tool_result(result: CallToolResult) -> _ToolCallOutcome:
    text = render_tool_result_text(result)
    # The Dex outcome wins over isError in both directions.
    dex_outcome = read_dex_tool_outcome(result.meta)
    if dex_outcome == "unknown":
        return _ToolCallOutcomeUnknown(text or UNKNOWN_OUTCOME_FALLBACK_MESSAGE)
    if dex_outcome == "rejected":
        return _ToolCallFailed(text or REJECTED_OUTCOME_FALLBACK_MESSAGE)
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


def _durable_task_poll_interval_seconds(task: McpTaskSnapshot, previous_seconds: int | None) -> int:
    hint_seconds = 1 if task.poll_interval_ms is None else math.ceil(task.poll_interval_ms / 1000)
    backoff_seconds = (
        1
        if previous_seconds is None
        else min(previous_seconds * 2, _DURABLE_TASK_POLL_BACKOFF_CAP_SECONDS)
    )
    return min(max(1, hint_seconds, backoff_seconds), _MAXIMUM_DURABLE_TASK_POLL_INTERVAL_SECONDS)


@dataclass(frozen=True)
class _AnsweredTaskRequest:
    """The input request a task's question came from, read back from its continuation."""

    input_key: str
    fingerprint: str
    field_name: str
    field_type: str
    delivered_at: float | None = None
    """Worker clock seconds when ``tasks/update`` delivered its resolution, or ``None``."""

    def is_listed_in(self, input_requests: Mapping[str, Any]) -> bool:
        # The same key may carry a later request; only the same request counts.
        request = input_requests.get(self.input_key)
        return request is not None and _input_request_fingerprint(request) == self.fingerprint

    def is_still_being_taken(self, input_requests: Mapping[str, Any]) -> bool:
        """Report whether a listed identical request is most likely the one just answered."""
        return (
            self.delivered_at is not None
            and time.time() - self.delivered_at < _TASK_ANSWER_GRACE_SECONDS
            and self.is_listed_in(input_requests)
        )

    def continuation(self, task_id: str) -> str:
        return json.dumps(
            {
                "mcp_task_id": task_id,
                "mcp_input_key": self.input_key,
                "mcp_input_fingerprint": self.fingerprint,
                "mcp_input_field": self.field_name,
                "mcp_input_type": self.field_type,
                "mcp_delivered_at": self.delivered_at,
            }
        )

    def input_response(self, resolution: QuestionAnswer) -> dict[str, Any]:
        if resolution.resolution is QuestionResolution.DECLINED:
            return {"action": "decline"}
        if resolution.resolution is QuestionResolution.DEADLINE_PASSED:
            return {"action": "cancel"}
        value: str | bool = (
            resolution.answer == "yes" if self.field_type == "boolean" else resolution.answer
        )
        return {"action": "accept", "content": {self.field_name: value}}


def _task_question(task_id: str, input_requests: Mapping[str, Any]) -> ToolInputRequested | None:
    """Return the question of one form elicitation with one string or boolean field, or ``None``."""
    if len(input_requests) != 1:
        return None
    ((input_key, request),) = input_requests.items()
    payload = (
        request.model_dump(mode="json", by_alias=True)
        if isinstance(request, BaseModel)
        else request
    )
    if not isinstance(payload, Mapping) or payload.get("method") != "elicitation/create":
        return None
    params = payload.get("params")
    if not isinstance(params, Mapping) or params.get("mode", "form") != "form":
        return None
    message = params.get("message")
    schema = params.get("requestedSchema")
    properties = schema.get("properties") if isinstance(schema, Mapping) else None
    if not isinstance(message, str) or not message.strip() or not isinstance(properties, Mapping):
        return None
    if len(properties) != 1:
        return None
    ((field_name, field_schema),) = properties.items()
    field_type = field_schema.get("type") if isinstance(field_schema, Mapping) else None
    choices: list[str] = []
    if field_type == "boolean":
        choices = list(_YES_NO_CHOICES)
    elif field_type == "string":
        enum = field_schema.get("enum")
        if isinstance(enum, list) and enum and all(isinstance(value, str) for value in enum):
            choices = list(enum)
    else:
        return None
    return ToolInputRequested(
        question=message,
        choices=choices,
        allows_free_text=not choices,
        continuation=json.dumps(
            {
                "mcp_task_id": task_id,
                "mcp_input_key": input_key,
                "mcp_input_fingerprint": _input_request_fingerprint(payload),
                "mcp_input_field": field_name,
                "mcp_input_type": field_type,
            }
        ),
    )


def _answered_task_request(continuation: str | None, task_id: str) -> _AnsweredTaskRequest | None:
    """Return the request a continuation of this task names; another writer's text reads as none."""
    if continuation is None:
        return None
    try:
        saved = json.loads(continuation)
    except json.JSONDecodeError:
        return None
    if not isinstance(saved, dict) or saved.get("mcp_task_id") != task_id:
        return None
    fields = [
        saved.get(name)
        for name in ("mcp_input_key", "mcp_input_fingerprint", "mcp_input_field", "mcp_input_type")
    ]
    if not all(isinstance(value, str) for value in fields):
        return None
    input_key, fingerprint, field_name, field_type = (str(value) for value in fields)
    delivered_at = saved.get("mcp_delivered_at")
    if isinstance(delivered_at, bool) or not isinstance(delivered_at, int | float):
        delivered_at = None
    return _AnsweredTaskRequest(input_key, fingerprint, field_name, field_type, delivered_at)


def _lists_request(snapshot: PolledMcpTask, answered_request: _AnsweredTaskRequest) -> bool:
    return snapshot.status == "input_required" and answered_request.is_listed_in(
        snapshot.input_requests or {}
    )


def _input_request_fingerprint(request: Any) -> str:
    """Return a digest of one input request, so a later request under the same key differs."""
    payload = (
        request.model_dump(mode="json", by_alias=True)
        if isinstance(request, BaseModel)
        else request
    )
    canonical = json.dumps(payload, sort_keys=True, separators=(",", ":"), default=str)
    return hashlib.sha256(canonical.encode()).hexdigest()


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
