# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Stepwise mode: the built-in Dex Agents loop, for agents defined without a framework.

Declare an ``AgentDefinition`` with instructions, a ``ModelClient``, and
function tools, then build its Flow with ``stepwise_agent_session_flow``. The
runner makes one model call per ``RunModel`` Step, and the session Flow, its
Steps, approvals, questions, durable waits, and events are the ones every
framework adapter uses.

Example:
    >>> from dex_agents import BuiltInTool, ToolEffect
    >>> from dex_agents.litellm import LiteLLMModelClient
    >>> from dex_agents.stepwise import AgentDefinition, function_tool
    >>> from dex_agents.stepwise import stepwise_agent_session_flow
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
    >>> flow = stepwise_agent_session_flow(support_agent, flow_type="SupportAgent")
"""

from dex_agents.stepwise.function_tools import FunctionTool, ToolCallContext, function_tool
from dex_agents.stepwise.model import (
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
from dex_agents.stepwise.runner import (
    DEFAULT_MAX_MODEL_CALLS_PER_TURN,
    TOOL_CALL_LIMIT_REACHED_RESULT,
    AgentDefinition,
    InstructionContext,
    StepwiseAgentRunner,
    stepwise_agent_session_flow,
    stepwise_agent_session_options,
)
from dex_agents.stepwise.transcript import (
    MODEL_OUTPUT_ITEM_TYPE,
    TOOL_RESULT_ITEM_TYPE,
    USER_TEXT_ITEM_TYPE,
    decode_stepwise_message,
    encode_stepwise_message,
    pair_tool_calls_with_results,
)

__all__ = [
    "DEFAULT_MAX_MODEL_CALLS_PER_TURN",
    "MODEL_OUTPUT_ITEM_TYPE",
    "TOOL_CALL_LIMIT_REACHED_RESULT",
    "TOOL_RESULT_ITEM_TYPE",
    "USER_TEXT_ITEM_TYPE",
    "AgentDefinition",
    "FunctionTool",
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
    "StepwiseAgentRunner",
    "ToolCallContext",
    "ToolChoice",
    "ToolResultItem",
    "UserTextItem",
    "decode_stepwise_message",
    "encode_stepwise_message",
    "function_tool",
    "pair_tool_calls_with_results",
    "stepwise_agent_session_flow",
    "stepwise_agent_session_options",
]
