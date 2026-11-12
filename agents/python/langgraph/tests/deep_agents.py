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
* ``pad_topic`` sets ``topic`` to as many characters as it is told.
* ``get_topic`` reads ``topic`` through its ``ToolRuntime``.
* ``add_visit`` returns a ``Command`` that appends its call ID to the ``visits`` list.
* ``flaky_visit`` sends the same append through the task's send, and its first attempt fails.
* ``echo_text`` (read) returns its text unchanged.
* ``wait_at_gate`` (read) waits at the gate it names.

``task`` is excluded. A middleware records each model request's model and tool names, and
each tool call its tool wrapper sees.
The ``read_file`` agent also has a tool source whose ``read_file`` replaces Deep Agents'.
The evicting agent's ``FilesystemMiddleware`` evicts every tool result over a small limit.
Two more Flows share one compiled graph whose recorder comes before the durable middleware,
one with the deferral first and one in middleware order.
"""

from __future__ import annotations

import json
import operator
import os
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Annotated, Any, NotRequired

from deepagents import create_deep_agent
from deepagents.graph import DeepAgentState
from deepagents.middleware import FilesystemMiddleware
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
from langgraph._internal._constants import CONFIG_KEY_SEND
from langgraph.config import get_config
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
SHARED_GRAPH_DEFERRED_FIRST_FLOW_TYPE = "SharedGraphDeferredFirstDeepAgent"
SHARED_GRAPH_MIDDLEWARE_ORDER_FLOW_TYPE = "SharedGraphMiddlewareOrderDeepAgent"
EVICTING_DEEP_AGENT_FLOW_TYPE = "EvictingDeepAgent"
#: Tokens above which the evicting agent writes a tool result to a file instead.
EVICTION_TOKEN_LIMIT = 50
MODEL_REQUESTS_LOG_NAME = "model-requests.jsonl"
#: The model the session settings may pick; the graph's own model is the scripted default.
TURN_MODEL_NAME = "scripted-turn-model"
SOURCE_READ_FILE_OUTPUT = "read from the workspace"
TOOL_WRAPPER_INVOCATION_PREFIX = "tool-wrapper:"
DEEP_AGENT_TOOL_POLICIES = {
    "write_file": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "read_file": ToolPolicy(ToolEffect.READ),
    "ls": ToolPolicy(ToolEffect.READ),
    "set_topic": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=False),
    "pad_topic": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=False),
    "get_topic": ToolPolicy(ToolEffect.READ),
    "add_visit": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "flaky_visit": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=False),
    "echo_text": ToolPolicy(ToolEffect.READ),
    "wait_at_gate": ToolPolicy(ToolEffect.READ),
}


class TopicState(DeepAgentState):
    """Deep Agents' state with ``topic`` and the ``visits`` list that each write appends to."""

    topic: NotRequired[str]
    visits: Annotated[NotRequired[list[str]], operator.add]


@dataclass(frozen=True)
class RecordedModelRequest:
    model: str
    tool_names: list[str]
    system: str
    messages: list[list[str | None]]
    state: dict[str, str]


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

    shared_graph = build_recorder_first_deep_agent(state_dir)
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
        langchain_agent_session_flow(
            shared_graph,
            flow_type=SHARED_GRAPH_DEFERRED_FIRST_FLOW_TYPE,
            tool_policies=DEEP_AGENT_TOOL_POLICIES,
            should_defer_tool_calls_before_middleware=True,
        ),
        langchain_agent_session_flow(
            shared_graph,
            flow_type=SHARED_GRAPH_MIDDLEWARE_ORDER_FLOW_TYPE,
            tool_policies=DEEP_AGENT_TOOL_POLICIES,
        ),
        langchain_agent_session_flow(
            _deep_agent_factory(
                state_dir,
                FilesystemMiddleware(tool_token_limit_before_evict=EVICTION_TOKEN_LIMIT),
            ),
            flow_type=EVICTING_DEEP_AGENT_FLOW_TYPE,
            tool_policies=DEEP_AGENT_TOOL_POLICIES,
            excluded_tool_names=["task"],
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


def build_recorder_first_deep_agent(state_dir: Path) -> CompiledGraph:
    """Build a Deep Agents graph whose recorder's tool wrapper is outside the durable one."""
    return create_deep_agent(
        _scripted_model(state_dir),
        tools=_build_topic_tools(state_dir),
        middleware=[_ModelRequestRecorder(state_dir), DurableToolCallMiddleware()],
        state_schema=TopicState,
    )


def _deep_agent_factory(
    state_dir: Path, *extra_middleware: AgentMiddleware[Any, Any, Any]
) -> Callable[[], Awaitable[CompiledGraph]]:
    async def build_deep_agent() -> CompiledGraph:
        return create_deep_agent(
            _scripted_model(state_dir),
            tools=_build_topic_tools(state_dir),
            middleware=[
                DurableToolCallMiddleware(),
                _ModelRequestRecorder(state_dir),
                *extra_middleware,
            ],
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
    def pad_topic(characters: int, runtime: ToolRuntime) -> Command[Any]:
        """Set the topic to a run of characters."""
        invocations.record("pad_topic")
        return Command(
            update={
                "topic": "x" * characters,
                "messages": [ToolMessage("topic padded", tool_call_id=runtime.tool_call_id)],
            }
        )

    @tool
    def add_visit(runtime: ToolRuntime) -> Command[Any]:
        """Record a visit."""
        invocations.record("add_visit")
        return Command(
            update={
                "visits": [str(runtime.tool_call_id)],
                "messages": [ToolMessage("visited", tool_call_id=runtime.tool_call_id)],
            }
        )

    @tool
    def flaky_visit(runtime: ToolRuntime) -> str:
        """Record a visit; the first attempt fails after its write."""
        invocations.record("flaky_visit")
        send = get_config()["configurable"][CONFIG_KEY_SEND]
        send([("visits", [str(runtime.tool_call_id)])])
        first_attempt_marker = state_dir / f"flaky-{runtime.tool_call_id}"
        if not first_attempt_marker.exists():
            first_attempt_marker.write_text("failed once")
            raise RuntimeError("the first attempt fails after its write")
        return "visited after a retry"

    @tool
    def echo_text(text: str) -> str:
        """Return the text unchanged."""
        invocations.record("echo_text")
        return text

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

    return [set_topic, pad_topic, get_topic, add_visit, flaky_visit, echo_text, wait_at_gate]


def _scripted_model(state_dir: Path, model: str | None = None) -> ScriptedChatModel:
    log_path = str(state_dir / INVOCATION_LOG_NAME)
    if model is None:
        return ScriptedChatModel(invocation_log_path=log_path)
    return ScriptedChatModel(invocation_log_path=log_path, model=model)


class _ModelRequestRecorder(AgentMiddleware[Any, Any, Any]):
    """Record every model request it sees: model, tools, system message, messages, and state."""

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
            "system": "" if request.system_message is None else request.system_message.text,
            "messages": [
                [
                    type(message).__name__,
                    message.id,
                    message.text,
                    json.dumps(getattr(message, "tool_calls", []), sort_keys=True, default=str),
                ]
                for message in request.messages
            ],
            "state": {
                key: json.dumps(request.state.get(key), sort_keys=True, default=str)
                for key in ("topic", "visits", "files")
            },
        }
        with self._path.open("a") as log:
            log.write(json.dumps(recorded) + "\n")
        return await handler(request)

    async def awrap_tool_call(
        self,
        request: ToolCallRequest,
        handler: Callable[[ToolCallRequest], Awaitable[ToolMessage | Command[Any]]],
    ) -> ToolMessage | Command[Any]:
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
