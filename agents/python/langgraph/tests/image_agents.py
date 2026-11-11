# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Agents with a media store, built with create_agent and as a custom StateGraph graph.

Both use the scripted chat model, which records the images it receives in
``model-inputs.log``, and two tools:

* ``take_screenshot`` comes from a tool source. It uploads a PNG and returns it
  as a reference.
* ``render_chart`` is the graph's own tool. It returns a PNG as a base64 image
  block; ``form`` picks a ``data:`` URL (``data_url``), the nonstandard
  ``image/jpg`` type (``jpg_mime``), or bytes that are not an image
  (``not_an_image``) instead.

The store is a ``LocalDirectoryMediaStore`` under the state directory, shared
by the test and the Worker. The custom graph's model node calls
``inline_image_references``.
"""

from __future__ import annotations

import base64
import os
import struct
import zlib
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from dex_agents import (
    AgentSessionFlow,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    LocalDirectoryMediaStore,
    ToolEffect,
    ToolExecutionResult,
    ToolPolicy,
)
from langchain.agents import create_agent
from langchain_core.messages import AIMessage
from langchain_core.tools import BaseTool, tool
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import tools_condition

from dex_agents_langgraph import (
    CompiledGraph,
    DurableToolCallMiddleware,
    durable_tool_node,
    get_current_tool_source_tools,
    inline_image_references,
    langchain_agent_session_flow,
    langgraph_agent_session_flow,
)
from tests.scripted_agents import INVOCATION_LOG_NAME
from tests.scripted_model import ScriptedChatModel

IMAGE_CREATE_AGENT_FLOW_TYPE = "ImageLangChainAgent"
IMAGE_STATE_GRAPH_FLOW_TYPE = "ImageLangGraphAgent"
IMAGE_AGENT_FLOW_TYPES = [IMAGE_CREATE_AGENT_FLOW_TYPE, IMAGE_STATE_GRAPH_FLOW_TYPE]
STATE_DIR_ENVIRONMENT_VARIABLE = "IMAGE_AGENT_STATE_DIR"
MODEL_INPUTS_LOG_NAME = "model-inputs.log"
MEDIA_DIRECTORY_NAME = "media"
TOOL_POLICIES = {
    "take_screenshot": ToolPolicy(ToolEffect.READ),
    "render_chart": ToolPolicy(ToolEffect.READ),
}


#: Bytes ``render_chart`` returns as an image with ``form`` ``not_an_image``.
NOT_AN_IMAGE = b"this is not an image, only text bytes"


def solid_png(red: int, green: int, blue: int) -> bytes:
    """Return a 2x2 PNG of one color, the same bytes on every call."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        checksum = struct.pack(">I", zlib.crc32(kind + data))
        return struct.pack(">I", len(data)) + kind + data + checksum

    header = struct.pack(">IIBBBBB", 2, 2, 8, 2, 0, 0, 0)
    row = b"\x00" + bytes((red, green, blue)) * 2
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", header)
        + chunk(b"IDAT", zlib.compress(row * 2, 9))
        + chunk(b"IEND", b"")
    )


def screenshot_png() -> bytes:
    return solid_png(0, 128, 255)


def chart_png() -> bytes:
    return solid_png(255, 128, 0)


def build_media_store(state_dir: Path) -> LocalDirectoryMediaStore:
    return LocalDirectoryMediaStore(state_dir / MEDIA_DIRECTORY_NAME)


@dataclass(frozen=True)
class ScreenshotToolDefinition:
    name: str
    description: str
    input_schema: Mapping[str, Any]


class ScreenshotToolSource:
    """Offer ``take_screenshot``, which uploads a PNG and returns its reference."""

    source_name = "screenshots"

    def __init__(self, media_store: LocalDirectoryMediaStore) -> None:
        self._media_store = media_store

    async def list_tools(self) -> Sequence[ScreenshotToolDefinition]:
        return [
            ScreenshotToolDefinition(
                "take_screenshot", "Take a screenshot.", {"type": "object", "properties": {}}
            )
        ]

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: Any,
    ) -> DeferredToolCall:
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=turn.resolve_tool_policy(tool_name, ToolEffect.READ),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        reference = await self._media_store.put_image(screenshot_png())
        return ToolExecutionResult(output="took a screenshot", images=[reference])

    async def poll_tool_task(self, poll: Any) -> Any:
        raise LookupError("take_screenshot starts no tool tasks")


def build_image_agent_flows(state_dir: Path) -> list[AgentSessionFlow]:
    media_store = build_media_store(state_dir)
    tool_sources = [ScreenshotToolSource(media_store)]
    return [
        langchain_agent_session_flow(
            create_agent(
                _image_model(state_dir),
                tools=[build_render_chart_tool()],
                middleware=[DurableToolCallMiddleware()],
            ),
            flow_type=IMAGE_CREATE_AGENT_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
            tool_sources=tool_sources,
            media_store=media_store,
        ),
        langgraph_agent_session_flow(
            build_image_state_graph(state_dir),
            flow_type=IMAGE_STATE_GRAPH_FLOW_TYPE,
            tool_policies=TOOL_POLICIES,
            tool_sources=tool_sources,
            media_store=media_store,
        ),
    ]


def build_image_agent_flows_from_environment() -> list[AgentSessionFlow]:
    return build_image_agent_flows(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


def build_image_state_graph(state_dir: Path) -> CompiledGraph:
    tools = [build_render_chart_tool()]
    model = _image_model(state_dir)

    async def call_model(state: MessagesState) -> dict[str, list[AIMessage]]:
        messages = await inline_image_references(state["messages"])
        reply = await model.bind_tools([*tools, *get_current_tool_source_tools()]).ainvoke(messages)
        return {"messages": [reply]}

    builder = StateGraph(MessagesState)
    builder.add_node("agent", call_model)
    builder.add_node("tools", durable_tool_node(tools))
    builder.add_edge(START, "agent")
    builder.add_conditional_edges("agent", tools_condition)
    builder.add_edge("tools", "agent")
    return builder.compile()


def build_render_chart_tool() -> BaseTool:
    @tool
    def render_chart(form: str = "base64") -> list[dict[str, str]]:
        """Render a chart."""
        encoded = base64.b64encode(chart_png()).decode()
        image = {
            "base64": {"type": "image", "base64": encoded, "mime_type": "image/png"},
            "data_url": {"type": "image", "url": f"data:image/png;base64,{encoded}"},
            "jpg_mime": {"type": "image", "base64": encoded, "mime_type": "image/jpg"},
            "not_an_image": {
                "type": "image",
                "base64": base64.b64encode(NOT_AN_IMAGE).decode(),
                "mime_type": "image/png",
            },
        }[form]
        return [{"type": "text", "text": "rendered a chart"}, image]

    return render_chart


def _image_model(state_dir: Path) -> ScriptedChatModel:
    return ScriptedChatModel(
        invocation_log_path=str(state_dir / INVOCATION_LOG_NAME),
        model_input_log_path=str(state_dir / MODEL_INPUTS_LOG_NAME),
    )
