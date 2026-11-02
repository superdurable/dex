# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The shared agent session Flow that every framework adapter registers.

The Flow runs in Step mode. Each model call is one ``RunModel`` Step. Each
tool call is its own ``CallTool`` or ``CallSingleAttemptTool`` Step, after an
``AwaitToolApproval`` Step when its policy requires approval. The tool call
Steps of one model response run in parallel, and ``ResumeTurn`` waits for all
of their results before the next ``RunModel``. A call to an enabled built-in
tool runs in the built-in's own Step instead: ``AwaitQuestionAnswer`` for
``ask_user`` and ``AwaitTimer`` for ``durable_wait``.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import logging
import math
from collections.abc import AsyncIterator, Awaitable, Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass, field, replace
from datetime import UTC, datetime, timedelta
from typing import Any, cast

from dex import (
    AsyncContext,
    AttributeMap,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    RPCResult,
    Step,
    StepDecision,
    StepDurability,
    StepList,
    StepMovement,
    StepOptions,
    Stream,
    Timer,
    Wait,
    dead_end,
    go_to,
    go_to_many,
    rpc,
)
from dex.condition import Condition

from dex_agents._session_tracing import AgentSessionTracer
from dex_agents.built_in_tools import (
    DEFAULT_MAX_DURABLE_WAIT,
    QUESTION_DEADLINE_PASSED,
    QUESTION_DECLINED_TOOL_RESULT,
    BuiltInTool,
    BuiltInToolArgumentsError,
    build_durable_wait_completed_output,
    build_question_answered_output,
    parse_ask_user_arguments,
    parse_durable_wait_arguments,
)
from dex_agents.events import AgentEventType, AgentEventWriter
from dex_agents.persistence import (
    AGENT_MESSAGES,
    AGENT_STATE,
    AGENT_TOOL_CALLS,
    AGENT_TRACE_CONTEXT,
    AGENT_USAGE,
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    FRAMEWORK_RUN_STATE,
    INTERRUPT_RESOLUTIONS,
    PENDING_INTERRUPTS,
    QUEUED_USER_MESSAGES,
    TOOL_CALL_RESULTS,
    message_sequence_key,
)
from dex_agents.runner import (
    APPROVAL_DEADLINE_PASSED,
    AgentFrameworkRunner,
    AgentTurnContext,
    DeferredToolExecution,
    DeferredToolOutcome,
    FrameworkMessage,
    ToolCallsRequested,
    ToolExecutionError,
    ToolInputRequested,
    ToolPolicy,
    TurnCompleted,
    UnfinishedTurnMessageBuilder,
    build_turn_failure_note,
)
from dex_agents.tracing import AgentTracingOptions
from dex_agents.values import (
    AgentEvent,
    AgentInterrupt,
    AgentInterruptResolution,
    AgentMessage,
    AgentMessagesPage,
    AgentSessionStartInput,
    AgentSessionStatus,
    AgentSnapshot,
    AgentState,
    AgentToolCall,
    AgentUsage,
    AgentUserMessage,
    AnswerQuestionRequest,
    InterruptKind,
    ListAgentMessagesRequest,
    MessageRole,
    QuestionAnswer,
    ResolveInterruptRequest,
    ToolCallCompleted,
    ToolCallStatus,
    ToolEffect,
)

_LOGGER = logging.getLogger(__name__)
MISSING_UNFINISHED_TURN_MESSAGE_BUILDER = (
    "a runner that sets ToolCallsRequested.new_messages must implement "
    "build_unfinished_turn_messages"
)
SNAPSHOT_MESSAGE_LIMIT = 200
MESSAGE_PAGE_LIMIT = 200
# Keeps liveness well inside the shortest heartbeat timeout, one minute.
BACKGROUND_HEARTBEAT_INTERVAL = timedelta(seconds=20)
RECOVERY_RETRY = RetryPolicy(maximum_interval=timedelta(seconds=5))
MULTI_ATTEMPT_TOOL_EFFECTS = (ToolEffect.READ, ToolEffect.IDEMPOTENT_WRITE)
BUILT_IN_TOOL_POLICY = ToolPolicy(ToolEffect.READ, requires_approval=False)
_NO_PENDING_TOOL_QUESTION: dict[str, Any] = {
    "input_question": None,
    "input_choices": [],
    "input_allows_free_text": True,
}


@dataclass(frozen=True, kw_only=True)
class AgentSessionOptions:
    """Configure tool policies, built-in tools, live events, deadlines, and tracing for one agent.

    Pass it to ``AgentSessionFlow``. Options are read when Steps run, so a
    change applies to running sessions after the Worker restarts. Enable a
    built-in tool only after every Worker of the Flow type runs a version
    that supports it.

    Example:
        >>> options = AgentSessionOptions(
        ...     tool_policies={"search": ToolPolicy(ToolEffect.READ)},
        ...     default_tool_effect=ToolEffect.UNCLASSIFIED,
        ...     interrupt_deadline=timedelta(hours=24),
        ...     tracing=AgentTracingOptions(agent_name="Triage"),
        ...     built_in_tools=frozenset({BuiltInTool.ASK_USER, BuiltInTool.DURABLE_WAIT}),
        ... )

    Attributes:
        tool_policies: Policies by tool name. They override framework metadata
            and ``default_tool_effect``. Defaults to none.
        default_tool_effect: Effect for a tool without a policy or framework
            metadata. Defaults to ``UNCLASSIFIED``, so unknown tools run once and
            need approval.
        event_stream_capacity_bytes: Approximate byte budget of the
            ``AgentEvents`` Stream, shared by all sessions of the Flow type.
            Defaults to 10 MiB.
        interrupt_deadline: How long an approval or a question may stay
            pending before the session rejects it with ``"approval deadline
            passed"`` or ``"question deadline passed"``. ``None``, the default,
            waits indefinitely.
        built_in_tools: Built-in tools the session runs itself. The runner
            offers their definitions to the model, and the session routes
            their calls to ``AwaitQuestionAnswer`` and ``AwaitTimer`` with no
            approval and the ``read`` effect. An enabled name is reserved for
            the built-in. Defaults to none.
        max_durable_wait: Longest ``durable_wait`` the session accepts, in
            whole seconds. A longer request fails at once with a message the
            model sees. Defaults to 365 days.
        tracing: OpenTelemetry GenAI span options. The default emits spans
            through the global tracer provider when the OpenTelemetry API is
            installed, and nothing otherwise.

    Raises:
        ValueError: If ``event_stream_capacity_bytes`` or ``interrupt_deadline``
            is not positive, if ``max_durable_wait`` is shorter than one second
            or not whole seconds, if ``built_in_tools`` names an unknown tool,
            or if ``tool_policies`` names an enabled built-in tool.
    """

    tool_policies: Mapping[str, ToolPolicy] = field(default_factory=dict)
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES
    interrupt_deadline: timedelta | None = None
    tracing: AgentTracingOptions = field(default_factory=AgentTracingOptions)
    built_in_tools: frozenset[BuiltInTool] = frozenset()
    max_durable_wait: timedelta = DEFAULT_MAX_DURABLE_WAIT

    def __post_init__(self) -> None:
        if self.event_stream_capacity_bytes <= 0:
            raise ValueError("event_stream_capacity_bytes must be positive")
        if self.interrupt_deadline is not None and self.interrupt_deadline <= timedelta(0):
            raise ValueError("interrupt_deadline must be positive")
        if self.max_durable_wait < timedelta(seconds=1) or self.max_durable_wait.microseconds:
            raise ValueError("max_durable_wait must be a whole number of seconds, at least one")
        built_in_tools = frozenset(BuiltInTool(tool) for tool in self.built_in_tools)
        reserved = sorted(built_in_tools.intersection(self.tool_policies))
        if reserved:
            raise ValueError(
                f"tool_policies names {reserved[0]!r}, which is an enabled built-in tool"
            )
        object.__setattr__(self, "tool_policies", dict(self.tool_policies))
        object.__setattr__(self, "built_in_tools", built_in_tools)


