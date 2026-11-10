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

"""The order support agent on LangChain create_agent, as a durable Dex agent session.

Every model call is one RunModel Step, and every tool call is its own Step.
look_up_order is a READ and runs without approval. cancel_order is a
NON_IDEMPOTENT_WRITE: it waits in AwaitToolApproval and runs at most once.
"""

from __future__ import annotations

from typing import Any

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents_langgraph import DurableToolCallMiddleware, langchain_agent_session_flow
from langchain.agents import create_agent
from langchain_core.language_models import BaseChatModel
from langchain_core.tools import tool
from langgraph.pregel import Pregel

from agent_harness_examples.order_desk import ORDER_SUPPORT_INSTRUCTIONS, OrderDesk

LANGCHAIN_ORDER_SUPPORT_FLOW_TYPE = "LangChainOrderSupport"


def build_langchain_order_support_flow(
    order_desk: OrderDesk, model: str | BaseChatModel
) -> AgentSessionFlow:
    """Builds the session Flow; ``model`` is a LangChain model name, such as "openai:gpt-5.2"."""

    @tool
    def look_up_order(order_id: str) -> str:
        """Look up one order by its order ID and return its items and status."""
        return order_desk.describe_order(order_id)

    @tool
    def cancel_order(order_id: str, reason: str) -> str:
        """Cancel one order that has not shipped yet."""
        return order_desk.cancel_order(order_id, reason)

    # Only a Worker calls the factory, so a Client process needs no provider key.
    def build_order_support_agent() -> Pregel[Any, Any, Any, Any]:
        return create_agent(
            model,
            tools=[look_up_order, cancel_order],
            system_prompt=ORDER_SUPPORT_INSTRUCTIONS,
            middleware=[DurableToolCallMiddleware()],
        )

    return langchain_agent_session_flow(
        build_order_support_agent,
        flow_type=LANGCHAIN_ORDER_SUPPORT_FLOW_TYPE,
        tool_policies={
            "look_up_order": ToolPolicy(ToolEffect.READ),
            "cancel_order": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        },
    )
