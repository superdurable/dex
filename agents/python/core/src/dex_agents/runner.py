# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The contract between the agent session Flow and a framework adapter.

An adapter implements ``AgentFrameworkRunner`` for one agent framework. The
session Flow runs in Step mode: every model call and every tool call is its
own Dex Step. It calls the runner from those Steps:

* ``build_user_message`` turns user text into a stored framework item.
* ``run_model_step`` lets the framework make at most one model call, from the
  start of the turn or resumed with final tool call records, and returns
  ``TurnCompleted`` or ``ToolCallsRequested``. It runs no tool it can defer.
* ``execute_deferred_tool_call`` runs one tool call in its own Step.

A runner may also implement ``UnfinishedTurnMessageBuilder`` when it stores a
turn's items incrementally through ``ToolCallsRequested.new_messages``,
``PlanModeRunner`` when it honors plan mode, ``ToolTaskPoller`` when its tools
start remote tasks with ``ToolTaskStarted``, and
``dex_agents.compaction.ContextCompactionRunner`` when it can summarize earlier
messages.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from datetime import timedelta
from typing import TYPE_CHECKING, Any, Protocol, TypeAlias, TypeVar, runtime_checkable

from dex import AsyncContext, RetryPolicy

from dex_agents.built_in_tools import BuiltInTool
from dex_agents.events import AgentEventWriter
from dex_agents.values import (
    AgentImageReference,
    AgentMessage,
    AgentPlan,
    AgentToolCall,
    AgentUsage,
    ContextSummary,
    InteractionMode,
    MessageRole,
    QuestionAnswer,
    ToolCallStatus,
    ToolEffect,
)

#: Model-visible text for a call whose outcome is unknown.
OUTCOME_UNKNOWN_TOOL_RESULT = (
    "The tool call's outcome is unknown: it stopped without returning a result, so it may "
    "or may not have taken effect. It will not be retried automatically."
)

#: First line of the note ``RecordTurnFailure`` stores after a failed turn ran tools.
TURN_FAILURE_NOTE_HEADER = "[Dex] The previous turn failed after these tool calls ran:"

#: Last line of that note.
TURN_FAILURE_NOTE_FOOTER = "Do not repeat these tool calls unless the user asks again."

#: Model-visible text for a rejection without a message.
DEFAULT_REJECTION_TOOL_RESULT = "The user rejected this tool call."

#: Rejection message recorded when an interrupt deadline passes.
APPROVAL_DEADLINE_PASSED = "approval deadline passed"

#: First line of the note ``ApplyTurnControl`` stores after a steered turn ran tools.
TURN_STEERED_NOTE_HEADER = "[Dex] The user steered the conversation after these tool calls ran:"

#: First line of the note ``ApplyTurnControl`` stores after a cancelled turn ran tools.
TURN_CANCELLED_NOTE_HEADER = "[Dex] The user stopped the previous turn after these tool calls ran:"

#: Model-visible result of a tool call that steering abandoned before it started.
TOOL_CALL_CANCELLED_BY_STEERING_RESULT = (
    '{"status":"cancelled","reason":"superseded_by_steered_user_message"}'
)

#: Model-visible result of a tool call that ``cancel_turn`` abandoned before it started.
TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT = (
    '{"status":"cancelled","reason":"turn_cancelled_by_user"}'
)

#: Error of an ``outcome_unknown`` record whose task ``cancel_turn`` abandoned.
TOOL_TASK_ABANDONED_ERROR = (
    "the turn was cancelled while the tool's task was running; the task may have taken effect"
)

#: Start of the ``error`` of a ``failed`` record whose tool Step certainly never ran the tool.
TOOL_CALL_NOT_RUN_ERROR_PREFIX = "the tool did not run, so it is safe to call again"

#: Detail after ``TOOL_CALL_NOT_RUN_ERROR_PREFIX`` on a record that write ordering skipped.
#:
#: .. versionadded:: 0.4.0
TOOL_CALL_SKIPPED_AFTER_WRITE_DETAIL = "an earlier write of this turn did not succeed"


@dataclass(frozen=True)
class ToolPolicy:
    """Declare how the session treats calls to one tool.

    Pass policies by tool name in ``AgentSessionOptions.tool_policies``. A
    policy overrides framework metadata, such as MCP ``readOnlyHint``, and the
    session-level ``default_tool_effect``. Every tool call runs in its own
    Step; the policy picks that Step and whether it waits for approval.

    Example:
        >>> AgentSessionOptions(tool_policies={
        ...     "search_tickets": ToolPolicy(ToolEffect.READ),
        ...     "create_refund": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=True),
        ...     "send_email": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        ... })

    Attributes:
        effect: What the tool does outside the session.
        requires_approval: Whether a person must approve each call. ``None``
            uses the effect's default: ``False`` for ``READ`` and
            ``IDEMPOTENT_WRITE``, ``True`` for ``NON_IDEMPOTENT_WRITE`` and
            ``UNCLASSIFIED``.
    """

    effect: ToolEffect
    requires_approval: bool | None = None

    @property
    def is_approval_required(self) -> bool:
        """Report whether each call waits for ``resolve_interrupt``.

        Returns:
            ``requires_approval`` when set, otherwise the effect's default.
        """
        if self.requires_approval is not None:
            return self.requires_approval
        return self.effect in (ToolEffect.NON_IDEMPOTENT_WRITE, ToolEffect.UNCLASSIFIED)


def resolve_tool_policy(
    tool_name: str,
    tool_policies: Mapping[str, ToolPolicy],
    default_tool_effect: ToolEffect,
    framework_effect: ToolEffect | None = None,
) -> ToolPolicy:
    """Return the policy that applies to one tool.

    Runners normally call ``AgentTurnContext.resolve_tool_policy`` instead.

    Args:
        tool_name: Tool name the model called.
        tool_policies: Explicit policies by tool name. They win.
        default_tool_effect: Effect for a tool without a policy or metadata.
        framework_effect: Effect derived from framework metadata, such as MCP
            annotations, or ``None`` when the framework says nothing.

    Returns:
        A policy whose ``requires_approval`` is never ``None``.
    """
    policy = tool_policies.get(tool_name)
    if policy is None:
        policy = ToolPolicy(framework_effect or default_tool_effect)
    return ToolPolicy(policy.effect, policy.is_approval_required)


