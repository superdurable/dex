# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Wire value types of the shared agent session Flow.

Every type here is a JSON wire contract shared by the Python and TypeScript
cores. Field names are snake_case in both languages. A UI or another Dex
client can decode them without the framework adapter that wrote them.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import StrEnum


class AgentSessionStatus(StrEnum):
    """Name what an agent session is doing now.

    The session Flow stores the status in ``AgentState.status``. A UI reads it
    from ``get_agent_snapshot`` or with ``AsyncClient.get_attributes``. The
    values are JSON strings on the wire.

    Attributes:
        WAITING_FOR_USER_MESSAGE: The session waits for the next user message.
        RUNNING_MODEL: ``RunModel`` makes one model call of the active turn.
        RUNNING_TOOLS: The tool call Steps of the active turn run, or wait for
            approval, in parallel; ``ResumeTurn`` waits for their results.
    """

    WAITING_FOR_USER_MESSAGE = "waiting_for_user_message"
    RUNNING_MODEL = "running_model"
    RUNNING_TOOLS = "running_tools"


class MessageRole(StrEnum):
    """Name the author of one stored agent message.

    Attributes:
        USER: A message the user sent.
        ASSISTANT: Model output, including a tool call request.
        TOOL: A tool result returned to the model.
    """

    USER = "user"
    ASSISTANT = "assistant"
    TOOL = "tool"


class ToolEffect(StrEnum):
    """Classify what a tool call does to the world outside the session.

    Every tool call runs in its own Step, and the effect picks that Step.
    ``READ`` and ``IDEMPOTENT_WRITE`` tools run in ``CallTool`` with up to
    three attempts and a stable idempotency key. ``NON_IDEMPOTENT_WRITE`` and
    ``UNCLASSIFIED`` tools run in ``CallSingleAttemptTool`` exactly once; a
    crash during the call records ``outcome_unknown``. The values are JSON
    strings on the wire.

    Attributes:
        READ: The tool only reads. Running it again is harmless.
        IDEMPOTENT_WRITE: The tool writes, and repeating it with the same
            idempotency key has no further effect.
        NON_IDEMPOTENT_WRITE: The tool writes, and repeating it can repeat the
            effect.
        UNCLASSIFIED: Nothing is known about the tool. It is treated like a
            non-idempotent write.
    """

    READ = "read"
    IDEMPOTENT_WRITE = "idempotent_write"
    NON_IDEMPOTENT_WRITE = "non_idempotent_write"
    UNCLASSIFIED = "unclassified"


class ToolCallStatus(StrEnum):
    """Name the lifecycle state of one deferred tool call record.

    ``SUCCEEDED``, ``FAILED``, ``REJECTED``, and ``OUTCOME_UNKNOWN`` are final.
    A runner resuming a turn reads final records and returns their results to
    the model instead of running the tools again. Every other status is
    non-final, and runners never see a non-final record. The set of final
    statuses never grows, so an exhaustive mapping of final statuses stays
    complete.

    Attributes:
        AWAITING_APPROVAL: The call waits for ``resolve_interrupt``.
        APPROVED: The call may run; its tool Step has not finished yet.
        AWAITING_ANSWER: The call waits for ``answer_question``. It is an
            ``ask_user`` call, or a tool call that returned ``ToolInputRequested``.
        WAITING_FOR_TIMER: A ``durable_wait`` call waits for its Timer until
            ``AgentToolCall.wait_until``.
        REJECTED: The user or an expired deadline rejected the call. It never ran.
        SUCCEEDED: The tool ran and returned ``output``.
        FAILED: The tool raised ``ToolExecutionError``; ``error`` holds the
            message for the model to see.
        OUTCOME_UNKNOWN: The tool Step ended without a result, through a
            Worker crash, a timeout, or another exception after its attempts
            ran out, so the effect may or may not have happened.
    """

    AWAITING_APPROVAL = "awaiting_approval"
    APPROVED = "approved"
    AWAITING_ANSWER = "awaiting_answer"
    WAITING_FOR_TIMER = "waiting_for_timer"
    REJECTED = "rejected"
    SUCCEEDED = "succeeded"
    FAILED = "failed"
    OUTCOME_UNKNOWN = "outcome_unknown"

    @property
    def is_final(self) -> bool:
        """Report whether the record has a result the model can see.

        Returns:
            ``True`` for ``REJECTED``, ``SUCCEEDED``, ``FAILED``, and
            ``OUTCOME_UNKNOWN``; ``False`` while the call waits or may still run.
        """
        return self in (
            ToolCallStatus.REJECTED,
            ToolCallStatus.SUCCEEDED,
            ToolCallStatus.FAILED,
            ToolCallStatus.OUTCOME_UNKNOWN,
        )


