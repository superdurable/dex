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
``ask_user`` and ``AwaitTimer`` for ``durable_wait``. ``RunModel`` records a
``write_todos`` call in its own commit, and ``execute_plan`` starts a turn that
runs an approved plan from ``AwaitUserMessage``. Steering ends a turn at its
next safe Step boundary through ``ApplyTurnControl``. ``cancel_turn`` also
cancels the running model call and the Steps that wait, and
``FinishTurnCancellation`` decides from committed state how the turn ends. A
tool that starts remote work returns ``ToolTaskStarted``, and ``PollToolTask``
polls it on a Timer; ``cancel_turn`` abandons it through ``CancelToolTask``.
With a compaction policy, ``CompactContext`` summarizes earlier messages before
a model call whose input would cross the threshold.
"""

from __future__ import annotations

import asyncio
import bisect
import contextlib
import json
import logging
import math
from collections.abc import AsyncIterator, Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field, replace
from datetime import UTC, datetime, timedelta
from typing import Any, cast

from dex import (
    AsyncContext,
    AttributeMap,
    AttributeMapLoad,
    Channel,
    CodecRegistry,
    Context,
    Flow,
    PersistenceSchema,
    RecoveryErrorInfo,
    RetryPolicy,
    RPCResult,
    Step,
    StepAttemptProgress,
    StepDecision,
    StepDurability,
    StepList,
    StepMovement,
    StepOptions,
    Stream,
    Timer,
    Wait,
    dead_end,
    force_fail,
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
    WRITE_TODOS_CALLED_MORE_THAN_ONCE,
    BuiltInTool,
    BuiltInToolArgumentsError,
    build_durable_wait_completed_output,
    build_plan_cleared_output,
    build_plan_updated_output,
    build_question_answered_output,
    parse_ask_user_arguments,
    parse_durable_wait_arguments,
    parse_write_todos_arguments,
)
from dex_agents.compaction import (
    CompactionPolicy,
    ContextCompactionRunner,
    ContextSummaryRequest,
    ContextSummaryResult,
)
from dex_agents.events import AgentEventType, AgentEventWriter
from dex_agents.persistence import (
    AGENT_MESSAGE_CHUNKS,
    AGENT_MESSAGE_INDEX,
    AGENT_MESSAGES,
    AGENT_PLAN,
    AGENT_STATE,
    AGENT_TOOL_CALLS,
    AGENT_TRACE_CONTEXT,
    AGENT_USAGE,
    CONTEXT_SUMMARY,
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    DEFAULT_MESSAGE_CHUNK_MAX_BYTES,
    DEFAULT_MESSAGE_CHUNK_SIZE,
    DEFAULT_MESSAGE_WINDOW_SIZE,
    DEFAULT_RETAINED_MESSAGE_LIMIT,
    FRAMEWORK_RUN_STATE,
    INTERRUPT_RESOLUTIONS,
    MESSAGE_ROLLOVER_MAX_BYTES_PER_COMMIT,
    MESSAGE_ROLLOVER_MAX_CHUNKS_PER_COMMIT,
    PENDING_INTERRUPTS,
    PENDING_PLAN_EXECUTION,
    PLAN_EXECUTIONS,
    QUEUED_USER_MESSAGES,
    SESSION_SETTINGS,
    STEERED_USER_MESSAGES,
    TOOL_CALL_CANCELLATIONS,
    TOOL_CALL_RESULTS,
    TURN_CANCELLATION_REQUEST,
    TURN_CONTROL_REQUESTS,
    TURN_REPLY_SEQUENCE,
    TURN_SESSION_SETTINGS,
    message_sequence_key,
    plan_execution_key,
)
from dex_agents.plans import (
    PLANNING_TURN_TOOL_REJECTION,
    build_plan_execution_message,
    plan_status_after_write,
)
from dex_agents.runner import (
    APPROVAL_DEADLINE_PASSED,
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
    TOOL_CALL_NOT_RUN_ERROR_PREFIX,
    TOOL_TASK_ABANDONED_ERROR,
    TURN_CANCELLED_NOTE_HEADER,
    TURN_STEERED_NOTE_HEADER,
    AgentFrameworkRunner,
    AgentTurnContext,
    DeferredToolExecution,
    DeferredToolOutcome,
    FrameworkMessage,
    PlanModeRunner,
    ToolCallsRequested,
    ToolExecutionError,
    ToolExecutionResult,
    ToolInputRequested,
    ToolPolicy,
    ToolTaskCancellation,
    ToolTaskCanceller,
    ToolTaskPending,
    ToolTaskPoll,
    ToolTaskPoller,
    ToolTaskPollOutcome,
    ToolTaskStarted,
    TurnCompleted,
    UnfinishedTurnMessageBuilder,
    build_tool_call_note,
    build_turn_failure_note,
)
from dex_agents.session_settings import check_stored_session_settings
from dex_agents.spend_limit import (
    MODEL_CALL_REFUSED_ERROR_PREFIX,
    SPEND_LIMIT_CALL_TIMEOUT,
    ModelCallCheck,
    ModelCallDecision,
    ModelSpendLimit,
    ModelUsageReport,
)
from dex_agents.tracing import AgentTracingOptions
from dex_agents.values import (
    AgentEvent,
    AgentInterrupt,
    AgentInterruptResolution,
    AgentMessage,
    AgentMessageChunk,
    AgentMessageChunkRange,
    AgentMessageIndex,
    AgentMessagesPage,
    AgentPlan,
    AgentSessionStartInput,
    AgentSessionStatus,
    AgentSnapshot,
    AgentState,
    AgentToolCall,
    AgentUsage,
    AgentUserMessage,
    AnswerQuestionRequest,
    CancelTurnRequest,
    CompactContextInput,
    ContextSummary,
    DeleteQueuedMessageRequest,
    ExecutePlanRequest,
    InteractionMode,
    InterruptKind,
    ListAgentMessagesRequest,
    MessageRole,
    PendingUserMessage,
    PlanExecutionRequest,
    PlanStatus,
    QuestionAnswer,
    QuestionResolution,
    ResolveInterruptRequest,
    SteerUserMessageRequest,
    ToolCallCancellation,
    ToolCallCancellationReason,
    ToolCallCompleted,
    ToolCallStatus,
    ToolEffect,
    TurnCancellationRequest,
    TurnControlKind,
    TurnControlRequest,
    TurnReplySequence,
    TurnSessionSettings,
    UpdateSessionSettingsRequest,
)

_LOGGER = logging.getLogger(__name__)
MISSING_UNFINISHED_TURN_MESSAGE_BUILDER = (
    "a runner that sets ToolCallsRequested.new_messages must implement "
    "build_unfinished_turn_messages"
)
#: Error recorded when a tool returns ``ToolTaskStarted`` to a runner that cannot poll it.
MISSING_TOOL_TASK_POLLER = (
    "the tool started a task, but the runner does not implement poll_tool_task"
)
#: Start of the Flow failure reason when ``StartAgentSession`` refuses the start input's settings.
INVALID_START_SESSION_SETTINGS_REASON = (
    "dex-agents: the start input's session_settings are not valid session settings"
)
#: Error recorded when ``ToolTaskStarted`` has a blank ``task_id``.
BLANK_TOOL_TASK_ID = "the tool started a task without a task ID"
#: Error recorded when a task's poll interval is not a number.
INVALID_TOOL_TASK_POLL_INTERVAL = "the tool task's poll interval is not a number"
# Longest wait between two polls of a tool task; a longer interval is shortened to it.
_MAXIMUM_TOOL_TASK_POLL_INTERVAL_SECONDS = 86_400
# server-zd reports a Step whose commit would pass limits.maxFlowStateBytes with this type and
# text: "run state of <X> bytes exceeds the maximum of <Y> bytes", or "active run state of ..."
# since the limit counts only active state.
STATE_LIMIT_ERROR_TYPE = "ValueTooLarge"
STATE_LIMIT_ERROR_TEXT = "run state of "
SNAPSHOT_MESSAGE_LIMIT = 200
MESSAGE_PAGE_LIMIT = 200
# Keeps liveness well inside the shortest heartbeat timeout, one minute.
BACKGROUND_HEARTBEAT_INTERVAL = timedelta(seconds=20)
RECOVERY_RETRY = RetryPolicy(maximum_interval=timedelta(seconds=5))
# Dex takes Step durations as a signed 32-bit number of seconds.
MAX_STEP_DURATION_SECONDS = 2_147_483_647
_MODEL_CALL_STEP_OPTIONS = StepOptions(
    execute_method_timeout=timedelta(minutes=15),
    heartbeat_timeout=timedelta(minutes=2),
    execute_retry=RetryPolicy(maximum_attempts=3, total_duration=timedelta(minutes=30)),
    execute_durability=StepDurability.SYNC,
)
MULTI_ATTEMPT_TOOL_EFFECTS = (ToolEffect.READ, ToolEffect.IDEMPOTENT_WRITE)
BUILT_IN_TOOL_POLICY = ToolPolicy(ToolEffect.READ, requires_approval=False)
EXECUTABLE_PLAN_STATUSES = (PlanStatus.DRAFT, PlanStatus.ACTIVE)
# Consumes every pending message of a Channel at once; the plan's MAX.
ALL_PENDING_MESSAGES = 2_147_483_647
TOOL_CALL_CANCELLATION_RESULTS = {
    ToolCallCancellationReason.STEERED: TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    ToolCallCancellationReason.TURN_CANCELLED: TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
}
_NO_PENDING_TOOL_QUESTION: dict[str, Any] = {
    "input_question": None,
    "input_choices": [],
    "input_allows_free_text": True,
}


@dataclass(frozen=True, kw_only=True)
class RunModelLimit:
    """Set how long one ``RunModel`` Step may run and how often it runs again.

    The defaults fit one model call. A harness runner, such as the Claude Agent
    SDK adapter, runs a whole turn segment in one ``RunModel``: several model
    calls and inline tool calls. Raise ``method_timeout`` when a segment needs
    longer than 15 minutes.

    Background heartbeats continue through the Step, so a long silent call
    stays within the heartbeat timeout and ``method_timeout`` is its only cap.
    Dex ends an attempt at ``method_timeout`` and cancels its handler. The next
    attempt reruns the whole segment and pays for it again, so consider
    ``maximum_attempts=1`` for an expensive segment. A hung call holds its
    Worker slot until ``method_timeout``, so do not set it higher than needed.

    ``CompactContext`` keeps the defaults, because it makes one model call.

    A changed limit applies to each ``RunModel`` scheduled after the Worker
    restarts. An attempt in flight, and its retries, keep their limit.

    Example:
        >>> options = AgentSessionOptions(
        ...     run_model_limit=RunModelLimit(
        ...         method_timeout=timedelta(hours=1), maximum_attempts=2
        ...     ),
        ... )

    .. versionadded:: 0.3.0

    Attributes:
        method_timeout: Longest one attempt may run, in whole seconds. Defaults
            to 15 minutes.
        maximum_attempts: Attempts, including the first. ``1`` disables
            retries. Defaults to 3.

    Raises:
        ValueError: If ``method_timeout`` is not a whole number of seconds of at
            least one, if ``maximum_attempts`` is not positive, or if
            ``total_duration`` does not fit a signed 32-bit number of seconds.
    """

    method_timeout: timedelta = timedelta(minutes=15)
    maximum_attempts: int = 3

    def __post_init__(self) -> None:
        if self.method_timeout < timedelta(seconds=1) or self.method_timeout.microseconds:
            raise ValueError("method_timeout must be a whole number of seconds, at least one")
        if self.maximum_attempts < 1:
            raise ValueError("maximum_attempts must be positive")
        if self.total_duration.total_seconds() > MAX_STEP_DURATION_SECONDS:
            raise ValueError("method_timeout times maximum_attempts is too long for Dex")

    @property
    def total_duration(self) -> timedelta:
        """Return the retry budget, which moves with the other two values.

        Dex checks it only after an attempt fails, and never ends a running
        attempt. So it must exceed ``method_timeout`` for a timed-out attempt to
        retry at all.

        Returns:
            ``method_timeout`` times one less than ``maximum_attempts``, and at
            least twice ``method_timeout``. The default limit gives 30 minutes.
        """
        return self.method_timeout * max(self.maximum_attempts - 1, 2)


@dataclass(frozen=True, kw_only=True)
class AgentSessionOptions:
    """Configure tool policies, built-in tools, live events, deadlines, tracing, and compaction.

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

    Plan mode needs ``write_todos`` and a ``PlanModeRunner``, such as the
    Stepwise runner:

        >>> options = AgentSessionOptions(
        ...     built_in_tools=frozenset({BuiltInTool.WRITE_TODOS}),
        ...     plan_mode_enabled=True,
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
            their calls to ``AwaitQuestionAnswer`` and ``AwaitTimer``, or
            records a ``write_todos`` call in ``RunModel``, with no approval
            and the ``read`` effect. An enabled name is reserved for the
            built-in. Defaults to none.
        max_durable_wait: Longest ``durable_wait`` the session accepts, in
            whole seconds. A longer request fails at once with a message the
            model sees. Defaults to 365 days.
        tracing: OpenTelemetry GenAI span options. The default emits spans
            through the global tracer provider when the OpenTelemetry API is
            installed, and nothing otherwise.
        plan_mode_enabled: Whether ``send_user_message`` accepts a message with
            ``plan_mode``, which starts a ``PLANNING`` turn that drafts a plan
            for ``execute_plan``. A ``DRAFT`` plan makes every new turn a
            ``PLANNING`` turn until it runs. Requires ``write_todos`` in
            ``built_in_tools`` and a runner that is a ``PlanModeRunner``.
            Defaults to ``False``.
        compaction: When to summarize earlier messages so the model input
            stays under the context window. It needs a runner that implements
            ``ContextCompactionRunner``. Summarized messages stay stored until
            retention deletes them; see ``retained_message_limit``. Enable it
            only after every Worker of the Flow type runs a version with the
            ``CompactContext`` Step. ``None``, the default, never compacts.
        message_window_size: Most messages the ``AgentMessages`` window holds.
            When a Step stores a message past it, the window's oldest messages
            move into immutable ``AgentMessageChunks`` instances in the same
            commit, at most four chunks and 8 MiB per commit. Every Step that
            stores messages loads the whole window, so it bounds that load.
            Defaults to 64.
        message_chunk_size: Most messages in one archive chunk. Defaults to 32.
        message_chunk_max_bytes: Most encoded bytes in one archive chunk, so a
            chunk stays well under the Server's ``values.maxValueBytes``. A
            larger message gets a chunk of its own, 15 bytes larger than the
            message. Defaults to 1 MiB; at most 8 MiB.
        retained_message_limit: With ``compaction``, how many stored messages
            retention always keeps. Retention deletes the oldest chunk when
            every message in it is summarized, at or before
            ``ContextSummary.last_sequence``, and at least this many messages
            are stored after it. The newest ``retained_message_limit``
            messages and every message after the summary always stay. Deleted
            messages leave ``list_agent_messages`` and ``get_agent_snapshot``,
            so copy them out first if the application needs the full
            transcript. ``None`` keeps every message. Defaults to 1,000.
            Without ``compaction`` nothing is deleted.
        run_model_limit: Method timeout and attempts of ``RunModel``, for a
            runner whose ``RunModel`` takes longer than one model call. Defaults
            to ``RunModelLimit()``: 15 minutes, 3 attempts in 30 minutes.
        spend_limit: The application's ``ModelSpendLimit``, called before and
            after each model call of ``RunModel`` and ``CompactContext``.
            ``None``, the default, makes no checks.

    Raises:
        ValueError: If ``event_stream_capacity_bytes`` or ``interrupt_deadline``
            is not positive, if ``max_durable_wait`` is shorter than one second
            or not whole seconds, if ``built_in_tools`` names an unknown tool,
            if ``tool_policies`` names an enabled built-in tool, or if
            ``plan_mode_enabled`` is set without ``write_todos``, if
            ``message_chunk_size`` is not positive, if
            ``message_window_size`` is smaller than ``message_chunk_size``, if
            ``message_chunk_max_bytes`` is not between 1 and 8 MiB, or if
            ``retained_message_limit`` is negative.
    """

    tool_policies: Mapping[str, ToolPolicy] = field(default_factory=dict)
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES
    interrupt_deadline: timedelta | None = None
    tracing: AgentTracingOptions = field(default_factory=AgentTracingOptions)
    built_in_tools: frozenset[BuiltInTool] = frozenset()
    max_durable_wait: timedelta = DEFAULT_MAX_DURABLE_WAIT
    plan_mode_enabled: bool = False
    compaction: CompactionPolicy | None = None
    message_window_size: int = DEFAULT_MESSAGE_WINDOW_SIZE
    message_chunk_size: int = DEFAULT_MESSAGE_CHUNK_SIZE
    message_chunk_max_bytes: int = DEFAULT_MESSAGE_CHUNK_MAX_BYTES
    retained_message_limit: int | None = DEFAULT_RETAINED_MESSAGE_LIMIT
    run_model_limit: RunModelLimit = field(default_factory=RunModelLimit)
    spend_limit: ModelSpendLimit | None = None

    def __post_init__(self) -> None:
        if self.message_chunk_size <= 0:
            raise ValueError("message_chunk_size must be positive")
        if self.message_window_size < self.message_chunk_size:
            raise ValueError("message_window_size must be at least message_chunk_size")
        if not 1 <= self.message_chunk_max_bytes <= MESSAGE_ROLLOVER_MAX_BYTES_PER_COMMIT:
            raise ValueError("message_chunk_max_bytes must be between 1 and 8388608")
        if self.retained_message_limit is not None and self.retained_message_limit < 0:
            raise ValueError("retained_message_limit must not be negative")
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
        if self.plan_mode_enabled and BuiltInTool.WRITE_TODOS not in built_in_tools:
            raise ValueError("plan_mode_enabled requires the write_todos built-in tool")
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

    ``steer_user_message`` ends the running turn at its next safe Step
    boundary. It never interrupts a model call or a tool call that has already
    started. ``cancel_turn`` stops the running model call and every wait for a
    person, a Timer, or a remote task too, but a tool call Step that has
    started still runs to the end. The calls that have not started record
    ``REJECTED`` with a ``cancellation_reason``, an abandoned task records
    ``OUTCOME_UNKNOWN``, and ``ApplyTurnControl`` ends the turn. A steered
    turn is followed by a new turn with the steered messages.

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
        agent_messages: The ``AgentMessages`` AttributeMap: the newest
            messages, one instance per message.
        agent_message_chunks: The ``AgentMessageChunks`` AttributeMap: older
            messages in immutable chunks.
        agent_message_index: The ``AgentMessageIndex`` Attribute: where the
            window starts and which chunks are kept.
        turn_reply_sequence: The ``TurnReplySequence`` Attribute: where the
            latest turn's replies start.
        framework_run_state: The ``FrameworkRunState`` Attribute.
        agent_tool_calls: The ``AgentToolCalls`` AttributeMap.
        pending_interrupts: The ``PendingInterrupts`` Attribute.
        agent_usage: The ``AgentUsage`` Attribute.
        queued_user_messages: The ``QueuedUserMessages`` Channel.
        interrupt_resolutions: The ``InterruptResolutions`` ChannelMap.
        tool_call_results: The internal ``ToolCallResults`` Channel.
        steered_user_messages: The ``SteeredUserMessages`` Channel.
        turn_control_requests: The internal ``TurnControlRequests`` Channel.
        turn_cancellation_request: The ``TurnCancellationRequest`` Attribute.
        tool_call_cancellations: The internal ``ToolCallCancellations`` ChannelMap.
        agent_trace_context: The ``AgentTraceContext`` Attribute: the W3C
            ``traceparent`` of the current activation's root span, set when
            that span has a valid context, sampled or not.
        agent_plan: The ``AgentPlan`` Attribute.
        plan_executions: The ``PlanExecutions`` ChannelMap, keyed by revision.
        pending_plan_execution: The ``PendingPlanExecution`` Attribute.
        context_summary: The ``ContextSummary`` Attribute, set once the
            session compacts.
        session_settings: The ``SessionSettings`` Attribute: the settings
            the next turn starts with.
        turn_session_settings: The ``TurnSessionSettings`` Attribute: the
            settings the active turn started with.
        agent_events: This Flow type's ``AgentEvents`` Stream. Pass it to
            ``AsyncClient.read_stream`` or ``AsyncClient.list_stream_messages``.
    """

    agent_state = AGENT_STATE
    agent_messages = AGENT_MESSAGES
    agent_message_chunks = AGENT_MESSAGE_CHUNKS
    agent_message_index = AGENT_MESSAGE_INDEX
    turn_reply_sequence = TURN_REPLY_SEQUENCE
    framework_run_state = FRAMEWORK_RUN_STATE
    agent_tool_calls = AGENT_TOOL_CALLS
    pending_interrupts = PENDING_INTERRUPTS
    agent_usage = AGENT_USAGE
    queued_user_messages = QUEUED_USER_MESSAGES
    interrupt_resolutions = INTERRUPT_RESOLUTIONS
    tool_call_results = TOOL_CALL_RESULTS
    steered_user_messages = STEERED_USER_MESSAGES
    turn_control_requests = TURN_CONTROL_REQUESTS
    turn_cancellation_request = TURN_CANCELLATION_REQUEST
    tool_call_cancellations = TOOL_CALL_CANCELLATIONS
    agent_trace_context = AGENT_TRACE_CONTEXT
    agent_plan = AGENT_PLAN
    plan_executions = PLAN_EXECUTIONS
    pending_plan_execution = PENDING_PLAN_EXECUTION
    context_summary = CONTEXT_SUMMARY
    session_settings = SESSION_SETTINGS
    turn_session_settings = TURN_SESSION_SETTINGS

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
            ValueError: If ``flow_type`` is empty or whitespace-only, or if
                ``options.plan_mode_enabled`` is set for a runner that is not a
                ``PlanModeRunner``.
            TypeError: If ``options.compaction`` is set and the runner does not
                implement ``ContextCompactionRunner``.
        """
        if not flow_type.strip():
            raise ValueError("flow_type must not be empty")
        self._runner = runner
        self._flow_type = flow_type
        self._options = options or AgentSessionOptions()
        if self._options.plan_mode_enabled and not _is_plan_mode_runner(runner):
            raise ValueError("plan_mode_enabled requires a runner that supports plan mode")
        if self._options.compaction is not None and not isinstance(runner, ContextCompactionRunner):
            raise TypeError(
                "AgentSessionOptions.compaction needs a runner that implements "
                "ContextCompactionRunner"
            )
        self._message_storage = _MessageStorage(
            window_size=self._options.message_window_size,
            chunk_size=self._options.message_chunk_size,
            chunk_max_bytes=self._options.message_chunk_max_bytes,
            retained_message_limit=(
                None if self._options.compaction is None else self._options.retained_message_limit
            ),
        )
        self._session_tracer = AgentSessionTracer(self._options.tracing, flow_type)
        self.agent_events: Stream[AgentEvent] = Stream(
            "AgentEvents", AgentEvent, self._options.event_stream_capacity_bytes
        )
        self._start_agent_session = StartAgentSession(self)
        self._await_user_message = AwaitUserMessage(self)
        self._run_model = RunModel(self)
        self._compact_context = CompactContext(self)
        self._await_tool_approval = AwaitToolApproval(self)
        self._await_question_answer = AwaitQuestionAnswer(self)
        self._await_timer = AwaitTimer(self)
        self._call_tool = CallTool(self)
        self._call_single_attempt_tool = CallSingleAttemptTool(self)
        self._poll_tool_task = PollToolTask(self)
        self._record_tool_failure = RecordToolFailure(self)
        self._record_tool_outcome_unknown = RecordToolOutcomeUnknown(self)
        self._resume_turn = ResumeTurn(self)
        self._apply_turn_control = ApplyTurnControl(self)
        self._record_turn_failure = RecordTurnFailure(self)
        self._finish_turn_cancellation = FinishTurnCancellation(self)
        self._cancel_tool_task = CancelToolTask(self)

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
            self._compact_context,
            self._await_tool_approval,
            self._await_question_answer,
            self._await_timer,
            self._call_tool,
            self._call_single_attempt_tool,
            self._poll_tool_task,
            self._record_tool_failure,
            self._record_tool_outcome_unknown,
            self._resume_turn,
            self._apply_turn_control,
            self._record_turn_failure,
            self._finish_turn_cancellation,
            self._cancel_tool_task,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        """Return the session Attributes, Channels, and ``AgentEvents`` Stream.

        ``AgentTraceContext`` and ``ContextSummary`` are always registered, so
        Workers with and without tracing or compaction can serve the same Flow
        type. ``SessionSettings`` and ``TurnSessionSettings`` hold the
        per-session settings.

        Returns:
            The persistence schema shared by every agent session Flow type.
        """
        return PersistenceSchema.of(
            AGENT_STATE,
            AGENT_MESSAGES,
            AGENT_MESSAGE_CHUNKS,
            AGENT_MESSAGE_INDEX,
            TURN_REPLY_SEQUENCE,
            FRAMEWORK_RUN_STATE,
            AGENT_TOOL_CALLS,
            PENDING_INTERRUPTS,
            AGENT_USAGE,
            QUEUED_USER_MESSAGES,
            INTERRUPT_RESOLUTIONS,
            TOOL_CALL_RESULTS,
            STEERED_USER_MESSAGES,
            TURN_CONTROL_REQUESTS,
            TURN_CANCELLATION_REQUEST,
            TOOL_CALL_CANCELLATIONS,
            AGENT_TRACE_CONTEXT,
            AGENT_PLAN,
            PLAN_EXECUTIONS,
            PENDING_PLAN_EXECUTION,
            CONTEXT_SUMMARY,
            SESSION_SETTINGS,
            TURN_SESSION_SETTINGS,
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
            ``True`` when the message was queued; ``False`` for whitespace-only
            text, or for a ``plan_mode`` message when
            ``AgentSessionOptions.plan_mode_enabled`` is not set.
        """
        if not input.text.strip() or (input.plan_mode and not self._options.plan_mode_enabled):
            return RPCResult(False)
        QUEUED_USER_MESSAGES.publish(context, input)
        return RPCResult(True)

    @rpc(lock_attributes=(PENDING_PLAN_EXECUTION.lock(),))
    def execute_plan(self, context: Context, input: ExecutePlanRequest) -> RPCResult[bool]:
        """Approve one plan revision, so the session starts a turn that runs it.

        Call it after the user reviewed the ``DRAFT`` plan that a ``plan_mode``
        message drafted, or to run an ``ACTIVE`` plan again. The next
        ``AwaitUserMessage`` starts an ``EXECUTING`` turn with every tool,
        whose user message is ``[Dex] The user approved plan revision <n>.
        Execute it and keep the task statuses current with write_todos.``. A
        queued message that the session took first runs its turn first; when
        that turn changes the plan, the approval is stale and never runs.

        The RPC records the approval in ``PendingPlanExecution`` until the
        session starts the turn, so a revision runs once per approval. It
        locks ``PendingPlanExecution``: a concurrent ``execute_plan`` can make
        it raise ``RpcLockConflictError``, which is safe to retry.

        Args:
            context: RPC Context supplied by Dex.
            input: The plan revision the user reviewed.

        Returns:
            ``True`` when the execution is accepted. ``False`` unless the
            session waits for a user message, the plan has this revision and is
            ``DRAFT`` or ``ACTIVE``, no execution of it is pending, and no
            queued or steered message or interrupt is pending.
        """
        state = _optional_agent_state(context)
        plan = _optional_agent_plan(context)
        pending = cast(PlanExecutionRequest | None, PENDING_PLAN_EXECUTION.get(context))
        if (
            state is None
            or state.status is not AgentSessionStatus.WAITING_FOR_USER_MESSAGE
            or state.active_turn_id is not None
            or plan is None
            or plan.revision != input.revision
            or plan.status not in EXECUTABLE_PLAN_STATUSES
            or (pending is not None and pending.revision == plan.revision)
            or QUEUED_USER_MESSAGES.size(context) > 0
            or STEERED_USER_MESSAGES.size(context) > 0
            or _pending_interrupts(context)
        ):
            return RPCResult(False)
        request = PlanExecutionRequest(revision=plan.revision)
        PENDING_PLAN_EXECUTION.set(context, request)
        PLAN_EXECUTIONS.publish(context, plan_execution_key(plan.revision), request)
        return RPCResult(True)

    @rpc(is_transactional=True, load_channels=(QUEUED_USER_MESSAGES,))
    def steer_user_message(
        self,
        context: Context,
        input: SteerUserMessageRequest,
    ) -> RPCResult[bool]:
        """Steer the running turn with a new message or with a queued one.

        A steered message applies at the turn's next safe Step boundary. The
        session never interrupts a model call or a tool call that has started.
        It records the calls that have not started as ``REJECTED`` with
        ``cancellation_reason`` ``STEERED``, ends the turn, and starts a new
        turn whose user messages are every steered message, in order.

        While no turn is active, there is nothing to steer: new ``text`` is
        queued like ``send_user_message``, so it starts the next turn at once,
        and a ``message_id`` that is still queued stays queued. A turn that a
        Worker without steering started or advanced cannot be steered; see
        ``AgentState.accepts_turn_control``.

        With ``message_id``, the RPC moves that queued message: it deletes it
        from ``QueuedUserMessages`` and publishes it to ``SteeredUserMessages``
        in one commit. It locks nothing. When another Step or RPC consumes the
        message between the RPC's read and its commit, the RPC raises
        ``ChannelMessageNotFoundError`` and commits nothing; reload the
        snapshot and let the user choose again.

        Example:
            >>> snapshot = await sessions.get_agent_snapshot("session-42")
            >>> await client.invoke_rpc(flow.steer_user_message, "session-42",
            ...     SteerUserMessageRequest(message_id=snapshot.queued_messages[0].message_id))

        Args:
            context: RPC Context supplied by Dex.
            input: Exactly one of a queued message ID and new text.

        Returns:
            ``True`` when the message is steered, or queued while no turn is
            active; ``False`` when both or neither field is set, the text is
            blank, the message is no longer queued, or the active turn does
            not accept steering.
        """
        if (input.message_id is None) == (input.text is None):
            return RPCResult(False)
        state = _optional_agent_state(context)
        if state is None or state.active_turn_id is None:
            # Any Worker version waits for queued messages while idle; a steer might wait forever.
            if input.text is None:
                return RPCResult(_find_queued_message(context, input.message_id or "") is not None)
            return self.send_user_message(context, AgentUserMessage(text=input.text))
        if not state.accepts_turn_control:
            return RPCResult(False)
        if input.text is not None:
            if not input.text.strip():
                return RPCResult(False)
            steered = AgentUserMessage(text=input.text)
        else:
            queued = _find_queued_message(context, input.message_id or "")
            if queued is None:
                return RPCResult(False)
            QUEUED_USER_MESSAGES.delete(context, queued.message_id)
            steered = queued.message
        STEERED_USER_MESSAGES.publish(context, steered)
        TURN_CONTROL_REQUESTS.publish(context, TurnControlRequest(kind=TurnControlKind.STEER))
        return RPCResult(True)

    @rpc(is_transactional=True, load_channels=(QUEUED_USER_MESSAGES,))
    def delete_queued_message(
        self,
        context: Context,
        input: DeleteQueuedMessageRequest,
    ) -> RPCResult[bool]:
        """Delete one queued user message before it starts a turn.

        The RPC locks nothing. When the session consumes the message between
        the RPC's read and its commit, the RPC raises
        ``ChannelMessageNotFoundError`` and commits nothing.

        Args:
            context: RPC Context supplied by Dex.
            input: ``PendingUserMessage.message_id`` from ``get_agent_snapshot``.

        Returns:
            ``True`` when the message was deleted; ``False`` when it is no
            longer queued.
        """
        queued = _find_queued_message(context, input.message_id)
        if queued is None:
            return RPCResult(False)
        QUEUED_USER_MESSAGES.delete(context, queued.message_id)
        return RPCResult(True)

    @rpc(is_transactional=True)
    def update_session_settings(
        self,
        context: Context,
        input: UpdateSessionSettingsRequest,
    ) -> RPCResult[bool]:
        """Replace the session's settings from the next turn on.

        The new settings apply when the next turn starts, the same boundary
        at which a ``plan_mode`` message takes effect. A running turn keeps
        the settings it started with for every model call, retry, and
        compaction, so one turn never mixes two models. The settings replace
        the old ones; nothing is merged, and ``{}`` clears them, so
        ``get_agent_snapshot`` returns ``None``.

        Settings are stored in the session and returned by
        ``get_agent_snapshot``, and the browser snapshot JSON of
        ``dex_agents.ui_stream`` includes them. Never put a secret or anything
        a browser must not see in them; put a reference, such as a credential
        name, that the Worker resolves. Call the RPC only after every Worker
        of the Flow type runs dex-agents 0.2.0.

        The RPC locks nothing. When two updates race, the one that commits
        last wins. A turn that starts between this RPC's read and its commit
        runs with the earlier settings, and the next turn gets these.

        Example:
            >>> await client.invoke_rpc(flow.update_session_settings, "session-42",
            ...     UpdateSessionSettingsRequest(session_settings={"model": "gpt-5"}))

        Args:
            context: RPC Context supplied by Dex.
            input: The complete new settings.

        Returns:
            ``True`` when the settings are stored; ``False`` before
            ``StartAgentSession`` committed, or when the settings are ``None``
            (a missing or ``null`` field), are not a JSON object, or are larger
            than ``MAX_SESSION_SETTINGS_BYTES`` plus
            ``SESSION_SETTINGS_SIZE_TOLERANCE_BYTES``.
        """
        if _optional_agent_state(context) is None or input.session_settings is None:
            return RPCResult(False)
        try:
            check_stored_session_settings(input.session_settings)
        except ValueError:
            return RPCResult(False)
        if input.session_settings:
            SESSION_SETTINGS.set(context, input.session_settings)
        elif _session_settings(context) is not None:
            SESSION_SETTINGS.delete(context)
        return RPCResult(True)

    # PendingInterrupts orders it with ResumeTurn, and TurnCancellationRequest with turn starts.
    @rpc(lock_attributes=(TURN_CANCELLATION_REQUEST.lock(), PENDING_INTERRUPTS.lock()))
    def cancel_turn(self, context: Context, input: CancelTurnRequest) -> RPCResult[bool]:
        """Stop the running turn now, with no new turn.

        The RPC cancels the turn's running model call or compaction, and every
        Step that waits for an approval, an answer, a ``durable_wait`` Timer,
        or a remote task's next poll. A cancelled model call stops at once and
        its answer is dropped. A tool call Step that has started runs to the
        end, and the turn ends after it. The calls that have not started
        record ``REJECTED`` with ``cancellation_reason`` ``TURN_CANCELLED``. A
        started remote task is abandoned: the call records ``OUTCOME_UNKNOWN``,
        and a runner that implements ``ToolTaskCanceller`` is asked to cancel
        the task. Then the session waits for the next user message.

        The RPC locks ``TurnCancellationRequest`` and ``PendingInterrupts``.
        No long-running Step locks them, so it never waits for a model call.
        It can raise ``RpcLockConflictError`` while a short Step holds a lock,
        which is safe to retry; ``AgentSessionClient.cancel_turn`` retries it.
        A second call for the same turn changes nothing and returns ``True``.

        Args:
            context: RPC Context supplied by Dex.
            input: The turn to stop, or ``None`` for the active turn.

        Returns:
            ``True`` when the request is recorded, although a turn whose model
            call already committed its final answer completes instead;
            ``False`` when no turn is active, ``turn_id`` names another turn, or
            the active turn does not accept cancellation.
        """
        state = _optional_agent_state(context)
        if state is None or state.active_turn_id is None or not state.accepts_turn_control:
            return RPCResult(False)
        turn_id = state.active_turn_id
        if input.turn_id is not None and input.turn_id != turn_id:
            return RPCResult(False)
        if _turn_control_reason(context, turn_id) is ToolCallCancellationReason.TURN_CANCELLED:
            return RPCResult(True)
        TURN_CANCELLATION_REQUEST.set(
            context,
            TurnCancellationRequest(turn_id=turn_id, requested_at=_rfc3339(datetime.now(UTC))),
        )
        # Each cancelled Step has no outside effect or only waits; the model call's answer is lost.
        return RPCResult(
            True,
            next_steps=(StepMovement.of(self._finish_turn_cancellation, turn_id),),
        ).with_canceling_steps(
            self._run_model,
            self._compact_context,
            self._record_turn_failure,
            self._await_tool_approval,
            self._await_question_answer,
            self._await_timer,
            self._poll_tool_task,
        )

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
        make it raise ``RpcLockConflictError``, which is safe to retry. The
        caller sends the RPC's locks: SDK Clients and Dex Web do, but a raw
        ``InvokeRPC`` request without them runs unlocked, and then more than one
        call can succeed.

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

    @rpc(
        load_attribute_maps=(AGENT_MESSAGES, AGENT_MESSAGE_CHUNKS, AGENT_TOOL_CALLS),
        load_channels=(QUEUED_USER_MESSAGES, STEERED_USER_MESSAGES),
    )
    def get_agent_snapshot(self, context: Context) -> RPCResult[AgentSnapshot]:
        """Return the session state a UI renders.

        The RPC reads committed state without locks, so it also serves a
        retained closed session. It loads the message window and every
        retained chunk.

        Args:
            context: RPC Context supplied by Dex.

        Returns:
            State, the last 200 stored messages, pending interrupts, the active
            turn's tool calls, usage, the queued message count, the plan, the
            queued and steered messages with their Channel message IDs, the
            context summary, and the settings the next turn starts with.
            Summarized messages stay in ``messages`` until retention deletes
            them.
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
                plan=_optional_agent_plan(context),
                queued_messages=_pending_user_messages(context, QUEUED_USER_MESSAGES),
                steered_messages=_pending_user_messages(context, STEERED_USER_MESSAGES),
                context_summary=_context_summary(context),
                session_settings=_session_settings(context),
            )
        )

    @rpc(load_attribute_maps=(AGENT_MESSAGES, AGENT_MESSAGE_CHUNKS))
    def list_agent_messages(
        self,
        context: Context,
        input: ListAgentMessagesRequest,
    ) -> RPCResult[AgentMessagesPage]:
        """Return one page of stored messages, oldest first.

        Start without ``before_sequence`` for the newest page, then pass each
        page's ``next_before_sequence`` to walk back. The RPC reads committed
        state without locks, so it also serves a retained closed session.
        Paging ends at the oldest stored message: retention may have deleted
        earlier ones.

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
        first_sequence = _first_stored_sequence(AGENT_MESSAGE_INDEX.get(context))
        end = state.next_message_sequence
        if input.before_sequence is not None:
            end = max(first_sequence, min(input.before_sequence, end))
        start = max(first_sequence, end - limit)
        return RPCResult(
            AgentMessagesPage(
                messages=_read_messages(context, start, end),
                next_before_sequence=start if start > first_sequence else None,
                deleted_before_sequence=first_sequence if first_sequence > 1 else None,
            )
        )

    def _begin_turn(
        self,
        context: AsyncContext,
        messages: Sequence[AgentUserMessage],
        step_type: str,
        interaction_mode: InteractionMode = InteractionMode.CHAT,
        executed_plan: AgentPlan | None = None,
        was_plan_draft: bool = False,
        *,
        session_settings: dict[str, Any] | None,
    ) -> StepDecision:
        """Store the turn's user messages, in order, and move to RunModel."""
        state = _require_agent_state(context)
        turn_id = f"turn-{state.turn_count + 1}"
        # The turn keeps these until it ends; later updates wait for the next turn.
        if session_settings:
            TURN_SESSION_SETTINGS.set(
                context, TurnSessionSettings(turn_id=turn_id, session_settings=session_settings)
            )
        elif TURN_SESSION_SETTINGS.get(context) is not None:
            TURN_SESSION_SETTINGS.delete(context)
        sequence = state.next_message_sequence
        text = "\n".join(message.text for message in messages)
        with self._session_tracer.trace_user_message_activation(
            context, step_type, turn_id, message_sequence_key(sequence), text
        ):
            user_messages = [self._runner.build_user_message(message.text) for message in messages]
            if any(user_message.role is not MessageRole.USER for user_message in user_messages):
                raise ValueError("build_user_message must return a user message")
            state = _append_messages(
                context, state, turn_id, user_messages, self._message_storage, starts_turn=True
            )
            AGENT_STATE.set(
                context,
                replace(
                    state,
                    status=AgentSessionStatus.RUNNING_MODEL,
                    active_turn_id=turn_id,
                    interaction_mode=interaction_mode,
                    accepts_turn_control=True,
                ),
            )
            events = self._event_writer(context, turn_id)
            events.write_event(AgentEventType.TURN_STARTED, {"sequence": sequence})
            if executed_plan is not None:
                events.write_event(
                    AgentEventType.PLAN_EXECUTION_STARTED, {"revision": executed_plan.revision}
                )
                if was_plan_draft:
                    _write_plan_updated(events, executed_plan)
        return self._go_to_message_step(context, self._run_model)

    def _interaction_mode_for_user_messages(
        self, context: Context, messages: Sequence[AgentUserMessage]
    ) -> InteractionMode:
        """Return the mode of a turn that these user messages start: planning or chat."""
        if not self._options.plan_mode_enabled:
            return InteractionMode.CHAT
        plan = _optional_agent_plan(context)
        if any(message.plan_mode for message in messages) or (
            plan is not None and plan.status is PlanStatus.DRAFT
        ):
            return InteractionMode.PLANNING
        return InteractionMode.CHAT

    def _begin_user_messages_turn(
        self, context: AsyncContext, messages: Sequence[AgentUserMessage], step_type: str
    ) -> StepDecision:
        mode = self._interaction_mode_for_user_messages(context, messages)
        return self._begin_turn(
            context, messages, step_type, mode, session_settings=_session_settings(context)
        )

    def _wait_for_user_message(self, context: Context) -> Wait:
        # Dex selects Channel conditions in argument order: a steer, a queued message, a plan.
        conditions: list[Condition] = [
            TURN_CONTROL_REQUESTS.for_range(at_least=1, at_most=ALL_PENDING_MESSAGES),
            QUEUED_USER_MESSAGES.for_one(),
        ]
        plan = _optional_agent_plan(context)
        # Keyed by revision, so an approval of an earlier revision never starts a turn.
        if plan is not None and plan.status in EXECUTABLE_PLAN_STATUSES:
            conditions.append(PLAN_EXECUTIONS.for_one(plan_execution_key(plan.revision)))
        return Wait.any_of(*conditions)

    def _start_user_message_turn(self, context: AsyncContext, step_type: str) -> StepDecision:
        messages = QUEUED_USER_MESSAGES.results(context)
        if messages:
            return self._begin_user_messages_turn(context, [messages[0]], step_type)
        plan = _optional_agent_plan(context)
        executions = (
            PLAN_EXECUTIONS.results(context, plan_execution_key(plan.revision))
            if plan is not None
            else ()
        )
        if not executions:
            raise RuntimeError("the user message wait completed without a message")
        if PENDING_PLAN_EXECUTION.get(context) is not None:
            PENDING_PLAN_EXECUTION.delete(context)
        if (
            plan is None
            or executions[0].revision != plan.revision
            or plan.status not in EXECUTABLE_PLAN_STATUSES
        ):
            return go_to(self._await_user_message, None)
        was_plan_draft = plan.status is not PlanStatus.ACTIVE
        if was_plan_draft:
            plan = replace(plan, status=PlanStatus.ACTIVE, updated_at=_rfc3339(datetime.now(UTC)))
            AGENT_PLAN.set(context, plan)
        return self._begin_turn(
            context,
            [AgentUserMessage(text=build_plan_execution_message(plan.revision))],
            step_type,
            InteractionMode.EXECUTING,
            plan,
            was_plan_draft,
            session_settings=_session_settings(context),
        )

    async def _run_model_step(self, context: AsyncContext) -> StepDecision:
        state = _require_agent_state(context)
        turn_id = _require_active_turn_id(state)
        tool_calls = _turn_tool_calls(context, turn_id)
        if any(not tool_call.status.is_final for tool_call in tool_calls):
            raise RuntimeError(f"turn {turn_id} resumed before every tool call finished")
        decision = self._decide_before_model_call(context, turn_id)
        if decision is not None:
            return decision
        events = self._event_writer(context, turn_id)
        turn = self._turn_context(context, state, tool_calls, events)
        through_sequence = self._compaction_cutoff_before_model_call(turn, state)
        if through_sequence is not None:
            return self._start_compaction(context, state, turn.context_summary, through_sequence)
        step_type = self._run_model.get_step_type()
        refusal = await self._check_spend_limit(context, state, turn_id, step_type)
        if refusal is not None:
            return refusal
        with self._session_tracer.trace_model_call(context, step_type, turn) as model_call_span:
            async with _background_heartbeats(context.heartbeat):
                outcome = await self._runner.run_model_step(turn)
                if isinstance(outcome, TurnCompleted | ToolCallsRequested):
                    await self._report_model_usage(context, turn_id, step_type, outcome.usage)
            # The index keeps where the turn's replies start, so summarized ones still count.
            stored_turn_message_count = _count_stored_turn_replies(context, state, turn_id)
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

    async def _check_spend_limit(
        self, context: AsyncContext, state: AgentState, turn_id: str, step_type: str
    ) -> StepDecision | None:
        """Return the move that ends a refused turn, or None to call the model."""
        spend_limit = self._options.spend_limit
        if spend_limit is None:
            return None
        # A raise or a timeout fails this attempt, so no model call runs unchecked.
        decision = await asyncio.wait_for(
            spend_limit.check_model_call(
                ModelCallCheck(
                    flow_id=context.flow_id,
                    turn_id=turn_id,
                    step_type=step_type,
                    attempt=context.attempt,
                    session_settings=_turn_session_settings(context),
                    session_usage=_agent_usage(context),
                )
            ),
            SPEND_LIMIT_CALL_TIMEOUT.total_seconds(),
        )
        if not isinstance(decision, ModelCallDecision):
            raise TypeError("check_model_call must return a ModelCallDecision")
        if decision.is_allowed:
            return None
        # RecordTurnFailure loads what ending a turn needs, which CompactContext does not.
        error = f"{MODEL_CALL_REFUSED_ERROR_PREFIX}: {decision.reason}"
        AGENT_STATE.set(context, replace(state, last_turn_error=error))
        return go_to(self._record_turn_failure, None)

    async def _report_model_usage(
        self, context: AsyncContext, turn_id: str, step_type: str, usage: AgentUsage
    ) -> None:
        spend_limit = self._options.spend_limit
        if spend_limit is None:
            return
        report = ModelUsageReport(
            report_id=f"{context.flow_id}/{turn_id}/{context.step_execution_id}/{context.attempt}",
            flow_id=context.flow_id,
            turn_id=turn_id,
            step_type=step_type,
            attempt=context.attempt,
            session_settings=_turn_session_settings(context),
            usage=usage,
            session_usage=_agent_usage(context).combined_with(usage),
        )
        try:
            await asyncio.wait_for(
                spend_limit.report_model_usage(report), SPEND_LIMIT_CALL_TIMEOUT.total_seconds()
            )
        except Exception:
            # The call is paid for: failing the attempt would pay for it again.
            _LOGGER.warning(
                "dex-agents: report_model_usage failed for %s", report.report_id, exc_info=True
            )

    def _decide_before_model_call(self, context: AsyncContext, turn_id: str) -> StepDecision | None:
        """Return RunModel's move instead of a model call, or None to call the model."""
        # Execute state is fixed at the first attempt, so this and the compaction
        # check are RunModel's decision points.
        if _turn_control_reason(context, turn_id) is None:
            return None
        # Ending the turn in this commit leaves FinishTurnCancellation no pending turn end to race.
        self._end_controlled_turn(
            context, _require_agent_state(context), STEERED_USER_MESSAGES.size(context)
        )
        return self._go_to_message_step(context, self._apply_turn_control)

    def _turn_context(
        self,
        context: AsyncContext,
        state: AgentState,
        tool_calls: Sequence[AgentToolCall],
        events: AgentEventWriter,
    ) -> AgentTurnContext:
        context_summary = self._runner_context_summary(context)
        index = AGENT_MESSAGE_INDEX.get(context)
        if context_summary is None and _first_stored_sequence(index) > 1:
            _LOGGER.warning(
                "dex-agents: session %s stores no messages before %s; retention deleted them, "
                "and this runner cannot use the context summary that covers them",
                context.flow_id,
                _first_stored_sequence(index),
            )
        first_sequence = _model_input_first_sequence(index, context_summary)
        return AgentTurnContext(
            flow_id=context.flow_id,
            turn_id=_require_active_turn_id(state),
            attempt=context.attempt,
            messages=_read_messages(context, first_sequence, state.next_message_sequence),
            framework_run_state=FRAMEWORK_RUN_STATE.get(context),
            tool_calls={tool_call.tool_call_id: tool_call for tool_call in tool_calls},
            tool_policies=self._options.tool_policies,
            default_tool_effect=self._options.default_tool_effect,
            events=events,
            heartbeat=context.heartbeat,
            enabled_built_in_tools=self._options.built_in_tools,
            interaction_mode=_interaction_mode(state),
            plan=_optional_agent_plan(context),
            context_summary=context_summary,
            session_settings=_turn_session_settings(context),
        )

    def _runner_context_summary(self, context: Context) -> ContextSummary | None:
        # A runner without the capability cannot use a summary, so it keeps every message.
        if not isinstance(self._runner, ContextCompactionRunner):
            return None
        return _context_summary(context)

    def _compaction_cutoff_before_model_call(
        self, turn: AgentTurnContext, state: AgentState
    ) -> int | None:
        """Return the sequence to summarize through before this model call, or None to call."""
        policy = self._options.compaction
        if policy is None or not isinstance(self._runner, ContextCompactionRunner):
            return None
        if not policy.is_triggered_by(self._runner.estimate_input_tokens(turn)):
            return None
        cutoff = self._runner.select_compaction_cutoff(turn, policy.retained_token_budget)
        summarized_through = (
            0 if turn.context_summary is None else turn.context_summary.last_sequence
        )
        # A cutoff that does not advance means nothing more to summarize, so the pair cannot loop.
        if cutoff is None or cutoff <= summarized_through:
            return None
        if (
            not isinstance(cutoff, int)
            or isinstance(cutoff, bool)
            or (cutoff >= state.next_message_sequence)
        ):
            raise ValueError(
                f"select_compaction_cutoff returned {cutoff!r}, which is not a stored message"
            )
        return cutoff

    def _start_compaction(
        self,
        context: AsyncContext,
        state: AgentState,
        previous_summary: ContextSummary | None,
        through_sequence: int,
    ) -> StepDecision:
        # CompactContext commits once, so this commit is where the status becomes visible.
        AGENT_STATE.set(context, replace(state, status=AgentSessionStatus.COMPACTING_CONTEXT))
        index = AGENT_MESSAGE_INDEX.get(context)
        first_sequence = _model_input_first_sequence(index, previous_summary)
        options = replace(
            self._compact_context.get_step_options(),
            execute_load_attribute_map_instances=_message_chunk_loads(
                index, first_sequence, through_sequence + 1
            ),
        )
        return go_to_many(
            StepMovement.of(
                self._compact_context,
                CompactContextInput(through_sequence=through_sequence),
                options=options,
            )
        )

    async def _compact_context_step(
        self,
        context: AsyncContext,
        input: CompactContextInput,
    ) -> StepDecision:
        state = _require_agent_state(context)
        turn_id = _require_active_turn_id(state)
        previous_summary = _context_summary(context)
        index = AGENT_MESSAGE_INDEX.get(context)
        first_sequence = _model_input_first_sequence(index, previous_summary)
        through_sequence = input.through_sequence
        if not first_sequence <= through_sequence < state.next_message_sequence:
            raise RuntimeError(
                f"CompactContext cannot summarize through message {through_sequence}"
            )
        if not isinstance(self._runner, ContextCompactionRunner):
            raise TypeError("CompactContext needs a runner that implements ContextCompactionRunner")
        step_type = self._compact_context.get_step_type()
        refusal = await self._check_spend_limit(context, state, turn_id, step_type)
        if refusal is not None:
            return refusal
        events = self._event_writer(context, turn_id)
        events.write_event(
            AgentEventType.COMPACTION_STARTED, {"through_sequence": through_sequence}
        )
        request = ContextSummaryRequest(
            flow_id=context.flow_id,
            turn_id=turn_id,
            attempt=context.attempt,
            previous_summary=previous_summary,
            messages=_read_messages(context, first_sequence, through_sequence + 1),
            through_sequence=through_sequence,
            events=events,
            heartbeat=context.heartbeat,
            input_token_budget=(
                None
                if self._options.compaction is None
                else self._options.compaction.context_window_tokens
            ),
            session_settings=_turn_session_settings(context),
        )
        with self._session_tracer.trace_context_summary(
            context,
            step_type,
            turn_id,
            through_sequence,
            request.messages,
        ) as summary_span:
            async with _background_heartbeats(context.heartbeat):
                result = await self._runner.summarize_context(request)
                if isinstance(result, ContextSummaryResult):
                    await self._report_model_usage(context, turn_id, step_type, result.usage)
            if not isinstance(result, ContextSummaryResult) or not str(result.text).strip():
                raise ValueError("summarize_context must return a ContextSummaryResult with text")
            summary_span.record_result(result)
        summary = ContextSummary(
            generation=1 if previous_summary is None else previous_summary.generation + 1,
            first_sequence=1,
            last_sequence=through_sequence,
            text=result.text,
            created_at=_rfc3339(datetime.now(UTC)),
        )
        CONTEXT_SUMMARY.set(context, summary)
        if index is not None:
            retained_index = _delete_retired_message_chunks(
                context,
                index,
                state.next_message_sequence,
                summary,
                self._message_storage.retained_message_limit,
            )
            if retained_index != index:
                AGENT_MESSAGE_INDEX.set(context, retained_index)
        _record_usage(context, events, result.usage)
        AGENT_STATE.set(context, replace(state, status=AgentSessionStatus.RUNNING_MODEL))
        events.write_event(
            AgentEventType.COMPACTION_FINISHED,
            {"generation": summary.generation, "through_sequence": through_sequence},
        )
        return self._go_to_message_step(context, self._run_model)

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
        state = _append_messages(
            context, state, turn_id, outcome.new_messages, self._message_storage
        )
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
                accepts_turn_control=False,
                last_turn_error=None,
            ),
        )
        # The turn's user messages are stored before RunModel; count only what came after them.
        appended_message_count = stored_turn_message_count + len(outcome.new_messages)
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
        state = _append_messages(
            context, state, turn_id, outcome.new_messages, self._message_storage
        )
        FRAMEWORK_RUN_STATE.set(context, outcome.framework_run_state)
        now = datetime.now(UTC)
        interrupts = _pending_interrupts(context)
        trace_context = self._session_tracer.read_activation_traceparent(context)
        movements: list[StepMovement[str]] = []
        write_todos_call_count = sum(
            1
            for requested in outcome.tool_calls
            if self._enabled_built_in_tool(requested.tool_name) is BuiltInTool.WRITE_TODOS
        )
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
            if (
                state.interaction_mode is InteractionMode.PLANNING
                and built_in_tool is not BuiltInTool.WRITE_TODOS
            ):
                # A planning turn hides executable tools; a call to one never runs.
                rejected = replace(
                    tool_call,
                    status=ToolCallStatus.REJECTED,
                    rejection_message=PLANNING_TURN_TOOL_REJECTION,
                )
                _record_final_tool_call(context, rejected, events)
                continue
            if built_in_tool is BuiltInTool.WRITE_TODOS:
                self._write_todos(
                    context, state, tool_call, write_todos_call_count > 1, now, events
                )
                continue
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

    def _write_todos(
        self,
        context: AsyncContext,
        state: AgentState,
        tool_call: AgentToolCall,
        is_called_more_than_once: bool,
        now: datetime,
        events: AgentEventWriter,
    ) -> None:
        """Replace the plan and record the call final, in RunModel's commit."""
        if is_called_more_than_once:
            failed = replace(
                tool_call, status=ToolCallStatus.FAILED, error=WRITE_TODOS_CALLED_MORE_THAN_ONCE
            )
            _record_final_tool_call(context, failed, events)
            return
        try:
            tasks = parse_write_todos_arguments(tool_call.arguments)
        except BuiltInToolArgumentsError as error:
            failed = replace(tool_call, status=ToolCallStatus.FAILED, error=str(error))
            _record_final_tool_call(context, failed, events)
            return
        previous = _optional_agent_plan(context)
        plan = AgentPlan(
            revision=(0 if previous is None else previous.revision) + 1,
            status=plan_status_after_write(tasks, _interaction_mode(state)),
            tasks=tasks,
            updated_at=_rfc3339(now),
        )
        AGENT_PLAN.set(context, plan)
        _write_plan_updated(events, plan)
        output = (
            build_plan_cleared_output(plan.revision)
            if plan.status is PlanStatus.CLEARED
            else build_plan_updated_output(plan.revision, len(tasks))
        )
        succeeded = replace(tool_call, status=ToolCallStatus.SUCCEEDED, output=output)
        _record_final_tool_call(context, succeeded, events)

    def _interrupt_deadline(self, now: datetime) -> str | None:
        if self._options.interrupt_deadline is None:
            return None
        return _rfc3339(now + self._options.interrupt_deadline)

    def _wait_for_tool_approval(self, context: Context, tool_call_id: str) -> Wait:
        conditions: list[Condition] = [
            TOOL_CALL_CANCELLATIONS.for_one(tool_call_id),
            INTERRUPT_RESOLUTIONS.for_one(tool_call_id),
        ]
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
        if TOOL_CALL_CANCELLATIONS.results(context, tool_call_id):
            return self._cancel_waiting_tool_call(
                context,
                tool_call,
                (tool_call_id, InterruptKind.TOOL_APPROVAL),
                events,
                self._tool_approval_movement(tool_call_id),
            )
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
                    cancellation_reason=None,
                )
                AGENT_TOOL_CALLS.set(context, approved.tool_call_id, approved)
                return go_to_many(self._tool_call_movement(approved))
            rejected = replace(
                tool_call,
                status=ToolCallStatus.REJECTED,
                rejection_message=resolution.rejection_message,
                cancellation_reason=None,
            )
            return _finish_tool_call(context, rejected, events)

    def _wait_for_question_answer(self, context: Context, tool_call_id: str) -> Wait:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        interrupt_id = _question_interrupt_id(tool_call)
        conditions: list[Condition] = [
            TOOL_CALL_CANCELLATIONS.for_one(tool_call_id),
            INTERRUPT_RESOLUTIONS.for_one(interrupt_id),
        ]
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
            error = (
                f"the tool's question cannot use interrupt ID {interrupt_id}, "
                "which another tool call of this turn uses"
            )
            failed = (
                # A started task may have taken effect, so its call is recorded by effect.
                _record_tool_task_defect(tool_call, error)
                if tool_call.task_id is not None
                else replace(
                    tool_call,
                    status=ToolCallStatus.FAILED,
                    error=error,
                    **_NO_PENDING_TOOL_QUESTION,
                )
            )
            return _finish_tool_call(context, failed, events)
        if TOOL_CALL_CANCELLATIONS.results(context, tool_call_id):
            return self._cancel_waiting_tool_call(
                context,
                tool_call,
                (interrupt_id, InterruptKind.QUESTION),
                events,
                self._question_answer_movement(tool_call),
            )
        resolutions = INTERRUPT_RESOLUTIONS.results(context, interrupt_id)
        if resolutions:
            return self._resolve_question(context, tool_call, interrupt_id, resolutions[0], events)
        if not context.has_timer_fired():
            raise RuntimeError(f"the question wait for {interrupt_id} completed without an answer")
        if _is_tool_question_unraised(context, tool_call, interrupt_id):
            reason = _turn_control_reason(context, tool_call.turn_id)
            if (
                reason is ToolCallCancellationReason.TURN_CANCELLED
                and tool_call.task_id is not None
            ):
                # cancel_turn abandons a started task instead of asking its question.
                return go_to_many(self._finish_turn_cancellation_movement(tool_call.turn_id))
            if reason is not None and _is_cancellable_by_turn_control(tool_call):
                # The turn is steered or cancelled, so the tool's question is never asked.
                return _finish_tool_call(context, _cancelled_tool_call(tool_call, reason), events)
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
            is_deadline_passed=True,
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
        *,
        is_deadline_passed: bool = False,
    ) -> StepDecision:
        _write_interrupt_resolved(events, interrupt_id, InterruptKind.QUESTION, resolution)
        if tool_call.task_id is not None and tool_call.input_question is not None:
            return self._return_question_to_task(
                context, tool_call, interrupt_id, resolution, is_deadline_passed
            )
        if not resolution.approved or resolution.answer is None:
            rejected = replace(
                tool_call,
                status=ToolCallStatus.REJECTED,
                rejection_message=resolution.rejection_message or QUESTION_DECLINED_TOOL_RESULT,
                cancellation_reason=None,
                **_NO_PENDING_TOOL_QUESTION,
            )
            return _finish_tool_call(context, rejected, events)
        if tool_call.input_question is None:
            answered = replace(
                tool_call,
                status=ToolCallStatus.SUCCEEDED,
                output=build_question_answered_output(resolution.answer),
                cancellation_reason=None,
            )
            return _finish_tool_call(context, answered, events)
        answer = QuestionAnswer(
            interrupt_id=interrupt_id, question=tool_call.input_question, answer=resolution.answer
        )
        approved = replace(
            tool_call,
            status=ToolCallStatus.APPROVED,
            input_answers=[*tool_call.input_answers, answer],
            cancellation_reason=None,
            **_NO_PENDING_TOOL_QUESTION,
        )
        AGENT_TOOL_CALLS.set(context, approved.tool_call_id, approved)
        return go_to_many(self._tool_call_movement(approved))

    def _return_question_to_task(
        self,
        context: AsyncContext,
        tool_call: AgentToolCall,
        interrupt_id: str,
        resolution: AgentInterruptResolution,
        is_deadline_passed: bool,
    ) -> StepDecision:
        """Hand a started task's question back; its result, not ``rejected``, ends the call."""
        if resolution.approved and resolution.answer is not None:
            answer, kind = resolution.answer, QuestionResolution.ANSWERED
        elif is_deadline_passed:
            answer, kind = QUESTION_DEADLINE_PASSED, QuestionResolution.DEADLINE_PASSED
        else:
            answer = resolution.rejection_message or QUESTION_DECLINED_TOOL_RESULT
            kind = QuestionResolution.DECLINED
        resolved = QuestionAnswer(
            interrupt_id=interrupt_id,
            question=tool_call.input_question or "",
            answer=answer,
            resolution=kind,
        )
        waiting = replace(
            tool_call,
            status=ToolCallStatus.WAITING_FOR_TASK,
            input_answers=[*tool_call.input_answers, resolved],
            task_answer_pending=True,
            cancellation_reason=None,
            **_NO_PENDING_TOOL_QUESTION,
        )
        AGENT_TOOL_CALLS.set(context, waiting.tool_call_id, waiting)
        return go_to_many(self._tool_task_poll_movement(waiting))

    def _wait_for_durable_wait_timer(self, context: Context, tool_call_id: str) -> Wait:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.wait_until is None:
            raise RuntimeError(f"durable wait {tool_call_id} has no wait_until")
        return Wait.any_of(
            _timer_until(tool_call.wait_until), TOOL_CALL_CANCELLATIONS.for_one(tool_call_id)
        )

    def _finish_durable_wait(self, context: AsyncContext, tool_call_id: str) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status is not ToolCallStatus.WAITING_FOR_TIMER:
            return dead_end()
        if not context.has_timer_fired():
            if not TOOL_CALL_CANCELLATIONS.results(context, tool_call_id):
                raise RuntimeError(f"the durable wait {tool_call_id} completed without its Timer")
            return self._cancel_waiting_tool_call(
                context,
                tool_call,
                None,
                self._event_writer(context, tool_call.turn_id),
                self._timer_movement(tool_call_id),
            )
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

    def _cancel_waiting_tool_call(
        self,
        context: AsyncContext,
        tool_call: AgentToolCall,
        interrupt: tuple[str, InterruptKind] | None,
        events: AgentEventWriter,
        wait_again: StepMovement[str],
    ) -> StepDecision:
        """Record a call woken by ToolCallCancellations as cancelled, or wait again."""
        # The flags decide; a wake-up left from an earlier turn's call ID changes nothing.
        reason = _turn_control_reason(context, tool_call.turn_id)
        if reason is None or not _is_cancellable_by_turn_control(tool_call):
            return go_to_many(wait_again)
        if interrupt is not None:
            interrupt_id, kind = interrupt
            if _pending_interrupt(context, interrupt_id) is not None:
                _remove_pending_interrupt(context, interrupt_id)
                _write_interrupt_cancelled(events, interrupt_id, kind, reason)
            elif INTERRUPT_RESOLUTIONS.size(context, interrupt_id) > 0:
                # The answer committed first; the next wait consumes it.
                return go_to_many(wait_again)
        return _finish_tool_call(context, _cancelled_tool_call(tool_call, reason), events)

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
        # Retries reuse the first attempt's state, so a call that started is never abandoned.
        reason = _turn_control_reason(context, tool_call.turn_id)
        if reason is not None:
            cancelled = _cancelled_tool_call(tool_call, reason)
            return _finish_tool_call(
                context, cancelled, self._event_writer(context, tool_call.turn_id)
            )
        with self._session_tracer.trace_tool_call(context, step_type, tool_call) as tool_call_span:
            events = self._event_writer(context, tool_call.turn_id)
            events.write_tool_call_started(tool_call.tool_call_id, tool_call.tool_name)
            heartbeats = _ToolStepHeartbeats(context)
            # Output first: Dex returns an attempt as not run when its stream half-closes
            # before any output.
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
                supports_tool_tasks=isinstance(self._runner, ToolTaskPoller),
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
            if isinstance(result, ToolTaskStarted):
                started = _record_tool_task_started(
                    tool_call, result, has_poller=isinstance(self._runner, ToolTaskPoller)
                )
                tool_call_span.record_finished(started)
                if started.status.is_final:
                    return _finish_tool_call(context, started, events)
                AGENT_TOOL_CALLS.set(context, started.tool_call_id, started)
                events.write_event(
                    AgentEventType.TOOL_TASK_STARTED,
                    {
                        "tool_call_id": started.tool_call_id,
                        "tool_name": started.tool_name,
                        "task_id": started.task_id,
                    },
                )
                return go_to_many(self._tool_task_poll_movement(started))
            succeeded = replace(tool_call, status=ToolCallStatus.SUCCEEDED, output=result.output)
            tool_call_span.record_finished(succeeded)
            return _finish_tool_call(context, succeeded, events)

    def _wait_for_tool_task_poll(self, context: Context, tool_call_id: str) -> Wait:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        # A resolved question, or a cancelled turn, reaches the task at once.
        interval = (
            0
            if tool_call.task_answer_pending or _is_turn_cancelled(context, tool_call.turn_id)
            else tool_call.task_poll_interval_seconds or 1
        )
        return Wait.until(Timer.by_duration(timedelta(seconds=interval)))

    async def _poll_task_once(self, context: AsyncContext, tool_call_id: str) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status.is_final:
            return dead_end()
        if tool_call.status is not ToolCallStatus.WAITING_FOR_TASK or tool_call.task_id is None:
            raise RuntimeError(f"tool call {tool_call_id} is not waiting for a task")
        if _is_turn_cancelled(context, tool_call.turn_id):
            # This poll started after cancel_turn committed, so it abandons the task instead.
            return go_to_many(self._finish_turn_cancellation_movement(tool_call.turn_id))
        events = self._event_writer(context, tool_call.turn_id)
        if not isinstance(self._runner, ToolTaskPoller):
            # A Worker whose runner lost the method; the task may still run.
            return _finish_tool_call(
                context, _record_tool_task_defect(tool_call, MISSING_TOOL_TASK_POLLER), events
            )
        poll = ToolTaskPoll(
            flow_id=context.flow_id,
            tool_call=tool_call,
            idempotency_key=f"{context.flow_id}/{tool_call.turn_id}/{tool_call.tool_call_id}",
            attempt=context.attempt,
            input_answers=tuple(tool_call.input_answers),
            events=events,
            heartbeat=context.heartbeat,
            pending_answer=(
                tool_call.input_answers[-1]
                if tool_call.task_answer_pending and tool_call.input_answers
                else None
            ),
        )
        step_type = self._poll_tool_task.get_step_type()
        with self._session_tracer.trace_tool_call(context, step_type, tool_call) as poll_span:
            async with _background_heartbeats(context.heartbeat):
                try:
                    outcome = await self._runner.poll_tool_task(poll)
                except ToolExecutionError as error:
                    failed = replace(tool_call, status=ToolCallStatus.FAILED, error=str(error))
                    poll_span.record_finished(failed)
                    return _finish_tool_call(context, failed, events)
            polled = self._record_tool_task_poll(tool_call, outcome)
            poll_span.record_finished(polled)
        if polled.status.is_final:
            return _finish_tool_call(context, polled, events)
        if polled.status is ToolCallStatus.AWAITING_ANSWER:
            # This Step runs unlocked; AwaitQuestionAnswer raises the interrupt under the lock.
            AGENT_TOOL_CALLS.set(context, polled.tool_call_id, polled)
            return go_to_many(self._question_answer_movement(polled))
        if polled != tool_call:
            AGENT_TOOL_CALLS.set(context, polled.tool_call_id, polled)
        return go_to_many(self._tool_task_poll_movement(polled))

    def _record_tool_task_poll(
        self, tool_call: AgentToolCall, outcome: ToolTaskPollOutcome
    ) -> AgentToolCall:
        """Return the record after one poll: final, awaiting an answer, or still waiting."""
        polled = replace(tool_call, task_answer_pending=False)
        if isinstance(outcome, ToolTaskPending):
            interval = _tool_task_poll_interval(outcome.poll_interval_seconds)
            if interval is None:
                return _record_tool_task_defect(tool_call, INVALID_TOOL_TASK_POLL_INTERVAL)
            # The runner decides what the next poll needs to know, such as an answer not taken yet.
            return replace(
                polled,
                task_poll_interval_seconds=interval,
                input_continuation=outcome.continuation,
            )
        if isinstance(outcome, ToolInputRequested):
            asked = _record_tool_input_request(polled, outcome)
            if asked.status.is_final:
                return _record_tool_task_defect(tool_call, asked.error or "")
            return asked
        if isinstance(outcome, ToolExecutionResult):
            return replace(polled, status=ToolCallStatus.SUCCEEDED, output=outcome.output)
        raise TypeError(
            "poll_tool_task must return ToolExecutionResult, ToolInputRequested, or ToolTaskPending"
        )

    def _record_tool_recovery(
        self,
        context: AsyncContext,
        tool_call_id: str,
        status: ToolCallStatus,
    ) -> StepDecision:
        if _is_state_limit_failure(context.recovery_error):
            return self._fail_session(context, _recovery_detail(context))
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status.is_final:
            return dead_end()
        finished = _recovered_tool_call(tool_call, context.recovery_error, status)
        return _finish_tool_call(context, finished, self._event_writer(context, tool_call.turn_id))

    def _resume_turn_after_tool_calls(
        self, context: AsyncContext, expected_result_count: int
    ) -> StepDecision:
        state = _require_agent_state(context)
        turn_id = _require_active_turn_id(state)
        tool_calls = _turn_tool_calls(context, turn_id)
        reason = _turn_control_reason(context, turn_id)
        if TURN_CONTROL_REQUESTS.results(context):
            # FinishTurnCancellation, not this wake, ends the waits of a cancelled turn.
            if reason is ToolCallCancellationReason.STEERED:
                self._wake_waiting_tool_calls(
                    context, tool_calls, reason, self._event_writer(context, turn_id)
                )
            # The results did not all arrive, so wait again for the same count.
            return go_to(self._resume_turn, expected_result_count)
        unfinished_count = sum(1 for tool_call in tool_calls if not tool_call.status.is_final)
        if unfinished_count:
            # A consumed result from an earlier turn must not resume this one early.
            return go_to(self._resume_turn, unfinished_count)
        if reason is not None:
            return self._go_to_message_step(context, self._apply_turn_control)
        AGENT_STATE.set(context, replace(state, status=AgentSessionStatus.RUNNING_MODEL))
        return self._go_to_message_step(context, self._run_model)

    def _wake_waiting_tool_calls(
        self,
        context: AsyncContext,
        tool_calls: Sequence[AgentToolCall],
        reason: ToolCallCancellationReason,
        events: AgentEventWriter,
    ) -> None:
        """Cancel the turn's pending interrupts and wake each Step waiting for a person or Timer."""
        interrupts = _pending_interrupts(context)
        cancelled_interrupt_ids: set[str] = set()
        for tool_call in tool_calls:
            if not _is_cancellable_by_turn_control(tool_call):
                continue
            if tool_call.status is ToolCallStatus.WAITING_FOR_TIMER:
                # A later control wake finds the first message still pending and adds none.
                if TOOL_CALL_CANCELLATIONS.size(context, tool_call.tool_call_id) == 0:
                    _publish_tool_call_cancellation(context, tool_call.tool_call_id, reason)
                continue
            if tool_call.status is ToolCallStatus.AWAITING_APPROVAL:
                interrupt_id = tool_call.tool_call_id
                kind = InterruptKind.TOOL_APPROVAL
            else:
                interrupt_id = _question_interrupt_id(tool_call)
                kind = InterruptKind.QUESTION
            # An answered interrupt is gone, so its answer wins; an unraised question checks flags.
            if any(
                interrupt.interrupt_id == interrupt_id
                and interrupt.tool_call_id == tool_call.tool_call_id
                for interrupt in interrupts
            ):
                cancelled_interrupt_ids.add(interrupt_id)
                _write_interrupt_cancelled(events, interrupt_id, kind, reason)
                _publish_tool_call_cancellation(context, tool_call.tool_call_id, reason)
        if cancelled_interrupt_ids:
            PENDING_INTERRUPTS.set(
                context,
                [
                    interrupt
                    for interrupt in interrupts
                    if interrupt.interrupt_id not in cancelled_interrupt_ids
                ],
            )

    def _finish_cancelled_turn(self, context: AsyncContext, turn_id: str) -> StepDecision:
        """Hand a cancelled turn to the one Step that ends it, deciding from committed state."""
        state = _optional_agent_state(context)
        if (
            state is None
            or state.active_turn_id != turn_id
            or not _is_turn_cancelled(context, turn_id)
        ):
            # The turn ended before the cancellation applied.
            return dead_end()
        if state.status is not AgentSessionStatus.RUNNING_TOOLS:
            # cancel_turn cancelled the model call or compaction, so no other Step ends the turn.
            return self._go_to_message_step(context, self._apply_turn_control)
        # ResumeTurn waits for every result and ends the turn; started tool Steps still finish.
        events = self._event_writer(context, turn_id)
        interrupts = _pending_interrupts(context)
        cancelled_interrupt_ids: set[str] = set()
        movements: list[StepMovement[str]] = []
        for tool_call in _turn_tool_calls(context, turn_id):
            interrupt = _waiting_interrupt(tool_call)
            if interrupt is not None:
                interrupt_id, kind = interrupt
                if INTERRUPT_RESOLUTIONS.size(context, interrupt_id) > 0:
                    # The answer committed first; a new wait applies it, and its next Step decides.
                    if tool_call.cancellation_reason is None:
                        # The mark keeps a later FinishTurnCancellation from starting a second wait.
                        handed_back = replace(
                            tool_call, cancellation_reason=ToolCallCancellationReason.TURN_CANCELLED
                        )
                        AGENT_TOOL_CALLS.set(context, handed_back.tool_call_id, handed_back)
                        movements.append(self._waiting_tool_call_movement(handed_back))
                    continue
                if any(pending.interrupt_id == interrupt_id for pending in interrupts):
                    cancelled_interrupt_ids.add(interrupt_id)
                    _write_interrupt_cancelled(
                        events, interrupt_id, kind, ToolCallCancellationReason.TURN_CANCELLED
                    )
            if _is_abandoned_tool_task(tool_call):
                if tool_call.cancellation_reason is None:
                    abandoned = replace(
                        tool_call,
                        status=ToolCallStatus.WAITING_FOR_TASK,
                        cancellation_reason=ToolCallCancellationReason.TURN_CANCELLED,
                        task_answer_pending=False,
                        **_NO_PENDING_TOOL_QUESTION,
                    )
                    AGENT_TOOL_CALLS.set(context, abandoned.tool_call_id, abandoned)
                    movements.append(self._cancel_tool_task_movement(abandoned))
                continue
            if _is_cancellable_by_turn_control(tool_call):
                cancelled = _cancelled_tool_call(
                    tool_call, ToolCallCancellationReason.TURN_CANCELLED
                )
                _record_final_tool_call(context, cancelled, events)
        if cancelled_interrupt_ids:
            PENDING_INTERRUPTS.set(
                context,
                [
                    interrupt
                    for interrupt in interrupts
                    if interrupt.interrupt_id not in cancelled_interrupt_ids
                ],
            )
        return go_to_many(*movements) if movements else dead_end()

    async def _cancel_abandoned_tool_task(
        self, context: AsyncContext, tool_call_id: str
    ) -> StepDecision:
        tool_call = AGENT_TOOL_CALLS.get(context, tool_call_id)
        if tool_call.status.is_final:
            return dead_end()
        if tool_call.task_id is None or tool_call.cancellation_reason is None:
            raise RuntimeError(f"tool call {tool_call_id} has no abandoned task")
        if isinstance(self._runner, ToolTaskCanceller):
            cancellation = ToolTaskCancellation(
                flow_id=context.flow_id,
                tool_call=tool_call,
                idempotency_key=f"{context.flow_id}/{tool_call.turn_id}/{tool_call.tool_call_id}",
                attempt=context.attempt,
                heartbeat=context.heartbeat,
            )
            async with _background_heartbeats(context.heartbeat):
                await self._runner.cancel_tool_task(cancellation)
        abandoned = replace(
            tool_call, status=ToolCallStatus.OUTCOME_UNKNOWN, error=TOOL_TASK_ABANDONED_ERROR
        )
        return _finish_tool_call(context, abandoned, self._event_writer(context, tool_call.turn_id))

    def _apply_turn_control_requests(self, context: AsyncContext) -> StepDecision:
        state = _require_agent_state(context)
        steered_messages = list(STEERED_USER_MESSAGES.results(context))
        if state.active_turn_id is not None:
            self._end_controlled_turn(context, state, len(steered_messages))
        if not steered_messages:
            return go_to(self._await_user_message, None)
        # Any steered message with plan_mode, or a draft plan, makes it a planning turn.
        return self._begin_user_messages_turn(
            context, steered_messages, self._apply_turn_control.get_step_type()
        )

    def _end_controlled_turn(
        self,
        context: AsyncContext,
        state: AgentState,
        steered_message_count: int,
    ) -> None:
        """End a steered or cancelled turn with its unfinished-turn messages, or with a note."""
        turn_id = _require_active_turn_id(state)
        tool_calls = _turn_tool_calls(context, turn_id)
        if any(not tool_call.status.is_final for tool_call in tool_calls):
            raise RuntimeError(f"turn {turn_id} ended before every tool call finished")
        events = self._event_writer(context, turn_id)
        is_cancelled = (
            _turn_control_reason(context, turn_id) is ToolCallCancellationReason.TURN_CANCELLED
        )
        stored_message_count = _count_stored_turn_replies(context, state, turn_id)
        messages = self._build_unfinished_turn_messages(context, state, tool_calls, events)
        if messages is None:
            messages = []
            if tool_calls:
                header = TURN_CANCELLED_NOTE_HEADER if is_cancelled else TURN_STEERED_NOTE_HEADER
                # The records are deleted below; the note keeps the next model from repeating them.
                note = self._runner.build_user_message(build_tool_call_note(header, tool_calls))
                if note.role is not MessageRole.USER:
                    raise ValueError("build_user_message must return a user message")
                messages.append(note)
        state = _append_messages(context, state, turn_id, messages, self._message_storage)
        for tool_call in tool_calls:
            AGENT_TOOL_CALLS.delete(context, tool_call.tool_call_id)
        if FRAMEWORK_RUN_STATE.get(context) is not None:
            FRAMEWORK_RUN_STATE.delete(context)
        AGENT_STATE.set(
            context,
            replace(
                state,
                status=AgentSessionStatus.WAITING_FOR_USER_MESSAGE,
                turn_count=state.turn_count + 1,
                active_turn_id=None,
                accepts_turn_control=False,
                last_turn_error=None,
            ),
        )
        message_count = stored_message_count + len(messages)
        if is_cancelled:
            events.write_event(AgentEventType.TURN_CANCELLED, {"message_count": message_count})
            return
        events.write_event(
            AgentEventType.TURN_STEERED,
            {"steered_message_count": steered_message_count, "message_count": message_count},
        )

    def _fail_active_turn(self, context: AsyncContext) -> StepDecision:
        if _is_state_limit_failure(context.recovery_error):
            return self._fail_session(context, _recovery_detail(context))
        state = _require_agent_state(context)
        turn_id = _require_active_turn_id(state)
        tool_calls = _turn_tool_calls(context, turn_id)
        events = self._event_writer(context, turn_id)
        # A refused model call moves here on purpose, with its reason already in the state.
        error = (
            state.last_turn_error
            if context.recovery_error is None and state.last_turn_error
            else _recovery_detail(context)
        )
        return self._fail_turn(context, state, tool_calls, events, error)

    def _fail_session(self, context: AsyncContext, error: str) -> StepDecision:
        """End a session whose Flow reached the state limit, in one commit with the Flow's close."""
        # This commit is checked against the same limit, so it stores no turn messages and keeps
        # the turn's tool call records. When it does not fit, Dex fails the Flow with the same
        # text and AgentState stays as it was; see dex_agents.session_failure.
        state = _require_agent_state(context)
        if FRAMEWORK_RUN_STATE.get(context) is not None:
            FRAMEWORK_RUN_STATE.delete(context)
        if _pending_interrupts(context):
            PENDING_INTERRUPTS.set(context, [])
        AGENT_STATE.set(
            context,
            replace(
                state,
                status=AgentSessionStatus.FAILED,
                turn_count=state.turn_count + (0 if state.active_turn_id is None else 1),
                active_turn_id=None,
                accepts_turn_control=False,
                last_turn_error=error,
            ),
        )
        events = self._event_writer(context, state.active_turn_id)
        events.write_event(AgentEventType.TURN_FAILED, {"error": error})
        return force_fail(error)

    def _fail_turn(
        self,
        context: AsyncContext,
        state: AgentState,
        tool_calls: Sequence[AgentToolCall],
        events: AgentEventWriter,
        error: str,
    ) -> StepDecision:
        turn_id = _require_active_turn_id(state)
        messages = list(
            self._build_unfinished_turn_messages(context, state, tool_calls, events) or ()
        )
        finished_tool_calls = [tool_call for tool_call in tool_calls if tool_call.status.is_final]
        if finished_tool_calls:
            # The records are deleted below; the note keeps the next model from repeating writes.
            messages.append(
                self._runner.build_user_message(build_turn_failure_note(finished_tool_calls))
            )
        state = _append_messages(context, state, turn_id, messages, self._message_storage)
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
                accepts_turn_control=False,
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
    ) -> list[FrameworkMessage] | None:
        """Return the runner's unfinished-turn messages, or None when it has none or raised."""
        if not isinstance(self._runner, UnfinishedTurnMessageBuilder):
            return None
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
            return None

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

    def _tool_task_poll_movement(self, tool_call: AgentToolCall) -> StepMovement[str]:
        # A failed poll leaves the task running, so its outcome is recorded by effect.
        record_load = AGENT_TOOL_CALLS.load(tool_call.tool_call_id)
        recovery_step: RecordToolFailure | RecordToolOutcomeUnknown = (
            self._record_tool_failure
            if tool_call.effect in MULTI_ATTEMPT_TOOL_EFFECTS
            else self._record_tool_outcome_unknown
        )
        recovery_options = replace(
            recovery_step.get_step_options(),
            execute_load_attribute_maps=(),
            execute_load_attribute_map_instances=(record_load,),
        )
        options = _with_record_loads(
            self._poll_tool_task.get_step_options(), tool_call.tool_call_id
        ).on_execute_failure_proceed_to(recovery_step, recovery_options)
        return StepMovement.of(self._poll_tool_task, tool_call.tool_call_id, options=options)

    def _finish_turn_cancellation_movement(self, turn_id: str) -> StepMovement[str]:
        return StepMovement.of(self._finish_turn_cancellation, turn_id)

    def _cancel_tool_task_movement(self, tool_call: AgentToolCall) -> StepMovement[str]:
        # Retries that run out still record the call, so the cancelled turn always ends.
        options = _with_record_loads(
            self._cancel_tool_task.get_step_options(), tool_call.tool_call_id
        ).on_execute_failure_proceed_to(
            self._record_tool_outcome_unknown,
            _with_record_loads(
                self._record_tool_outcome_unknown.get_step_options(), tool_call.tool_call_id
            ),
        )
        return StepMovement.of(self._cancel_tool_task, tool_call.tool_call_id, options=options)

    def _waiting_tool_call_movement(self, tool_call: AgentToolCall) -> StepMovement[str]:
        if tool_call.status is ToolCallStatus.AWAITING_APPROVAL:
            return self._tool_approval_movement(tool_call.tool_call_id)
        return self._question_answer_movement(tool_call)

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

    def _message_step_map_loads(self) -> tuple[AttributeMap[Any], ...]:
        """Return the maps RunModel and ApplyTurnControl load whole."""
        # Without compaction the model input is every stored message, so every chunk loads.
        if self._options.compaction is None:
            return (AGENT_MESSAGES, AGENT_TOOL_CALLS, AGENT_MESSAGE_CHUNKS)
        return (AGENT_MESSAGES, AGENT_TOOL_CALLS)

    def _go_to_message_step(
        self, context: AsyncContext, step: RunModel | ApplyTurnControl
    ) -> StepDecision:
        """Move to a Step that reads the model input, loading only the chunks that input needs."""
        options = step.get_step_options()
        # Without compaction the input is every stored message, and the static options load them.
        if self._options.compaction is not None:
            index = AGENT_MESSAGE_INDEX.get(context)
            first_sequence = _model_input_first_sequence(
                index, self._runner_context_summary(context)
            )
            options = replace(
                options,
                execute_load_attribute_map_instances=_message_chunk_loads(
                    index, first_sequence, _require_agent_state(context).next_message_sequence
                ),
            )
        return go_to_many(StepMovement.of(step, None, options=options))

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
        return StepOptions(
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_MESSAGES,),
        )

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: AgentSessionStartInput,
    ) -> StepDecision:
        if input.session_settings is not None:
            try:
                check_stored_session_settings(input.session_settings)
            except ValueError as error:
                # Every retry would fail the same way, so the session fails now.
                return force_fail(f"{INVALID_START_SESSION_SETTINGS_REASON}: {error}")
            if input.session_settings:
                SESSION_SETTINGS.set(context, input.session_settings)
        AGENT_STATE.set(context, AgentState(status=AgentSessionStatus.WAITING_FOR_USER_MESSAGE))
        AGENT_USAGE.set(context, AgentUsage())
        PENDING_INTERRUPTS.set(context, [])
        if input.initial_message is None or not input.initial_message.strip():
            return go_to(self._session._await_user_message, None)
        return self._session._begin_turn(
            context,
            [AgentUserMessage(text=input.initial_message)],
            self.get_step_type(),
            session_settings=input.session_settings,
        )


