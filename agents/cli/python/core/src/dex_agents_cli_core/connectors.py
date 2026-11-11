# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Connector tools from the connectors MCP server, filtered by the configuration."""

from __future__ import annotations

import json
from collections.abc import Callable, Mapping, Sequence
from dataclasses import asdict, dataclass, field, replace
from typing import Any

import httpx2
from mcp import Client, Implementation

from dex_agents import (
    AgentToolCall,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    MediaStore,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    ToolTaskCancellation,
    ToolTaskPoll,
    ToolTaskPollOutcome,
    ToolTaskStarted,
)
from dex_agents.builtin_loop import SourceToolDefinition
from dex_agents.mcp import (
    McpStreamableHttpServerConfig,
    McpTool,
    McpToolListingError,
    McpToolSource,
)
from dex_agents_cli_core.config import ConnectorsConfig, EffectiveConfig
from dex_agents_cli_core.tool_policy import disabled_tool_call, raise_if_disabled
from dex_agents_cli_core.turns import (
    TurnConfigResolver,
    current_turn_config,
    recorded_turn_settings,
)

#: The connector source's ``source_name``; stored calls are routed by it.
CONNECTORS_TOOL_SOURCE_NAME = "dex_connectors"

_MCP_SERVER_NAME = "connectors"
_MCP_REFERENCE_KEY = "mcp"
_IDENTITY_REFERENCE_KEY = "connector_tool"
_CLIENT_NAME = "dex-agents-cli"
_CATALOG_TIMEOUT_SECONDS = 15.0
_CATALOG_REFRESH_SECONDS = 30.0
_LISTING_PAGE_LIMIT = 100


@dataclass(frozen=True)
class ConnectorToolMetaKeys:
    """The ``_meta`` keys that name what one connectors MCP server tool runs.

    Attributes:
        connector: Key of the connector ID, such as ``slack``.
        connection: Key of the Dex Web connection name, such as ``work``.
        operation: Key of the operationId, such as ``getMessage``.
        operation_name: Key of the snake-case operation name, such as ``get_message``.
        expected_connector: ``tools/call`` request key of the connector the call was approved for.
        expected_connection: ``tools/call`` request key of the connection it was approved for.
        expected_operation: ``tools/call`` request key of the operation it was approved for.
    """

    connector: str
    connection: str
    operation: str
    operation_name: str
    expected_connector: str
    expected_connection: str
    expected_operation: str


#: The keys of the connectors MCP server: the first four in the top-level ``_meta`` of every
#: tool, the ``expected_*`` keys in the ``_meta`` of a ``tools/call`` request.
CONNECTOR_TOOL_META_KEYS = ConnectorToolMetaKeys(
    connector="io.superdurable.connectors/connector",
    connection="io.superdurable.connectors/connection",
    operation="io.superdurable.connectors/operation",
    operation_name="io.superdurable.connectors/operationName",
    expected_connector="io.superdurable.connectors/expectedConnector",
    expected_connection="io.superdurable.connectors/expectedConnection",
    expected_operation="io.superdurable.connectors/expectedOperation",
)


@dataclass(frozen=True)
class ConnectorToolIdentity:
    """Which connector operation on which connection one MCP tool runs.

    Attributes:
        connector: Connector ID, such as ``slack``.
        connection: Connection name.
        operation: The operationId, such as ``getMessage``, or ``None`` when
            the server did not send it.
        operation_name: The snake-case operation name, such as ``get_message``,
            or ``None`` when the server did not send it.
    """

    connector: str
    connection: str
    operation: str | None = None
    operation_name: str | None = None

    def is_enabled_by(self, connectors: ConnectorsConfig) -> bool:
        """Report whether a configuration enables this operation on this connection.

        Args:
            connectors: The ``connectors`` section of a turn.

        Returns:
            ``True`` when an entry covers the connection and either lists no
            operations or lists this one, by its snake-case name or its
            operationId. An entry that lists operations needs both
            ``operation`` and ``operationName`` in the tool's ``_meta``.
        """
        if connectors.enabled is None:
            return True
        for entry in connectors.enabled:
            if not entry.covers_connection(self.connector, self.connection):
                continue
            if entry.tools is None:
                return True
            if (
                self.operation is not None
                and self.operation_name is not None
                and (
                    entry.allows_operation(self.operation_name)
                    or entry.allows_operation(self.operation)
                )
            ):
                return True
        return False


@dataclass(frozen=True)
class ConnectorCatalogTool:
    """One tool of the connectors MCP server.

    Attributes:
        name: The tool name the agent sees.
        description: Its description.
        identity: Its connector operation, or ``None`` when the server did not say.
    """

    name: str
    description: str
    identity: ConnectorToolIdentity | None