class InterruptKind(StrEnum):
    """Name why a turn paused for a person.

    Attributes:
        TOOL_APPROVAL: A tool call needs approval before it runs. Answer it
            with ``resolve_interrupt``.
        QUESTION: The model or a tool asked the user a question. Answer it with
            ``answer_question``, or decline it with ``resolve_interrupt`` and
            ``approved=False``.
    """

    TOOL_APPROVAL = "tool_approval"
    QUESTION = "question"


@dataclass(frozen=True, kw_only=True)
class AgentSessionStartInput:
    """Start one agent session Flow, optionally with its first user message.

    Pass it as the input of ``AsyncClient.start_flow`` for an
    ``AgentSessionFlow``. The Flow ID you choose is the session ID.

    Example:
        >>> await client.start_flow(flow, "session-42", AgentSessionStartInput(
        ...     initial_message="Summarize my open tickets"))

    Attributes:
        initial_message: Text of the first user message. ``None`` or
            whitespace-only text starts the session in ``AwaitUserMessage``.
    """

    initial_message: str | None = None


@dataclass(frozen=True, kw_only=True)
class AgentState:
    """Describe the current position of one agent session.

    The ``AgentState`` Attribute holds one value. Every Step that changes the
    session position replaces it.

    Attributes:
        status: What the session is doing now.
        turn_count: Number of turns that completed or failed.
        active_turn_id: ``"turn-<n>"`` of the running turn, or ``None`` between
            turns. ``n`` is ``turn_count + 1`` when the turn starts.
        next_message_sequence: Sequence the next stored message receives.
            Sequences start at 1 and have no gaps.
        last_turn_error: Recovery error text of the last finished turn when it
            failed, or ``None`` when it completed.
    """

    status: AgentSessionStatus
    turn_count: int = 0
    active_turn_id: str | None = None
    next_message_sequence: int = 1
    last_turn_error: str | None = None


@dataclass(frozen=True, kw_only=True)
class AgentMessage:
    """Store one framework item of the session conversation.

    The ``AgentMessages`` AttributeMap holds one value per message, keyed by
    its sequence as a 12-digit zero-padded decimal. ``framework_item`` is the
    replay truth: the framework adapter writes it and reads it back unchanged
    to rebuild framework input. The other fields are a display projection.

    Attributes:
        sequence: Position in the session, starting at 1.
        turn_id: ``"turn-<n>"`` of the turn that produced the message.
        role: Author of the message.
        text: Display text, or ``None`` for an item without text.
        tool_call_id: Tool call ID for a tool request or result item.
        tool_name: Tool name for a tool request or result item.
        tool_arguments: Tool arguments as JSON text for a tool request item.
        tool_output: Tool result text for a tool result item.
        framework_item: Framework-native JSON text, replayed unchanged.
        created_at: RFC 3339 time when the session stored the message.
    """

    sequence: int
    turn_id: str
    role: MessageRole
    text: str | None = None
    tool_call_id: str | None = None
    tool_name: str | None = None
    tool_arguments: str | None = None
    tool_output: str | None = None
    framework_item: str
    created_at: str


