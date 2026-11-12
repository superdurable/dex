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

"""The OpenAI Agents SDK quick start agent with the tools of an MCP server."""

from __future__ import annotations

from collections.abc import Sequence

from agents import Model
from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents.mcp import McpServerConfig, McpToolSource
from dex_agents_openai_agents import openai_agents_session_flow

from agent_harness_examples.order_desk import OrderDesk
from agent_harness_examples.quick_start.openai_agents_quick_start import build_plain_agent


def build_durable_agent_with_mcp(
    order_desk: OrderDesk, model: str | Model, mcp_servers: Sequence[McpServerConfig]
) -> AgentSessionFlow:
    """Returns the quick start agent with MCP tools, each call in its own Step.

    The servers go through tool_sources, not Agent.mcp_servers, which the
    adapter refuses. A tool marked readOnlyHint is a read.
    """
    return openai_agents_session_flow(
        build_plain_agent(order_desk, model),
        flow_type="OpenAIAgentsMcpQuickStart",
        tool_policies={
            "look_up_order": ToolPolicy(ToolEffect.READ),
            "cancel_order": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        },
        tool_sources=[McpToolSource(mcp_servers)],
    )
