# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Context compaction: keep the model input under the context window.

Set ``AgentSessionOptions.compaction`` to a ``CompactionPolicy`` with a runner
that implements ``ContextCompactionRunner``. Before each model call,
``RunModel`` asks the runner for the size of the next model input. When it
reaches ``trigger_ratio`` of the context window, ``RunModel`` makes no model
call. It moves to ``CompactContext``, which asks the runner to fold the earlier
messages into a ``ContextSummary``, and then back to ``RunModel``. From then
on, the runner receives only the messages after the summary, plus the summary.

Compaction changes the model input, not the record: every message stays in
``AgentMessages``, and ``list_agent_messages`` returns all of them.
"""

from __future__ import annotations

import math
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass, field
from typing import Protocol, runtime_checkable

from dex_agents.events import AgentEventWriter
from dex_agents.runner import AgentTurnContext, ModelCallMetadata
from dex_agents.values import AgentMessage, AgentUsage, ContextSummary


@dataclass(frozen=True, kw_only=True)
class CompactionPolicy:
    """Decide when a session summarizes its earlier messages, and how much it keeps.

    Pass it as ``AgentSessionOptions.compaction``, or as
    ``AgentDefinition.compaction`` for a Stepwise agent. The runner must
    implement ``ContextCompactionRunner``; the session Flow constructor raises
    otherwise. Enable compaction only after every Worker of the Flow type runs
    a release that has the ``CompactContext`` Step.

    Example:
        >>> options = AgentSessionOptions(
        ...     compaction=CompactionPolicy(context_window_tokens=400_000),
        ... )

    Attributes:
        context_window_tokens: The model's context window, in the tokens that
            the runner's ``estimate_input_tokens`` counts.
        trigger_ratio: Share of the window at which the session compacts
            before the next model call. Defaults to 0.85.
        retained_ratio: Share of the window that the newest messages may keep
            unsummarized. Defaults to 0.3.

    Raises:
        ValueError: If ``context_window_tokens`` is not a positive integer, or
            unless ``0 < retained_ratio < trigger_ratio <= 1``.
    """

    context_window_tokens: int
    trigger_ratio: float = 0.85
    retained_ratio: float = 0.3

    def __post_init__(self) -> None:
        if (
            not isinstance(self.context_window_tokens, int)
            or isinstance(self.context_window_tokens, bool)
            or self.context_window_tokens <= 0
        ):
            raise ValueError("context_window_tokens must be a positive integer")
        if not 0 < self.retained_ratio < self.trigger_ratio <= 1:
            raise ValueError("compaction needs 0 < retained_ratio < trigger_ratio <= 1")

    def is_triggered_by(self, estimated_input_tokens: int) -> bool:
        """Report whether a model input of this size makes the session compact first.

        Args:
            estimated_input_tokens: The runner's estimate of the next model input.

        Returns:
            ``True`` when the estimate is at least ``trigger_ratio`` of the window.
        """
        return estimated_input_tokens >= self.trigger_ratio * self.context_window_tokens

    @property
    def retained_token_budget(self) -> int:
        """Return the tokens the newest messages may keep after compaction.

        Returns:
            ``retained_ratio`` of the window, rounded down.
        """
        return math.floor(self.retained_ratio * self.context_window_tokens)


@dataclass(frozen=True, kw_only=True)
class ContextSummaryRequest:
    """Give ``summarize_context`` the messages to fold into a new summary.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        turn_id: ``"turn-<n>"`` of the running turn.
        attempt: One-based ``CompactContext`` attempt.
        previous_summary: The summary so far, or ``None`` for the first one.
            Fold its text into the new summary.
        messages: The messages after the previous summary through
            ``through_sequence``, in sequence order. No tool call is separated
            from its result.
        through_sequence: Sequence of the last message to summarize.
        events: Writer for live events, bound to the turn and attempt.
        heartbeat: Coroutine function that records Step liveness. The session
            already heartbeats in the background.
        input_token_budget: Tokens the summary call may use, the policy's
            ``context_window_tokens``, or ``None`` for no limit. A runner
            keeps the input below it, less what it reserves for the summary,
            such as its ``max_output_tokens``.
            The range can be larger after the window shrank or when
            compaction starts on a long session; leave out its oldest part
            rather than send an over-window request that fails every turn.
    """

    flow_id: str
    turn_id: str
    attempt: int
    previous_summary: ContextSummary | None
    messages: Sequence[AgentMessage]
    through_sequence: int
    events: AgentEventWriter
    heartbeat: Callable[[], Awaitable[None]]
    input_token_budget: int | None = None


@dataclass(frozen=True, kw_only=True)
class ContextSummaryResult:
    """Return the text of a new summary and the usage of the call that wrote it.

    Attributes:
        text: Summary text. It must not be blank. It replaces the previous
            summary, so it includes what that one said.
        usage: Usage of the summary model call. The session adds it to the
            session's ``AgentUsage``.
        model_call_metadata: Model and response of the summary call, for the
            ``CompactContext`` attempt's ``chat`` span. ``None``, the default,
            leaves the span named ``chat``.
    """

    text: str
    usage: AgentUsage = field(default_factory=AgentUsage)
    model_call_metadata: ModelCallMetadata | None = None


@runtime_checkable
class ContextCompactionRunner(Protocol):
    """Let a runner summarize earlier messages so the model input fits its window.

    Implement it next to ``AgentFrameworkRunner`` to support
    ``AgentSessionOptions.compaction``. The session checks for the methods at
    runtime. A runner without them never compacts, and the session Flow
    constructor refuses a compaction policy for it.

    The session calls ``estimate_input_tokens`` before every model call. At
    the threshold it calls ``select_compaction_cutoff``, and when that returns
    a sequence after the current summary, ``CompactContext`` calls
    ``summarize_context`` once and stores the result. A crash during the
    summary call reruns only that call. Once a summary exists, the runner's
    ``AgentTurnContext.messages`` start after it, and
    ``AgentTurnContext.context_summary`` holds it.

    Example:
        >>> class MyRunner:
        ...     def estimate_input_tokens(self, turn):
        ...         return count_tokens(build_model_input(turn))
        ...
        ...     def select_compaction_cutoff(self, turn, retained_token_budget):
        ...         return last_sequence_before_the_newest(turn.messages, retained_token_budget)
        ...
        ...     async def summarize_context(self, request):
        ...         response = await model.summarize(request.previous_summary, request.messages)
        ...         return ContextSummaryResult(text=response.text, usage=response.usage)
    """

    def estimate_input_tokens(self, turn: AgentTurnContext) -> int:
        """Estimate the size of the model input that the next ``run_model_step`` sends.

        Args:
            turn: The same context the next ``run_model_step`` receives.

        Returns:
            The estimated input tokens, in the unit of
            ``CompactionPolicy.context_window_tokens``.
        """
        ...

    def select_compaction_cutoff(
        self, turn: AgentTurnContext, retained_token_budget: int
    ) -> int | None:
        """Choose the last message to summarize, so the newest messages fit a budget.

        Never separate a tool call from its result: the messages after the
        cutoff must not start with a tool result or hold a result whose call
        is before the cutoff. Keep the turn's unstored tool results answerable.

        Args:
            turn: The context of the model call that crossed the threshold.
            retained_token_budget: Tokens the messages after the cutoff may use.

        Returns:
            The sequence of the last message to summarize, one of
            ``turn.messages``; or ``None`` when nothing can be summarized,
            which lets the model call proceed.
        """
        ...

    async def summarize_context(self, request: ContextSummaryRequest) -> ContextSummaryResult:
        """Fold the previous summary and a range of messages into a new summary.

        Make at most one model call. Write no text to ``request.events`` as
        model output: the summary is not the assistant's answer.

        Args:
            request: The previous summary and the messages to summarize.

        Returns:
            The new summary text and the usage of the call.

        Raises:
            Exception: Any exception fails the ``CompactContext`` attempt. It
                retries up to three attempts within 30 minutes, then the turn
                fails through ``RecordTurnFailure``.
        """
        ...
