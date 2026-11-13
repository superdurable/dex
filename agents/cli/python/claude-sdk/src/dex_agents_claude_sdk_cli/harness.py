# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Claude Agent SDK harness: a session Flow with Claude Code's own local tools."""

from __future__ import annotations

import json
import logging
import os
import re
from collections.abc import Mapping
from contextvars import ContextVar
from dataclasses import dataclass
from pathlib import Path
from typing import Any, cast

import dex_agents_cli_core  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from claude_agent_sdk import ClaudeAgentOptions, EffortLevel

from dex_agents import (
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTracingOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    DeferredToolExecution,
    DeferredToolOutcome,
    RunModelLimit,
    ToolEffect,
    ToolExecutionError,
    ToolPolicy,
)
from dex_agents_claude_agent_sdk import (
    ClaudeAgentOptionsContext,
    ClaudeAgentRunner,
    ClaudeCodeToolDenial,
)
from dex_agents_cli_core import (
    ConfigError,
    EffectiveConfig,
    HarnessContext,
    LocalToolGroup,
    current_turn_config,
    is_local_tool_group_enabled,
    is_shell_command_approval_required,
    resolve_turn_tool_policy,
)

#: The Flow type of the command's sessions, before the CLI core namespaces it.
CLAUDE_SDK_CLI_FLOW_TYPE = "DexAgentsClaudeSdkCliSession"

#: The model the command uses when the configuration file sets none.
DEFAULT_CLAUDE_MODEL = "claude-opus-5-5"

#: The instructions the agent gets when the configuration sets none.
DEFAULT_INSTRUCTIONS = (
    "You are a helpful assistant. Use your tools to work with the files and commands "
    "in your working directory and with the connected services."
)


@dataclass(frozen=True)
class _ClaudeCodeLocalTool:
    group: LocalToolGroup
    effect: ToolEffect
    is_approval_required: bool
    path_argument: str | None


# Claude Code's tools that replace the CLI core's files, shell, and web_fetch groups.
CLAUDE_CODE_LOCAL_TOOLS: Mapping[str, _ClaudeCodeLocalTool] = {
    "Read": _ClaudeCodeLocalTool(LocalToolGroup.FILES, ToolEffect.READ, False, "file_path"),
    "Grep": _ClaudeCodeLocalTool(LocalToolGroup.FILES, ToolEffect.READ, False, "path"),
    "Glob": _ClaudeCodeLocalTool(LocalToolGroup.FILES, ToolEffect.READ, False, "path"),
    "Write": _ClaudeCodeLocalTool(
        LocalToolGroup.FILES, ToolEffect.IDEMPOTENT_WRITE, True, "file_path"
    ),
    "Edit": _ClaudeCodeLocalTool(
        LocalToolGroup.FILES, ToolEffect.NON_IDEMPOTENT_WRITE, True, "file_path"
    ),
    "Bash": _ClaudeCodeLocalTool(LocalToolGroup.SHELL, ToolEffect.NON_IDEMPOTENT_WRITE, True, None),
    "WebFetch": _ClaudeCodeLocalTool(LocalToolGroup.WEB_FETCH, ToolEffect.READ, False, None),
}
_RUNNING_TURN: ContextVar[AgentTurnContext] = ContextVar("dex_agents_claude_sdk_cli_turn")
# OSC, then CSI (Claude Code resets the cursor with one on exit), then other escapes.
_TERMINAL_CONTROL_SEQUENCE = re.compile(
    r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)"
    r"|\x1b\[[0-?]*[ -/]*[@-~]"
    r"|\x1b[ -/]*[0-~]"
    r"|[\x00-\x08\x0b-\x1f\x7f]"
)
_GLOB_CHARACTERS = frozenset("*?[{")
# The input field of each file tool that holds a glob pattern.
_GLOB_PATTERN_ARGUMENTS = {"Glob": "pattern", "Grep": "glob"}


