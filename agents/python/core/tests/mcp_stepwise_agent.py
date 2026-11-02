# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Stepwise agent whose tools come from a function tool and an ``McpToolSource``.

Its model is the scripted demo model of ``dex_agents.stepwise``. It reads
``call <tool> <json>`` from the user text, calls that tool, and answers
``<tool>: <output>`` once the result arrives. Any other text is answered
``said: <text>``. The ``local`` server runs ``tests.fake_mcp_server`` over
stdio, and ``remote`` is a Streamable HTTP URL. The source offers the
resource and prompt tools.
"""

from __future__ import annotations

import sys
from pathlib import Path

from dex_agents import AgentSessionFlow, BuiltInTool, ToolEffect
from dex_agents.mcp import McpStdioServerConfig, McpStreamableHttpServerConfig, McpToolSource
from dex_agents.stepwise import (
    AgentDefinition,
    ModelRequest,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
    function_tool,
    stepwise_agent_session_flow,
)
from tests.fake_mcp_server import STATE_DIR_ENVIRONMENT_VARIABLE

MCP_STEPWISE_AGENT_FLOW_TYPE = "McpStepwiseAgent"
CORE_DIR = Path(__file__).resolve().parents[1]


@function_tool(effect=ToolEffect.READ)
def take_note(text: str) -> str:
    """Keep a note for later."""
    return f"kept {text}"


def reply_from_commands(request: ModelRequest) -> ScriptedModelReply:
    results = ScriptedModelClient.tool_results(request)
    if results:
        return ScriptedModelReply(text="; ".join(f"{r.tool_name}: {r.output}" for r in results))
    command = ScriptedModelClient.user_texts(request)[-1]
    if not command.startswith("call "):
        return ScriptedModelReply(text=f"said: {command}")
    tool_name, arguments = command.removeprefix("call ").split(" ", 1)
    return ScriptedModelReply(
        tool_calls=[ScriptedToolCall(tool_name=tool_name, arguments=arguments)]
    )


def build_mcp_stepwise_definition(state_dir: Path, http_url: str) -> AgentDefinition:
    tools = McpToolSource(
        [
            McpStdioServerConfig(
                name="local",
                command=sys.executable,
                args=["-m", "tests.fake_mcp_server", "stdio"],
                env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
                cwd=str(CORE_DIR),
            ),
            McpStreamableHttpServerConfig(name="remote", url=http_url),
        ],
        resource_and_prompt_tools_enabled=True,
    )
    return AgentDefinition(
        name="mcp-stepwise",
        instructions="You are a scripted test agent.",
        model=ScriptedModelClient(reply_from_commands),
        tools=[take_note],
        tool_sources=[tools],
        built_in_tools=frozenset({BuiltInTool.ASK_USER}),
    )


def build_mcp_stepwise_flow(definition: AgentDefinition) -> AgentSessionFlow:
    return stepwise_agent_session_flow(definition, flow_type=MCP_STEPWISE_AGENT_FLOW_TYPE)
