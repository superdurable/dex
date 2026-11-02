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
conversation, tool approvals, questions, durable waits, and live events. See
the package README for the full contract.

``dex_agents.stepwise`` adds a built-in agent loop for agents defined in Dex
terms, and ``dex_agents.litellm`` (extra ``dex-agents[litellm]``) a model
client on LiteLLM.

``AgentTracingOptions`` configures its optional OpenTelemetry GenAI spans.
"""

from dex_agents.built_in_tools import (
    BUILT_IN_TOOL_DEFINITIONS,
    DEFAULT_MAX_DURABLE_WAIT,
    QUESTION_DEADLINE_PASSED,
    QUESTION_DECLINED_TOOL_RESULT,
    BuiltInTool,
    BuiltInToolDefinition,
)
from dex_agents.client import AgentSessionClient
from dex_agents.events import AgentEventType, AgentEventWriter
from dex_agents.persistence import (
    AGENT_MESSAGES,
    AGENT_STATE,
    AGENT_TOOL_CALLS,
    AGENT_TRACE_CONTEXT,
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
    DeferredToolOutcome,
    FrameworkMessage,
    ModelCallMetadata,
    ToolCallsRequested,
    ToolExecutionError,
    ToolExecutionResult,
    ToolInputRequested,
    ToolPolicy,
    TurnCompleted,
    UnfinishedTurnMessageBuilder,
    build_turn_failure_note,
    describe_tool_call_outcome,
    resolve_tool_policy,
)
from dex_agents.session_flow import AgentSessionFlow, AgentSessionOptions
from dex_agents.tracing import AgentTracingOptions, GenAISemanticConventionsVersion
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
    AnswerQuestionRequest,
    InterruptKind,
    ListAgentMessagesRequest,
    MessageRole,
    QuestionAnswer,
    ResolveInterruptRequest,
    ToolCallCompleted,
    ToolCallStatus,
    ToolEffect,
)

__all__ = [
    "AGENT_MESSAGES",
    "AGENT_STATE",
    "AGENT_TOOL_CALLS",
    "AGENT_TRACE_CONTEXT",
    "AGENT_USAGE",
    "APPROVAL_DEADLINE_PASSED",
    "BUILT_IN_TOOL_DEFINITIONS",
    "DEFAULT_AGENT_EVENTS_CAPACITY_BYTES",
    "DEFAULT_MAX_DURABLE_WAIT",
    "DEFAULT_REJECTION_TOOL_RESULT",
    "FRAMEWORK_RUN_STATE",
    "INTERRUPT_RESOLUTIONS",
    "OUTCOME_UNKNOWN_TOOL_RESULT",
    "PENDING_INTERRUPTS",
    "QUESTION_DEADLINE_PASSED",
    "QUESTION_DECLINED_TOOL_RESULT",
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
    "AgentTracingOptions",
    "AgentTurnContext",
    "AgentTurnOutcome",
    "AgentUsage",
    "AgentUserMessage",
    "AnswerQuestionRequest",
    "BuiltInTool",
    "BuiltInToolDefinition",
    "DeferredToolCall",
    "DeferredToolExecution",
    "DeferredToolOutcome",
    "FrameworkMessage",
    "GenAISemanticConventionsVersion",
    "InterruptKind",
    "ListAgentMessagesRequest",
    "MessageRole",
    "ModelCallMetadata",
    "QuestionAnswer",
    "ResolveInterruptRequest",
    "ToolCallCompleted",
    "ToolCallStatus",
    "ToolCallsRequested",
    "ToolEffect",
    "ToolExecutionError",
    "ToolExecutionResult",
    "ToolInputRequested",
    "ToolPolicy",
    "TurnCompleted",
    "UnfinishedTurnMessageBuilder",
    "build_turn_failure_note",
    "describe_tool_call_outcome",
    "message_sequence_key",
    "resolve_tool_policy",
]
