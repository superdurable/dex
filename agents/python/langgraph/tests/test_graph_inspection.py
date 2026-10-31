# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The runner checks a graph's tool nodes when it is constructed."""

from __future__ import annotations

from collections.abc import Callable
from pathlib import Path

import pytest
from langchain.agents import create_agent
from langchain_core.messages import AIMessage
from langchain_core.tools import BaseTool, tool
from langgraph.graph import END, START, MessagesState, StateGraph
from langgraph.prebuilt import tools_condition

from dex_agents_langgraph import (
    CompiledGraph,
    DurableToolCallMiddleware,
    LangGraphAgentRunner,
    durable_tool_node,
)
from tests.scripted_model import ScriptedChatModel


def build_ticket_tools() -> tuple[BaseTool, BaseTool]:
    @tool("create_ticket")
    def create_billing_ticket(title: str) -> str:
        """Create a billing ticket."""
        return f"billing ticket {title}"

    @tool("create_ticket")
    def create_support_ticket(title: str) -> str:
        """Create a support ticket."""
        return f"support ticket {title}"

    return create_billing_ticket, create_support_ticket


def build_two_create_agents(tmp_path: Path, tools: tuple[BaseTool, BaseTool]) -> CompiledGraph:
    model = ScriptedChatModel(invocation_log_path=str(tmp_path / "invocations.log"))
    builder = StateGraph(MessagesState)
    for name, agent_tool in zip(("billing", "support"), tools, strict=True):
        builder.add_node(
            name,
            create_agent(model, tools=[agent_tool], middleware=[DurableToolCallMiddleware()]),
        )
    builder.add_edge(START, "billing")
    builder.add_edge("billing", "support")
    builder.add_edge("support", END)
    return builder.compile()


def build_two_tool_nodes(tmp_path: Path, tools: tuple[BaseTool, BaseTool]) -> CompiledGraph:
    model = ScriptedChatModel(invocation_log_path=str(tmp_path / "invocations.log"))
    builder = StateGraph(MessagesState)

    async def call_model(state: MessagesState) -> dict[str, list[AIMessage]]:
        return {"messages": [await model.ainvoke(state["messages"])]}

    builder.add_node("agent", call_model)
    builder.add_node("billing_tools", durable_tool_node([tools[0]]))
    builder.add_node("support_tools", durable_tool_node([tools[1]]))
    builder.add_edge(START, "agent")
    builder.add_conditional_edges("agent", tools_condition, {"tools": "billing_tools", END: END})
    builder.add_edge("billing_tools", "support_tools")
    builder.add_edge("support_tools", END)
    return builder.compile()


GRAPH_BUILDERS = [build_two_create_agents, build_two_tool_nodes]


@pytest.mark.parametrize("build_graph", GRAPH_BUILDERS)
def test_two_different_tools_with_one_name_are_rejected(
    tmp_path: Path,
    build_graph: Callable[[Path, tuple[BaseTool, BaseTool]], CompiledGraph],
) -> None:
    with pytest.raises(ValueError, match="different tools named \\['create_ticket'\\]"):
        LangGraphAgentRunner(build_graph(tmp_path, build_ticket_tools()))


@pytest.mark.parametrize("build_graph", GRAPH_BUILDERS)
def test_an_explicit_tool_settles_a_shared_tool_name(
    tmp_path: Path,
    build_graph: Callable[[Path, tuple[BaseTool, BaseTool]], CompiledGraph],
) -> None:
    billing, support = build_ticket_tools()
    LangGraphAgentRunner(build_graph(tmp_path, (billing, support)), tools=[support])


@pytest.mark.parametrize("build_graph", GRAPH_BUILDERS)
def test_one_tool_in_two_tool_nodes_is_not_ambiguous(
    tmp_path: Path,
    build_graph: Callable[[Path, tuple[BaseTool, BaseTool]], CompiledGraph],
) -> None:
    billing, _ = build_ticket_tools()
    LangGraphAgentRunner(build_graph(tmp_path, (billing, billing)))
