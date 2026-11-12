# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Persistence definitions shared by every agent session Flow type.

Every ``AgentSessionFlow`` registers these exact definition objects, so a
Client can read any agent session with ``AsyncClient.get_attributes``. The
``AgentEvents`` Stream is not here: each Flow type owns its own Stream object,
available as ``AgentSessionFlow.agent_events``.

Example:
    >>> values = await client.get_attributes("session-42", AGENT_STATE, AGENT_USAGE)
    >>> state = values.get(AGENT_STATE)
"""

from __future__ import annotations

from typing import Any

from dex import Attribute, AttributeIndex, AttributeMap, Channel, ChannelMap, IndexType

from dex_agents.values import (
    AgentInterrupt,
    AgentInterruptResolution,
    AgentMessage,
    AgentMessageChunk,
    AgentMessageIndex,
    AgentPlan,
    AgentState,
    AgentToolCall,
    AgentUsage,
    AgentUserMessage,
    ContextSummary,
    PlanExecutionRequest,
    ToolCallCancellation,
    ToolCallCompleted,
    TurnCancellationRequest,
    TurnControlRequest,
    TurnReplySequence,
    TurnSessionSettings,
)

#: Current session position. Unset until ``StartAgentSession`` commits.
AGENT_STATE: Attribute[AgentState] = Attribute("AgentState", AgentState)

#: The newest conversation messages, the window, keyed by ``message_sequence_key(sequence)``.
#: ``AGENT_MESSAGE_INDEX`` says where the window starts; older messages are in chunks.
AGENT_MESSAGES: AttributeMap[AgentMessage] = AttributeMap("AgentMessages", AgentMessage)

#: Immutable archive chunks of older messages, keyed by ``message_sequence_key(first sequence)``.
AGENT_MESSAGE_CHUNKS: AttributeMap[AgentMessageChunk] = AttributeMap(
    "AgentMessageChunks", AgentMessageChunk
)

#: Where the stored messages are: the window start, the chunk size, and the oldest kept message.
AGENT_MESSAGE_INDEX: Attribute[AgentMessageIndex] = Attribute(
    "AgentMessageIndex", AgentMessageIndex
)

#: Where the latest turn's replies start; the Step that starts a turn writes it.
TURN_REPLY_SEQUENCE: Attribute[TurnReplySequence] = Attribute(
    "TurnReplySequence", TurnReplySequence
)

#: Opaque framework run state JSON text, set only while a turn is suspended.
FRAMEWORK_RUN_STATE: Attribute[str] = Attribute("FrameworkRunState", str)

#: Deferred tool call records of the active turn, keyed by tool call ID.
AGENT_TOOL_CALLS: AttributeMap[AgentToolCall] = AttributeMap("AgentToolCalls", AgentToolCall)

#: Interrupts that ``resolve_interrupt`` can still answer.
PENDING_INTERRUPTS: Attribute[list[AgentInterrupt]] = Attribute(
    "PendingInterrupts", list[AgentInterrupt]
)

#: Model usage of the whole session.
AGENT_USAGE: Attribute[AgentUsage] = Attribute("AgentUsage", AgentUsage)

#: User messages waiting for the session to start their turns.
QUEUED_USER_MESSAGES: Channel[AgentUserMessage] = Channel("QueuedUserMessages", AgentUserMessage)

#: Interrupt answers keyed by interrupt ID.
INTERRUPT_RESOLUTIONS: ChannelMap[AgentInterruptResolution] = ChannelMap(
    "InterruptResolutions", AgentInterruptResolution
)

#: Internal results of tool call Steps; ``ResumeTurn`` waits for one per tool call.
TOOL_CALL_RESULTS: Channel[ToolCallCompleted] = Channel("ToolCallResults", ToolCallCompleted)

#: Steered user messages that ``ApplyTurnControl`` has not applied yet.
STEERED_USER_MESSAGES: Channel[AgentUserMessage] = Channel("SteeredUserMessages", AgentUserMessage)

#: Internal wake-ups that ``steer_user_message`` publishes.
TURN_CONTROL_REQUESTS: Channel[TurnControlRequest] = Channel(
    "TurnControlRequests", TurnControlRequest
)

#: The latest ``cancel_turn`` request. Only ``cancel_turn`` writes it.
TURN_CANCELLATION_REQUEST: Attribute[TurnCancellationRequest] = Attribute(
    "TurnCancellationRequest", TurnCancellationRequest
)

#: Internal wake-ups of the Steps that wait on a tool call, keyed by tool call ID.
TOOL_CALL_CANCELLATIONS: ChannelMap[ToolCallCancellation] = ChannelMap(
    "ToolCallCancellations", ToolCallCancellation
)

#: W3C ``traceparent`` of the current activation's root span when valid, sampled or not.
AGENT_TRACE_CONTEXT: Attribute[str] = Attribute("AgentTraceContext", str)

#: The session's plan. Unset until ``write_todos`` first succeeds.
AGENT_PLAN: Attribute[AgentPlan] = Attribute("AgentPlan", AgentPlan)

#: Approved plan executions keyed by ``plan_execution_key(revision)``.
PLAN_EXECUTIONS: ChannelMap[PlanExecutionRequest] = ChannelMap(
    "PlanExecutions", PlanExecutionRequest
)

#: The execution ``execute_plan`` accepted and ``AwaitUserMessage`` has not consumed yet.
PENDING_PLAN_EXECUTION: Attribute[PlanExecutionRequest] = Attribute(
    "PendingPlanExecution", PlanExecutionRequest
)
#: Summary of the earlier messages that compaction left out of the model input; only
#: ``CompactContext`` writes it.
CONTEXT_SUMMARY: Attribute[ContextSummary] = Attribute("ContextSummary", ContextSummary)

#: The settings the next turn starts with: the start input's ``session_settings``, then
#: each ``update_session_settings``. Unset when the session has none.
SESSION_SETTINGS: Attribute[dict[str, Any]] = Attribute("SessionSettings", dict[str, Any])

#: The settings a turn started with, tagged with its turn, copied from ``SessionSettings``
#: when the turn began. A copy from an earlier turn reads as no settings.
TURN_SESSION_SETTINGS: Attribute[TurnSessionSettings] = Attribute(
    "TurnSessionSettings", TurnSessionSettings
)

#: The session's title: its first user message with each run of spaces, tabs, and line
#: breaks made one space, cut to 80 code points. Set by the session's first turn only, so
#: a session that an earlier release started never has one. Indexed as FULL_TEXT under
#: ``text1``. See ``list_agent_sessions``.
#:
#: .. versionadded:: 0.6.0
AGENT_SESSION_TITLE: Attribute[str] = Attribute(
    "AgentSessionTitle", str, AttributeIndex(IndexType.FULL_TEXT, "text1")
)

#: When the latest turn started or ended, as RFC 3339 UTC text with milliseconds, such as
#: ``2026-11-02T09:30:00.000Z``. It is the time Dex scheduled the Step that started or ended
#: the turn, at one-second precision. Indexed as DATETIME under ``datetime1``.
#:
#: .. versionadded:: 0.6.0
AGENT_SESSION_LAST_ACTIVITY_AT: Attribute[str] = Attribute(
    "AgentSessionLastActivityAt", str, AttributeIndex(IndexType.DATETIME, "datetime1")
)

#: ``AgentState.turn_count`` as of the latest turn start or end: the turns that completed,
#: failed, or ended by steering or cancellation. Indexed as INT under ``int1``.
#:
#: .. versionadded:: 0.6.0
AGENT_SESSION_TURN_COUNT: Attribute[int] = Attribute(
    "AgentSessionTurnCount", int, AttributeIndex(IndexType.INT, "int1")
)

#: Default ``AgentEvents`` Stream capacity: 10 MiB.
DEFAULT_AGENT_EVENTS_CAPACITY_BYTES = 10 * 1024 * 1024

#: Default ``AgentSessionOptions.message_window_size``.
DEFAULT_MESSAGE_WINDOW_SIZE = 64

#: Default ``AgentSessionOptions.message_chunk_size``.
DEFAULT_MESSAGE_CHUNK_SIZE = 32

#: Default ``AgentSessionOptions.retained_message_limit``.
DEFAULT_RETAINED_MESSAGE_LIMIT = 1000

#: Default ``AgentSessionOptions.message_chunk_max_bytes``: 1 MiB.
DEFAULT_MESSAGE_CHUNK_MAX_BYTES = 1024 * 1024

#: Most chunks one Step moves out of the message window, so one commit stays small.
MESSAGE_ROLLOVER_MAX_CHUNKS_PER_COMMIT = 4

#: Most chunk bytes one Step moves out of the window, unless its first chunk alone is larger.
MESSAGE_ROLLOVER_MAX_BYTES_PER_COMMIT = 8 * 1024 * 1024


def message_sequence_key(sequence: int) -> str:
    """Return the instance key of a message sequence.

    A message in the window is the ``AGENT_MESSAGES`` instance with this key.
    An archive chunk is the ``AGENT_MESSAGE_CHUNKS`` instance with the key of
    its first sequence. ``AGENT_MESSAGE_INDEX`` says which holds a message.

    Args:
        sequence: Positive message sequence.

    Returns:
        The sequence as a 12-digit zero-padded decimal, such as ``"000000000007"``.

    Raises:
        ValueError: If ``sequence`` is not positive.
    """
    if sequence <= 0:
        raise ValueError("message sequence must be positive")
    return f"{sequence:012d}"


def plan_execution_key(revision: int) -> str:
    """Return the ``PlanExecutions`` map instance for a plan revision.

    Args:
        revision: Positive ``AgentPlan.revision``.

    Returns:
        The revision as a decimal, such as ``"3"``.

    Raises:
        ValueError: If ``revision`` is not positive.
    """
    if revision <= 0:
        raise ValueError("plan revision must be positive")
    return str(revision)
