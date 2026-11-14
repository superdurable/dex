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
from typing import Any


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
        COMPACTING_CONTEXT: ``CompactContext`` summarizes earlier messages
            before the turn's next model call. Only a session with
            ``AgentSessionOptions.compaction`` reaches it.
        FAILED: The session ended and takes no more messages. Its Flow closed
            ``FAILED``, and ``AgentState.last_turn_error`` holds the reason.
    """

    WAITING_FOR_USER_MESSAGE = "waiting_for_user_message"
    RUNNING_MODEL = "running_model"
    RUNNING_TOOLS = "running_tools"
    COMPACTING_CONTEXT = "compacting_context"
    FAILED = "failed"


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


class AgentMessageOrigin(StrEnum):
    """Name why the session wrote a ``USER`` message itself.

    ``AgentMessage.origin`` holds one of these values. A later release may
    add values. Show a message with a value you do not know as a note from
    the session, never as text the user typed.

    .. versionadded:: 0.6.0

    Attributes:
        PLAN_EXECUTION: ``execute_plan`` started the turn with the text of
            ``build_plan_execution_message``.
        TURN_FAILURE_NOTE: ``RecordTurnFailure`` told the model which tool
            calls of the failed turn ran.
        TURN_STEERED_NOTE: ``steer_user_message`` ended the turn, and the
            note lists the tool calls that ran.
        TURN_CANCELLED_NOTE: ``cancel_turn`` ended the turn, and the note
            lists the tool calls that ran.
    """

    PLAN_EXECUTION = "plan_execution"
    TURN_FAILURE_NOTE = "turn_failure_note"
    TURN_STEERED_NOTE = "turn_steered_note"
    TURN_CANCELLED_NOTE = "turn_cancelled_note"


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
        WAITING_FOR_TASK: The tool started remote work and returned
            ``ToolTaskStarted``. ``PollToolTask`` polls the task in
            ``AgentToolCall.task_id`` every ``task_poll_interval_seconds``.
        REJECTED: The user or an expired deadline rejected the call. It never ran.
        SUCCEEDED: The tool ran and returned ``output``.
        FAILED: The tool raised ``ToolExecutionError``, a read call's attempts
            ran out, or the tool did not run (``error`` starts with
            ``TOOL_CALL_NOT_RUN_ERROR_PREFIX``); ``error`` holds the message
            for the model to see.
        OUTCOME_UNKNOWN: The tool Step ended without a result, through a
            Worker crash, a timeout, or another exception after its attempts
            ran out, so the effect may or may not have happened.
    """

    AWAITING_APPROVAL = "awaiting_approval"
    APPROVED = "approved"
    AWAITING_ANSWER = "awaiting_answer"
    WAITING_FOR_TIMER = "waiting_for_timer"
    WAITING_FOR_TASK = "waiting_for_task"
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


class InteractionMode(StrEnum):
    """Name what kind of turn the session runs, for the runner to choose the tools it offers.

    The session sets ``AgentState.interaction_mode`` when a turn starts, and the
    runner reads it from ``AgentTurnContext.interaction_mode``. Plan mode needs
    ``AgentSessionOptions.plan_mode_enabled``; without it every turn is ``CHAT``.

    Attributes:
        CHAT: An ordinary turn. The runner offers every tool.
        PLANNING: A turn that only plans. A message of the turn has
            ``plan_mode``, or a ``DRAFT`` plan waits for review. The runner
            offers only ``write_todos``, and ``RunModel`` rejects any other call.
        EXECUTING: A turn that ``execute_plan`` started. The runner offers every tool.
    """

    CHAT = "chat"
    PLANNING = "planning"
    EXECUTING = "executing"


class PlanStatus(StrEnum):
    """Name where an ``AgentPlan`` stands.

    Attributes:
        DRAFT: A planning turn wrote the plan, and it waits for ``execute_plan``.
        ACTIVE: The plan runs, or ``write_todos`` wrote it outside a planning
            turn with a task not completed.
        COMPLETED: Every task is completed.
        CLEARED: ``write_todos`` replaced the plan with no tasks.
    """

    DRAFT = "draft"
    ACTIVE = "active"
    COMPLETED = "completed"
    CLEARED = "cleared"


class PlanTaskStatus(StrEnum):
    """Name where one task of an ``AgentPlan`` stands.

    Attributes:
        PENDING: The task has not started.
        IN_PROGRESS: The model works on the task.
        COMPLETED: The task is done.
    """

    PENDING = "pending"
    IN_PROGRESS = "in_progress"
    COMPLETED = "completed"


class ToolCallCancellationReason(StrEnum):
    """Name why steering or cancellation abandoned a tool call.

    A ``REJECTED`` record that steering or ``cancel_turn`` abandoned carries
    one in ``AgentToolCall.cancellation_reason``, and so does the
    ``OUTCOME_UNKNOWN`` record of a task that ``cancel_turn`` abandoned. A
    ``REJECTED`` record's ``rejection_message`` is the structured result the
    model sees, such as
    ``{"status":"cancelled","reason":"superseded_by_steered_user_message"}``.
    The values are JSON strings on the wire.

    Attributes:
        STEERED: ``steer_user_message`` superseded the turn.
        TURN_CANCELLED: ``cancel_turn`` stopped the turn.
    """

    STEERED = "steered"
    TURN_CANCELLED = "turn_cancelled"


