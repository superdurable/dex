# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A real OpenAI Agents SDK agent on a scripted model with a small context window."""

from __future__ import annotations

import json
import os
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any

from agents import Agent, Model, ModelResponse, RunConfig, function_tool
from agents.testing import ModelStep, ScriptedModel, assistant_message, function_call

from dex_agents import AgentSessionFlow, AgentSessionOptions, ToolEffect, ToolPolicy
from dex_agents.testing import (
    CONTEXT_WINDOW_TOOL_NAME,
    ContextWindowModelScript,
    ContextWindowReply,
    ContextWindowSettings,
)
from dex_agents_openai_agents import OpenAIAgentsRunner

COMPACTION_AGENT_FLOW_TYPE = "OpenAIAgentsCompactionAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "OPENAI_AGENTS_COMPACTION_TEST_STATE_DIR"
AGENT_INSTRUCTIONS = "Answer with what the lookup tool returns."


class ContextWindowModel(Model):
    def __init__(self, script: ContextWindowModelScript) -> None:
        self._script = script

    async def get_response(self, *args: Any, **kwargs: Any) -> ModelResponse:
        reply = await self._reply(*args, **kwargs)
        return await ScriptedModel([_model_step(reply)]).get_response(*args, **kwargs)

    async def stream_response(self, *args: Any, **kwargs: Any) -> AsyncIterator[Any]:
        reply = await self._reply(*args, **kwargs)
        async for event in ScriptedModel([_model_step(reply)]).stream_response(*args, **kwargs):
            yield event

    async def _reply(
        self, system_instructions: str | None, input: Any, *_: Any, **__: Any
    ) -> ContextWindowReply:
        items = [{"role": "user", "content": input}] if isinstance(input, str) else input
        last_user = max(
            index for index, item in enumerate(items) if dict(item).get("role") == "user"
        )
        return await self._script.reply(
            instructions=system_instructions,
            input_text=json.dumps(items, default=str),
            has_tool_result=any(
                dict(item).get("type") == "function_call_output" for item in items[last_user:]
            ),
        )


def _model_step(reply: ContextWindowReply) -> ModelStep:
    if reply.tool_tag is not None:
        return ModelStep(
            output=[
                function_call(
                    CONTEXT_WINDOW_TOOL_NAME,
                    {"tag": reply.tool_tag},
                    call_id=f"call-{reply.tool_tag}",
                )
            ]
        )
    return ModelStep(output=[assistant_message(reply.text or "")])


def build_compaction_agent_flow(
    state_dir: Path, settings: ContextWindowSettings
) -> AgentSessionFlow:
    script = ContextWindowModelScript(state_dir, settings)

    @function_tool(name_override=CONTEXT_WINDOW_TOOL_NAME)
    def lookup(tag: str) -> str:
        """Look up a tag."""
        return script.tool_output(tag)

    agent = Agent[Any](
        name="Assistant",
        instructions=AGENT_INSTRUCTIONS,
        model=ContextWindowModel(script),
        tools=[lookup],
    )
    return AgentSessionFlow(
        OpenAIAgentsRunner(agent, run_config=RunConfig(tracing_disabled=True)),
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
