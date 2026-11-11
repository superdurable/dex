# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deep Agents agents under test: ``create_deep_agent`` graphs with the scripted chat model.

Each agent keeps Deep Agents' default stack, with its ``DeltaChannel`` state and its
file tools on the in-state ``StateBackend``, and adds these tools:

* ``set_topic`` returns a ``Command`` that sets the ``topic`` state key.
* ``get_topic`` reads ``topic`` through its ``ToolRuntime``.
* ``wait_at_gate`` (read) waits at the gate it names.

``task`` is excluded. A middleware records each model request's model and tool names.
The ``read_file`` agent also has a tool source whose ``read_file`` replaces Deep Agents'.
"""

from __future__ import annotations

import json
import os
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, NotRequired

from deepagents import create_deep_agent
from deepagents.graph import DeepAgentState
from dex_agents import (
    AgentSessionFlow,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    ToolEffect,
    ToolExecutionResult,
    ToolPolicy,
)
from dex_agents.builtin_loop import SourceToolDefinition
from dex_agents.testing import FileGate, InvocationLog
from langchain.agents.middleware import AgentMiddleware, ModelRequest, ModelResponse
from langchain.tools import ToolRuntime
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import ToolMessage
from langchain_core.tools import BaseTool, tool
from langgraph.prebuilt.tool_node import ToolCallRequest
from langgraph.types import Command

from dex_agents_langgraph import (
    CompiledGraph,
    DurableToolCallMiddleware,
    langchain_agent_session_flow,
)
from tests.scripted_agents import INVOCATION_LOG_NAME, STATE_DIR_ENVIRONMENT_VARIABLE
from tests.scripted_model import ScriptedChatModel

DEEP_AGENT_FLOW_TYPE = "ScriptedDeepAgent"
SOURCE_READ_FILE_DEEP_AGENT_FLOW_TYPE = "SourceReadFileDeepAgent"
MODEL_REQUESTS_LOG_NAME = "model-requests.jsonl"
#: The model the session settings may pick; the graph's own model is the scripted default.
TURN_MODEL_NAME = "scripted-turn-model"
SOURCE_READ_FILE_OUTPUT = "read from the workspace"
TOOL_WRAPPER_INVOCATION_PREFIX = "tool-wrapper:"
DEEP_AGENT_TOOL_POLICIES = {
    "write_file": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "read_file": ToolPolicy(ToolEffect.READ),
    "set_topic": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=False),
    "get_topic": ToolPolicy(ToolEffect.READ),
    "wait_at_gate": ToolPolicy(ToolEffect.READ),
}


class TopicState(DeepAgentState):
    """Deep Agents' state with the ``topic`` key that ``set_topic`` writes."""

    topic: NotRequired[str]


@dataclass(frozen=True)
class RecordedModelRequest:
    model: str
    tool_names: list[str]


def build_deep_agent_flows(state_dir: Path) -> list[AgentSessionFlow]:
    """Build the Deep Agents session Flows; every Worker builds the same ones."""
    turn_models: dict[str, BaseChatModel] = {}

    def select_turn_chat_model(turn: AgentTurnContext) -> BaseChatModel | None:
        model_name = turn.session_settings.get("model")
        if model_name is None:
            return None
        if model_name not in turn_models:
            turn_models[model_name] = _scripted_model(state_dir, model=str(model_name))
        return turn_models[model_name]

    return [
        langchain_agent_session_flow(
            _deep_agent_factory(state_dir),
            flow_type=DEEP_AGENT_FLOW_TYPE,
            tool_policies=DEEP_AGENT_TOOL_POLICIES,
            excluded_tool_names=["task"],
            select_turn_chat_model=select_turn_chat_model,
            should_defer_tool_calls_before_middleware=True,
        ),
        langchain_agent_session_flow(
            _deep_agent_factory(state_dir),
            flow_type=SOURCE_READ_FILE_DEEP_AGENT_FLOW_TYPE,
            tool_policies=DEEP_AGENT_TOOL_POLICIES,
            tool_sources=[WorkspaceReadFileSource()],
            excluded_tool_names=["task", "read_file"],
            should_defer_tool_calls_before_middleware=True,
        ),
    ]


