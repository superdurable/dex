# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Stepwise agent definition and the runner that makes one model call per ``RunModel``."""

from __future__ import annotations

import json
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field, replace
from datetime import timedelta

from dex_agents.built_in_tools import BUILT_IN_TOOL_DEFINITIONS, BuiltInTool
from dex_agents.persistence import DEFAULT_AGENT_EVENTS_CAPACITY_BYTES
from dex_agents.runner import (
    AgentTurnContext,
    AgentTurnOutcome,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    FrameworkMessage,
    ToolCallsRequested,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
    describe_tool_call_outcome,
    resolve_tool_policy,
)
from dex_agents.session_flow import AgentSessionFlow, AgentSessionOptions
from dex_agents.stepwise.function_tools import FunctionTool, ToolCallContext
from dex_agents.stepwise.model import (
    ModelClient,
    ModelRequest,
    ModelRequestMetadata,
    ModelSettings,
    ModelToolDefinition,
    ToolChoice,
    ToolResultItem,
    UserTextItem,
)
from dex_agents.stepwise.transcript import (
    decode_stepwise_message,
    encode_stepwise_message,
    pair_tool_calls_with_results,
)
from dex_agents.values import MessageRole, ToolEffect

#: Default ``AgentDefinition.max_model_calls_per_turn``.
DEFAULT_MAX_MODEL_CALLS_PER_TURN = 100

#: Result of a tool call the model made after the turn's model call limit; ``str.format`` it.
TOOL_CALL_LIMIT_REACHED_RESULT = (
    "The tool call was not run: this turn reached its limit of "
    "{max_model_calls_per_turn} model calls."
)
_UNKNOWN_TOOL_POLICY = ToolPolicy(ToolEffect.READ, requires_approval=False)


@dataclass(frozen=True, kw_only=True)
class InstructionContext:
    """Tell a function of ``AgentDefinition.instructions`` which model call it serves.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        turn_id: ``"turn-<n>"`` of the running turn.
        attempt: One-based ``RunModel`` attempt.
    """

    flow_id: str
    turn_id: str
    attempt: int