class ConnectorsMcpUnavailableError(Exception):
    """Report that the connectors MCP server cannot be listed, with the reason."""


class ConnectorsMcpListingError(ConnectorsMcpUnavailableError):
    """Report that the connectors MCP server answered ``initialize`` but its listing failed."""


@dataclass(frozen=True)
class ConnectorToolDefinition:
    """A connector tool as the model sees it: the MCP tool name without a server prefix.

    Attributes:
        name: The MCP tool name, such as ``slack_post_thread_reply``.
        description: Tool description for the model.
        input_schema: JSON Schema of the arguments.
        mcp_tool: The ``McpTool`` that runs the call.
        identity: Its connector operation, or ``None`` when the server did not say.
    """

    name: str
    description: str
    input_schema: Mapping[str, Any] = field(repr=False)
    mcp_tool: McpTool = field(repr=False)
    identity: ConnectorToolIdentity | None = None


async def read_connector_catalog(url: str) -> tuple[ConnectorCatalogTool, ...]:
    """List the connectors MCP server's tools and the connector operation each one runs.

    Args:
        url: The server's MCP endpoint.

    Returns:
        Every listed tool.

    Raises:
        ConnectorsMcpUnavailableError: If the server cannot be listed. A server
            that answers 503 gives its reason, such as that it is still starting.
            It is a ``ConnectorsMcpListingError`` when the server answered
            ``initialize`` and only the listing failed.
    """
    is_initialized = False
    try:
        async with Client(
            url,
            read_timeout_seconds=_CATALOG_TIMEOUT_SECONDS,
            client_info=_client_info(),
            cache=None,
        ) as client:
            is_initialized = True
            tools: list[Any] = []
            cursor: str | None = None
            for _ in range(_LISTING_PAGE_LIMIT):
                page = await client.list_tools(cursor=cursor)
                tools.extend(page.tools)
                cursor = page.next_cursor
                if not cursor:
                    break
    except Exception as error:
        reason = await _describe_unavailable(url, error)
        if is_initialized:
            raise ConnectorsMcpListingError(reason) from error
        raise ConnectorsMcpUnavailableError(reason) from error
    return tuple(
        ConnectorCatalogTool(
            name=tool.name, description=tool.description or "", identity=_identity(tool.meta)
        )
        for tool in tools
    )


