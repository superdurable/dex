# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""The quick start agent on the Claude Agent SDK, before and after Dex."""

from __future__ import annotations

import os
from typing import Any

from claude_agent_sdk import ClaudeAgentOptions, SdkMcpTool, create_sdk_mcp_server, tool
from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents_claude_agent_sdk import ClaudeAgentOptionsContext, claude_agent_session_flow

from agent_harness_examples.claude_agent_sdk_order_support import build_claude_cli_environment
from agent_harness_examples.order_desk import ORDER_SUPPORT_INSTRUCTIONS, OrderDesk


def build_order_tools(order_desk: OrderDesk) -> list[SdkMcpTool[Any]]:
    """Returns the two order tools, as the Claude Agent SDK defines tools."""

    @tool(
        "look_up_order",
        "Look up one order by its order ID and return its items and status.",
        {"order_id": str},
    )
    async def look_up_order(arguments: dict[str, Any]) -> dict[str, Any]:
        order = order_desk.describe_order(arguments["order_id"])
        return {"content": [{"type": "text", "text": order}]}

    @tool(
        "cancel_order",
        "Cancel one order that has not shipped yet.",
        {"order_id": str, "reason": str},
    )
    async def cancel_order(arguments: dict[str, Any]) -> dict[str, Any]:
        result = order_desk.cancel_order(arguments["order_id"], arguments["reason"])
        return {"content": [{"type": "text", "text": result}]}

    return [look_up_order, cancel_order]


def build_plain_agent(order_desk: OrderDesk, model: str) -> ClaudeAgentOptions:
    """Returns the agent's options as the Claude Agent SDK alone writes them."""
    orders = create_sdk_mcp_server("orders", tools=build_order_tools(order_desk))
    return ClaudeAgentOptions(
        model=model,
        system_prompt=ORDER_SUPPORT_INSTRUCTIONS,
        mcp_servers={"orders": orders},
        allowed_tools=["mcp__orders__look_up_order", "mcp__orders__cancel_order"],
    )


def build_durable_agent(order_desk: OrderDesk, model: str) -> AgentSessionFlow:
    """Returns the same agent as a durable Dex agent session."""

    # Dex passes the tools to the CLI itself and decides when each one runs.
    def build_options(context: ClaudeAgentOptionsContext) -> ClaudeAgentOptions:
        return ClaudeAgentOptions(
            model=model,
            env=build_claude_cli_environment(os.environ),
            system_prompt=ORDER_SUPPORT_INSTRUCTIONS,
            tools=[],
            setting_sources=[],
        )

    return claude_agent_session_flow(
        build_options,
        build_order_tools(order_desk),
        flow_type="ClaudeAgentSDKQuickStart",
        tool_policies={
            "look_up_order": ToolPolicy(ToolEffect.READ),
            "cancel_order": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        },
    )