class TurnControlKind(StrEnum):
    """Name the RPC that published one ``TurnControlRequest``.

    Attributes:
        STEER: ``steer_user_message`` published a steered message.
        CANCEL_TURN: ``cancel_turn`` recorded a ``TurnCancellationRequest``.
            dex-agents 0.2.0 no longer publishes it; Workers still accept it.
    """

    STEER = "steer"
    CANCEL_TURN = "cancel_turn"


@dataclass(frozen=True, kw_only=True)
class AgentImageReference:
    """Point to one image in a media store by the SHA-256 of its bytes.

    Session state never holds image bytes. A caller uploads the image with
    ``MediaStore.put_image`` first and puts the returned reference in
    ``AgentUserMessage.images`` or ``ToolExecutionResult.images``. A runner
    reads the bytes back with ``MediaStore.get_image`` or ``load_images``
    when it builds model input.

    The store keys every object by its content, so uploading the same bytes
    twice returns the same reference, and reading a reference always returns
    the bytes it was made from. ``validate_image_reference`` checks the
    fields.

    Example:
        >>> reference = await media_store.put_image(Path("chart.png").read_bytes())
        >>> await sessions.send_user_message("session-42", "What does this chart show?",
        ...                                  images=[reference])

    Attributes:
        uri: Where the store keeps the bytes, ending in ``/sha256/<sha256>``,
            such as ``"s3://media/agents/sha256/<sha256>"``.
        mime_type: ``"image/png"``, ``"image/jpeg"``, ``"image/gif"``, or
            ``"image/webp"``, detected from the bytes.
        sha256: SHA-256 of the bytes as 64 lowercase hex digits.
        size_bytes: Size of the bytes, from 1 to ``MAX_IMAGE_BYTES``.
        type: Always ``"image"``.

    .. versionadded:: 0.5.0
    """

    uri: str
    mime_type: str
    sha256: str
    size_bytes: int
    type: str = "image"


@dataclass(frozen=True, kw_only=True)
class AgentSessionStartInput:
    """Start one agent session Flow, optionally with its first user message.

    Pass it as the input of ``AsyncClient.start_flow`` for an
    ``AgentSessionFlow``. The Flow ID you choose is the session ID.

    Example:
        >>> await client.start_flow(flow, "session-42", AgentSessionStartInput(
        ...     initial_message="Summarize my open tickets",
        ...     session_settings={"model": "gpt-5-mini"}))

    Attributes:
        initial_message: Text of the first user message. ``None`` or
            whitespace-only text starts the session in ``AwaitUserMessage``.
        session_settings: The session's settings, a JSON object the runner
            reads from ``AgentTurnContext.session_settings``, such as the
            model to use. ``None``, the default, starts without settings.
            Settings are stored in the session, so never put a secret in
            them; put a reference, such as a credential name, that the
            Worker resolves. ``get_agent_snapshot`` and the browser snapshot
            JSON return them. The compact JSON text must fit in
            ``MAX_SESSION_SETTINGS_BYTES``; ``AgentSessionClient.start_session``
            checks it before it starts the Flow, and ``StartAgentSession``
            fails the session at once, with no retry, for settings it
            refuses. Start sessions with settings only after every Worker of
            the Flow type runs dex-agents 0.2.0: a 0.1.0 Worker drops them.
        start_environment: Where the program that started the session ran,
            as string keys and values, such as a host name and a workspace
            path. ``None``, the default, records nothing. The session never
            reads it: it stays in the Flow's start input, so a run shows the
            environment that started it. The Dex Agents CLI records its host,
            user, configuration file, and workspace here. Never put a secret
            in it. Workers before dex-agents 0.5.0 ignore it.

    .. versionchanged:: 0.5.0
        Added ``start_environment``.
    """

    initial_message: str | None = None
    session_settings: dict[str, Any] | None = None
    start_environment: dict[str, str] | None = None


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
            failed, or ``None`` when it completed. With status ``FAILED``, the
            reason the session ended.
        interaction_mode: Kind of the active turn, or of the last turn between
            turns. The session sets it when a turn starts. Defaults to
            ``CHAT``; ``None``, as an explicit JSON ``null`` decodes, also
            becomes ``CHAT``, so a read value is never ``None``.
        accepts_turn_control: Whether ``steer_user_message`` and
            ``cancel_turn`` may end the active turn. A Worker that applies
            them sets it when a turn starts and clears it when the turn ends.
            An earlier Worker drops it whenever it writes ``AgentState``, so
            the RPCs refuse a turn that such a Worker started or advanced.
    """

    status: AgentSessionStatus
    turn_count: int = 0
    active_turn_id: str | None = None
    next_message_sequence: int = 1
    last_turn_error: str | None = None
    interaction_mode: InteractionMode | None = InteractionMode.CHAT
    accepts_turn_control: bool = False

    def __post_init__(self) -> None:
        if self.interaction_mode is None:
            object.__setattr__(self, "interaction_mode", InteractionMode.CHAT)


@dataclass(frozen=True, kw_only=True)
class AgentMessage:
    """Store one framework item of the session conversation.

    The ``AgentMessages`` AttributeMap holds the newest messages, one value
    per message, keyed by its sequence as a 12-digit zero-padded decimal.
    Older messages are in ``AgentMessageChunks``. ``framework_item`` is the
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
        images: Images of the item, for display: those of a user message, or
            those a tool result returned. Empty for an item without images.

            .. versionadded:: 0.5.0
        origin: Why the session wrote this ``USER`` message itself, usually an
            ``AgentMessageOrigin`` value, or ``None``. It is ``None`` for a
            message that a user or the start input sent, for ``ASSISTANT``
            and ``TOOL`` messages, and for every message stored before 0.6.0.
            Show a message that has an origin as a session note, not as text
            the user typed. Only the session sets it: neither
            ``send_user_message`` nor a runner's messages can. The value is a
            plain string, so a value that a later release adds still decodes.
            A Worker before 0.6.0 ignores the field, and drops it when it
            moves the message into an archive chunk.

            .. versionadded:: 0.6.0
        sender_name: The display name that the sender of this ``USER``
            message declared, from ``AgentUserMessage.sender_name``, or
            ``None``. The sender chose it; it is not an authenticated
            identity. ``text`` holds the message without the name, and the
            model saw it as ``"<sender_name>: <text>"``. It is ``None`` for
            every other message. Encoding omits it when it is ``None``.

            .. versionadded:: 0.6.2

    Example:
        >>> page = await sessions.list_agent_messages("session-42")
        >>> for message in page.messages:
        ...     if message.origin == AgentMessageOrigin.PLAN_EXECUTION:
        ...         print("Running the approved plan")
        ...     elif message.origin is None and message.role is MessageRole.USER:
        ...         print(f"> {message.text}")
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
    images: list[AgentImageReference] = field(default_factory=list)
    origin: str | None = None
    sender_name: str | None = field(default=None, metadata={"dex_json_omit_default": True})


