# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deprecated alias of ``dex_agents.builtin_loop.runner``.

.. deprecated:: 0.5.0
   Import from ``dex_agents.builtin_loop`` instead.
"""

from typing import TYPE_CHECKING, Any

import dex_agents.builtin_loop.runner as _replacement_module
from dex_agents.stepwise._deprecated_alias import forward_deprecated_attribute

if TYPE_CHECKING:
    from dex_agents.built_in_tools import BUILT_IN_TOOL_DEFINITIONS, BuiltInTool
    from dex_agents.builtin_loop.function_tools import FunctionTool, ToolCallContext
    from dex_agents.builtin_loop.model import (
        ModelClient,
        ModelInputItem,
        ModelOutputItem,
        ModelRequest,
        ModelRequestMetadata,
        ModelSettings,
        ModelToolDefinition,
        NamedToolChoice,
        ToolChoice,
        ToolResultItem,
        UserTextItem,
    )
    from dex_agents.builtin_loop.runner import (
        DEFAULT_MAX_MODEL_CALLS_PER_TURN,
        DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
        TOOL_CALL_LIMIT_REACHED_RESULT,
        AgentDefinition,
        InstructionContext,
        PlanningToolChoice,
        build_unknown_tool_error,
    )
    from dex_agents.builtin_loop.runner import BuiltinLoopAgentRunner as StepwiseAgentRunner
    from dex_agents.builtin_loop.runner import (
        builtin_agent_session_flow as stepwise_agent_session_flow,
    )
    from dex_agents.builtin_loop.runner import (
        builtin_agent_session_options as stepwise_agent_session_options,
    )
    from dex_agents.builtin_loop.tool_sources import SourceToolDefinition, ToolSource
    from dex_agents.builtin_loop.transcript import (
        decode_builtin_loop_message as decode_stepwise_message,
    )
    from dex_agents.builtin_loop.transcript import (
        encode_builtin_loop_message as encode_stepwise_message,
    )
    from dex_agents.builtin_loop.transcript import pair_tool_calls_with_results
    from dex_agents.compaction import (
        CONTEXT_SUMMARY_INSTRUCTIONS,
        CONTEXT_SUMMARY_REQUEST,
        CompactionPolicy,
        ContextSummaryRequest,
        ContextSummaryResult,
        build_context_summary_note,
        build_context_summary_omission_note,
    )
    from dex_agents.persistence import DEFAULT_AGENT_EVENTS_CAPACITY_BYTES
    from dex_agents.plans import build_plan_note
    from dex_agents.runner import (
        AgentTurnContext,
        AgentTurnOutcome,
        DeferredToolCall,
        DeferredToolExecution,
        DeferredToolOutcome,
        FrameworkMessage,
        ToolCallsRequested,
        ToolCallStepOptions,
        ToolExecutionError,
        ToolExecutionResult,
        ToolPolicy,
        ToolTaskCancellation,
        ToolTaskPoll,
        ToolTaskPollOutcome,
        TurnCompleted,
        describe_tool_call_outcome,
        resolve_tool_policy,
    )
    from dex_agents.session_flow import AgentSessionFlow, AgentSessionOptions
    from dex_agents.spend_limit import ModelSpendLimit
    from dex_agents.values import (
        AgentPlan,
        AgentToolCall,
        InteractionMode,
        MessageRole,
        PlanStatus,
        ToolEffect,
    )

_RENAMED_ATTRIBUTES = {
    "StepwiseAgentRunner": "BuiltinLoopAgentRunner",
    "stepwise_agent_session_flow": "builtin_agent_session_flow",
    "stepwise_agent_session_options": "builtin_agent_session_options",
    "encode_stepwise_message": "encode_builtin_loop_message",
    "decode_stepwise_message": "decode_builtin_loop_message",
}

__all__ = [
    "BUILT_IN_TOOL_DEFINITIONS",
    "CONTEXT_SUMMARY_INSTRUCTIONS",
    "CONTEXT_SUMMARY_REQUEST",
    "DEFAULT_AGENT_EVENTS_CAPACITY_BYTES",
    "DEFAULT_MAX_MODEL_CALLS_PER_TURN",
    "DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT",
    "TOOL_CALL_LIMIT_REACHED_RESULT",
    "AgentDefinition",
    "AgentPlan",
    "AgentSessionFlow",
    "AgentSessionOptions",
    "AgentToolCall",
    "AgentTurnContext",
    "AgentTurnOutcome",
    "BuiltInTool",
    "CompactionPolicy",
    "ContextSummaryRequest",
    "ContextSummaryResult",
    "DeferredToolCall",
    "DeferredToolExecution",
    "DeferredToolOutcome",
    "FrameworkMessage",
    "FunctionTool",
    "InstructionContext",
    "InteractionMode",
    "MessageRole",
    "ModelClient",
    "ModelInputItem",
    "ModelOutputItem",
    "ModelRequest",
    "ModelRequestMetadata",
    "ModelSettings",
    "ModelSpendLimit",
    "ModelToolDefinition",
    "NamedToolChoice",
    "PlanStatus",
    "PlanningToolChoice",
    "SourceToolDefinition",
    "StepwiseAgentRunner",
    "ToolCallContext",
    "ToolCallStepOptions",
    "ToolCallsRequested",
    "ToolChoice",
    "ToolEffect",
    "ToolExecutionError",
    "ToolExecutionResult",
    "ToolPolicy",
    "ToolResultItem",
    "ToolSource",
    "ToolTaskCancellation",
    "ToolTaskPoll",
    "ToolTaskPollOutcome",
    "TurnCompleted",
    "UserTextItem",
    "build_context_summary_note",
    "build_context_summary_omission_note",
    "build_plan_note",
    "build_unknown_tool_error",
    "decode_stepwise_message",
    "describe_tool_call_outcome",
    "encode_stepwise_message",
    "pair_tool_calls_with_results",
    "resolve_tool_policy",
    "stepwise_agent_session_flow",
    "stepwise_agent_session_options",
]


def __getattr__(name: str) -> Any:
    return forward_deprecated_attribute(__name__, _replacement_module, _RENAMED_ATTRIBUTES, name)


def __dir__() -> list[str]:
    return sorted(__all__)
