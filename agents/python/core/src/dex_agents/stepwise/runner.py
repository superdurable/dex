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
from typing import Literal, TypeAlias

from dex_agents.built_in_tools import BUILT_IN_TOOL_DEFINITIONS, BuiltInTool
from dex_agents.persistence import DEFAULT_AGENT_EVENTS_CAPACITY_BYTES
from dex_agents.plans import build_plan_note
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
    ModelInputItem,
    ModelRequest,
    ModelRequestMetadata,
    ModelSettings,
    ModelToolDefinition,
    NamedToolChoice,
    ToolChoice,
    ToolResultItem,
    UserTextItem,
)
from dex_agents.stepwise.transcript import (
    decode_stepwise_message,
    encode_stepwise_message,
    pair_tool_calls_with_results,
)
from dex_agents.values import AgentPlan, InteractionMode, MessageRole, PlanStatus, ToolEffect

#: Default ``AgentDefinition.max_model_calls_per_turn``.
DEFAULT_MAX_MODEL_CALLS_PER_TURN = 100

#: Result of a tool call the model made after the turn's model call limit; ``str.format`` it.
TOOL_CALL_LIMIT_REACHED_RESULT = (
    "The tool call was not run: this turn reached its limit of "
    "{max_model_calls_per_turn} model calls."
)
_UNKNOWN_TOOL_POLICY = ToolPolicy(ToolEffect.READ, requires_approval=False)

#: How the first model call of a planning turn without a draft uses ``write_todos``.
PlanningToolChoice: TypeAlias = Literal["auto", "required"]
_PLANNING_TOOL_CHOICES: tuple[PlanningToolChoice, ...] = ("auto", "required")


@dataclass(frozen=True, kw_only=True)
class InstructionContext:
    """Tell a function of ``AgentDefinition.instructions`` which model call it serves.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        turn_id: ``"turn-<n>"`` of the running turn.
        attempt: One-based ``RunModel`` attempt.
        interaction_mode: The turn's mode. Defaults to ``CHAT``.
        plan: The session's plan before this model call, or ``None``. The
            runner already shows it to the model; use it to adapt the
            instructions. Defaults to ``None``.
    """

    flow_id: str
    turn_id: str
    attempt: int
    interaction_mode: InteractionMode = InteractionMode.CHAT
    plan: AgentPlan | None = None


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
        plan_mode_enabled: Whether the session accepts ``plan_mode`` user
            messages. A planning turn offers only ``write_todos``, and the
            ``execute_plan`` RPC then runs the draft with every tool. Requires
            ``BuiltInTool.WRITE_TODOS`` in ``built_in_tools``. Defaults to ``False``.
        planning_tool_choice: ``"auto"``, the default, lets the model of a
            planning turn call ``write_todos`` or answer in text, such as with
            a clarifying question; the plan note tells it to write the plan
            with ``write_todos``, and a text answer ends the turn without a
            draft. ``"required"`` forces ``write_todos`` on the first model
            call of a planning turn while no draft exists. Some models refuse
            a forced tool choice, such as Claude Opus 5.5 and Claude Sonnet 5.5,
            and so does Anthropic extended thinking, including a
            ``reasoning_effort`` that LiteLLM turns into thinking. A model client that
            reports it with ``supports_forced_tool_choice(settings)``, as
            ``LiteLLMModelClient`` does, makes ``"required"`` fail here.
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
            tool policy uses an enabled built-in name,
            ``max_model_calls_per_turn`` is not positive,
            ``plan_mode_enabled`` is set without ``write_todos``,
            ``planning_tool_choice`` is unknown, or it is ``"required"`` for a
            model client that reports it cannot force a tool choice.
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
    plan_mode_enabled: bool = False
    planning_tool_choice: PlanningToolChoice = "auto"

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
        if self.plan_mode_enabled and BuiltInTool.WRITE_TODOS not in built_in_tools:
            raise ValueError("plan_mode_enabled requires the write_todos built-in tool")
        if self.planning_tool_choice not in _PLANNING_TOOL_CHOICES:
            raise ValueError(
                "planning_tool_choice must be 'auto' or 'required', "
                f"not {self.planning_tool_choice!r}"
            )
        if self.planning_tool_choice == "required" and not _supports_forced_tool_choice(
            self.model, self.model_settings
        ):
            raise ValueError(
                "planning_tool_choice='required' needs a model that accepts a forced tool "
                "choice, and this model client reports that its model does not; use 'auto'"
            )
        object.__setattr__(self, "tools", tuple(self.tools))
        object.__setattr__(self, "tool_policies", dict(self.tool_policies))
        object.__setattr__(self, "built_in_tools", built_in_tools)


