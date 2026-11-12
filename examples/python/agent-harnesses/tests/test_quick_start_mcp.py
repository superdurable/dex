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

"""The MCP variant of the OpenAI Agents SDK quick start agent."""

from __future__ import annotations

import sys

from dex_agents import AgentSessionFlow
from dex_agents.mcp import McpStdioServerConfig, McpToolSource

from agent_harness_examples.order_desk import OrderDesk
from agent_harness_examples.quick_start.openai_agents_mcp import build_durable_agent_with_mcp

ORDERS_SERVER = McpStdioServerConfig(
    name="orders",
    command=sys.executable,
    args=["-m", "agent_harness_examples.quick_start.order_desk_mcp_server"],
)


async def test_the_mcp_server_offers_a_read_tool() -> None:
    tools = await McpToolSource([ORDERS_SERVER]).list_tools()
    assert [tool.name for tool in tools] == ["orders__track_shipment"]
    annotations = tools[0].annotations
    assert annotations is not None and annotations.read_only_hint is True


def test_the_agent_takes_mcp_tools_through_tool_sources() -> None:
    flow = build_durable_agent_with_mcp(OrderDesk(), "gpt-5.2", [ORDERS_SERVER])
    assert isinstance(flow, AgentSessionFlow)
