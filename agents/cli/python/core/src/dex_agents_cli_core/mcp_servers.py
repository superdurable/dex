# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Remote MCP server tools from the configuration's ``mcp_servers``."""

from __future__ import annotations

import json
import logging
import re
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import replace
from typing import TypeVar

from dex_agents import (
    AgentToolCall,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    MediaStore,
    ToolEffect,
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
from dex_agents_cli_core.config import EffectiveConfig
from dex_agents_cli_core.environment import DEX_SECRET_ENVIRONMENT_NAMES
from dex_agents_cli_core.turns import (
    TurnConfigResolver,
    current_turn_config,
    recorded_turn_settings,
)

#: The remote MCP server source's ``source_name``; stored calls are routed by it.
MCP_SERVERS_TOOL_SOURCE_NAME = "mcp_servers"

_CLIENT_NAME = "dex-agents-cli"
_VARIABLE_REFERENCE = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}")
_PRINTABLE_ASCII = re.compile(r"[ -~]*")
_LOGGER = logging.getLogger(__name__)
_REDACTED = "[redacted]"
# Shorter texts are no credible secrets, and replacing them would garble messages.
_MINIMUM_SECRET_LENGTH = 8
_SECRET_SEPARATOR = re.compile(r"[\s=:,;]+")
_SINGLE_ATTEMPT_EFFECTS = frozenset({ToolEffect.NON_IDEMPOTENT_WRITE, ToolEffect.UNCLASSIFIED})
_OutcomeT = TypeVar("_OutcomeT")


class McpServerSettingsError(Exception):
    """Report an ``mcp_servers`` header whose environment variable is unusable.

    The message names the setting and the variable, never the variable's
    value. The command prints it and exits with code 2.

    Attributes:
        setting: The header to fix, such as
            ``mcp_servers.studio.headers.Authorization``.
        message: The exact message from ``agents/conformance/cli_config.json``.

    .. versionadded:: 0.6.4
    """

    def __init__(self, setting: str, message: str) -> None:
        """Create the error.

        Args:
            setting: The header to fix.
            message: What is wrong with its variable.
        """
        super().__init__(message)
        self.setting = setting
        self.message = message


def resolve_mcp_servers(
    config: EffectiveConfig, environment: Mapping[str, str]
) -> tuple[McpStreamableHttpServerConfig, ...]:
    """Return the remote MCP servers of a configuration, with every header expanded.

    Each ``${NAME}`` in a header value becomes the value of the environment
    variable ``NAME``, exactly as set: it is never trimmed or expanded again.
    Any other ``$`` stays as written. The Dex credential variables
    ``DEX_API_KEY``, ``DEX_CREDENTIAL``, and ``DEX_CREDENTIAL_FILE`` count as
    not set, so a Dex credential never reaches another server. The servers
    come in Unicode code point order of their names. The checks take the servers in that order, the
    headers of a server in code point order of their names, and the
    references of a value from left to right.

    Only the Worker calls it, when the command starts. The returned configs
    hold the secrets, and their ``repr`` replaces every header value with
    ``***``; never log or store the headers themselves.

    Example:
        >>> servers = resolve_mcp_servers(config, os.environ)
        >>> [server.name for server in servers]
        ['studio']

    .. versionadded:: 0.6.4

    Args:
        config: A file's effective configuration.
        environment: The variables the references read, such as ``os.environ``.

    Returns:
        One Streamable HTTP server config per ``mcp_servers`` entry, whose
        ``trusts_tool_annotations`` is the entry's ``trust_tool_annotations``.

    Raises:
        McpServerSettingsError: If a referenced variable is not set, is empty,
            or holds anything but printable ASCII (U+0020 to U+007E).
    """
    servers, _ = _expand_mcp_servers(config, environment)
    return servers


def mcp_server_secret_texts(
    config: EffectiveConfig, environment: Mapping[str, str]
) -> frozenset[str]:
    """Return the texts that ``McpServersToolSource`` replaces with ``[redacted]``.

    They are the expanded header values, their parts between whitespace,
    ``=``, ``:``, ``,``, or ``;``, and
    the values that ``${NAME}`` references substituted, each of at least 8
    characters. Pass them as ``secret_texts``, so a value that a server echoes
    in an error never reaches a log, an error message, or the session.

    Example:
        >>> secrets = mcp_server_secret_texts(config, os.environ)

    .. versionadded:: 0.6.4

    Args:
        config: A file's effective configuration.
        environment: The variables the references read, such as ``os.environ``.

    Returns:
        The texts to redact.

    Raises:
        McpServerSettingsError: As ``resolve_mcp_servers`` does.
    """
    servers, substituted = _expand_mcp_servers(config, environment)
    texts = set(substituted)
    for server in servers:
        for value in (server.headers or {}).values():
            texts.add(value)
            texts.update(_SECRET_SEPARATOR.split(value))
    return frozenset(text for text in texts if len(text) >= _MINIMUM_SECRET_LENGTH)