class AgentSessionFlow(Flow[AgentSessionStartInput]):
    """Run one durable agent session per Flow execution with a framework runner.

    One Flow execution is one agent session, and the Flow ID is the session
    ID. Register one ``AgentSessionFlow`` per agent, each with its own Flow
    type, in a Registry created with ``allow_async_handlers=True`` and served
    by an ``AsyncWorker``. Framework adapters usually construct it for you.

    Every model call runs in its own ``RunModel`` Step and every tool call in
    its own tool call Step, so each has its own retry policy, timeout, failure
    route, and history entry. The session never completes on its own: it waits
    for the next user message after every turn. Stop it with
    ``AsyncClient.stop_flow``.

    Example:
        >>> flow = AgentSessionFlow(MyFrameworkRunner(agent), flow_type="TriageAgent")
        >>> registry = Registry([flow], allow_async_handlers=True)
        >>> cache = open_blob_cache(BlobCacheConfig(".dex-cache", 64 * 1024**2))
        >>> worker = AsyncWorker(registry, cache, WorkerOptions(server_address="localhost:8801"))
        >>> client = AsyncClient(registry, cache, ClientOptions(server_address="localhost:8801"))
        >>> await client.start_flow(flow, "session-42", AgentSessionStartInput())
        >>> await client.invoke_rpc(flow.send_user_message, "session-42",
        ...                         AgentUserMessage(text="Hello"))

    Attributes:
        agent_state: The ``AgentState`` Attribute.
        agent_messages: The ``AgentMessages`` AttributeMap.
        framework_run_state: The ``FrameworkRunState`` Attribute.
        agent_tool_calls: The ``AgentToolCalls`` AttributeMap.
        pending_interrupts: The ``PendingInterrupts`` Attribute.
        agent_usage: The ``AgentUsage`` Attribute.
        queued_user_messages: The ``QueuedUserMessages`` Channel.
        interrupt_resolutions: The ``InterruptResolutions`` ChannelMap.
        tool_call_results: The internal ``ToolCallResults`` Channel.
        agent_trace_context: The ``AgentTraceContext`` Attribute: the W3C
            ``traceparent`` of the current activation's root span, set when
            that span has a valid context, sampled or not.
        agent_events: This Flow type's ``AgentEvents`` Stream. Pass it to
            ``AsyncClient.read_stream`` or ``AsyncClient.list_stream_messages``.
    """

    agent_state = AGENT_STATE
    agent_messages = AGENT_MESSAGES
    framework_run_state = FRAMEWORK_RUN_STATE
    agent_tool_calls = AGENT_TOOL_CALLS
    pending_interrupts = PENDING_INTERRUPTS
    agent_usage = AGENT_USAGE
    queued_user_messages = QUEUED_USER_MESSAGES
    interrupt_resolutions = INTERRUPT_RESOLUTIONS
    tool_call_results = TOOL_CALL_RESULTS
    agent_trace_context = AGENT_TRACE_CONTEXT

    def __init__(
        self,
        runner: AgentFrameworkRunner[DeferredToolOutcome],
        *,
        flow_type: str,
        options: AgentSessionOptions | None = None,
    ) -> None:
        """Create the session Flow for one agent.

        Construct it once per agent at application startup and pass the same
        instance to the Registry, the Worker, and every Client call.

        Args:
            runner: The framework runner. The Flow owns no other dependency.
            flow_type: Protocol Flow type, unique within the Registry, such as
                ``"TriageAgent"``. Keep it stable: running sessions resume by it.
            options: Tool policies, Stream capacity, approval deadline, and
                tracing. ``None`` uses ``AgentSessionOptions()``.

        Raises:
            ValueError: If ``flow_type`` is empty or whitespace-only.
        """
        if not flow_type.strip():
            raise ValueError("flow_type must not be empty")
        self._runner = runner
        self._flow_type = flow_type
        self._options = options or AgentSessionOptions()
        self._session_tracer = AgentSessionTracer(self._options.tracing, flow_type)
        self.agent_events: Stream[AgentEvent] = Stream(
            "AgentEvents", AgentEvent, self._options.event_stream_capacity_bytes
        )
        self._start_agent_session = StartAgentSession(self)
        self._await_user_message = AwaitUserMessage(self)
        self._run_model = RunModel(self)
        self._await_tool_approval = AwaitToolApproval(self)
        self._await_question_answer = AwaitQuestionAnswer(self)
        self._await_timer = AwaitTimer(self)
        self._call_tool = CallTool(self)
        self._call_single_attempt_tool = CallSingleAttemptTool(self)
        self._record_tool_failure = RecordToolFailure(self)
        self._record_tool_outcome_unknown = RecordToolOutcomeUnknown(self)
        self._resume_turn = ResumeTurn(self)
        self._record_turn_failure = RecordTurnFailure(self)

    @property
    def runner(self) -> AgentFrameworkRunner[DeferredToolOutcome]:
        """Return the framework runner this Flow calls.

        Returns:
            The runner passed to the constructor.
        """
        return self._runner

    @property
    def options(self) -> AgentSessionOptions:
        """Return the options this Flow applies.

        Returns:
            The options passed to the constructor, or the defaults.
        """
        return self._options

    def get_flow_type(self) -> str:
        """Return the Flow type passed to the constructor.

        Returns:
            The agent's protocol Flow type.
        """
        return self._flow_type

    def get_steps(self) -> StepList[AgentSessionStartInput]:
        """Return the session Steps.

        Returns:
            ``StartAgentSession`` as the start Step and every other session Step.
        """
        return StepList.start_step(self._start_agent_session).other_steps(
            self._await_user_message,
            self._run_model,
            self._await_tool_approval,
            self._await_question_answer,
            self._await_timer,
            self._call_tool,
            self._call_single_attempt_tool,
            self._record_tool_failure,
            self._record_tool_outcome_unknown,
            self._resume_turn,
            self._record_turn_failure,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        """Return the session Attributes, Channels, and ``AgentEvents`` Stream.

        ``AgentTraceContext`` is always registered, so Workers with and without
        tracing can serve the same Flow type.

        Returns:
            The persistence schema shared by every agent session Flow type.
        """
        return PersistenceSchema.of(
            AGENT_STATE,
            AGENT_MESSAGES,
            FRAMEWORK_RUN_STATE,
            AGENT_TOOL_CALLS,
            PENDING_INTERRUPTS,
            AGENT_USAGE,
            QUEUED_USER_MESSAGES,
            INTERRUPT_RESOLUTIONS,
            TOOL_CALL_RESULTS,
            AGENT_TRACE_CONTEXT,
            self.agent_events,
        )

    @rpc(is_transactional=True)
    def send_user_message(self, context: Context, input: AgentUserMessage) -> RPCResult[bool]:
        """Queue one user message for the session.

        The session starts a turn for each queued message, in order, after the
        current turn ends. The RPC requires a running session.

        Args:
            context: RPC Context supplied by Dex.
            input: The user message.

        Returns:
            ``True`` when the message was queued; ``False`` for whitespace-only text.
        """
        if not input.text.strip():
            return RPCResult(False)
        QUEUED_USER_MESSAGES.publish(context, input)
        return RPCResult(True)

    @rpc(lock_attributes=(PENDING_INTERRUPTS.lock(),))
    def resolve_interrupt(
        self,
        context: Context,
        input: ResolveInterruptRequest,
    ) -> RPCResult[bool]:
        """Answer one pending interrupt, approving or rejecting its tool call.

        The RPC removes the interrupt from ``PendingInterrupts`` and publishes
        the answer in the same commit, so only one call can succeed for each
        interrupt. It locks ``PendingInterrupts``: a concurrent session Step can
        make it raise ``RpcLockConflictError``, which is safe to retry.

        For a ``question`` interrupt, ``approved=False`` declines the question:
        the model sees ``rejection_message``, or ``"The user declined to
        answer."``. A question needs an answer, so ``approved=True`` on a
        question returns ``False``; call ``answer_question`` instead.

        Args:
            context: RPC Context supplied by Dex.
            input: Interrupt ID, decision, and optional rejection message.

        Returns:
            ``True`` when the interrupt was pending and is now resolved;
            ``False`` when it is unknown, already resolved, or past its
            deadline, which rejects the call, or when it approves a question.
        """
        pending = _pending_interrupts(context)
        resolved = next(
            (interrupt for interrupt in pending if interrupt.interrupt_id == input.interrupt_id),
            None,
        )
        if resolved is None or _is_deadline_passed(resolved):
            return RPCResult(False)
        if resolved.kind is InterruptKind.QUESTION and input.approved:
            return RPCResult(False)
        PENDING_INTERRUPTS.set(
            context, [interrupt for interrupt in pending if interrupt is not resolved]
        )
        INTERRUPT_RESOLUTIONS.publish(
            context,
            resolved.interrupt_id,
            AgentInterruptResolution(
                approved=input.approved,
                rejection_message=None if input.approved else input.rejection_message,
            ),
        )
        return RPCResult(True)

    @rpc(lock_attributes=(PENDING_INTERRUPTS.lock(),))
    def answer_question(
        self,
        context: Context,
        input: AnswerQuestionRequest,
    ) -> RPCResult[bool]:
        """Answer one pending question, so its turn continues with the answer.

        The question comes from an ``ask_user`` call or from a tool that
        returned ``ToolInputRequested``. For ``ask_user``, the model sees
        ``{"status":"answered","answer":"<answer>"}`` as the tool result. For a
        tool, the tool runs again with the answer. The RPC removes the
        interrupt from ``PendingInterrupts`` and publishes the answer in the
        same commit, so only one answer can succeed for each question. It
        locks ``PendingInterrupts``: a concurrent session Step can make it
        raise ``RpcLockConflictError``, which is safe to retry. Decline a
        question with ``resolve_interrupt`` and ``approved=False``.

        Args:
            context: RPC Context supplied by Dex.
            input: Interrupt ID and answer text.

        Returns:
            ``True`` when the question was pending and is now answered;
            ``False`` when the interrupt is unknown, already resolved, past its
            deadline, or not a question, when the answer is blank, or when the
            answer is not one of ``choices`` and free text is not allowed.
        """
        pending = _pending_interrupts(context)
        answered = next(
            (interrupt for interrupt in pending if interrupt.interrupt_id == input.interrupt_id),
            None,
        )
        if (
            answered is None
            or answered.kind is not InterruptKind.QUESTION
            or _is_deadline_passed(answered)
            or not input.answer.strip()
        ):
            return RPCResult(False)
        if not answered.allows_free_text and input.answer not in answered.choices:
            return RPCResult(False)
        PENDING_INTERRUPTS.set(
            context, [interrupt for interrupt in pending if interrupt is not answered]
        )
        INTERRUPT_RESOLUTIONS.publish(
            context,
            answered.interrupt_id,
            AgentInterruptResolution(approved=True, answer=input.answer),
        )
        return RPCResult(True)

    @rpc(load_attribute_maps=(AGENT_MESSAGES, AGENT_TOOL_CALLS))
    def get_agent_snapshot(self, context: Context) -> RPCResult[AgentSnapshot]:
        """Return the session state a UI renders.

        The RPC reads committed state without locks, so it also serves a
        retained closed session. It loads the whole ``AgentMessages`` map.

        Args:
            context: RPC Context supplied by Dex.

        Returns:
            State, the last 200 messages, pending interrupts, the active turn's
            tool calls, usage, and the queued message count.
        """
        state = _optional_agent_state(context)
        if state is None:
            return RPCResult(AgentSnapshot(state=None))
        end = state.next_message_sequence
        return RPCResult(
            AgentSnapshot(
                state=state,
                messages=_read_messages(context, max(1, end - SNAPSHOT_MESSAGE_LIMIT), end),
                pending_interrupts=_pending_interrupts(context),
                tool_calls=_turn_tool_calls(context, state.active_turn_id),
                usage=_agent_usage(context),
                queued_message_count=QUEUED_USER_MESSAGES.size(context),
            )
        )

    @rpc(load_attribute_maps=(AGENT_MESSAGES,))
    def list_agent_messages(
        self,
        context: Context,
        input: ListAgentMessagesRequest,
    ) -> RPCResult[AgentMessagesPage]:
        """Return one page of stored messages, oldest first.

        Start without ``before_sequence`` for the newest page, then pass each
        page's ``next_before_sequence`` to walk back. The RPC reads committed
        state without locks, so it also serves a retained closed session.

        Args:
            context: RPC Context supplied by Dex.
            input: Page anchor and size; the size is clamped to 1 through 200.

        Returns:
            The page and the anchor of the previous page.
        """
        state = _optional_agent_state(context)
        if state is None:
            return RPCResult(AgentMessagesPage(messages=[], next_before_sequence=None))
        limit = max(1, min(input.limit, MESSAGE_PAGE_LIMIT))
        end = state.next_message_sequence
        if input.before_sequence is not None:
            end = max(1, min(input.before_sequence, end))
        start = max(1, end - limit)
        return RPCResult(
            AgentMessagesPage(
                messages=_read_messages(context, start, end),
                next_before_sequence=start if start > 1 else None,
            )
        )

    def _begin_turn(self, context: AsyncContext, text: str, step_type: str) -> StepDecision:
        state = _require_agent_state(context)
        turn_id = f"turn-{state.turn_count + 1}"
        sequence = state.next_message_sequence
        with self._session_tracer.trace_user_message_activation(
            context, step_type, turn_id, message_sequence_key(sequence), text
        ):
            user_message = self._runner.build_user_message(text)
            if user_message.role is not MessageRole.USER:
                raise ValueError("build_user_message must return a user message")
            state = _append_messages(context, state, turn_id, [user_message])
            AGENT_STATE.set(
                context,
                replace(state, status=AgentSessionStatus.RUNNING_MODEL, active_turn_id=turn_id),
            )
            self._event_writer(context, turn_id).write_event(
                AgentEventType.TURN_STARTED, {"sequence": sequence}
            )
        return go_to(self._run_model, None)

    async def _run_model_step(self, context: AsyncContext) -> StepDecision:
        state = _require_agent_state(context)
        turn_id = _require_active_turn_id(state)
        tool_calls = _turn_tool_calls(context, turn_id)
        if any(not tool_call.status.is_final for tool_call in tool_calls):
            raise RuntimeError(f"turn {turn_id} resumed before every tool call finished")
        events = self._event_writer(context, turn_id)
        turn = self._turn_context(context, state, tool_calls, events)
        with self._session_tracer.trace_model_call(
            context, self._run_model.get_step_type(), turn
        ) as model_call_span:
            async with _background_heartbeats(context.heartbeat):
                outcome = await self._runner.run_model_step(turn)
            stored_turn_message_count = sum(
                1 for message in turn.messages if message.turn_id == turn_id
            )
            if isinstance(outcome, TurnCompleted):
                model_call_span.record_outcome(
                    outcome,
                    [
                        message_sequence_key(state.next_message_sequence + offset)
                        for offset in range(len(outcome.new_messages))
                    ],
                )
                return self._complete_turn(
                    context, state, tool_calls, outcome, events, stored_turn_message_count
                )
            if isinstance(outcome, ToolCallsRequested):
                model_call_span.record_outcome(outcome, [])
                return self._start_tool_calls(context, state, tool_calls, outcome, events)
            raise TypeError("run_model_step must return TurnCompleted or ToolCallsRequested")

    def _turn_context(
        self,
        context: AsyncContext,
        state: AgentState,
        tool_calls: Sequence[AgentToolCall],
        events: AgentEventWriter,
    ) -> AgentTurnContext:
        return AgentTurnContext(
            flow_id=context.flow_id,
            turn_id=_require_active_turn_id(state),
            attempt=context.attempt,
            messages=_read_messages(context, 1, state.next_message_sequence),
            framework_run_state=FRAMEWORK_RUN_STATE.get(context),
            tool_calls={tool_call.tool_call_id: tool_call for tool_call in tool_calls},
            tool_policies=self._options.tool_policies,
            default_tool_effect=self._options.default_tool_effect,
            events=events,
            heartbeat=context.heartbeat,
            enabled_built_in_tools=self._options.built_in_tools,
        )

    def _complete_turn(
        self,
        context: AsyncContext,
        state: AgentState,
        tool_calls: Sequence[AgentToolCall],
        outcome: TurnCompleted,
        events: AgentEventWriter,
        stored_turn_message_count: int,
    ) -> StepDecision:
        turn_id = _require_active_turn_id(state)
        state = _append_messages(context, state, turn_id, outcome.new_messages)
        for tool_call in tool_calls:
            AGENT_TOOL_CALLS.delete(context, tool_call.tool_call_id)
        if FRAMEWORK_RUN_STATE.get(context) is not None:
            FRAMEWORK_RUN_STATE.delete(context)
        _record_usage(context, events, outcome.usage)
        AGENT_STATE.set(
            context,
            replace(
                state,
                status=AgentSessionStatus.WAITING_FOR_USER_MESSAGE,
                turn_count=state.turn_count + 1,
                active_turn_id=None,
                last_turn_error=None,
            ),
        )
        # The turn's user message is stored before RunModel; count only what came after it.
        appended_message_count = max(stored_turn_message_count - 1, 0) + len(outcome.new_messages)
        events.write_event(AgentEventType.TURN_COMPLETED, {"message_count": appended_message_count})
        return go_to(self._await_user_message, None)

    def _start_tool_calls(
        self,
        context: AsyncContext,
        state: AgentState,
        tool_calls: Sequence[AgentToolCall],
        outcome: ToolCallsRequested,
        events: AgentEventWriter,
    ) -> StepDecision:
        turn_id = _require_active_turn_id(state)
        _validate_tool_calls_requested(outcome, tool_calls)
        if outcome.new_messages and not isinstance(self._runner, UnfinishedTurnMessageBuilder):
            # A runner defect: a retry would only repeat the paid model call.
            _record_usage(context, events, outcome.usage)
            return self._fail_turn(
                context, state, tool_calls, events, MISSING_UNFINISHED_TURN_MESSAGE_BUILDER
            )
        state = _append_messages(context, state, turn_id, outcome.new_messages)
        FRAMEWORK_RUN_STATE.set(context, outcome.framework_run_state)
        now = datetime.now(UTC)
        interrupts = _pending_interrupts(context)
        trace_context = self._session_tracer.read_activation_traceparent(context)
        movements: list[StepMovement[str]] = []
        for index, requested in enumerate(outcome.tool_calls):
            built_in_tool = self._enabled_built_in_tool(requested.tool_name)
            policy = BUILT_IN_TOOL_POLICY if built_in_tool is not None else requested.policy
            requires_approval = policy.is_approval_required
            tool_call = AgentToolCall(
                tool_call_id=requested.tool_call_id,
                turn_id=turn_id,
                tool_name=requested.tool_name,
                arguments=requested.arguments,
                effect=policy.effect,
                requires_approval=requires_approval,
                status=(
                    ToolCallStatus.AWAITING_APPROVAL
                    if requires_approval
                    else ToolCallStatus.APPROVED
                ),
                deferral_order=len(tool_calls) + index,
                tool_source_reference=requested.tool_source_reference,
                trace_context=trace_context,
            )
            events.write_event(
                AgentEventType.TOOL_CALL_DEFERRED,
                {
                    "tool_call_id": tool_call.tool_call_id,
                    "tool_name": tool_call.tool_name,
                    "effect": str(tool_call.effect),
                    "requires_approval": requires_approval,
                },
            )
            if built_in_tool is not None:
                movement = self._start_built_in_tool_call(
                    context, built_in_tool, tool_call, interrupts, now, events
                )
                if movement is not None:
                    movements.append(movement)
                continue
            AGENT_TOOL_CALLS.set(context, tool_call.tool_call_id, tool_call)
            if not requires_approval:
                movements.append(self._tool_call_movement(tool_call))
                continue
            interrupt = AgentInterrupt(
                interrupt_id=tool_call.tool_call_id,
                kind=InterruptKind.TOOL_APPROVAL,
                tool_call_id=tool_call.tool_call_id,
                tool_name=tool_call.tool_name,
                arguments=tool_call.arguments,
                created_at=_rfc3339(now),
                deadline=self._interrupt_deadline(now),
            )
            interrupts.append(interrupt)
            _write_interrupt_raised(events, interrupt)
            movements.append(self._tool_approval_movement(tool_call.tool_call_id))
        PENDING_INTERRUPTS.set(context, interrupts)
        _record_usage(context, events, outcome.usage)
        AGENT_STATE.set(context, replace(state, status=AgentSessionStatus.RUNNING_TOOLS))
        return go_to_many(
            *movements,
            StepMovement.of(self._resume_turn, len(outcome.tool_calls)),
        )

    def _enabled_built_in_tool(self, tool_name: str) -> BuiltInTool | None:
        if tool_name not in self._options.built_in_tools:
            return None
        return BuiltInTool(tool_name)

    def _start_built_in_tool_call(
        self,
        context: AsyncContext,
        built_in_tool: BuiltInTool,
        tool_call: AgentToolCall,
        interrupts: list[AgentInterrupt],
        now: datetime,
        events: AgentEventWriter,
    ) -> StepMovement[str] | None:
        """Record one built-in call in RunModel's commit; return its Step, or None when final."""
        try:
            if built_in_tool is BuiltInTool.ASK_USER:
                question = parse_ask_user_arguments(tool_call.arguments)
                interrupt = AgentInterrupt(
                    interrupt_id=tool_call.tool_call_id,
                    kind=InterruptKind.QUESTION,
                    tool_call_id=tool_call.tool_call_id,
                    tool_name=tool_call.tool_name,
                    arguments=tool_call.arguments,
                    created_at=_rfc3339(now),
                    deadline=self._interrupt_deadline(now),
                    question=question.question,
                    choices=question.choices,
                    allows_free_text=question.allows_free_text,
                )
                waiting = replace(tool_call, status=ToolCallStatus.AWAITING_ANSWER)
                AGENT_TOOL_CALLS.set(context, waiting.tool_call_id, waiting)
                interrupts.append(interrupt)
                _write_interrupt_raised(events, interrupt)
                return self._question_answer_movement(waiting)
            wait = parse_durable_wait_arguments(tool_call.arguments, self._options.max_durable_wait)
        except BuiltInToolArgumentsError as error:
            _record_final_tool_call(
                context, replace(tool_call, status=ToolCallStatus.FAILED, error=str(error)), events
            )
            return None
        wait_until = _rfc3339(now + timedelta(seconds=wait.duration_seconds))
        waiting = replace(tool_call, status=ToolCallStatus.WAITING_FOR_TIMER, wait_until=wait_until)
        AGENT_TOOL_CALLS.set(context, waiting.tool_call_id, waiting)
        events.write_event(
            AgentEventType.TIMER_STARTED,
            {"tool_call_id": waiting.tool_call_id, "wait_until": wait_until, "reason": wait.reason},
        )
        return self._timer_movement(waiting.tool_call_id)

    def _interrupt_deadline(self, now: datetime) -> str | None:
        if self._options.interrupt_deadline is None:
            return None
        return _rfc3339(now + self._options.interrupt_deadline)

    def _wait_for_tool_approval(self, context: Context, tool_call_id: str) -> Wait:
        conditions: list[Condition] = [INTERRUPT_RESOLUTIONS.for_one(tool_call_id)]
        interrupt = _pending_interrupt(context, tool_call_id)
        # After resolve_interrupt removed the interrupt, its deadline no longer applies.
        if interrupt is not None and interrupt.deadline is not None:
            conditions.append(_timer_until(interrupt.deadline))
        return Wait.any_of(*conditions)

    def _apply_tool_approval(self, context: AsyncContext, tool_call_id: str) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status.is_final:
            return dead_end()
        events = self._event_writer(context, tool_call.turn_id)
        resolutions = INTERRUPT_RESOLUTIONS.results(context, tool_call_id)
        if resolutions:
            return self._resolve_tool_approval(
                context, tool_call, resolutions[0], events, is_deadline_passed=False
            )
        if not context.has_timer_fired():
            raise RuntimeError(f"the approval wait for {tool_call_id} completed without an answer")
        interrupt = _pending_interrupt(context, tool_call_id)
        if interrupt is None or interrupt.deadline is None:
            # resolve_interrupt answered first; the next wait consumes that answer.
            return go_to_many(self._tool_approval_movement(tool_call_id))
        if not _is_deadline_passed(interrupt):
            return go_to_many(self._tool_approval_movement(tool_call_id))
        _remove_pending_interrupt(context, tool_call_id)
        return self._resolve_tool_approval(
            context,
            tool_call,
            AgentInterruptResolution(approved=False, rejection_message=APPROVAL_DEADLINE_PASSED),
            events,
            is_deadline_passed=True,
        )

    def _resolve_tool_approval(
        self,
        context: AsyncContext,
        tool_call: AgentToolCall,
        resolution: AgentInterruptResolution,
        events: AgentEventWriter,
        *,
        is_deadline_passed: bool,
    ) -> StepDecision:
        with self._session_tracer.trace_interrupt_activation(
            context,
            self._await_tool_approval.get_step_type(),
            tool_call.turn_id,
            tool_call.tool_call_id,
            resolution.approved,
            is_deadline_passed,
        ) as activation:
            _write_interrupt_resolved(
                events, tool_call.tool_call_id, InterruptKind.TOOL_APPROVAL, resolution
            )
            if resolution.approved:
                approved = replace(
                    tool_call,
                    status=ToolCallStatus.APPROVED,
                    trace_context=activation.traceparent,
                )
                AGENT_TOOL_CALLS.set(context, approved.tool_call_id, approved)
                return go_to_many(self._tool_call_movement(approved))
            rejected = replace(
                tool_call,
                status=ToolCallStatus.REJECTED,
                rejection_message=resolution.rejection_message,
            )
            return _finish_tool_call(context, rejected, events)

    def _wait_for_question_answer(self, context: Context, tool_call_id: str) -> Wait:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        interrupt_id = _question_interrupt_id(tool_call)
        conditions: list[Condition] = [INTERRUPT_RESOLUTIONS.for_one(interrupt_id)]
        if _is_question_interrupt_id_taken(context, tool_call, interrupt_id):
            # The answers in that instance belong to the other call; Execute fails this one.
            return Wait.until(Timer.by_duration(timedelta(0)))
        interrupt = _pending_interrupt(context, interrupt_id)
        # After answer_question removed the interrupt, its deadline no longer applies.
        if interrupt is not None and interrupt.deadline is not None:
            conditions.append(_timer_until(interrupt.deadline))
        elif _is_tool_question_unraised(context, tool_call, interrupt_id):
            # Execute holds the PendingInterrupts lock, so it raises the tool's question at once.
            conditions.append(Timer.by_duration(timedelta(0)))
        return Wait.any_of(*conditions)

    def _apply_question_answer(self, context: AsyncContext, tool_call_id: str) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status is not ToolCallStatus.AWAITING_ANSWER:
            return dead_end()
        events = self._event_writer(context, tool_call.turn_id)
        interrupt_id = _question_interrupt_id(tool_call)
        if _is_question_interrupt_id_taken(context, tool_call, interrupt_id):
            failed = replace(
                tool_call,
                status=ToolCallStatus.FAILED,
                error=(
                    f"the tool's question cannot use interrupt ID {interrupt_id}, "
                    "which another tool call of this turn uses"
                ),
                **_NO_PENDING_TOOL_QUESTION,
            )
            return _finish_tool_call(context, failed, events)
        resolutions = INTERRUPT_RESOLUTIONS.results(context, interrupt_id)
        if resolutions:
            return self._resolve_question(context, tool_call, interrupt_id, resolutions[0], events)
        if not context.has_timer_fired():
            raise RuntimeError(f"the question wait for {interrupt_id} completed without an answer")
        if _is_tool_question_unraised(context, tool_call, interrupt_id):
            self._raise_tool_question(context, tool_call, interrupt_id, events)
            return go_to_many(self._question_answer_movement(tool_call))
        interrupt = _pending_interrupt(context, interrupt_id)
        if interrupt is None or interrupt.deadline is None or not _is_deadline_passed(interrupt):
            # An early Timer, or an answer that the next wait consumes.
            return go_to_many(self._question_answer_movement(tool_call))
        _remove_pending_interrupt(context, interrupt_id)
        return self._resolve_question(
            context,
            tool_call,
            interrupt_id,
            AgentInterruptResolution(approved=False, rejection_message=QUESTION_DEADLINE_PASSED),
            events,
        )

    def _raise_tool_question(
        self,
        context: AsyncContext,
        tool_call: AgentToolCall,
        interrupt_id: str,
        events: AgentEventWriter,
    ) -> None:
        now = datetime.now(UTC)
        interrupt = AgentInterrupt(
            interrupt_id=interrupt_id,
            kind=InterruptKind.QUESTION,
            tool_call_id=tool_call.tool_call_id,
            tool_name=tool_call.tool_name,
            arguments=tool_call.arguments,
            created_at=_rfc3339(now),
            deadline=self._interrupt_deadline(now),
            question=tool_call.input_question,
            choices=list(tool_call.input_choices),
            allows_free_text=tool_call.input_allows_free_text,
        )
        PENDING_INTERRUPTS.set(context, [*_pending_interrupts(context), interrupt])
        _write_interrupt_raised(events, interrupt)

    def _resolve_question(
        self,
        context: AsyncContext,
        tool_call: AgentToolCall,
        interrupt_id: str,
        resolution: AgentInterruptResolution,
        events: AgentEventWriter,
    ) -> StepDecision:
        _write_interrupt_resolved(events, interrupt_id, InterruptKind.QUESTION, resolution)
        if not resolution.approved or resolution.answer is None:
            rejected = replace(
                tool_call,
                status=ToolCallStatus.REJECTED,
                rejection_message=resolution.rejection_message or QUESTION_DECLINED_TOOL_RESULT,
                **_NO_PENDING_TOOL_QUESTION,
            )
            return _finish_tool_call(context, rejected, events)
        if tool_call.input_question is None:
            answered = replace(
                tool_call,
                status=ToolCallStatus.SUCCEEDED,
                output=build_question_answered_output(resolution.answer),
            )
            return _finish_tool_call(context, answered, events)
        answer = QuestionAnswer(
            interrupt_id=interrupt_id, question=tool_call.input_question, answer=resolution.answer
        )
        approved = replace(
            tool_call,
            status=ToolCallStatus.APPROVED,
            input_answers=[*tool_call.input_answers, answer],
            **_NO_PENDING_TOOL_QUESTION,
        )
        AGENT_TOOL_CALLS.set(context, approved.tool_call_id, approved)
        return go_to_many(self._tool_call_movement(approved))

    def _wait_for_durable_wait_timer(self, context: Context, tool_call_id: str) -> Wait:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.wait_until is None:
            raise RuntimeError(f"durable wait {tool_call_id} has no wait_until")
        return Wait.until(_timer_until(tool_call.wait_until))

    def _finish_durable_wait(self, context: AsyncContext, tool_call_id: str) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status is not ToolCallStatus.WAITING_FOR_TIMER:
            return dead_end()
        if not context.has_timer_fired():
            raise RuntimeError(f"the durable wait {tool_call_id} completed without its Timer")
        # The Timer is the authority: it never fires before wait_until, and SkipTimer ends it early.
        # RunModel validated these arguments; a later max_durable_wait must not fail a started wait.
        arguments = json.loads(tool_call.arguments)
        completed = replace(
            tool_call,
            status=ToolCallStatus.SUCCEEDED,
            output=build_durable_wait_completed_output(
                int(arguments["duration_seconds"]), str(arguments["reason"])
            ),
        )
        return _finish_tool_call(context, completed, self._event_writer(context, tool_call.turn_id))

    async def _run_tool_call(
        self,
        context: AsyncContext,
        tool_call_id: str,
        step_type: str,
    ) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status.is_final:
            return dead_end()
        if tool_call.status is not ToolCallStatus.APPROVED:
            raise RuntimeError(f"tool call {tool_call_id} is not approved")
        with self._session_tracer.trace_tool_call(context, step_type, tool_call) as tool_call_span:
            events = self._event_writer(context, tool_call.turn_id)
            events.write_tool_call_started(tool_call.tool_call_id, tool_call.tool_name)
            heartbeats = _ToolStepHeartbeats(context)
            # Dex may redeliver a task without output to another Worker, at the same attempt.
            await heartbeats.record()
            execution = DeferredToolExecution(
                flow_id=context.flow_id,
                tool_call=tool_call,
                idempotency_key=f"{context.flow_id}/{tool_call.turn_id}/{tool_call.tool_call_id}",
                attempt=context.attempt,
                events=events,
                heartbeat=heartbeats.record,
                heartbeat_checkpoint=heartbeats.previous_checkpoint,
                save_heartbeat_checkpoint=heartbeats.save_checkpoint,
                input_answers=tuple(tool_call.input_answers),
            )
            async with _background_heartbeats(heartbeats.record):
                try:
                    result = await self._runner.execute_deferred_tool_call(execution)
                except ToolExecutionError as error:
                    failed = replace(tool_call, status=ToolCallStatus.FAILED, error=str(error))
                    tool_call_span.record_finished(failed)
                    return _finish_tool_call(context, failed, events)
            if isinstance(result, ToolInputRequested):
                asked = _record_tool_input_request(tool_call, result)
                tool_call_span.record_finished(asked)
                if asked.status.is_final:
                    return _finish_tool_call(context, asked, events)
                # This Step runs unlocked; AwaitQuestionAnswer raises the interrupt under the lock.
                AGENT_TOOL_CALLS.set(context, asked.tool_call_id, asked)
                return go_to_many(self._question_answer_movement(asked))
            succeeded = replace(tool_call, status=ToolCallStatus.SUCCEEDED, output=result.output)
            tool_call_span.record_finished(succeeded)
            return _finish_tool_call(context, succeeded, events)

    def _record_tool_recovery(
        self,
        context: AsyncContext,
        tool_call_id: str,
        status: ToolCallStatus,
    ) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status.is_final:
            return dead_end()
        finished = replace(tool_call, status=status, error=_recovery_detail(context))
        return _finish_tool_call(context, finished, self._event_writer(context, tool_call.turn_id))

    def _resume_turn_after_tool_calls(self, context: AsyncContext) -> StepDecision:
        state = _require_agent_state(context)
        tool_calls = _turn_tool_calls(context, _require_active_turn_id(state))
        unfinished_count = sum(1 for tool_call in tool_calls if not tool_call.status.is_final)
        if unfinished_count:
            # A consumed result from an earlier turn must not resume this one early.
            return go_to(self._resume_turn, unfinished_count)
        AGENT_STATE.set(context, replace(state, status=AgentSessionStatus.RUNNING_MODEL))
        return go_to(self._run_model, None)

    def _fail_active_turn(self, context: AsyncContext) -> StepDecision:
        state = _require_agent_state(context)
        turn_id = _require_active_turn_id(state)
        tool_calls = _turn_tool_calls(context, turn_id)
        events = self._event_writer(context, turn_id)
        return self._fail_turn(context, state, tool_calls, events, _recovery_detail(context))

    def _fail_turn(
        self,
        context: AsyncContext,
        state: AgentState,
        tool_calls: Sequence[AgentToolCall],
        events: AgentEventWriter,
        error: str,
    ) -> StepDecision:
        turn_id = _require_active_turn_id(state)
        messages = list(self._build_unfinished_turn_messages(context, state, tool_calls, events))
        finished_tool_calls = [tool_call for tool_call in tool_calls if tool_call.status.is_final]
        if finished_tool_calls:
            # The records are deleted below; the note keeps the next model from repeating writes.
            messages.append(
                self._runner.build_user_message(build_turn_failure_note(finished_tool_calls))
            )
        state = _append_messages(context, state, turn_id, messages)
        for tool_call in tool_calls:
            AGENT_TOOL_CALLS.delete(context, tool_call.tool_call_id)
        if FRAMEWORK_RUN_STATE.get(context) is not None:
            FRAMEWORK_RUN_STATE.delete(context)
        PENDING_INTERRUPTS.set(context, [])
        AGENT_STATE.set(
            context,
            replace(
                state,
                status=AgentSessionStatus.WAITING_FOR_USER_MESSAGE,
                turn_count=state.turn_count + 1,
                active_turn_id=None,
                last_turn_error=error,
            ),
        )
        events.write_event(AgentEventType.TURN_FAILED, {"error": error})
        return go_to(self._await_user_message, None)

    def _build_unfinished_turn_messages(
        self,
        context: AsyncContext,
        state: AgentState,
        tool_calls: Sequence[AgentToolCall],
        events: AgentEventWriter,
    ) -> Iterable[FrameworkMessage]:
        if not isinstance(self._runner, UnfinishedTurnMessageBuilder):
            return ()
        try:
            turn = self._turn_context(context, state, tool_calls, events)
            messages = list(self._runner.build_unfinished_turn_messages(turn))
            for message in messages:
                _validate_framework_message(message)
            return messages
        except Exception:
            # A runner defect must not block the failure route; the note still stores.
            _LOGGER.warning(
                "build_unfinished_turn_messages failed for %s; storing only the note",
                state.active_turn_id,
                exc_info=True,
            )
            return ()

    def _tool_call_movement(self, tool_call: AgentToolCall) -> StepMovement[str]:
        # Per-movement options load only this call's record, not the whole map.
        record_load = AGENT_TOOL_CALLS.load(tool_call.tool_call_id)
        if tool_call.effect in MULTI_ATTEMPT_TOOL_EFFECTS:
            step: CallTool | CallSingleAttemptTool = self._call_tool
            recovery_step: RecordToolFailure | RecordToolOutcomeUnknown = self._record_tool_failure
        else:
            step = self._call_single_attempt_tool
            recovery_step = self._record_tool_outcome_unknown
        recovery_options = replace(
            recovery_step.get_step_options(),
            execute_load_attribute_maps=(),
            execute_load_attribute_map_instances=(record_load,),
        )
        options = replace(
            step.get_step_options(),
            execute_load_attribute_maps=(),
            execute_load_attribute_map_instances=(record_load,),
        ).on_execute_failure_proceed_to(recovery_step, recovery_options)
        return StepMovement.of(step, tool_call.tool_call_id, options=options)

    def _tool_approval_movement(self, tool_call_id: str) -> StepMovement[str]:
        options = replace(
            self._await_tool_approval.get_step_options(),
            execute_load_attribute_maps=(),
            execute_load_attribute_map_instances=(AGENT_TOOL_CALLS.load(tool_call_id),),
        )
        return StepMovement.of(self._await_tool_approval, tool_call_id, options=options)

    def _question_answer_movement(self, tool_call: AgentToolCall) -> StepMovement[str]:
        interrupt_id = _question_interrupt_id(tool_call)
        record_loads = tuple(
            AGENT_TOOL_CALLS.load(key)
            for key in dict.fromkeys((tool_call.tool_call_id, interrupt_id))
        )
        # The Step reads the question's answer count to tell an unraised question from an answer.
        answer_load = INTERRUPT_RESOLUTIONS.load_messages(interrupt_id)
        return StepMovement.of(
            self._await_question_answer,
            tool_call.tool_call_id,
            options=replace(
                self._await_question_answer.get_step_options(),
                wait_for_load_attribute_maps=(),
                wait_for_load_attribute_map_instances=record_loads,
                execute_load_attribute_maps=(),
                execute_load_attribute_map_instances=record_loads,
                wait_for_load_channel_map_instances=(answer_load,),
                execute_load_channel_map_instances=(answer_load,),
            ),
        )

    def _timer_movement(self, tool_call_id: str) -> StepMovement[str]:
        return StepMovement.of(
            self._await_timer,
            tool_call_id,
            options=_with_record_loads(self._await_timer.get_step_options(), tool_call_id),
        )

    def _event_writer(self, context: AsyncContext, turn_id: str | None) -> AgentEventWriter:
        return AgentEventWriter(
            lambda event: self.agent_events.write(context, event),
            turn_id,
            context.attempt,
            context.step_execution_id,
        )


class StartAgentSession(Step[AgentSessionStartInput]):
    """Initialize the session and start its first turn when a message is given."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_durability=StepDurability.SYNC)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: AgentSessionStartInput,
    ) -> StepDecision:
        AGENT_STATE.set(context, AgentState(status=AgentSessionStatus.WAITING_FOR_USER_MESSAGE))
        AGENT_USAGE.set(context, AgentUsage())
        PENDING_INTERRUPTS.set(context, [])
        if input.initial_message is None or not input.initial_message.strip():
            return go_to(self._session._await_user_message, None)
        return self._session._begin_turn(context, input.initial_message, self.get_step_type())


class AwaitUserMessage(Step[None]):
    """Wait for one queued user message and start its turn."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
        )

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(QUEUED_USER_MESSAGES.for_one())

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        messages = QUEUED_USER_MESSAGES.results(context)
        if not messages:
            raise RuntimeError("the user message wait completed without a message")
        return self._session._begin_turn(context, messages[0].text, self.get_step_type())


