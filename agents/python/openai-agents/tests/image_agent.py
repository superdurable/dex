# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""An OpenAI Agents SDK agent with a media store, for the image tests.

The agent runs the scripted model of ``tests.scripted_model``, which records
every request's image parts in ``model-requests.log``. ``take_screenshot``
comes from a tool source: it uploads the conformance JPEG to the shared
``LocalDirectoryMediaStore`` and returns its reference. ``lookup`` is a function
tool that may wait at a ``FileGate``. The run config's own
``call_model_input_filter`` records how many image parts it saw and whether
any held bytes.
"""

from __future__ import annotations

import base64
import json
import os
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from agents import Agent, RunConfig, function_tool
from agents.run_config import CallModelData, ModelInputData

from dex_agents import (
    AgentSessionFlow,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    LocalDirectoryMediaStore,
    MediaStore,
    ToolEffect,
    ToolExecutionResult,
    ToolPolicy,
    ToolTaskPoll,
    ToolTaskPollOutcome,
)
from dex_agents.testing import FileGate, InvocationLog
from dex_agents_openai_agents import openai_agents_session_flow
from tests.scripted_model import CommandScriptedModel

IMAGE_AGENT_FLOW_TYPE = "OpenAIAgentsImageAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "OPENAI_AGENTS_IMAGE_TEST_STATE_DIR"
INVOCATION_LOG_NAME = "invocations.log"
MODEL_REQUEST_LOG_NAME = "model-requests.log"
INPUT_FILTER_LOG_NAME = "input-filter.log"
MEDIA_DIRECTORY_NAME = "media"
IMAGE_TOOL_POLICIES = {
    "take_screenshot": ToolPolicy(ToolEffect.READ),
    "lookup": ToolPolicy(ToolEffect.READ),
}
_CONFORMANCE_IMAGES: dict[str, Any] = {
    case["name"]: case
    for case in json.loads(
        (Path(__file__).resolve().parents[3] / "conformance" / "images.json").read_text()
    )["images"]
}
USER_PNG = base64.b64decode(_CONFORMANCE_IMAGES["png"]["base64"])
SCREENSHOT_JPEG = base64.b64decode(_CONFORMANCE_IMAGES["jpeg"]["base64"])


@dataclass(frozen=True)
class _ToolDefinition:
    name: str
    description: str
    input_schema: Mapping[str, Any]


_SCREENSHOT_TOOL = _ToolDefinition(
    name="take_screenshot",
    description="Take a screenshot of the screen.",
    input_schema={"type": "object", "properties": {}},
)


class ScreenshotToolSource:
    """A tool source whose one tool uploads an image and returns its reference."""

    def __init__(self, media_store: MediaStore, invocations: InvocationLog) -> None:
        self._media_store = media_store
        self._invocations = invocations

    @property
    def source_name(self) -> str:
        return "screenshots"

    async def list_tools(self) -> Sequence[_ToolDefinition]:
        return [_SCREENSHOT_TOOL]

    def cached_tools(self) -> Sequence[_ToolDefinition]:
        return [_SCREENSHOT_TOOL]

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
            policy=turn.resolve_tool_policy(tool_name),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        self._invocations.record("take_screenshot")
        reference = await self._media_store.put_image(SCREENSHOT_JPEG)
        return ToolExecutionResult(output="took a screenshot", images=[reference])

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        raise LookupError("take_screenshot starts no tool tasks")


def build_media_store(state_dir: Path) -> LocalDirectoryMediaStore:
    return LocalDirectoryMediaStore(state_dir / MEDIA_DIRECTORY_NAME)


def build_image_agent_flow(state_dir: Path) -> AgentSessionFlow:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)
    input_filter_log = InvocationLog(state_dir / INPUT_FILTER_LOG_NAME)
    model = CommandScriptedModel(
        state_dir, invocations, InvocationLog(state_dir / MODEL_REQUEST_LOG_NAME)
    )
    media_store = build_media_store(state_dir)

    @function_tool
    async def lookup(query: str, gate: str = "") -> str:
        """Search the knowledge base."""
        invocations.record("lookup")
        if gate:
            file_gate = FileGate(state_dir, gate)
            file_gate.mark_reached()
            await file_gate.wait_for_release()
        return f"found {query}"

    def record_model_input(call: CallModelData[Any]) -> ModelInputData:
        image_urls = [
            str(part.get("image_url"))
            for item in call.model_data.input
            if isinstance(item, Mapping)
            for part in _list_parts(item)
            if part.get("type") == "input_image"
        ]
        has_bytes = any(url.startswith("data:") for url in image_urls)
        input_filter_log.record(f"images {len(image_urls)} bytes {has_bytes}")
        return call.model_data

    agent = Agent(name="Assistant", instructions="Help the user.", model=model, tools=[lookup])
    return openai_agents_session_flow(
        agent,
        flow_type=IMAGE_AGENT_FLOW_TYPE,
        tool_policies=IMAGE_TOOL_POLICIES,
        run_config=RunConfig(tracing_disabled=True, call_model_input_filter=record_model_input),
        tool_sources=[ScreenshotToolSource(media_store, invocations)],
        media_store=media_store,
    )


def build_image_agent_flow_from_environment() -> AgentSessionFlow:
    return build_image_agent_flow(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


def _list_parts(item: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    parts = item.get("content") if "content" in item else item.get("output")
    return [part for part in parts if isinstance(part, Mapping)] if isinstance(parts, list) else []