class ClaudeSdkHarness:
    """The ``dex-agents-claude-sdk`` command: a Claude Agent SDK agent in the terminal.

    Claude Code's own Read, Grep, Glob, Write, Edit, Bash, and WebFetch tools
    replace the CLI core's ``files``, ``shell``, and ``web_fetch`` groups; the
    CLI core's browser tools and connector tools stay. Each turn offers only
    the Claude Code tools whose group the turn switches on, plus, in a
    resumed segment, the tools of calls the turn already recorded, so their
    records replay; new calls of a switched-off tool are refused. The
    workspace is the working directory. Reads run at once, Write, Edit, and
    Bash wait for approval, and a Bash command whose whole text is in the
    shell allow list runs at once. File tools refuse paths outside the
    workspace.

    The ``reasoning_effort`` session setting accepts ``low``, ``medium``,
    ``high``, ``xhigh``, and ``max``. Each turn segment passes it to Claude
    Code as ``ClaudeAgentOptions.effort``. Without it, Claude Code's default
    applies. ``suggested_models`` lists Claude models for the terminal UI's
    ``/model``.

    .. versionchanged:: 0.6.2
       The ``reasoning_effort`` session setting and the suggested models.
    """

    name = "claude-sdk"
    config_section = "claude"
    flow_type = CLAUDE_SDK_CLI_FLOW_TYPE
    default_model: str | None = DEFAULT_CLAUDE_MODEL
    replaced_local_tools = frozenset(
        {LocalToolGroup.FILES, LocalToolGroup.SHELL, LocalToolGroup.WEB_FETCH}
    )
    supported_reasoning_efforts = ("low", "medium", "high", "xhigh", "max")
    suggested_models = (
        "claude-opus-5-5",
        "claude-sonnet-5-5",
        "claude-haiku-5-5",
        "claude-fable-5-2",
    )

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        """Build the Claude agent session Flow from the effective configuration.

        Args:
            context: The configuration, workspace, and tool sources.

        Returns:
            The Flow, with ``context.flow_type`` as its type.

        Raises:
            ConfigError: If the ``claude`` section has a key; it has no options yet.
        """
        validate_claude_section(context.harness_options)
        local_tools = ClaudeCodeLocalTools(context)
        runner = _ReauthorizingClaudeAgentRunner(
            context,
            local_tools,
            local_tools.build_options,
            tool_sources=context.tool_sources,
            claude_code_tool_permission=local_tools.decide_tool_call,
            media_store=context.media_store,
        )
        return AgentSessionFlow(
            runner,
            flow_type=context.flow_type,
            options=AgentSessionOptions(
                tool_policies={},
                default_tool_effect=ToolEffect.UNCLASSIFIED,
                tracing=AgentTracingOptions(),
                run_model_limit=RunModelLimit(),
                summary_attributes_indexed=context.is_session_summary_indexed,
            ),
        )


def validate_claude_section(options: Mapping[str, Any]) -> None:
    """Reject every key of the ``claude`` section, which has no options yet.

    Args:
        options: The section, ``{}`` when the file omits it.

    Raises:
        ConfigError: ``schema`` with the first key's JSON Pointer.
    """
    for key in sorted(options):
        escaped = key.replace("~", "~0").replace("/", "~1")
        raise ConfigError("schema", f"/claude/{escaped}", keyword="additionalProperties")


