# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Stepwise agent with plan mode and a scripted model, driven by the user text.

The model reads the current turn's user text:

* ``plan <task> | <task> ...`` drafts those tasks with ``write_todos``, then
  answers ``drafted <n> tasks``. Send it with ``plan_mode``.
* ``plan-sneak <task>`` calls ``lookup_order`` next to ``write_todos``.
* ``revise <task> | ...`` writes those tasks as a new draft.
* ``discuss`` answers with the number of tasks the plan note shows.
* ``todos <json>`` calls ``write_todos`` with that ``todos`` list;
  ``todos-twice <json>`` calls it twice in one response; ``todos-raw <json>``
  sends the raw arguments.
* ``stall <gate>`` waits at a ``FileGate``, then answers.
* The approved plan's message makes the model look up the first task, then
  answer, or, when a task mentions ``finish``, mark every task completed first.
  A first task ``stall <gate>`` waits at that gate before the lookup.

Every model call records its request as JSON in ``model-requests.log``.
``PLAN_AGENT_PLANNING_TOOL_CHOICE`` sets ``planning_tool_choice``. A session
whose settings set ``"refuses_forced_tool_choice"`` resolves a model client that
reports it cannot force a tool choice.
``PLAN_AGENT_USER_MESSAGE_GATE`` set to ``<block|fail>:<text prefix>`` makes
``build_user_message`` wait at the ``user-message`` gate, or fail, for user
text with that prefix until the gate is released.