@dataclass(frozen=True, kw_only=True)
class QuestionAnswer:
    """Record one answer to a question that a tool asked through ``ToolInputRequested``.

    ``AwaitQuestionAnswer`` appends one to ``AgentToolCall.input_answers`` when
    the user answers, and the tool receives them in
    ``DeferredToolExecution.input_answers`` when it runs again.

    Attributes:
        interrupt_id: ``"<tool_call_id>-input-<n>"`` of the answered question,
            where ``n`` counts the tool's questions from 1.
        question: The question text the tool asked.
        answer: The user's answer, exactly as sent to ``answer_question``.
    """

    interrupt_id: str
    question: str
    answer: str


@dataclass(frozen=True, kw_only=True)
class AgentToolCall:
    """Record one tool call that a turn deferred to a Dex Step.

    The ``AgentToolCalls`` AttributeMap holds one record per deferred call,
    keyed by tool call ID. Records exist only while their turn runs. When the
    turn completes or fails, the session deletes them, because the results are
    already conversation messages.

    Attributes:
        tool_call_id: Framework tool call ID. It is non-empty and has no ``/``.
        turn_id: ``"turn-<n>"`` of the turn that deferred the call.
        tool_name: Name of the tool the model asked for.
        arguments: Tool arguments as JSON text.
        effect: Effect that decides which Step runs the call.
        requires_approval: Whether ``resolve_interrupt`` must approve the call.
        status: Lifecycle state of the call.
        output: Tool output text after ``SUCCEEDED``.
        error: The ``ToolExecutionError`` message after ``FAILED``, or the
            recovery detail of the failed Step after ``OUTCOME_UNKNOWN``.
        rejection_message: Message after ``REJECTED``, or ``None`` when the
            person gave none.
        deferral_order: Zero-based order in which the turn deferred the call.
            Approved calls run in this order. This field extends the planned
            contract so the order survives storage in an AttributeMap.
        tool_source_reference: ``DeferredToolCall.tool_source_reference``,
            opaque text the tool's source stored with the call, or ``None``.
        trace_context: W3C ``traceparent`` of the activation root that the
            call's ``execute_tool`` spans join, or ``None``. The session sets it
            when ``RunModel`` defers the call and again when an approval starts
            a new activation, so parallel activations never swap tool spans.
        wait_until: RFC 3339 time when a ``durable_wait`` call's Timer ends,
            or ``None`` for every other call.
        input_continuation: Opaque text from the tool's last
            ``ToolInputRequested``, returned to the tool when it runs again
            after the answer. ``None`` when the tool gave none.
        input_answers: Answers to the questions the tool asked through
            ``ToolInputRequested``, oldest first. Empty for other calls.
        input_question: Question text of the tool's pending
            ``ToolInputRequested`` while the record is ``AWAITING_ANSWER``, or
            ``None``. It is ``None`` for an ``ask_user`` call, whose question
            is in ``arguments``. ``AwaitQuestionAnswer`` raises the question
            interrupt from it under the ``PendingInterrupts`` lock.
        input_choices: Choices of that pending ``ToolInputRequested``; empty
            for free text only or when no tool question is pending.
        input_allows_free_text: Whether that pending ``ToolInputRequested``
            accepts free text. ``True`` when no tool question is pending.
    """

    tool_call_id: str
    turn_id: str
    tool_name: str
    arguments: str
    effect: ToolEffect
    requires_approval: bool
    status: ToolCallStatus
    output: str | None = None
    error: str | None = None
    rejection_message: str | None = None
    deferral_order: int = 0
    tool_source_reference: str | None = None
    trace_context: str | None = None
    wait_until: str | None = None
    input_continuation: str | None = None
    input_answers: list[QuestionAnswer] = field(default_factory=list)
    input_question: str | None = None
    input_choices: list[str] = field(default_factory=list)
    input_allows_free_text: bool = True


