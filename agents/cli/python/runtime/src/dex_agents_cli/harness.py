# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The harness plugin interface: what a harness command gives the shared runtime."""

from __future__ import annotations

import getpass
import hashlib
import os
import socket
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from enum import StrEnum
from pathlib import Path
from typing import Any, Protocol

from dex_agents import AgentSessionFlow, DeferredToolExecution
from dex_agents.builtin_loop import ToolSource
from dex_agents_cli.config import EffectiveConfig
from dex_agents_cli.turns import (
    ResolvedTurn,
    TurnConfigResolver,
    current_turn_config,
    recorded_turn_settings,
)

_FLOW_TYPE_SUFFIX_DIGITS = 12


class LocalToolGroup(StrEnum):
    """Name one group of local tools, as ``local_tools`` in the configuration switches them.

    Attributes:
        FILES: Read, write, edit, list, and search files in the workspace.
        SHELL: Run shell commands in the workspace.
        WEB_FETCH: Fetch web pages.
        BROWSER: Drive a local browser and read its pages as text snapshots.
    """

    FILES = "files"
    SHELL = "shell"
    WEB_FETCH = "web_fetch"
    BROWSER = "browser"


class AgentHarness(Protocol):
    """Describe one harness command and build its agent session Flow.

    A harness package implements this and calls ``run_agent_cli`` with it from
    its console script. The runtime owns everything else: the configuration
    file, the Dex server, connector and local tools, sessions, the Worker, and
    the terminal chat.

    A harness whose runner fixes its tools when it is built, such as one on
    the OpenAI Agents SDK adapter, also sets the optional class attribute
    ``is_tool_set_fixed_at_build = True``. Such a runner offers only the tools
    that the sources cache before the Flow is built: the tools the
    configuration file enables and the connectors MCP server lists at
    startup. When that listing fails, the command warns that the run has no
    connector tools and that a restart brings them back.

    Example:
        >>> class ClaudeHarness:
        ...     name = "claude-sdk"
        ...     config_section = "claude"
        ...     flow_type = "DexAgentsClaudeSdkCliSession"
        ...     default_model = "claude-opus-5-5"
        ...     replaced_local_tools = frozenset(
        ...         {LocalToolGroup.FILES, LocalToolGroup.SHELL, LocalToolGroup.WEB_FETCH})
        ...
        ...     def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        ...         return claude_agent_session_flow(
        ...             options_factory_for(context), flow_type=context.flow_type,
        ...             tool_sources=context.tool_sources)
        >>> raise SystemExit(run_agent_cli(ClaudeHarness()))
    """

    @property
    def name(self) -> str:
        """Return the name: command ``dex-agents-<name>``, file ``~/.dex/agents/<name>.yaml``."""
        ...

    @property
    def config_section(self) -> str:
        """Return the harness section of the configuration file, such as ``claude``."""
        ...

    @property
    def flow_type(self) -> str:
        """Return the Flow type of the sessions. Keep it stable: sessions resume by it."""
        ...

    @property
    def default_model(self) -> str | None:
        """Return the model used when the file sets none, or ``None`` to require one."""
        ...

    @property
    def replaced_local_tools(self) -> frozenset[LocalToolGroup]:
        """Return the local tool groups the harness provides itself.

        The runtime leaves these groups out of ``HarnessContext.tool_sources``.
        The harness switches its own tools on from ``config.local_tools`` and
        gives them the same policy: reads run at once, writes and shell
        commands wait for approval unless the shell allow list has the exact
        command, and the working directory is the workspace.
        """
        ...

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        """Build the agent session Flow from the effective configuration.

        Called once per command run, before the Worker starts. Offer every
        source in ``context.tool_sources`` to the model; a runner-based
        adapter passes them as ``tool_sources``, and a built-in loop harness puts
        them in ``AgentDefinition.tool_sources``. Choose each model call's
        model and instructions with ``context.resolve_turn``.

        Args:
            context: The configuration, workspace, and tool sources.

        Returns:
            The Flow, with ``context.flow_type`` as its type. The runtime registers it.
        """
        ...