class RunModel(Step[None]):
    """Make one model call of the active turn through the framework runner."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_method_timeout=timedelta(minutes=15),
            heartbeat_timeout=timedelta(minutes=2),
            execute_retry=RetryPolicy(maximum_attempts=3, total_duration=timedelta(minutes=30)),
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_MESSAGES, AGENT_TOOL_CALLS),
        ).on_execute_failure_proceed_to(self._session._record_turn_failure)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        return await self._session._run_model_step(context)


class AwaitToolApproval(Step[str]):
    """Wait for one tool call's approval or deadline, then run or reject it."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # The deadline path rewrites PendingInterrupts, which resolve_interrupt also writes.
        return StepOptions(
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
            execute_lock_attributes=(PENDING_INTERRUPTS.lock(),),
        )

    def wait_for(self, context: Context, input: str) -> Wait:
        return self._session._wait_for_tool_approval(context, input)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        return self._session._apply_tool_approval(context, input)


class AwaitQuestionAnswer(Step[str]):
    """Wait for one question's answer, decline, or deadline, then record the result."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # The deadline path rewrites PendingInterrupts, which answer_question also writes.
        return StepOptions(
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
            wait_for_load_attribute_maps=(AGENT_TOOL_CALLS,),
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
            execute_lock_attributes=(PENDING_INTERRUPTS.lock(),),
        )

    def wait_for(self, context: Context, input: str) -> Wait:
        return self._session._wait_for_question_answer(context, input)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        return self._session._apply_question_answer(context, input)


class AwaitTimer(Step[str]):
    """Wait on the Timer of one ``durable_wait`` call, then record that it completed."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
            wait_for_load_attribute_maps=(AGENT_TOOL_CALLS,),
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
        )

    def wait_for(self, context: Context, input: str) -> Wait:
        return self._session._wait_for_durable_wait_timer(context, input)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        return self._session._finish_durable_wait(context, input)


