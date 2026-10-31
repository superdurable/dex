# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Durable agent sessions for Dex.

``AgentSessionFlow`` runs one agent session per Flow execution. A framework
adapter implements ``AgentFrameworkRunner`` and passes it to the Flow; the
Flow runs every model call and every tool call as its own Step, and owns the
conversation, tool approvals, and live events. See the package README for the full contract.
"""

from dex_agents.client import AgentSessionClient
from dex_agents.events import AgentEventType, AgentEventWriter
from dex_agents.persistence import (
    AGENT_MESSAGES,
    AGENT_STATE,
    AGENT_TOOL_CALLS,
    AGENT_USAGE,
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    FRAMEWORK_RUN_STATE,
    INTERRUPT_RESOLUTIONS,
    PENDING_INTERRUPTS,
    QUEUED_USER_MESSAGES,
    TOOL_CALL_RESULTS,
    message_sequence_key,
)
from dex_agents.runner import (
    APPROVAL_DEADLINE_PASSED,
    DEFAULT_REJECTION_TOOL_RESULT,
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TURN_FAILURE_NOTE_FOOTER,
    TURN_FAILURE_NOTE_HEADER,
    AgentFrameworkRunner,
    AgentTurnContext,
    AgentTurnOutcome,
    DeferredToolCall,
    DeferredToolExecution,
    FrameworkMessage,
    ToolCallsRequested,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
    build_turn_failure_note,
    describe_tool_call_outcome,
    resolve_tool_policy,
)
from dex_agents.session_flow import AgentSessionFlow, AgentSessionOptions
from dex_agents.values import (
    AgentEvent,
    AgentInterrupt,
    AgentInterruptResolution,
    AgentMessage,
    AgentMessagesPage,
    AgentSessionStartInput,
    AgentSessionStatus,
    AgentSnapshot,
    AgentState,
    AgentToolCall,
    AgentUsage,
    AgentUserMessage,
    InterruptKind,
    ListAgentMessagesRequest,
    MessageRole,
    ResolveInterruptRequest,
    ToolCallCompleted,
    ToolCallStatus,
    ToolEffect,
)

__all__ = [
    "AGENT_MESSAGES",
    "AGENT_STATE",
    "AGENT_TOOL_CALLS",
    "AGENT_USAGE",
    "APPROVAL_DEADLINE_PASSED",
    "DEFAULT_AGENT_EVENTS_CAPACITY_BYTES",
    "DEFAULT_REJECTION_TOOL_RESULT",
    "FRAMEWORK_RUN_STATE",
    "INTERRUPT_RESOLUTIONS",
    "OUTCOME_UNKNOWN_TOOL_RESULT",
    "PENDING_INTERRUPTS",
    "QUEUED_USER_MESSAGES",
    "TOOL_CALL_RESULTS",
    "TURN_FAILURE_NOTE_FOOTER",
    "TURN_FAILURE_NOTE_HEADER",
    "AgentEvent",
    "AgentEventType",
    "AgentEventWriter",
    "AgentFrameworkRunner",
    "AgentInterrupt",
    "AgentInterruptResolution",
    "AgentMessage",
    "AgentMessagesPage",
    "AgentSessionClient",
    "AgentSessionFlow",
    "AgentSessionOptions",
    "AgentSessionStartInput",
    "AgentSessionStatus",
    "AgentSnapshot",
    "AgentState",
    "AgentToolCall",
    "AgentTurnContext",
    "AgentTurnOutcome",
    "AgentUsage",
    "AgentUserMessage",
    "DeferredToolCall",
    "DeferredToolExecution",
    "FrameworkMessage",
    "InterruptKind",
    "ListAgentMessagesRequest",
    "MessageRole",
    "ResolveInterruptRequest",
    "ToolCallCompleted",
    "ToolCallStatus",
    "ToolCallsRequested",
    "ToolEffect",
    "ToolExecutionError",
    "ToolExecutionResult",
    "ToolPolicy",
    "TurnCompleted",
    "build_turn_failure_note",
    "describe_tool_call_outcome",
    "message_sequence_key",
    "resolve_tool_policy",
]
