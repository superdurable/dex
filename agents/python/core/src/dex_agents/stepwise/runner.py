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
from dex_agents.compaction import CompactionPolicy, ContextSummaryRequest, ContextSummaryResult
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
    ModelOutputItem,
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
from dex_agents.values import (
    AgentPlan,
    ContextSummary,
    InteractionMode,
    MessageRole,
    PlanStatus,
    ToolEffect,
)

#: Default ``AgentDefinition.max_model_calls_per_turn``.
DEFAULT_MAX_MODEL_CALLS_PER_TURN = 100

#: Result of a tool call the model made after the turn's model call limit; ``str.format`` it.
TOOL_CALL_LIMIT_REACHED_RESULT = (
    "The tool call was not run: this turn reached its limit of "
    "{max_model_calls_per_turn} model calls."
)
#: First line of the user text that carries the ``ContextSummary`` into the model input.
CONTEXT_SUMMARY_NOTE_HEADER = "[Dex] Summary of the earlier conversation:"

#: Instructions of the model call that writes a ``ContextSummary``.
CONTEXT_SUMMARY_INSTRUCTIONS = (
    "You summarize the earlier part of an agent conversation. The agent continues from your "
    "summary and the messages after it, so keep every fact, decision, user request and "
    "preference, open task, and tool result that it still needs, including what an earlier "
    "summary says. Write plain text."
)

#: Last user text of the model call that writes a ``ContextSummary``.
CONTEXT_SUMMARY_REQUEST = "[Dex] Summarize the conversation above. Reply with the summary only."


_UNKNOWN_TOOL_POLICY = ToolPolicy(ToolEffect.READ, requires_approval=False)

#: How the first model call of a planning turn without a draft uses ``write_todos``.
PlanningToolChoice: TypeAlias = Literal["auto", "required"]
_PLANNING_TOOL_CHOICES: tuple[PlanningToolChoice, ...] = ("auto", "required")


def build_context_summary_omission_note(omitted_count: int) -> str:
    """Return the user text saying how many of the oldest messages a summary request left out.

    Args:
        omitted_count: Messages left out, at least 1.

    Returns:
        ``[Dex] <n> earlier messages did not fit in this request and are left
        out.``, or ``[Dex] 1 earlier message did not fit in this request and is
        left out.`` for one message.
    """
    if omitted_count == 1:
        return "[Dex] 1 earlier message did not fit in this request and is left out."
    return f"[Dex] {omitted_count} earlier messages did not fit in this request and are left out."


