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

Compaction changes the model input. Summarized messages stay stored until
retention deletes them; see ``AgentSessionOptions.retained_message_limit``.

A framework adapter assembles its prompt inside the framework, so it cannot
measure the exact request and cannot replay items into a summary call. The
functions here give adapters one shared rule: ``estimate_stored_input_tokens``
measures the stored items, ``select_turn_boundary_cutoff`` cuts only between
turns, and ``build_context_summary_prompt`` turns a range into one text prompt.
"""

from __future__ import annotations

import math
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any, Protocol, runtime_checkable

from dex_agents.events import AgentEventWriter
from dex_agents.runner import AgentTurnContext, ModelCallMetadata
from dex_agents.values import AgentMessage, AgentUsage, ContextSummary

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

#: First line of the transcript in the prompt of ``build_context_summary_prompt``.
CONTEXT_SUMMARY_TRANSCRIPT_HEADER = "[Dex] The conversation to summarize:"

#: The most tokens ``build_context_summary_prompt`` leaves free for the summary it asks for.
CONTEXT_SUMMARY_OUTPUT_TOKENS = 4096

# Bytes per estimated token. Below the 4 of English text, and 1 token per CJK character.
_ESTIMATED_BYTES_PER_TOKEN = 3


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
class CompactionPolicy:
    """Decide when a session summarizes its earlier messages, and how much it keeps.

    Pass it as ``AgentSessionOptions.compaction``, or as
    ``AgentDefinition.compaction`` for an agent on the built-in loop. The runner must
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
        reserved_input_tokens: Tokens of every model input that the runner's
            estimate cannot see, such as a system prompt inside a compiled
            LangGraph graph, a dynamic instruction, or tools listed at run
            time. The session adds them to the estimate. Defaults to 0.

            .. versionadded:: 0.3.0

    Raises:
        ValueError: If ``context_window_tokens`` is not a positive integer,
            ``reserved_input_tokens`` is negative or not below the window, or
            unless ``0 < retained_ratio < trigger_ratio <= 1``.
    """

    context_window_tokens: int
    trigger_ratio: float = 0.85
    retained_ratio: float = 0.3
    reserved_input_tokens: int = 0

    def __post_init__(self) -> None:
        if (
            not isinstance(self.context_window_tokens, int)
            or isinstance(self.context_window_tokens, bool)
            or self.context_window_tokens <= 0
        ):
            raise ValueError("context_window_tokens must be a positive integer")
        if not 0 < self.retained_ratio < self.trigger_ratio <= 1:
            raise ValueError("compaction needs 0 < retained_ratio < trigger_ratio <= 1")
        if (
            not isinstance(self.reserved_input_tokens, int)
            or isinstance(self.reserved_input_tokens, bool)
            or not 0 <= self.reserved_input_tokens < self.context_window_tokens
        ):
            raise ValueError(
                "reserved_input_tokens must be an integer from 0 to below context_window_tokens"
            )

    def is_triggered_by(self, estimated_input_tokens: int) -> bool:
        """Report whether a model input of this size makes the session compact first.

        Args:
            estimated_input_tokens: The runner's estimate of the next model input.

        Returns:
            ``True`` when the estimate plus ``reserved_input_tokens`` is at
            least ``trigger_ratio`` of the window.
        """
        return (
            estimated_input_tokens + self.reserved_input_tokens
            >= self.trigger_ratio * self.context_window_tokens
        )

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
        session_settings: The settings the running turn started with, as
            ``AgentTurnContext.session_settings`` has them. Use them to choose
            the model that writes the summary. Empty when the session has none.
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
    session_settings: Mapping[str, Any] = field(default_factory=dict)


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


def estimate_text_tokens(text: str) -> int:
    """Estimate the tokens of a text without a tokenizer, rounding up.

    Use it where a framework assembles the prompt and offers no token counter.
    It counts one token per three UTF-8 bytes. That is more than a provider
    counts for English text, and about one token per CJK character, so the
    estimate errs toward compacting early.

    Example:
        >>> estimate_text_tokens("twelve bytes")
        4

    .. versionadded:: 0.3.0

    Args:
        text: The text to measure.

    Returns:
        The estimated tokens; 0 for an empty text.
    """
    return -(-len(text.encode()) // _ESTIMATED_BYTES_PER_TOKEN)


def estimate_stored_input_tokens(turn: AgentTurnContext, *, fixed_input_tokens: int = 0) -> int:
    """Estimate a model input that a framework builds from the stored items.

    Call it from ``estimate_input_tokens`` of a framework adapter. It measures
    each ``framework_item`` as stored, so JSON syntax and opaque provider items
    count too, plus the summary and any ``framework_run_state``.

    Example:
        >>> def estimate_input_tokens(self, turn):
        ...     return estimate_stored_input_tokens(
        ...         turn, fixed_input_tokens=estimate_text_tokens(self._instructions)
        ...     )

    .. versionadded:: 0.3.0

    Args:
        turn: The context of the next model call.
        fixed_input_tokens: Tokens of what every request repeats, such as
            instructions and tool definitions. Measure what the framework
            shows; ``CompactionPolicy.reserved_input_tokens`` covers the rest.

    Returns:
        The estimated input tokens.
    """
    tokens = fixed_input_tokens + sum(
        estimate_text_tokens(message.framework_item) for message in turn.messages
    )
    if turn.context_summary is not None:
        tokens += estimate_text_tokens(build_context_summary_note(turn.context_summary))
    if turn.framework_run_state is not None:
        tokens += estimate_text_tokens(turn.framework_run_state)
    return tokens


def select_turn_boundary_cutoff(turn: AgentTurnContext, retained_token_budget: int) -> int | None:
    """Choose the last message to summarize, cutting only between turns.

    Call it from ``select_compaction_cutoff`` of a framework adapter. A turn's
    messages are stored together, so a cut between turns never separates a
    tool call from its result and never splits a model response, whatever
    framework items the turn holds. The running turn is always kept, because
    the adapter reads its opening user message from ``turn.messages``.

    It returns ``None`` while ``turn.framework_run_state`` is set. A resumed
    model call continues from that value, which may already hold the older
    messages, so the summary could not replace them. Compaction waits for the
    next turn.

    Example:
        >>> def select_compaction_cutoff(self, turn, retained_token_budget):
        ...     return select_turn_boundary_cutoff(turn, retained_token_budget)

    .. versionadded:: 0.3.0

    Args:
        turn: The context of the model call that crossed the threshold.
        retained_token_budget: Tokens that the kept messages may use.

    Returns:
        The sequence of the last message to summarize: the earliest turn
        boundary whose kept messages fit the budget, else the boundary before
        the running turn. ``None`` when the turn is resuming, every message
        already fits, or no earlier turn is left to summarize.
    """
    if turn.framework_run_state is not None:
        return None
    messages = list(turn.messages)
    running_turn_index = next(
        (index for index, message in enumerate(messages) if message.turn_id == turn.turn_id),
        len(messages),
    )
    cuts = [
        index
        for index in range(1, min(running_turn_index + 1, len(messages)))
        if messages[index].turn_id != messages[index - 1].turn_id
    ]
    if not cuts:
        return None
    kept_tokens = [0] * (len(messages) + 1)
    for index in range(len(messages) - 1, -1, -1):
        kept_tokens[index] = kept_tokens[index + 1] + estimate_text_tokens(
            messages[index].framework_item
        )
    if kept_tokens[0] <= retained_token_budget:
        return None
    fitting_cuts = [cut for cut in cuts if kept_tokens[cut] <= retained_token_budget]
    cut = fitting_cuts[0] if fitting_cuts else cuts[-1]
    return messages[cut - 1].sequence


def build_context_summary_prompt(
    request: ContextSummaryRequest,
    *,
    describe_message: Callable[[AgentMessage], str | None] | None = None,
) -> str:
    """Build the one user prompt of a summary call for a framework adapter.

    Call it from ``summarize_context`` and send the result with
    ``CONTEXT_SUMMARY_INSTRUCTIONS`` and no tools. The prompt holds the previous
    summary, the messages as plain text, and ``CONTEXT_SUMMARY_REQUEST``. Plain
    text works with any framework: it needs no tool definitions, and no
    provider sees an item out of its place.

    The prompt fits ``request.input_token_budget``, less room for the summary,
    as ``estimate_text_tokens`` counts. When the range is too large, it leaves
    out the oldest messages and says so with
    ``build_context_summary_omission_note``. It shortens a single message only
    when that message alone is too large.

    Example:
        >>> async def summarize_context(self, request):
        ...     prompt = build_context_summary_prompt(request)
        ...     response = await model.complete(CONTEXT_SUMMARY_INSTRUCTIONS, prompt)
        ...     return ContextSummaryResult(text=response.text, usage=response.usage)

    .. versionadded:: 0.3.0

    Args:
        request: The previous summary and the messages to summarize.
        describe_message: Returns a message's text for the prompt, or ``None``
            to leave it out, such as an encrypted reasoning item. ``None``, the
            default, uses ``describe_stored_message``.

    Returns:
        The prompt text.
    """
    describe = describe_message or describe_stored_message
    opening = (
        []
        if request.previous_summary is None
        else [build_context_summary_note(request.previous_summary)]
    )
    closing = [CONTEXT_SUMMARY_TRANSCRIPT_HEADER, CONTEXT_SUMMARY_REQUEST]
    descriptions = [
        description
        for description in (describe(message) for message in request.messages)
        if description is not None
    ]
    remaining_tokens: int | None = None
    if request.input_token_budget is not None:
        fixed_tokens = estimate_text_tokens(
            "\n\n".join([CONTEXT_SUMMARY_INSTRUCTIONS, *opening, *closing])
        )
        room_for_transcript = request.input_token_budget - fixed_tokens
        remaining_tokens = room_for_transcript - min(
            CONTEXT_SUMMARY_OUTPUT_TOKENS, max(room_for_transcript, 0) // 4
        )
    kept: list[str] = []
    for description in reversed(descriptions):
        if remaining_tokens is None:
            kept.append(description)
            continue
        # Two newlines separate messages; they count toward the estimate.
        tokens = estimate_text_tokens(description + "\n\n")
        if tokens <= remaining_tokens:
            kept.append(description)
            remaining_tokens -= tokens
            continue
        if not kept and remaining_tokens > 0:
            kept.append(_shorten_to_tokens(description, remaining_tokens))
        break
    kept.reverse()
    omitted_count = len(descriptions) - len(kept)
    if omitted_count:
        opening.append(build_context_summary_omission_note(omitted_count))
    return "\n\n".join(
        [*opening, CONTEXT_SUMMARY_TRANSCRIPT_HEADER, *kept, CONTEXT_SUMMARY_REQUEST]
    )


def describe_stored_message(message: AgentMessage) -> str:
    """Return a stored message as the plain text a summary prompt shows.

    Wrap it in the ``describe_message`` of ``build_context_summary_prompt`` to
    leave out items that only a provider can read.

    .. versionadded:: 0.3.0

    Args:
        message: The stored message.

    Returns:
        Its role and text, tool arguments, and tool output. A message with none
        of them returns its role and ``framework_item``, such as a model
        response with several tool calls.
    """
    lines: list[str] = []
    if message.text:
        lines.append(f"{message.role.value}: {message.text}")
    if message.tool_arguments is not None:
        lines.append(f"tool call {message.tool_name}: {message.tool_arguments}")
    if message.tool_output is not None:
        lines.append(f"tool result {message.tool_name}: {message.tool_output}")
    return "\n".join(lines) or f"{message.role.value}: {message.framework_item}"


def _shorten_to_tokens(text: str, tokens: int) -> str:
    marker = "[Dex] The rest of this message is left out."
    kept_bytes = max(tokens - estimate_text_tokens(marker) - 1, 0) * _ESTIMATED_BYTES_PER_TOKEN
    return text.encode()[:kept_bytes].decode(errors="ignore") + "\n" + marker