class AwaitUserMessage(Step[None]):
    """Wait for a steer, a queued user message, or a plan execution."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # The lock keeps a turn from starting while cancel_turn decides about the last one.
        return StepOptions(
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
            execute_lock_attributes=(TURN_CANCELLATION_REQUEST.lock(),),
            execute_load_attribute_maps=(AGENT_MESSAGES,),
        )

    def wait_for(self, context: Context, input: None) -> Wait:
        return self._session._wait_for_user_message(context)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        if TURN_CONTROL_REQUESTS.results(context):
            return self._session._go_to_message_step(context, self._session._apply_turn_control)
        return self._session._start_user_message_turn(context, self.get_step_type())


# RunModel always receives None. A failure route's input type must match each Step routing to it.
TurnFailureRouteInput = CompactContextInput | None


class RunModel(Step[TurnFailureRouteInput]):
    """Make one model call of the active turn through the framework runner."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # Movements add the chunks of the model input; see AgentSessionFlow._go_to_message_step.
        limit = self._session._options.run_model_limit
        return replace(
            _MODEL_CALL_STEP_OPTIONS,
            execute_method_timeout=limit.method_timeout,
            execute_retry=RetryPolicy(
                maximum_attempts=limit.maximum_attempts, total_duration=limit.total_duration
            ),
            execute_load_attribute_maps=self._session._message_step_map_loads(),
        ).on_execute_failure_proceed_to(self._session._record_turn_failure)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: TurnFailureRouteInput,
    ) -> StepDecision:
        return await self._session._run_model_step(context)