class CallTool(Step[str]):
    """Run one read or idempotent write tool call with up to three attempts."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=3),
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
        ).on_execute_failure_proceed_to(self._session._record_tool_failure)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        return await self._session._run_tool_call(context, input, self.get_step_type())


class CallSingleAttemptTool(Step[str]):
    """Run one non-idempotent or unclassified tool call exactly once."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=1),
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
        ).on_execute_failure_proceed_to(self._session._record_tool_outcome_unknown)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        return await self._session._run_tool_call(context, input, self.get_step_type())


class RecordToolFailure(Step[str]):
    """Record a ``CallTool`` call whose attempts ran out: failed for reads, unknown for writes."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RECOVERY_RETRY,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
        )

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, input)
        status = (
            ToolCallStatus.FAILED
            if tool_call.effect is ToolEffect.READ
            else ToolCallStatus.OUTCOME_UNKNOWN
        )
        return self._session._record_tool_recovery(context, input, status)


class RecordToolOutcomeUnknown(Step[str]):
    """Record that a single-attempt tool call stopped without a result."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RECOVERY_RETRY,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
        )

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        return self._session._record_tool_recovery(context, input, ToolCallStatus.OUTCOME_UNKNOWN)


class ResumeTurn(Step[int]):
    """Wait for every tool call result of the model response, then call the model again."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
        )

    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(TOOL_CALL_RESULTS.for_n(input))

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: int,
    ) -> StepDecision:
        return self._session._resume_turn_after_tool_calls(context)


class RecordTurnFailure(Step[None]):
    """Record a failed turn and wait for the next user message."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # An incremental runner reads the turn's stored messages to finish its transcript.
        loads: tuple[AttributeMap[Any], ...] = (
            (AGENT_TOOL_CALLS, AGENT_MESSAGES)
            if isinstance(self._session.runner, UnfinishedTurnMessageBuilder)
            else (AGENT_TOOL_CALLS,)
        )
        return StepOptions(
            execute_retry=RECOVERY_RETRY,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=loads,
        )

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        return self._session._fail_active_turn(context)


