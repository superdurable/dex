# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The built-in agent loop: run an agent without an agent framework.

Dex owns the conversation and builds every model input from durable state, so
the agent does not depend on a model provider's conversation API.

.. versionadded:: 0.5.0
   This module replaces ``dex_agents.stepwise``, which stays as a deprecated
   alias with the old names.

Declare an ``AgentDefinition`` with instructions, a ``ModelClient``, function
tools, and tool sources such as ``McpToolSource``, then build its Flow with
``builtin_agent_session_flow``. ``ScriptedModelClient`` answers model calls
from a script, so an agent runs without a model key. The
runner makes one model call per ``RunModel`` Step, and the session Flow, its
Steps, approvals, questions, durable waits, and events are the ones every
framework adapter uses.

Example:
    >>> from dex_agents import BuiltInTool, ToolEffect
    >>> from dex_agents.litellm import LiteLLMModelClient
    >>> from dex_agents.builtin_loop import AgentDefinition, function_tool
    >>> from dex_agents.builtin_loop import builtin_agent_session_flow
    >>>
    >>> @function_tool(effect=ToolEffect.READ)
    ... async def lookup_order(order_id: str) -> dict[str, str]:
    ...     \"\"\"Look up an order by its ID.\"\"\"
    ...     return await orders.get(order_id)
    >>>
    >>> support_agent = AgentDefinition(
    ...     name="support",
    ...     instructions="You help customers with their orders.",
    ...     model=LiteLLMModelClient("openai/gpt-5-mini"),
    ...     tools=[lookup_order],
    ...     built_in_tools=frozenset({BuiltInTool.ASK_USER, BuiltInTool.DURABLE_WAIT}),
    ... )
    >>> flow = builtin_agent_session_flow(support_agent, flow_type="SupportAgent")
"""

from dex_agents.builtin_loop.framework_tool_sources import (
    FRAMEWORK_TOOL_SOURCE_ENVELOPE_KEY,
    SOURCE_TOOL_NOT_OFFERED_RESULT,
    FrameworkToolSourceRouter,
    SourceToolListing,
)
from dex_agents.builtin_loop.function_tools import (
    FunctionTool,
    FunctionToolResult,
    ToolCallContext,
    function_tool,
)
from dex_agents.builtin_loop.model import (
    ModelClient,
    ModelInputItem,
    ModelOutputItem,
    ModelOutputWriter,
    ModelRequest,
    ModelRequestMetadata,
    ModelResponse,
    ModelSettings,
    ModelToolCall,
    ModelToolDefinition,
    NamedToolChoice,
    ToolChoice,
    ToolResultItem,
    UserTextItem,
)
from dex_agents.builtin_loop.runner import (
    DEFAULT_MAX_MODEL_CALLS_PER_TURN,
    DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
    MAX_CONSECUTIVE_TURN_CONTINUATIONS,
    TOOL_CALL_LIMIT_REACHED_RESULT,
    TURN_CONTINUATION_LIMIT_ERROR,
    AgentDefinition,
    BuiltinLoopAgentRunner,
    ContinueTurn,
    InstructionContext,
    PlanningToolChoice,
    TurnContinuation,
    TurnContinuationContext,
    builtin_agent_session_flow,
    builtin_agent_session_options,
)
from dex_agents.builtin_loop.scripted_model import (
    DEFAULT_MAX_RECORDED_REQUESTS,
    SCRIPTED_MODEL_API,
    SCRIPTED_MODEL_PROVIDER,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedModelResponder,
    ScriptedToolCall,
)
from dex_agents.builtin_loop.tool_sources import SourceToolDefinition, ToolSource
from dex_agents.builtin_loop.transcript import (
    MODEL_OUTPUT_ITEM_TYPE,
    TOOL_RESULT_ITEM_TYPE,
    USER_TEXT_ITEM_TYPE,
    decode_builtin_loop_message,
    encode_builtin_loop_message,
    pair_tool_calls_with_results,
)
from dex_agents.compaction import (
    CONTEXT_SUMMARY_INSTRUCTIONS,
    CONTEXT_SUMMARY_NOTE_HEADER,
    CONTEXT_SUMMARY_REQUEST,
    build_context_summary_note,
    build_context_summary_omission_note,
)

__all__ = [
    "CONTEXT_SUMMARY_INSTRUCTIONS",
    "CONTEXT_SUMMARY_NOTE_HEADER",
    "CONTEXT_SUMMARY_REQUEST",
    "DEFAULT_MAX_MODEL_CALLS_PER_TURN",
    "DEFAULT_MAX_RECORDED_REQUESTS",
    "DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT",
    "FRAMEWORK_TOOL_SOURCE_ENVELOPE_KEY",
    "MAX_CONSECUTIVE_TURN_CONTINUATIONS",
    "MODEL_OUTPUT_ITEM_TYPE",
    "SCRIPTED_MODEL_API",
    "SCRIPTED_MODEL_PROVIDER",
    "SOURCE_TOOL_NOT_OFFERED_RESULT",
    "TOOL_CALL_LIMIT_REACHED_RESULT",
    "TOOL_RESULT_ITEM_TYPE",
    "TURN_CONTINUATION_LIMIT_ERROR",
    "USER_TEXT_ITEM_TYPE",
    "AgentDefinition",
    "BuiltinLoopAgentRunner",
    "ContinueTurn",
    "FrameworkToolSourceRouter",
    "FunctionTool",
    "FunctionToolResult",
    "InstructionContext",
    "ModelClient",
    "ModelInputItem",
    "ModelOutputItem",
    "ModelOutputWriter",
    "ModelRequest",
    "ModelRequestMetadata",
    "ModelResponse",
    "ModelSettings",
    "ModelToolCall",
    "ModelToolDefinition",
    "NamedToolChoice",
    "PlanningToolChoice",
    "ScriptedModelClient",
    "ScriptedModelReply",
    "ScriptedModelResponder",
    "ScriptedToolCall",
    "SourceToolDefinition",
    "SourceToolListing",
    "ToolCallContext",
    "ToolChoice",
    "ToolResultItem",
    "ToolSource",
    "TurnContinuation",
    "TurnContinuationContext",
    "UserTextItem",
    "build_context_summary_note",
    "build_context_summary_omission_note",
    "builtin_agent_session_flow",
    "builtin_agent_session_options",
    "decode_builtin_loop_message",
    "encode_builtin_loop_message",
    "function_tool",
    "pair_tool_calls_with_results",
]
