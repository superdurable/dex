# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted runner whose tools all come from an ``McpToolSource``.

The model reads ``call <tool> <json>`` from the user message, joined with
`` && call `` for parallel calls, and asks for those MCP tools. The second
model call answers ``<tool>: <result>`` per call, joined with ``; ``. The
``local`` server runs ``tests.fake_mcp_server`` over stdio and the ``remote``
server is the Streamable HTTP URL in ``$MCP_AGENT_HTTP_URL``. When
``$MCP_AGENT_GATEWAY_URL`` is set, a ``gateway`` server is configured too.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

from dex_agents import (
    AgentFrameworkRunner,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    DeferredToolExecution,
    FrameworkMessage,
    MessageRole,
    ToolCallsRequested,
    ToolEffect,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
    describe_tool_call_outcome,
)
from dex_agents.mcp import (
    McpServerConfig,
    McpStdioServerConfig,
    McpStreamableHttpServerConfig,
    McpToolSource,
)
from tests.fake_mcp_server import STATE_DIR_ENVIRONMENT_VARIABLE

MCP_AGENT_FLOW_TYPE = "McpScriptedAgent"
HTTP_URL_ENVIRONMENT_VARIABLE = "MCP_AGENT_HTTP_URL"
GATEWAY_URL_ENVIRONMENT_VARIABLE = "MCP_AGENT_GATEWAY_URL"
CORE_DIR = Path(__file__).resolve().parents[1]
# A policy wins over the tool's readOnlyHint annotation.
TOOL_POLICIES = {"local__audit_lookup": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE)}
_MODEL_CALL_USAGE = AgentUsage(model_calls=1, input_tokens=10, output_tokens=5)


class McpScriptedRunner(AgentFrameworkRunner):
    def __init__(self, tools: McpToolSource) -> None:
        self._tools = tools

    def build_user_message(self, text: str) -> FrameworkMessage:
        return FrameworkMessage(
            role=MessageRole.USER, text=text, framework_item=json.dumps({"text": text})
        )

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        if turn.framework_run_state is not None:
            return self._answer_with_results(turn, json.loads(turn.framework_run_state))
        command = json.loads(turn.messages[-1].framework_item)["text"]
        offered = {tool.name for tool in await self._tools.list_tools()}
        calls = []
        for index, request in enumerate(command.removeprefix("call ").split(" && call ")):
            tool_name, arguments = request.split(" ", 1)
            if tool_name not in offered:
                raise ValueError(f"the model was not offered {tool_name}")
            calls.append(
                self._tools.deferred_tool_call(
                    turn,
                    tool_call_id=f"{turn.turn_id}-call-{index}",
                    tool_name=tool_name,
                    arguments=arguments,
                )
            )
        return ToolCallsRequested(
            framework_run_state=json.dumps(
                [[call.tool_call_id, call.tool_name, call.arguments] for call in calls]
            ),
            tool_calls=calls,
            usage=_MODEL_CALL_USAGE,
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        handlers = await self._tools.deferred_tool_handlers()
        return await handlers[call.tool_call.tool_name](call)

    def _answer_with_results(
        self, turn: AgentTurnContext, calls: list[list[str]]
    ) -> AgentTurnOutcome:
        messages: list[FrameworkMessage] = []
        summaries: list[str] = []
        for tool_call_id, tool_name, arguments in calls:
            record = turn.recorded_tool_call(tool_call_id)
            if record is None:
                raise RuntimeError(f"no record for {tool_call_id}")
            result = describe_tool_call_outcome(record)
            messages.append(
                FrameworkMessage(
                    role=MessageRole.TOOL,
                    tool_call_id=tool_call_id,
                    tool_name=tool_name,
                    tool_arguments=arguments,
                    tool_output=result,
                    framework_item=json.dumps({"tool_call_id": tool_call_id, "result": result}),
                )
            )
            summaries.append(f"{tool_name}: {result}")
        text = "; ".join(summaries)
        messages.append(
            FrameworkMessage(
                role=MessageRole.ASSISTANT, text=text, framework_item=json.dumps({"text": text})
            )
        )
        return TurnCompleted(new_messages=messages, usage=_MODEL_CALL_USAGE)


def build_mcp_tool_source(
    state_dir: Path, http_url: str, gateway_url: str | None = None
) -> McpToolSource:
    servers: list[McpServerConfig] = [
        McpStdioServerConfig(
            name="local",
            command=sys.executable,
            args=["-m", "tests.fake_mcp_server", "stdio"],
            env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
            cwd=str(CORE_DIR),
        ),
        McpStreamableHttpServerConfig(name="remote", url=http_url),
    ]
    if gateway_url is not None:
        servers.append(McpStreamableHttpServerConfig(name="gateway", url=gateway_url))
    return McpToolSource(servers)


def build_mcp_agent_flow(
    state_dir: Path, http_url: str, gateway_url: str | None = None
) -> AgentSessionFlow:
    return AgentSessionFlow(
        McpScriptedRunner(build_mcp_tool_source(state_dir, http_url, gateway_url)),
        flow_type=MCP_AGENT_FLOW_TYPE,
        options=AgentSessionOptions(tool_policies=TOOL_POLICIES),
    )


def build_mcp_agent_flow_from_environment() -> AgentSessionFlow:
    return build_mcp_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        os.environ[HTTP_URL_ENVIRONMENT_VARIABLE],
        os.environ.get(GATEWAY_URL_ENVIRONMENT_VARIABLE),
    )
