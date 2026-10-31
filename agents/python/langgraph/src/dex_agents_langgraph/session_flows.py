# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Agent session Flows for LangChain agents and LangGraph graphs."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from datetime import timedelta

from dex_agents import (
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    AgentSessionFlow,
    AgentSessionOptions,
    ToolEffect,
    ToolPolicy,
)
from langchain_core.tools import BaseTool
from langgraph.checkpoint.serde.base import SerializerProtocol

from dex_agents_langgraph.runner import CompiledGraphSource, LangGraphAgentRunner


def langchain_agent_session_flow(
    agent: CompiledGraphSource,
    *,
    flow_type: str,
    tool_policies: Mapping[str, ToolPolicy] | None = None,
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED,
    interrupt_deadline: timedelta | None = None,
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    tools: Sequence[BaseTool] = (),
    checkpoint_serializer: SerializerProtocol | None = None,
) -> AgentSessionFlow:
    """Create the durable agent session Flow for a ``create_agent`` agent.

    Build the agent with ``langchain.agents.create_agent`` and put
    ``DurableToolCallMiddleware`` first in its ``middleware``. Register the
    returned Flow in a Registry created with ``allow_async_handlers=True`` and
    serve it with an ``AsyncWorker``. Build the Flow the same way in every
    process that starts sessions or calls their RPCs.

    Pass a factory instead of the agent to build it lazily on the Worker, for
    example when the model client needs credentials that only Workers hold or
    when MCP tools load asynchronously. Client processes then never call it.

    Example:
        >>> from langchain.agents import create_agent
        >>> from langchain_core.tools import tool
        >>>
        >>> @tool
        ... def search_orders(customer_id: str) -> str:
        ...     \"\"\"List a customer's orders.\"\"\"
        ...     return orders.search(customer_id)
        >>>
        >>> @tool
        ... def refund_order(order_id: str) -> str:
        ...     \"\"\"Refund an order.\"\"\"
        ...     return payments.refund(order_id)
        >>>
        >>> def build_support_agent():
        ...     return create_agent(
        ...         "anthropic:claude-sonnet-4-5",
        ...         tools=[search_orders, refund_order],
        ...         middleware=[DurableToolCallMiddleware()],
        ...     )
        >>>
        >>> flow = langchain_agent_session_flow(
        ...     build_support_agent,
        ...     flow_type="SupportAgent",
        ...     tool_policies={"search_orders": ToolPolicy(ToolEffect.READ)},
        ... )
        >>> registry = Registry([flow], allow_async_handlers=True)

    Args:
        agent: The compiled agent, or a function that returns it. The function
            may be ``async``; the Worker calls it once, at first use.
        flow_type: Protocol Flow type, unique within the Registry. Keep it
            stable: running sessions resume by it.
        tool_policies: Policies by tool name. They override MCP annotations
            and ``default_tool_effect``. ``None`` sets none.
        default_tool_effect: Effect of a tool without a policy or annotation.
            Defaults to ``UNCLASSIFIED``, so such tools run once, after approval.
        interrupt_deadline: How long an approval may stay pending before the
            session rejects it. ``None``, the default, waits indefinitely.
        event_stream_capacity_bytes: Byte budget of the ``AgentEvents`` Stream.
            Defaults to 10 MiB.
        tools: Tools that tool call Steps may run beyond those of the agent's
            ``ToolNode``, such as tools that middleware adds dynamically.
        checkpoint_serializer: Serializer for the graph checkpoint stored while
            tool calls run. ``None`` uses LangGraph's ``JsonPlusSerializer``.

    Returns:
        The session Flow.

    Raises:
        ValueError: If ``flow_type`` is empty, an option is not positive, or a
            compiled ``agent`` has no ``messages`` channel, uses a
            ``DeltaChannel``, or has two tool nodes whose different tools share
            a name that ``tools`` does not settle.
        TypeError: If ``agent`` is neither a compiled graph nor callable.
    """
    return _graph_session_flow(
        agent,
        flow_type=flow_type,
        messages_key="messages",
        options=AgentSessionOptions(
            tool_policies=tool_policies or {},
            default_tool_effect=default_tool_effect,
            event_stream_capacity_bytes=event_stream_capacity_bytes,
            interrupt_deadline=interrupt_deadline,
        ),
        tools=tools,
        checkpoint_serializer=checkpoint_serializer,
    )


