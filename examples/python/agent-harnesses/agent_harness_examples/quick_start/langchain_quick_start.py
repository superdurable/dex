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

"""The quick start agent on LangChain create_agent, before and after Dex."""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents_langgraph import DurableToolCallMiddleware, langchain_agent_session_flow
from langchain.agents import create_agent
from langchain.agents.middleware import AgentMiddleware
from langchain_core.language_models import BaseChatModel
from langchain_core.tools import tool
from langgraph.pregel import Pregel

from agent_harness_examples.order_desk import ORDER_SUPPORT_INSTRUCTIONS, OrderDesk


def build_plain_agent(
    order_desk: OrderDesk,
    model: str | BaseChatModel,
    middleware: Sequence[AgentMiddleware[Any, Any]] = (),
) -> Pregel[Any, Any, Any, Any]:
    """Returns the agent as LangChain alone writes it."""

    @tool
    def look_up_order(order_id: str) -> str:
        """Look up one order by its order ID and return its items and status."""
        return order_desk.describe_order(order_id)

    @tool
    def cancel_order(order_id: str, reason: str) -> str:
        """Cancel one order that has not shipped yet."""
        return order_desk.cancel_order(order_id, reason)

    return create_agent(
        model,
        tools=[look_up_order, cancel_order],
        system_prompt=ORDER_SUPPORT_INSTRUCTIONS,
        middleware=middleware,
    )


def build_durable_agent(order_desk: OrderDesk, model: str | BaseChatModel) -> AgentSessionFlow:
    """Returns the same agent as a durable Dex agent session."""
    return langchain_agent_session_flow(
        # Dex builds the agent on the Worker, with middleware that runs each tool call as a Step.
        lambda: build_plain_agent(order_desk, model, [DurableToolCallMiddleware()]),
        flow_type="LangChainQuickStart",
        tool_policies={
            "look_up_order": ToolPolicy(ToolEffect.READ),
            "cancel_order": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        },
    )