class _ToolStepHeartbeats:
    """Heartbeats of one tool Step attempt; each repeats the latest checkpoint."""

    def __init__(self, context: AsyncContext) -> None:
        self._context = context
        self.previous_checkpoint: str | None = (
            context.get_last_heartbeat_value(str) if context.has_last_heartbeat_value() else None
        )
        # A bare heartbeat clears saved details, so carry the earlier attempt's forward.
        self._checkpoint = self.previous_checkpoint

    async def record(self) -> None:
        if self._checkpoint is None:
            await self._context.heartbeat()
        else:
            await self._context.heartbeat(self._checkpoint)

    async def save_checkpoint(self, checkpoint: str) -> None:
        self._checkpoint = checkpoint
        await self.record()


@contextlib.asynccontextmanager
async def _background_heartbeats(record: Callable[[], Awaitable[None]]) -> AsyncIterator[None]:
    heartbeats = asyncio.create_task(_record_heartbeats(record))
    try:
        yield
    finally:
        heartbeats.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await heartbeats


async def _record_heartbeats(record: Callable[[], Awaitable[None]]) -> None:
    while True:
        await asyncio.sleep(BACKGROUND_HEARTBEAT_INTERVAL.total_seconds())
        await record()


def _finish_tool_call(
    context: AsyncContext,
    finished: AgentToolCall,
    events: AgentEventWriter,
) -> StepDecision:
    _record_final_tool_call(context, finished, events)
    return dead_end()