class CompactContext(Step[TurnFailureRouteInput]):
    """Summarize the messages through a sequence for the model input, then call the model."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # The summary is one model call, so it gets RunModel's options and failure route.
        return replace(
            _MODEL_CALL_STEP_OPTIONS,
            execute_load_attribute_maps=(AGENT_MESSAGES,),
        ).on_execute_failure_proceed_to(self._session._record_turn_failure)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: TurnFailureRouteInput,
    ) -> StepDecision:
        if input is None:
            raise TypeError("CompactContext needs a CompactContextInput")
        return await self._session._compact_context_step(context, input)


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


class PollToolTask(Step[str]):
    """Poll one tool's remote task after its poll interval, until the task finishes."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # No lock and no ToolCallCancellations: this Step owns the record; a started task runs on.
        return StepOptions(
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
            execute_retry=RetryPolicy(maximum_attempts=3),
            wait_for_load_attribute_maps=(AGENT_TOOL_CALLS,),
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
        ).on_execute_failure_proceed_to(self._session._record_tool_failure)

    def wait_for(self, context: Context, input: str) -> Wait:
        return self._session._wait_for_tool_task_poll(context, input)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        return await self._session._poll_task_once(context, input)


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
    """Wait for every tool call result of the model response, then call the model again.

    A steer wakes it early: it cancels the turn's pending interrupts and
    Timers, and waits again. After a steer or ``cancel_turn``, once every
    result is in, it moves to ``ApplyTurnControl`` instead of ``RunModel``.
    """

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # A control wake rewrites PendingInterrupts, which interrupt RPCs also write.
        return StepOptions(
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
            execute_lock_attributes=(PENDING_INTERRUPTS.lock(),),
        )

    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.any_of(
            TOOL_CALL_RESULTS.for_n(input),
            TURN_CONTROL_REQUESTS.for_range(at_least=1, at_most=ALL_PENDING_MESSAGES),
        )

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: int,
    ) -> StepDecision:
        return self._session._resume_turn_after_tool_calls(context, input)