@dataclass(frozen=True, kw_only=True)
class AgentInterrupt:
    """Describe one decision the session waits for a person to make.

    The ``PendingInterrupts`` Attribute holds the list of unresolved
    interrupts. A UI shows them. It answers a ``TOOL_APPROVAL`` interrupt with
    ``resolve_interrupt`` and a ``QUESTION`` interrupt with ``answer_question``.

    Attributes:
        interrupt_id: ID passed to ``resolve_interrupt`` or ``answer_question``.
            It equals the tool call ID for a tool approval and an ``ask_user``
            question, and is ``"<tool_call_id>-input-<n>"`` for the ``n``-th
            question a tool asked through ``ToolInputRequested``.
        kind: Why the turn paused.
        tool_call_id: Tool call that waits.
        tool_name: Name of that tool, ``"ask_user"`` for an ``ask_user`` question.
        arguments: Tool arguments as JSON text.
        created_at: RFC 3339 time when the interrupt was raised.
        deadline: RFC 3339 time after which the session rejects the call with
            ``"approval deadline passed"`` or ``"question deadline passed"``,
            or ``None`` for no deadline.
        question: Question text of a ``QUESTION`` interrupt, or ``None``.
        choices: Answers to offer for a ``QUESTION`` interrupt. Empty means
            free text only.
        allows_free_text: Whether ``answer_question`` accepts an answer that
            is not one of ``choices``. Always ``True`` when ``choices`` is empty.
    """

    interrupt_id: str
    kind: InterruptKind
    tool_call_id: str
    tool_name: str
    arguments: str
    created_at: str
    deadline: str | None = None
    question: str | None = None
    choices: list[str] = field(default_factory=list)
    allows_free_text: bool = True


@dataclass(frozen=True, kw_only=True)
class AgentInterruptResolution:
    """Carry one answer to an interrupt through ``InterruptResolutions``.

    ``resolve_interrupt`` and ``answer_question`` publish it to the ChannelMap
    instance named by the interrupt ID. ``AwaitToolApproval`` or
    ``AwaitQuestionAnswer`` consumes it.

    Attributes:
        approved: Whether the tool call may run, or, for a question, whether
            the user answered it.
        rejection_message: Message the model sees after a rejection, or ``None``.
        answer: The answer to a ``QUESTION`` interrupt, or ``None``.
    """

    approved: bool
    rejection_message: str | None = None
    answer: str | None = None


@dataclass(frozen=True, kw_only=True)
class ResolveInterruptRequest:
    """Answer one pending interrupt through the ``resolve_interrupt`` RPC.

    Attributes:
        interrupt_id: ``AgentInterrupt.interrupt_id`` of a pending interrupt.
        approved: ``True`` to run the tool call; ``False`` to reject it or to
            decline a question.
        rejection_message: Message the model sees after a rejection. ``None``
            makes the model see a generic rejection.
    """

    interrupt_id: str
    approved: bool
    rejection_message: str | None = None


@dataclass(frozen=True, kw_only=True)
class AnswerQuestionRequest:
    """Answer one pending question through the ``answer_question`` RPC.

    Attributes:
        interrupt_id: ``AgentInterrupt.interrupt_id`` of a pending ``QUESTION``
            interrupt.
        answer: The answer. It must not be blank. When the interrupt has
            ``choices`` and ``allows_free_text`` is ``False``, it must equal
            one of the choices exactly.
    """

    interrupt_id: str
    answer: str


@dataclass(frozen=True, kw_only=True)
class AgentUsage:
    """Count model usage, for one model call or for the whole session.

    A runner returns the usage of one model call in ``TurnCompleted`` or
    ``ToolCallsRequested``. The session adds it to the ``AgentUsage`` Attribute.
    Report zero for a count the framework does not expose.

    Attributes:
        model_calls: Number of model requests.
        input_tokens: Input tokens, including cached input tokens.
        output_tokens: Output tokens, including reasoning tokens.
        cached_input_tokens: Input tokens served from a provider cache.
        reasoning_tokens: Output tokens spent on reasoning.
    """

    model_calls: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    cached_input_tokens: int = 0
    reasoning_tokens: int = 0

    def combined_with(self, other: AgentUsage) -> AgentUsage:
        """Return the field-by-field sum of this usage and another.

        Args:
            other: Usage to add.

        Returns:
            A new ``AgentUsage``; neither input changes.
        """
        return AgentUsage(
            model_calls=self.model_calls + other.model_calls,
            input_tokens=self.input_tokens + other.input_tokens,
            output_tokens=self.output_tokens + other.output_tokens,
            cached_input_tokens=self.cached_input_tokens + other.cached_input_tokens,
            reasoning_tokens=self.reasoning_tokens + other.reasoning_tokens,
        )