class QuestionResolution(StrEnum):
    """Name how a question that a tool or its task asked was resolved.

    A tool's own question is only ever answered: a decline or a passed
    deadline ends the call with ``rejected``. A question that a started task
    asked through ``poll_tool_task`` goes back to the task in every case, so
    the task, not the session, decides what a decline means.

    Attributes:
        ANSWERED: The user answered with ``answer_question``.
        DECLINED: The user declined with ``resolve_interrupt`` and
            ``approved=False``.
        DEADLINE_PASSED: ``interrupt_deadline`` passed first.
    """

    ANSWERED = "answered"
    DECLINED = "declined"
    DEADLINE_PASSED = "deadline_passed"


@dataclass(frozen=True, kw_only=True)
class QuestionAnswer:
    """Record how one question that a tool asked through ``ToolInputRequested`` was resolved.

    ``AwaitQuestionAnswer`` appends one to ``AgentToolCall.input_answers`` when
    the user answers, and the tool receives them in
    ``DeferredToolExecution.input_answers`` when it runs again. For a question
    that a started task asked, it also appends one when the user declines or
    the deadline passes, and ``PollToolTask`` hands it to the task as
    ``ToolTaskPoll.pending_answer``.

    Attributes:
        interrupt_id: ``"<tool_call_id>-input-<n>"`` of the resolved question,
            where ``n`` counts the call's questions from 1.
        question: The question text the tool asked.
        answer: The user's answer, exactly as sent to ``answer_question``. For
            a declined question, the user's rejection message or
            ``"The user declined to answer."``; for a passed deadline,
            ``"question deadline passed"``.
        resolution: How the question was resolved. Defaults to ``ANSWERED``,
            the only resolution of a tool's own question.
    """

    interrupt_id: str
    question: str
    answer: str
    resolution: QuestionResolution = QuestionResolution.ANSWERED


