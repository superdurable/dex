# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The local tool source: file, shell, web fetch, and browser tools as Dex tool Steps."""

from __future__ import annotations

import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from dex_agents import (
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolTaskPoll,
    ToolTaskPollOutcome,
)
from dex_agents.builtin_loop import SourceToolDefinition
from dex_agents_cli_core.config import EffectiveConfig
from dex_agents_cli_core.harness import LocalToolGroup
from dex_agents_cli_core.local_tools.browser import BrowserSessions
from dex_agents_cli_core.local_tools.files import WorkspaceFiles
from dex_agents_cli_core.local_tools.shell import (
    DEFAULT_SHELL_TIMEOUT_SECONDS,
    MAX_SHELL_TIMEOUT_SECONDS,
    run_shell_command,
)
from dex_agents_cli_core.local_tools.web import fetch_web_page
from dex_agents_cli_core.tool_policy import (
    disabled_tool_call,
    is_local_tool_group_enabled,
    is_shell_command_approval_required,
    raise_if_disabled,
    resolve_turn_tool_policy,
)
from dex_agents_cli_core.turns import (
    TurnConfigResolver,
    current_turn_config,
    recorded_turn_settings,
)

#: The local tool source's ``source_name``; stored calls are routed by it.
LOCAL_TOOL_SOURCE_NAME = "dex_local_tools"
SHELL_TOOL_NAME = "run_shell_command"


@dataclass(frozen=True)
class LocalToolDefinition:
    """One local tool as the model sees it, with its group and default policy.

    Attributes:
        name: Tool name.
        description: Tool description for the model.
        input_schema: JSON Schema of the arguments.
        group: The ``local_tools`` group that switches it.
        effect: Its effect class.
        requires_approval: Whether every call waits for approval by default.
    """

    name: str
    description: str
    input_schema: Mapping[str, Any] = field(repr=False)
    group: LocalToolGroup
    effect: ToolEffect
    requires_approval: bool


def _schema(properties: dict[str, Any], required: Sequence[str]) -> dict[str, Any]:
    return {
        "type": "object",
        "properties": properties,
        "required": list(required),
        "additionalProperties": False,
    }


_PATH = {"type": "string", "description": "Path relative to the workspace."}
_SELECTOR = {
    "type": "string",
    "description": (
        'A Playwright selector, such as text=Sign in, role=button[name="Submit"], '
        "or a CSS selector."
    ),
}

#: Every local tool, in the order the model sees them.
LOCAL_TOOL_DEFINITIONS: tuple[LocalToolDefinition, ...] = (
    LocalToolDefinition(
        name="read_file",
        description="Read a text file in the workspace. Returns its lines.",
        input_schema=_schema(
            {
                "path": _PATH,
                "offset": {"type": "integer", "minimum": 0, "description": "First line, from 0."},
                "limit": {"type": "integer", "minimum": 1, "description": "Most lines to read."},
            },
            ["path"],
        ),
        group=LocalToolGroup.FILES,
        effect=ToolEffect.READ,
        requires_approval=False,
    ),
    LocalToolDefinition(
        name="list_directory",
        description="List a directory in the workspace. Directories end with /.",
        input_schema=_schema({"path": _PATH}, []),
        group=LocalToolGroup.FILES,
        effect=ToolEffect.READ,
        requires_approval=False,
    ),
    LocalToolDefinition(
        name="search_files",
        description="Find lines matching a regular expression in the workspace's files.",
        input_schema=_schema(
            {"pattern": {"type": "string", "description": "A regular expression."}, "path": _PATH},
            ["pattern"],
        ),
        group=LocalToolGroup.FILES,
        effect=ToolEffect.READ,
        requires_approval=False,
    ),
    LocalToolDefinition(
        name="write_file",
        description="Create or replace a file in the workspace with the given text.",
        input_schema=_schema({"path": _PATH, "content": {"type": "string"}}, ["path", "content"]),
        group=LocalToolGroup.FILES,
        effect=ToolEffect.IDEMPOTENT_WRITE,
        requires_approval=True,
    ),
    LocalToolDefinition(
        name="edit_file",
        description="Replace the one occurrence of old_text with new_text in a workspace file.",
        input_schema=_schema(
            {"path": _PATH, "old_text": {"type": "string"}, "new_text": {"type": "string"}},
            ["path", "old_text", "new_text"],
        ),
        group=LocalToolGroup.FILES,
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=True,
    ),
    LocalToolDefinition(
        name=SHELL_TOOL_NAME,
        description=(
            "Run a shell command with /bin/sh in the workspace. Returns the exit code and output."
        ),
        input_schema=_schema(
            {
                "command": {"type": "string"},
                "timeout_seconds": {
                    "type": "integer",
                    "minimum": 1,
                    "maximum": MAX_SHELL_TIMEOUT_SECONDS,
                    "description": f"Defaults to {DEFAULT_SHELL_TIMEOUT_SECONDS}.",
                },
            },
            ["command"],
        ),
        group=LocalToolGroup.SHELL,
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=True,
    ),
    LocalToolDefinition(
        name="fetch_web_page",
        description="Fetch a web page with GET. Returns its text.",
        input_schema=_schema({"url": {"type": "string"}}, ["url"]),
        group=LocalToolGroup.WEB_FETCH,
        effect=ToolEffect.READ,
        requires_approval=False,
    ),
    LocalToolDefinition(
        name="browser_navigate",
        description="Open a URL in this session's browser page. Returns a text snapshot.",
        input_schema=_schema({"url": {"type": "string"}}, ["url"]),
        group=LocalToolGroup.BROWSER,
        effect=ToolEffect.READ,
        requires_approval=False,
    ),
    LocalToolDefinition(
        name="browser_snapshot",
        description="Read the browser page as a text accessibility snapshot.",
        input_schema=_schema({}, []),
        group=LocalToolGroup.BROWSER,
        effect=ToolEffect.READ,
        requires_approval=False,
    ),
    LocalToolDefinition(
        name="browser_click",
        description="Click an element of the browser page. Returns a text snapshot.",
        input_schema=_schema({"selector": _SELECTOR}, ["selector"]),
        group=LocalToolGroup.BROWSER,
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=True,
    ),
    LocalToolDefinition(
        name="browser_fill",
        description="Type text into a field of the browser page. Returns a text snapshot.",
        input_schema=_schema(
            {"selector": _SELECTOR, "value": {"type": "string"}}, ["selector", "value"]
        ),
        group=LocalToolGroup.BROWSER,
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=True,
    ),
    LocalToolDefinition(
        name="browser_submit_form",
        description="Submit the form that contains an element. Returns a text snapshot.",
        input_schema=_schema({"selector": _SELECTOR}, ["selector"]),
        group=LocalToolGroup.BROWSER,
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=True,
    ),
)
_DEFINITIONS_BY_NAME = {definition.name: definition for definition in LOCAL_TOOL_DEFINITIONS}


