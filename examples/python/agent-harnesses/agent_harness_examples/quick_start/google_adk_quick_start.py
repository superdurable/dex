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

"""The quick start agent on Google ADK, before and after Dex."""

from __future__ import annotations

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents_google_adk import adk_agent_session_flow
from google.adk.agents import LlmAgent
from google.adk.models.base_llm import BaseLlm

from agent_harness_examples.order_desk import ORDER_SUPPORT_INSTRUCTIONS, OrderDesk


def build_plain_agent(order_desk: OrderDesk, model: str | BaseLlm) -> LlmAgent:
    """Returns the agent as Google ADK alone writes it."""

    def look_up_order(order_id: str) -> str:
        """Look up one order by its order ID and return its items and status."""
        return order_desk.describe_order(order_id)

    def cancel_order(order_id: str, reason: str) -> str:
        """Cancel one order that has not shipped yet."""
        return order_desk.cancel_order(order_id, reason)

    return LlmAgent(
        name="order_support",
        model=model,
        instruction=ORDER_SUPPORT_INSTRUCTIONS,
        tools=[look_up_order, cancel_order],
    )


def build_durable_agent(order_desk: OrderDesk, model: str | BaseLlm) -> AgentSessionFlow:
    """Returns the same agent as a durable Dex agent session."""
    return adk_agent_session_flow(
        build_plain_agent(order_desk, model),
        flow_type="GoogleADKQuickStart",
        tool_policies={
            "look_up_order": ToolPolicy(ToolEffect.READ),
            "cancel_order": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        },
    )