def describe_tool_call_outcome(tool_call: AgentToolCall) -> str:
    """Return the tool result text the model should see for a final record.

    Use it when resuming a turn: give the framework this text as the tool's
    result instead of running the tool again. A framework with a native
    rejection mechanism may pass ``rejection_message`` there instead.

    Args:
        tool_call: A record whose status is final.

    Returns:
        The output for ``SUCCEEDED``; the error, the rejection message, or a
        fixed explanation for the other final statuses.

    Raises:
        ValueError: If the record is still awaiting approval or approved.
    """
    if tool_call.status is ToolCallStatus.SUCCEEDED:
        return tool_call.output or ""
    if tool_call.status is ToolCallStatus.FAILED:
        return f"The tool call failed: {tool_call.error or 'unknown error'}"
    if tool_call.status is ToolCallStatus.REJECTED:
        return tool_call.rejection_message or DEFAULT_REJECTION_TOOL_RESULT
    if tool_call.status is ToolCallStatus.OUTCOME_UNKNOWN:
        return OUTCOME_UNKNOWN_TOOL_RESULT
    raise ValueError(f"tool call {tool_call.tool_call_id} has no final outcome yet")


def build_turn_failure_note(tool_calls: Sequence[AgentToolCall]) -> str:
    """Return the note text that tells the model what a failed turn already did.

    ``RecordTurnFailure`` stores this note through ``build_user_message`` when a
    failed turn has final tool call records, so the next turn's model does not
    repeat a write. The note has one line per record, in ``deferral_order``:

    .. code-block:: text

        [Dex] The previous turn failed after these tool calls ran:
        - charge {"amount": 5} -> succeeded: charged 5
        Do not repeat these tool calls unless the user asks again.

    Args:
        tool_calls: Final records of the failed turn.

    Returns:
        ``TURN_FAILURE_NOTE_HEADER``, one ``"- <tool_name> <arguments> ->
        <status>: <describe_tool_call_outcome(record)>"`` line per record, and
        ``TURN_FAILURE_NOTE_FOOTER``, joined with newlines.

    Raises:
        ValueError: If a record is not final.
    """
    return build_tool_call_note(TURN_FAILURE_NOTE_HEADER, tool_calls)


def build_tool_call_note(header: str, tool_calls: Sequence[AgentToolCall]) -> str:
    """Return a note in the turn-failure note's line format with another first line.

    The session stores it for a turn that ended without a final answer. Use
    ``build_turn_failure_note`` for a failed turn.

    Args:
        header: First line, such as ``TURN_STEERED_NOTE_HEADER``.
        tool_calls: Final records of the ending turn.

    Returns:
        ``header``, one line per record as in ``build_turn_failure_note``, and
        ``TURN_FAILURE_NOTE_FOOTER``, joined with newlines.

    Raises:
        ValueError: If a record is not final.
    """
    lines = [header]
    for tool_call in sorted(tool_calls, key=lambda record: record.deferral_order):
        outcome = describe_tool_call_outcome(tool_call)
        lines.append(
            f"- {tool_call.tool_name} {tool_call.arguments} -> {tool_call.status}: {outcome}"
        )
    lines.append(TURN_FAILURE_NOTE_FOOTER)
    return "\n".join(lines)


@dataclass(frozen=True, kw_only=True)
class FrameworkMessage:
    """Describe one framework item that the session should store.

    A runner returns these from ``build_user_message``, in
    ``TurnCompleted.new_messages``, and in ``ToolCallsRequested.new_messages``.
    The session adds the sequence, turn ID, and
    creation time, and stores each as an ``AgentMessage``. On later turns the
    runner receives them back in ``AgentTurnContext.messages`` and rebuilds
    framework input from ``framework_item`` alone.

    Set the projection fields for display. Set ``tool_*`` fields when the item
    carries exactly one tool call or result; leave them ``None`` otherwise.

    Attributes:
        role: Author of the item.
        framework_item: Framework-native JSON text, replayed unchanged.
        text: Display text, or ``None``.
        tool_call_id: Tool call ID of a tool request or result item.
        tool_name: Tool name of a tool request or result item.
        tool_arguments: Tool arguments as JSON text.
        tool_output: Tool result text.
        images: Images of the item, for display, such as those of a tool
            result from ``AgentToolCall.images``. The session sets the images of
            a user message itself, from ``AgentUserMessage.images``. The session
            checks them with ``validate_image_references`` before it stores
            the message; invalid ones fail the calling Step. Defaults to none.

            .. versionadded:: 0.5.0
    """

    role: MessageRole
    framework_item: str
    text: str | None = None
    tool_call_id: str | None = None
    tool_name: str | None = None
    tool_arguments: str | None = None
    tool_output: str | None = None
    images: Sequence[AgentImageReference] = ()


@dataclass(frozen=True, kw_only=True)
class DeferredToolCall:
    """Ask the session to run one tool call in its own Dex Step.

    Return one in ``ToolCallsRequested.tool_calls`` for every tool call of the
    model response. The session records each call, starts one Step per call in
    parallel, raises an interrupt first when the policy requires approval, and
    calls ``run_model_step`` again once every call has a final record.

    Attributes:
        tool_call_id: Framework tool call ID. It must be non-empty, contain no
            ``/``, and be unique within one turn. IDs may repeat across turns.
        tool_name: Tool name the model called.
        arguments: Tool arguments as JSON text.
        policy: Policy from ``AgentTurnContext.resolve_tool_policy``.
        tool_source_reference: Opaque text that the tool's source stores on
            the call's record, such as which MCP server and tool to call. The
            tool Step returns it in ``DeferredToolExecution.tool_call``, so the
            source resolves the tool without listing it again. ``None``, the
            default, stores nothing.
        ordered: Whether the call is a write that runs in the model's order
            after the turn's earlier ordered calls. The session starts one
            ordered call at a time, raises its approval only when its turn
            comes, and records later ordered calls as skipped once one does not
            succeed. ``READ`` calls ignore it. Defaults to ``False``, which runs
            the call in parallel with the turn's other calls.
            ``ConnectorToolSource`` sets it for connector writes.

            .. versionadded:: 0.4.0
    """

    tool_call_id: str
    tool_name: str
    arguments: str
    policy: ToolPolicy
    tool_source_reference: str | None = None
    ordered: bool = False