def build_context_summary_note(summary: ContextSummary) -> str:
    """Return the user text that puts a summary before the newer messages in a model input.

    Args:
        summary: The session's summary.

    Returns:
        ``CONTEXT_SUMMARY_NOTE_HEADER``, a newline, and the summary text.
    """
    return f"{CONTEXT_SUMMARY_NOTE_HEADER}\n{summary.text}"


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
        compaction: When to summarize earlier messages so the model input
            stays under the window, counted by the model client's
            ``estimate_input_tokens``. ``None``, the default, never compacts.

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
    compaction: CompactionPolicy | None = None

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
    ``UnfinishedTurnMessageBuilder``, ``PlanModeRunner``, and
    ``ContextCompactionRunner``. ``run_model_step`` makes at most one model
    call, from committed state only. It stores every item as it appears: the
    assistant item that asks for tools commits with the tool call records, and
    the tool results commit with the next model call. A tool call is never
    stored without a result. With a compaction policy, the model input starts
    with the summary. Most applications use ``stepwise_agent_session_flow``
    instead of constructing it.

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
        request = self._model_request(turn, unstored_results, model_calls, is_tool_budget_spent)
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

    def estimate_input_tokens(self, turn: AgentTurnContext) -> int:
        """Estimate the model input of the next ``run_model_step`` with the model client.

        Args:
            turn: The context of the next model call.

        Returns:
            ``ModelClient.estimate_input_tokens`` of the request that
            ``run_model_step`` would send: instructions, summary, messages,
            unstored tool results, and tools.
        """
        model_calls = _model_call_count(turn.framework_run_state)
        request = self._model_request(
            turn,
            _unstored_tool_results(turn),
            model_calls,
            model_calls >= self._definition.max_model_calls_per_turn,
        )
        return self._definition.model.estimate_input_tokens(request)

    def select_compaction_cutoff(
        self, turn: AgentTurnContext, retained_token_budget: int
    ) -> int | None:
        """Choose the last message to summarize, at a boundary between model responses.

        The runner cuts only before a user message or before the first output
        item of a model response whose earlier tool calls all have stored
        results, so no tool call is separated from its result and no response
        is split. It keeps the newest messages that fit the budget, and never
        summarizes the newest stored message.

        Args:
            turn: The context of the model call that crossed the threshold.
            retained_token_budget: Tokens that the kept messages may use.

        Returns:
            The sequence of the last message to summarize: the earliest cut
            whose kept messages fit the budget, else the latest cut. ``None``
            when every message already fits or no cut is possible.
        """
        messages = list(turn.messages)
        items = [decode_stepwise_message(message) for message in messages]
        cuts = _compaction_cuts(items)
        if not cuts:
            return None
        kept_tokens = [0] * (len(items) + 1)
        kept_tokens[len(items)] = self._estimate_items(turn, _unstored_tool_results(turn))
        for index in range(len(items) - 1, -1, -1):
            kept_tokens[index] = kept_tokens[index + 1] + self._estimate_items(turn, [items[index]])
        if kept_tokens[0] <= retained_token_budget:
            return None
        fitting_cuts = [cut for cut in cuts if kept_tokens[cut] <= retained_token_budget]
        cut = fitting_cuts[0] if fitting_cuts else cuts[-1]
        return messages[cut - 1].sequence

    async def summarize_context(self, request: ContextSummaryRequest) -> ContextSummaryResult:
        """Fold the previous summary and the messages of the range into new text.

        The runner makes one model call with ``CONTEXT_SUMMARY_INSTRUCTIONS``,
        no tools, the previous summary as a note, the range's items, and
        ``CONTEXT_SUMMARY_REQUEST`` as the last user text. Its text streams
        nowhere, because it is not the assistant's answer.

        When the request would exceed ``request.input_token_budget``, such as
        after a smaller context window or when compaction starts on a long
        session, the runner leaves out the oldest items of the range and says
        so with ``build_context_summary_omission_note``, so the call and its
        ``max_output_tokens``, when set, still fit.
        It leaves them out up to a boundary between model responses, as
        ``select_compaction_cutoff`` cuts, so it never splits a response or
        separates a tool call from its result.

        Args:
            request: The previous summary and the messages to summarize.

        Returns:
            The response text and the usage of the call.

        Raises:
            ValueError: If the model returned no text.
            Exception: What the model client raises.
        """
        range_items = [decode_stepwise_message(message) for message in request.messages]
        omitted_count = 0
        budget = request.input_token_budget
        if budget is not None:
            # The window also holds the summary the call writes.
            budget -= self._definition.model_settings.max_output_tokens or 0
            # Estimate by parts, then leave out the oldest items up to the first cut that fits.
            item_tokens = [self._estimate_summary_item(request, item) for item in range_items]
            tokens = self._definition.model.estimate_input_tokens(
                self._summary_request(request, [], omitted_count=len(range_items))
            ) + sum(item_tokens)
            # A cut never splits a model response or separates a tool call from its result.
            for cut in [*_compaction_cuts(range_items), len(range_items)]:
                if tokens <= budget:
                    break
                tokens -= sum(item_tokens[omitted_count:cut])
                omitted_count = cut
        model_request = self._summary_request(
            request, range_items[omitted_count:], omitted_count=omitted_count
        )
        response = await self._definition.model.call_model(model_request, _DISCARDED_MODEL_OUTPUT)
        if not response.text.strip():
            raise ValueError("the summary model call returned no text")
        return ContextSummaryResult(
            text=response.text.strip(), usage=replace(response.usage, model_calls=1)
        )

    def _summary_request(
        self,
        request: ContextSummaryRequest,
        range_items: Sequence[ModelInputItem],
        *,
        omitted_count: int,
    ) -> ModelRequest:
        items: list[ModelInputItem] = []
        if request.previous_summary is not None:
            items.append(UserTextItem(text=build_context_summary_note(request.previous_summary)))
        if omitted_count:
            items.append(UserTextItem(text=build_context_summary_omission_note(omitted_count)))
        items.extend(range_items)
        items.append(UserTextItem(text=CONTEXT_SUMMARY_REQUEST))
        return ModelRequest(
            instructions=CONTEXT_SUMMARY_INSTRUCTIONS,
            input_items=pair_tool_calls_with_results(items),
            tools=[],
            tool_choice="none",
            settings=self._definition.model_settings,
            metadata=ModelRequestMetadata(
                flow_id=request.flow_id,
                turn_id=request.turn_id,
                step_execution_id=request.events.step_execution_id,
                attempt=request.attempt,
            ),
        )

    def _estimate_summary_item(self, request: ContextSummaryRequest, item: ModelInputItem) -> int:
        return self._definition.model.estimate_input_tokens(
            replace(
                self._summary_request(request, [], omitted_count=0),
                instructions="",
                input_items=[item],
            )
        )

    def _model_request(
        self,
        turn: AgentTurnContext,
        unstored_results: Sequence[ToolResultItem],
        model_calls: int,
        is_tool_budget_spent: bool,
    ) -> ModelRequest:
        summary_items: list[ModelInputItem] = []
        if turn.context_summary is not None:
            summary_items.append(
                UserTextItem(text=build_context_summary_note(turn.context_summary))
            )
        tool_choice: ToolChoice = "none" if is_tool_budget_spent else "auto"
        if (
            not is_tool_budget_spent
            and self._definition.planning_tool_choice == "required"
            and _must_write_plan_first(turn, model_calls)
        ):
            tool_choice = NamedToolChoice(tool_name=BuiltInTool.WRITE_TODOS)
        return ModelRequest(
            instructions=self._instructions(turn),
            # The plan note comes after the summary, so compaction never hides the plan.
            input_items=pair_tool_calls_with_results(
                [*summary_items, *_stored_input_items_with_plan_note(turn), *unstored_results]
            ),
            tools=[] if is_tool_budget_spent else self._tool_definitions(turn),
            tool_choice=tool_choice,
            settings=self._definition.model_settings,
            metadata=ModelRequestMetadata(
                flow_id=turn.flow_id,
                turn_id=turn.turn_id,
                step_execution_id=turn.events.step_execution_id,
                attempt=turn.attempt,
            ),
        )

    def _estimate_items(self, turn: AgentTurnContext, items: Sequence[ModelInputItem]) -> int:
        if not items:
            return 0
        return self._definition.model.estimate_input_tokens(
            ModelRequest(
                instructions="",
                input_items=items,
                tools=[],
                tool_choice="none",
                settings=self._definition.model_settings,
                metadata=ModelRequestMetadata(
                    flow_id=turn.flow_id,
                    turn_id=turn.turn_id,
                    step_execution_id=turn.events.step_execution_id,
                    attempt=turn.attempt,
                ),
            )
        )

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
        tools, plan mode, interrupt deadline, Stream capacity, and compaction policy.
    """
    return AgentSessionOptions(
        tool_policies=definition.tool_policies,
        default_tool_effect=definition.default_tool_effect,
        event_stream_capacity_bytes=definition.event_stream_capacity_bytes,
        interrupt_deadline=definition.interrupt_deadline,
        built_in_tools=definition.built_in_tools,
        plan_mode_enabled=definition.plan_mode_enabled,
        compaction=definition.compaction,
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
    turn_messages = [message for message in turn.messages if message.turn_id == turn.turn_id]
    stored_result_ids = {
        message.tool_call_id for message in turn_messages if message.role is MessageRole.TOOL
    }
    records = sorted(turn.tool_calls.values(), key=lambda record: record.deferral_order)
    if turn.context_summary is not None:
        # A call summarized away was summarized with its result, so only shown calls can lack one.
        shown_call_ids = {
            call.tool_call_id
            for item in map(decode_stepwise_message, turn_messages)
            if isinstance(item, ModelOutputItem)
            for call in item.tool_calls
        }
        records = [record for record in records if record.tool_call_id in shown_call_ids]
    return [
        ToolResultItem(
            tool_call_id=record.tool_call_id,
            tool_name=record.tool_name,
            output=describe_tool_call_outcome(record),
        )
        for record in records
        if record.tool_call_id not in stored_result_ids
    ]


def _compaction_cuts(items: Sequence[ModelInputItem]) -> list[int]:
    """Return each index that may start the kept items, ascending; never the last item's index."""
    cuts: list[int] = []
    open_call_ids: set[str] = set()
    for index, item in enumerate(items[:-1]):
        if isinstance(item, ModelOutputItem):
            open_call_ids.update(call.tool_call_id for call in item.tool_calls)
        elif isinstance(item, ToolResultItem):
            open_call_ids.discard(item.tool_call_id)
        else:
            # User text closes calls left open, as pair_tool_calls_with_results does.
            open_call_ids.clear()
        following = items[index + 1]
        if open_call_ids or isinstance(following, ToolResultItem):
            continue
        # Consecutive output items are one model response, which must stay whole.
        if isinstance(item, ModelOutputItem) and isinstance(following, ModelOutputItem):
            continue
        cuts.append(index + 1)
    return cuts


class _DiscardedModelOutput:
    """Drop the live text of a summary call, which is not the assistant's answer."""

    def write_text_delta(self, text: str) -> None:
        return None

    def write_reasoning_delta(self, text: str) -> None:
        return None


_DISCARDED_MODEL_OUTPUT = _DiscardedModelOutput()
