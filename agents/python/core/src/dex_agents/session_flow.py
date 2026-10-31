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
of their results before the next ``RunModel``.
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator, Mapping, Sequence
from dataclasses import dataclass, field, replace
from datetime import UTC, datetime, timedelta
from typing import cast

from dex import (
    AsyncContext,
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

from dex_agents.events import AgentEventType, AgentEventWriter
from dex_agents.persistence import (
    AGENT_MESSAGES,
    AGENT_STATE,
    AGENT_TOOL_CALLS,
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
    FrameworkMessage,
    ToolCallsRequested,
    ToolExecutionError,
    ToolPolicy,
    TurnCompleted,
    build_turn_failure_note,
)
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
    InterruptKind,
    ListAgentMessagesRequest,
    MessageRole,
    ResolveInterruptRequest,
    ToolCallCompleted,
    ToolCallStatus,
    ToolEffect,
)

SNAPSHOT_MESSAGE_LIMIT = 200
MESSAGE_PAGE_LIMIT = 200
# Keeps liveness well inside the shortest heartbeat timeout, one minute.
BACKGROUND_HEARTBEAT_INTERVAL = timedelta(seconds=20)
RECOVERY_RETRY = RetryPolicy(maximum_interval=timedelta(seconds=5))
MULTI_ATTEMPT_TOOL_EFFECTS = (ToolEffect.READ, ToolEffect.IDEMPOTENT_WRITE)


