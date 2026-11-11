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
model call answers ``<tool>: <result>`` per call, joined with ``; ``. MCP
tasks of the HTTP servers are polled through ``poll_tool_task``. The
``local`` server runs ``tests.fake_mcp_server`` over stdio and the ``remote``
server is the Streamable HTTP URL in ``$MCP_AGENT_HTTP_URL``. Images of MCP
results go to a ``LocalDirectoryMediaStore`` under the state directory; the
second model call loads them and adds ``image <sha256> <size>`` per image,
computed from the loaded bytes, to that call's summary. When
``$MCP_AGENT_GATEWAY_URL`` is set, a ``gateway`` server is configured too.
"""

from __future__ import annotations

import hashlib
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
    DeferredToolOutcome,
    FrameworkMessage,
    LocalDirectoryMediaStore,
    MediaStore,
    MessageRole,
    ToolCallsRequested,
    ToolEffect,
    ToolPolicy,
    ToolTaskCancellation,
    ToolTaskPoll,
    ToolTaskPollOutcome,
    TurnCompleted,
    describe_tool_call_outcome,
    load_images,
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
MEDIA_DIRECTORY_NAME = "media"
# A policy wins over the tool's readOnlyHint annotation, and raises the effect a tool declares in
# the Dex _meta keys.
TOOL_POLICIES = {
    "local__audit_lookup": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    "remote__get_balance": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
}
_MODEL_CALL_USAGE = AgentUsage(model_calls=1, input_tokens=10, output_tokens=5)


class McpScriptedRunner(AgentFrameworkRunner[DeferredToolOutcome]):
    def __init__(self, tools: McpToolSource, media_store: MediaStore) -> None:
        self._tools = tools
        self._media_store = media_store

    def build_user_message(self, text: str) -> FrameworkMessage:
        return FrameworkMessage(
            role=MessageRole.USER, text=text, framework_item=json.dumps({"text": text})
        )

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        if turn.framework_run_state is not None:
            return await self._answer_with_results(turn, json.loads(turn.framework_run_state))
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

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        handlers = await self._tools.deferred_tool_handlers()
        return await handlers[call.tool_call.tool_name](call)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        return await self._tools.poll_tool_task(poll)

    async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        await self._tools.cancel_tool_task(cancellation)

    async def _answer_with_results(
        self, turn: AgentTurnContext, calls: list[list[str]]
    ) -> AgentTurnOutcome:
        messages: list[FrameworkMessage] = []
        summaries: list[str] = []
        for tool_call_id, tool_name, arguments in calls:
            record = turn.recorded_tool_call(tool_call_id)
            if record is None:
                raise RuntimeError(f"no record for {tool_call_id}")
            result = describe_tool_call_outcome(record)
            item: dict[str, object] = {"tool_call_id": tool_call_id, "result": result}
            if record.images:
                item["images"] = [image.uri for image in record.images]
            messages.append(
                FrameworkMessage(
                    role=MessageRole.TOOL,
                    tool_call_id=tool_call_id,
                    tool_name=tool_name,
                    tool_arguments=arguments,
                    tool_output=result,
                    framework_item=json.dumps(item),
                    images=record.images,
                )
            )
            loaded = await load_images(self._media_store, record.images)
            seen = [
                f"image {hashlib.sha256(data).hexdigest()} {len(data)}"
                for data in (loaded[image.sha256] for image in record.images)
            ]
            summaries.append(" ".join([f"{tool_name}: {result}", *seen]))
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
    return McpToolSource(servers, media_store=build_media_store(state_dir))


def build_media_store(state_dir: Path) -> MediaStore:
    return LocalDirectoryMediaStore(state_dir / MEDIA_DIRECTORY_NAME)


def build_mcp_agent_flow(
    state_dir: Path, http_url: str, gateway_url: str | None = None
) -> AgentSessionFlow:
    return AgentSessionFlow(
        McpScriptedRunner(
            build_mcp_tool_source(state_dir, http_url, gateway_url), build_media_store(state_dir)
        ),
        flow_type=MCP_AGENT_FLOW_TYPE,
        options=AgentSessionOptions(tool_policies=TOOL_POLICIES),
    )


def build_mcp_agent_flow_from_environment() -> AgentSessionFlow:
    return build_mcp_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        os.environ[HTTP_URL_ENVIRONMENT_VARIABLE],
        os.environ.get(GATEWAY_URL_ENVIRONMENT_VARIABLE),
    )