class LocalToolSource:
    """Offer the local tools as a ``ToolSource``, so every call is its own Dex tool Step.

    Reads run at once. Writes, browser clicks, fills, and submits wait for
    approval. A shell command waits for approval unless the turn's allow list
    has its exact text; it is always a single-attempt write, so a Worker that
    dies during it never runs it again. The tools a turn offers follow its
    ``local_tools`` switches, and tools outside a model call's turn are never
    offered.
    """

    def __init__(
        self,
        workspace: Path,
        groups: frozenset[LocalToolGroup],
        resolver: TurnConfigResolver,
        browser: BrowserSessions | None = None,
    ) -> None:
        """Offer some groups of local tools.

        Args:
            workspace: The existing workspace directory.
            groups: The groups this source offers; a harness may replace the others.
            resolver: The turn configuration resolver.
            browser: The browser pages; ``None`` creates them on first use.
        """
        self._workspace = workspace
        self._files = WorkspaceFiles(workspace)
        self._groups = groups
        self._resolver = resolver
        self._browser = browser or BrowserSessions()
        # Until the first model call lists, the file's tools are cached for runners built before it.
        self._last_listing = self._enabled_tools(resolver.file_config)

    @property
    def source_name(self) -> str:
        """Return ``dex_local_tools``."""
        return LOCAL_TOOL_SOURCE_NAME

    @property
    def browser(self) -> BrowserSessions:
        """Return the browser pages, so the command can close them on exit."""
        return self._browser

    def declared_tool_names(self) -> frozenset[str]:
        """Return the names of every tool this source can offer."""
        return frozenset(
            definition.name
            for definition in LOCAL_TOOL_DEFINITIONS
            if definition.group in self._groups
        )

    def cached_tools(self) -> tuple[LocalToolDefinition, ...]:
        """Return the last listing, or the tools the configuration file enables before it."""
        return self._last_listing

    async def list_tools(self) -> Sequence[SourceToolDefinition]:
        """Return the tools the running turn enables; none outside a model call."""
        turn_config = current_turn_config()
        if turn_config is None:
            return ()
        self._last_listing = self._enabled_tools(turn_config)
        return self._last_listing

    def _enabled_tools(self, config: EffectiveConfig) -> tuple[LocalToolDefinition, ...]:
        return tuple(
            definition
            for definition in LOCAL_TOOL_DEFINITIONS
            if definition.group in self._groups
            and is_local_tool_group_enabled(config, definition.group)
        )

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        """Build a call with the tool's policy, the turn's allow list, and ``approval.always``.

        A tool its turn switches off, or that this source does not offer,
        becomes a call that its tool Step refuses.
        """
        definition = _DEFINITIONS_BY_NAME.get(tool_name)
        turn_config = self._resolver.resolve_turn(turn)
        if (
            definition is None
            or definition.group not in self._groups
            or not is_local_tool_group_enabled(turn_config, definition.group)
        ):
            return disabled_tool_call(tool_call_id, tool_name, arguments)
        requires_approval = definition.requires_approval
        if tool_name == SHELL_TOOL_NAME:
            command = _shell_command_or_none(arguments)
            requires_approval = command is None or is_shell_command_approval_required(
                turn_config, command
            )
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=resolve_turn_tool_policy(
                turn, tool_name, definition.effect, requires_approval, turn_config
            ),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        """Run one approved local tool call, after authorizing it again on this Worker.

        The call may run on another Worker than the one that built it, with
        its own configuration file. Here the tool's group must be offered and
        switched on for the turn, and a shell command that was built without
        approval must still be in the allow list; otherwise nothing runs.
        """
        raise_if_disabled(call)
        record = call.tool_call
        turn = current_turn_config() or self._resolver.resolve(recorded_turn_settings(call))
        definition = _DEFINITIONS_BY_NAME.get(record.tool_name)
        if (
            definition is None
            or definition.group not in self._groups
            or not is_local_tool_group_enabled(turn, definition.group)
        ):
            raise ToolExecutionError(f"tool {record.tool_name} is switched off for this session")
        if record.tool_name == SHELL_TOOL_NAME and not record.requires_approval:
            command = _shell_command_or_none(record.arguments)
            if command is None or is_shell_command_approval_required(turn, command):
                raise ToolExecutionError(
                    "the command is no longer in the shell allow list, and it was not approved; "
                    "it did not run"
                )
        arguments = _read_arguments(record.arguments)
        output = await self._run(record.tool_name, arguments, call.flow_id)
        return ToolExecutionResult(output=output)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        """Refuse a poll: local tools never start tasks."""
        raise ToolExecutionError(f"{poll.tool_call.tool_name} does not run as a task")

    async def _run(self, tool_name: str, arguments: Mapping[str, Any], session_id: str) -> str:
        files = self._files
        match tool_name:
            case "read_file":
                return files.read_file(
                    _text(arguments, "path"),
                    _optional_int(arguments, "offset") or 0,
                    _optional_int(arguments, "limit"),
                )
            case "list_directory":
                return files.list_directory(_optional_text(arguments, "path") or ".")
            case "search_files":
                return files.search_files(
                    _text(arguments, "pattern"), _optional_text(arguments, "path") or "."
                )
            case "write_file":
                return files.write_file(_text(arguments, "path"), _text(arguments, "content"))
            case "edit_file":
                return files.edit_file(
                    _text(arguments, "path"),
                    _text(arguments, "old_text"),
                    _text(arguments, "new_text"),
                )
            case "run_shell_command":
                timeout = _optional_int(arguments, "timeout_seconds")
                return await run_shell_command(
                    _text(arguments, "command"),
                    self._workspace,
                    min(timeout or DEFAULT_SHELL_TIMEOUT_SECONDS, MAX_SHELL_TIMEOUT_SECONDS),
                )
            case "fetch_web_page":
                return await fetch_web_page(_text(arguments, "url"))
            case "browser_navigate":
                return await self._browser.open_page(session_id, _text(arguments, "url"))
            case "browser_snapshot":
                return await self._browser.snapshot(session_id)
            case "browser_click":
                return await self._browser.click(session_id, _text(arguments, "selector"))
            case "browser_fill":
                return await self._browser.fill(
                    session_id, _text(arguments, "selector"), _text(arguments, "value")
                )
            case "browser_submit_form":
                return await self._browser.submit(session_id, _text(arguments, "selector"))
        raise ToolExecutionError(f"unknown local tool {tool_name}")