@dataclass(frozen=True, kw_only=True)
class ModelCallMetadata:
    """Describe the model and response of one model call, for tracing.

    Return it as ``model_call_metadata`` in ``TurnCompleted`` or
    ``ToolCallsRequested``. The session never stores it: it only names the
    ``chat`` span and sets its GenAI attributes. Leave a field ``None`` when the
    framework does not expose it.

    Example:
        >>> ModelCallMetadata(
        ...     request_model="gpt-5",
        ...     response_model="gpt-5-2026-08-07",
        ...     provider_name="openai",
        ...     response_id="resp_123",
        ...     finish_reasons=("tool_calls",),
        ... )

    Attributes:
        request_model: Model name the call requested, such as ``"gpt-5"``. It
            becomes ``gen_ai.request.model`` and the span name
            ``chat <request_model>``.
        response_model: Model name the provider reported, or ``None``.
        provider_name: GenAI provider, such as ``"openai"``, ``"anthropic"``, or
            ``"gcp.gemini"``. Use the well-known ``gen_ai.provider.name`` value
            when one applies.
        response_id: Provider response ID, or ``None``.
        finish_reasons: Provider finish reasons, one per returned choice.
    """

    request_model: str | None = None
    response_model: str | None = None
    provider_name: str | None = None
    response_id: str | None = None
    finish_reasons: Sequence[str] = ()


@dataclass(frozen=True, kw_only=True)
class TurnCompleted:
    """Report that the model call produced the turn's final answer.

    Attributes:
        new_messages: The turn's items that the session has not stored yet,
            in order: model output, tool requests, tool results, and the final
            answer. For a runner that never sets
            ``ToolCallsRequested.new_messages``, that is every item the turn
            produced after its user message, across all of its model calls.
            The session appends them in one commit.
        usage: Usage of this model call only.
        model_call_metadata: Model and response of this model call, for the
            ``chat`` span. ``None``, the default, leaves the span named ``chat``.
    """

    new_messages: Sequence[FrameworkMessage]
    usage: AgentUsage = field(default_factory=AgentUsage)
    model_call_metadata: ModelCallMetadata | None = None


@dataclass(frozen=True, kw_only=True)
class ToolCallsRequested:
    """Report that the model call asked for tools, which the session runs as Steps.

    The session commits the run state, ``new_messages``, and one record per
    tool call in one Step result, then starts the tool call Steps in parallel.

    A runner either keeps the turn's items inside ``framework_run_state`` until
    the turn completes, or stores them as they appear through
    ``new_messages``. Storing them keeps ``framework_run_state`` small, because
    the session rewrites it after every model call. A runner that sets
    ``new_messages`` must implement ``UnfinishedTurnMessageBuilder``; otherwise
    ``RunModel`` raises ``TypeError`` and the attempt fails.

    Example:
        >>> return ToolCallsRequested(
        ...     framework_run_state=json.dumps({"model_calls": 2}),
        ...     tool_calls=[DeferredToolCall(tool_call_id="call-1", tool_name="lookup",
        ...                                  arguments='{"id": 7}',
        ...                                  policy=turn.resolve_tool_policy("lookup"))],
        ...     new_messages=[previous_tool_results, assistant_tool_call_item],
        ... )

    Attributes:
        framework_run_state: Framework-serialized state that continues the
            turn after the tools return. It must be non-empty. The session
            returns it unchanged as ``AgentTurnContext.framework_run_state``.
        tool_calls: At least one tool call, in the order the model made them.
        usage: Usage of this model call only.
        model_call_metadata: Model and response of this model call, for the
            ``chat`` span. ``None``, the default, leaves the span named ``chat``.
        new_messages: Items the session appends now, in the same commit that
            records the tool calls, such as the previous round's tool results
            and the assistant item that asks for these tools. Defaults to none.
    """

    framework_run_state: str
    tool_calls: Sequence[DeferredToolCall]
    usage: AgentUsage = field(default_factory=AgentUsage)
    model_call_metadata: ModelCallMetadata | None = None
    new_messages: Sequence[FrameworkMessage] = ()


#: The result of one ``run_model_step`` call.
AgentTurnOutcome: TypeAlias = TurnCompleted | ToolCallsRequested


