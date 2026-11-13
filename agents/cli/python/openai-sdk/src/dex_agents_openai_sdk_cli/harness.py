# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The OpenAI Agents SDK harness: an SDK agent with the CLI core's tools."""

from __future__ import annotations

from collections.abc import AsyncIterator, Mapping
from dataclasses import replace
from typing import Any

import dex_agents_cli_core  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from agents import (
    Agent,
    AgentOutputSchemaBase,
    Handoff,
    Model,
    ModelProvider,
    ModelResponse,
    ModelSettings,
    ModelTracing,
    MultiProvider,
    RunConfig,
    RunContextWrapper,
    Tool,
    TResponseInputItem,
)
from agents.items import TResponseStreamEvent
from agents.retry import ModelRetryAdvice, ModelRetryAdviceRequest
from openai.types.responses.response_prompt_param import ResponsePromptParam
from openai.types.shared import Reasoning

from dex_agents import AgentSessionFlow
from dex_agents_cli_core import ConfigError, HarnessContext, LocalToolGroup, current_turn_config
from dex_agents_openai_agents import openai_agents_session_flow

#: The Flow type of the command's sessions, before the CLI core namespaces it.
OPENAI_SDK_CLI_FLOW_TYPE = "DexAgentsOpenAISdkCliSession"

#: The model the command uses when the configuration file sets none.
DEFAULT_OPENAI_MODEL = "gpt-5.2"

#: The instructions the agent gets when the configuration sets none.
DEFAULT_INSTRUCTIONS = (
    "You are a helpful assistant. Use your tools to work with the files and commands "
    "in your workspace and with the connected services."
)


class OpenAISdkHarness:
    """The ``dex-agents-openai-sdk`` command: an OpenAI Agents SDK agent in the terminal.

    Each model call uses the turn's model through the SDK's default model
    provider, so ``OPENAI_API_KEY`` and ``OPENAI_BASE_URL`` apply, and a
    ``litellm/`` prefix needs LiteLLM installed. The CLI core's connector and
    local tools are the agent's only tools. The SDK's own tracing is off, so
    no conversation leaves the machine for the OpenAI traces dashboard.

    The ``reasoning_effort`` session setting accepts ``low``, ``medium``, and
    ``high``. Each model call of its turn sends it as the SDK's
    ``ModelSettings.reasoning.effort``. Without it, the call sends none, so
    the model's default applies. ``suggested_models`` lists OpenAI models
    for the terminal UI's ``/model``.

    .. versionchanged:: 0.6.2
       The ``reasoning_effort`` session setting and the suggested models.
    """

    name = "openai-sdk"
    config_section = "openai"
    flow_type = OPENAI_SDK_CLI_FLOW_TYPE
    default_model: str | None = DEFAULT_OPENAI_MODEL
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset()
    # The SDK fixes an agent's tools when the runner is built.
    is_tool_set_fixed_at_build = True
    supported_reasoning_efforts = ("low", "medium", "high")
    suggested_models = ("gpt-5.2", "gpt-5-mini", "gpt-5-nano")

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        """Build the OpenAI Agents SDK agent session Flow from the effective configuration.

        The runner offers the connector tools the file enables when the
        command starts; a connection authorized later needs a restart.

        Args:
            context: The configuration, workspace, and tool sources.

        Returns:
            The Flow, with ``context.flow_type`` as its type.

        Raises:
            ConfigError: If the ``openai`` section has a key; it has no options yet.
        """
        validate_openai_section(context.harness_options)

        def instructions(run_context: RunContextWrapper[Any], agent: Agent[Any]) -> str:
            return context.current_turn().instructions or DEFAULT_INSTRUCTIONS

        agent: Agent[Any] = Agent(
            name="assistant", instructions=instructions, model=context.config.model
        )
        return openai_agents_session_flow(
            agent,
            flow_type=context.flow_type,
            run_config=RunConfig(model_provider=TurnModelProvider(), tracing_disabled=True),
            tool_sources=context.tool_sources,
            media_store=context.media_store,
            summary_attributes_indexed=context.is_session_summary_indexed,
        )


