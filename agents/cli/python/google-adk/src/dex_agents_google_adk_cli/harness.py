# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Google ADK harness: an ADK ``LlmAgent`` on Gemini with the CLI core's tools."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import dex_agents_cli_core  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from google.adk.agents import LlmAgent
from google.adk.agents.callback_context import CallbackContext
from google.adk.agents.readonly_context import ReadonlyContext
from google.adk.models.google_llm import Gemini
from google.adk.models.llm_request import LlmRequest
from google.adk.models.llm_response import LlmResponse

from dex_agents import AgentSessionFlow
from dex_agents_cli_core import ConfigError, HarnessContext, LocalToolGroup
from dex_agents_google_adk import adk_agent_session_flow

#: The Flow type of the command's sessions, before the CLI core namespaces it.
GOOGLE_ADK_CLI_FLOW_TYPE = "DexAgentsGoogleAdkCliSession"

#: The Gemini model the command uses when the configuration file sets none.
DEFAULT_GOOGLE_ADK_MODEL = "gemini-2.5-flash"

#: The instructions the agent gets when the configuration sets none.
DEFAULT_INSTRUCTIONS = (
    "You are a helpful assistant. Use your tools to work with the files and commands "
    "in your workspace and with the connected services."
)


class GoogleAdkHarness:
    """The ``dex-agents-google-adk`` command: a Google ADK agent in the terminal.

    The agent is one ADK ``LlmAgent`` on the Gemini API. Each model call
    sends the running turn's model and instructions, so session settings
    that change them apply from the next turn. The CLI core's connector and
    local tools are the agent's only tools.
    """

    name = "google-adk"
    config_section = "google_adk"
    flow_type = GOOGLE_ADK_CLI_FLOW_TYPE
    default_model: str | None = DEFAULT_GOOGLE_ADK_MODEL
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset()

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        """Build the ADK agent session Flow from the effective configuration.

        Args:
            context: The configuration, workspace, and tool sources.

        Returns:
            The Flow, with ``context.flow_type`` as its type.

        Raises:
            ConfigError: If the ``google_adk`` section has a key; it has no options yet.
        """
        validate_google_adk_section(context.harness_options)

        def instructions(readonly_context: ReadonlyContext) -> str:
            return context.current_turn().instructions or DEFAULT_INSTRUCTIONS

        def use_turn_model(
            callback_context: CallbackContext, llm_request: LlmRequest
        ) -> LlmResponse | None:
            # The Gemini model sends the request's model name, so this switches models per turn.
            llm_request.model = context.current_turn().model
            return None

        agent = LlmAgent(
            name="assistant",
            model=Gemini(model=context.config.model),
            instruction=instructions,
            before_model_callback=use_turn_model,
        )
        return adk_agent_session_flow(
            agent,
            flow_type=context.flow_type,
            tool_sources=context.tool_sources,
            media_store=context.media_store,
        )


def validate_google_adk_section(options: Mapping[str, Any]) -> None:
    """Reject every key of the ``google_adk`` section, which has no options yet.

    Args:
        options: The section, ``{}`` when the file omits it.

    Raises:
        ConfigError: ``schema`` with the first key's JSON Pointer.
    """
    for key in sorted(options):
        escaped = key.replace("~", "~0").replace("/", "~1")
        raise ConfigError("schema", f"/google_adk/{escaped}", keyword="additionalProperties")
