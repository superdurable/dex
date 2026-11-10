# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The harness plugin interface: what a harness command gives the shared runtime."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from enum import StrEnum
from pathlib import Path
from typing import Any, Protocol

from dex_agents import AgentSessionFlow
from dex_agents.stepwise import ToolSource
from dex_agents_cli.config import EffectiveConfig
from dex_agents_cli.turns import ResolvedTurn, TurnConfigResolver


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
        adapter passes them as ``tool_sources``, and a Stepwise harness puts
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
        ``AgentTurnContext.session_settings`` or a Stepwise ``resolve_model``
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
