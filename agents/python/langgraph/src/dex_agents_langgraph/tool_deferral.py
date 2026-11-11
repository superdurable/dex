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
from dataclasses import asdict, dataclass, field
from typing import Any

from dex_agents import (
    AgentImageReference,
    AgentTurnContext,
    MediaStore,
    ToolCallStatus,
    ToolEffect,
    ToolPolicy,
)
from dex_agents.stepwise import SourceToolDefinition, SourceToolListing
from langchain.agents.middleware import AgentMiddleware, ModelRequest, ModelResponse
from langchain_core.messages import AIMessage, AnyMessage, ToolCall, ToolMessage
from langchain_core.runnables import RunnableConfig
from langchain_core.tools import BaseTool, StructuredTool
from langgraph.prebuilt import ToolNode
from langgraph.prebuilt.tool_node import ToolCallRequest
from langgraph.runtime import Runtime
from langgraph.types import Command, interrupt
from pydantic import BaseModel
from pydantic_core import to_jsonable_python

from dex_agents_langgraph._images import (
    image_reference_block,
    inline_message_images,
    message_image_references,
)

#: ``RunnableConfig["configurable"]`` key that carries a deferred call's idempotency key.
TOOL_CALL_IDEMPOTENCY_KEY_CONFIGURABLE = "dex_tool_call_idempotency_key"

_DEFERRED_TOOL_CALLS_INTERRUPT_TYPE = "dex_deferred_tool_calls"

#: The callable a ``wrap_tool_call`` hook receives to run the tool.
ToolCallExecutor = Callable[[ToolCallRequest], Awaitable[ToolMessage | Command[Any]]]

_ModelCallResult = ModelResponse[Any] | AIMessage


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

    Inside a session it also adds the tools of the session's tool sources,
    such as ``ConnectorToolSource``, to every model request, after the
    agent's own tools, so the model can call them. Their calls are deferred
    like any other call and run through their source in their own Step.

    With a runner ``media_store``, it also replaces each image reference in
    the request's messages with the image's bytes, as a base64 image block,
    right before the model call. The graph state keeps the references.

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

    def wrap_model_call(
        self,
        request: ModelRequest[Any],
        handler: Callable[[ModelRequest[Any]], _ModelCallResult],
    ) -> _ModelCallResult:
        """Add the session's tool source tools to one model request.

        ``create_agent`` calls this hook for every model call; applications do
        not call it. Outside a session the request passes unchanged. The
        runner always calls the model asynchronously, through
        ``awrap_model_call``.

        .. versionadded:: 0.4.0

        .. versionchanged:: 0.5.0
            Raises ``RuntimeError`` for a request that holds image references,
            whose bytes only the asynchronous hook can read.

        Args:
            request: The model request.
            handler: Runs the model, or the next wrapper.

        Returns:
            What ``handler`` returns.

        Raises:
            RuntimeError: If a message of the request holds an image reference.
        """
        if any(message_image_references(message) for message in request.messages):
            raise RuntimeError(
                "a model request with image references needs the asynchronous model call"
            )
        return handler(_with_source_tools(request))

    async def awrap_model_call(
        self,
        request: ModelRequest[Any],
        handler: Callable[[ModelRequest[Any]], Awaitable[_ModelCallResult]],
    ) -> _ModelCallResult:
        """Add the session's tool source tools and image bytes to one model request.

        ``create_agent`` calls this hook for every model call; applications do
        not call it. Outside a session the request passes unchanged.

        .. versionadded:: 0.4.0

        .. versionchanged:: 0.5.0
            Replaces image references with their bytes, as
            ``inline_image_references`` does.

        Args:
            request: The model request.
            handler: Runs the model, or the next wrapper.

        Returns:
            What ``handler`` returns.

        Raises:
            RuntimeError: If a message holds an image reference and the runner
                has no ``media_store``.
            MediaStoreError: If the store cannot return an image's bytes. The
                model call fails, and its ``RunModel`` Step retries.
        """
        request = _with_source_tools(request)
        messages = await inline_image_references(request.messages)
        if messages is not request.messages:
            request = request.override(messages=messages)
        return await handler(request)


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