@dataclass(frozen=True, kw_only=True)
class AgentSessionOptions:
    """Configure tool policies, live events, and approval deadlines for one agent.

    Pass it to ``AgentSessionFlow``. Options are read when Steps run, so a
    change applies to running sessions after the Worker restarts.

    Example:
        >>> options = AgentSessionOptions(
        ...     tool_policies={"search": ToolPolicy(ToolEffect.READ)},
        ...     default_tool_effect=ToolEffect.UNCLASSIFIED,
        ...     interrupt_deadline=timedelta(hours=24),
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
        interrupt_deadline: How long an approval may stay pending before the
            session rejects it with ``"approval deadline passed"``. ``None``, the
            default, waits indefinitely.

    Raises:
        ValueError: If ``event_stream_capacity_bytes`` or ``interrupt_deadline``
            is not positive.
    """

    tool_policies: Mapping[str, ToolPolicy] = field(default_factory=dict)
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES
    interrupt_deadline: timedelta | None = None

    def __post_init__(self) -> None:
        if self.event_stream_capacity_bytes <= 0:
            raise ValueError("event_stream_capacity_bytes must be positive")
        if self.interrupt_deadline is not None and self.interrupt_deadline <= timedelta(0):
            raise ValueError("interrupt_deadline must be positive")
        object.__setattr__(self, "tool_policies", dict(self.tool_policies))


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

    def __init__(
        self,
        runner: AgentFrameworkRunner,
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
            options: Tool policies, Stream capacity, and approval deadline.
                ``None`` uses ``AgentSessionOptions()``.

        Raises:
            ValueError: If ``flow_type`` is empty or whitespace-only.
        """
        if not flow_type.strip():
            raise ValueError("flow_type must not be empty")
        self._runner = runner
        self._flow_type = flow_type
        self._options = options or AgentSessionOptions()
        self.agent_events: Stream[AgentEvent] = Stream(
            "AgentEvents", AgentEvent, self._options.event_stream_capacity_bytes
        )
        self._start_agent_session = StartAgentSession(self)
        self._await_user_message = AwaitUserMessage(self)
        self._run_model = RunModel(self)
        self._await_tool_approval = AwaitToolApproval(self)
        self._call_tool = CallTool(self)
        self._call_single_attempt_tool = CallSingleAttemptTool(self)
        self._record_tool_failure = RecordToolFailure(self)
        self._record_tool_outcome_unknown = RecordToolOutcomeUnknown(self)
        self._resume_turn = ResumeTurn(self)
        self._record_turn_failure = RecordTurnFailure(self)

    @property
    def runner(self) -> AgentFrameworkRunner:
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
            self._call_tool,
            self._call_single_attempt_tool,
            self._record_tool_failure,
            self._record_tool_outcome_unknown,
            self._resume_turn,
            self._record_turn_failure,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        """Return the session Attributes, Channels, and ``AgentEvents`` Stream.

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

        Args:
            context: RPC Context supplied by Dex.
            input: Interrupt ID, decision, and optional rejection message.

        Returns:
            ``True`` when the interrupt was pending and is now resolved;
            ``False`` when it is unknown or already resolved.
        """
        pending = _pending_interrupts(context)
        resolved = next(
            (interrupt for interrupt in pending if interrupt.interrupt_id == input.interrupt_id),
            None,
        )
        if resolved is None:
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

    def _begin_turn(self, context: AsyncContext, text: str) -> StepDecision:
        state = _require_agent_state(context)
        turn_id = f"turn-{state.turn_count + 1}"
        user_message = self._runner.build_user_message(text)
        if user_message.role is not MessageRole.USER:
            raise ValueError("build_user_message must return a user message")
        sequence = state.next_message_sequence
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
        turn = AgentTurnContext(
            flow_id=context.flow_id,
            turn_id=turn_id,
            attempt=context.attempt,
            messages=_read_messages(context, 1, state.next_message_sequence),
            framework_run_state=FRAMEWORK_RUN_STATE.get(context),
            tool_calls={tool_call.tool_call_id: tool_call for tool_call in tool_calls},
            tool_policies=self._options.tool_policies,
            default_tool_effect=self._options.default_tool_effect,
            events=events,
            heartbeat=context.heartbeat,
        )
        async with _background_heartbeats(context):
            outcome = await self._runner.run_model_step(turn)
        if isinstance(outcome, TurnCompleted):
            return self._complete_turn(context, state, tool_calls, outcome, events)
        if isinstance(outcome, ToolCallsRequested):
            return self._start_tool_calls(context, state, tool_calls, outcome, events)
        raise TypeError("run_model_step must return TurnCompleted or ToolCallsRequested")

    def _complete_turn(
        self,
        context: AsyncContext,
        state: AgentState,
        tool_calls: Sequence[AgentToolCall],
        outcome: TurnCompleted,
        events: AgentEventWriter,
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
        events.write_event(
            AgentEventType.TURN_COMPLETED, {"message_count": len(outcome.new_messages)}
        )
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
        FRAMEWORK_RUN_STATE.set(context, outcome.framework_run_state)
        now = datetime.now(UTC)
        deadline = (
            None
            if self._options.interrupt_deadline is None
            else (now + self._options.interrupt_deadline).isoformat()
        )
        interrupts = _pending_interrupts(context)
        movements: list[StepMovement[str]] = []
        for index, requested in enumerate(outcome.tool_calls):
            requires_approval = requested.policy.is_approval_required
            tool_call = AgentToolCall(
                tool_call_id=requested.tool_call_id,
                turn_id=turn_id,
                tool_name=requested.tool_name,
                arguments=requested.arguments,
                effect=requested.policy.effect,
                requires_approval=requires_approval,
                status=(
                    ToolCallStatus.AWAITING_APPROVAL
                    if requires_approval
                    else ToolCallStatus.APPROVED
                ),
                deferral_order=len(tool_calls) + index,
            )
            AGENT_TOOL_CALLS.set(context, tool_call.tool_call_id, tool_call)
            events.write_event(
                AgentEventType.TOOL_CALL_DEFERRED,
                {
                    "tool_call_id": tool_call.tool_call_id,
                    "tool_name": tool_call.tool_name,
                    "effect": str(tool_call.effect),
                    "requires_approval": requires_approval,
                },
            )
            if not requires_approval:
                movements.append(
                    StepMovement.of(self._tool_call_step(tool_call), tool_call.tool_call_id)
                )
                continue
            interrupt = AgentInterrupt(
                interrupt_id=tool_call.tool_call_id,
                kind=InterruptKind.TOOL_APPROVAL,
                tool_call_id=tool_call.tool_call_id,
                tool_name=tool_call.tool_name,
                arguments=tool_call.arguments,
                created_at=now.isoformat(),
                deadline=deadline,
            )
            interrupts.append(interrupt)
            events.write_event(
                AgentEventType.INTERRUPT_RAISED,
                {
                    "interrupt_id": interrupt.interrupt_id,
                    "tool_call_id": interrupt.tool_call_id,
                    "tool_name": interrupt.tool_name,
                    "deadline": interrupt.deadline,
                },
            )
            movements.append(StepMovement.of(self._await_tool_approval, tool_call.tool_call_id))
        PENDING_INTERRUPTS.set(context, interrupts)
        _record_usage(context, events, outcome.usage)
        AGENT_STATE.set(context, replace(state, status=AgentSessionStatus.RUNNING_TOOLS))
        return go_to_many(
            *movements,
            StepMovement.of(self._resume_turn, len(outcome.tool_calls)),
        )

    def _wait_for_tool_approval(self, context: Context, tool_call_id: str) -> Wait:
        conditions: list[Condition] = [INTERRUPT_RESOLUTIONS.for_one(tool_call_id)]
        interrupt = _pending_interrupt(context, tool_call_id)
        # After resolve_interrupt removed the interrupt, its deadline no longer applies.
        if interrupt is not None and interrupt.deadline is not None:
            remaining = datetime.fromisoformat(interrupt.deadline) - datetime.now(UTC)
            conditions.append(Timer.by_duration(max(remaining, timedelta(0))))
        return Wait.any_of(*conditions)

    def _apply_tool_approval(self, context: AsyncContext, tool_call_id: str) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status.is_final:
            return dead_end()
        events = self._event_writer(context, tool_call.turn_id)
        resolutions = INTERRUPT_RESOLUTIONS.results(context, tool_call_id)
        if resolutions:
            return self._resolve_tool_approval(context, tool_call, resolutions[0], events)
        if not context.has_timer_fired():
            raise RuntimeError(f"the approval wait for {tool_call_id} completed without an answer")
        interrupt = _pending_interrupt(context, tool_call_id)
        if interrupt is None or interrupt.deadline is None:
            # resolve_interrupt answered first; the next wait consumes that answer.
            return go_to(self._await_tool_approval, tool_call_id)
        if datetime.fromisoformat(interrupt.deadline) > datetime.now(UTC):
            return go_to(self._await_tool_approval, tool_call_id)
        PENDING_INTERRUPTS.set(
            context,
            [
                pending
                for pending in _pending_interrupts(context)
                if pending.interrupt_id != tool_call_id
            ],
        )
        return self._resolve_tool_approval(
            context,
            tool_call,
            AgentInterruptResolution(approved=False, rejection_message=APPROVAL_DEADLINE_PASSED),
            events,
        )

    def _resolve_tool_approval(
        self,
        context: AsyncContext,
        tool_call: AgentToolCall,
        resolution: AgentInterruptResolution,
        events: AgentEventWriter,
    ) -> StepDecision:
        events.write_event(
            AgentEventType.INTERRUPT_RESOLVED,
            {
                "interrupt_id": tool_call.tool_call_id,
                "approved": resolution.approved,
                "rejection_message": resolution.rejection_message,
            },
        )
        if resolution.approved:
            approved = replace(tool_call, status=ToolCallStatus.APPROVED)
            AGENT_TOOL_CALLS.set(context, approved.tool_call_id, approved)
            return go_to(self._tool_call_step(approved), approved.tool_call_id)
        rejected = replace(
            tool_call,
            status=ToolCallStatus.REJECTED,
            rejection_message=resolution.rejection_message,
        )
        return _finish_tool_call(context, rejected, events)

    async def _run_tool_call(self, context: AsyncContext, tool_call_id: str) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status.is_final:
            return dead_end()
        if tool_call.status is not ToolCallStatus.APPROVED:
            raise RuntimeError(f"tool call {tool_call_id} is not approved")
        events = self._event_writer(context, tool_call.turn_id)
        events.write_tool_call_started(tool_call.tool_call_id, tool_call.tool_name)
        # Dex may redeliver a task without output to another Worker, at the same attempt.
        await context.heartbeat()
        execution = DeferredToolExecution(
            flow_id=context.flow_id,
            tool_call=tool_call,
            idempotency_key=f"{context.flow_id}/{tool_call.turn_id}/{tool_call.tool_call_id}",
            attempt=context.attempt,
            events=events,
            heartbeat=context.heartbeat,
        )
        async with _background_heartbeats(context):
            try:
                result = await self._runner.execute_deferred_tool_call(execution)
                finished = replace(tool_call, status=ToolCallStatus.SUCCEEDED, output=result.output)
            except ToolExecutionError as error:
                finished = replace(tool_call, status=ToolCallStatus.FAILED, error=str(error))
        return _finish_tool_call(context, finished, events)

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
        error = _recovery_detail(context)
        tool_calls = _turn_tool_calls(context, turn_id)
        finished_tool_calls = [tool_call for tool_call in tool_calls if tool_call.status.is_final]
        if finished_tool_calls:
            # The records are deleted below; the note keeps the next model from repeating writes.
            note = self._runner.build_user_message(build_turn_failure_note(finished_tool_calls))
            state = _append_messages(context, state, turn_id, [note])
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
        self._event_writer(context, turn_id).write_event(
            AgentEventType.TURN_FAILED, {"error": error}
        )
        return go_to(self._await_user_message, None)

    def _tool_call_step(self, tool_call: AgentToolCall) -> CallTool | CallSingleAttemptTool:
        if tool_call.effect in MULTI_ATTEMPT_TOOL_EFFECTS:
            return self._call_tool
        return self._call_single_attempt_tool

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
        return self._session._begin_turn(context, input.initial_message)


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
        return self._session._begin_turn(context, messages[0].text)


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
        return await self._session._run_tool_call(context, input)


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
        return await self._session._run_tool_call(context, input)


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
        return StepOptions(
            execute_retry=RECOVERY_RETRY,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
        )

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        return self._session._fail_active_turn(context)


@contextlib.asynccontextmanager
async def _background_heartbeats(context: AsyncContext) -> AsyncIterator[None]:
    heartbeats = asyncio.create_task(_record_heartbeats(context))
    try:
        yield
    finally:
        heartbeats.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await heartbeats


async def _record_heartbeats(context: AsyncContext) -> None:
    while True:
        await asyncio.sleep(BACKGROUND_HEARTBEAT_INTERVAL.total_seconds())
        await context.heartbeat()


def _finish_tool_call(
    context: AsyncContext,
    finished: AgentToolCall,
    events: AgentEventWriter,
) -> StepDecision:
    AGENT_TOOL_CALLS.set(context, finished.tool_call_id, finished)
    TOOL_CALL_RESULTS.publish(context, ToolCallCompleted(tool_call_id=finished.tool_call_id))
    events.write_tool_call_finished(finished.tool_call_id, finished.tool_name, finished.status)
    return dead_end()


def _append_messages(
    context: Context,
    state: AgentState,
    turn_id: str,
    messages: Sequence[FrameworkMessage],
) -> AgentState:
    sequence = state.next_message_sequence
    created_at = datetime.now(UTC).isoformat()
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