@dataclass(frozen=True, kw_only=True)
class AgentInterruptDisplay:
    """Say what one tool approval acts on and what the call does.

    The tool's source builds it from the call's concrete arguments and
    returns it in ``DeferredToolCall.approval_display``. The session stores
    it in ``AgentInterrupt.display``. An approval UI shows ``target`` and
    ``summary`` above the arguments, and ``preview`` when it is set. When an
    interrupt has no display, show the concrete arguments only and do not
    guess the call's effect from its name or description.

    The text describes the request, not its result: it never says how often
    the call runs or what it achieves, because a retried write may run again
    and a provider may refuse it. Its text comes from the call's arguments,
    which the session stores in full as before. As best-effort hygiene, a
    source shows the value of a credential-like argument, such as
    ``api_key``, ``clientSecret``, or ``credentials``, as ``"<redacted>"``
    (see ``dex_agents.approval_display.is_credential_like_name``), and
    replaces common credential shapes in text it shows (see
    ``dex_agents.approval_display.redact_inline_credentials``). This is not a
    security boundary: a credential in another form can still appear. The session
    cuts a target over ``MAX_APPROVAL_TARGET_BYTES`` and a summary over
    ``MAX_APPROVAL_SUMMARY_BYTES``, ending them with ``…``, and drops a
    preview over ``MAX_APPROVAL_PREVIEW_BYTES``, so a display adds little to
    the records that hold it.

    .. versionadded:: 0.6.0

    Example:
        >>> display = AgentInterruptDisplay(
        ...     target="docs/notes.md",
        ...     summary="Writes 12 bytes to docs/notes.md.",
        ... )
        >>> interrupt = next(i for i in snapshot.pending_interrupts if i.display)
        >>> print(interrupt.display.summary)

    Attributes:
        target: What the call acts on, such as a workspace path, a shell
            command's working directory, or a connector and its connection
            (``slack/support-slack``).
        summary: One sentence that says what the call does to ``target``,
            built from the call's concrete arguments, such as
            ``Runs a shell command in /work/app: npm test``.
        preview: Longer text to show with the summary, or ``None``. A local
            file write sets a unified diff of the file as it was when the call
            was requested against the new content; its header lines say so.
    """

    target: str
    summary: str
    preview: str | None = None


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
            recovery detail of the failed Step after ``OUTCOME_UNKNOWN``, or
            ``TOOL_TASK_ABANDONED_ERROR`` for a task that ``cancel_turn``
            abandoned. A tool Step that failed before the tool ran records
            ``FAILED`` with ``TOOL_CALL_NOT_RUN_ERROR_PREFIX``, ``": "``, and the
            recovery detail.
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
            after the answer. For a call waiting for a task, the continuation
            of the task's last question until the next poll, and then what the
            last ``ToolTaskPending`` returned. ``None`` when there is none.
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
        cancellation_reason: Why steering or ``cancel_turn`` abandoned the
            call before it started, on a ``REJECTED`` record, or ``None``.
            ``TURN_CANCELLED`` on a ``WAITING_FOR_TASK`` or
            ``OUTCOME_UNKNOWN`` record means ``cancel_turn`` abandoned the
            call's started task.
        task_id: The remote task ID from the tool's ``ToolTaskStarted``, or
            ``None`` for a call that started no task. It stays on the record
            after the task finishes.
        task_poll_interval_seconds: Whole seconds ``PollToolTask`` waits
            before the next poll, at least 1, from ``ToolTaskStarted`` or the
            last ``ToolTaskPending``. ``None`` for a call that started no task.
        task_answer_pending: Whether the last entry of ``input_answers``, the
            resolution of the task's last question, has not reached the task
            yet. ``AwaitQuestionAnswer`` sets it, ``PollToolTask`` then polls at
            once and hands that entry to the runner, and the poll's commit
            clears it.
        ordered_write: Whether the call is a write that runs in the model's
            order after the turn's earlier ordered writes, from
            ``DeferredToolCall.ordered``. ``False`` for every other call.

            .. versionadded:: 0.4.0
        write_queued: Whether the call waits for an earlier ordered write of
            its turn. No Step runs it yet, and its approval is not raised yet.
            ``RunModel`` starts it once every earlier call is final and every
            earlier ordered write succeeded. Otherwise ``RunModel`` records it
            ``FAILED`` with ``skipped`` set. ``ResumeTurn`` does not wait for it.

            .. versionadded:: 0.4.0
        skipped: Whether write ordering skipped the call because an earlier
            ordered write of the turn failed, was rejected, or ended
            ``OUTCOME_UNKNOWN``. The record is ``FAILED``, and its ``error``
            starts with ``TOOL_CALL_NOT_RUN_ERROR_PREFIX``, so a reader that does
            not know this field still sees that the tool did not run.

            .. versionadded:: 0.4.0
        truncated_output_bytes: Size in bytes of the full output when
            ``output`` holds a truncated copy, from
            ``ToolExecutionResult.truncated_output_bytes``, or ``None``.

            .. versionadded:: 0.4.0
        images: Images the tool returned with ``output`` after ``SUCCEEDED``,
            from ``ToolExecutionResult.images``. Empty for every other call.

            .. versionadded:: 0.5.0
        runner_tool_state: Opaque text from
            ``ToolExecutionResult.runner_tool_state`` after ``SUCCEEDED``, which
            only the runner reads, or ``None``. The session never shows it to
            the model, in the browser snapshot, or in trace attributes. A record
            written before 0.5.0 has none, and a Worker before 0.5.0 ignores it.

            .. versionadded:: 0.5.0
        approval_display: What the call acts on and does, from
            ``DeferredToolCall.approval_display``, for a call that needs
            approval, or ``None``. The session copies it into the call's
            ``AgentInterrupt.display`` when it raises the approval, which for
            a queued ordered write happens later. A Worker before 0.6.0
            ignores it, and drops it when it rewrites the record.

            .. versionadded:: 0.6.0
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
    cancellation_reason: ToolCallCancellationReason | None = None
    task_id: str | None = None
    task_poll_interval_seconds: int | None = None
    task_answer_pending: bool = False
    ordered_write: bool = False
    write_queued: bool = False
    skipped: bool = False
    truncated_output_bytes: int | None = None
    images: list[AgentImageReference] = field(default_factory=list)
    runner_tool_state: str | None = None
    approval_display: AgentInterruptDisplay | None = None


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
        display: What a ``TOOL_APPROVAL`` call acts on and does, from its
            tool's source, or ``None`` when the source gave none and for a
            ``QUESTION``. A Worker before 0.6.0 writes none, and drops it when
            it rewrites ``PendingInterrupts``, so treat it as optional.

            .. versionadded:: 0.6.0
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
    display: AgentInterruptDisplay | None = None


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
    turn ends. ``steer_user_message`` publishes it to ``SteeredUserMessages``
    instead, which ends the running turn at its next safe Step boundary.

    Attributes:
        text: Message text. Whitespace-only text is rejected.
        plan_mode: Whether the message starts a ``PLANNING`` turn, which drafts
            a plan for review instead of doing the work. ``send_user_message``
            rejects it unless ``AgentSessionOptions.plan_mode_enabled`` is set.
            Defaults to ``False``; ``None``, as an explicit JSON ``null``
            decodes, also becomes ``False``.
        images: Images the user attached, uploaded with ``MediaStore.put_image``
            first. At most ``MAX_IMAGES_PER_MESSAGE``, each valid for
            ``validate_image_reference``. ``send_user_message`` rejects images
            when the session's runner does not implement
            ``ImageUserMessageBuilder``. Defaults to none. Send images only
            after every Worker of the Flow type runs dex-agents 0.5.0: an
            earlier Worker drops them.

            .. versionadded:: 0.5.0
        sender_name: A display name the sender declared, such as a person
            who sends from Dex Web, or ``None`` for none. The model sees the
            message as ``"<sender_name>: <text>"``, and UIs show the name next
            to the message. The sender chooses it: it is not authenticated.
            A valid name has 1 to ``MAX_SENDER_NAME_LENGTH`` Unicode code
            points, contains no control, format, or surrogate character and no
            line break, and neither starts nor ends with a space; the RPCs
            refuse any other name.
            Encoding omits it when it is ``None``. A Worker before 0.6.2
            ignores it.

            .. versionadded:: 0.6.2
    """

    text: str
    plan_mode: bool | None = False
    images: list[AgentImageReference] = field(default_factory=list)
    sender_name: str | None = field(default=None, metadata={"dex_json_omit_default": True})

    def __post_init__(self) -> None:
        if self.plan_mode is None:
            object.__setattr__(self, "plan_mode", False)


@dataclass(frozen=True, kw_only=True)
class AgentPlanTask:
    """Describe one task of an ``AgentPlan``.

    Attributes:
        content: What the task does, as the model wrote it. Never blank.
        status: Where the task stands.
    """

    content: str
    status: PlanTaskStatus


@dataclass(frozen=True, kw_only=True)
class AgentPlan:
    """Hold the session's plan: a short task list the model replaces as a whole.

    The ``AgentPlan`` Attribute holds one value. ``write_todos`` replaces it
    with the next revision, and ``execute_plan`` names the revision the user
    approves. Compaction never summarizes it.

    Attributes:
        revision: Revision of the plan, starting at 1. Every ``write_todos``
            call that succeeds adds 1, an empty list included.
        status: Where the plan stands.
        tasks: Every task in order; empty when ``CLEARED``.
        updated_at: RFC 3339 time of the last change.
    """

    revision: int
    status: PlanStatus
    tasks: list[AgentPlanTask] = field(default_factory=list)
    updated_at: str


@dataclass(frozen=True, kw_only=True)
class ExecutePlanRequest:
    """Approve one plan revision through the ``execute_plan`` RPC.

    Attributes:
        revision: ``AgentPlan.revision`` the user reviewed. A stale revision is
            refused, so the session never runs a plan the user did not see.
    """

    revision: int


@dataclass(frozen=True, kw_only=True)
class PlanExecutionRequest:
    """Carry one approved plan execution through the ``PlanExecutions`` ChannelMap.

    ``execute_plan`` publishes it to the instance named by the revision, and
    ``AwaitUserMessage`` consumes it to start an ``EXECUTING`` turn. The
    ``PendingPlanExecution`` Attribute holds it from acceptance until then.

    Attributes:
        revision: The approved ``AgentPlan.revision``.
    """

    revision: int


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
class PendingUserMessage:
    """Show one user message that waits in ``QueuedUserMessages`` or ``SteeredUserMessages``.

    ``get_agent_snapshot`` lists them, so a UI can delete or steer a queued
    message by its ID.

    Attributes:
        message_id: Channel message ID that Dex assigned when the message was
            published.
        message: The user message.
    """

    message_id: str
    message: AgentUserMessage


@dataclass(frozen=True, kw_only=True)
class SteerUserMessageRequest:
    """Steer the running turn through the ``steer_user_message`` RPC.

    Set exactly one of ``message_id`` and ``text``. ``message_id`` moves a
    queued message, and ``text`` sends a new one.

    Example:
        >>> SteerUserMessageRequest(text="Use the staging account instead")
        >>> SteerUserMessageRequest(message_id=snapshot.queued_messages[0].message_id)

    Attributes:
        message_id: ``PendingUserMessage.message_id`` of a queued message, or ``None``.
        text: Text of a new steered message, or ``None``.
        sender_name: The declared display name of the sender of ``text``, as
            ``AgentUserMessage.sender_name`` describes it, or ``None``. The
            RPC refuses it together with ``message_id``: a queued message
            keeps its own name. Encoding omits it when it is ``None``.

            .. versionadded:: 0.6.2
    """

    message_id: str | None = None
    text: str | None = None
    sender_name: str | None = field(default=None, metadata={"dex_json_omit_default": True})


@dataclass(frozen=True, kw_only=True)
class DeleteQueuedMessageRequest:
    """Delete one queued user message through the ``delete_queued_message`` RPC.

    Attributes:
        message_id: ``PendingUserMessage.message_id`` of a queued message.
    """

    message_id: str


@dataclass(frozen=True, kw_only=True)
class CancelTurnRequest:
    """Stop the running turn through the ``cancel_turn`` RPC.

    Attributes:
        turn_id: The turn to stop, or ``None`` for whichever turn is active.
            A turn ID that is not the active turn makes the RPC return ``False``.
    """

    turn_id: str | None = None


@dataclass(frozen=True, kw_only=True)
class SessionTerminalLease:
    """Record which terminal has a session open, in the ``SessionTerminalLease`` Attribute.

    A chat client, such as a CLI command, holds the lease while it shows the
    session, so that a second client waits instead of chatting at the same
    time. The holder renews it before ``expires_at``. A lease past
    ``expires_at`` is free for any holder, so a holder that crashed frees the
    session without a release. A session without the Attribute has no holder.

    .. versionadded:: 0.6.3

    Attributes:
        holder_id: The holder's random ID, one per holding process.
        host_name: The holder's host name, shown to a waiting client.
        process_id: The process ID of the command the user ran, shown to a
            waiting client.
        opened_at: RFC 3339 time at which this holder first acquired the lease.
            A renewal keeps it.
        expires_at: RFC 3339 time after which another holder may acquire it.
    """

    holder_id: str
    host_name: str
    process_id: int
    opened_at: str
    expires_at: str


@dataclass(frozen=True, kw_only=True)
class AcquireSessionTerminalLeaseRequest:
    """Acquire or renew the session's terminal lease through ``acquire_session_terminal_lease``.

    The same request with the same ``holder_id`` renews the lease and keeps
    its ``opened_at``.

    .. versionadded:: 0.6.3

    .. versionchanged:: 0.6.7
       Added ``environment``.

    Attributes:
        holder_id: The holder's random ID. It must not be blank.
        host_name: The holder's host name, at most 255 Unicode code points.
        process_id: The process ID to show to a waiting client; not negative.
        ttl_seconds: How long the lease lasts from now, from 1 to 3600 seconds.
        environment: The caller's execution environment as text pairs, such
            as its host name and workspace, or ``None`` for none. When the
            call gives the lease to a holder that did not hold it, the session
            stores this map in the ``SessionLastTerminalEnvironment``
            Attribute. A renewal and a refused call leave that Attribute
            unchanged. A session list can then show where each session was
            last open. The session never reads the keys. Workers before 0.6.7
            ignore the field. The wire field is left out when ``None``.
    """

    holder_id: str
    host_name: str
    process_id: int
    ttl_seconds: int
    environment: dict[str, str] | None = field(
        default=None, metadata={"dex_json_omit_default": True}
    )


@dataclass(frozen=True, kw_only=True)
class SessionTerminalLeaseResult:
    """Report the outcome of ``acquire_session_terminal_lease``.

    .. versionadded:: 0.6.3

    Attributes:
        is_acquired: ``True`` when the caller holds the lease now.
        lease: The lease as it is now: the caller's when ``is_acquired``, the
            other holder's when not, and ``None`` for a request that is not
            valid.
    """

    is_acquired: bool
    lease: SessionTerminalLease | None = None


@dataclass(frozen=True, kw_only=True)
class ReleaseSessionTerminalLeaseRequest:
    """Release the session's terminal lease through ``release_session_terminal_lease``.

    .. versionadded:: 0.6.3

    Attributes:
        holder_id: The holder that releases it. Another holder's lease stays.
    """

    holder_id: str


@dataclass(frozen=True, kw_only=True)
class UpdateSessionSettingsRequest:
    """Replace the session's settings through the ``update_session_settings`` RPC.

    The new settings apply from the next turn that starts. A turn that is
    running keeps the settings it started with.

    Attributes:
        session_settings: The complete new settings, a JSON object. They
            replace the old settings; nothing is merged. An empty object
            clears them. Never put a secret in them. ``None``, as a missing
            field or an explicit JSON ``null`` decodes, makes the RPC return
            ``False`` and change nothing.
    """

    session_settings: dict[str, Any] | None = None


@dataclass(frozen=True, kw_only=True)
class TurnSessionSettings:
    """Hold the settings one turn started with, in the ``TurnSessionSettings`` Attribute.

    The Step that starts a turn writes it in the commit that starts the turn.
    The session reads it only while ``turn_id`` is ``AgentState.active_turn_id``,
    so a copy that an earlier turn left, such as when a Worker without
    session settings started the current turn, reads as no settings.

    Attributes:
        turn_id: ``"turn-<n>"`` of the turn that started with these settings.
        session_settings: The settings, a non-empty JSON object.
    """

    turn_id: str
    session_settings: dict[str, Any]


@dataclass(frozen=True, kw_only=True)
class TurnControlRequest:
    """Wake a waiting session Step through the internal ``TurnControlRequests`` Channel.

    ``steer_user_message`` and ``cancel_turn`` publish one. The message only
    wakes ``ResumeTurn`` or ``AwaitUserMessage``; the session decides from
    ``SteeredUserMessages`` and ``TurnCancellationRequest``.

    Attributes:
        kind: The RPC that published it.
    """

    kind: TurnControlKind


@dataclass(frozen=True, kw_only=True)
class TurnCancellationRequest:
    """Record that ``cancel_turn`` asked to stop one turn.

    The ``TurnCancellationRequest`` Attribute holds the latest request. A turn
    is cancelled while ``turn_id`` equals ``AgentState.active_turn_id``. Turn
    IDs never repeat, so a request for an earlier turn never matches.

    Attributes:
        turn_id: The turn to stop.
        requested_at: RFC 3339 time of the request.
    """

    turn_id: str
    requested_at: str


@dataclass(frozen=True, kw_only=True)
class ToolCallCancellation:
    """Wake the Step that waits on one tool call through ``ToolCallCancellations``.

    ``ResumeTurn`` publishes one to the instance named by the tool call ID
    when the turn is steered or cancelled. The waiting Step decides from the
    session's flags, not from this message.

    Attributes:
        reason: Why the turn ends.
    """

    reason: ToolCallCancellationReason


@dataclass(frozen=True, kw_only=True)
class AgentEvent:
    """Carry one live update on the ``AgentEvents`` Stream.

    Streams are best effort. A Step retry can repeat events, and a lost
    consumer can miss them. Rebuild durable state from ``get_agent_snapshot``.

    Group live text by ``(turn_id, step_execution_id, attempt)``. Within one
    ``step_execution_id``, keep only the events of the highest ``attempt``
    seen, and drop the partial text of lower attempts. Different
    ``step_execution_id`` values of one turn are separate model call and tool
    call Steps, and each keeps its own events. After ``turn_completed``,
    ``turn_failed``, ``turn_steered``, or ``turn_cancelled``, replace the
    turn's live text with the stored messages.

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
class AgentMessageChunk:
    """Hold one immutable archive chunk of the session conversation.

    The ``AgentMessageChunks`` AttributeMap holds the session's older
    messages in chunks of consecutive sequences. A chunk holds at most
    ``AgentSessionOptions.message_chunk_size`` messages and at most
    ``message_chunk_max_bytes`` encoded bytes, except that a larger message
    gets a chunk of its own. Its instance key is its first sequence as a
    12-digit zero-padded decimal, as ``message_sequence_key`` returns, and
    ``AgentMessageIndex.chunks`` lists its range. A chunk is never rewritten;
    retention deletes it whole.

    Example:
        >>> index = (await client.get_attributes(session_id, AGENT_MESSAGE_INDEX)).get(
        ...     AGENT_MESSAGE_INDEX
        ... )
        >>> first = index.chunks[0]
        >>> read = AGENT_MESSAGE_CHUNKS.instance(message_sequence_key(first.first_sequence))
        >>> chunk = (await client.get_attributes(session_id, read)).get(read)

    Attributes:
        messages: The chunk's messages in sequence order.
    """

    messages: list[AgentMessage]