@dataclass(frozen=True, kw_only=True)
class AgentTurnContext:
    """Give ``run_model_step`` everything it needs for one model call of a turn.

    The session builds a new context for every ``RunModel`` attempt from
    committed state. A retry after a crash receives the same messages, run
    state, and records as the failed attempt, with a larger ``attempt``, so
    only that one model call runs again.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        turn_id: ``"turn-<n>"`` of the running turn.
        attempt: One-based ``RunModel`` attempt.
        messages: Every stored message in sequence order. The last user
            message belongs to this turn. After compaction, only the messages
            after ``context_summary.last_sequence``.
        framework_run_state: The value from this turn's last
            ``ToolCallsRequested``, or ``None`` for the turn's first model call.
        tool_calls: Every tool call record of this turn by tool call ID. When
            ``RunModel`` runs, every record is final.
        tool_policies: Explicit policies from ``AgentSessionOptions``.
        default_tool_effect: Effect for a tool without policy or metadata.
        events: Writer for live events, bound to this turn and attempt.
        heartbeat: Coroutine function that records Step liveness. The session
            already heartbeats in the background; call it only from a loop that
            could otherwise starve the event loop.
        enabled_built_in_tools: Built-in tools from
            ``AgentSessionOptions.built_in_tools``. Offer their definitions from
            ``BUILT_IN_TOOL_DEFINITIONS`` to the model. Empty by default.
        interaction_mode: The turn's ``AgentState.interaction_mode``. A
            ``PlanModeRunner`` offers only ``write_todos`` in a ``PLANNING``
            turn. Defaults to ``CHAT``.
        plan: The ``AgentPlan`` as committed before this model call, or ``None``
            before ``write_todos`` first succeeded. Show it to the model with
            ``build_plan_note``. Defaults to ``None``.
        context_summary: The summary of the messages before ``messages``, or
            ``None``. Only a runner that implements
            ``ContextCompactionRunner`` receives one; put its text before
            ``messages`` in the model input.
        session_settings: The settings this turn started with, such as
            ``{"model": "gpt-5-mini"}``. Use them to choose the model or the
            instructions of each model call. ``update_session_settings``
            changes them from the next turn on, so every model call of one
            turn sees the same value, retries included. Empty when the
            session has no settings, such as a session started on
            dex-agents 0.1.0. Treat the mapping as read-only.
    """

    flow_id: str
    turn_id: str
    attempt: int
    messages: Sequence[AgentMessage]
    framework_run_state: str | None
    tool_calls: Mapping[str, AgentToolCall]
    tool_policies: Mapping[str, ToolPolicy]
    default_tool_effect: ToolEffect
    events: AgentEventWriter
    heartbeat: Callable[[], Awaitable[None]]
    enabled_built_in_tools: frozenset[BuiltInTool] = frozenset()
    interaction_mode: InteractionMode = InteractionMode.CHAT
    plan: AgentPlan | None = None
    context_summary: ContextSummary | None = None
    session_settings: Mapping[str, Any] = field(default_factory=dict)

    @property
    def is_resuming(self) -> bool:
        """Report whether this model call continues the turn after tool calls.

        Returns:
            ``True`` when ``framework_run_state`` is set.
        """
        return self.framework_run_state is not None

    def resolve_tool_policy(
        self,
        tool_name: str,
        framework_effect: ToolEffect | None = None,
    ) -> ToolPolicy:
        """Return the policy for one tool call.

        An explicit policy wins over ``framework_effect``, which wins over
        ``default_tool_effect``. Put the returned policy in the
        ``DeferredToolCall`` for that call.

        Args:
            tool_name: Tool name the model called.
            framework_effect: Effect derived from framework metadata, or ``None``.

        Returns:
            A policy whose ``requires_approval`` is never ``None``.
        """
        return resolve_tool_policy(
            tool_name, self.tool_policies, self.default_tool_effect, framework_effect
        )

    def recorded_tool_call(self, tool_call_id: str) -> AgentToolCall | None:
        """Return this turn's record for a tool call ID.

        Args:
            tool_call_id: Framework tool call ID.

        Returns:
            The record, or ``None`` when the turn never deferred that call.
        """
        return self.tool_calls.get(tool_call_id)


@dataclass(frozen=True, kw_only=True)
class DeferredToolExecution:
    """Give ``execute_deferred_tool_call`` one approved tool call to run.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        tool_call: The approved record.
        idempotency_key: ``"<flow_id>/<turn_id>/<tool_call_id>"``. It is the same
            on every attempt. Pass it to the external system when it supports
            idempotency.
        attempt: One-based Step attempt. ``IDEMPOTENT_WRITE`` and approved
            ``READ`` calls get up to three attempts; other calls get one.
        events: Writer for live events, bound to the turn and attempt.
        heartbeat: Coroutine function that records Step liveness. The session
            already heartbeats in the background.
        heartbeat_checkpoint: The checkpoint an earlier attempt of this call
            saved with ``save_heartbeat_checkpoint``, or ``None``. Use it to
            resume work that the earlier attempt started, such as polling a
            long-running job, instead of starting it again.
        save_heartbeat_checkpoint: Coroutine function that records a heartbeat
            carrying a checkpoint. Every later heartbeat of the attempt,
            including the session's background heartbeats, carries it too, so
            the next attempt reads it as ``heartbeat_checkpoint``. ``None`` when
            the caller cannot save checkpoints.
        input_answers: Answers to the questions this tool asked through
            ``ToolInputRequested``, oldest first. Empty on the first run. The
            continuation of the last question is
            ``tool_call.input_continuation``.
        supports_tool_tasks: Whether the runner implements ``ToolTaskPoller``,
            so the tool may return ``ToolTaskStarted``. When it is ``False``,
            run remote work to its end inside the call instead. Defaults to
            ``False``.
        context: The tool Step's live ``AsyncContext``, or ``None`` when the
            caller has none, such as a unit test. It gives the Step execution
            ID, the attempt deadline, and heartbeats. Use it only during this
            call, never store it, and record heartbeats only through
            ``heartbeat`` and ``save_heartbeat_checkpoint``, because a bare
            ``context.heartbeat`` would drop the checkpoint. It is never part of
            stored state. ``ConnectorToolSource`` derives the connector Call ID
            from its Step execution ID. Defaults to ``None``.

            .. versionadded:: 0.4.0
    """

    flow_id: str
    tool_call: AgentToolCall
    idempotency_key: str
    attempt: int
    events: AgentEventWriter
    heartbeat: Callable[[], Awaitable[None]]
    heartbeat_checkpoint: str | None = None
    save_heartbeat_checkpoint: Callable[[str], Awaitable[None]] | None = None
    input_answers: Sequence[QuestionAnswer] = ()
    supports_tool_tasks: bool = False
    context: AsyncContext | None = field(default=None, repr=False, compare=False)