@dataclass(frozen=True)
class HarnessContext:
    """Everything a harness needs to build its session Flow.

    Attributes:
        flow_type: The harness's Flow type.
        config: The configuration file's effective configuration, with any
            ``--model`` applied. It bounds every turn.
        workspace: The absolute workspace directory, which exists.
        instructions: The file's ``system_prompt``, or the text of its
            ``system_prompt_file``, or ``None`` for the harness's default.
        harness_options: The harness's own configuration section, ``{}`` when omitted.
        tool_sources: The connector and local tool sources, which apply each
            turn's session settings themselves. Offer all of them.
        resolver: The turn configuration resolver behind ``resolve_turn``.
    """

    flow_type: str
    config: EffectiveConfig
    workspace: Path
    instructions: str | None
    harness_options: Mapping[str, Any]
    tool_sources: Sequence[ToolSource]
    resolver: TurnConfigResolver = field(repr=False)

    def resolve_turn(self, session_settings: Mapping[str, Any] | None) -> ResolvedTurn:
        """Return a turn's model, instructions, and narrowed configuration.

        Call it in each model call with the turn's session settings, such as
        ``AgentTurnContext.session_settings`` or a built-in loop ``resolve_model``
        argument.

        Example:
            >>> turn = context.resolve_turn(instruction_context.session_settings)
            >>> instructions = turn.instructions or "You are a helpful assistant."

        Args:
            session_settings: The turn's settings; ``None`` or ``{}`` for none.

        Returns:
            The turn's configuration and instructions.

        Raises:
            ConfigError: If the settings fail the schema or widen the file, so
                the model call fails with ``<code> at "<pointer>"``.
        """
        return self.resolver.resolve(session_settings)

    def current_turn(self) -> ResolvedTurn:
        """Return the turn whose model call or tool Step is running, or the file's outside both.

        A harness that replaces local tool groups reads it in its own tools:
        in an approval hook during the model call, and in the tool's own Step,
        where it is the configuration of the turn that made the call, with
        that turn's switches, shell allow list, and ``approval.always``. Use it
        with ``is_local_tool_group_enabled``, ``is_shell_command_approval_required``,
        and ``resolve_turn_tool_policy``.

        Example:
            >>> @function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE)
            ... async def run_command(command: str) -> str:
            ...     turn = context.current_turn()
            ...     if not is_local_tool_group_enabled(turn, LocalToolGroup.SHELL):
            ...         raise ToolExecutionError("shell commands are switched off for this session")
            ...     return await run(command, cwd=context.workspace)

        Returns:
            The turn's configuration and instructions.
        """
        return current_turn_config() or self.resolver.resolve(None)

    def resolve_tool_step_permissions(
        self, call: DeferredToolExecution | None = None
    ) -> ResolvedTurn:
        """Return the permissions a replacement tool's own Step must apply.

        They are the configuration of the turn that made the call: this
        Worker's configuration file narrowed by the session settings that the
        session recorded for that turn, with its local tool switches, shell
        allow list, and ``approval.always``. A Worker other than the one that
        built the call, with another file, applies its own file. Check them in
        the tool Step before the tool takes any effect, and refuse the call
        when they switch the tool off.

        Example:
            >>> @function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE)
            ... async def run_command(command: str) -> str:
            ...     permissions = context.resolve_tool_step_permissions()
            ...     if not is_local_tool_group_enabled(permissions, LocalToolGroup.SHELL):
            ...         raise ToolExecutionError("shell commands are switched off for this session")
            ...     return await run(command, cwd=context.workspace)

        Args:
            call: The tool Step's call, for a runner's ``execute_deferred_tool_call``.
                ``None`` reads the tool Step or model call running in this
                task, as a built-in loop function tool does.

        Returns:
            The turn's configuration and instructions.

        Raises:
            RuntimeError: If no call is given outside a tool Step and a model call.
            ConfigError: If the recorded settings are rejected.
        """
        if call is not None:
            return self.resolver.resolve(recorded_turn_settings(call))
        return current_turn_config() or _no_running_turn()


def _no_running_turn() -> ResolvedTurn:
    raise RuntimeError(
        "resolve_tool_step_permissions needs a tool Step or a model call; pass the call"
    )


def session_flow_type(
    harness_flow_type: str,
    config_path: Path | None,
    workspace: Path,
    *,
    user: str | None = None,
) -> str:
    """Return the Flow type a command registers: the harness's, namespaced by environment.

    Commands with the same harness but another machine, user, configuration
    file, or workspace register different Flow types against one Dex server.
    Their Workers then never take each other's tool Steps, so an approved
    write always runs with the workspace, tools, and connections it was
    approved under. ``sessions`` and ``--resume`` use the same type.

    The suffix is the first 12 hex digits of the SHA-256 of the UTF-8 text
    ``"<hostname>\\n<user>\\n<config identity>\\n<workspace>"``: the user is
    the numeric user ID where the system has one, else the user name; the
    config identity is the configuration file's real path, or ``defaults``
    without a file; the workspace is its real path. The file's contents,
    such as tool settings, do not count, so editing them keeps the sessions.

    Example:
        >>> session_flow_type("DexAgentsClaudeSdkCliSession", config_path, workspace)
        'DexAgentsClaudeSdkCliSession-3f9a1c0b7d2e'

    Args:
        harness_flow_type: The harness's ``flow_type``.
        config_path: The configuration file, or ``None`` when the command runs on defaults.
        workspace: The workspace directory.
        user: The user part; ``None`` uses this process's user.

    Returns:
        ``<harness_flow_type>-<12 hex digits>``.
    """
    config_identity = "defaults" if config_path is None else os.path.realpath(config_path)
    user_identity = _current_user() if user is None else user
    text = (
        f"{socket.gethostname()}\n{user_identity}\n{config_identity}\n{os.path.realpath(workspace)}"
    )
    digest = hashlib.sha256(text.encode("utf-8")).hexdigest()
    return f"{harness_flow_type}-{digest[:_FLOW_TYPE_SUFFIX_DIGITS]}"


def _current_user() -> str:
    get_user_id = getattr(os, "getuid", None)
    return str(get_user_id()) if get_user_id is not None else getpass.getuser()