After its tool results arrive, the model answers ``<tool_name>: <output>``.
"""

from __future__ import annotations

import json
import os
import time
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from dex_agents import (
    AgentSessionFlow,
    BuiltInTool,
    FrameworkMessage,
    ToolEffect,
)
from dex_agents.builtin_loop import (
    AgentDefinition,
    BuiltinLoopAgentRunner,
    ModelClient,
    ModelRequest,
    NamedToolChoice,
    PlanningToolChoice,
    builtin_agent_session_options,
    function_tool,
)
from dex_agents.testing import (
    FileGate,
    InvocationLog,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
)

PLAN_AGENT_FLOW_TYPE = "StepwisePlanAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "PLAN_AGENT_STATE_DIR"
USER_MESSAGE_GATE_ENVIRONMENT_VARIABLE = "PLAN_AGENT_USER_MESSAGE_GATE"
PLANNING_TOOL_CHOICE_ENVIRONMENT_VARIABLE = "PLAN_AGENT_PLANNING_TOOL_CHOICE"
USER_MESSAGE_GATE_NAME = "user-message"
INVOCATION_LOG_NAME = "invocations.log"
MODEL_REQUEST_LOG_NAME = "model-requests.log"
APPROVED_PLAN_PREFIX = "[Dex] The user approved plan revision"
PLAN_NOTE_PREFIX = "[Dex] The current plan"


class GatedPlanRunner(BuiltinLoopAgentRunner):
    """Waits at, or fails before, the user message whose text has a prefix."""

    def __init__(self, definition: AgentDefinition, state_dir: Path, gate: str | None) -> None:
        super().__init__(definition)
        self._state_dir = state_dir
        self._gate_mode, _, self._gated_prefix = (gate or "").partition(":")

    def build_user_message(self, text: str) -> FrameworkMessage:
        if self._gate_mode and text.startswith(self._gated_prefix):
            gate = FileGate(self._state_dir, USER_MESSAGE_GATE_NAME)
            if not gate.is_released():
                gate.mark_reached()
                if self._gate_mode == "fail":
                    raise RuntimeError("scripted build_user_message failure")
                # The test kills this Worker here; nothing else needs its event loop.
                while not gate.is_released():
                    time.sleep(0.05)
        return super().build_user_message(text)


def build_plan_agent_flow(
    state_dir: Path,
    user_message_gate: str | None = None,
    planning_tool_choice: PlanningToolChoice = "auto",
) -> AgentSessionFlow:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)
    model_requests = InvocationLog(state_dir / MODEL_REQUEST_LOG_NAME)

    async def wait_at_gate(gate_name: str) -> None:
        gate = FileGate(state_dir, gate_name)
        gate.mark_reached()
        await gate.wait_for_release()

    @function_tool(effect=ToolEffect.READ)
    def lookup_order(query: str) -> str:
        """Look up an order."""
        invocations.record("lookup_order")
        return f"found {query}"

    @function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE)
    def publish_note(message: str) -> str:
        """Publish a note."""
        invocations.record("publish_note")
        return f"published {message}"

    async def respond(request: ModelRequest) -> ScriptedModelReply:
        user_texts = ScriptedModelClient.user_texts(request)
        command = user_texts[-1]
        results = ScriptedModelClient.tool_results(request)
        model_requests.record(json.dumps(_describe_request(request, user_texts)))
        verb, _, rest = command.partition(" ")
        if command.startswith(APPROVED_PLAN_PREFIX):
            tasks = _plan_note_tasks(user_texts)
            if not results and tasks[0].startswith("stall "):
                await wait_at_gate(tasks[0].removeprefix("stall "))
            return _execute_plan(tasks, results)
        if results:
            if verb == "plan":
                return ScriptedModelReply(text=f"drafted {len(_split_tasks(rest))} tasks")
            return ScriptedModelReply(
                text="; ".join(f"{result.tool_name}: {result.output}" for result in results)
            )
        if verb in ("plan", "revise"):
            return _write_todos(
                [{"content": task, "status": "pending"} for task in _split_tasks(rest)]
            )
        if verb == "plan-sneak":
            return ScriptedModelReply(
                tool_calls=[
                    ScriptedToolCall(tool_name="lookup_order", arguments={"query": rest}),
                    _write_todos_call([{"content": rest, "status": "pending"}]),
                ]
            )
        if verb == "discuss":
            return ScriptedModelReply(
                text=f"the plan has {len(_plan_note_tasks(user_texts))} tasks"
            )
        if verb == "todos":
            return _write_todos(json.loads(rest))
        if verb == "todos-twice":
            todos = json.loads(rest)
            return ScriptedModelReply(
                tool_calls=[_write_todos_call(todos), _write_todos_call(todos)]
            )
        if verb == "todos-raw":
            return ScriptedModelReply(
                tool_calls=[ScriptedToolCall(tool_name=BuiltInTool.WRITE_TODOS, arguments=rest)]
            )
        if verb == "stall":
            await wait_at_gate(rest)
            return ScriptedModelReply(text=f"stalled at {rest}")
        return ScriptedModelReply(text=f"said: {command}")

    model = ScriptedModelClient(respond)
    refusing_model = _RefusesForcedToolChoiceClient(model)

    def resolve_model(session_settings: Mapping[str, Any]) -> ModelClient:
        return refusing_model if session_settings.get("refuses_forced_tool_choice") else model

    definition = AgentDefinition(
        name="plan-test",
        instructions="You are a scripted planning agent.",
        model=model,
        resolve_model=resolve_model,
        tools=[lookup_order, publish_note],
        built_in_tools=frozenset({BuiltInTool.ASK_USER, BuiltInTool.WRITE_TODOS}),
        plan_mode_enabled=True,
        planning_tool_choice=planning_tool_choice,
    )
    return AgentSessionFlow(
        GatedPlanRunner(definition, state_dir, user_message_gate),
        flow_type=PLAN_AGENT_FLOW_TYPE,
        options=builtin_agent_session_options(definition),
    )


def build_plan_agent_flow_from_environment() -> AgentSessionFlow:
    planning_tool_choice = os.environ.get(PLANNING_TOOL_CHOICE_ENVIRONMENT_VARIABLE, "auto")
    return build_plan_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        os.environ.get(USER_MESSAGE_GATE_ENVIRONMENT_VARIABLE),
        "required" if planning_tool_choice == "required" else "auto",
    )


def read_model_requests(state_dir: Path) -> list[dict[str, Any]]:
    """Return every logged model request, oldest first."""
    return [json.loads(line) for line in InvocationLog(state_dir / MODEL_REQUEST_LOG_NAME).names()]


def _describe_request(request: ModelRequest, user_texts: list[str]) -> dict[str, Any]:
    tool_choice = request.tool_choice
    return {
        "flow_id": request.metadata.flow_id,
        "turn_id": request.metadata.turn_id,
        "tools": [tool.name for tool in request.tools],
        "tool_choice": tool_choice.tool_name
        if isinstance(tool_choice, NamedToolChoice)
        else tool_choice,
        "user_texts": user_texts,
    }


def _execute_plan(tasks: list[str], results: list[Any]) -> ScriptedModelReply:
    if not results:
        return ScriptedModelReply(
            tool_calls=[ScriptedToolCall(tool_name="lookup_order", arguments={"query": tasks[0]})]
        )
    if any("finish" in task for task in tasks) and all(
        result.tool_name != BuiltInTool.WRITE_TODOS for result in results
    ):
        return _write_todos([{"content": task, "status": "completed"} for task in tasks])
    return ScriptedModelReply(text=f"executed {len(tasks)} tasks")


def _plan_note_tasks(user_texts: list[str]) -> list[str]:
    note = next((text for text in user_texts if text.startswith(PLAN_NOTE_PREFIX)), "")
    return [line.split("] ", 1)[1] for line in note.splitlines() if line.startswith("- [")]


def _split_tasks(text: str) -> list[str]:
    return [task.strip() for task in text.split("|") if task.strip()]


def _write_todos(todos: list[dict[str, str]]) -> ScriptedModelReply:
    return ScriptedModelReply(tool_calls=[_write_todos_call(todos)])


def _write_todos_call(todos: list[dict[str, str]]) -> ScriptedToolCall:
    return ScriptedToolCall(tool_name=BuiltInTool.WRITE_TODOS, arguments={"todos": todos})


class _RefusesForcedToolChoiceClient:
    """Delegates to a scripted client but reports that its model cannot force a tool choice."""

    def __init__(self, scripted: ScriptedModelClient) -> None:
        self._scripted = scripted

    async def call_model(self, request: ModelRequest, output: Any) -> Any:
        return await self._scripted.call_model(request, output)

    def estimate_input_tokens(self, request: ModelRequest) -> int:
        return self._scripted.estimate_input_tokens(request)

    def supports_forced_tool_choice(self, settings: object) -> bool:
        return False