@dataclass(frozen=True, kw_only=True)
class AgentDefinition:
    """Declare one Stepwise agent in Dex terms: instructions, model, and tools.

    Pass it to ``stepwise_agent_session_flow``, which builds the session Flow
    and derives its ``AgentSessionOptions``, so tool policies, built-in tools,
    and the interrupt deadline are declared once. Every model call runs in its
    own ``RunModel`` Step and every tool call in its own tool Step.

    Example:
        >>> support_agent = AgentDefinition(
        ...     name="support",
        ...     instructions="You help customers with their orders.",
        ...     model=LiteLLMModelClient("openai/gpt-5-mini"),
        ...     tools=[lookup_order, refund_order],
        ...     built_in_tools=frozenset({BuiltInTool.ASK_USER, BuiltInTool.DURABLE_WAIT}),
        ... )
        >>> flow = stepwise_agent_session_flow(support_agent, flow_type="SupportAgent")
        >>> registry = Registry([flow], allow_async_handlers=True)

    Attributes:
        name: Agent name, used in events and spans. It must not be blank.
        instructions: System instructions, or a function of
            ``InstructionContext`` that returns them. A function runs in every
            ``RunModel``, so a deploy changes the next model call.
        model: The model client.
        model_settings: Settings for every model call. Defaults to
            ``ModelSettings()``.
        tools: Function tools, with unique names. Defaults to none.
        tool_policies: Policies by tool name. They override a function tool's
            own effect and approval. Defaults to none.
        default_tool_effect: Effect of a tool from a source that declares
            none. Defaults to ``UNCLASSIFIED``.
        built_in_tools: Built-in tools the model sees. Their names are
            reserved. Defaults to none.
        max_model_calls_per_turn: After this many model calls in one turn,
            the next call offers no tools, so the model must answer. If the
            model asks for tools anyway, the turn ends with that response and
            each requested call gets the result ``The tool call was not run:
            this turn reached its limit of <n> model calls.``. Defaults to 100.
        interrupt_deadline: How long an approval or question may stay pending.
            ``None``, the default, waits indefinitely.
        event_stream_capacity_bytes: ``AgentEvents`` capacity. Defaults to 10 MiB.

    Raises:
        ValueError: If ``name`` is blank, two tools share a name, a tool or a
            tool policy uses an enabled built-in name, or
            ``max_model_calls_per_turn`` is not positive.
    """

    name: str
    instructions: str | Callable[[InstructionContext], str]
    model: ModelClient
    model_settings: ModelSettings = field(default_factory=ModelSettings)
    tools: Sequence[FunctionTool] = ()
    tool_policies: Mapping[str, ToolPolicy] = field(default_factory=dict)
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED
    built_in_tools: frozenset[BuiltInTool] = frozenset()
    max_model_calls_per_turn: int = DEFAULT_MAX_MODEL_CALLS_PER_TURN
    interrupt_deadline: timedelta | None = None
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES

    def __post_init__(self) -> None:
        if not self.name.strip():
            raise ValueError("AgentDefinition.name must not be blank")
        if self.max_model_calls_per_turn <= 0:
            raise ValueError("max_model_calls_per_turn must be positive")
        built_in_tools = frozenset(BuiltInTool(tool) for tool in self.built_in_tools)
        names = [tool.name for tool in self.tools]
        duplicates = sorted({tool_name for tool_name in names if names.count(tool_name) > 1})
        if duplicates:
            raise ValueError(f"two tools are named {duplicates[0]!r}")
        reserved = sorted(built_in_tools.intersection(names))
        if reserved:
            raise ValueError(_built_in_name_collision(reserved[0]))
        object.__setattr__(self, "tools", tuple(self.tools))
        object.__setattr__(self, "tool_policies", dict(self.tool_policies))
        object.__setattr__(self, "built_in_tools", built_in_tools)


