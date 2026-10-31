# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The contract between the agent session Flow and a framework adapter.

An adapter implements ``AgentFrameworkRunner`` for one agent framework. The
session Flow calls it from durable Steps:

* ``build_user_message`` turns user text into a stored framework item.
* ``run_turn`` runs or resumes the framework loop for one turn segment. It
  runs model calls and READ tools inline, defers every other tool call, and
  returns ``TurnCompleted`` or ``TurnSuspended``.
* ``execute_deferred_tool_call`` runs one deferred tool call in its own Step.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Protocol, TypeAlias

from dex_agents.events import AgentEventWriter
from dex_agents.values import (
    AgentMessage,
    AgentToolCall,
    AgentUsage,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
)

#: Model-visible text for a call whose outcome is unknown.
OUTCOME_UNKNOWN_TOOL_RESULT = (
    "The tool call's outcome is unknown: the Worker stopped while the tool ran, so it may "
    "or may not have taken effect. It was not retried."
)

#: Model-visible text for a rejection without a message.
DEFAULT_REJECTION_TOOL_RESULT = "The user rejected this tool call."

#: Rejection message recorded when an interrupt deadline passes.
APPROVAL_DEADLINE_PASSED = "approval deadline passed"


@dataclass(frozen=True)
class ToolPolicy:
    """Declare how the session treats calls to one tool.

    Pass policies by tool name in ``AgentSessionOptions.tool_policies``. A
    policy overrides framework metadata, such as MCP ``readOnlyHint``, and the
    session-level ``default_tool_effect``.

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

    @property
    def should_defer(self) -> bool:
        """Report whether the runner must defer calls instead of running them inline.

        Returns:
            ``False`` only for a ``READ`` tool that needs no approval.
        """
        return self.effect is not ToolEffect.READ or self.is_approval_required


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


@dataclass(frozen=True, kw_only=True)
class FrameworkMessage:
    """Describe one framework item that the session should store.

    A runner returns these from ``build_user_message`` and in
    ``TurnCompleted.new_messages``. The session adds the sequence, turn ID, and
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
    """Ask the session to run one tool call in a Dex Step.

    Return these in ``TurnSuspended.deferred_tool_calls`` for every tool call
    whose policy says ``should_defer``. The session records each call,
    raises an interrupt when the policy requires approval, runs approved calls
    in order, and then calls ``run_turn`` again with the final records.

    Attributes:
        tool_call_id: Framework tool call ID. It must be non-empty, contain no
            ``/``, and be unique within the turn.
        tool_name: Tool name the model called.
        arguments: Tool arguments as JSON text.
        policy: Policy from ``AgentTurnContext.resolve_tool_policy``.
    """

    tool_call_id: str
    tool_name: str
    arguments: str
    policy: ToolPolicy


@dataclass(frozen=True, kw_only=True)
class TurnCompleted:
    """Report that the framework produced the turn's final answer.

    Attributes:
        new_messages: Every item the turn produced after its user message, in
            order and across all segments of the turn: model output, tool
            requests, tool results (including deferred ones), and the final
            answer. The session appends them in one commit.
        usage: Model usage of this segment only.
    """

    new_messages: Sequence[FrameworkMessage]
    usage: AgentUsage = field(default_factory=AgentUsage)


@dataclass(frozen=True, kw_only=True)
class TurnSuspended:
    """Report that the framework paused for deferred tool calls.

    The session commits the run state and deferred calls in one Step result.
    The turn's new messages stay inside ``framework_run_state`` until the turn
    completes; the session stores no partial messages.

    Attributes:
        framework_run_state: Framework-serialized state that resumes the loop.
            It must be non-empty. The session returns it unchanged as
            ``AgentTurnContext.framework_run_state``.
        deferred_tool_calls: At least one deferred call.
        usage: Model usage of this segment only.
    """

    framework_run_state: str
    deferred_tool_calls: Sequence[DeferredToolCall]
    usage: AgentUsage = field(default_factory=AgentUsage)


#: The result of one ``run_turn`` call.
AgentTurnOutcome: TypeAlias = TurnCompleted | TurnSuspended


