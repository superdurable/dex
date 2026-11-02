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
turn's items incrementally through ``ToolCallsRequested.new_messages``.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Protocol, TypeAlias, TypeVar, runtime_checkable

from dex_agents.built_in_tools import BuiltInTool
from dex_agents.events import AgentEventWriter
from dex_agents.values import (
    AgentMessage,
    AgentToolCall,
    AgentUsage,
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
    lines = [TURN_FAILURE_NOTE_HEADER]
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
    """

    role: MessageRole
    framework_item: str
    text: str | None = None
    tool_call_id: str | None = None
    tool_name: str | None = None
    tool_arguments: str | None = None
    tool_output: str | None = None


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
    """

    tool_call_id: str
    tool_name: str
    arguments: str
    policy: ToolPolicy
    tool_source_reference: str | None = None


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
            message belongs to this turn.
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


@dataclass(frozen=True, kw_only=True)
class ToolExecutionResult:
    """Return the output of one deferred tool call.

    Attributes:
        output: Tool output text the model sees.
    """

    output: str


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


#: The result of one ``execute_deferred_tool_call`` call.
DeferredToolOutcome: TypeAlias = ToolExecutionResult | ToolInputRequested

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
    question implements ``AgentFrameworkRunner[DeferredToolOutcome]``.

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
            The tool output the model sees, or ``ToolInputRequested`` when the
            tool needs an answer from the user before it takes any effect.

        Raises:
            ToolExecutionError: The tool failed in a way the model should see.
            Exception: Any other exception fails the Step attempt. After the
                last attempt the record becomes ``FAILED`` for a ``READ`` call
                and ``OUTCOME_UNKNOWN`` for a write.
        """
        ...


@runtime_checkable
class UnfinishedTurnMessageBuilder(Protocol):
    """Let a runner that stores items incrementally finish a turn's transcript.

    Implement it next to ``AgentFrameworkRunner`` when the runner sets
    ``ToolCallsRequested.new_messages``. The session calls it when a turn ends
    without a final answer, such as in ``RecordTurnFailure``, so the stored
    transcript never holds a tool call without its result. The session checks
    for the method at runtime; a runner without it behaves as before.

    Example:
        >>> class MyRunner:
        ...     def build_unfinished_turn_messages(self, turn):
        ...         return [tool_result_message(record) for record in turn.tool_calls.values()
        ...                 if not is_stored(turn, record.tool_call_id)]
    """

    def build_unfinished_turn_messages(self, turn: AgentTurnContext) -> Sequence[FrameworkMessage]:
        """Return the items of an ending turn that the session has not stored yet.

        The session calls it with every record of the turn final, before it
        stores its turn-failure note. It makes no model call. If it raises,
        the session stores only the note.

        Args:
            turn: Committed state of the ending turn. ``events`` writes live
                events; ``attempt`` is the calling Step's attempt.

        Returns:
            The items not stored yet, including a tool result for every tool
            call already stored or returned by the turn.
        """
        ...