def _expand_mcp_servers(
    config: EffectiveConfig, environment: Mapping[str, str]
) -> tuple[tuple[McpStreamableHttpServerConfig, ...], list[str]]:
    servers: list[McpStreamableHttpServerConfig] = []
    substituted: list[str] = []
    for name in sorted(config.mcp_servers):
        server = config.mcp_servers[name]
        expanded = {
            header: _expand_header_value(
                f"mcp_servers.{name}.headers.{header}",
                server.headers[header],
                environment,
                substituted,
            )
            for header in sorted(server.headers)
        }
        servers.append(
            McpStreamableHttpServerConfig(
                name=name,
                url=server.url,
                headers={header: expanded[header] for header in server.headers},
                trusts_tool_annotations=server.trust_tool_annotations,
            )
        )
    return tuple(servers), substituted


def _expand_header_value(
    setting: str, value: str, environment: Mapping[str, str], substituted: list[str]
) -> str:
    def expand(reference: re.Match[str]) -> str:
        variable = reference[1]
        variable_value = (
            "" if variable in DEX_SECRET_ENVIRONMENT_NAMES else environment.get(variable, "")
        )
        if variable_value == "":
            raise McpServerSettingsError(
                setting,
                f"{setting} needs the environment variable {variable}, "
                "which is not set or is empty.",
            )
        if _PRINTABLE_ASCII.fullmatch(variable_value) is None:
            raise McpServerSettingsError(
                setting,
                f"{setting} needs the environment variable {variable}, "
                "whose value is not printable ASCII.",
            )
        substituted.append(variable_value)
        return variable_value

    return _VARIABLE_REFERENCE.sub(expand, value)


