# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deprecated alias of ``dex_agents.builtin_loop``, the built-in agent loop.

.. deprecated:: 0.5.0
   Import from ``dex_agents.builtin_loop`` instead. Every name this module
   exported still works and refers to the same object, and each access emits a
   ``DeprecationWarning``. Five names changed:

   * ``StepwiseAgentRunner`` is ``BuiltinLoopAgentRunner``.
   * ``stepwise_agent_session_flow`` is ``builtin_agent_session_flow``.
   * ``stepwise_agent_session_options`` is ``builtin_agent_session_options``.
   * ``encode_stepwise_message`` is ``encode_builtin_loop_message``.
   * ``decode_stepwise_message`` is ``decode_builtin_loop_message``.

   The submodules, such as ``dex_agents.stepwise.runner``, forward to the
   matching ``dex_agents.builtin_loop`` submodules in the same way.

Example:
    >>> # Before
    >>> from dex_agents.stepwise import AgentDefinition, stepwise_agent_session_flow
    >>> # After
    >>> from dex_agents.builtin_loop import AgentDefinition, builtin_agent_session_flow
"""

import importlib
from typing import TYPE_CHECKING, Any

import dex_agents.builtin_loop as _replacement_module
from dex_agents.stepwise._deprecated_alias import forward_deprecated_attribute

if TYPE_CHECKING:
    from dex_agents.builtin_loop import (
        CONTEXT_SUMMARY_INSTRUCTIONS,
        CONTEXT_SUMMARY_NOTE_HEADER,
        CONTEXT_SUMMARY_REQUEST,
        DEFAULT_MAX_MODEL_CALLS_PER_TURN,
        DEFAULT_MAX_RECORDED_REQUESTS,
        DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
        FRAMEWORK_TOOL_SOURCE_ENVELOPE_KEY,
        MODEL_OUTPUT_ITEM_TYPE,
        SCRIPTED_MODEL_API,
        SCRIPTED_MODEL_PROVIDER,
        SOURCE_TOOL_NOT_OFFERED_RESULT,
        TOOL_CALL_LIMIT_REACHED_RESULT,
        TOOL_RESULT_ITEM_TYPE,
        USER_TEXT_ITEM_TYPE,
        AgentDefinition,
        FrameworkToolSourceRouter,
        FunctionTool,
        InstructionContext,
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
        PlanningToolChoice,
        ScriptedModelClient,
        ScriptedModelReply,
        ScriptedModelResponder,
        ScriptedToolCall,
        SourceToolDefinition,
        SourceToolListing,
        ToolCallContext,
        ToolChoice,
        ToolResultItem,
        ToolSource,
        UserTextItem,
        build_context_summary_note,
        build_context_summary_omission_note,
        function_tool,
        pair_tool_calls_with_results,
    )
    from dex_agents.builtin_loop import BuiltinLoopAgentRunner as StepwiseAgentRunner
    from dex_agents.builtin_loop import builtin_agent_session_flow as stepwise_agent_session_flow
    from dex_agents.builtin_loop import (
        builtin_agent_session_options as stepwise_agent_session_options,
    )
    from dex_agents.builtin_loop import decode_builtin_loop_message as decode_stepwise_message
    from dex_agents.builtin_loop import encode_builtin_loop_message as encode_stepwise_message

_RENAMED_ATTRIBUTES = {
    "StepwiseAgentRunner": "BuiltinLoopAgentRunner",
    "stepwise_agent_session_flow": "builtin_agent_session_flow",
    "stepwise_agent_session_options": "builtin_agent_session_options",
    "encode_stepwise_message": "encode_builtin_loop_message",
    "decode_stepwise_message": "decode_builtin_loop_message",
}

_SUBMODULE_NAMES = frozenset(
    {
        "framework_tool_sources",
        "function_tools",
        "model",
        "runner",
        "scripted_model",
        "tool_sources",
        "transcript",
    }
)

__all__ = [
    "CONTEXT_SUMMARY_INSTRUCTIONS",
    "CONTEXT_SUMMARY_NOTE_HEADER",
    "CONTEXT_SUMMARY_REQUEST",
    "DEFAULT_MAX_MODEL_CALLS_PER_TURN",
    "DEFAULT_MAX_RECORDED_REQUESTS",
    "DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT",
    "FRAMEWORK_TOOL_SOURCE_ENVELOPE_KEY",
    "MODEL_OUTPUT_ITEM_TYPE",
    "SCRIPTED_MODEL_API",
    "SCRIPTED_MODEL_PROVIDER",
    "SOURCE_TOOL_NOT_OFFERED_RESULT",
    "TOOL_CALL_LIMIT_REACHED_RESULT",
    "TOOL_RESULT_ITEM_TYPE",
    "USER_TEXT_ITEM_TYPE",
    "AgentDefinition",
    "FrameworkToolSourceRouter",
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
    "PlanningToolChoice",
    "ScriptedModelClient",
    "ScriptedModelReply",
    "ScriptedModelResponder",
    "ScriptedToolCall",
    "SourceToolDefinition",
    "SourceToolListing",
    "StepwiseAgentRunner",
    "ToolCallContext",
    "ToolChoice",
    "ToolResultItem",
    "ToolSource",
    "UserTextItem",
    "build_context_summary_note",
    "build_context_summary_omission_note",
    "decode_stepwise_message",
    "encode_stepwise_message",
    "function_tool",
    "pair_tool_calls_with_results",
    "stepwise_agent_session_flow",
    "stepwise_agent_session_options",
]


def __getattr__(name: str) -> Any:
    if name in _SUBMODULE_NAMES:
        # The deprecated submodule keeps the old names, unlike its replacement.
        return importlib.import_module(f"{__name__}.{name}")
    return forward_deprecated_attribute(__name__, _replacement_module, _RENAMED_ATTRIBUTES, name)


def __dir__() -> list[str]:
    return sorted(__all__)
