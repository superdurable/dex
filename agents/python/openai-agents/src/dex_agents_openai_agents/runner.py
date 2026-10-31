# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Step-mode ``AgentFrameworkRunner`` for the OpenAI Agents SDK."""

from __future__ import annotations

import asyncio
import contextlib
import copy
import json
from collections.abc import Mapping
from contextvars import ContextVar
from typing import Any

from agents import (
    Agent,
    ItemHelpers,
    RunConfig,
    RunContextWrapper,
    RunHooks,
    Runner,
    RunResultStreaming,
    RunState,
    ToolInputGuardrailData,
    Usage,
)
from agents.agent_tool_state import consume_agent_tool_run_result, get_agent_tool_state_scope
from agents.run import DEFAULT_MAX_TURNS
from agents.tool_context import ToolContext
from openai.types.responses import ResponseFunctionToolCall
from pydantic import ValidationError

from dex_agents import (
    AgentFrameworkRunner,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    DeferredToolCall,
    DeferredToolExecution,
    FrameworkMessage,
    MessageRole,
    ToolCallsRequested,
    ToolCallStatus,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
    describe_tool_call_outcome,
)
from dex_agents_openai_agents._agent_graph import (
    ACTIVE_TURN,
    ActiveTurn,
    DeferrableAgentGraph,
    DeferrableFunctionTool,
)
from dex_agents_openai_agents._items import (
    build_user_input_item,
    format_tool_output,
    project_input_items,
    read_input_items,
)
from dex_agents_openai_agents._streaming import forward_stream_events

_ACTIVE_DEFERRED_TOOL_EXECUTION: ContextVar[DeferredToolExecution | None] = ContextVar(
    "dex_openai_agents_active_deferred_tool_execution", default=None
)


def get_current_deferred_tool_execution() -> DeferredToolExecution | None:
    """Return the Dex tool call that the current function tool invocation runs for.

    Call it from inside a function tool. Every function tool call runs in its
    own Dex Step, and there the function returns the ``DeferredToolExecution``.
    Its ``idempotency_key`` is ``"<flow_id>/<turn_id>/<tool_call_id>"`` on every
    attempt, and its ``attempt`` counts Step attempts. Pass the key to an
    external system that supports idempotency, so a retried
    ``IDEMPOTENT_WRITE`` takes effect once.

    The function returns ``None`` outside a tool call Step, such as in a test
    that calls the tool directly. It also returns ``None`` for a tool that runs
    inside an agent tool's nested run, because that call is not the Step's
    own call and must not reuse its key.

    Example:
        >>> @function_tool
        ... async def create_refund(order_id: str) -> str:
        ...     execution = get_current_deferred_tool_execution()
        ...     key = execution.idempotency_key if execution else None
        ...     return await payments.refund(order_id, idempotency_key=key)

    Returns:
        The execution of the tool call that runs now, or ``None``.
    """
    return _ACTIVE_DEFERRED_TOOL_EXECUTION.get()


