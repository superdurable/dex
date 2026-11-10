# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Agents whose tool source offers the fake ledger Connector's operations.

One is a ``create_agent`` agent with ``DurableToolCallMiddleware``, which adds
the source tools to every model request. The other is a custom graph whose
model node binds ``get_current_tool_source_tools()``.

The chat model reads ``calls <json>`` from the last user message, a JSON list
of ``[tool_name, arguments]`` pairs, and calls them in one response with IDs
``call-0``, ``call-1``, and so on. It calls only tools bound to it; any other
name makes it answer ``unbound <name>``. Once results arrive it answers one
``<tool_name>: <result>`` line per result. A ``hold:<gate>`` word before
``calls`` makes that answer wait at a ``FileGate``, so a test can read the
turn's records before the turn ends.

Both agents also have their own tool, ``lookup``. The session policy for
``policy_note`` claims ``READ`` and asks for approval; only the approval
applies, because the effect class comes from the manifest.
"""

from __future__ import annotations

import json
import os
from collections.abc import Callable, Sequence
from pathlib import Path
from typing import Any

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents.testing import FileGate
from langchain.agents import create_agent
from langchain_core.callbacks import AsyncCallbackManagerForLLMRun, CallbackManagerForLLMRun
from langchain_core.language_models import BaseChatModel, LanguageModelInput
from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, ToolCall, ToolMessage
from langchain_core.outputs import ChatGeneration, ChatResult
from langchain_core.runnables import Runnable
from langchain_core.tools import BaseTool, tool
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import tools_condition

from dex_agents_langgraph import (
    CompiledGraph,
    DurableToolCallMiddleware,
    durable_tool_node,
    get_current_tool_source_tools,
    langchain_agent_session_flow,
    langgraph_agent_session_flow,
)
from tests.fake_ledger import (
    LEDGER_CONNECTION,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    CreatePayoutOperation,
    FakeLedgerProvider,
    GetBalanceOperation,
    PostNoteOperation,
)

CONNECTOR_CREATE_AGENT_FLOW_TYPE = "LangChainConnectorAgent"
CONNECTOR_STATE_GRAPH_FLOW_TYPE = "LangGraphConnectorAgent"
CONNECTOR_TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "policy_note": ToolPolicy(ToolEffect.READ, requires_approval=True),
}


class ConnectorChatModel(BaseChatModel):
    """Call the tools the user message names, if they are bound, then report their results."""

    state_dir: str
    bound_tool_names: tuple[str, ...] = ()

    @property
    def _llm_type(self) -> str:
        return "scripted-connector"

    def bind_tools(
        self,
        tools: Sequence[dict[str, Any] | type | Callable[..., Any] | BaseTool],
        *,
        tool_choice: str | None = None,
        **kwargs: Any,
    ) -> Runnable[LanguageModelInput, AIMessage]:
        names = tuple(tool.name for tool in tools if isinstance(tool, BaseTool))
        return self.model_copy(update={"bound_tool_names": names})

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        raise NotImplementedError("the connector agent runs asynchronously")

    async def _agenerate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        return ChatResult(generations=[ChatGeneration(message=await self._reply(messages))])

    async def _reply(self, messages: list[BaseMessage]) -> AIMessage:
        prompt_index = max(
            index for index, message in enumerate(messages) if isinstance(message, HumanMessage)
        )
        prompt = str(messages[prompt_index].content)
        hold = None
        if prompt.startswith("hold:"):
            hold_word, prompt = prompt.split(" ", 1)
            hold = hold_word.removeprefix("hold:")
        results = [
            f"{message.name}: {message.content}"
            for message in messages[prompt_index + 1 :]
            if isinstance(message, ToolMessage)
        ]
        if results:
            if hold is not None:
                gate = FileGate(Path(self.state_dir), hold)
                gate.mark_reached()
                await gate.wait_for_release()
            return AIMessage(content="\n".join(results))
        if not prompt.startswith("calls "):
            return AIMessage(content=f"said: {prompt}")
        requested: list[Any] = json.loads(prompt.removeprefix("calls "))
        unbound = [name for name, _ in requested if name not in self.bound_tool_names]
        if unbound:
            return AIMessage(content=f"unbound {', '.join(unbound)}")
        return AIMessage(
            content="",
            tool_calls=[
                ToolCall(name=name, args=arguments, id=f"call-{index}", type="tool_call")
                for index, (name, arguments) in enumerate(requested)
            ],
        )


def build_connector_source(state_dir: Path) -> ConnectorToolSource:
    provider = FakeLedgerProvider(state_dir)
    payouts = CreatePayoutOperation(provider)
    notes = PostNoteOperation(provider)
    return ConnectorToolSource(
        [
            connector_tool(GetBalanceOperation(provider), LEDGER_CONNECTION, name="get_balance"),
            connector_tool(
                payouts, LEDGER_CONNECTION, name="create_payout", requires_approval=False
            ),
            connector_tool(payouts, LEDGER_CONNECTION, name="approved_payout"),
            connector_tool(notes, LEDGER_CONNECTION, name="post_note"),
            connector_tool(notes, LEDGER_CONNECTION, name="policy_note"),
        ]
    )


@tool
def lookup(query: str) -> str:
    """Look up a document."""
    return f"found {query}"


def build_connector_create_agent(state_dir: Path) -> CompiledGraph:
    return create_agent(
        ConnectorChatModel(state_dir=str(state_dir)),
        tools=[lookup],
        middleware=[DurableToolCallMiddleware()],
    )


def build_connector_state_graph(state_dir: Path) -> CompiledGraph:
    model = ConnectorChatModel(state_dir=str(state_dir))

    async def call_model(state: MessagesState) -> dict[str, list[AIMessage]]:
        bound = model.bind_tools([lookup, *get_current_tool_source_tools()])
        reply = await bound.ainvoke(state["messages"])
        return {"messages": [reply]}

    builder = StateGraph(MessagesState)
    builder.add_node("agent", call_model)
    builder.add_node("tools", durable_tool_node([lookup]))
    builder.add_edge(START, "agent")
    builder.add_conditional_edges("agent", tools_condition)
    builder.add_edge("tools", "agent")
    return builder.compile()


def build_connector_agent_flows(state_dir: Path) -> list[AgentSessionFlow]:
    return [
        langchain_agent_session_flow(
            build_connector_create_agent(state_dir),
            flow_type=CONNECTOR_CREATE_AGENT_FLOW_TYPE,
            tool_policies=CONNECTOR_TOOL_POLICIES,
            tool_sources=[build_connector_source(state_dir)],
        ),
        langgraph_agent_session_flow(
            build_connector_state_graph(state_dir),
            flow_type=CONNECTOR_STATE_GRAPH_FLOW_TYPE,
            tool_policies=CONNECTOR_TOOL_POLICIES,
            tool_sources=[build_connector_source(state_dir)],
        ),
    ]


def build_connector_agent_flows_from_environment() -> list[AgentSessionFlow]:
    return build_connector_agent_flows(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))