class ClaudeCodeLocalTools:
    """Apply the CLI core's local tool rules to Claude Code's own tools."""

    def __init__(self, context: HarnessContext) -> None:
        """Bind the rules to one command's configuration and workspace.

        Args:
            context: The harness context.
        """
        self._context = context
        self._workspace = Path(os.path.realpath(context.workspace))

    def build_options(self, options_context: ClaudeAgentOptionsContext) -> ClaudeAgentOptions:
        """Return one turn segment's options: the turn's model, instructions, and tools.

        .. versionchanged:: 0.6.0
           The options load no MCP servers or claude.ai connectors from the
           user's Claude Code configuration or login.

        .. versionchanged:: 0.6.2
           The options carry the turn's ``reasoning_effort`` as ``effort``.

        Args:
            options_context: The segment's Flow ID, turn ID, and attempt.

        Returns:
            Options with the turn's reasoning effort as ``effort``, ``None``
            without one, the Claude Code tools the turn switches on and the
            tools of calls the turn already recorded, the workspace as
            ``cwd``, no settings files, and no MCP servers or claude.ai
            connectors from the user's Claude Code configuration. The environment
            passes through unchanged, so ``ANTHROPIC_API_KEY`` or the
            machine's Claude Code login reaches the CLI. The CLI's standard
            error goes to the command's log file, never to the chat terminal.

        Raises:
            RuntimeError: Outside a model call, which never happens.
        """
        turn = current_turn_config()
        turn_context = _RUNNING_TURN.get(None)
        if turn is None or turn_context is None:
            raise RuntimeError("the Claude options need the running turn's configuration")
        # A resumed CLI must find a recorded call's tool to replay it; new calls stay denied.
        recorded_tool_names = {record.tool_name for record in turn_context.tool_calls.values()}
        return ClaudeAgentOptions(
            model=turn.model,
            effort=cast(EffortLevel | None, turn.reasoning_effort),
            system_prompt=turn.instructions or DEFAULT_INSTRUCTIONS,
            tools=[
                tool_name
                for tool_name, tool in CLAUDE_CODE_LOCAL_TOOLS.items()
                if is_local_tool_group_enabled(turn, tool.group) or tool_name in recorded_tool_names
            ],
            cwd=str(self._context.workspace),
            # Settings files could allow tools before the session's policy sees them.
            setting_sources=[],
            # External tools come only from the Dex connectors, never the user's MCP config.
            strict_mcp_config=True,
            settings=json.dumps({"disableClaudeAiConnectors": True}, separators=(",", ":")),
            # An inherited terminal would get the CLI's control sequences in the chat.
            stderr=ClaudeCodeStderrLog.for_command_log().write_line,
        )

    def decide_tool_call(
        self, turn: AgentTurnContext, tool_name: str, tool_input: Mapping[str, Any]
    ) -> ToolPolicy | ClaudeCodeToolDenial | None:
        """Decide one call of a Claude Code tool in the model call.

        Args:
            turn: The ``RunModel`` context.
            tool_name: The tool's Claude Code name.
            tool_input: The call's input.

        Returns:
            The call's policy, or a denial for a switched-off tool or a path
            outside the workspace.
        """
        turn_config = self._context.resolve_turn(turn.session_settings)
        tool = CLAUDE_CODE_LOCAL_TOOLS.get(tool_name)
        if tool is None:
            return ClaudeCodeToolDenial(f"{tool_name} is not available in this session")
        refusal = self.refusal_reason(turn_config, tool_name, tool_input)
        if refusal is not None:
            return ClaudeCodeToolDenial(refusal)
        is_approval_required = tool.is_approval_required
        if tool_name == "Bash":
            # A background command outlives the turn, so it always waits for approval.
            is_approval_required = (
                is_shell_command_approval_required(turn_config, str(tool_input.get("command", "")))
                or tool_input.get("run_in_background") is True
            )
        return resolve_turn_tool_policy(
            turn, tool_name, tool.effect, is_approval_required, turn_config
        )

    def refusal_reason(
        self, turn_config: EffectiveConfig, tool_name: str, tool_input: Mapping[str, Any]
    ) -> str | None:
        """Return why a call of a Claude Code tool must not run, or ``None``.

        Args:
            turn_config: The configuration of the call's turn.
            tool_name: The tool's Claude Code name.
            tool_input: The call's input.

        Returns:
            The reason when the turn switches the tool's group off or a file
            tool names a path outside the workspace; ``None`` otherwise.
        """
        tool = CLAUDE_CODE_LOCAL_TOOLS.get(tool_name)
        if tool is None:
            return None
        if not is_local_tool_group_enabled(turn_config, tool.group):
            return f"{tool_name} is switched off for this session"
        paths = []
        if tool.path_argument is not None and isinstance(tool_input.get(tool.path_argument), str):
            paths.append(str(tool_input[tool.path_argument]))
        pattern_argument = _GLOB_PATTERN_ARGUMENTS.get(tool_name)
        pattern = None if pattern_argument is None else tool_input.get(pattern_argument)
        if isinstance(pattern, str):
            # Brace expansion can produce .. from any part of a pattern, so none may contain it.
            if ".." in pattern:
                return f"{tool_name} may only use paths inside the workspace {self._workspace}"
            paths.append(_glob_base(pattern))
        for path in paths:
            if not self._is_inside_workspace(path):
                return f"{tool_name} may only use paths inside the workspace {self._workspace}"
        return None

    def _is_inside_workspace(self, path_text: str) -> bool:
        if ".." in Path(path_text).parts:
            return False
        candidate = Path(os.path.expanduser(path_text))
        if not candidate.is_absolute():
            candidate = self._workspace / candidate
        # realpath also follows a dangling link to the target it would create.
        resolved = Path(os.path.realpath(candidate))
        return resolved == self._workspace or self._workspace in resolved.parents