class ApplyTurnControl(Step[None]):
    """End a steered or cancelled turn, then start a turn with the steered messages."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # The lock keeps a turn from starting while cancel_turn decides about the last one.
        return StepOptions(
            execute_retry=RECOVERY_RETRY,
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=self._session._message_step_map_loads(),
            execute_lock_attributes=(TURN_CANCELLATION_REQUEST.lock(),),
        )

    def wait_for(self, context: Context, input: None) -> Wait:
        # Both conditions are non-blocking: the wait consumes every steered message and stale wake.
        return Wait.all_of(
            STEERED_USER_MESSAGES.at_most(ALL_PENDING_MESSAGES),
            TURN_CONTROL_REQUESTS.at_most(ALL_PENDING_MESSAGES),
        )

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        return self._session._apply_turn_control_requests(context)


class RecordTurnFailure(Step[TurnFailureRouteInput]):
    """Record a failed turn and wait for the next user message."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # An incremental runner reads the turn's stored messages to finish its transcript.
        # A failure route has no movement options, so it loads every retained chunk.
        loads: tuple[AttributeMap[Any], ...] = (
            (AGENT_TOOL_CALLS, AGENT_MESSAGES, AGENT_MESSAGE_CHUNKS)
            if isinstance(self._session.runner, UnfinishedTurnMessageBuilder)
            else (AGENT_TOOL_CALLS, AGENT_MESSAGES)
        )
        return StepOptions(
            execute_retry=RECOVERY_RETRY,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=loads,
        )

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: TurnFailureRouteInput,
    ) -> StepDecision:
        return self._session._fail_active_turn(context)