def build_deep_agent_flows_from_environment() -> list[AgentSessionFlow]:
    return build_deep_agent_flows(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


def read_recorded_model_requests(state_dir: Path) -> list[RecordedModelRequest]:
    path = state_dir / MODEL_REQUESTS_LOG_NAME
    if not path.exists():
        return []
    return [
        RecordedModelRequest(**json.loads(line)) for line in path.read_text().splitlines() if line
    ]


def _deep_agent_factory(state_dir: Path) -> Callable[[], Awaitable[CompiledGraph]]:
    async def build_deep_agent() -> CompiledGraph:
        return create_deep_agent(
            _scripted_model(state_dir),
            tools=_build_topic_tools(state_dir),
            middleware=[DurableToolCallMiddleware(), _ModelRequestRecorder(state_dir)],
            state_schema=TopicState,
        )

    return build_deep_agent


def _build_topic_tools(state_dir: Path) -> list[BaseTool]:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    @tool
    def set_topic(topic: str, runtime: ToolRuntime) -> Command[Any]:
        """Remember the conversation's topic."""
        invocations.record("set_topic")
        return Command(
            update={
                "topic": topic,
                "messages": [
                    ToolMessage(f"topic set to {topic}", tool_call_id=runtime.tool_call_id)
                ],
            }
        )

    @tool
    def get_topic(runtime: ToolRuntime) -> str:
        """Return the conversation's topic."""
        invocations.record("get_topic")
        return f"topic is {runtime.state.get('topic', 'unset')}"

    @tool
    async def wait_at_gate(gate: str) -> str:
        """Wait until the test releases a gate."""
        invocations.record("wait_at_gate")
        file_gate = FileGate(state_dir, gate)
        file_gate.mark_reached()
        await file_gate.wait_for_release()
        return f"passed {gate}"

    return [set_topic, get_topic, wait_at_gate]


def _scripted_model(state_dir: Path, model: str | None = None) -> ScriptedChatModel:
    log_path = str(state_dir / INVOCATION_LOG_NAME)
    if model is None:
        return ScriptedChatModel(invocation_log_path=log_path)
    return ScriptedChatModel(invocation_log_path=log_path, model=model)


class _ModelRequestRecorder(AgentMiddleware[Any, Any, Any]):
    """Record the model and the tool names of every model request it sees."""

    def __init__(self, state_dir: Path) -> None:
        super().__init__()
        self._path = state_dir / MODEL_REQUESTS_LOG_NAME
        self._invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    async def awrap_model_call(
        self,
        request: ModelRequest[Any],
        handler: Callable[[ModelRequest[Any]], Awaitable[ModelResponse[Any]]],
    ) -> ModelResponse[Any]:
        model = request.model
        recorded = {
            "model": getattr(model, "model", type(model).__name__),
            "tool_names": [_tool_name(tool) for tool in request.tools],
        }
        with self._path.open("a") as log:
            log.write(json.dumps(recorded) + "\n")
        return await handler(request)

    async def awrap_tool_call(
        self,
        request: ToolCallRequest,
        handler: Callable[[ToolCallRequest], Awaitable[ToolMessage | Command[Any]]],
    ) -> ToolMessage | Command[Any]:
        # The deferral runs first in a session, so this records nothing there.
        self._invocations.record(f"{TOOL_WRAPPER_INVOCATION_PREFIX}{request.tool_call['name']}")
        return await handler(request)


def _tool_name(tool: BaseTool | Mapping[str, Any]) -> str:
    return tool.name if isinstance(tool, BaseTool) else str(tool.get("name"))


@dataclass(frozen=True)
class _ListedTool:
    name: str
    description: str
    input_schema: Mapping[str, Any]


class WorkspaceReadFileSource:
    """A tool source whose ``read_file`` has the name of Deep Agents' own file tool."""

    _READ_FILE = _ListedTool(
        name="read_file",
        description="Read a file of the workspace.",
        input_schema={
            "type": "object",
            "properties": {"file_path": {"type": "string"}},
            "required": ["file_path"],
        },
    )

    @property
    def source_name(self) -> str:
        return "workspace"

    def declared_tool_names(self) -> Sequence[str]:
        return [self._READ_FILE.name]

    async def list_tools(self) -> Sequence[SourceToolDefinition]:
        return [self._READ_FILE]

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=turn.resolve_tool_policy(tool_name, ToolEffect.READ),
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        file_path = json.loads(call.tool_call.arguments)["file_path"]
        return ToolExecutionResult(output=f"{SOURCE_READ_FILE_OUTPUT}: {file_path}")

    async def poll_tool_task(self, poll: Any) -> Any:
        raise NotImplementedError("the workspace source starts no tool tasks")
