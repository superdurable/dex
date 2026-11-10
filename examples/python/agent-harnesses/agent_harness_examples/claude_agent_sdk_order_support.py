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

"""The order support agent on the Claude Agent SDK, as a durable Dex agent session.

The Claude Code CLI runs a whole turn segment in one RunModel Step. The READ
tool look_up_order runs inside that segment. cancel_order is a
NON_IDEMPOTENT_WRITE: the CLI defers it, it waits in AwaitToolApproval, runs
at most once in its own Step, and a new segment resumes the Claude session.
The Worker authenticates with ANTHROPIC_API_KEY.
"""

from __future__ import annotations

from typing import Any

from claude_agent_sdk import ClaudeAgentOptions, tool
from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents_claude_agent_sdk import ClaudeAgentOptionsContext, claude_agent_session_flow

from agent_harness_examples.order_desk import ORDER_SUPPORT_INSTRUCTIONS, OrderDesk

CLAUDE_AGENT_SDK_ORDER_SUPPORT_FLOW_TYPE = "ClaudeAgentSDKOrderSupport"


def build_claude_agent_sdk_order_support_flow(order_desk: OrderDesk, model: str) -> AgentSessionFlow:
    """Builds the session Flow; ``model`` is a Claude model name, such as "claude-opus-5-5"."""

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

    def build_options(context: ClaudeAgentOptionsContext) -> ClaudeAgentOptions:
        return ClaudeAgentOptions(
            model=model,
            system_prompt=ORDER_SUPPORT_INSTRUCTIONS,
            # No Claude Code built-in tools: the agent uses only the order tools.
            tools=[],
            # Settings files could approve a tool before Dex asks for approval.
            setting_sources=[],
        )

    return claude_agent_session_flow(
        build_options,
        [look_up_order, cancel_order],
        flow_type=CLAUDE_AGENT_SDK_ORDER_SUPPORT_FLOW_TYPE,
        tool_policies={
            "look_up_order": ToolPolicy(ToolEffect.READ),
            "cancel_order": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        },
    )