class FinishTurnCancellation(Step[str]):
    """Finish a turn that ``cancel_turn`` stopped, from the state that committed.

    ``cancel_turn`` cancels the Steps that call a model or wait, then moves
    here with the turn ID. When the model call or compaction was cancelled, it
    moves to ``ApplyTurnControl``. Otherwise ``ResumeTurn`` still waits: this
    Step records each call whose wait was cancelled, hands each abandoned task
    to ``CancelToolTask``, and ends, so ``ResumeTurn`` ends the turn once every
    started tool Step finishes.
    """

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        # The cancel_turn RPC holds both locks, so this Step reads the state that RPC committed.
        return StepOptions(
            execute_retry=RECOVERY_RETRY,
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
            execute_lock_attributes=(
                TURN_CANCELLATION_REQUEST.lock(),
                PENDING_INTERRUPTS.lock(),
            ),
        )

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        return self._session._finish_cancelled_turn(context, input)


class CancelToolTask(Step[str]):
    """Ask the runner to cancel one task that ``cancel_turn`` abandoned, then record it."""

    def __init__(self, session: AgentSessionFlow) -> None:
        self._session = session

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_method_timeout=timedelta(seconds=30),
            execute_retry=RetryPolicy(maximum_attempts=3, total_duration=timedelta(minutes=2)),
            execute_durability=StepDurability.SYNC,
            execute_load_attribute_maps=(AGENT_TOOL_CALLS,),
        ).on_execute_failure_proceed_to(self._session._record_tool_outcome_unknown)

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: str,
    ) -> StepDecision:
        return await self._session._cancel_abandoned_tool_task(context, input)


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


