# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The built-in harness: a Dex built-in loop agent on LiteLLM with the runtime's tools."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import dex_agents_cli  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from dex_agents import AgentSessionFlow, BuiltInTool
from dex_agents.builtin_loop import AgentDefinition, InstructionContext, builtin_agent_session_flow
from dex_agents.litellm import LiteLLMModelClient
from dex_agents_cli import ConfigError, HarnessContext, LocalToolGroup

#: The Flow type of the command's sessions, before the runtime namespaces it.
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
    runtime's connector and local tools are the agent's only tools, and the
    agent can ask the user a question with the ``ask_user`` built-in tool.
    """

    name = "builtin"
    config_section = "builtin"
    flow_type = BUILTIN_CLI_FLOW_TYPE
    default_model: str | None = DEFAULT_BUILTIN_MODEL
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset()

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
        models: dict[str, LiteLLMModelClient] = {}

        def resolve_model(session_settings: Mapping[str, Any]) -> LiteLLMModelClient:
            model_name = context.resolve_turn(session_settings).model
            if model_name not in models:
                models[model_name] = LiteLLMModelClient(model_name)
            return models[model_name]

        def instructions(instruction_context: InstructionContext) -> str:
            turn = context.resolve_turn(instruction_context.session_settings)
            return turn.instructions or DEFAULT_INSTRUCTIONS

        definition = AgentDefinition(
            name="builtin",
            instructions=instructions,
            model=LiteLLMModelClient(context.config.model),
            resolve_model=resolve_model,
            tool_sources=list(context.tool_sources),
            built_in_tools=frozenset({BuiltInTool.ASK_USER}),
        )
        return builtin_agent_session_flow(definition, flow_type=context.flow_type)


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
