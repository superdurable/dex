# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Tool call deferral for LangChain agents and LangGraph tool nodes."""

from __future__ import annotations

import contextlib
import json
from collections.abc import Awaitable, Callable, Iterator, Mapping, Sequence
from contextvars import ContextVar
from dataclasses import dataclass, field
from typing import Any

from dex_agents import AgentTurnContext, ToolCallStatus, ToolEffect, ToolPolicy
from langchain.agents.middleware import AgentMiddleware
from langchain_core.messages import AnyMessage, ToolCall, ToolMessage
from langchain_core.runnables import RunnableConfig
from langchain_core.tools import BaseTool
from langgraph.prebuilt import ToolNode
from langgraph.prebuilt.tool_node import ToolCallRequest
from langgraph.runtime import Runtime
from langgraph.types import Command, interrupt
from pydantic import BaseModel
from pydantic_core import to_jsonable_python

#: ``RunnableConfig["configurable"]`` key that carries a deferred call's idempotency key.
TOOL_CALL_IDEMPOTENCY_KEY_CONFIGURABLE = "dex_tool_call_idempotency_key"

_DEFERRED_TOOL_CALLS_INTERRUPT_TYPE = "dex_deferred_tool_calls"

#: The callable a ``wrap_tool_call`` hook receives to run the tool.
ToolCallExecutor = Callable[[ToolCallRequest], Awaitable[ToolMessage | Command[Any]]]


class DurableToolCallMiddleware(AgentMiddleware):
    """Run every tool call of a ``create_agent`` agent in its own Dex Step.

    Add one instance to ``langchain.agents.create_agent`` and register the
    agent with ``langchain_agent_session_flow``. Inside an agent session the
    middleware never runs a tool. It pauses the graph with LangGraph's
    ``interrupt()`` for every tool call, READ tools included, so each
    ``RunModel`` Step makes exactly one model call. The session then runs each
    call in its own Step, in parallel, after approval when the call's
    ``ToolPolicy`` requires it. When the next ``RunModel`` resumes the graph,
    the middleware returns each call's recorded result as its ``ToolMessage``.

    Put it first in ``middleware`` so it is the outermost tool wrapper.
    Middleware listed before it see every tool call. Middleware listed after it
    never see a call inside a session, because the tool runs outside the graph.

    The middleware works only while an agent session runs the graph. Invoking
    the agent outside a session raises ``RuntimeError`` on the first tool call,
    so no tool runs unprotected. Inside a deferred tool's own Step, such as a
    tool that invokes a sub-agent built with this middleware, the sub-agent's
    tools run inline, as part of that Step.

    Example:
        >>> from langchain.agents import create_agent
        >>> agent = create_agent(
        ...     model,
        ...     tools=[search_orders, refund_order],
        ...     middleware=[DurableToolCallMiddleware()],
        ... )
        >>> flow = langchain_agent_session_flow(
        ...     agent,
        ...     flow_type="SupportAgent",
        ...     tool_policies={"search_orders": ToolPolicy(ToolEffect.READ)},
        ... )
    """

    async def awrap_tool_call(
        self,
        request: ToolCallRequest,
        handler: ToolCallExecutor,
    ) -> ToolMessage | Command[Any]:
        """Defer one tool call to its own Dex Step.

        ``create_agent`` calls this hook for every tool call; applications do
        not call it.

        Args:
            request: The tool call, its tool, and the graph state.
            handler: Runs the tool and the inner middleware. It is called only
                inside a deferred tool's own Step.

        Returns:
            A ``ToolMessage`` with the call's recorded result.

        Raises:
            GraphInterrupt: When the call is deferred. LangGraph handles it.
            RuntimeError: If no agent session runs the graph.
        """
        return await awrap_durable_tool_call(request, handler)