@dataclass(frozen=True, kw_only=True)
class AgentMessageChunkRange:
    """Name the messages one ``AgentMessageChunks`` instance holds.

    Attributes:
        first_sequence: Sequence of the chunk's first message, which is also
            its instance key.
        last_sequence: Sequence of the chunk's last message.
    """

    first_sequence: int
    last_sequence: int


@dataclass(frozen=True, kw_only=True)
class AgentMessageIndex:
    """Locate the stored messages of one session across the window and the chunks.

    The ``AgentMessageIndex`` Attribute holds one value. The newest messages
    stay in the ``AgentMessages`` window, one instance per message. When the
    window holds more than ``AgentSessionOptions.message_window_size``
    messages, its oldest ones move into ``AgentMessageChunks`` instances. A
    message too large for a shared chunk skips the window and gets a chunk of
    its own at once. Retention deletes whole chunks from the oldest end. The
    index changes only when chunks are written or deleted. A session without
    an index stores every message in ``AgentMessages``, from sequence 1, as
    dex-agents 0.1.0 did.

    Attributes:
        chunks: The range of every kept chunk, oldest first. The archived
            ones are consecutive from ``first_sequence`` up to
            ``window_first_sequence``. A range at or after
            ``window_first_sequence`` is one oversized message among the
            window's messages.
        first_sequence: Sequence of the oldest stored message. Retention
            deleted every earlier one.
        window_first_sequence: Sequence of the oldest message that has not
            been archived. Every later message is in ``AgentMessages`` unless
            a chunk range holds it.
    """

    chunks: list[AgentMessageChunkRange]
    first_sequence: int
    window_first_sequence: int


