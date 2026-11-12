# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The built-in harness: a Dex built-in loop agent on LiteLLM with the CLI core's tools."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import replace
from typing import Any

import dex_agents_cli_core  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from dex_agents import AgentSessionFlow, BuiltInTool
from dex_agents.builtin_loop import (
    AgentDefinition,
    InstructionContext,
    ModelClient,
    ModelOutputWriter,
    ModelRequest,
    ModelResponse,
    ModelSettings,
    builtin_agent_session_flow,
)
from dex_agents.litellm import LiteLLMModelClient
from dex_agents_cli_core import ConfigError, HarnessContext, LocalToolGroup

#: The Flow type of the command's sessions, before the CLI core namespaces it.
BUILTIN_CLI_FLOW_TYPE = "DexAgentsBuiltinCliSession"

#: The LiteLLM model the command uses when the configuration file sets none.
DEFAULT_BUILTIN_MODEL = "openai/gpt-5.2"

#: The instructions the agent gets when the configuration sets none.
DEFAULT_INSTRUCTIONS = (
    "You are a helpful assistant. Use your tools to work with the files and commands "
    "in your workspace and with the connected services."
)


class BuiltinHarness:
    """The ``dex-agents-builtin`` command: a Dex built-in loop agent in the terminal.

    Every model call is one LiteLLM request with the turn's model, so any
    LiteLLM provider works with its standard environment variables. The
    CLI core's connector and local tools are the agent's tools, with two
    built-in tools: ``ask_user`` asks the user a question, and ``write_todos``
    keeps the session's plan. Plan mode is on, so a ``plan_mode`` message
    drafts a plan with ``write_todos`` and ``execute_plan`` runs it. Both
    built-in names are reserved: no connector or local tool may use them.

    The ``reasoning_effort`` session setting accepts ``low``, ``medium``, and
    ``high``. Each model call of its turn sends it as LiteLLM's
    ``reasoning_effort``, which LiteLLM maps to the provider's own setting,
    such as thinking for Anthropic models. Without it, the call sends none,
    so the provider's default applies. ``suggested_models`` lists OpenAI and
    Anthropic models for the terminal UI's ``/model``.

    .. versionchanged:: 0.6.0
       Plan mode is on, and the agent has the ``write_todos`` built-in tool.

    .. versionchanged:: 0.7.0
       The ``reasoning_effort`` session setting and the suggested models.
    """

    name = "builtin"
    config_section = "builtin"
    flow_type = BUILTIN_CLI_FLOW_TYPE
    default_model: str | None = DEFAULT_BUILTIN_MODEL
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset()
    supported_reasoning_efforts = ("low", "medium", "high")
    suggested_models = (
        "openai/gpt-5.2",
        "openai/gpt-5-mini",
        "anthropic/claude-opus-5-5",
        "anthropic/claude-sonnet-5-5",
    )

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        """Build the built-in loop agent session Flow from the effective configuration.

        Args:
            context: The configuration, workspace, and tool sources.

        Returns:
            The Flow, with ``context.flow_type`` as its type.

        Raises:
            ConfigError: If the ``builtin`` section has a key; it has no options yet.
        """
        validate_builtin_section(context.harness_options)
        models: dict[tuple[str, str | None], ModelClient] = {}

        def resolve_model(session_settings: Mapping[str, Any]) -> ModelClient:
            turn = context.resolve_turn(session_settings)
            key = (turn.model, turn.reasoning_effort)
            if key not in models:
                client = LiteLLMModelClient(turn.model)
                models[key] = (
                    client
                    if turn.reasoning_effort is None
                    else _ReasoningEffortModelClient(client, turn.reasoning_effort)
                )
            return models[key]

        def instructions(instruction_context: InstructionContext) -> str:
            turn = context.resolve_turn(instruction_context.session_settings)
            return turn.instructions or DEFAULT_INSTRUCTIONS

        definition = AgentDefinition(
            name="builtin",
            instructions=instructions,
            model=LiteLLMModelClient(context.config.model),
            resolve_model=resolve_model,
            tool_sources=list(context.tool_sources),
            built_in_tools=frozenset({BuiltInTool.ASK_USER, BuiltInTool.WRITE_TODOS}),
            plan_mode_enabled=True,
        )
        return builtin_agent_session_flow(
            definition,
            flow_type=context.flow_type,
            media_store=context.media_store,
            summary_attributes_indexed=context.is_session_summary_indexed,
        )


class _ReasoningEffortModelClient:
    """Send every request of a LiteLLM model client with one reasoning effort.

    The agent's static ``ModelSettings`` hold no reasoning effort, so the
    turn's ``reasoning_effort`` session setting replaces theirs in each
    request. ``supports_forced_tool_choice`` answers for the same settings,
    because LiteLLM turns a reasoning effort into thinking for Anthropic
    models, which then refuse a forced tool choice. The command's agent
    never forces a tool choice: its ``planning_tool_choice`` is ``"auto"``.
    """

    def __init__(self, client: LiteLLMModelClient, reasoning_effort: str) -> None:
        """Wrap a client.

        Args:
            client: The LiteLLM model client of the turn's model.
            reasoning_effort: The turn's reasoning effort, such as ``high``.
        """
        self._client = client
        self._reasoning_effort = reasoning_effort

    async def call_model(self, request: ModelRequest, output: ModelOutputWriter) -> ModelResponse:
        """Make the request with the reasoning effort.

        Args:
            request: The runner's request.
            output: Writer for text and reasoning deltas.

        Returns:
            The wrapped client's response.
        """
        request_with_effort = replace(request, settings=self._with_effort(request.settings))
        return await self._client.call_model(request_with_effort, output)

    def estimate_input_tokens(self, request: ModelRequest) -> int:
        """Estimate the input tokens, as the wrapped client does.

        Args:
            request: The request to measure.

        Returns:
            The wrapped client's estimate.
        """
        return self._client.estimate_input_tokens(request)

    def supports_forced_tool_choice(self, settings: ModelSettings | None = None) -> bool:
        """Report whether the model accepts a forced tool choice with the reasoning effort.

        Args:
            settings: The agent's model settings; ``None`` uses none.

        Returns:
            The wrapped client's answer for the settings with the reasoning effort.
        """
        return self._client.supports_forced_tool_choice(
            self._with_effort(settings or ModelSettings())
        )

    def _with_effort(self, settings: ModelSettings) -> ModelSettings:
        return replace(settings, reasoning_effort=self._reasoning_effort)


def validate_builtin_section(options: Mapping[str, Any]) -> None:
    """Reject every key of the ``builtin`` section, which has no options yet.

    Args:
        options: The section, ``{}`` when the file omits it.

    Raises:
        ConfigError: ``schema`` with the first key's JSON Pointer.
    """
    for key in sorted(options):
        escaped = key.replace("~", "~0").replace("/", "~1")
        raise ConfigError("schema", f"/builtin/{escaped}", keyword="additionalProperties")