def _record_final_tool_call(
    context: AsyncContext,
    finished: AgentToolCall,
    events: AgentEventWriter,
) -> None:
    AGENT_TOOL_CALLS.set(context, finished.tool_call_id, finished)
    TOOL_CALL_RESULTS.publish(context, ToolCallCompleted(tool_call_id=finished.tool_call_id))
    events.write_tool_call_finished(finished.tool_call_id, finished.tool_name, finished.status)


def _write_interrupt_raised(events: AgentEventWriter, interrupt: AgentInterrupt) -> None:
    events.write_event(
        AgentEventType.INTERRUPT_RAISED,
        {
            "interrupt_id": interrupt.interrupt_id,
            "tool_call_id": interrupt.tool_call_id,
            "tool_name": interrupt.tool_name,
            "deadline": interrupt.deadline,
            "kind": str(interrupt.kind),
            "question": interrupt.question,
            "choices": list(interrupt.choices),
            "allows_free_text": interrupt.allows_free_text,
        },
    )


def _write_interrupt_resolved(
    events: AgentEventWriter,
    interrupt_id: str,
    kind: InterruptKind,
    resolution: AgentInterruptResolution,
) -> None:
    events.write_event(
        AgentEventType.INTERRUPT_RESOLVED,
        {
            "interrupt_id": interrupt_id,
            "approved": resolution.approved,
            "rejection_message": resolution.rejection_message,
            "kind": str(kind),
            "answered": kind is InterruptKind.QUESTION
            and resolution.approved
            and resolution.answer is not None,
            "cancellation_reason": None,
        },
    )