@dataclass(frozen=True, kw_only=True)
class TurnReplySequence:
    """Mark where a turn's replies start, after the user messages that started it.

    The ``TurnReplySequence`` Attribute holds one value, written by the Step
    that starts the turn. ``turn_completed``, ``turn_steered``, and
    ``turn_cancelled`` count the turn's messages from it, so no Step reads
    the turn's older messages, which compaction may have summarized and
    retention deleted.

    Attributes:
        turn_id: ``"turn-<n>"`` of the turn.
        sequence: First sequence after the turn's user messages.
    """

    turn_id: str
    sequence: int


@dataclass(frozen=True, kw_only=True)
class ContextSummary:
    """Hold the summary that replaces a session's earlier messages in the model input.

    The ``ContextSummary`` Attribute holds one value, and only ``CompactContext``
    writes it. A session with ``AgentSessionOptions.compaction`` folds its
    earlier messages into this text when the next model input would cross the
    threshold. Each new summary folds in the previous one, so it always covers
    the messages from ``first_sequence`` through ``last_sequence``. The
    messages stay stored: ``list_agent_messages`` and ``get_agent_snapshot``
    return them until retention deletes their chunk; see
    ``AgentSessionOptions.retained_message_limit``.

    Example:
        >>> snapshot = await sessions.get_agent_snapshot("session-42")
        >>> if snapshot.context_summary is not None:
        ...     print(snapshot.context_summary.last_sequence)

    Attributes:
        generation: Count of summaries the session has written, starting at 1.
        first_sequence: Sequence of the first summarized message. Always 1.
        last_sequence: Sequence of the last summarized message. A runner with
            the compaction capability receives only the messages after it.
        text: Summary text from the runner's ``summarize_context``.
        created_at: RFC 3339 time when ``CompactContext`` wrote the summary.
    """

    generation: int
    first_sequence: int
    last_sequence: int
    text: str
    created_at: str


