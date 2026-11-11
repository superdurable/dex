# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The OpenAI Agents SDK harness: an SDK agent with the runtime's tools."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import dex_agents_cli  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from agents import Agent, Model, ModelProvider, MultiProvider, RunConfig, RunContextWrapper

from dex_agents import AgentSessionFlow
from dex_agents_cli import ConfigError, HarnessContext, LocalToolGroup, current_turn_config
from dex_agents_openai_agents import openai_agents_session_flow

#: The Flow type of the command's sessions, before the runtime namespaces it.
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
    ``litellm/`` prefix needs LiteLLM installed. The runtime's connector and
    local tools are the agent's only tools. The SDK's own tracing is off, so
    no conversation leaves the machine for the OpenAI traces dashboard.
    """

    name = "openai-sdk"
    config_section = "openai"
    flow_type = OPENAI_SDK_CLI_FLOW_TYPE
    default_model: str | None = DEFAULT_OPENAI_MODEL
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset()
    # The SDK fixes an agent's tools when the runner is built.
    is_tool_set_fixed_at_build = True

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
        )


class TurnModelProvider(ModelProvider):
    """Give every model call the running turn's model, which session settings may change."""

    def __init__(self) -> None:
        """Wrap the SDK's default ``MultiProvider``."""
        self._provider = MultiProvider()

    def get_model(self, model_name: str | None) -> Model:
        """Return the running turn's model; ``model_name`` is the file's.

        Args:
            model_name: The agent's model, the configuration file's.

        Returns:
            The SDK model for the turn's model name.
        """
        turn = current_turn_config()
        return self._provider.get_model(turn.model if turn is not None else model_name)


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