class ConnectorsMcpToolSource:
    """Offer the connectors MCP server's tools that the running turn enables.

    Calls run through ``McpToolSource``, so each one is a Dex tool Step with
    the server's effect class, approval, idempotency key, and tasks. The tool
    names are the server's own, without a server prefix, so
    ``approval.always`` names them as the agent sees them. A tool whose
    connector, connection, and operation the server does not report is
    offered only while ``connectors.enabled`` is omitted.
    """

    def __init__(
        self,
        url: str,
        resolver: TurnConfigResolver,
        *,
        is_available: bool = True,
        report: Callable[[str], None] | None = None,
        media_store: MediaStore | None = None,
    ) -> None:
        """Connect to one connectors MCP server.

        Args:
            url: The server's MCP endpoint, on a loopback host.
            resolver: The turn configuration resolver.
            is_available: Whether the server answered when the command started.
            report: Prints a line when the server stops or starts answering;
                ``None`` prints nothing.
            media_store: The store for the images that tool results carry.
                With a store, each image of a successful result is uploaded
                in the tool Step and reaches the model as an image reference.
                ``None``, the default, keeps images as JSON in the output text.

                .. versionadded:: 0.5.0
        """
        self._url = url
        self._resolver = resolver
        self._is_available = is_available
        self._report = report
        self._mcp = McpToolSource(
            [McpStreamableHttpServerConfig(name=_MCP_SERVER_NAME, url=url)],
            client_name=_CLIENT_NAME,
            media_store=media_store,
        )
        self._last_listing: tuple[ConnectorToolDefinition, ...] = ()

    @property
    def source_name(self) -> str:
        """Return ``dex_connectors``."""
        return CONNECTORS_TOOL_SOURCE_NAME

    @property
    def url(self) -> str:
        """Return the server's MCP endpoint."""
        return self._url

    @property
    def is_available(self) -> bool:
        """Return whether the server answered its latest listing or catalog read."""
        return self._is_available

    def cached_tools(self) -> tuple[ConnectorToolDefinition, ...]:
        """Return the last listing, or the startup listing of ``list_file_enabled_tools``."""
        return self._last_listing

    async def list_tools(self) -> Sequence[SourceToolDefinition]:
        """Return the tools the running turn enables; none outside a model call.

        The server is listed again in every model call. While it does not
        answer, no connector tools are offered, and a line is printed when it
        stops and when it starts answering again.
        """
        turn_config = current_turn_config()
        if turn_config is None:
            return ()
        return await self._list_enabled_tools(turn_config)

    async def list_file_enabled_tools(self) -> Sequence[SourceToolDefinition]:
        """List the tools the configuration file enables, before any turn runs.

        The command calls it at startup, so ``cached_tools`` has the tools
        before the harness builds its Flow. A runner that fixes its tools when
        it is built, such as the OpenAI Agents SDK runner, can offer only these.

        Returns:
            The listed tools; none while the server does not answer.
        """
        return await self._list_enabled_tools(self._resolver.file_config)

    async def _list_enabled_tools(
        self, turn_config: EffectiveConfig
    ) -> Sequence[SourceToolDefinition]:
        try:
            mcp_tools = await self._mcp.list_tools(refresh=True)
            catalog = await read_connector_catalog(self._url)
        except (McpToolListingError, ConnectorsMcpUnavailableError) as error:
            if self._is_available and self._report is not None:
                self._report(f"Connector tools are unavailable for now: {error}")
            self._is_available = False
            return ()
        if not self._is_available and self._report is not None:
            self._report("Connector tools are available again.")
        self._is_available = True
        identities = {tool.name: tool.identity for tool in catalog}
        self._last_listing = tuple(
            ConnectorToolDefinition(
                name=tool.mcp_tool_name,
                description=tool.description,
                input_schema=tool.input_schema,
                mcp_tool=tool,
                identity=identities.get(tool.mcp_tool_name),
            )
            for tool in mcp_tools
            if _is_enabled(turn_config.connectors, identities.get(tool.mcp_tool_name))
        )
        return self._last_listing

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        """Build the MCP call under the agent's tool name, adding ``approval.always``.

        A tool whose connection or operation the turn does not enable becomes
        a call that its tool Step refuses.
        """
        if not isinstance(listed_tool, ConnectorToolDefinition):
            raise LookupError(f"{tool_name} was not listed by the connectors MCP server")
        turn_config = self._resolver.resolve_turn(turn)
        if not _is_enabled(turn_config.connectors, listed_tool.identity):
            return disabled_tool_call(tool_call_id, tool_name, arguments)
        mcp_tool = listed_tool.mcp_tool
        built = self._mcp.deferred_tool_call(
            turn,
            tool_call_id=tool_call_id,
            tool_name=mcp_tool.name,
            arguments=arguments,
            listed_tool=mcp_tool,
        )
        policy = built.policy
        if tool_name in turn_config.approval.always:
            policy = ToolPolicy(policy.effect, requires_approval=True)
        identity = listed_tool.identity
        reference = {
            _MCP_REFERENCE_KEY: built.tool_source_reference,
            _IDENTITY_REFERENCE_KEY: None if identity is None else asdict(identity),
        }
        return replace(
            built, tool_name=tool_name, policy=policy, tool_source_reference=json.dumps(reference)
        )

    async def execute_deferred_tool_call(
        self, call: DeferredToolExecution
    ) -> ToolExecutionResult | ToolTaskStarted:
        """Run one call through ``McpToolSource``, after authorizing it again on this Worker.

        The call may run on another Worker than the one that built it, with
        its own configuration file. Its connection and operation must be
        enabled for the turn here too; otherwise nothing runs.
        """
        raise_if_disabled(call)
        record, identity = _unwrap_reference(call.tool_call)
        turn = current_turn_config() or self._resolver.resolve(recorded_turn_settings(call))
        if not _is_enabled(turn.connectors, identity):
            raise ToolExecutionError(f"tool {record.tool_name} is switched off for this session")
        unwrapped = replace(call, tool_call=record)
        if identity is None:
            return await self._mcp.execute_deferred_tool_call(unwrapped)
        # Only a resume that can never send tools/call again skips the listing check.
        if not self._mcp.will_resume_tool_task_without_sending(unwrapped):
            await self._check_binding(record, identity)
        return await self._mcp.execute_deferred_tool_call(
            unwrapped, request_meta=_expected_identity_meta(identity)
        )

    async def _check_binding(self, record: AgentToolCall, approved: ConnectorToolIdentity) -> None:
        """Refuse a call whose tool name now runs another connection or operation.

        A name can move to another connection between the model call and the
        tool Step, such as when a second connection of the connector is
        authorized. Nothing is sent then.
        """
        mcp_tool_name = _mcp_tool_name(record)
        try:
            catalog = await read_connector_catalog(self._url)
        except ConnectorsMcpUnavailableError as error:
            raise ToolExecutionError(
                f"cannot check what {record.tool_name} runs now, so it was not sent: {error}"
            ) from error
        current = next((tool.identity for tool in catalog if tool.name == mcp_tool_name), None)
        if current is None or (current.connector, current.connection, current.operation) != (
            approved.connector,
            approved.connection,
            approved.operation,
        ):
            now = (
                "is no longer offered"
                if current is None
                else f"now runs {_describe_identity(current)}"
            )
            raise ToolExecutionError(
                f"{record.tool_name} {now}, but the call was made for "
                f"{_describe_identity(approved)}; it was not sent"
            )

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        """Poll one MCP task through ``McpToolSource``."""
        record, _ = _unwrap_reference(poll.tool_call)
        return await self._mcp.poll_tool_task(replace(poll, tool_call=record))

    async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        """Cancel one abandoned MCP task through ``McpToolSource``."""
        record, _ = _unwrap_reference(cancellation.tool_call)
        await self._mcp.cancel_tool_task(replace(cancellation, tool_call=record))