class StepwiseAgentRunner:
    """Run an ``AgentDefinition`` as the native Dex Agents loop, with no framework.

    The runner implements ``AgentFrameworkRunner`` and
    ``UnfinishedTurnMessageBuilder``. ``run_model_step`` makes at most one
    model call, from committed state only. It stores every item as it appears:
    the assistant item that asks for tools commits with the tool call records,
    and the tool results commit with the next model call. A tool call is never
    stored without a result. Most applications use
    ``stepwise_agent_session_flow`` instead of constructing it.

    Example:
        >>> flow = AgentSessionFlow(StepwiseAgentRunner(definition), flow_type="SupportAgent",
        ...                         options=stepwise_agent_session_options(definition))
    """

    def __init__(self, definition: AgentDefinition) -> None:
        """Create the runner for one agent.

        Args:
            definition: The agent. The runner keeps no other state.
        """
        self._definition = definition
        self._tools_by_name = {tool.name: tool for tool in definition.tools}

    @property
    def definition(self) -> AgentDefinition:
        """Return the agent this runner runs.

        Returns:
            The definition passed to the constructor.
        """
        return self._definition

    def build_user_message(self, text: str) -> FrameworkMessage:
        """Store user text, and Dex notes, as a provider-neutral ``user_text`` item.

        Args:
            text: The text.

        Returns:
            A ``MessageRole.USER`` message.
        """
        return encode_stepwise_message(UserTextItem(text=text))

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        """Make one model call of the turn.

        Args:
            turn: Committed session state.

        Returns:
            ``ToolCallsRequested`` when the model asked for tools, otherwise
            ``TurnCompleted``. Both carry the previous round's tool results and
            the new output items.

        Raises:
            ValueError: If a function tool uses the name of a built-in tool
                that the session enables. No model request is made.
            Exception: What the model client raises.
        """
        reserved = sorted(
            tool.name for tool in self._definition.tools if tool.name in turn.enabled_built_in_tools
        )
        if reserved:
            # Session options can enable a built-in that the definition did not list.
            raise ValueError(_built_in_name_collision(reserved[0]))
        model_calls = _model_call_count(turn.framework_run_state)
        unstored_results = _unstored_tool_results(turn)
        is_tool_budget_spent = model_calls >= self._definition.max_model_calls_per_turn
        tools = [] if is_tool_budget_spent else self._tool_definitions(turn)
        tool_choice: ToolChoice = "none" if is_tool_budget_spent else "auto"
        request = ModelRequest(
            instructions=self._instructions(turn),
            input_items=pair_tool_calls_with_results(
                [
                    *(decode_stepwise_message(message) for message in turn.messages),
                    *unstored_results,
                ]
            ),
            tools=tools,
            tool_choice=tool_choice,
            settings=self._definition.model_settings,
            metadata=ModelRequestMetadata(
                flow_id=turn.flow_id,
                turn_id=turn.turn_id,
                step_execution_id=turn.events.step_execution_id,
                attempt=turn.attempt,
            ),
        )
        response = await self._definition.model.call_model(request, turn.events)
        usage = replace(response.usage, model_calls=1)
        new_messages = [
            *(encode_stepwise_message(result) for result in unstored_results),
            *(encode_stepwise_message(item) for item in response.output_items),
        ]
        if not response.tool_calls:
            return TurnCompleted(new_messages=new_messages, usage=usage)
        if is_tool_budget_spent:
            # The model ignored that no tools were offered; answer each call instead of running it.
            not_run = TOOL_CALL_LIMIT_REACHED_RESULT.format(
                max_model_calls_per_turn=self._definition.max_model_calls_per_turn
            )
            return TurnCompleted(
                new_messages=[
                    *new_messages,
                    *(
                        encode_stepwise_message(
                            ToolResultItem(
                                tool_call_id=call.tool_call_id,
                                tool_name=call.tool_name,
                                output=not_run,
                            )
                        )
                        for call in response.tool_calls
                    ),
                ],
                usage=usage,
            )
        return ToolCallsRequested(
            framework_run_state=json.dumps({"model_calls": model_calls + 1}),
            tool_calls=[
                DeferredToolCall(
                    tool_call_id=call.tool_call_id,
                    tool_name=call.tool_name,
                    arguments=call.arguments,
                    policy=self._tool_policy(turn, call.tool_name),
                )
                for call in response.tool_calls
            ],
            usage=usage,
            new_messages=new_messages,
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        """Run one function tool call in its own Step.

        Args:
            call: The approved record.

        Returns:
            The tool output, or the tool's ``ToolInputRequested``.

        Raises:
            ToolExecutionError: If the tool is unknown, the arguments break its
                schema, or the tool raised it.
        """
        record = call.tool_call
        tool = self._tools_by_name.get(record.tool_name)
        if tool is None:
            raise ToolExecutionError(f"unknown tool {record.tool_name}")
        result = await tool.invoke(
            record.arguments,
            ToolCallContext(
                flow_id=call.flow_id,
                turn_id=record.turn_id,
                tool_call_id=record.tool_call_id,
                idempotency_key=call.idempotency_key,
                attempt=call.attempt,
                events=call.events,
                heartbeat=call.heartbeat,
                input_answers=tuple(call.input_answers),
                input_continuation=record.input_continuation,
            ),
        )
        return result if not isinstance(result, str) else ToolExecutionResult(output=result)

    def build_unfinished_turn_messages(self, turn: AgentTurnContext) -> Sequence[FrameworkMessage]:
        """Return the results of the turn's tool calls that are not stored yet.

        Args:
            turn: The ending turn, with every record final.

        Returns:
            One ``tool_result`` message per unstored result.
        """
        return [encode_stepwise_message(result) for result in _unstored_tool_results(turn)]

    def _instructions(self, turn: AgentTurnContext) -> str:
        instructions = self._definition.instructions
        if isinstance(instructions, str):
            return instructions
        return instructions(
            InstructionContext(flow_id=turn.flow_id, turn_id=turn.turn_id, attempt=turn.attempt)
        )

    def _tool_definitions(self, turn: AgentTurnContext) -> list[ModelToolDefinition]:
        built_ins = [
            ModelToolDefinition(
                name=definition.name,
                description=definition.description,
                parameters=definition.parameters,
            )
            for definition in BUILT_IN_TOOL_DEFINITIONS
            if definition.name in turn.enabled_built_in_tools
        ]
        return [*(tool.definition for tool in self._definition.tools), *built_ins]

    def _tool_policy(self, turn: AgentTurnContext, tool_name: str) -> ToolPolicy:
        if tool_name in turn.tool_policies:
            return resolve_tool_policy(tool_name, turn.tool_policies, turn.default_tool_effect)
        tool = self._tools_by_name.get(tool_name)
        # An unknown tool fails fast in CallTool, so the model sees why.
        return _UNKNOWN_TOOL_POLICY if tool is None else tool.policy


def stepwise_agent_session_options(definition: AgentDefinition) -> AgentSessionOptions:
    """Return the session options an ``AgentDefinition`` declares.

    Args:
        definition: The agent.

    Returns:
        Options with the definition's tool policies, default effect, built-in
        tools, interrupt deadline, and Stream capacity.
    """
    return AgentSessionOptions(
        tool_policies=definition.tool_policies,
        default_tool_effect=definition.default_tool_effect,
        event_stream_capacity_bytes=definition.event_stream_capacity_bytes,
        interrupt_deadline=definition.interrupt_deadline,
        built_in_tools=definition.built_in_tools,
    )


def stepwise_agent_session_flow(definition: AgentDefinition, *, flow_type: str) -> AgentSessionFlow:
    """Build the durable session Flow of one Stepwise agent.

    Register the Flow in a Registry created with ``allow_async_handlers=True``
    and serve it with an ``AsyncWorker``. Call it through
    ``AgentSessionClient``, as for any adapter.

    Example:
        >>> flow = stepwise_agent_session_flow(support_agent, flow_type="SupportAgent")
        >>> registry = Registry([flow], allow_async_handlers=True)
        >>> sessions = AgentSessionClient(client, flow)
        >>> await sessions.start_session("session-42", "Where is order 7?")

    Args:
        definition: The agent.
        flow_type: Protocol Flow type, unique within the Registry. Keep it
            stable: running sessions resume by it.

    Returns:
        ``AgentSessionFlow(StepwiseAgentRunner(definition), ...)`` with options
        from ``stepwise_agent_session_options``.

    Raises:
        ValueError: If ``flow_type`` is blank.
    """
    return AgentSessionFlow(
        StepwiseAgentRunner(definition),
        flow_type=flow_type,
        options=stepwise_agent_session_options(definition),
    )


def _built_in_name_collision(tool_name: str) -> str:
    return f"tool {tool_name} uses the name of an enabled built-in tool"


def _model_call_count(framework_run_state: str | None) -> int:
    if framework_run_state is None:
        return 0
    return int(json.loads(framework_run_state)["model_calls"])


def _unstored_tool_results(turn: AgentTurnContext) -> list[ToolResultItem]:
    stored_result_ids = {
        message.tool_call_id
        for message in turn.messages
        if message.turn_id == turn.turn_id and message.role is MessageRole.TOOL
    }
    records = sorted(turn.tool_calls.values(), key=lambda record: record.deferral_order)
    return [
        ToolResultItem(
            tool_call_id=record.tool_call_id,
            tool_name=record.tool_name,
            output=describe_tool_call_outcome(record),
        )
        for record in records
        if record.tool_call_id not in stored_result_ids
    ]
