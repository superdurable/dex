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
from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from typing import Any, Protocol

from dex_agents import AgentSessionFlow
from dex_agents.stepwise import ToolSource
from dex_agents_cli.config import EffectiveConfig
from dex_agents_cli.turns import TurnConfigResolver, current_turn_config


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

    A harness package implements this and calls ``dex_agents_cli.main`` with
    it from its console script. The runtime owns everything else: the
    configuration file, the Dex server, connector and local tools, sessions,
    the Worker, and the terminal chat.

    Example:
        >>> class ClaudeHarness:
        ...     command_name = "dex-agents-claude-sdk"
        ...     config_name = "claude-sdk"
        ...     config_section = "claude"
        ...     flow_type = "DexAgentsClaudeSdk"
        ...     default_model = "claude-opus-5-5"
        ...     replaced_local_tool_groups = frozenset(
        ...         {LocalToolGroup.FILES, LocalToolGroup.SHELL, LocalToolGroup.WEB_FETCH})
        ...
        ...     def build_session_flow(self, context: HarnessBuildContext) -> AgentSessionFlow:
        ...         return claude_agent_session_flow(
        ...             build_options_factory(context), flow_type=self.flow_type,
        ...             tool_sources=context.tool_sources)
        >>> raise SystemExit(main(ClaudeHarness()))
    """

    @property
    def command_name(self) -> str:
        """Return the console script name, such as ``dex-agents-claude-sdk``."""
        ...

    @property
    def config_name(self) -> str:
        """Return the configuration file stem: ``~/.dex/agents/<config_name>.yaml``."""
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
    def replaced_local_tool_groups(self) -> frozenset[LocalToolGroup]:
        """Return the local tool groups the harness provides itself.

        The runtime leaves these groups out of its local tool source. The
        harness must still honor ``local_tools`` and the workspace for them,
        reading each turn's switches from ``current_turn_config``.
        """
        ...

    def build_session_flow(self, context: HarnessBuildContext) -> AgentSessionFlow:
        """Build the agent session Flow from the effective configuration.

        Called once per command run, before the Worker starts. Offer every
        source in ``context.tool_sources`` to the model; a runner-based
        adapter passes them as ``tool_sources``, and a Stepwise harness puts
        them in ``AgentDefinition.tool_sources``. Choose each turn's model and
        instructions from ``context.turn_config()``.

        Args:
            context: The configuration, workspace, and tool sources.

        Returns:
            The Flow, with ``flow_type`` as its type. The runtime registers it.
        """
        ...


@dataclass(frozen=True)
class HarnessBuildContext:
    """Everything a harness needs to build its session Flow.

    Attributes:
        config: The configuration file's effective configuration. It bounds
            every turn; read a turn's own values with ``turn_config``.
        config_path: The configuration file, or ``None`` when defaults are used.
        workspace: The expanded workspace directory, which exists.
        tool_sources: The runtime's connector and local tool sources, already
            narrowed per turn. Offer all of them.
        system_prompt_file_text: The text of ``system_prompt_file``, read at
            startup, or ``None``.
        resolver: The turn configuration resolver.
    """

    config: EffectiveConfig
    config_path: Path | None
    workspace: Path
    tool_sources: Sequence[ToolSource]
    system_prompt_file_text: str | None
    resolver: TurnConfigResolver

    @property
    def harness_options(self) -> Mapping[str, Any]:
        """Return the harness's own configuration section, ``{}`` when the file omits it."""
        return self.config.harness_options

    def turn_config(self, session_settings: Mapping[str, Any] | None = None) -> EffectiveConfig:
        """Return the configuration of the running turn, or of some session settings.

        Inside a model call with no argument, it is the running turn's
        configuration. Pass ``session_settings`` where a turn context gives
        them but no model call runs, such as a Stepwise ``resolve_model``.

        Args:
            session_settings: Settings to resolve; ``None`` reads the running turn.

        Returns:
            The turn's configuration.

        Raises:
            SessionSettingsRejectedError: If the given settings are rejected.
            RuntimeError: If no settings are given outside a model call. Read
                the file's bounds from ``config`` instead; a turn never
                silently gets them.
        """
        if session_settings is not None:
            return self.resolver.resolve(session_settings).config
        turn_config = current_turn_config()
        if turn_config is None:
            raise RuntimeError(
                "turn_config() without session settings works only inside a model call"
            )
        return turn_config

    def instructions(self, turn_config: EffectiveConfig) -> str | None:
        """Return a turn's instructions.

        Args:
            turn_config: The turn's configuration.

        Returns:
            ``system_prompt``, else the text of ``system_prompt_file``, else
            ``None`` for the harness's own default instructions.
        """
        if turn_config.system_prompt is not None:
            return turn_config.system_prompt
        if turn_config.system_prompt_file is not None:
            return self.system_prompt_file_text
        return None
