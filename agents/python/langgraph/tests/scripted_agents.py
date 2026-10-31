# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The agents under test, built with create_agent and as custom StateGraph graphs.

Each agent uses the scripted chat model and these tools:

* ``lookup`` (read) answers ``found <query>``.
* ``publish`` (non-idempotent write, approval by default) answers ``published <message>``.
* ``charge`` (idempotent write) answers with its idempotency key; a non-positive amount fails.
* ``slow_write`` (non-idempotent write without approval) waits at the ``slow_write`` gate.
* ``wait_at_gate`` (read) waits at the gate it names.
* ``refund`` (non-idempotent write without approval) records its run, then raises a
  ``ValidationError`` from its body.
* ``send_email`` is in no tool node; only the dynamic-tool agents pass it to the runner.
* ``create_ticket`` has two tools: a read-hinted one in the shadowed-tool agents' tool node,
  and an unannotated write passed to their runner, which shadows it.
"""

from __future__ import annotations

import os
import time
from collections.abc import Awaitable, Callable, Sequence
from pathlib import Path
from typing import Any

import langgraph.checkpoint.base.id as checkpoint_id_module
from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents.testing import FileGate, InvocationLog
from langchain.agents import create_agent
from langchain.agents.middleware import AgentMiddleware, ModelRequest, ModelResponse
from langchain_core.messages import AIMessage
from langchain_core.runnables import RunnableConfig
from langchain_core.tools import BaseTool, ToolException, tool
from langgraph.graph import END, START, MessagesState, StateGraph
from langgraph.prebuilt import tools_condition
from pydantic import BaseModel

from dex_agents_langgraph import (
    CompiledGraph,
    DurableToolCallMiddleware,
    durable_tool_node,
    get_tool_call_idempotency_key,
    langchain_agent_session_flow,
    langgraph_agent_session_flow,
)
from tests.scripted_model import ScriptedChatModel

CREATE_AGENT_FLOW_TYPE = "ScriptedLangChainAgent"
STATE_GRAPH_FLOW_TYPE = "ScriptedLangGraphAgent"
UNPROTECTED_AGENT_FLOW_TYPE = "UnprotectedLangChainAgent"
DYNAMIC_TOOL_CREATE_AGENT_FLOW_TYPE = "DynamicToolLangChainAgent"
DYNAMIC_TOOL_STATE_GRAPH_FLOW_TYPE = "DynamicToolLangGraphAgent"
UNCHECKPOINTED_CREATE_AGENT_FLOW_TYPE = "UncheckpointedSubgraphLangChainAgent"
UNCHECKPOINTED_STATE_GRAPH_FLOW_TYPE = "UncheckpointedSubgraphLangGraphAgent"
SHADOWED_TOOL_CREATE_AGENT_FLOW_TYPE = "ShadowedToolLangChainAgent"
SHADOWED_TOOL_STATE_GRAPH_FLOW_TYPE = "ShadowedToolLangGraphAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_STATE_DIR"
CHECKPOINT_CLOCK_SKEW_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_CHECKPOINT_CLOCK_SKEW_SECONDS"
INVOCATION_LOG_NAME = "invocations.log"
SLOW_WRITE_GATE = "slow_write"
TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "wait_at_gate": ToolPolicy(ToolEffect.READ),
    "publish": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    "charge": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "slow_write": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "refund": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
}


class RefundReceipt(BaseModel):
    refund_id: str


class AddSendEmailTool(AgentMiddleware):
    """Offer ``send_email`` to the model and run it from middleware, as dynamic tools do."""

    def __init__(self, send_email: BaseTool) -> None:
        self._send_email = send_email

    async def awrap_model_call(
        self,
        request: ModelRequest[None],
        handler: Callable[[ModelRequest[None]], Awaitable[ModelResponse[Any]]],
    ) -> ModelResponse[Any]:
        return await handler(request.override(tools=[*request.tools, self._send_email]))


def build_scripted_agent_flows(state_dir: Path) -> list[AgentSessionFlow]:
    send_email = build_send_email_tool(state_dir)
    read_hinted_ticket_tool, write_ticket_tool = build_ticket_tools(state_dir)
    return [
        # A factory, so the agent is built lazily on the Worker.
        langchain_agent_session_flow(
            lambda: build_create_agent(state_dir),
            flow_type=CREATE_AGENT_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
        ),
        langgraph_agent_session_flow(
            build_state_graph(state_dir),
            flow_type=STATE_GRAPH_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
        ),
        # An async factory; the agent lacks DurableToolCallMiddleware on purpose.
        langchain_agent_session_flow(
            build_unprotected_agent_factory(state_dir),
            flow_type=UNPROTECTED_AGENT_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
        ),
        langchain_agent_session_flow(
            build_create_agent(state_dir, [AddSendEmailTool(send_email)]),
            flow_type=DYNAMIC_TOOL_CREATE_AGENT_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
            tools=[send_email],
        ),
        langgraph_agent_session_flow(
            build_state_graph(state_dir),
            flow_type=DYNAMIC_TOOL_STATE_GRAPH_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
            tools=[send_email],
        ),
        langgraph_agent_session_flow(
            _wrap_in_parent_graph(
                build_create_agent(state_dir, fresh_tool_call_ids=True).copy(
                    update={"checkpointer": False}
                )
            ),
            flow_type=UNCHECKPOINTED_CREATE_AGENT_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
        ),
        langgraph_agent_session_flow(
            _wrap_in_parent_graph(
                build_state_graph(state_dir, fresh_tool_call_ids=True, checkpointer=False)
            ),
            flow_type=UNCHECKPOINTED_STATE_GRAPH_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
        ),
        langchain_agent_session_flow(
            create_agent(
                _scripted_model(state_dir),
                tools=[read_hinted_ticket_tool],
                middleware=[DurableToolCallMiddleware()],
            ),
            flow_type=SHADOWED_TOOL_CREATE_AGENT_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
            tools=[write_ticket_tool],
        ),
        langgraph_agent_session_flow(
            build_state_graph(state_dir, tools=[read_hinted_ticket_tool]),
            flow_type=SHADOWED_TOOL_STATE_GRAPH_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
            tools=[write_ticket_tool],
        ),
    ]


def build_scripted_agent_flows_from_environment() -> list[AgentSessionFlow]:
    skew_seconds = os.environ.get(CHECKPOINT_CLOCK_SKEW_ENVIRONMENT_VARIABLE)
    if skew_seconds is not None:
        _skew_checkpoint_id_clock(float(skew_seconds))
    return build_scripted_agent_flows(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


def build_create_agent(
    state_dir: Path,
    extra_middleware: Sequence[AgentMiddleware] = (),
    *,
    fresh_tool_call_ids: bool = False,
) -> CompiledGraph:
    return create_agent(
        _scripted_model(state_dir, fresh_tool_call_ids),
        tools=build_scripted_tools(state_dir),
        middleware=[DurableToolCallMiddleware(), *extra_middleware],
    )


def build_unprotected_agent_factory(state_dir: Path) -> Callable[[], Awaitable[CompiledGraph]]:
    async def build_unprotected_agent() -> CompiledGraph:
        return create_agent(_scripted_model(state_dir), tools=build_scripted_tools(state_dir))

    return build_unprotected_agent


def build_state_graph(
    state_dir: Path,
    *,
    fresh_tool_call_ids: bool = False,
    checkpointer: bool | None = None,
    tools: Sequence[BaseTool] | None = None,
) -> CompiledGraph:
    tools = list(tools) if tools is not None else build_scripted_tools(state_dir)
    model = _scripted_model(state_dir, fresh_tool_call_ids).bind_tools(tools)

    async def call_model(state: MessagesState) -> dict[str, list[AIMessage]]:
        reply = await model.ainvoke(state["messages"])
        return {"messages": [reply]}

    builder = StateGraph(MessagesState)
    builder.add_node("agent", call_model)
    builder.add_node("tools", durable_tool_node(tools))
    builder.add_edge(START, "agent")
    builder.add_conditional_edges("agent", tools_condition)
    builder.add_edge("tools", "agent")
    return builder.compile(checkpointer=checkpointer)


def build_scripted_tools(state_dir: Path) -> list[BaseTool]:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    @tool
    def lookup(query: str) -> str:
        """Look up a document."""
        invocations.record("lookup")
        return f"found {query}"

    @tool
    def publish(message: str) -> str:
        """Publish a message."""
        invocations.record("publish")
        return f"published {message}"

    @tool
    def charge(amount: int, config: RunnableConfig) -> str:
        """Charge an amount."""
        invocations.record("charge")
        if amount <= 0:
            raise ToolException("amount must be positive")
        return f"charged {amount} with key {get_tool_call_idempotency_key(config)}"

    @tool
    async def slow_write(message: str) -> str:
        """Write slowly."""
        invocations.record("slow_write")
        gate = FileGate(state_dir, SLOW_WRITE_GATE)
        gate.mark_reached()
        await gate.wait_for_release()
        return f"wrote {message}"

    @tool
    async def wait_at_gate(gate: str) -> str:
        """Wait until the test releases a gate."""
        invocations.record("wait_at_gate")
        file_gate = FileGate(state_dir, gate)
        file_gate.mark_reached()
        await file_gate.wait_for_release()
        return f"passed {gate}"

    @tool
    def refund(order_id: str) -> str:
        """Refund an order."""
        invocations.record("refund")
        return RefundReceipt.model_validate({"id": order_id}).refund_id

    return [lookup, publish, charge, slow_write, wait_at_gate, refund]


def build_send_email_tool(state_dir: Path) -> BaseTool:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    @tool
    def send_email(to: str) -> str:
        """Send an email."""
        invocations.record("send_email")
        return f"emailed {to}"

    return send_email


def build_ticket_tools(state_dir: Path) -> tuple[BaseTool, BaseTool]:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    @tool("create_ticket")
    def read_billing_ticket(title: str) -> str:
        """Read a billing ticket."""
        invocations.record("read_billing_ticket")
        return f"billing ticket {title}"

    read_billing_ticket.metadata = {"readOnlyHint": True}

    @tool("create_ticket")
    def create_support_ticket(title: str) -> str:
        """Create a support ticket."""
        invocations.record("create_support_ticket")
        return f"support ticket {title}"

    return read_billing_ticket, create_support_ticket


def _wrap_in_parent_graph(subgraph: CompiledGraph) -> CompiledGraph:
    builder = StateGraph(MessagesState)
    builder.add_node("agent", subgraph)
    builder.add_edge(START, "agent")
    builder.add_edge("agent", END)
    return builder.compile()


def _scripted_model(state_dir: Path, fresh_tool_call_ids: bool = False) -> ScriptedChatModel:
    return ScriptedChatModel(
        invocation_log_path=str(state_dir / INVOCATION_LOG_NAME),
        fresh_tool_call_ids=fresh_tool_call_ids,
    )


class _SkewedClock:
    def __init__(self, skew_nanoseconds: int) -> None:
        self._skew_nanoseconds = skew_nanoseconds

    def time_ns(self) -> int:
        return time.time_ns() - self._skew_nanoseconds


def _skew_checkpoint_id_clock(skew_seconds: float) -> None:
    # Only LangGraph checkpoint IDs read this clock, as on a Worker whose clock runs behind.
    skewed_clock = _SkewedClock(int(skew_seconds * 1_000_000_000))
    checkpoint_id_module.time = skewed_clock  # type: ignore[attr-defined,assignment]