def _write_plan_updated(events: AgentEventWriter, plan: AgentPlan) -> None:
    events.write_event(
        AgentEventType.PLAN_UPDATED,
        {"revision": plan.revision, "status": str(plan.status), "task_count": len(plan.tasks)},
    )


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
    cancellation_reason: ToolCallCancellationReason | None = None,
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
            "cancellation_reason": None
            if cancellation_reason is None
            else str(cancellation_reason),
        },
    )


def _write_interrupt_cancelled(
    events: AgentEventWriter,
    interrupt_id: str,
    kind: InterruptKind,
    reason: ToolCallCancellationReason,
) -> None:
    _write_interrupt_resolved(
        events,
        interrupt_id,
        kind,
        AgentInterruptResolution(
            approved=False, rejection_message=TOOL_CALL_CANCELLATION_RESULTS[reason]
        ),
        reason,
    )


def _turn_control_reason(
    context: Context, turn_id: str | None
) -> ToolCallCancellationReason | None:
    """Return why the turn ends at this boundary, or None; reads committed state only."""
    if turn_id is None:
        return None
    request = cast(TurnCancellationRequest | None, TURN_CANCELLATION_REQUEST.get(context))
    # Turn IDs never repeat, so a request for an earlier turn never matches.
    if request is not None and request.turn_id == turn_id:
        return ToolCallCancellationReason.TURN_CANCELLED
    if STEERED_USER_MESSAGES.size(context) > 0:
        return ToolCallCancellationReason.STEERED
    return None


