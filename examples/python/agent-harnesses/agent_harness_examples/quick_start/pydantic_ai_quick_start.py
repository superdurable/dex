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

"""The quick start agent on Pydantic AI, before and after Dex."""

from __future__ import annotations

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents_pydantic_ai import pydantic_ai_session_flow
from pydantic_ai import Agent
from pydantic_ai.models import Model

from agent_harness_examples.order_desk import ORDER_SUPPORT_INSTRUCTIONS, OrderDesk


def build_plain_agent(order_desk: OrderDesk, model: str | Model) -> Agent[None, str]:
    """Returns the agent as Pydantic AI alone writes it."""
    # The model is resolved at the first run, so a Client process needs no provider key.
    agent = Agent(model, instructions=ORDER_SUPPORT_INSTRUCTIONS, defer_model_check=True)

    @agent.tool_plain
    def look_up_order(order_id: str) -> str:
        """Look up one order by its order ID and return its items and status."""
        return order_desk.describe_order(order_id)

    @agent.tool_plain
    def cancel_order(order_id: str, reason: str) -> str:
        """Cancel one order that has not shipped yet."""
        return order_desk.cancel_order(order_id, reason)

    return agent


def build_durable_agent(order_desk: OrderDesk, model: str | Model) -> AgentSessionFlow:
    """Returns the same agent as a durable Dex agent session."""
    return pydantic_ai_session_flow(
        build_plain_agent(order_desk, model),
        flow_type="PydanticAIQuickStart",
        tool_policies={
            "look_up_order": ToolPolicy(ToolEffect.READ),
            "cancel_order": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        },
    )
