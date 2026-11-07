# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A real ADK agent on a scripted model with a small context window.

The agent stores a ``turns_seen`` count in the ADK session state on every
turn, so the tests prove that compaction keeps the state.
"""

from __future__ import annotations

import json
import os
from collections.abc import AsyncGenerator
from pathlib import Path

from google.adk.agents import LlmAgent
from google.adk.agents.callback_context import CallbackContext
from google.adk.models import BaseLlm, LlmRequest, LlmResponse
from google.adk.tools import ToolContext
from google.genai import types

from dex_agents import AgentSessionFlow, AgentSessionOptions, ToolEffect, ToolPolicy
from dex_agents.testing import (
    CONTEXT_WINDOW_TOOL_NAME,
    ContextWindowModelScript,
    ContextWindowSettings,
)
from dex_agents_google_adk import AdkAgentRunner

COMPACTION_AGENT_FLOW_TYPE = "AdkCompactionAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "ADK_COMPACTION_TEST_STATE_DIR"
AGENT_INSTRUCTIONS = "Answer with what the lookup tool returns."
TURNS_SEEN_STATE_KEY = "turns_seen"


class ContextWindowLlm(BaseLlm):
    model: str = "context-window-scripted"
    state_dir: str
    settings_json: str

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        script = ContextWindowModelScript(
            Path(self.state_dir), ContextWindowSettings(**json.loads(self.settings_json))
        )
        contents = llm_request.contents
        last_user = max(
            index
            for index, content in enumerate(contents)
            if content.role == "user" and any(part.text for part in content.parts or [])
        )
        config = llm_request.config
        instructions = None if config is None else config.system_instruction
        reply = await script.reply(
            instructions=None if instructions is None else str(instructions),
            input_text=json.dumps(
                [content.model_dump(mode="json", exclude_none=True) for content in contents]
            ),
            has_tool_result=any(
                part.function_response is not None
                for content in contents[last_user:]
                for part in content.parts or []
            ),
        )
        if reply.tool_tag is not None:
            part = types.Part(
                function_call=types.FunctionCall(
                    name=CONTEXT_WINDOW_TOOL_NAME, args={"tag": reply.tool_tag}
                )
            )
        else:
            part = types.Part(text=reply.text or "")
        yield LlmResponse(content=types.Content(role="model", parts=[part]))


def build_compaction_agent_flow(
    state_dir: Path, settings: ContextWindowSettings
) -> AgentSessionFlow:
    script = ContextWindowModelScript(state_dir, settings)

    def lookup(tag: str, tool_context: ToolContext) -> str:
        """Look up a tag."""
        return script.tool_output(tag)

    def count_turn(callback_context: CallbackContext) -> None:
        seen = int(callback_context.state.get(TURNS_SEEN_STATE_KEY, 0))
        callback_context.state[TURNS_SEEN_STATE_KEY] = seen + 1

    agent = LlmAgent(
        name="assistant",
        model=ContextWindowLlm(
            state_dir=str(state_dir), settings_json=json.dumps(settings.__dict__)
        ),
        instruction=AGENT_INSTRUCTIONS,
        tools=[lookup],
        before_agent_callback=count_turn,
    )
    return AgentSessionFlow(
        AdkAgentRunner(agent),
        flow_type=COMPACTION_AGENT_FLOW_TYPE,
        options=AgentSessionOptions(
            tool_policies={CONTEXT_WINDOW_TOOL_NAME: ToolPolicy(ToolEffect.READ)},
            **settings.session_options(),
        ),
    )


def build_compaction_agent_flow_from_environment() -> AgentSessionFlow:
    return build_compaction_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]), ContextWindowSettings.from_environment()
    )