def _is_cancellable_by_turn_control(tool_call: AgentToolCall) -> bool:
    """Report whether steering or cancel_turn may abandon a call that waits in a session Step."""
    # Each of these waits for a person or a Timer; the call has taken no effect. A task's question
    # waits for a person too, but its task has started; a WAITING_FOR_TASK record is never listed.
    return tool_call.status in (
        ToolCallStatus.AWAITING_APPROVAL,
        ToolCallStatus.WAITING_FOR_TIMER,
    ) or (tool_call.status is ToolCallStatus.AWAITING_ANSWER and tool_call.task_id is None)


def _is_turn_cancelled(context: Context, turn_id: str) -> bool:
    return _turn_control_reason(context, turn_id) is ToolCallCancellationReason.TURN_CANCELLED


def _is_abandoned_tool_task(tool_call: AgentToolCall) -> bool:
    """Report whether cancel_turn abandons this call's started task."""
    return tool_call.task_id is not None and tool_call.status in (
        ToolCallStatus.WAITING_FOR_TASK,
        ToolCallStatus.AWAITING_ANSWER,
    )


def _waiting_interrupt(tool_call: AgentToolCall) -> tuple[str, InterruptKind] | None:
    """Return the interrupt a waiting record's Step waits on, or None."""
    if tool_call.status is ToolCallStatus.AWAITING_APPROVAL:
        return tool_call.tool_call_id, InterruptKind.TOOL_APPROVAL
    if tool_call.status is ToolCallStatus.AWAITING_ANSWER:
        return _question_interrupt_id(tool_call), InterruptKind.QUESTION
    return None


def _cancelled_tool_call(
    tool_call: AgentToolCall, reason: ToolCallCancellationReason
) -> AgentToolCall:
    return replace(
        tool_call,
        status=ToolCallStatus.REJECTED,
        rejection_message=TOOL_CALL_CANCELLATION_RESULTS[reason],
        cancellation_reason=reason,
        **_NO_PENDING_TOOL_QUESTION,
    )


def _publish_tool_call_cancellation(
    context: Context, tool_call_id: str, reason: ToolCallCancellationReason
) -> None:
    TOOL_CALL_CANCELLATIONS.publish(context, tool_call_id, ToolCallCancellation(reason=reason))


def _pending_user_messages(
    context: Context, channel: Channel[AgentUserMessage]
) -> list[PendingUserMessage]:
    return [
        PendingUserMessage(message_id=pending.message_id, message=pending.value)
        for pending in channel.pending_messages(context)
    ]


def _find_queued_message(context: Context, message_id: str) -> PendingUserMessage | None:
    return next(
        (
            queued
            for queued in _pending_user_messages(context, QUEUED_USER_MESSAGES)
            if queued.message_id == message_id
        ),
        None,
    )


def _count_messages_after_user_messages(messages: Sequence[AgentMessage], turn_id: str) -> int:
    """Count a turn's stored messages after the user messages that started it."""
    turn_messages = [message for message in messages if message.turn_id == turn_id]
    leading_user_count = next(
        (
            index
            for index, message in enumerate(turn_messages)
            if message.role is not MessageRole.USER
        ),
        len(turn_messages),
    )
    return len(turn_messages) - leading_user_count


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


def _record_tool_task_started(
    tool_call: AgentToolCall, started: ToolTaskStarted, *, has_poller: bool
) -> AgentToolCall:
    """Return the record of a started task: waiting for it, or final when it cannot be polled."""
    if not has_poller:
        return _record_tool_task_defect(tool_call, MISSING_TOOL_TASK_POLLER)
    if not isinstance(started.task_id, str) or not started.task_id.strip():
        return _record_tool_task_defect(tool_call, BLANK_TOOL_TASK_ID)
    interval = _tool_task_poll_interval(started.poll_interval_seconds)
    if interval is None:
        return _record_tool_task_defect(tool_call, INVALID_TOOL_TASK_POLL_INTERVAL)
    # The tool consumed any continuation of its own questions when it ran again.
    return replace(
        tool_call,
        status=ToolCallStatus.WAITING_FOR_TASK,
        task_id=started.task_id,
        task_poll_interval_seconds=interval,
        input_continuation=None,
        **_NO_PENDING_TOOL_QUESTION,
    )


def _record_tool_task_defect(tool_call: AgentToolCall, error: str) -> AgentToolCall:
    """Return a final record for a task that cannot be polled; the task may still take effect."""
    status = (
        ToolCallStatus.FAILED
        if tool_call.effect is ToolEffect.READ
        else ToolCallStatus.OUTCOME_UNKNOWN
    )
    return replace(tool_call, status=status, error=error, **_NO_PENDING_TOOL_QUESTION)


def _tool_task_poll_interval(seconds: object) -> int | None:
    """Return a poll interval in whole seconds, 1 to a day, or ``None`` if it is not a number."""
    if isinstance(seconds, bool) or not isinstance(seconds, int | float):
        return None
    if not math.isfinite(seconds):
        return None
    # Dex Timers take whole seconds, and a zero interval would poll without a pause.
    return min(max(1, math.ceil(seconds)), _MAXIMUM_TOOL_TASK_POLL_INTERVAL_SECONDS)


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


@dataclass(frozen=True)
class _MessageStorage:
    """The options that decide where messages are stored and when chunks are deleted."""

    window_size: int
    chunk_size: int
    chunk_max_bytes: int
    # None when nothing is deleted: no compaction, or no limit.
    retained_message_limit: int | None


