# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Agent session Flows for LangChain agents and LangGraph graphs."""

from __future__ import annotations

from collections.abc import Callable, Collection, Mapping, Sequence
from datetime import timedelta

from dex_agents import (
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    DEFAULT_RETAINED_MESSAGE_LIMIT,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTracingOptions,
    AgentTurnContext,
    CompactionPolicy,
    MediaStore,
    ToolEffect,
    ToolPolicy,
)
from dex_agents.builtin_loop import DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT, ToolSource
from langchain_core.language_models import BaseChatModel
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
    tracing: AgentTracingOptions | None = None,
    tools: Sequence[BaseTool] = (),
    checkpoint_serializer: SerializerProtocol | None = None,
    compaction: CompactionPolicy | None = None,
    retained_message_limit: int | None = DEFAULT_RETAINED_MESSAGE_LIMIT,
    summary_model: BaseChatModel | None = None,
    tool_sources: Sequence[ToolSource] = (),
    tool_source_listing_timeout: timedelta = DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
    media_store: MediaStore | None = None,
    excluded_tool_names: Collection[str] = (),
    select_turn_chat_model: Callable[[AgentTurnContext], BaseChatModel | None] | None = None,
    should_defer_tool_calls_before_middleware: bool = True,
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

    ``tool_sources``, such as ``ConnectorToolSource``, add tools that
    ``DurableToolCallMiddleware`` offers to every model request. Each call
    runs in its own tool Step through its source, which picks the effect
    class, approval, timeout, and retry policy, as in an agent on the built-in loop.

    A Deep Agents agent from ``deepagents.create_deep_agent`` registers the
    same way: pass ``DurableToolCallMiddleware()`` in its ``middleware``. The
    runner defers each tool call before Deep Agents' own middleware, which
    ``create_deep_agent`` places before yours, and runs that middleware's
    tool wrappers in each tool Step. Its
    ``DeltaChannel`` state, its file tools on the default ``StateBackend``,
    and tools that return a ``Command`` work in a session. Each turn starts
    from the stored messages, so its ``files`` last for one turn. Exclude
    ``"task"`` with ``excluded_tool_names`` to turn off its subagents, whose
    model and tool calls would otherwise run inside one tool Step.

    Deep Agents example:
        >>> from deepagents import create_deep_agent
        >>>
        >>> def build_research_agent():
        ...     return create_deep_agent(
        ...         "anthropic:claude-sonnet-4-5",
        ...         tools=[search_papers],
        ...         middleware=[DurableToolCallMiddleware()],
        ...     )
        >>>
        >>> flow = langchain_agent_session_flow(
        ...     build_research_agent,
        ...     flow_type="ResearchAgent",
        ...     excluded_tool_names=["task"],
        ...     tool_policies={"search_papers": ToolPolicy(ToolEffect.READ)},
        ... )

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
        tracing: OpenTelemetry GenAI span options. ``None``, the default,
            uses ``AgentTracingOptions()``, which emits spans through the global
            tracer provider when the OpenTelemetry API is installed.
        tools: Tools that tool call Steps may run beyond those of the agent's
            ``ToolNode``, such as tools that middleware adds dynamically.
        checkpoint_serializer: Serializer for the graph checkpoint stored while
            tool calls run. ``None`` uses LangGraph's ``JsonPlusSerializer``.
        compaction: When to summarize earlier turns so the model input fits the
            context window. It needs ``summary_model``. ``None``, the default,
            never compacts. See ``AgentSessionOptions.compaction``.

            .. versionadded:: 0.3.0
        retained_message_limit: With ``compaction``, how many stored messages
            retention keeps. See ``AgentSessionOptions.retained_message_limit``.

            .. versionadded:: 0.3.0
        summary_model: Chat model of the summary call. A compiled graph does
            not expose its own model.

            .. versionadded:: 0.3.0
        tool_sources: Tool sources, such as ``ConnectorToolSource``, whose
            tools every ``RunModel`` offers to the model. See
            ``LangGraphAgentRunner``. A ``tool_policies`` entry for a connector
            tool changes only its approval. Defaults to none.

            .. versionadded:: 0.4.0
        tool_source_listing_timeout: How long one ``RunModel`` waits for each
            source's ``list_tools``. Defaults to 10 seconds.

            .. versionadded:: 0.4.0
        media_store: The store that holds the session's images. With a store,
            user messages and tool results may carry images, which
            ``DurableToolCallMiddleware`` gives the model as base64 image
            blocks. ``None``, the default, makes the session refuse images.
            See ``LangGraphAgentRunner``.

            .. versionadded:: 0.5.0
        excluded_tool_names: Names of the agent's tools that the session never
            offers or runs, such as ``"task"`` to turn off Deep Agents'
            subagents. A tool source may list a tool of the same name, which
            then replaces the agent's tool. See ``LangGraphAgentRunner``.
            Defaults to none.

            .. versionadded:: 0.5.0
        select_turn_chat_model: Function that picks the chat model of a turn
            from its ``AgentTurnContext``, such as from
            ``turn.session_settings``. ``DurableToolCallMiddleware`` gives the
            model it returns to every model request of the turn; ``None``
            keeps the agent's model. ``None``, the default, never changes it.
            See ``LangGraphAgentRunner``.

            .. versionadded:: 0.5.0
        should_defer_tool_calls_before_middleware: Whether to defer each tool
            call before any middleware tool wrapper runs, as if
            ``DurableToolCallMiddleware`` came first in ``middleware``, and to
            run the wrappers in each attempt of the call's tool Step. Defaults
            to ``True``; pass ``False`` to keep the middleware order and call
            each tool alone in its tool Step. See ``LangGraphAgentRunner``.

            .. versionadded:: 0.5.0

            .. versionchanged:: 0.5.1
                The tool Step runs the middleware tool wrappers.

            .. versionchanged:: 0.6.0
                Defaults to ``True``. Before, it defaulted to ``False``.

    Returns:
        The session Flow.

    Raises:
        ValueError: If ``flow_type`` is empty, an option is not positive,
            ``compaction`` is set without ``summary_model``, or a
            compiled ``agent`` has no ``messages`` channel, uses a
            ``DeltaChannel`` in a subgraph, or has two tool nodes whose
            different tools share a name that ``tools`` does not settle,
            ``excluded_tool_names`` names a tool of ``tools``, or a tool
            source is invalid as ``LangGraphAgentRunner`` describes.
        TypeError: If ``agent`` is neither a compiled graph nor callable, a
            tool source lacks a ``ToolSource`` method or a non-blank
            ``source_name``, or ``excluded_tool_names`` is a string.

    .. versionchanged:: 0.5.0
        A ``DeltaChannel`` in the root graph, as in a Deep Agents agent, is
        accepted.
    """
    return _graph_session_flow(
        agent,
        flow_type=flow_type,
        messages_key="messages",
        options=AgentSessionOptions(
            tool_policies=tool_policies or {},
            default_tool_effect=default_tool_effect,
            event_stream_capacity_bytes=event_stream_capacity_bytes,
            tracing=AgentTracingOptions() if tracing is None else tracing,
            interrupt_deadline=interrupt_deadline,
            compaction=compaction,
            retained_message_limit=retained_message_limit,
        ),
        tools=tools,
        checkpoint_serializer=checkpoint_serializer,
        summary_model=summary_model,
        tool_sources=tool_sources,
        tool_source_listing_timeout=tool_source_listing_timeout,
        media_store=media_store,
        excluded_tool_names=excluded_tool_names,
        select_turn_chat_model=select_turn_chat_model,
        should_defer_tool_calls_before_middleware=should_defer_tool_calls_before_middleware,
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
    tracing: AgentTracingOptions | None = None,
    tools: Sequence[BaseTool] = (),
    checkpoint_serializer: SerializerProtocol | None = None,
    compaction: CompactionPolicy | None = None,
    retained_message_limit: int | None = DEFAULT_RETAINED_MESSAGE_LIMIT,
    summary_model: BaseChatModel | None = None,
    tool_sources: Sequence[ToolSource] = (),
    tool_source_listing_timeout: timedelta = DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
    media_store: MediaStore | None = None,
    excluded_tool_names: Collection[str] = (),
    select_turn_chat_model: Callable[[AgentTurnContext], BaseChatModel | None] | None = None,
    should_defer_tool_calls_before_middleware: bool = True,
) -> AgentSessionFlow:
    """Create the durable agent session Flow for a custom LangGraph graph.

    The graph's state must hold the conversation as LangChain messages under
    ``messages_key`` with the ``add_messages`` reducer, as ``MessagesState``
    does. Build every ``ToolNode`` with ``durable_tool_node``. Each turn starts
    the graph with the stored messages as its only input; other state keys
    start from their defaults on every turn. With ``tool_sources``, bind
    ``get_current_tool_source_tools()`` next to the graph's own tools in the
    model node, so the model can call the sources' tools.

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
        tracing: OpenTelemetry GenAI span options. ``None``, the default,
            uses ``AgentTracingOptions()``, which emits spans through the global
            tracer provider when the OpenTelemetry API is installed.
        tools: Tools that tool call Steps may run beyond those of the graph's
            tool nodes.
        checkpoint_serializer: Serializer for the graph checkpoint stored while
            tool calls run. ``None`` uses LangGraph's ``JsonPlusSerializer``.
        compaction: When to summarize earlier turns so the model input fits the
            context window. It needs ``summary_model``. ``None``, the default,
            never compacts. See ``AgentSessionOptions.compaction``.

            .. versionadded:: 0.3.0
        retained_message_limit: With ``compaction``, how many stored messages
            retention keeps. See ``AgentSessionOptions.retained_message_limit``.

            .. versionadded:: 0.3.0
        summary_model: Chat model of the summary call. A compiled graph does
            not expose its own model.

            .. versionadded:: 0.3.0
        tool_sources: Tool sources, such as ``ConnectorToolSource``, whose
            tools every ``RunModel`` offers to the model. See
            ``LangGraphAgentRunner``. A ``tool_policies`` entry for a connector
            tool changes only its approval. Defaults to none.

            .. versionadded:: 0.4.0
        tool_source_listing_timeout: How long one ``RunModel`` waits for each
            source's ``list_tools``. Defaults to 10 seconds.

            .. versionadded:: 0.4.0
        media_store: The store that holds the session's images. With a store,
            user messages and tool results may carry images. The model node
            calls ``inline_image_references`` before the chat model call to
            give the model the bytes as base64 image blocks. ``None``, the
            default, makes the session refuse images. See
            ``LangGraphAgentRunner``.

            .. versionadded:: 0.5.0
        excluded_tool_names: Names of the graph's tools that the session never
            offers or runs, such as ``"task"`` to turn off Deep Agents'
            subagents. A tool source may list a tool of the same name, which
            then replaces the graph's tool. See ``LangGraphAgentRunner``.
            Defaults to none.

            .. versionadded:: 0.5.0
        select_turn_chat_model: Function that picks the chat model of a turn
            from its ``AgentTurnContext``, such as from
            ``turn.session_settings``. ``DurableToolCallMiddleware`` gives the
            model it returns to every model request of the turn; ``None``
            keeps the graph's model. ``None``, the default, never changes it.
            See ``LangGraphAgentRunner``.

            .. versionadded:: 0.5.0
        should_defer_tool_calls_before_middleware: Whether to defer each tool
            call before any middleware tool wrapper runs, as if
            ``DurableToolCallMiddleware`` came first in ``middleware``, and to
            run the wrappers in each attempt of the call's tool Step. Defaults
            to ``True``; pass ``False`` to keep the middleware order and call
            each tool alone in its tool Step. See ``LangGraphAgentRunner``.

            .. versionadded:: 0.5.0

            .. versionchanged:: 0.5.1
                The tool Step runs the middleware tool wrappers.

            .. versionchanged:: 0.6.0
                Defaults to ``True``. Before, it defaulted to ``False``.

    Returns:
        The session Flow.

    Raises:
        ValueError: If ``flow_type`` is empty, an option is not positive,
            ``compaction`` is set without ``summary_model``, or a
            compiled ``graph`` has no ``messages_key`` channel, uses a
            ``DeltaChannel`` in a subgraph, or has two tool nodes whose
            different tools share a name that ``tools`` does not settle,
            ``excluded_tool_names`` names a tool of ``tools``, or a tool
            source is invalid as ``LangGraphAgentRunner`` describes.
        TypeError: If ``graph`` is neither a compiled graph nor callable, a
            tool source lacks a ``ToolSource`` method or a non-blank
            ``source_name``, or ``excluded_tool_names`` is a string.

    .. versionchanged:: 0.5.0
        A ``DeltaChannel`` in the root graph is accepted.
    """
    return _graph_session_flow(
        graph,
        flow_type=flow_type,
        messages_key=messages_key,
        options=AgentSessionOptions(
            tool_policies=tool_policies or {},
            default_tool_effect=default_tool_effect,
            event_stream_capacity_bytes=event_stream_capacity_bytes,
            tracing=AgentTracingOptions() if tracing is None else tracing,
            interrupt_deadline=interrupt_deadline,
            compaction=compaction,
            retained_message_limit=retained_message_limit,
        ),
        tools=tools,
        checkpoint_serializer=checkpoint_serializer,
        summary_model=summary_model,
        tool_sources=tool_sources,
        tool_source_listing_timeout=tool_source_listing_timeout,
        media_store=media_store,
        excluded_tool_names=excluded_tool_names,
        select_turn_chat_model=select_turn_chat_model,
        should_defer_tool_calls_before_middleware=should_defer_tool_calls_before_middleware,
    )


def _graph_session_flow(
    graph: CompiledGraphSource,
    *,
    flow_type: str,
    messages_key: str,
    options: AgentSessionOptions,
    tools: Sequence[BaseTool],
    checkpoint_serializer: SerializerProtocol | None,
    summary_model: BaseChatModel | None,
    tool_sources: Sequence[ToolSource],
    tool_source_listing_timeout: timedelta,
    media_store: MediaStore | None,
    excluded_tool_names: Collection[str],
    select_turn_chat_model: Callable[[AgentTurnContext], BaseChatModel | None] | None,
    should_defer_tool_calls_before_middleware: bool,
) -> AgentSessionFlow:
    if options.compaction is not None and summary_model is None:
        raise ValueError("compaction needs a summary_model")
    runner = LangGraphAgentRunner(
        graph,
        tools=tools,
        messages_key=messages_key,
        checkpoint_serializer=checkpoint_serializer,
        summary_model=summary_model,
        tool_sources=tool_sources,
        tool_source_listing_timeout=tool_source_listing_timeout,
        media_store=media_store,
        excluded_tool_names=excluded_tool_names,
        select_turn_chat_model=select_turn_chat_model,
        should_defer_tool_calls_before_middleware=should_defer_tool_calls_before_middleware,
    )
    return AgentSessionFlow(runner, flow_type=flow_type, options=options)