def _record_tool_input_request(
    tool_call: AgentToolCall, request: ToolInputRequested
) -> AgentToolCall:
    """Return the record of a tool's question: awaiting its answer, or failed when blank."""
    choices = list(request.choices)
    if not request.question.strip() or not all(choice.strip() for choice in choices):
        return replace(
            tool_call,
            status=ToolCallStatus.FAILED,
            error="the tool asked for input with a blank question or choice",
        )
    return replace(
        tool_call,
        status=ToolCallStatus.AWAITING_ANSWER,
        input_continuation=request.continuation,
        input_question=request.question,
        input_choices=choices,
        input_allows_free_text=request.allows_free_text or not choices,
    )


def _question_interrupt_id(tool_call: AgentToolCall) -> str:
    # An ask_user question uses the call ID; a tool's n-th question appends "-input-<n>".
    if tool_call.input_question is None:
        return tool_call.tool_call_id
    return f"{tool_call.tool_call_id}-input-{len(tool_call.input_answers) + 1}"


def _is_question_interrupt_id_taken(
    context: Context, tool_call: AgentToolCall, interrupt_id: str
) -> bool:
    """Report whether a tool question's ID belongs to another call's interrupt or record."""
    if tool_call.status is not ToolCallStatus.AWAITING_ANSWER or tool_call.input_question is None:
        return False
    pending = _pending_interrupt(context, interrupt_id)
    if pending is not None and pending.tool_call_id != tool_call.tool_call_id:
        return True
    return cast(AgentToolCall | None, AGENT_TOOL_CALLS.get(context, interrupt_id)) is not None


