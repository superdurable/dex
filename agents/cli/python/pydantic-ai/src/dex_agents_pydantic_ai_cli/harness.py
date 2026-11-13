# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Pydantic AI harness: a Pydantic AI agent with the CLI core's tools."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, cast

import dex_agents_cli_core  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from pydantic_ai import Agent
from pydantic_ai.models import Model, infer_model
from pydantic_ai.models.wrapper import WrapperModel
from pydantic_ai.settings import ModelSettings, ThinkingLevel, merge_model_settings

from dex_agents import AgentSessionFlow
from dex_agents_cli_core import ConfigError, HarnessContext, LocalToolGroup, current_turn_config
from dex_agents_pydantic_ai import pydantic_ai_session_flow

#: The Flow type of the command's sessions, before the CLI core namespaces it.
PYDANTIC_AI_CLI_FLOW_TYPE = "DexAgentsPydanticAiCliSession"

#: The Pydantic AI model the command uses when the configuration file sets none.
DEFAULT_PYDANTIC_AI_MODEL = "openai:gpt-5.2"

#: The instructions the agent gets when the configuration sets none.
DEFAULT_INSTRUCTIONS = (
    "You are a helpful assistant. Use your tools to work with the files and commands "
    "in your workspace and with the connected services."
)


class PydanticAiHarness:
    """The ``dex-agents-pydantic-ai`` command: a Pydantic AI agent in the terminal.

    Every model call uses the turn's model, a Pydantic AI model name such as
    ``openai:gpt-5.2`` or ``anthropic:claude-opus-5-5``, and the turn's
    instructions. The provider reads its standard environment variables. The
    CLI core's connector and local tools are the agent's only tools.

    The ``reasoning_effort`` session setting accepts ``low``, ``medium``, and
    ``high``. Each model call of its turn sends it as Pydantic AI's unified
    ``thinking`` model setting, which each provider maps to its own reasoning
    or thinking option. Without it, the model's default applies.
    ``suggested_models`` lists OpenAI and Anthropic models for the terminal
    UI's ``/model``.

    .. versionchanged:: 0.6.2
       The ``reasoning_effort`` session setting and the suggested models.
    """

    name = "pydantic-ai"
    config_section = "pydantic_ai"
    flow_type = PYDANTIC_AI_CLI_FLOW_TYPE
    default_model: str | None = DEFAULT_PYDANTIC_AI_MODEL
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset()
    supported_reasoning_efforts = ("low", "medium", "high")
    suggested_models = (
        "openai:gpt-5.2",
        "openai:gpt-5-mini",
        "anthropic:claude-opus-5-5",
        "anthropic:claude-sonnet-5-5",
    )

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        """Build the Pydantic AI agent session Flow from the effective configuration.

        Args:
            context: The configuration, workspace, and tool sources.

        Returns:
            The Flow, with ``context.flow_type`` as its type.

        Raises:
            ConfigError: If the ``pydantic_ai`` section has a key; it has no options yet.
        """
        validate_pydantic_ai_section(context.harness_options)

        def instructions() -> str:
            return context.current_turn().instructions or DEFAULT_INSTRUCTIONS

        agent: Agent[None, str] = Agent(
            TurnModel(context.config.model), instructions=instructions, name="pydantic-ai"
        )
        return pydantic_ai_session_flow(
            agent,
            flow_type=context.flow_type,
            tool_sources=context.tool_sources,
            media_store=context.media_store,
            summary_attributes_indexed=context.is_session_summary_indexed,
        )


class TurnModel(WrapperModel):
    """Call the running turn's model, so session settings can change it between turns.

    Outside a turn, such as in a compaction summary call, it calls the file's model.
    Its ``settings`` add the turn's ``reasoning_effort`` as ``thinking``.

    .. versionchanged:: 0.6.2
       The turn's ``reasoning_effort`` reaches every model call as ``thinking``.
    """

    def __init__(self, file_model: str) -> None:
        """Create the model without resolving any provider yet.

        Args:
            file_model: The configuration file's model name.
        """
        Model.__init__(self)
        self._file_model = file_model
        self._models: dict[str, Model] = {}

    @property
    def wrapped(self) -> Model:  # type: ignore[override]
        """Return the running turn's model, resolved once per name."""
        turn = current_turn_config()
        model_name = self._file_model if turn is None else turn.model
        model = self._models.get(model_name)
        if model is None:
            model = infer_model(model_name)
            self._models[model_name] = model
        return model

    @property
    def settings(self) -> ModelSettings | None:
        """Return the wrapped model's settings, with the turn's reasoning effort as ``thinking``.

        The agent merges these into every request of the turn.
        """
        turn = current_turn_config()
        if turn is None or turn.reasoning_effort is None:
            return self.wrapped.settings
        thinking = cast(ThinkingLevel, turn.reasoning_effort)
        return merge_model_settings(self.wrapped.settings, ModelSettings(thinking=thinking))


def validate_pydantic_ai_section(options: Mapping[str, Any]) -> None:
    """Reject every key of the ``pydantic_ai`` section, which has no options yet.

    Args:
        options: The section, ``{}`` when the file omits it.

    Raises:
        ConfigError: ``schema`` with the first key's JSON Pointer.
    """
    for key in sorted(options):
        escaped = key.replace("~", "~0").replace("/", "~1")
        raise ConfigError("schema", f"/pydantic_ai/{escaped}", keyword="additionalProperties")