class ClaudeCodeStderrLog:
    """Append the Claude Code CLI's standard error lines to the command's log file.

    Each line loses its terminal control sequences and goes to the log in the
    command's log format. A failed write drops the line: unlike a logging
    handler, it never reports the failure on the chat terminal.
    """

    def __init__(self, log_path: Path | None, formatter: logging.Formatter) -> None:
        """Write to one log file.

        Args:
            log_path: The log file, or ``None`` to drop every line.
            formatter: The format of each line, the log file's own.
        """
        self._log_path = log_path
        self._formatter = formatter

    @classmethod
    def for_command_log(cls) -> ClaudeCodeStderrLog:
        """Return a log that writes to the file of the root logger's file handler.

        Returns:
            A log for that file in its handler's format, or one that drops
            every line when the root logger has no file handler.
        """
        for handler in logging.getLogger().handlers:
            if isinstance(handler, logging.FileHandler):
                return cls(Path(handler.baseFilename), handler.formatter or logging.Formatter())
        return cls(None, logging.Formatter())

    def write_line(self, line: str) -> None:
        """Append one standard error line, without its control sequences.

        Args:
            line: The line as the CLI wrote it. A line that is empty without
                its control sequences is dropped.
        """
        text = _TERMINAL_CONTROL_SEQUENCE.sub("", line).strip()
        if not text or self._log_path is None:
            return
        record = logging.LogRecord(
            __name__, logging.INFO, __file__, 0, "Claude Code stderr: %s", (text,), None
        )
        try:
            with self._log_path.open("a", encoding="utf-8") as log_file:
                log_file.write(self._formatter.format(record) + "\n")
        except OSError:
            # The chat terminal must never show this line, so a failed write loses it.
            return


def _glob_base(pattern: str) -> str:
    """Return the leading directories of a glob pattern, before its first glob character."""
    parts: list[str] = []
    for part in Path(pattern).parts:
        if _GLOB_CHARACTERS & set(part):
            break
        parts.append(part)
    return str(Path(*parts)) if parts else "."


class _ReauthorizingClaudeAgentRunner(ClaudeAgentRunner):
    """Give the options their turn, and check an approved Claude Code tool call in its tool Step."""

    def __init__(
        self,
        context: HarnessContext,
        local_tools: ClaudeCodeLocalTools,
        *args: Any,
        **kwargs: Any,
    ) -> None:
        super().__init__(*args, **kwargs)
        self._context = context
        self._local_tools = local_tools

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        token = _RUNNING_TURN.set(turn)
        try:
            return await super().run_model_step(turn)
        finally:
            _RUNNING_TURN.reset(token)

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        tool_name = call.tool_call.tool_name
        if tool_name in CLAUDE_CODE_LOCAL_TOOLS:
            permissions = self._context.resolve_tool_step_permissions(call)
            refusal = self._local_tools.refusal_reason(
                permissions, tool_name, _tool_input(call.tool_call.arguments)
            )
            if refusal is not None:
                raise ToolExecutionError(f"{refusal}; the call did not run")
        return await super().execute_deferred_tool_call(call)


def _tool_input(arguments: str) -> Mapping[str, Any]:
    parsed = json.loads(arguments)
    return parsed if isinstance(parsed, dict) else {}
