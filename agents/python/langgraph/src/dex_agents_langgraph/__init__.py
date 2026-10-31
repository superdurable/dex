# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Durable Dex agent sessions for LangChain agents and LangGraph graphs.

Build an agent with ``langchain.agents.create_agent`` and
``DurableToolCallMiddleware``, or a custom ``StateGraph`` with
``durable_tool_node``, and register it with ``langchain_agent_session_flow``
or ``langgraph_agent_session_flow``. Each Flow execution is one agent session
in Step mode: every model call is one ``RunModel`` Step, and every tool call
runs in its own Step, in parallel, so a crash repeats at most the one call
that was running. See the package README for the full guide.
"""

from dex_agents_langgraph.messages import (
    framework_message_to_langchain_message,
    framework_messages_to_langchain_messages,
    langchain_message_to_framework_message,
)
from dex_agents_langgraph.runner import CompiledGraph, CompiledGraphSource, LangGraphAgentRunner
from dex_agents_langgraph.session_flows import (
    langchain_agent_session_flow,
    langgraph_agent_session_flow,
)
from dex_agents_langgraph.tool_deferral import (
    TOOL_CALL_IDEMPOTENCY_KEY_CONFIGURABLE,
    DurableToolCallMiddleware,
    DurableToolNode,
    ToolCallExecutor,
    awrap_durable_tool_call,
    durable_tool_node,
    get_tool_call_idempotency_key,
    tool_effect_from_metadata,
)

__all__ = [
    "TOOL_CALL_IDEMPOTENCY_KEY_CONFIGURABLE",
    "CompiledGraph",
    "CompiledGraphSource",
    "DurableToolCallMiddleware",
    "DurableToolNode",
    "LangGraphAgentRunner",
    "ToolCallExecutor",
    "awrap_durable_tool_call",
    "durable_tool_node",
    "framework_message_to_langchain_message",
    "framework_messages_to_langchain_messages",
    "get_tool_call_idempotency_key",
    "langchain_agent_session_flow",
    "langchain_message_to_framework_message",
    "langgraph_agent_session_flow",
    "tool_effect_from_metadata",
]