@dataclass(frozen=True, kw_only=True)
class ToolExecutionResult:
    """Return the output of one deferred tool call.

    Attributes:
        output: Tool output text the model sees.
        truncated_output_bytes: Size in bytes of the full output when ``output``
            is a truncated copy, or ``None``, the default, when it is whole. The
            session stores it as ``AgentToolCall.truncated_output_bytes``.

            .. versionadded:: 0.4.0
        images: Images the tool returns with ``output``, uploaded with
            ``MediaStore.put_image`` inside the call, at most
            ``MAX_IMAGES_PER_MESSAGE``. The session stores them as
            ``AgentToolCall.images``; a reference that
            ``validate_image_reference`` refuses fails the attempt. Upload
            before returning: a retried attempt uploads the same bytes to the
            same object. Defaults to none.

            .. versionadded:: 0.5.0
    """

    output: str
    truncated_output_bytes: int | None = None
    images: Sequence[AgentImageReference] = ()


@dataclass(frozen=True, kw_only=True)
class ToolCallStepOptions:
    """Override the timeout and retry policy of one tool call's Step.

    A runner returns it from ``ToolCallStepOptionsProvider.tool_call_step_options``.
    The session applies it to the movement that starts the call's tool Step,
    and to no other Step. A ``CallSingleAttemptTool`` call takes only
    ``execute_method_timeout``; it still runs one attempt.

    Example:
        >>> ToolCallStepOptions(
        ...     execute_method_timeout=timedelta(seconds=30),
        ...     execute_retry=RetryPolicy(maximum_attempts=3, total_duration=timedelta(hours=1)),
        ... )

    Attributes:
        execute_method_timeout: Timeout of each attempt, or ``None`` for the
            tool Step's default.
        execute_retry: Retry policy of a ``CallTool`` call, or ``None`` for the
            default of three attempts.

    .. versionadded:: 0.4.0
    """

    execute_method_timeout: timedelta | None = None
    execute_retry: RetryPolicy | None = None


@dataclass(frozen=True, kw_only=True)
class ToolInputRequested:
    """Ask the user a question on behalf of a tool that needs input to continue.

    Return it from ``execute_deferred_tool_call`` instead of a
    ``ToolExecutionResult`` only when the call has taken no effect yet. The
    tool Step records ``awaiting_answer`` with the question and moves to
    ``AwaitQuestionAnswer``, which raises a ``question`` interrupt with ID
    ``"<tool_call_id>-input-<n>"`` under the ``PendingInterrupts`` lock and
    waits for the answer. Parallel tool calls can ask at the same time.
    After ``answer_question``, the same tool Step type runs the call again,
    with every answer in ``DeferredToolExecution.input_answers`` and this
    ``continuation`` in ``tool_call.input_continuation``. The tool's later
    output is the result the model sees. A declined question or a passed
    deadline records ``rejected`` instead.

    A single-attempt tool that runs again after an answer still never repeats
    an effect, because the first run took none.

    Example:
        >>> async def execute_deferred_tool_call(self, call):
        ...     if not call.input_answers:
        ...         return ToolInputRequested(question="Which account?",
        ...                                   choices=["checking", "savings"],
        ...                                   allows_free_text=False,
        ...                                   continuation='{"step": "pick_account"}')
        ...     return ToolExecutionResult(output=transfer(call.input_answers[-1].answer))

    Attributes:
        question: Question text the user sees. It must not be blank.
        choices: Answers to offer. Empty means free text only.
        allows_free_text: Whether an answer outside ``choices`` is accepted.
            Ignored, as ``True``, when ``choices`` is empty.
        continuation: Opaque text the tool needs to continue, or ``None``.
    """

    question: str
    choices: Sequence[str] = ()
    allows_free_text: bool = True
    continuation: str | None = None


@dataclass(frozen=True, kw_only=True)
class ToolTaskStarted:
    """Report that a tool started remote work and returned a handle to poll.

    Return it from ``execute_deferred_tool_call`` when the tool's work runs
    elsewhere and outlives the call, such as an MCP task. The tool Step records
    ``waiting_for_task`` with ``task_id`` and the poll interval, writes
    ``tool_task_started``, and ends. ``PollToolTask`` then waits on a Timer for
    the interval and calls ``ToolTaskPoller.poll_tool_task`` once per
    execution, so no Step or connection is held while the work runs, and a
    Worker restart only delays the next poll. The runner must implement
    ``ToolTaskPoller``; without it the session records the call ``failed`` for
    a ``READ`` tool and ``outcome_unknown`` otherwise.

    The tool Step commits the handle with its result. A ``CallTool`` attempt
    that dies after the remote work started runs again, so save the handle with
    ``DeferredToolExecution.save_heartbeat_checkpoint`` before you return it,
    and return the saved handle when ``heartbeat_checkpoint`` holds one. Once
    the checkpoint has reached Dex, a retried attempt resumes the same work.
    Dex may drop a heartbeat checkpoint, though, so an attempt that dies before
    its checkpoint is saved and before the tool Step commits can still start
    the work a second time on a ``CallTool`` retry; pass the idempotency key to
    the remote system so it can drop the duplicate. Steering waits for a
    started task. ``cancel_turn`` abandons it: the session records
    ``outcome_unknown`` and asks a ``ToolTaskCanceller`` runner to cancel it.

    Example:
        >>> async def execute_deferred_tool_call(self, call):
        ...     if call.heartbeat_checkpoint is not None:
        ...         return ToolTaskStarted(task_id=call.heartbeat_checkpoint,
        ...                                poll_interval_seconds=5)
        ...     job_id = await reports.start(call.idempotency_key)
        ...     await call.save_heartbeat_checkpoint(job_id)
        ...     return ToolTaskStarted(task_id=job_id, poll_interval_seconds=5)

    Attributes:
        task_id: Handle of the remote work. It must not be blank. The session
            stores it as ``AgentToolCall.task_id``.
        poll_interval_seconds: Seconds to wait before the first poll. The
            session rounds it up to whole seconds, at least 1, because Dex
            Timers take whole seconds, and shortens it to at most a day.
    """

    task_id: str
    poll_interval_seconds: int