def _validate_framework_message(message: object) -> None:
    if not isinstance(message, FrameworkMessage):
        raise TypeError(f"expected a FrameworkMessage, got {type(message).__name__}")
    MessageRole(message.role)
    if not isinstance(message.framework_item, str):
        raise TypeError("FrameworkMessage.framework_item must be a str")
    for name in ("text", "tool_call_id", "tool_name", "tool_arguments", "tool_output"):
        if not isinstance(getattr(message, name), str | None):
            raise TypeError(f"FrameworkMessage.{name} must be a str or None")


def _rfc3339(moment: datetime) -> str:
    # Milliseconds and "Z", byte for byte what JavaScript's toISOString writes.
    return moment.astimezone(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _is_tool_question_unraised(
    context: Context, tool_call: AgentToolCall, interrupt_id: str
) -> bool:
    """Report whether a tool's question waits to be raised: not pending and not answered."""
    return (
        tool_call.status is ToolCallStatus.AWAITING_ANSWER
        and tool_call.input_question is not None
        and _pending_interrupt(context, interrupt_id) is None
        and INTERRUPT_RESOLUTIONS.size(context, interrupt_id) == 0
    )


def _timer_until(rfc3339_time: str) -> Condition:
    remaining = datetime.fromisoformat(rfc3339_time) - datetime.now(UTC)
    # Dex Timers take whole seconds; an early fire waits again in execute.
    whole_seconds = math.ceil(max(remaining.total_seconds(), 0))
    return Timer.by_duration(timedelta(seconds=whole_seconds))


def _with_record_loads(options: StepOptions, tool_call_id: str) -> StepOptions:
    # Per-movement options load only this call's record, not the whole map.
    record_load = AGENT_TOOL_CALLS.load(tool_call_id)
    return replace(
        options,
        wait_for_load_attribute_maps=(),
        wait_for_load_attribute_map_instances=(record_load,),
        execute_load_attribute_maps=(),
        execute_load_attribute_map_instances=(record_load,),
    )


def _remove_pending_interrupt(context: Context, interrupt_id: str) -> None:
    PENDING_INTERRUPTS.set(
        context,
        [
            interrupt
            for interrupt in _pending_interrupts(context)
            if interrupt.interrupt_id != interrupt_id
        ],
    )


def _append_messages(
    context: Context,
    state: AgentState,
    turn_id: str,
    messages: Sequence[FrameworkMessage],
) -> AgentState:
    sequence = state.next_message_sequence
    created_at = _rfc3339(datetime.now(UTC))
    for message in messages:
        AGENT_MESSAGES.set(
            context,
            message_sequence_key(sequence),
            AgentMessage(
                sequence=sequence,
                turn_id=turn_id,
                role=message.role,
                text=message.text,
                tool_call_id=message.tool_call_id,
                tool_name=message.tool_name,
                tool_arguments=message.tool_arguments,
                tool_output=message.tool_output,
                framework_item=message.framework_item,
                created_at=created_at,
            ),
        )
        sequence += 1
    state = replace(state, next_message_sequence=sequence)
    AGENT_STATE.set(context, state)
    return state


def _read_messages(context: Context, start: int, end: int) -> list[AgentMessage]:
    return [
        AGENT_MESSAGES.get(context, message_sequence_key(sequence))
        for sequence in range(max(1, start), end)
    ]


def _turn_tool_calls(context: Context, turn_id: str | None) -> list[AgentToolCall]:
    if turn_id is None:
        return []
    tool_calls = [
        AGENT_TOOL_CALLS.get(context, tool_call_id)
        for tool_call_id in AGENT_TOOL_CALLS.get_all_instance_keys(context)
    ]
    return sorted(
        (tool_call for tool_call in tool_calls if tool_call.turn_id == turn_id),
        key=lambda tool_call: tool_call.deferral_order,
    )


def _record_usage(context: Context, events: AgentEventWriter, usage: AgentUsage) -> None:
    session_usage = _agent_usage(context).combined_with(usage)
    AGENT_USAGE.set(context, session_usage)
    events.write_event(
        AgentEventType.USAGE_RECORDED,
        {"recorded": _usage_payload(usage), "session": _usage_payload(session_usage)},
    )


def _usage_payload(usage: AgentUsage) -> dict[str, int]:
    return {
        "model_calls": usage.model_calls,
        "input_tokens": usage.input_tokens,
        "output_tokens": usage.output_tokens,
        "cached_input_tokens": usage.cached_input_tokens,
        "reasoning_tokens": usage.reasoning_tokens,
    }


def _validate_tool_calls_requested(
    outcome: ToolCallsRequested,
    tool_calls: Sequence[AgentToolCall],
) -> None:
    if not outcome.framework_run_state:
        raise ValueError("ToolCallsRequested needs a non-empty framework_run_state")
    if not outcome.tool_calls:
        raise ValueError("ToolCallsRequested needs at least one tool call")
    seen_ids = {tool_call.tool_call_id for tool_call in tool_calls}
    for requested in outcome.tool_calls:
        if not requested.tool_call_id.strip() or "/" in requested.tool_call_id:
            raise ValueError(f"tool call ID {requested.tool_call_id!r} is empty or contains '/'")
        if requested.tool_call_id in seen_ids:
            raise ValueError(
                f"tool call {requested.tool_call_id} was already requested in this turn"
            )
        seen_ids.add(requested.tool_call_id)


def _recovery_detail(context: Context) -> str:
    error = context.recovery_error
    if error is None:
        return "unknown failure"
    return error.detail or error.error_type


def _optional_agent_state(context: Context) -> AgentState | None:
    return cast(AgentState | None, AGENT_STATE.get(context))


def _require_agent_state(context: Context) -> AgentState:
    state = _optional_agent_state(context)
    if state is None:
        raise RuntimeError("the agent session is not initialized")
    return state


def _require_active_turn_id(state: AgentState) -> str:
    if state.active_turn_id is None:
        raise RuntimeError("the agent session has no active turn")
    return state.active_turn_id


def _pending_interrupts(context: Context) -> list[AgentInterrupt]:
    return list(cast(list[AgentInterrupt] | None, PENDING_INTERRUPTS.get(context)) or [])


def _is_deadline_passed(interrupt: AgentInterrupt) -> bool:
    return interrupt.deadline is not None and datetime.fromisoformat(
        interrupt.deadline
    ) <= datetime.now(UTC)


def _pending_interrupt(context: Context, interrupt_id: str) -> AgentInterrupt | None:
    return next(
        (
            interrupt
            for interrupt in _pending_interrupts(context)
            if interrupt.interrupt_id == interrupt_id
        ),
        None,
    )


def _agent_usage(context: Context) -> AgentUsage:
    return cast(AgentUsage | None, AGENT_USAGE.get(context)) or AgentUsage()
