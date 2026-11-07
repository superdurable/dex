# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A real Pydantic AI agent on a scripted ``FunctionModel`` with a small context window."""

from __future__ import annotations

import os
from collections.abc import AsyncIterator
from pathlib import Path

from pydantic_ai import (
    Agent,
    ModelMessage,
    ModelMessagesTypeAdapter,
    ModelRequest,
    ModelResponse,
    TextPart,
    ToolCallPart,
    ToolReturnPart,
    UserPromptPart,
)
from pydantic_ai.models.function import AgentInfo, DeltaToolCall, DeltaToolCalls, FunctionModel

from dex_agents import AgentSessionFlow, AgentSessionOptions, ToolEffect, ToolPolicy
from dex_agents.testing import (
    CONTEXT_WINDOW_TOOL_NAME,
    ContextWindowModelScript,
    ContextWindowSettings,
)
from dex_agents_pydantic_ai import PydanticAIRunner

COMPACTION_AGENT_FLOW_TYPE = "PydanticAICompactionAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "PYDANTIC_AI_COMPACTION_TEST_STATE_DIR"
AGENT_INSTRUCTIONS = "Answer with what the lookup tool returns."


def build_compaction_agent(script: ContextWindowModelScript) -> Agent[None, str]:
    async def respond(messages: list[ModelMessage], info: AgentInfo) -> ModelResponse:
        reply = await script.reply(
            instructions=info.instructions,
            input_text=ModelMessagesTypeAdapter.dump_json(messages).decode(),
            has_tool_result=_has_tool_result_after_last_prompt(messages),
        )
        if reply.tool_tag is not None:
            return ModelResponse(
                parts=[
                    ToolCallPart(
                        CONTEXT_WINDOW_TOOL_NAME,
                        {"tag": reply.tool_tag},
                        tool_call_id=f"call-{reply.tool_tag}",
                    )
                ]
            )
        return ModelResponse(parts=[TextPart(reply.text or "")])

    async def stream_response(
        messages: list[ModelMessage], info: AgentInfo
    ) -> AsyncIterator[str | DeltaToolCalls]:
        response = await respond(messages, info)
        for part in response.parts:
            if isinstance(part, ToolCallPart):
                yield {
                    0: DeltaToolCall(
                        part.tool_name, part.args_as_json_str(), tool_call_id=part.tool_call_id
                    )
                }
            elif isinstance(part, TextPart):
                yield part.content

    agent = Agent(
        FunctionModel(respond, stream_function=stream_response), instructions=AGENT_INSTRUCTIONS
    )

    @agent.tool_plain(name=CONTEXT_WINDOW_TOOL_NAME)
    def lookup(tag: str) -> str:
        """Look up a tag."""
        return script.tool_output(tag)

    return agent


def _has_tool_result_after_last_prompt(messages: list[ModelMessage]) -> bool:
    has_tool_result = False
    for message in messages:
        if not isinstance(message, ModelRequest):
            continue
        for part in message.parts:
            if isinstance(part, UserPromptPart):
                has_tool_result = False
            elif isinstance(part, ToolReturnPart):
                has_tool_result = True
    return has_tool_result


def build_compaction_agent_flow(
    state_dir: Path, settings: ContextWindowSettings
) -> AgentSessionFlow:
    agent = build_compaction_agent(ContextWindowModelScript(state_dir, settings))
    return AgentSessionFlow(
        PydanticAIRunner(agent),
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
