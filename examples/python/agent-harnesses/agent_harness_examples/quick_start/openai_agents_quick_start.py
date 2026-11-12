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

"""The quick start agent on the OpenAI Agents SDK, before and after Dex."""

from __future__ import annotations

from agents import Agent, Model, function_tool
from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents_openai_agents import openai_agents_session_flow

from agent_harness_examples.order_desk import ORDER_SUPPORT_INSTRUCTIONS, OrderDesk


def build_plain_agent(order_desk: OrderDesk, model: str | Model) -> Agent:
    """Returns the agent as the OpenAI Agents SDK alone writes it."""

    @function_tool
    def look_up_order(order_id: str) -> str:
        """Look up one order by its order ID and return its items and status."""
        return order_desk.describe_order(order_id)

    @function_tool
    def cancel_order(order_id: str, reason: str) -> str:
        """Cancel one order that has not shipped yet."""
        return order_desk.cancel_order(order_id, reason)

    return Agent(
        name="OrderSupport",
        instructions=ORDER_SUPPORT_INSTRUCTIONS,
        model=model,
        tools=[look_up_order, cancel_order],
    )


def build_durable_agent(order_desk: OrderDesk, model: str | Model) -> AgentSessionFlow:
    """Returns the same agent as a durable Dex agent session."""
    return openai_agents_session_flow(
        build_plain_agent(order_desk, model),
        flow_type="OpenAIAgentsQuickStart",
        tool_policies={
            "look_up_order": ToolPolicy(ToolEffect.READ),
            "cancel_order": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        },
    )