def get_current_tool_source_tools() -> list[BaseTool]:
    """Return the tools that the session's tool sources offer to the current model call.

    Call it in the model node of a custom graph and bind the tools next to the
    graph's own, so the model can call them. ``DurableToolCallMiddleware``
    adds them to a ``create_agent`` agent by itself. Each tool has the
    source's name, description, and JSON Schema. Its body never runs: a
    ``durable_tool_node`` defers every call, and the session runs it through
    its source in its own Step.

    .. versionadded:: 0.4.0

    Example:
        >>> async def call_model(state: MessagesState) -> dict[str, list[AIMessage]]:
        ...     tools = [search_orders, *get_current_tool_source_tools()]
        ...     reply = await model.bind_tools(tools).ainvoke(state["messages"])
        ...     return {"messages": [reply]}

    Returns:
        The tools of the ``RunModel`` that runs the graph, in the order the
        sources list them; empty outside a session, in a tool Step, and when
        the session has no tool sources.
    """
    binding = _ACTIVE_TOOL_BINDING.get()
    if not isinstance(binding, _RunModelBinding):
        return []
    return list(binding.source_tools)


async def inline_image_references(messages: list[AnyMessage]) -> list[AnyMessage]:
    """Return the messages with each Dex image reference replaced by the image's bytes.

    Call it in the model node of a custom graph, right before the chat model
    call, when the runner has a ``media_store``. ``DurableToolCallMiddleware``
    does this for a ``create_agent`` agent by itself. The session stores each
    image of a user message or a tool result as a reference: a standard image
    block whose ``url`` is the reference's ``uri`` and whose ``extras`` hold
    the reference under ``"dex_image_reference"``. This function reads the
    bytes from the runner's store and returns copies of the messages in which
    each such block is ``{"type": "image", "base64": ..., "mime_type": ...}``.
    It leaves the given messages and the graph state unchanged, so the state
    and the stored messages keep holding references.

    .. versionadded:: 0.5.0

    Example:
        >>> async def call_model(state: MessagesState) -> dict[str, list[AIMessage]]:
        ...     messages = await inline_image_references(state["messages"])
        ...     reply = await model.bind_tools(tools).ainvoke(messages)
        ...     return {"messages": [reply]}

    Args:
        messages: The messages the model call sends.

    Returns:
        ``messages`` itself when no message holds an image reference, or
        outside a session's ``RunModel``; otherwise a new list in which each
        message with references is a copy with the bytes.

    Raises:
        RuntimeError: If a message holds an image reference and the runner has
            no ``media_store``.
        MediaStoreError: If the store cannot return an image's bytes. The model
            call fails, and its ``RunModel`` Step retries.
    """
    binding = _ACTIVE_TOOL_BINDING.get()
    if not isinstance(binding, _RunModelBinding):
        return messages
    inlined = await inline_message_images(messages, binding.media_store)
    return messages if inlined is None else inlined


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
    source_tools: Sequence[BaseTool] = ()
    source_tool_listing: SourceToolListing | None = None
    media_store: MediaStore | None = None
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
    tool_name: str
    status: ToolCallStatus
    output: str
    images: Sequence[AgentImageReference] = ()

    def to_tool_message(self, tool_name: str) -> ToolMessage:
        content: str | list[str | dict[str, Any]] = self.output
        if self.images:
            content = [
                {"type": "text", "text": self.output},
                *map(image_reference_block, self.images),
            ]
        return ToolMessage(
            content=content,
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
            outcome.tool_call_id: {
                "tool_name": outcome.tool_name,
                "status": str(outcome.status),
                "output": outcome.output,
                # References only; without images the value keeps its earlier shape.
                **(
                    {"images": [asdict(image) for image in outcome.images]}
                    if outcome.images
                    else {}
                ),
            }
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
            tool_name=str(result["tool_name"]),
            status=ToolCallStatus(result["status"]),
            output=str(result["output"]),
            images=[AgentImageReference(**image) for image in result.get("images", ())],
        )
        for tool_call_id, result in results.items()
    }