@dataclass(frozen=True, kw_only=True)
class ToolTaskPending:
    """Report that a polled task is still running.

    Return it from ``ToolTaskPoller.poll_tool_task``. ``PollToolTask`` stores
    the interval and the continuation, and polls again after the interval.

    Attributes:
        poll_interval_seconds: Seconds to wait before the next poll. The
            session rounds it up to whole seconds, from 1 to a day.
        continuation: Text the next poll reads as
            ``tool_call.input_continuation``, such as which question the task
            took an answer to and when. ``None``, the default, clears it: keep
            what the next poll needs, and drop a question's continuation once
            the task took its answer, so an identical later question reads as
            new.
    """

    poll_interval_seconds: int
    continuation: str | None = None


@dataclass(frozen=True, kw_only=True)
class ToolTaskPoll:
    """Give ``poll_tool_task`` one task to poll.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        tool_call: The call's record, ``WAITING_FOR_TASK``, with ``task_id``,
            ``task_poll_interval_seconds``, and ``tool_source_reference``.
        idempotency_key: ``"<flow_id>/<turn_id>/<tool_call_id>"``, the key the
            tool Step passed when it started the task.
        attempt: One-based ``PollToolTask`` attempt. Each poll gets up to three
            attempts.
        input_answers: How the questions this call asked were resolved,
            oldest first, whether its tool or its task asked them.
        events: Writer for live events, bound to the turn and attempt.
        heartbeat: Coroutine function that records Step liveness. The session
            already heartbeats in the background.
        pending_answer: The resolution of the task's last question, which has
            not reached the task yet, or ``None``. It is set only on the first
            poll after ``AwaitQuestionAnswer`` resolved the question, which runs
            at once. Deliver it to the task: the answer when ``resolution`` is
            ``ANSWERED``, a decline when it is ``DECLINED``, and a cancelled
            request when it is ``DEADLINE_PASSED``. ``tool_call.input_continuation``
            holds the question's continuation, so recognize the request it
            answers and deliver only to that request. Later polls read the
            continuation the last ``ToolTaskPending`` returned. A retried attempt
            gets it again; when it cannot tell whether an earlier attempt
            delivered it, return the task's question as ``ToolInputRequested``
            again rather than send it to what may be a later question. When the
            task cannot take the resolution, cancel the task best-effort and
            raise an exception other than ``ToolExecutionError``, so the session
            records the call by its effect.
    """

    flow_id: str
    tool_call: AgentToolCall
    idempotency_key: str
    attempt: int
    input_answers: Sequence[QuestionAnswer]
    events: AgentEventWriter
    heartbeat: Callable[[], Awaitable[None]]
    pending_answer: QuestionAnswer | None = None


@dataclass(frozen=True, kw_only=True)
class ToolTaskCancellation:
    """Give ``cancel_tool_task`` one task that ``cancel_turn`` abandoned.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        tool_call: The call's record with ``task_id`` and
            ``tool_source_reference``. Its ``cancellation_reason`` is
            ``TURN_CANCELLED``.
        idempotency_key: ``"<flow_id>/<turn_id>/<tool_call_id>"``, the key the
            tool Step passed when it started the task.
        attempt: One-based ``CancelToolTask`` attempt. Each cancellation gets
            up to three attempts.
        heartbeat: Coroutine function that records Step liveness. The session
            already heartbeats in the background.
    """

    flow_id: str
    tool_call: AgentToolCall
    idempotency_key: str
    attempt: int
    heartbeat: Callable[[], Awaitable[None]]


#: The result of one ``execute_deferred_tool_call`` call.
DeferredToolOutcome: TypeAlias = ToolExecutionResult | ToolInputRequested | ToolTaskStarted

#: The result of one ``poll_tool_task`` call.
ToolTaskPollOutcome: TypeAlias = ToolExecutionResult | ToolInputRequested | ToolTaskPending

if TYPE_CHECKING:
    # Type checkers read the default; Python 3.11 has no TypeVar defaults at runtime.
    from typing_extensions import TypeVar as DefaultedTypeVar

    ToolOutcomeT_co = DefaultedTypeVar(
        "ToolOutcomeT_co",
        bound=DeferredToolOutcome,
        covariant=True,
        default=ToolExecutionResult,
    )
else:
    ToolOutcomeT_co = TypeVar("ToolOutcomeT_co", bound=DeferredToolOutcome, covariant=True)


class ToolOutcomeUnknownError(Exception):
    """Report that a tool call may or may not have taken effect, so it must not run again.

    Raise it from ``execute_deferred_tool_call`` when the tool sent a write
    and cannot tell whether it took effect, such as a connector operation's
    ``uncertain`` branch. The session records ``OUTCOME_UNKNOWN`` with
    ``str(error)`` as ``error``, does not retry, and the model sees
    ``OUTCOME_UNKNOWN_TOOL_RESULT``. A later ordered write of the turn is
    skipped.

    ``ToolTaskPoller.poll_tool_task`` may raise it too, for a task that
    ended without a known outcome. The session then records
    ``OUTCOME_UNKNOWN`` the same way and stops polling.

    Example:
        >>> raise ToolOutcomeUnknownError("the provider timed out after receiving the refund")

    .. versionadded:: 0.4.0

    .. versionchanged:: 0.5.0
        ``poll_tool_task`` may raise it.
    """


class ToolExecutionError(Exception):
    """Report a tool failure that the model should see.

    Raise it from ``execute_deferred_tool_call`` when the tool ran and failed
    in a way the model can react to, such as invalid arguments. The session
    records ``FAILED`` with ``str(error)`` and does not retry. Any other
    exception is a Step failure. A ``READ`` or ``IDEMPOTENT_WRITE`` call
    retries up to three attempts; a single-attempt call does not retry. When
    the attempts run out, the session records ``FAILED`` for a ``READ`` call
    and ``OUTCOME_UNKNOWN`` for a write, because the write may have taken
    effect.

    Example:
        >>> raise ToolExecutionError("order 42 is already shipped")
    """