@dataclass(frozen=True, kw_only=True)
class CompactContextInput:
    """Name the messages that one ``CompactContext`` Step summarizes.

    ``RunModel`` moves to ``CompactContext`` with this input when the next
    model input would cross the compaction threshold.

    Attributes:
        through_sequence: Sequence of the last message to summarize. It is
            after the current summary's ``last_sequence``.
    """

    through_sequence: int


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
        plan: The ``AgentPlan``, or ``None`` before ``write_todos`` first succeeded.
        queued_messages: Those queued messages with their Channel message
            IDs, oldest first. Pass an ID to ``delete_queued_message`` or
            ``steer_user_message``.
        steered_messages: Steered messages that the session has not applied
            yet, oldest first.
        context_summary: The current ``ContextSummary``, or ``None`` when the
            session never compacted. ``messages`` still holds the summarized
            messages.
        session_settings: The ``SessionSettings`` Attribute: the settings the
            next turn starts with, or ``None`` when the session has none.
            The active turn may still run with the settings it started with.
    """

    state: AgentState | None
    messages: list[AgentMessage] = field(default_factory=list)
    pending_interrupts: list[AgentInterrupt] = field(default_factory=list)
    tool_calls: list[AgentToolCall] = field(default_factory=list)
    usage: AgentUsage = field(default_factory=AgentUsage)
    queued_message_count: int = 0
    plan: AgentPlan | None = None
    queued_messages: list[PendingUserMessage] = field(default_factory=list)
    steered_messages: list[PendingUserMessage] = field(default_factory=list)
    context_summary: ContextSummary | None = None
    session_settings: dict[str, Any] | None = None


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
            page, or ``None`` when this page starts at the oldest stored
            message.
        deleted_before_sequence: ``AgentMessageIndex.first_sequence`` when
            retention deleted every message before it, or ``None`` when the
            session still stores its first message. Set on every page.
    """

    messages: list[AgentMessage]
    next_before_sequence: int | None
    deleted_before_sequence: int | None = None