class McpServersToolSource:
    """Offer the tools of the configuration's remote MCP servers.

    Calls run through ``McpToolSource``, so each one is a Dex tool Step with
    the session's idempotency key, and the server's effect, approval, and
    tasks apply when its configuration trusts its annotations. The agent
    sees each tool as ``<server name>__<tool name>``, and the turn's
    ``approval.always`` names it that way. A Worker registers this source
    even without servers, so it refuses the stored calls another Worker
    requested.

    The header values hold secrets. The source replaces each of its
    ``secret_texts`` and expanded header values with ``[redacted]`` in the
    errors and tool output it returns and in its log records. Install
    ``secret_redacting_log_filter`` on the log handlers, so the warnings of
    ``McpToolSource`` and the MCP client are redacted too. A Worker whose
    configuration does not trust a server's annotations runs a stored call of
    that server only when it had approval and runs at most once.

    Example:
        >>> source = McpServersToolSource(
        ...     resolve_mcp_servers(config, os.environ),
        ...     resolver,
        ...     report=print,
        ...     secret_texts=mcp_server_secret_texts(config, os.environ),
        ... )
        >>> await source.list_file_enabled_tools()

    .. versionadded:: 0.6.4
    """

    def __init__(
        self,
        servers: Sequence[McpStreamableHttpServerConfig],
        resolver: TurnConfigResolver,
        *,
        report: Callable[[str], None] | None = None,
        report_at_startup: Callable[[str], None] | None = None,
        unavailable_line: str = "MCP server tools are unavailable for now; each turn tries again.",
        media_store: MediaStore | None = None,
        secret_texts: Iterable[str] = (),
    ) -> None:
        """Describe the servers without connecting to them.

        Args:
            servers: The servers from ``resolve_mcp_servers``.
            resolver: The turn configuration resolver.
            report: Prints one line when every server stops answering and when
                one answers again; ``None`` prints nothing. Each failed
                listing, with its error, goes to the ``logging`` warning log.
            report_at_startup: Prints the line of ``list_file_enabled_tools``
                instead of ``report``; ``None`` uses ``report``.
            unavailable_line: The line ``report`` prints when no server answers.
            media_store: The store for the images that tool results carry.
                ``None``, the default, keeps images as JSON in the output text.
            secret_texts: More texts to redact, such as the values that
                ``mcp_server_secret_texts`` returns. Texts shorter than 8
                characters are kept.
        """
        self._resolver = resolver
        self._servers = {server.name: server for server in servers}
        self._secret_redactor = _SecretRedactor([*secret_texts, *_header_secret_texts(servers)])
        self._report = report
        self._report_at_startup = report_at_startup or report
        self._unavailable_line = unavailable_line
        self._is_available = True
        self._mcp = McpToolSource(
            list(servers),
            client_name=_CLIENT_NAME,
            source_name=MCP_SERVERS_TOOL_SOURCE_NAME,
            media_store=media_store,
        )

    @property
    def source_name(self) -> str:
        """Return ``mcp_servers``."""
        return MCP_SERVERS_TOOL_SOURCE_NAME

    @property
    def secret_redacting_log_filter(self) -> logging.Filter:
        """Return a log filter that redacts the secret texts of every record it sees."""
        return _SecretRedactingLogFilter(self._secret_redactor)

    @property
    def is_available(self) -> bool:
        """Return whether a server answered the latest listing; ``True`` before the first."""
        return self._is_available

    def cached_tools(self) -> tuple[McpTool, ...]:
        """Return the last listing without I/O."""
        return self._mcp.cached_tools()

    async def list_tools(self) -> Sequence[SourceToolDefinition]:
        """Return the tools of every server that answers.

        A server that cannot be listed is left out until it answers again.
        While no server answers, no tools are offered.

        Returns:
            The tools, named as the agent sees them.
        """
        return await self._list(refresh=False, report=self._report)

    async def list_file_enabled_tools(self) -> Sequence[SourceToolDefinition]:
        """List every server now, before any turn runs.

        The command calls it at startup, so ``cached_tools`` has the tools
        before a harness whose tool set is fixed at build builds its Flow.

        Returns:
            The listed tools; none while no server answers.
        """
        return await self._list(refresh=True, report=self._report_at_startup)

    async def _list(
        self, *, refresh: bool, report: Callable[[str], None] | None
    ) -> Sequence[SourceToolDefinition]:
        try:
            tools = await self._mcp.list_tools(refresh=refresh)
        except McpToolListingError as error:
            _LOGGER.warning(
                "remote MCP servers could not be listed: %s",
                self._secret_redactor.redact_text(str(error)),
            )
            if self._is_available and report is not None:
                report(self._unavailable_line)
            self._is_available = False
            return ()
        if not self._is_available:
            _LOGGER.info("remote MCP servers are available again")
            if report is not None:
                report("MCP server tools are available again.")
        self._is_available = True
        return tools

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition | None = None,
    ) -> DeferredToolCall:
        """Build the MCP call, adding the turn's ``approval.always``.

        Args:
            turn: The ``RunModel`` context.
            tool_call_id: The model's tool call ID.
            tool_name: The tool name as the agent sees it.
            arguments: The arguments as JSON text.
            listed_tool: The tool the listing offered for this model call.

        Returns:
            The deferred call.

        Raises:
            LookupError: If the tool was not listed.
        """
        built = self._mcp.deferred_tool_call(
            turn,
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            listed_tool=listed_tool,
        )
        if tool_name not in self._resolver.resolve_turn(turn).approval.always:
            return built
        return replace(built, policy=ToolPolicy(built.policy.effect, requires_approval=True))

    async def execute_deferred_tool_call(
        self, call: DeferredToolExecution
    ) -> ToolExecutionResult | ToolTaskStarted:
        """Run one call, after checking the approval rules of the Worker that runs it.

        Args:
            call: The approved call.

        Returns:
            The tool's outcome, or the started MCP task.

        Raises:
            ToolExecutionError: If the call had no approval but this Worker's
                ``approval.always`` names it, if this Worker does not trust its
                server's annotations and the call had no approval or may be
                retried, if its server is not configured on this Worker, or if
                the tool failed. Every error has its secret texts redacted.

        .. versionchanged:: 0.6.4
           Checks the call against this Worker's trust of the server's annotations.
        """
        record = call.tool_call
        turn = current_turn_config() or self._resolver.resolve(recorded_turn_settings(call))
        if not record.requires_approval and record.tool_name in turn.approval.always:
            raise ToolExecutionError(
                f"tool {record.tool_name} now needs approval, and this call had none; "
                "it did not run"
            )
        server = self._servers.get(self._server_name_of(record) or "")
        if (
            server is not None
            and not server.trusts_tool_annotations
            and (not record.requires_approval or record.effect not in _SINGLE_ATTEMPT_EFFECTS)
        ):
            raise ToolExecutionError(
                f"tool {record.tool_name} did not run: this Worker does not trust the "
                f"annotations of MCP server {server.name}, so its calls need approval and "
                "run at most once"
            )
        try:
            outcome = await self._mcp.execute_deferred_tool_call(call)
        except BaseException as error:
            self._secret_redactor.redact_exception(error)
            raise
        return self._redacted_outcome(outcome)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        """Poll one MCP task through ``McpToolSource``, with its secret texts redacted."""
        try:
            outcome = await self._mcp.poll_tool_task(poll)
        except BaseException as error:
            self._secret_redactor.redact_exception(error)
            raise
        return self._redacted_outcome(outcome)

    async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        """Cancel one abandoned MCP task through ``McpToolSource``, with its errors redacted."""
        try:
            await self._mcp.cancel_tool_task(cancellation)
        except BaseException as error:
            self._secret_redactor.redact_exception(error)
            raise

    def _redacted_outcome(self, outcome: _OutcomeT) -> _OutcomeT:
        if isinstance(outcome, ToolExecutionResult):
            redacted: _OutcomeT = replace(
                outcome, output=self._secret_redactor.redact_text(outcome.output)
            )
            return redacted
        return outcome

    def _server_name_of(self, record: AgentToolCall) -> str | None:
        """Return the server of a stored call, from its reference or this Worker's listing."""
        try:
            reference = json.loads(record.tool_source_reference or "null")
        except json.JSONDecodeError:
            reference = None
        if isinstance(reference, dict) and isinstance(reference.get("mcp_server"), str):
            return str(reference["mcp_server"])
        listed = next(
            (tool for tool in self._mcp.cached_tools() if tool.name == record.tool_name), None
        )
        return None if listed is None else listed.server_name