async def awrap_durable_tool_call(
    request: ToolCallRequest,
    handler: ToolCallExecutor,
) -> ToolMessage | Command[Any]:
    """Defer one tool call to its own Dex Step.

    This is the ``awrap_tool_call`` wrapper behind ``DurableToolCallMiddleware``.
    Prefer ``durable_tool_node`` for a custom graph: it defers every call of a
    model message with one interrupt, so they all run in parallel Steps. Pass
    this function as a plain ``ToolNode``'s ``awrap_tool_call`` only when the
    node must stay a ``ToolNode``.

    A plain ``ToolNode`` runs all calls of one model message in one graph
    task, and LangGraph surfaces one interrupt per task at a time. Behind this
    wrapper such calls therefore run one after another, and each later call
    surfaces from a ``RunModel`` Step that makes no model call. Call it
    before any ``await`` in an outer wrapper: LangGraph pairs results with
    interrupts by call order within a task, and an ``await`` first can change
    that order. A mismatched result fails the ``RunModel`` call; it never
    reaches the wrong tool call.

    The tool is ``request.tool``, or the session runner's tool of that name
    for a tool that middleware adds dynamically. LangGraph runs an interrupted
    node again from its start when the turn resumes; this function has no side
    effect before ``interrupt()``, so the rerun only reads the recorded result.

    Example:
        >>> tool_node = ToolNode(tools, awrap_tool_call=awrap_durable_tool_call)

    Args:
        request: The tool call, its tool, and the graph state.
        handler: Runs the tool, or the next wrapper. It is called only inside a
            deferred tool's own Step.

    Returns:
        A ``ToolMessage`` with the call's recorded result.

    Raises:
        GraphInterrupt: When the call is deferred. LangGraph handles it.
        RuntimeError: If no agent session runs the graph.
    """
    binding = _require_active_tool_binding()
    if isinstance(binding, _ToolStepBinding):
        return await handler(request)
    [tool_message] = _defer_tool_calls(binding, [request.tool_call], request.tool)
    return tool_message


class DurableToolNode(ToolNode):
    """Defer every tool call of a custom LangGraph graph to its own Dex Step.

    Create it with ``durable_tool_node``. Inside an agent session it never
    runs a tool. It pauses the graph with one ``interrupt()`` for all tool
    calls of the latest model message, so the session runs them as parallel
    Steps, and returns their recorded results as ``ToolMessage`` values when
    the next ``RunModel`` resumes the graph. Inside a deferred tool's own Step
    it runs tools like ``ToolNode``.

    Attributes:
        tools_by_name: The node's tools by name, as on ``ToolNode``.
    """

    def _func(
        self,
        input: list[AnyMessage] | dict[str, Any] | BaseModel,
        config: RunnableConfig,
        runtime: Runtime,
    ) -> Any:
        binding = _require_active_tool_binding()
        if isinstance(binding, _ToolStepBinding):
            return super()._func(input, config, runtime)
        return self._defer_parsed_tool_calls(binding, input)

    async def _afunc(
        self,
        input: list[AnyMessage] | dict[str, Any] | BaseModel,
        config: RunnableConfig,
        runtime: Runtime,
    ) -> Any:
        binding = _require_active_tool_binding()
        if isinstance(binding, _ToolStepBinding):
            return await super()._afunc(input, config, runtime)
        return self._defer_parsed_tool_calls(binding, input)

    def _defer_parsed_tool_calls(
        self,
        binding: _RunModelBinding,
        input: list[AnyMessage] | dict[str, Any] | BaseModel,
    ) -> Any:
        tool_calls, input_type = self._parse_input(input)
        tool_messages = _defer_tool_calls(binding, tool_calls, None, self.tools_by_name)
        outputs: list[ToolMessage | Command[Any] | list[ToolMessage | Command[Any]]] = [
            *tool_messages
        ]
        return self._combine_tool_outputs(outputs, input_type)


def durable_tool_node(
    tools: Sequence[BaseTool | Callable[..., Any]],
    **tool_node_options: Any,
) -> DurableToolNode:
    """Create a LangGraph tool node that runs every call in its own Dex Step.

    Use it in a custom ``StateGraph`` whose state holds a ``messages`` list, in
    place of ``ToolNode(tools)``. Then register the compiled graph with
    ``langgraph_agent_session_flow``. All tool calls of one model message
    become parallel Steps with a single interrupt, and the next ``RunModel``
    receives all their results at once.

    Example:
        >>> from langgraph.graph import START, MessagesState, StateGraph
        >>> from langgraph.prebuilt import tools_condition
        >>> builder = StateGraph(MessagesState)
        >>> builder.add_node("agent", call_model)
        >>> builder.add_node("tools", durable_tool_node([search_orders, refund_order]))
        >>> builder.add_edge(START, "agent")
        >>> builder.add_conditional_edges("agent", tools_condition)
        >>> builder.add_edge("tools", "agent")
        >>> flow = langgraph_agent_session_flow(builder.compile(), flow_type="SupportGraph")

    Args:
        tools: Tools the node can run, as ``ToolNode`` accepts them.
        **tool_node_options: Any other ``ToolNode`` keyword argument, such as
            ``name`` or ``messages_key``. ``wrap_tool_call`` and
            ``awrap_tool_call`` are not allowed, because tools never run in the
            graph inside a session.

    Returns:
        The tool node.

    Raises:
        ValueError: If ``tool_node_options`` sets a tool call wrapper.
    """
    for wrapper_option in ("wrap_tool_call", "awrap_tool_call"):
        if wrapper_option in tool_node_options:
            raise ValueError(f"durable_tool_node does not accept {wrapper_option}")
    return DurableToolNode(tools, **tool_node_options)


