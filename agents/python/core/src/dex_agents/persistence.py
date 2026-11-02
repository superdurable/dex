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

from dex import Attribute, AttributeMap, Channel, ChannelMap

from dex_agents.values import (
    AgentInterrupt,
    AgentInterruptResolution,
    AgentMessage,
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
)

#: Current session position. Unset until ``StartAgentSession`` commits.
AGENT_STATE: Attribute[AgentState] = Attribute("AgentState", AgentState)

#: Conversation messages keyed by ``message_sequence_key(sequence)``.
AGENT_MESSAGES: AttributeMap[AgentMessage] = AttributeMap("AgentMessages", AgentMessage)

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

#: Internal wake-ups that ``steer_user_message`` and ``cancel_turn`` publish.
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

#: Default ``AgentEvents`` Stream capacity: 10 MiB.
DEFAULT_AGENT_EVENTS_CAPACITY_BYTES = 10 * 1024 * 1024


def message_sequence_key(sequence: int) -> str:
    """Return the ``AgentMessages`` map instance for a message sequence.

    Use it to read one stored message with ``AGENT_MESSAGES.instance(...)``.

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
