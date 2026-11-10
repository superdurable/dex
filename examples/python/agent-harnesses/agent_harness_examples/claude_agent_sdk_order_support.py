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

The Claude Code CLI makes the model calls and runs look_up_order, a READ,
inside one RunModel Step. When the model asks for cancel_order, a
NON_IDEMPOTENT_WRITE, the CLI stops before running it. The call waits in
AwaitToolApproval and runs at most once in its own Step. Then the next
RunModel Step continues the same Claude conversation. The Worker
authenticates with ANTHROPIC_API_KEY.
"""

from __future__ import annotations

import os
from collections.abc import Mapping
from typing import Any

from claude_agent_sdk import ClaudeAgentOptions, tool
from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents_claude_agent_sdk import ClaudeAgentOptionsContext, claude_agent_session_flow

from agent_harness_examples.order_desk import ORDER_SUPPORT_INSTRUCTIONS, OrderDesk

CLAUDE_AGENT_SDK_ORDER_SUPPORT_FLOW_TYPE = "ClaudeAgentSDKOrderSupport"
# Anthropic settings the CLI keeps from the Worker environment.
KEPT_ANTHROPIC_SETTINGS = ("ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL")
# Claude Code settings that would sign in another way than with ANTHROPIC_API_KEY.
OTHER_CLAUDE_SIGN_IN_SETTINGS = (
    "CLAUDE_CODE_OAUTH_TOKEN",
    "CLAUDE_CODE_USE_BEDROCK",
    "CLAUDE_CODE_USE_VERTEX",
    "CLAUDE_CODE_USE_FOUNDRY",
)


def build_claude_agent_sdk_order_support_flow(order_desk: OrderDesk, model: str) -> AgentSessionFlow:
    """Builds the session Flow; ``model`` is a Claude model name, such as "claude-opus-5-5"."""

    # The tools are closures, so they call the OrderDesk passed in here.
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

    # Every session gets the same options, so the factory does not read its context.
    def build_options(context: ClaudeAgentOptionsContext) -> ClaudeAgentOptions:
        return ClaudeAgentOptions(
            model=model,
            env=build_claude_cli_environment(os.environ),
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


def build_claude_cli_environment(worker_environment: Mapping[str, str]) -> dict[str, str]:
    """Returns the CLI environment overrides that leave ANTHROPIC_API_KEY as the only sign-in.

    The CLI inherits the Worker's environment, and these values override it.
    Every other Anthropic setting, such as ANTHROPIC_AUTH_TOKEN, and every
    other Claude Code sign-in setting is set to an empty value.
    """
    environment = {
        name: ""
        for name in worker_environment
        if name.startswith("ANTHROPIC_") and name not in KEPT_ANTHROPIC_SETTINGS
    }
    environment.update({name: "" for name in OTHER_CLAUDE_SIGN_IN_SETTINGS})
    environment.update(
        {name: worker_environment[name] for name in KEPT_ANTHROPIC_SETTINGS if name in worker_environment}
    )
    if not environment.get("ANTHROPIC_API_KEY"):
        raise ValueError("the Claude Agent SDK agent needs ANTHROPIC_API_KEY")
    return environment