def _defer_tool_calls(
    binding: _RunModelBinding,
    tool_calls: Sequence[ToolCall],
    request_tool: BaseTool | None,
    node_tools_by_name: Mapping[str, BaseTool] | None = None,
) -> list[ToolMessage]:
    for tool_call in tool_calls:
        if not tool_call["id"]:
            # The session records and runs each call by its ID, so it cannot guess one.
            raise RuntimeError(
                f"the model called tool {tool_call['name']} without a tool call ID; a Dex "
                "agent session needs one to record the call"
            )
    # One entry per call, in model order: a refusal text, or the request that is deferred.
    entries: list[str | _DeferredToolCallRequest] = [
        _source_tool_refusal(binding, tool_call["id"] or "", tool_call["name"])
        or _DeferredToolCallRequest(
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
    requests = [entry for entry in entries if isinstance(entry, _DeferredToolCallRequest)]
    # No side effect may precede interrupt(): LangGraph reruns this node from its start.
    recorded = (
        _recorded_outcomes_from_resume_value(
            interrupt(_deferred_tool_calls_interrupt_value(requests))
        )
        if requests
        else {}
    )
    tool_messages: list[ToolMessage] = []
    for tool_call, entry in zip(tool_calls, entries, strict=True):
        if isinstance(entry, _DeferredToolCallRequest):
            tool_messages.append(_recorded_tool_message(binding, entry, recorded))
        else:
            tool_messages.append(
                ToolMessage(
                    content=entry,
                    tool_call_id=tool_call["id"] or "",
                    name=tool_call["name"],
                    status="error",
                )
            )
    return tool_messages


def _source_tool_refusal(
    binding: _RunModelBinding, tool_call_id: str, tool_name: str
) -> str | None:
    """Return the refusal of a new call of a tool its source no longer lists, else ``None``."""
    listing = binding.source_tool_listing
    if listing is None or binding.turn.recorded_tool_call(tool_call_id) is not None:
        return None
    return listing.not_offered_result(tool_name) if listing.is_replay_only(tool_name) else None


def _recorded_tool_message(
    binding: _RunModelBinding,
    request: _DeferredToolCallRequest,
    recorded: Mapping[str, _RecordedToolCallOutcome],
) -> ToolMessage:
    """Replay a resumed call's record once, and only for the tool the record names."""
    outcome = recorded.get(request.tool_call_id)
    if outcome is None:
        raise RuntimeError(f"tool call {request.tool_call_id} resumed without its result")
    if outcome.tool_name != request.tool_name:
        raise RuntimeError(
            f"tool call {request.tool_call_id} is recorded for {outcome.tool_name}, "
            f"not {request.tool_name}"
        )
    if request.tool_call_id in binding.resumed_tool_call_ids:
        raise RuntimeError(
            f"tool call {request.tool_call_id} already replayed its recorded result in this "
            "model call"
        )
    binding.resumed_tool_call_ids.add(request.tool_call_id)
    return outcome.to_tool_message(request.tool_name)


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


def _with_source_tools(request: ModelRequest[Any]) -> ModelRequest[Any]:
    binding = _ACTIVE_TOOL_BINDING.get()
    if not isinstance(binding, _RunModelBinding) or not binding.source_tools:
        return request
    return request.override(tools=[*request.tools, *binding.source_tools])


def _source_tool_as_langchain_tool(definition: SourceToolDefinition) -> BaseTool:
    # The model sees the source's schema; the body never runs, because every call defers.
    return StructuredTool(
        name=definition.name,
        description=definition.description,
        args_schema=dict(definition.input_schema),
        func=_refuse_inline_source_tool_call,
        coroutine=_refuse_inline_source_tool_call_async,
    )


def _refuse_inline_source_tool_call(**arguments: Any) -> str:
    raise RuntimeError("a tool source's tool runs only in its own Dex tool Step")


async def _refuse_inline_source_tool_call_async(**arguments: Any) -> str:
    raise RuntimeError("a tool source's tool runs only in its own Dex tool Step")


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