def _expected_identity_meta(identity: ConnectorToolIdentity) -> dict[str, str]:
    keys = CONNECTOR_TOOL_META_KEYS
    meta = {
        keys.expected_connector: identity.connector,
        keys.expected_connection: identity.connection,
    }
    if identity.operation is not None:
        meta[keys.expected_operation] = identity.operation
    return meta


def _describe_identity(identity: ConnectorToolIdentity) -> str:
    operation = identity.operation or "an unnamed operation"
    return f"{identity.connector}/{identity.connection} {operation}"


def _mcp_tool_name(record: AgentToolCall) -> str:
    """Return the tool's name on the MCP server from ``McpToolSource``'s own reference."""
    try:
        parsed = json.loads(record.tool_source_reference or "null")
    except json.JSONDecodeError:
        parsed = None
    name = parsed.get("mcp_tool") if isinstance(parsed, dict) else None
    return name if isinstance(name, str) else record.tool_name


def _unwrap_reference(
    record: AgentToolCall,
) -> tuple[AgentToolCall, ConnectorToolIdentity | None]:
    """Return the record with ``McpToolSource``'s own reference, and the tool's identity."""
    try:
        parsed = json.loads(record.tool_source_reference or "null")
    except json.JSONDecodeError:
        return record, None
    if not isinstance(parsed, dict) or _MCP_REFERENCE_KEY not in parsed:
        return record, None
    stored = parsed.get(_IDENTITY_REFERENCE_KEY)
    identity = ConnectorToolIdentity(**stored) if isinstance(stored, dict) else None
    return replace(record, tool_source_reference=parsed[_MCP_REFERENCE_KEY]), identity


def _is_enabled(connectors: ConnectorsConfig, identity: ConnectorToolIdentity | None) -> bool:
    if connectors.enabled is None:
        return True
    return identity is not None and identity.is_enabled_by(connectors)


def _identity(meta: Mapping[str, Any] | None) -> ConnectorToolIdentity | None:
    if not meta:
        return None
    keys = CONNECTOR_TOOL_META_KEYS
    connector = meta.get(keys.connector)
    connection = meta.get(keys.connection)
    operation = meta.get(keys.operation)
    operation_name = meta.get(keys.operation_name)
    if not all(isinstance(value, str) and value for value in (connector, connection)):
        return None
    assert isinstance(connector, str) and isinstance(connection, str)
    return ConnectorToolIdentity(
        connector=connector,
        connection=connection,
        operation=operation if isinstance(operation, str) and operation else None,
        operation_name=operation_name
        if isinstance(operation_name, str) and operation_name
        else None,
    )


async def _describe_unavailable(url: str, error: Exception) -> str:
    """Ask the endpoint directly, so a 503 shows the server's own reason."""
    try:
        async with httpx2.AsyncClient(timeout=5.0) as http:
            response = await http.get(url, headers={"Accept": "text/event-stream"})
    except httpx2.HTTPError as http_error:
        return f"cannot reach {url}: {http_error}"
    if response.status_code == 503:
        reason = response.text.strip() or "the server is not ready"
        return f"{url} is not ready: {reason}"
    if response.status_code == 404:
        return (
            f"{url} does not serve MCP (404); this dexcli dev predates the connectors MCP "
            "server or runs with --connectors-mcp=false"
        )
    return f"cannot list the tools of {url}: {error}"


def _client_info() -> Implementation:
    return Implementation(name=_CLIENT_NAME, version="0.1.0")