class AgentFrameworkRunner(Protocol[ToolOutcomeT_co]):
    """Run one agent framework inside the durable agent session Flow.

    Implement this protocol once per framework and pass the runner to
    ``AgentSessionFlow``. The session calls it from Steps, so every method may
    run again after a crash or retry: keep the runner stateless between calls
    and derive everything from the arguments. Tool calls run in parallel
    Steps, so ``execute_deferred_tool_call`` may run concurrently with itself.

    The type parameter is what ``execute_deferred_tool_call`` returns. It
    defaults to ``ToolExecutionResult``, so a bare ``AgentFrameworkRunner``
    means the same as in 0.1.0. A runner whose tools can ask the user a
    question or start a remote task implements
    ``AgentFrameworkRunner[DeferredToolOutcome]``.

    Example:
        >>> class EchoRunner:
        ...     def build_user_message(self, text: str) -> FrameworkMessage:
        ...         return FrameworkMessage(role=MessageRole.USER, text=text,
        ...                                 framework_item=json.dumps({"user": text}))
        ...
        ...     async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        ...         text = turn.messages[-1].text or ""
        ...         return TurnCompleted(new_messages=[FrameworkMessage(
        ...             role=MessageRole.ASSISTANT, text=text,
        ...             framework_item=json.dumps({"assistant": text}))])
        ...
        ...     async def execute_deferred_tool_call(
        ...         self, call: DeferredToolExecution
        ...     ) -> ToolExecutionResult:
        ...         raise ToolExecutionError("this agent has no deferred tools")
    """

    def build_user_message(self, text: str) -> FrameworkMessage:
        """Convert user text into the framework item the session stores.

        The session calls it once per user message, before ``run_model_step``.
        ``RecordTurnFailure`` also calls it for the turn-failure note.

        Args:
            text: The user message text.

        Returns:
            A ``MessageRole.USER`` message.
        """
        ...

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        """Let the framework make at most one model call of the turn.

        Without run state, rebuild framework input from ``turn.messages``. With
        run state, restore the framework from it and give every tool call the
        result from its final record in ``turn.tool_calls``
        (``describe_tool_call_outcome`` renders it). Then let the framework make
        one model call and stop it before it runs any tool.

        Return ``ToolCallsRequested`` with one ``DeferredToolCall`` per tool
        call of the response, each with ``turn.resolve_tool_policy``. Return
        ``TurnCompleted`` when the response is the final answer. Never run a
        tool here, unless the framework cannot defer it; document such tools
        as at-least-once.

        Args:
            turn: Committed session state for this model call.

        Returns:
            ``TurnCompleted`` or ``ToolCallsRequested``.

        Raises:
            Exception: Any exception fails the attempt. ``RunModel`` retries up
                to three attempts within 30 minutes, then records the turn as
                failed in ``last_turn_error``.
        """
        ...

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolOutcomeT_co:
        """Run one tool call exactly as the framework would, in its own Step.

        Args:
            call: The approved record and its idempotency key.

        Returns:
            The tool output the model sees; ``ToolInputRequested`` when the
            tool needs an answer from the user before it takes any effect; or
            ``ToolTaskStarted`` when the tool started remote work that
            ``ToolTaskPoller.poll_tool_task`` polls.

        Raises:
            ToolExecutionError: The tool failed in a way the model should see.
            Exception: Any other exception fails the Step attempt. After the
                last attempt the record becomes ``FAILED`` for a ``READ`` call
                and ``OUTCOME_UNKNOWN`` for a write.
        """
        ...


@runtime_checkable
class ImageUserMessageBuilder(Protocol):
    """Let a runner show the images of a user message to its model.

    Implement it next to ``AgentFrameworkRunner`` when the runner can send
    images to its model. ``send_user_message`` accepts images only for a runner
    with this method, and the session calls it instead of
    ``build_user_message`` for a message with images. Put the references,
    never the bytes, in ``framework_item``; read the bytes with
    ``dex_agents.load_images`` when the model call builds its input. The
    session sets the stored message's ``images`` itself.

    Example:
        >>> class MyRunner:
        ...     def build_user_message_with_images(self, text, images):
        ...         item = {"user": text, "images": [asdict(image) for image in images]}
        ...         return FrameworkMessage(role=MessageRole.USER, text=text,
        ...                                 framework_item=json.dumps(item))

    .. versionadded:: 0.5.0
    """

    def build_user_message_with_images(
        self, text: str, images: Sequence[AgentImageReference]
    ) -> FrameworkMessage:
        """Convert user text and its images into the framework item the session stores.

        It must be deterministic and free of side effects, like
        ``build_user_message``.

        Args:
            text: The user message text, never blank.
            images: The message's images, at least one, each valid for
                ``validate_image_reference``.

        Returns:
            A ``MessageRole.USER`` message.
        """
        ...


@runtime_checkable
class UnfinishedTurnMessageBuilder(Protocol):
    """Let a runner that stores items incrementally finish a turn's transcript.

    Implement it next to ``AgentFrameworkRunner`` when the runner sets
    ``ToolCallsRequested.new_messages``. The session calls it when a turn ends
    without a final answer, in ``RecordTurnFailure`` and, after steering or
    ``cancel_turn``, in ``ApplyTurnControl``, so the stored transcript never
    holds a tool call without its result. The session checks for the method at
    runtime; a runner without it behaves as before. A runner that keeps its
    items in ``framework_run_state`` can implement it too: after steering, the
    next model input then holds the turn's exact tool calls and results instead
    of a note.

    Example:
        >>> class MyRunner:
        ...     def build_unfinished_turn_messages(self, turn):
        ...         return [tool_result_message(record) for record in turn.tool_calls.values()
        ...                 if not is_stored(turn, record.tool_call_id)]
    """

    def build_unfinished_turn_messages(self, turn: AgentTurnContext) -> Sequence[FrameworkMessage]:
        """Return the items of an ending turn that the session has not stored yet.

        The session calls it with every record of the turn final. In
        ``RecordTurnFailure`` it stores the result before the turn-failure
        note. In ``ApplyTurnControl`` it stores the result instead of the
        steered or cancelled note. It makes no model call. If it raises, the
        session stores only the note.

        Args:
            turn: Committed state of the ending turn. ``events`` writes live
                events; ``attempt`` is the calling Step's attempt.

        Returns:
            The items not stored yet, including a tool result for every tool
            call already stored or returned by the turn.
        """
        ...