class TurnModelProvider(ModelProvider):
    """Give every model call the running turn's model and reasoning effort.

    Session settings may change both between turns.

    .. versionchanged:: 0.6.2
       The model carries the turn's ``reasoning_effort``.
    """

    def __init__(self) -> None:
        """Wrap the SDK's default ``MultiProvider``."""
        self._provider = MultiProvider()

    def get_model(self, model_name: str | None) -> Model:
        """Return the running turn's model; ``model_name`` is the file's.

        Args:
            model_name: The agent's model, the configuration file's.

        Returns:
            The SDK model for the turn's model name. With a turn
            ``reasoning_effort``, every call of the model sends it as
            ``ModelSettings.reasoning.effort``.
        """
        turn = current_turn_config()
        model = self._provider.get_model(turn.model if turn is not None else model_name)
        if turn is None or turn.reasoning_effort is None:
            return model
        return _ReasoningEffortModel(model, turn.reasoning_effort)


class _ReasoningEffortModel(Model):
    """Make every call of an SDK model with one reasoning effort."""

    def __init__(self, model: Model, reasoning_effort: str) -> None:
        self._model = model
        self._reasoning_effort = reasoning_effort

    def __getattr__(self, name: str) -> Any:
        # The SDK probes optional model members, such as the prompt cache key support, by name.
        if name == "_model":
            raise AttributeError(name)
        return getattr(self._model, name)

    async def _cleanup_on_run_end(self, owner: object) -> None:
        await self._model._cleanup_on_run_end(owner)

    async def close(self) -> None:
        await self._model.close()

    def get_retry_advice(self, request: ModelRetryAdviceRequest) -> ModelRetryAdvice | None:
        return self._model.get_retry_advice(request)

    async def get_response(
        self,
        system_instructions: str | None,
        input: str | list[TResponseInputItem],
        model_settings: ModelSettings,
        tools: list[Tool],
        output_schema: AgentOutputSchemaBase | None,
        handoffs: list[Handoff],
        tracing: ModelTracing,
        *,
        previous_response_id: str | None,
        conversation_id: str | None,
        prompt: ResponsePromptParam | None,
    ) -> ModelResponse:
        return await self._model.get_response(
            system_instructions,
            input,
            self._with_effort(model_settings),
            tools,
            output_schema,
            handoffs,
            tracing,
            previous_response_id=previous_response_id,
            conversation_id=conversation_id,
            prompt=prompt,
        )

    def stream_response(
        self,
        system_instructions: str | None,
        input: str | list[TResponseInputItem],
        model_settings: ModelSettings,
        tools: list[Tool],
        output_schema: AgentOutputSchemaBase | None,
        handoffs: list[Handoff],
        tracing: ModelTracing,
        *,
        previous_response_id: str | None,
        conversation_id: str | None,
        prompt: ResponsePromptParam | None,
    ) -> AsyncIterator[TResponseStreamEvent]:
        return self._model.stream_response(
            system_instructions,
            input,
            self._with_effort(model_settings),
            tools,
            output_schema,
            handoffs,
            tracing,
            previous_response_id=previous_response_id,
            conversation_id=conversation_id,
            prompt=prompt,
        )

    def _with_effort(self, model_settings: ModelSettings) -> ModelSettings:
        # Keep the other reasoning fields, such as the summary, that the settings set.
        reasoning = (
            Reasoning(effort=self._reasoning_effort)  # type: ignore[arg-type]
            if model_settings.reasoning is None
            else model_settings.reasoning.model_copy(update={"effort": self._reasoning_effort})
        )
        return replace(model_settings, reasoning=reasoning)


def validate_openai_section(options: Mapping[str, Any]) -> None:
    """Reject every key of the ``openai`` section, which has no options yet.

    Args:
        options: The section, ``{}`` when the file omits it.

    Raises:
        ConfigError: ``schema`` with the first key's JSON Pointer.
    """
    for key in sorted(options):
        escaped = key.replace("~", "~0").replace("/", "~1")
        raise ConfigError("schema", f"/openai/{escaped}", keyword="additionalProperties")