def tool_effect_from_metadata(tool: BaseTool) -> ToolEffect | None:
    """Return the effect a tool's metadata declares, as MCP annotations do.

    The session uses it as the framework effect when no ``ToolPolicy`` names
    the tool. A tool whose ``metadata`` has ``readOnlyHint`` set to ``True`` is
    ``READ``. Otherwise one with ``idempotentHint`` set to ``True`` is
    ``IDEMPOTENT_WRITE``. Tools converted from MCP tools carry these
    annotations in their metadata.

    Example:
        >>> lookup.metadata = {"readOnlyHint": True}
        >>> tool_effect_from_metadata(lookup)
        <ToolEffect.READ: 'read'>

    Args:
        tool: A LangChain tool.

    Returns:
        The declared effect, or ``None`` when the metadata declares none.
    """
    metadata = tool.metadata or {}
    if metadata.get("readOnlyHint") is True:
        return ToolEffect.READ
    if metadata.get("idempotentHint") is True:
        return ToolEffect.IDEMPOTENT_WRITE
    return None


def get_tool_call_idempotency_key(config: RunnableConfig) -> str | None:
    """Return the idempotency key of the deferred tool call that is running.

    A tool call runs in its own Dex Step, which passes the session's
    ``DeferredToolExecution.idempotency_key``,
    ``"<flow_id>/<turn_id>/<tool_call_id>"``, in the tool's ``RunnableConfig``.
    The key is the same on every attempt of the call. Give it to an external
    system that deduplicates requests, so an ``IDEMPOTENT_WRITE`` retry has no
    further effect. Declare a ``config: RunnableConfig`` parameter on the tool
    function to receive the config.

    Example:
        >>> @tool
        ... async def charge_card(amount_cents: int, config: RunnableConfig) -> str:
        ...     \"\"\"Charge the customer's card.\"\"\"
        ...     key = get_tool_call_idempotency_key(config)
        ...     return await payments.charge(amount_cents, idempotency_key=key)

    Args:
        config: The config LangChain passed to the tool.

    Returns:
        The key while a tool call Step runs the tool; ``None`` outside an
        agent session.
    """
    key = config.get("configurable", {}).get(TOOL_CALL_IDEMPOTENCY_KEY_CONFIGURABLE)
    return key if isinstance(key, str) else None


@dataclass(kw_only=True)
class _RunModelBinding:
    turn: AgentTurnContext
    tools_by_name: Mapping[str, BaseTool]
    resumed_tool_call_ids: set[str] = field(default_factory=set)


@dataclass(frozen=True)
class _ToolStepBinding:
    pass


_ACTIVE_TOOL_BINDING: ContextVar[_RunModelBinding | _ToolStepBinding | None] = ContextVar(
    "dex_agents_langgraph_active_tool_binding", default=None
)


@dataclass(frozen=True, kw_only=True)
class _DeferredToolCallRequest:
    tool_call_id: str
    tool_name: str
    arguments: str
    policy: ToolPolicy


@dataclass(frozen=True, kw_only=True)
class _RecordedToolCallOutcome:
    tool_call_id: str
    status: ToolCallStatus
    output: str

    def to_tool_message(self, tool_name: str) -> ToolMessage:
        return ToolMessage(
            content=self.output,
            tool_call_id=self.tool_call_id,
            name=tool_name,
            status="success" if self.status is ToolCallStatus.SUCCEEDED else "error",
        )


def _deferred_tool_calls_interrupt_value(
    requests: Sequence[_DeferredToolCallRequest],
) -> dict[str, Any]:
    return {
        "type": _DEFERRED_TOOL_CALLS_INTERRUPT_TYPE,
        "tool_calls": [
            {
                "tool_call_id": request.tool_call_id,
                "tool_name": request.tool_name,
                "arguments": request.arguments,
                "effect": str(request.policy.effect),
                "requires_approval": request.policy.is_approval_required,
            }
            for request in requests
        ],
    }