@runtime_checkable
class PlanModeRunner(Protocol):
    """Declare that a runner honors plan mode.

    ``AgentSessionOptions.plan_mode_enabled`` needs a runner that sets
    ``supports_plan_mode`` to ``True``; ``AgentSessionFlow`` raises otherwise.
    Such a runner reads ``AgentTurnContext.interaction_mode`` in every
    ``run_model_step``:

    * In a ``PLANNING`` turn it offers only the ``write_todos`` definition from
      ``BUILT_IN_TOOL_DEFINITIONS``. It may force ``write_todos`` on the
      turn's first model call while the plan is not a draft yet, where the
      model accepts a forced tool choice; Stepwise does with
      ``planning_tool_choice="required"``.
    * In every turn it shows the plan with ``build_plan_note(turn.plan,
      turn.interaction_mode)`` as user text before the turn's messages.

    The Stepwise runner implements it. A framework adapter that cannot change
    the tools it offers between turns leaves plan mode off.

    Example:
        >>> class MyRunner:
        ...     supports_plan_mode = True
    """

    @property
    def supports_plan_mode(self) -> bool:
        """Report whether the runner honors ``AgentTurnContext.interaction_mode``.

        Returns:
            ``True`` when the runner implements plan mode as described above.
        """
        ...


@runtime_checkable
class ToolCallStepOptionsProvider(Protocol):
    """Give a tool call's Step its own timeout and retry policy.

    A runner implements it when some tools need options other than the
    tool Steps' defaults, as ``StepwiseAgentRunner`` does for a tool source
    that defines ``tool_call_step_options``. The session calls it whenever it
    starts a call's tool Step, including after an approval, so it must
    answer from the record alone. A method that raises leaves the defaults.

    .. versionadded:: 0.4.0
    """

    def tool_call_step_options(self, tool_call: AgentToolCall) -> ToolCallStepOptions | None:
        """Return the options of one call's tool Step.

        Args:
            tool_call: The record whose tool Step starts.

        Returns:
            The options, or ``None`` for the defaults.
        """
        ...


@runtime_checkable
class ToolTaskPoller(Protocol):
    """Let a runner whose tools return ``ToolTaskStarted`` poll their tasks.

    Implement it next to ``AgentFrameworkRunner``. ``PollToolTask`` calls
    ``poll_tool_task`` once per execution, after a Timer for the call's poll
    interval, until it returns a result. The session checks for the method at
    runtime; a runner without it never starts a task.

    Example:
        >>> class MyRunner:
        ...     async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        ...         job = await reports.get(poll.tool_call.task_id)
        ...         if job.done:
        ...             return ToolExecutionResult(output=job.summary)
        ...         return ToolTaskPending(poll_interval_seconds=10)
    """

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        """Poll one remote task once.

        Polling must only read the task, and deliver ``poll.pending_answer``
        when it is set, because the session may poll again after a crash.
        Steering never stops a poll. ``cancel_turn`` cancels a running poll,
        and the session drops whatever it returns.

        Args:
            poll: The call's record and its idempotency key.

        Returns:
            ``ToolExecutionResult`` when the task finished, which the model
            sees; ``ToolTaskPending`` while it runs; or ``ToolInputRequested``
            when it needs the user's answer. The session asks the question
            through ``AwaitQuestionAnswer`` and polls again at once with its
            resolution, answered, declined, or past its deadline, as
            ``pending_answer``. The task's later result is the call's result.

        Raises:
            ToolExecutionError: The task failed in a way the model should see.
                The session records ``FAILED``.
            ToolOutcomeUnknownError: The task ended, and it may or may not
                have taken effect. The session records ``OUTCOME_UNKNOWN``
                and does not poll again.
            Exception: Any other exception fails the poll attempt. After three
                attempts the record becomes ``FAILED`` for a ``READ`` call and
                ``OUTCOME_UNKNOWN`` otherwise, because the task may still run.

        .. versionchanged:: 0.5.0
            ``ToolOutcomeUnknownError`` records ``OUTCOME_UNKNOWN``.
        """
        ...


@runtime_checkable
class ToolTaskCanceller(Protocol):
    """Let a runner that polls tool tasks cancel one that ``cancel_turn`` abandoned.

    Implement it next to ``ToolTaskPoller``. ``cancel_turn`` does not wait for
    a started task: the session records the call ``outcome_unknown`` with
    ``cancellation_reason`` ``TURN_CANCELLED``, and the ``CancelToolTask``
    Step calls ``cancel_tool_task`` best-effort before it records the call. A
    runner without the method leaves the task running. Steering never
    cancels a task.

    Example:
        >>> class MyRunner:
        ...     async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        ...         await reports.cancel(cancellation.tool_call.task_id)
    """

    async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        """Ask the remote system to stop one task.

        The session may call it more than once for one task, after a crash or
        a failed attempt, so make it idempotent by ``task_id``. A task that
        already finished or that the remote system no longer knows is
        cancelled: return normally. The call's record is final whatever this
        method does, and a result the task returns later is dropped.

        Args:
            cancellation: The abandoned call's record and its idempotency key.

        Raises:
            Exception: The cancellation did not reach the remote system. The
                session retries it up to three times, then records the call
                ``outcome_unknown`` anyway, so the turn always ends.
        """
        ...