class OpenAIAgentsRunner(AgentFrameworkRunner):
    """Run an OpenAI Agents SDK agent in Step mode inside the Dex agent session Flow.

    Pass the runner to ``AgentSessionFlow``, or let
    ``openai_agents_session_flow`` build both. Each ``RunModel`` Step runs
    ``Runner.run_streamed`` for one model call. The session stores the
    conversation as the SDK's client-managed input items
    (``RunResult.to_input_list()``), so no server-side conversation state is used.

    At construction the runner copies the agent and every agent reachable
    through its handoffs. Every copied ``FunctionTool``, READ tools included,
    gets a ``needs_approval`` gate that always stops the SDK, so a run ends
    right after the model asks for tools. The stop becomes
    ``ToolCallsRequested`` with the serialized ``RunState``, and the session runs
    every call in its own Step, in parallel. The next ``RunModel`` restores the
    ``RunState`` and approves every call, and the copied tool returns the Dex
    result instead of running. A rejected call is rejected with its message.
    Later changes to the copied agents do not reach the runner. Agents used
    as tools (``Agent.as_tool()``) are not copied, so changes to them do.

    A handoff is not a function tool, so the SDK runs it and makes the next
    agent's model call in the same ``RunModel``. Provider-hosted tools, such as
    ``WebSearchTool``, run inside the model call. Tools the SDK cannot gate,
    such as ``Agent.mcp_servers`` tools and local non-function tools, are
    refused unless ``allow_at_least_once_tools`` is ``True``.

    The runner is stateless between calls and safe to share across concurrent
    sessions and parallel tool call Steps in one Worker.

    Example:
        >>> agent = Agent(name="Support", instructions="Help the user.",
        ...               tools=[search_orders, create_refund])
        >>> runner = OpenAIAgentsRunner(agent)
        >>> options = AgentSessionOptions(tool_policies={
        ...     "search_orders": ToolPolicy(ToolEffect.READ)})
        >>> runner.validate_tool_policies(options.tool_policies)
        >>> flow = AgentSessionFlow(runner, flow_type="SupportAgent", options=options)
    """

    def __init__(
        self,
        agent: Agent[Any],
        *,
        context: Any = None,
        run_config: RunConfig | None = None,
        max_turns: int | None = DEFAULT_MAX_TURNS,
        hooks: RunHooks[Any] | None = None,
        allow_at_least_once_tools: bool = False,
    ) -> None:
        """Copy the agent graph and keep the run settings for every model call.

        Args:
            agent: The starting agent. Every agent it reaches through
                ``handoffs`` is copied too. Handoffs must be ``Agent`` objects or
                ``Handoff`` objects made with ``handoff(agent)``.
            context: The run context passed as ``context`` to every run and to
                every ``ToolContext``. The runner never restores it from storage.
                A mapping context is also written into the serialized
                ``RunState`` while tool calls run, so keep secrets out of mapping
                contexts. Defaults to ``None``.
            run_config: SDK run settings, such as the model provider and
                tracing, used for every run. ``None`` uses the SDK defaults.
            max_turns: Maximum SDK turns, which are model calls, in one session
                turn across all its ``RunModel`` Steps. ``None`` disables the
                limit. Defaults to the SDK default, 10. Exceeding it fails the
                ``RunModel`` attempt.
            hooks: SDK run hooks. They run inside ``RunModel`` and run again
                when it retries.
            allow_at_least_once_tools: Accept tools that the SDK runs itself
                inside ``RunModel``: tools of ``Agent.mcp_servers``, sandbox
                capabilities, and ``ComputerTool``, ``CustomTool``,
                ``ApplyPatchTool``, ``LocalShellTool``, or a local ``ShellTool``.
                They run again when ``RunModel`` retries, and a ``RunModel`` that
                runs one also makes the next model call. Defaults to ``False``,
                which raises ``ValueError`` for them.

        Raises:
            TypeError: If ``context`` is a ``RunContextWrapper``. Pass the
                application value it would wrap instead.
            ValueError: If two different function tools in the agent graph have
                the same qualified name, a handoff has no known target agent, an
                agent tool's nested agent has a tool that needs approval, or the
                graph has a tool the SDK cannot gate and
                ``allow_at_least_once_tools`` is ``False``.
        """
        if isinstance(context, RunContextWrapper):
            raise TypeError("pass the application context value, not a RunContextWrapper")
        self._graph = DeferrableAgentGraph(
            agent, allow_at_least_once_tools=allow_at_least_once_tools
        )
        self._context = context
        self._run_config = run_config
        self._max_turns = max_turns
        self._hooks = hooks

    @property
    def agent(self) -> Agent[Any]:
        """Return the copy of the starting agent that the runner runs.

        Its function tools work only inside a session model call; running the
        copy with ``Runner`` directly raises ``RuntimeError`` at the first tool
        call.

        Returns:
            The starting agent's copy with gated function tools.
        """
        return self._graph.starting_agent

    def validate_tool_policies(self, tool_policies: Mapping[str, ToolPolicy]) -> None:
        """Check that every tool policy names a function tool of the agent graph.

        ``openai_agents_session_flow`` calls it at construction. Call it yourself
        when you build ``AgentSessionFlow`` directly. A policy for a tool the SDK
        lists at run time, such as an MCP server tool, would never apply, so it
        is an error.

        Args:
            tool_policies: The session's explicit policies by qualified tool name.

        Raises:
            ValueError: If a policy names a tool that is not in the agent graph.
        """
        self._graph.validate_tool_policies(tool_policies)

    def build_user_message(self, text: str) -> FrameworkMessage:
        """Store user text as an SDK input item.

        The session also stores the turn-failure note through this method, so
        the next turn's model sees it as user input.

        Args:
            text: The user message text.

        Returns:
            A user message whose ``framework_item`` is ``{"role": "user", "content": text}``.
        """
        return FrameworkMessage(
            role=MessageRole.USER,
            framework_item=json.dumps(build_user_input_item(text), ensure_ascii=False),
            text=text,
        )

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        """Let the agent make one model call, starting or continuing the turn.

        Without run state, the runner replays every stored ``framework_item``
        as SDK input. With run state, it restores the ``RunState``, approves
        every interruption whose record is final, so the gated tool returns the
        recorded result, and rejects rejected calls with their rejection
        message. The run then makes one model call; a handoff in its response
        adds the next agent's model call. Text and reasoning-summary deltas go
        to ``turn.events``.

        Args:
            turn: Committed session state for this model call.

        Returns:
            ``ToolCallsRequested`` with ``RunState.to_json()`` and one call per
            function tool call, in model order, when the model asked for tools.
            Otherwise ``TurnCompleted`` with every input item the turn produced.

        Raises:
            ValueError: If the SDK stopped for a tool outside the gated agent
                graph, such as a nested agent tool or a local MCP server tool.
            Exception: Any SDK or model error fails the ``RunModel`` attempt.
        """
        active_turn = ActiveTurn(turn=turn)
        token = ACTIVE_TURN.set(active_turn)
        try:
            run_input: list[Any] | RunState[Any, Agent[Any]]
            if turn.framework_run_state is None:
                run_input = read_input_items(turn.messages)
            else:
                run_input = await self._restore_run_state(turn, turn.framework_run_state)
            result = Runner.run_streamed(
                self._graph.starting_agent,
                run_input,
                context=self._context,
                max_turns=self._max_turns,
                hooks=self._hooks,
                run_config=self._run_config,
            )
            # Nothing runs before the first await, so this is the usage restored from RunState.
            restored_usage = copy.deepcopy(result.context_wrapper.usage)
            await forward_stream_events(result, turn.events)
        finally:
            ACTIVE_TURN.reset(token)
        usage = _segment_usage(result.context_wrapper.usage, restored_usage)
        if result.interruptions:
            return self._request_tool_calls(active_turn, result, usage)
        new_items = result.to_input_list()[len(ItemHelpers.input_to_new_input_list(result.input)) :]
        return TurnCompleted(
            new_messages=project_input_items(turn.messages, new_items), usage=usage
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        """Run the application's function tool once for one tool call Step.

        The runner first parses the arguments and validates them against a
        decorated tool's parameters; invalid arguments raise
        ``ToolExecutionError``, so the model sees the problem and nothing runs.
        It then runs the tool's input guardrails. A ``reject_content`` result
        or a tripwire raises ``ToolExecutionError`` and the body does not run;
        the resumed ``RunModel`` does not run them again. Last, it calls the
        original tool's ``on_invoke_tool`` with a
        ``ToolContext`` that carries the runner's context, the tool call ID, the
        arguments, and the owning agent. The SDK's tool failure formatter is
        off here, so exceptions reach the session: raise
        ``dex_agents.ToolExecutionError`` from the tool for a failure the model
        should see. Any other exception fails the Step. A ``READ`` or
        ``IDEMPOTENT_WRITE`` call retries up to three attempts; after the last,
        the session records ``failed`` for a read and ``outcome_unknown`` for a
        write. A single-attempt call records ``outcome_unknown`` at once. The
        tool's ``timeout_seconds`` applies, and a timeout fails the Step.

        Args:
            call: The approved record and its idempotency key. Inside the tool,
                ``get_current_deferred_tool_execution()`` returns it.

        Returns:
            The tool output as text. A tool with an output type records the
            JSON that its type adapter dumps; other structured outputs become
            JSON text.

        Raises:
            ToolExecutionError: If the tool is unknown, the arguments are
                invalid, an input guardrail rejected or tripped, a nested agent
                tool run stopped for approval, or the tool raised it.
            Exception: Any other tool exception fails the Step attempt.
        """
        record = call.tool_call
        tool = self._graph.tools_by_qualified_name.get(record.tool_name)
        if tool is None:
            raise ToolExecutionError(f"tool {record.tool_name} is not available to this agent")
        _validate_tool_arguments(tool, record.arguments)
        context = ToolContext(
            context=self._context,
            usage=Usage(),
            tool_name=tool.name,
            tool_call_id=record.tool_call_id,
            tool_arguments=record.arguments,
            tool_call=ResponseFunctionToolCall(
                type="function_call",
                name=tool.name,
                arguments=record.arguments,
                call_id=record.tool_call_id,
                namespace=tool.namespace,
            ),
            tool_namespace=tool.namespace,
            agent=tool.owner_agent,
            run_config=self._run_config,
        )
        await _run_tool_input_guardrails(tool, context)
        execution_tool = tool.deferred_execution_tool
        # Tools nested in an agent tool are separate calls and must not reuse its key.
        is_agent_tool = bool(getattr(tool.original_tool, "_is_agent_tool", False))
        token = _ACTIVE_DEFERRED_TOOL_EXECUTION.set(None if is_agent_tool else call)
        try:
            timeout = (
                asyncio.timeout(execution_tool.timeout_seconds)
                if execution_tool.timeout_seconds is not None
                else contextlib.nullcontext()
            )
            async with timeout:
                output = await execution_tool.on_invoke_tool(context, record.arguments)
        finally:
            _ACTIVE_DEFERRED_TOOL_EXECUTION.reset(token)
        _reject_nested_interruptions(tool, context)
        return ToolExecutionResult(output=_record_tool_output(tool, output))

    async def _restore_run_state(
        self,
        turn: AgentTurnContext,
        framework_run_state: str,
    ) -> RunState[Any, Agent[Any]]:
        state = await RunState.from_json(
            self._graph.starting_agent,
            json.loads(framework_run_state),
            context_override=self._context,
        )
        for interruption in state.get_interruptions():
            call_id = interruption.call_id
            record = turn.recorded_tool_call(call_id) if call_id else None
            if record is None or not record.status.is_final:
                raise RuntimeError(f"interrupted tool call {call_id} has no final record")
            if record.status is ToolCallStatus.REJECTED:
                state.reject(interruption, rejection_message=describe_tool_call_outcome(record))
            else:
                state.approve(interruption)
        return state

    def _request_tool_calls(
        self,
        active_turn: ActiveTurn,
        result: RunResultStreaming,
        usage: AgentUsage,
    ) -> ToolCallsRequested:
        tool_calls: list[DeferredToolCall] = []
        for interruption in result.interruptions:
            tool = self._graph.find_interrupted_tool(active_turn, interruption)
            tool_call_id = interruption.call_id or ""
            tool_calls.append(
                DeferredToolCall(
                    tool_call_id=tool_call_id,
                    tool_name=tool.qualified_name,
                    arguments=interruption.arguments or "{}",
                    policy=active_turn.resolve_tool_call_policy(tool, tool_call_id),
                )
            )
        state_json = result.to_state().to_json(context_serializer=_omit_application_context)
        return ToolCallsRequested(
            framework_run_state=json.dumps(state_json, ensure_ascii=False),
            tool_calls=tool_calls,
            usage=usage,
        )


def _validate_tool_arguments(tool: DeferrableFunctionTool, arguments: str) -> None:
    try:
        parsed = json.loads(arguments) if arguments.strip() else {}
    except json.JSONDecodeError as error:
        raise ToolExecutionError(f"Invalid JSON input for tool {tool.name}: {error}") from error
    if not isinstance(parsed, dict):
        raise ToolExecutionError(f"Invalid JSON input for tool {tool.name}: expected an object")
    if tool.arguments_model is None:
        return
    try:
        tool.arguments_model(**parsed)
    except ValidationError as error:
        raise ToolExecutionError(f"Invalid JSON input for tool {tool.name}: {error}") from error


async def _run_tool_input_guardrails(
    tool: DeferrableFunctionTool, context: ToolContext[Any]
) -> None:
    for guardrail in tool.original_tool.tool_input_guardrails or []:
        output = await guardrail.run(
            ToolInputGuardrailData(context=context, agent=tool.owner_agent)
        )
        behavior = output.behavior
        if behavior["type"] == "reject_content":
            raise ToolExecutionError(behavior["message"])
        if behavior["type"] == "raise_exception":
            raise ToolExecutionError(
                f"tool input guardrail {guardrail.get_name()} stopped the call to {tool.name}"
            )


def _reject_nested_interruptions(tool: DeferrableFunctionTool, context: ToolContext[Any]) -> None:
    if not getattr(tool.original_tool, "_is_agent_tool", False) or context.tool_call is None:
        return
    nested_result = consume_agent_tool_run_result(
        context.tool_call, scope_id=get_agent_tool_state_scope(context)
    )
    interruptions = getattr(nested_result, "interruptions", None)
    if interruptions:
        names = ", ".join(str(interruption.name) for interruption in interruptions)
        raise ToolExecutionError(
            f"agent tool {tool.name} stopped for a nested approval of {names}, which a tool "
            "call Step cannot wait for"
        )


def _record_tool_output(tool: DeferrableFunctionTool, output: Any) -> str:
    adapter = tool.output_type_adapter
    if adapter is None:
        return format_tool_output(output)
    if isinstance(output, str):
        return output
    # The resumed run validates the recorded text with the same adapter.
    return adapter.dump_json(adapter.validate_python(output), by_alias=True).decode()


def _omit_application_context(context: Any) -> Mapping[str, Any]:
    return {}


def _segment_usage(total: Usage, restored: Usage) -> AgentUsage:
    return AgentUsage(
        model_calls=total.requests - restored.requests,
        input_tokens=total.input_tokens - restored.input_tokens,
        output_tokens=total.output_tokens - restored.output_tokens,
        cached_input_tokens=total.input_tokens_details.cached_tokens
        - restored.input_tokens_details.cached_tokens,
        reasoning_tokens=total.output_tokens_details.reasoning_tokens
        - restored.output_tokens_details.reasoning_tokens,
    )