def _shell_command_or_none(arguments: str) -> str | None:
    """Return a shell call's command; ``None`` for arguments the call itself will refuse."""
    try:
        parsed = json.loads(arguments or "{}")
    except json.JSONDecodeError:
        return None
    command = parsed.get("command") if isinstance(parsed, dict) else None
    return command if isinstance(command, str) else None


def _read_arguments(arguments: str) -> dict[str, Any]:
    try:
        parsed = json.loads(arguments or "{}")
    except json.JSONDecodeError as error:
        raise ToolExecutionError("arguments are not valid JSON") from error
    if not isinstance(parsed, dict):
        raise ToolExecutionError("arguments must be a JSON object")
    return parsed


def _text(arguments: Mapping[str, Any], name: str) -> str:
    value = arguments.get(name)
    if not isinstance(value, str):
        raise ToolExecutionError(f"argument {name} must be a string")
    return value


def _optional_text(arguments: Mapping[str, Any], name: str) -> str | None:
    value = arguments.get(name)
    if value is None:
        return None
    if not isinstance(value, str):
        raise ToolExecutionError(f"argument {name} must be a string")
    return value


def _optional_int(arguments: Mapping[str, Any], name: str) -> int | None:
    value = arguments.get(name)
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, int):
        raise ToolExecutionError(f"argument {name} must be an integer")
    return int(value)