def _header_secret_texts(servers: Iterable[McpStreamableHttpServerConfig]) -> list[str]:
    texts: list[str] = []
    for server in servers:
        for value in (server.headers or {}).values():
            texts.append(value)
            texts.extend(_SECRET_SEPARATOR.split(value))
    return texts


class _SecretRedactor:
    """Replace each secret text, longest first, with ``[redacted]``."""

    def __init__(self, secret_texts: Iterable[str]) -> None:
        unique = {text for text in secret_texts if len(text) >= _MINIMUM_SECRET_LENGTH}
        self._secret_texts = sorted(unique, key=len, reverse=True)

    def redact_text(self, text: str) -> str:
        for secret in self._secret_texts:
            text = text.replace(secret, _REDACTED)
        return text

    def redact_exception(self, error: BaseException) -> None:
        """Redact the args, string attributes, and notes of an exception and its chain."""
        seen: set[int] = set()
        pending: list[BaseException] = [error]
        while pending:
            current = pending.pop()
            if id(current) in seen:
                continue
            seen.add(id(current))
            self._redact_one_exception(current)
            pending.extend(
                linked for linked in (current.__cause__, current.__context__) if linked is not None
            )

    def _redact_one_exception(self, error: BaseException) -> None:
        if not self._secret_texts:
            return
        error.args = tuple(
            self.redact_text(argument) if isinstance(argument, str) else argument
            for argument in error.args
        )
        for name, value in list(vars(error).items()):
            if isinstance(value, str):
                setattr(error, name, self.redact_text(value))
        notes = getattr(error, "__notes__", None)
        if isinstance(notes, list):
            error.__notes__ = [
                self.redact_text(note) if isinstance(note, str) else note for note in notes
            ]


class _SecretRedactingLogFilter(logging.Filter):
    """Redact the secret texts of a log record's message and exception; always keep it."""

    def __init__(self, redactor: _SecretRedactor) -> None:
        super().__init__()
        self._redactor = redactor

    def filter(self, record: logging.LogRecord) -> bool:
        record.msg = self._redactor.redact_text(record.getMessage())
        record.args = None
        if record.exc_info and record.exc_info[1] is not None:
            self._redactor.redact_exception(record.exc_info[1])
            record.exc_text = None
        if record.stack_info:
            record.stack_info = self._redactor.redact_text(record.stack_info)
        return True