@dataclass(frozen=True, kw_only=True)
class AgentTurnContext:
    """Give ``run_turn`` everything it needs to run or resume one turn segment.

    The session builds a new context for every ``RunAgentTurn`` attempt from
    committed state. A retry after a crash receives the same messages, run
    state, and records as the failed attempt, with a larger ``attempt``.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        turn_id: ``"turn-<n>"`` of the running turn.
        attempt: One-based ``RunAgentTurn`` attempt.
        messages: Every stored message in sequence order. The last user
            message belongs to this turn.
        framework_run_state: The value from the last ``TurnSuspended`` of this
            turn, or ``None`` for the first segment.
        tool_calls: This turn's deferred tool call records by tool call ID.
            When resuming, every record is final.
        tool_policies: Explicit policies from ``AgentSessionOptions``.
        default_tool_effect: Effect for a tool without policy or metadata.
        events: Writer for live events, bound to this turn and attempt.
        heartbeat: Coroutine function that records Step liveness. The session
            already heartbeats in the background; call it only from a loop that
            could otherwise starve the event loop.
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

    @property
    def is_resuming(self) -> bool:
        """Report whether this segment resumes a suspended framework run.

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
        ``default_tool_effect``. Run the call inline only when the returned
        policy's ``should_defer`` is ``False``.

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
        idempotency_key: ``"<flow_id>/<tool_call_id>"``. Pass it to the external
            system when it supports idempotency.
        attempt: One-based Step attempt. ``IDEMPOTENT_WRITE`` and approved
            ``READ`` calls get up to three attempts; other calls get one.
        events: Writer for live events, bound to the turn and attempt.
        heartbeat: Coroutine function that records Step liveness. The session
            already heartbeats in the background.
    """

    flow_id: str
    tool_call: AgentToolCall
    idempotency_key: str
    attempt: int
    events: AgentEventWriter
    heartbeat: Callable[[], Awaitable[None]]


@dataclass(frozen=True, kw_only=True)
class ToolExecutionResult:
    """Return the output of one deferred tool call.

    Attributes:
        output: Tool output text the model sees.
    """

    output: str


class ToolExecutionError(Exception):
    """Report a tool failure that the model should see.

    Raise it from ``execute_deferred_tool_call`` when the tool ran and failed
    in a way the model can react to, such as invalid arguments. The session
    records ``FAILED`` with ``str(error)`` and does not retry. Any other
    exception is a Step failure: an ``IDEMPOTENT_WRITE`` call retries, and a
    single-attempt call records ``OUTCOME_UNKNOWN``.

    Example:
        >>> raise ToolExecutionError("order 42 is already shipped")
    """


class AgentFrameworkRunner(Protocol):
    """Run one agent framework inside the durable agent session Flow.

    Implement this protocol once per framework and pass the runner to
    ``AgentSessionFlow``. The session calls it from Steps, so every method may
    run again after a crash or retry: keep the runner stateless between calls
    and derive everything from the arguments.

    Example:
        >>> class EchoRunner:
        ...     def build_user_message(self, text: str) -> FrameworkMessage:
        ...         return FrameworkMessage(role=MessageRole.USER, text=text,
        ...                                 framework_item=json.dumps({"user": text}))
        ...
        ...     async def run_turn(self, turn: AgentTurnContext) -> AgentTurnOutcome:
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

        The session calls it once per user message, before ``run_turn``.

        Args:
            text: The user message text.

        Returns:
            A ``MessageRole.USER`` message.
        """
        ...

    async def run_turn(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        """Run or resume the framework loop for one turn segment.

        Without run state, rebuild framework input from ``turn.messages`` and
        start the loop. With run state, restore the framework from it and
        resume, giving every deferred call the result from its final record in
        ``turn.tool_calls`` (``describe_tool_call_outcome`` renders it). Never
        run a deferred tool here.

        Run model calls and tools whose policy has ``should_defer`` ``False``
        inline. Defer every other call with the framework's own mechanism and
        return ``TurnSuspended``. Return ``TurnCompleted`` once the framework
        produces its final answer.

        Args:
            turn: Committed session state for this segment.

        Returns:
            ``TurnCompleted`` or ``TurnSuspended``.

        Raises:
            Exception: Any exception fails the attempt. ``RunAgentTurn`` retries
                up to three attempts within 30 minutes, then records the turn
                as failed in ``last_turn_error``.
        """
        ...

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        """Run one deferred tool call exactly as the framework would.

        Args:
            call: The approved record and its idempotency key.

        Returns:
            The tool output the model sees.

        Raises:
            ToolExecutionError: The tool failed in a way the model should see.
            Exception: Any other exception fails the Step attempt.
        """
        ...