class StepwiseAgentRunner:
    """Run an ``AgentDefinition`` as the native Dex Agents loop, with no framework.

    The runner implements ``AgentFrameworkRunner``,
    ``UnfinishedTurnMessageBuilder``, and ``PlanModeRunner``. ``run_model_step``
    makes at most one model call, from committed state only. It stores every item as it appears:
    the assistant item that asks for tools commits with the tool call records,
    and the tool results commit with the next model call. A tool call is never
    stored without a result. Most applications use
    ``stepwise_agent_session_flow`` instead of constructing it.

    Example:
        >>> flow = AgentSessionFlow(StepwiseAgentRunner(definition), flow_type="SupportAgent",
        ...                         options=stepwise_agent_session_options(definition))
    """

    #: The runner honors ``AgentTurnContext.interaction_mode``, so plan mode can be enabled.
    supports_plan_mode = True

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
        if (
            not is_tool_budget_spent
            and self._definition.planning_tool_choice == "required"
            and _must_write_plan_first(turn, model_calls)
        ):
            tool_choice = NamedToolChoice(tool_name=BuiltInTool.WRITE_TODOS)
        request = ModelRequest(
            instructions=self._instructions(turn),
            input_items=pair_tool_calls_with_results(
                [*_stored_input_items_with_plan_note(turn), *unstored_results]
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
            InstructionContext(
                flow_id=turn.flow_id,
                turn_id=turn.turn_id,
                attempt=turn.attempt,
                interaction_mode=turn.interaction_mode,
                plan=turn.plan,
            )
        )

    def _tool_definitions(self, turn: AgentTurnContext) -> list[ModelToolDefinition]:
        is_planning = turn.interaction_mode == InteractionMode.PLANNING
        built_ins = [
            ModelToolDefinition(
                name=definition.name,
                description=definition.description,
                parameters=definition.parameters,
            )
            for definition in BUILT_IN_TOOL_DEFINITIONS
            if definition.name in turn.enabled_built_in_tools
            and (not is_planning or definition.name is BuiltInTool.WRITE_TODOS)
        ]
        if is_planning:
            return built_ins
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
        tools, plan mode, interrupt deadline, and Stream capacity.
    """
    return AgentSessionOptions(
        tool_policies=definition.tool_policies,
        default_tool_effect=definition.default_tool_effect,
        event_stream_capacity_bytes=definition.event_stream_capacity_bytes,
        interrupt_deadline=definition.interrupt_deadline,
        built_in_tools=definition.built_in_tools,
        plan_mode_enabled=definition.plan_mode_enabled,
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


def _must_write_plan_first(turn: AgentTurnContext, model_calls: int) -> bool:
    """A planning turn without a draft drafts one first; feedback on a draft may only answer."""
    return (
        turn.interaction_mode == InteractionMode.PLANNING
        and model_calls == 0
        and BuiltInTool.WRITE_TODOS in turn.enabled_built_in_tools
        and (turn.plan is None or turn.plan.status != PlanStatus.DRAFT)
    )


def _supports_forced_tool_choice(model: ModelClient, settings: ModelSettings) -> bool:
    # An optional client capability; a client without it is trusted to accept forcing.
    check = getattr(model, "supports_forced_tool_choice", None)
    return not callable(check) or check(settings) is not False


def _stored_input_items_with_plan_note(turn: AgentTurnContext) -> list[ModelInputItem]:
    items = [decode_stepwise_message(message) for message in turn.messages]
    plan_note = build_plan_note(turn.plan, turn.interaction_mode)
    if plan_note is None:
        return items
    # Before the turn's messages, so a plan change rewrites only the turn's tail of the input.
    turn_start = next(
        (index for index, message in enumerate(turn.messages) if message.turn_id == turn.turn_id),
        len(turn.messages),
    )
    items.insert(turn_start, UserTextItem(text=plan_note))
    return items


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