_MESSAGE_CODEC = CodecRegistry().resolve(AgentMessage)
# The bytes of {"messages":[...]} around the messages, without the commas between them.
_CHUNK_ENVELOPE_BYTES = len('{"messages":[]}')


def _append_messages(
    context: Context,
    state: AgentState,
    turn_id: str,
    messages: Sequence[FrameworkMessage],
    storage: _MessageStorage,
    *,
    starts_turn: bool = False,
) -> AgentState:
    """Store messages, move full chunks out of the window, and apply retention.

    Every caller loads the whole ``AgentMessages`` window. A message too large to
    share a chunk gets its own chunk at once, so no later commit can fail on it.
    The index changes only when chunks are written or deleted.
    """
    if not messages and not starts_turn:
        AGENT_STATE.set(context, state)
        return state
    stored_index = AGENT_MESSAGE_INDEX.get(context)
    index = stored_index or _EMPTY_MESSAGE_INDEX
    if not starts_turn and state.active_turn_id is not None:
        _record_turn_reply_sequence(context, index, state, state.active_turn_id)
    sequence = state.next_message_sequence
    created_at = _rfc3339(datetime.now(UTC))
    chunks = list(index.chunks)
    for message in messages:
        stored = AgentMessage(
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
        )
        if _CHUNK_ENVELOPE_BYTES + _message_bytes(stored) > storage.chunk_max_bytes:
            # The first store writes the largest value this message ever needs.
            AGENT_MESSAGE_CHUNKS.set(
                context, message_sequence_key(sequence), AgentMessageChunk(messages=[stored])
            )
            chunks.append(AgentMessageChunkRange(first_sequence=sequence, last_sequence=sequence))
        else:
            AGENT_MESSAGES.set(context, message_sequence_key(sequence), stored)
        sequence += 1
    if len(chunks) != len(index.chunks):
        index = replace(index, chunks=chunks)
    state = replace(state, next_message_sequence=sequence)
    AGENT_STATE.set(context, state)
    if starts_turn:
        TURN_REPLY_SEQUENCE.set(context, TurnReplySequence(turn_id=turn_id, sequence=sequence))
    index = _roll_over_message_window(context, index, sequence, storage)
    index = _delete_retired_message_chunks(
        context, index, sequence, _context_summary(context), storage.retained_message_limit
    )
    if index != (stored_index or _EMPTY_MESSAGE_INDEX):
        AGENT_MESSAGE_INDEX.set(context, index)
    return state


_EMPTY_MESSAGE_INDEX = AgentMessageIndex(chunks=[], first_sequence=1, window_first_sequence=1)


def _record_turn_reply_sequence(
    context: Context, index: AgentMessageIndex, state: AgentState, turn_id: str
) -> None:
    """Record where a turn that a 0.1.0 Worker started has its replies, before chunks hide them."""
    recorded = TURN_REPLY_SEQUENCE.get(context)
    if recorded is not None and recorded.turn_id == turn_id:
        return
    turn_messages = _read_stored_turn_messages(context, index, state, turn_id)
    TURN_REPLY_SEQUENCE.set(
        context,
        TurnReplySequence(
            turn_id=turn_id,
            sequence=state.next_message_sequence
            - _count_messages_after_user_messages(turn_messages, turn_id),
        ),
    )


def _message_bytes(message: AgentMessage) -> int:
    return len(str(_MESSAGE_CODEC.encode(message).data).encode())


def _roll_over_message_window(
    context: Context, index: AgentMessageIndex, next_sequence: int, storage: _MessageStorage
) -> AgentMessageIndex:
    """Move the window's oldest messages into immutable chunks, a bounded amount per commit."""
    window_first = index.window_first_sequence
    chunks = list(index.chunks)
    direct_starts = {
        chunk.first_sequence for chunk in chunks if chunk.first_sequence >= window_first
    }
    window_count = next_sequence - window_first - len(direct_starts)
    moved_bytes = 0
    moved_chunks = 0
    while window_first in direct_starts:
        window_first += 1
    while (
        window_count > storage.window_size and moved_chunks < MESSAGE_ROLLOVER_MAX_CHUNKS_PER_COMMIT
    ):
        messages: list[AgentMessage] = []
        chunk_bytes = _CHUNK_ENVELOPE_BYTES - 1
        while (
            window_first + len(messages) < next_sequence
            and window_first + len(messages) not in direct_starts
            and len(messages) < storage.chunk_size
        ):
            message = _read_window_message(context, window_first + len(messages))
            grown_bytes = chunk_bytes + 1 + _message_bytes(message)
            if messages and grown_bytes > storage.chunk_max_bytes:
                break
            messages.append(message)
            chunk_bytes = grown_bytes
        if moved_chunks and moved_bytes + chunk_bytes > MESSAGE_ROLLOVER_MAX_BYTES_PER_COMMIT:
            break
        AGENT_MESSAGE_CHUNKS.set(
            context, message_sequence_key(window_first), AgentMessageChunk(messages=messages)
        )
        for message in messages:
            AGENT_MESSAGES.delete(context, message_sequence_key(message.sequence))
        last_sequence = window_first + len(messages) - 1
        bisect.insort(
            chunks,
            AgentMessageChunkRange(first_sequence=window_first, last_sequence=last_sequence),
            key=lambda chunk: chunk.first_sequence,
        )
        window_first = last_sequence + 1
        while window_first in direct_starts:
            window_first += 1
        window_count -= len(messages)
        moved_bytes += chunk_bytes
        moved_chunks += 1
    if window_first == index.window_first_sequence and chunks == index.chunks:
        return index
    return replace(
        index,
        chunks=chunks,
        first_sequence=_archived_first_sequence(chunks, window_first),
        window_first_sequence=window_first,
    )


def _archived_first_sequence(chunks: Sequence[AgentMessageChunkRange], window_first: int) -> int:
    """Return the oldest stored message: the first archived chunk's, else the window's."""
    if chunks and chunks[0].first_sequence < window_first:
        return chunks[0].first_sequence
    return window_first


def _delete_retired_message_chunks(
    context: Context,
    index: AgentMessageIndex,
    next_sequence: int,
    summary: ContextSummary | None,
    retained_message_limit: int | None,
) -> AgentMessageIndex:
    """Delete the oldest fully summarized chunks while at least the limit of messages stays."""
    if retained_message_limit is None or summary is None:
        return index
    chunks = list(index.chunks)
    while (
        chunks
        and chunks[0].first_sequence < index.window_first_sequence
        and chunks[0].last_sequence <= summary.last_sequence
        and next_sequence - (chunks[0].last_sequence + 1) >= retained_message_limit
    ):
        AGENT_MESSAGE_CHUNKS.delete(context, message_sequence_key(chunks[0].first_sequence))
        chunks.pop(0)
    if len(chunks) == len(index.chunks):
        return index
    return replace(
        index,
        chunks=chunks,
        first_sequence=_archived_first_sequence(chunks, index.window_first_sequence),
    )


def _first_stored_sequence(index: AgentMessageIndex | None) -> int:
    return 1 if index is None else index.first_sequence


def _model_input_first_sequence(
    index: AgentMessageIndex | None, summary: ContextSummary | None
) -> int:
    """Return the first message of the model input: after the summary, and never a deleted one."""
    after_summary = 1 if summary is None else summary.last_sequence + 1
    return max(_first_stored_sequence(index), after_summary)


def _find_message_chunk(index: AgentMessageIndex, sequence: int) -> AgentMessageChunkRange | None:
    """Return the range of the kept chunk that holds a sequence, or None for a window message."""
    position = bisect.bisect_right([chunk.first_sequence for chunk in index.chunks], sequence) - 1
    if position < 0 or index.chunks[position].last_sequence < sequence:
        return None
    return index.chunks[position]


def _message_chunk_loads(
    index: AgentMessageIndex | None, start: int, end: int
) -> tuple[AttributeMapLoad, ...]:
    """Return the loads of the chunks that hold any message with a sequence in [start, end)."""
    if index is None:
        return ()
    return tuple(
        AGENT_MESSAGE_CHUNKS.load(message_sequence_key(chunk.first_sequence))
        for chunk in index.chunks
        if chunk.last_sequence >= start and chunk.first_sequence < end
    )


def _read_window_message(context: Context, sequence: int) -> AgentMessage:
    message = AGENT_MESSAGES.get(context, message_sequence_key(sequence))
    if message is None:
        raise RuntimeError(f"AgentMessages has no message {message_sequence_key(sequence)}")
    return message


def _read_message_chunk(context: Context, chunk_range: AgentMessageChunkRange) -> AgentMessageChunk:
    key = message_sequence_key(chunk_range.first_sequence)
    try:
        chunk = AGENT_MESSAGE_CHUNKS.get(context, key)
    except Exception as error:
        raise RuntimeError(f"AgentMessageChunks instance {key} is not loaded") from error
    expected = list(range(chunk_range.first_sequence, chunk_range.last_sequence + 1))
    if chunk is None or [message.sequence for message in chunk.messages] != expected:
        raise RuntimeError(
            f"AgentMessageChunks instance {key} does not hold messages "
            f"{chunk_range.first_sequence} through {chunk_range.last_sequence}"
        )
    return chunk


def _read_stored_turn_messages(
    context: Context, index: AgentMessageIndex | None, state: AgentState, turn_id: str
) -> list[AgentMessage]:
    """Return the turn's newest stored messages, read back until another turn's or a gap."""
    turn_messages: list[AgentMessage] = []
    sequence = state.next_message_sequence - 1
    while sequence >= _first_stored_sequence(index):
        try:
            [message] = _read_messages(context, sequence, sequence + 1)
        except RuntimeError:
            # A chunk this Step did not load: the turn's older messages are not needed.
            break
        if message.turn_id != turn_id:
            break
        turn_messages.append(message)
        sequence -= 1
    turn_messages.reverse()
    return turn_messages


def _count_stored_turn_replies(context: Context, state: AgentState, turn_id: str) -> int:
    """Count the turn's stored messages after the user messages that started it."""
    recorded = TURN_REPLY_SEQUENCE.get(context)
    if recorded is not None and recorded.turn_id == turn_id:
        return state.next_message_sequence - recorded.sequence
    # A turn that a 0.1.0 Worker started and no message has been stored in since.
    index = AGENT_MESSAGE_INDEX.get(context)
    return _count_messages_after_user_messages(
        _read_stored_turn_messages(context, index, state, turn_id), turn_id
    )


def _read_messages(context: Context, start: int, end: int) -> list[AgentMessage]:
    """Return the stored messages with sequences in [start, end) from the window and the chunks."""
    index = AGENT_MESSAGE_INDEX.get(context)
    start = max(start, _first_stored_sequence(index))
    messages: list[AgentMessage] = []
    chunk: AgentMessageChunk | None = None
    chunk_range: AgentMessageChunkRange | None = None
    for sequence in range(start, end):
        if chunk is not None and chunk_range is not None and sequence <= chunk_range.last_sequence:
            messages.append(chunk.messages[sequence - chunk_range.first_sequence])
            continue
        found = None if index is None else _find_message_chunk(index, sequence)
        if found is None:
            messages.append(_read_window_message(context, sequence))
            continue
        chunk_range = found
        chunk = _read_message_chunk(context, chunk_range)
        messages.append(chunk.messages[sequence - chunk_range.first_sequence])
    return messages


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
    return _recovery_error_detail(context.recovery_error)


def _recovery_error_detail(error: RecoveryErrorInfo | None) -> str:
    if error is None:
        return "unknown failure"
    return error.detail or error.error_type


def _is_state_limit_failure(error: RecoveryErrorInfo | None) -> bool:
    """Report whether a Step failed because its commit would exceed the Flow's state limit."""
    # The Server gives this failure no code of its own; a value over its own limit shares the type.
    return (
        error is not None
        and error.error_type == STATE_LIMIT_ERROR_TYPE
        and STATE_LIMIT_ERROR_TEXT in error.detail
    )


def _recovered_tool_call(
    tool_call: AgentToolCall,
    recovery_error: RecoveryErrorInfo | None,
    status: ToolCallStatus,
) -> AgentToolCall:
    """Return the final record that a tool Step's failure route writes."""
    # An abandoned task keeps the text CancelToolTask records, whatever made its attempts fail.
    error = (
        TOOL_TASK_ABANDONED_ERROR
        if _is_abandoned_tool_task(tool_call) and tool_call.cancellation_reason is not None
        else _recovery_error_detail(recovery_error)
    )
    if _is_tool_call_not_run(tool_call, recovery_error):
        return replace(
            tool_call,
            status=ToolCallStatus.FAILED,
            error=f"{TOOL_CALL_NOT_RUN_ERROR_PREFIX}: {error}",
        )
    return replace(tool_call, status=status, error=error)


def _is_tool_call_not_run(
    tool_call: AgentToolCall, recovery_error: RecoveryErrorInfo | None
) -> bool:
    """Report whether the failed Step certainly never ran the call's tool."""
    # Polls and cancellations follow a tool that already ran, as does an answered question's call.
    return (
        recovery_error is not None
        and recovery_error.attempt_progress is StepAttemptProgress.NOT_STARTED
        and tool_call.status is ToolCallStatus.APPROVED
        and not tool_call.input_answers
    )


def _optional_agent_state(context: Context) -> AgentState | None:
    return cast(AgentState | None, AGENT_STATE.get(context))


def _interaction_mode(state: AgentState) -> InteractionMode:
    # AgentState replaces None with CHAT when it is built.
    return state.interaction_mode or InteractionMode.CHAT


def _optional_agent_plan(context: Context) -> AgentPlan | None:
    return cast(AgentPlan | None, AGENT_PLAN.get(context))


def _is_plan_mode_runner(runner: object) -> bool:
    return isinstance(runner, PlanModeRunner) and runner.supports_plan_mode is True


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


def _context_summary(context: Context) -> ContextSummary | None:
    return cast(ContextSummary | None, CONTEXT_SUMMARY.get(context))


def _session_settings(context: Context) -> dict[str, Any] | None:
    return cast(dict[str, Any] | None, SESSION_SETTINGS.get(context))


def _turn_session_settings(context: Context) -> dict[str, Any]:
    stored = cast(TurnSessionSettings | None, TURN_SESSION_SETTINGS.get(context))
    state = _optional_agent_state(context)
    # A turn that a Worker without settings started, such as 0.1.0, has none, even
    # when an earlier turn left its copy.
    if stored is None or state is None or stored.turn_id != state.active_turn_id:
        return {}
    return stored.session_settings