@dataclass(frozen=True, kw_only=True)
class AgentUserMessage:
    """Send one user message through the ``send_user_message`` RPC.

    The RPC queues the message in the ``QueuedUserMessages`` Channel. The
    session consumes queued messages in order, one per turn, after the current
    turn ends.

    Attributes:
        text: Message text. Whitespace-only text is rejected.
    """

    text: str


@dataclass(frozen=True, kw_only=True)
class ToolCallCompleted:
    """Report that one tool call of the active turn has a final record.

    Every tool call Step publishes exactly one to the internal
    ``ToolCallResults`` Channel when it records a final status. ``ResumeTurn``
    waits for one per tool call the model requested.

    Attributes:
        tool_call_id: The tool call whose ``AgentToolCall`` record is final.
    """

    tool_call_id: str


@dataclass(frozen=True, kw_only=True)
class AgentEvent:
    """Carry one live update on the ``AgentEvents`` Stream.

    Streams are best effort. A Step retry can repeat events, and a lost
    consumer can miss them. Rebuild durable state from ``get_agent_snapshot``.

    Group live text by ``(turn_id, step_execution_id, attempt)``. Within one
    ``step_execution_id``, keep only the events of the highest ``attempt``
    seen, and drop the partial text of lower attempts. Different
    ``step_execution_id`` values of one turn are separate model call and tool
    call Steps, and each keeps its own events. After ``turn_completed`` or
    ``turn_failed``, replace the turn's live text with the stored messages.

    Attributes:
        type: One of the ``AgentEventType`` values.
        turn_id: Turn the event belongs to, or ``None``.
        attempt: One-based attempt of the Step that wrote the event.
        payload: Event-specific JSON object text.
        step_execution_id: Dex Step execution ID of the Step that wrote the
            event. All attempts of one Step execution share it. Events written
            before this field existed decode with ``""``.
    """

    type: str
    turn_id: str | None
    attempt: int
    payload: str
    step_execution_id: str = ""


@dataclass(frozen=True, kw_only=True)
class AgentSnapshot:
    """Return everything a UI needs to render one agent session.

    ``get_agent_snapshot`` returns it. The RPC reads committed state, so a
    retained closed session stays readable.

    Attributes:
        state: Current session position, or ``None`` before
            ``StartAgentSession`` committed.
        messages: The last 200 messages in sequence order.
        pending_interrupts: Unresolved interrupts.
        tool_calls: Deferred tool call records of the active turn, in
            ``deferral_order``.
        usage: Model usage of the whole session.
        queued_message_count: User messages queued for later turns.
    """

    state: AgentState | None
    messages: list[AgentMessage] = field(default_factory=list)
    pending_interrupts: list[AgentInterrupt] = field(default_factory=list)
    tool_calls: list[AgentToolCall] = field(default_factory=list)
    usage: AgentUsage = field(default_factory=AgentUsage)
    queued_message_count: int = 0


@dataclass(frozen=True, kw_only=True)
class ListAgentMessagesRequest:
    """Select one page of stored messages for ``list_agent_messages``.

    Attributes:
        before_sequence: Return messages with a smaller sequence. ``None``
            starts after the newest message.
        limit: Maximum messages to return, clamped to 1 through 200.
    """

    before_sequence: int | None = None
    limit: int = 50


@dataclass(frozen=True, kw_only=True)
class AgentMessagesPage:
    """Return one page of stored messages, oldest first.

    Attributes:
        messages: Messages in sequence order.
        next_before_sequence: Pass as ``before_sequence`` to read the previous
            page, or ``None`` when this page starts at the first message.
    """

    messages: list[AgentMessage]
    next_before_sequence: int | None
