# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A real ``create_agent`` agent on a scripted chat model with a small context window."""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any

from dex_agents import AgentSessionFlow, AgentSessionOptions, ToolEffect, ToolPolicy
from dex_agents.testing import (
    CONTEXT_WINDOW_TOOL_NAME,
    ContextWindowModelScript,
    ContextWindowSettings,
)
from langchain.agents import create_agent
from langchain_core.callbacks import AsyncCallbackManagerForLLMRun, CallbackManagerForLLMRun
from langchain_core.language_models import BaseChatModel, LanguageModelInput
from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, SystemMessage, ToolMessage
from langchain_core.outputs import ChatGeneration, ChatResult
from langchain_core.runnables import Runnable
from langchain_core.tools import tool

from dex_agents_langgraph import DurableToolCallMiddleware, LangGraphAgentRunner

COMPACTION_AGENT_FLOW_TYPE = "LangGraphCompactionAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "LANGGRAPH_COMPACTION_TEST_STATE_DIR"
AGENT_INSTRUCTIONS = "Answer with what the lookup tool returns."


class ContextWindowChatModel(BaseChatModel):
    state_dir: str
    settings_json: str

    @property
    def _llm_type(self) -> str:
        return "context-window-scripted"

    def bind_tools(self, tools: Any, **kwargs: Any) -> Runnable[LanguageModelInput, AIMessage]:
        return self

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        raise NotImplementedError("the context window model is async only")

    async def _agenerate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        script = ContextWindowModelScript(
            Path(self.state_dir), ContextWindowSettings(**json.loads(self.settings_json))
        )
        instructions = "\n".join(
            str(message.content) for message in messages if isinstance(message, SystemMessage)
        )
        last_user = max(
            index for index, message in enumerate(messages) if isinstance(message, HumanMessage)
        )
        reply = await script.reply(
            instructions=instructions,
            input_text=json.dumps([message.model_dump() for message in messages], default=str),
            has_tool_result=any(isinstance(m, ToolMessage) for m in messages[last_user:]),
        )
        if reply.tool_tag is not None:
            message = AIMessage(
                content="",
                tool_calls=[
                    {
                        "name": CONTEXT_WINDOW_TOOL_NAME,
                        "args": {"tag": reply.tool_tag},
                        "id": f"call-{reply.tool_tag}",
                        "type": "tool_call",
                    }
                ],
            )
        else:
            message = AIMessage(content=reply.text or "")
        return ChatResult(generations=[ChatGeneration(message=message)])


def build_compaction_agent_flow(
    state_dir: Path, settings: ContextWindowSettings
) -> AgentSessionFlow:
    script = ContextWindowModelScript(state_dir, settings)
    model = ContextWindowChatModel(
        state_dir=str(state_dir), settings_json=json.dumps(settings.__dict__)
    )

    @tool(CONTEXT_WINDOW_TOOL_NAME)
    def lookup(tag: str) -> str:
        """Look up a tag."""
        return script.tool_output(tag)

    agent = create_agent(
        model,
        tools=[lookup],
        system_prompt=AGENT_INSTRUCTIONS,
        middleware=[DurableToolCallMiddleware()],
    )
    return AgentSessionFlow(
        LangGraphAgentRunner(agent, summary_model=model),
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