def _deferred_tool_call_requests(value: object) -> list[_DeferredToolCallRequest] | None:
    if not isinstance(value, Mapping) or value.get("type") != _DEFERRED_TOOL_CALLS_INTERRUPT_TYPE:
        return None
    return [
        _DeferredToolCallRequest(
            tool_call_id=str(call["tool_call_id"]),
            tool_name=str(call["tool_name"]),
            arguments=str(call["arguments"]),
            policy=ToolPolicy(ToolEffect(call["effect"]), bool(call["requires_approval"])),
        )
        for call in value["tool_calls"]
    ]


def _recorded_outcomes_resume_value(
    outcomes: Sequence[_RecordedToolCallOutcome],
) -> dict[str, Any]:
    return {
        "results": {
            outcome.tool_call_id: {"status": str(outcome.status), "output": outcome.output}
            for outcome in outcomes
        }
    }


def _recorded_outcomes_from_resume_value(value: object) -> dict[str, _RecordedToolCallOutcome]:
    results = value.get("results") if isinstance(value, Mapping) else None
    if not isinstance(results, Mapping):
        raise ValueError(f"deferred tool calls resumed with an unexpected value {value!r}")
    return {
        str(tool_call_id): _RecordedToolCallOutcome(
            tool_call_id=str(tool_call_id),
            status=ToolCallStatus(result["status"]),
            output=str(result["output"]),
        )
        for tool_call_id, result in results.items()
    }


def _defer_tool_calls(
    binding: _RunModelBinding,
    tool_calls: Sequence[ToolCall],
    request_tool: BaseTool | None,
    node_tools_by_name: Mapping[str, BaseTool] | None = None,
) -> list[ToolMessage]:
    requests = [
        _DeferredToolCallRequest(
            tool_call_id=tool_call["id"] or "",
            tool_name=tool_call["name"],
            arguments=json.dumps(tool_call["args"], ensure_ascii=False, default=to_jsonable_python),
            # The tool Step runs the runner's tool, where tools= overrides a node's tool.
            policy=_resolve_deferred_tool_policy(
                binding,
                tool_call["name"],
                binding.tools_by_name.get(tool_call["name"])
                or request_tool
                or (node_tools_by_name or {}).get(tool_call["name"]),
            ),
        )
        for tool_call in tool_calls
    ]
    # No side effect may precede interrupt(): LangGraph reruns this node from its start.
    recorded = _recorded_outcomes_from_resume_value(
        interrupt(_deferred_tool_calls_interrupt_value(requests))
    )
    tool_messages: list[ToolMessage] = []
    for request in requests:
        outcome = recorded.get(request.tool_call_id)
        if outcome is None:
            raise RuntimeError(f"tool call {request.tool_call_id} resumed without its result")
        binding.resumed_tool_call_ids.add(request.tool_call_id)
        tool_messages.append(outcome.to_tool_message(request.tool_name))
    return tool_messages


def _resolve_deferred_tool_policy(
    binding: _RunModelBinding,
    tool_name: str,
    tool: BaseTool | None,
) -> ToolPolicy:
    if tool is None and tool_name not in binding.turn.tool_policies:
        # Nothing can run for an unknown tool; its Step only records the error.
        return ToolPolicy(ToolEffect.READ, requires_approval=False)
    framework_effect = None if tool is None else tool_effect_from_metadata(tool)
    return binding.turn.resolve_tool_policy(tool_name, framework_effect)


@contextlib.contextmanager
def _bind_run_model(binding: _RunModelBinding) -> Iterator[None]:
    # Graph tasks copy this context when they start, so concurrent sessions stay apart.
    token = _ACTIVE_TOOL_BINDING.set(binding)
    try:
        yield
    finally:
        _ACTIVE_TOOL_BINDING.reset(token)


@contextlib.contextmanager
def _bind_tool_step() -> Iterator[None]:
    token = _ACTIVE_TOOL_BINDING.set(_ToolStepBinding())
    try:
        yield
    finally:
        _ACTIVE_TOOL_BINDING.reset(token)


def _require_active_tool_binding() -> _RunModelBinding | _ToolStepBinding:
    binding = _ACTIVE_TOOL_BINDING.get()
    if binding is None:
        raise RuntimeError(
            "durable tool calls run only inside a Dex agent session; register the graph with "
            "langchain_agent_session_flow or langgraph_agent_session_flow"
        )
    return binding