def langgraph_agent_session_flow(
    graph: CompiledGraphSource,
    *,
    flow_type: str,
    messages_key: str = "messages",
    tool_policies: Mapping[str, ToolPolicy] | None = None,
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED,
    interrupt_deadline: timedelta | None = None,
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    tools: Sequence[BaseTool] = (),
    checkpoint_serializer: SerializerProtocol | None = None,
) -> AgentSessionFlow:
    """Create the durable agent session Flow for a custom LangGraph graph.

    The graph's state must hold the conversation as LangChain messages under
    ``messages_key`` with the ``add_messages`` reducer, as ``MessagesState``
    does. Build every ``ToolNode`` with ``durable_tool_node``. Each turn starts
    the graph with the stored messages as its only input; other state keys
    start from their defaults on every turn.

    Example:
        >>> from langgraph.graph import START, MessagesState, StateGraph
        >>> from langgraph.prebuilt import tools_condition
        >>>
        >>> async def call_model(state: MessagesState) -> dict[str, list[AIMessage]]:
        ...     reply = await model.bind_tools([search_orders, refund_order]).ainvoke(
        ...         state["messages"]
        ...     )
        ...     return {"messages": [reply]}
        >>>
        >>> builder = StateGraph(MessagesState)
        >>> builder.add_node("agent", call_model)
        >>> builder.add_node("tools", durable_tool_node([search_orders, refund_order]))
        >>> builder.add_edge(START, "agent")
        >>> builder.add_conditional_edges("agent", tools_condition)
        >>> builder.add_edge("tools", "agent")
        >>> flow = langgraph_agent_session_flow(
        ...     builder.compile(),
        ...     flow_type="SupportGraph",
        ...     tool_policies={"search_orders": ToolPolicy(ToolEffect.READ)},
        ... )

    Args:
        graph: The compiled graph, or a function that returns it. The function
            may be ``async``; the Worker calls it once, at first use.
        flow_type: Protocol Flow type, unique within the Registry. Keep it
            stable: running sessions resume by it.
        messages_key: State key of the message list. Defaults to ``"messages"``.
        tool_policies: Policies by tool name. They override MCP annotations
            and ``default_tool_effect``. ``None`` sets none.
        default_tool_effect: Effect of a tool without a policy or annotation.
            Defaults to ``UNCLASSIFIED``, so such tools run once, after approval.
        interrupt_deadline: How long an approval may stay pending before the
            session rejects it. ``None``, the default, waits indefinitely.
        event_stream_capacity_bytes: Byte budget of the ``AgentEvents`` Stream.
            Defaults to 10 MiB.
        tools: Tools that tool call Steps may run beyond those of the graph's
            tool nodes.
        checkpoint_serializer: Serializer for the graph checkpoint stored while
            tool calls run. ``None`` uses LangGraph's ``JsonPlusSerializer``.

    Returns:
        The session Flow.

    Raises:
        ValueError: If ``flow_type`` is empty, an option is not positive, or a
            compiled ``graph`` has no ``messages_key`` channel, uses a
            ``DeltaChannel``, or has two tool nodes whose different tools share
            a name that ``tools`` does not settle.
        TypeError: If ``graph`` is neither a compiled graph nor callable.
    """
    return _graph_session_flow(
        graph,
        flow_type=flow_type,
        messages_key=messages_key,
        options=AgentSessionOptions(
            tool_policies=tool_policies or {},
            default_tool_effect=default_tool_effect,
            event_stream_capacity_bytes=event_stream_capacity_bytes,
            interrupt_deadline=interrupt_deadline,
        ),
        tools=tools,
        checkpoint_serializer=checkpoint_serializer,
    )


def _graph_session_flow(
    graph: CompiledGraphSource,
    *,
    flow_type: str,
    messages_key: str,
    options: AgentSessionOptions,
    tools: Sequence[BaseTool],
    checkpoint_serializer: SerializerProtocol | None,
) -> AgentSessionFlow:
    runner = LangGraphAgentRunner(
        graph,
        tools=tools,
        messages_key=messages_key,
        checkpoint_serializer=checkpoint_serializer,
    )
    return AgentSessionFlow(runner, flow_type=flow_type, options=options)
