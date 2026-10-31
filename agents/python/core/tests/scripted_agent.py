# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted framework runner that stands in for a real agent framework.

The scripted model reads commands from the user message:

* ``say <text>`` answers ``said: <text>``.
* ``recall`` answers with every earlier user message, proving history replay.
* ``call <tool> <json>`` asks for one tool call; join several with `` && ``.
* ``call-then-fail <tool> <json>`` does the same, then fails on resume.
* ``stall <gate>`` runs ``lookup`` inline, then waits at a ``FileGate``.
* ``fail`` raises on every attempt.
"""

from __future__ import annotations

import asyncio
import json
import os
from dataclasses import asdict, dataclass
from datetime import timedelta
from pathlib import Path
from typing import Any

from dex_agents import (
    AgentFrameworkRunner,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    DeferredToolCall,
    DeferredToolExecution,
    FrameworkMessage,
    MessageRole,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
    TurnSuspended,
    describe_tool_call_outcome,
)
from dex_agents.testing import FileGate, InvocationLog

SCRIPTED_AGENT_FLOW_TYPE = "ScriptedAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_STATE_DIR"
INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_INTERRUPT_DEADLINE_SECONDS"
SLOW_WRITE_GATE = "slow_write"
GATED_PUBLISH_GATE = "gated_publish"
INVOCATION_LOG_NAME = "invocations.log"
IDEMPOTENCY_KEY_LOG_NAME = "idempotency-keys.log"
MODEL_CALL_USAGE = AgentUsage(model_calls=1, input_tokens=10, output_tokens=5)
TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "publish": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    "charge": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "slow_write": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "gated_publish": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
}


@dataclass(frozen=True)
class ScriptedToolCall:
    tool_call_id: str
    tool_name: str
    arguments: str
    inline_result: str | None


class ScriptedRunner(AgentFrameworkRunner):
    def __init__(self, state_dir: Path) -> None:
        self._state_dir = state_dir
        self._invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)
        self._idempotency_keys = InvocationLog(state_dir / IDEMPOTENCY_KEY_LOG_NAME)

    def build_user_message(self, text: str) -> FrameworkMessage:
        return FrameworkMessage(
            role=MessageRole.USER,
            text=text,
            framework_item=json.dumps({"type": "user", "text": text}),
        )

    async def run_turn(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        self._invocations.record(f"run_turn:{turn.turn_id}")
        if turn.framework_run_state is not None:
            return self._resume_turn(turn, turn.framework_run_state)
        command = _current_user_text(turn)
        if command.startswith("say "):
            return _answer(command, f"said: {command.removeprefix('say ')}")
        if command == "recall":
            earlier = [
                json.loads(message.framework_item)["text"]
                for message in turn.messages
                if message.role is MessageRole.USER and message.turn_id != turn.turn_id
            ]
            return _answer(command, "history: " + " | ".join(earlier))
        if command.startswith(("call ", "call-then-fail ")):
            return await self._start_tool_calls(turn, command)
        if command.startswith("stall "):
            return await self._stall(turn, command.removeprefix("stall "))
        if command == "fail":
            raise RuntimeError("scripted failure")
        raise ValueError(f"unknown scripted command {command!r}")

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        record = call.tool_call
        self._invocations.record(record.tool_name)
        arguments = json.loads(record.arguments)
        if record.tool_name == "publish":
            await asyncio.to_thread(
                call.events.write_text_delta, f"publishing {arguments['message']}"
            )
            return ToolExecutionResult(output=f"published {arguments['message']}")
        if record.tool_name == "gated_publish":
            gate = FileGate(self._state_dir, GATED_PUBLISH_GATE)
            gate.mark_reached()
            await gate.wait_for_release()
            return ToolExecutionResult(output=f"published {arguments['message']}")
        if record.tool_name == "charge":
            self._idempotency_keys.record(f"{call.idempotency_key} attempt {call.attempt}")
            if "gate" in arguments:
                gate = FileGate(self._state_dir, str(arguments["gate"]))
                gate.mark_reached()
                await gate.wait_for_release()
            if call.attempt <= int(arguments.get("fail_attempts", 0)):
                raise RuntimeError(f"charge attempt {call.attempt} lost its connection")
            if int(arguments["amount"]) <= 0:
                raise ToolExecutionError("amount must be positive")
            return ToolExecutionResult(
                output=f"charged {arguments['amount']} with key {call.idempotency_key}"
            )
        if record.tool_name == "slow_write":
            gate = FileGate(self._state_dir, SLOW_WRITE_GATE)
            gate.mark_reached()
            await gate.wait_for_release()
            return ToolExecutionResult(output=f"wrote {arguments['message']}")
        raise ToolExecutionError(f"unknown tool {record.tool_name}")

    async def _start_tool_calls(self, turn: AgentTurnContext, command: str) -> AgentTurnOutcome:
        calls: list[ScriptedToolCall] = []
        deferred: list[DeferredToolCall] = []
        requests = command.split(" ", 1)[1]
        for index, request in enumerate(requests.split(" && call ")):
            tool_name, arguments = request.removeprefix("call ").split(" ", 1)
            tool_call_id = f"{turn.turn_id}-call-{index}"
            policy = turn.resolve_tool_policy(tool_name)
            inline_result = None
            if not policy.should_defer:
                inline_result = self._run_read_tool(turn, tool_call_id, tool_name, arguments)
            else:
                deferred.append(
                    DeferredToolCall(
                        tool_call_id=tool_call_id,
                        tool_name=tool_name,
                        arguments=arguments,
                        policy=policy,
                    )
                )
            calls.append(ScriptedToolCall(tool_call_id, tool_name, arguments, inline_result))
        if not deferred:
            usage = MODEL_CALL_USAGE.combined_with(MODEL_CALL_USAGE)
            return _complete_tool_turn(command, calls, {}, usage)
        return TurnSuspended(
            framework_run_state=json.dumps(
                {"command": command, "calls": [asdict(call) for call in calls]}
            ),
            deferred_tool_calls=deferred,
            usage=MODEL_CALL_USAGE,
        )

    def _resume_turn(self, turn: AgentTurnContext, framework_run_state: str) -> AgentTurnOutcome:
        state = json.loads(framework_run_state)
        if state["command"].startswith("call-then-fail "):
            raise RuntimeError("scripted failure after the tool calls ran")
        calls = [ScriptedToolCall(**call) for call in state["calls"]]
        results: dict[str, str] = {}
        for call in calls:
            if call.inline_result is not None:
                continue
            record = turn.recorded_tool_call(call.tool_call_id)
            if record is None:
                raise RuntimeError(f"no record for deferred call {call.tool_call_id}")
            results[call.tool_call_id] = describe_tool_call_outcome(record)
        return _complete_tool_turn(state["command"], calls, results, MODEL_CALL_USAGE)

    async def _stall(self, turn: AgentTurnContext, gate_name: str) -> AgentTurnOutcome:
        found = self._run_read_tool(turn, f"{turn.turn_id}-lookup", "lookup", '{"query": "stall"}')
        turn.events.write_text_delta(f"thinking after {found}")
        gate = FileGate(self._state_dir, gate_name)
        gate.mark_reached()
        await gate.wait_for_release()
        return _answer(f"stall {gate_name}", f"stalled at {gate_name}")

    def _run_read_tool(
        self,
        turn: AgentTurnContext,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
    ) -> str:
        if tool_name != "lookup":
            raise ValueError(f"{tool_name} is not a READ tool")
        turn.events.write_tool_call_started(tool_call_id, tool_name)
        self._invocations.record(tool_name)
        return f"found {json.loads(arguments)['query']}"


def build_scripted_agent_flow(
    state_dir: Path,
    interrupt_deadline: timedelta | None = None,
) -> AgentSessionFlow:
    return AgentSessionFlow(
        ScriptedRunner(state_dir),
        flow_type=SCRIPTED_AGENT_FLOW_TYPE,
        options=AgentSessionOptions(
            tool_policies=TOOL_POLICIES, interrupt_deadline=interrupt_deadline
        ),
    )


def build_scripted_agent_flow_from_environment() -> AgentSessionFlow:
    deadline_seconds = os.environ.get(INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE)
    return build_scripted_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        None if deadline_seconds is None else timedelta(seconds=float(deadline_seconds)),
    )


def _current_user_text(turn: AgentTurnContext) -> str:
    for message in reversed(turn.messages):
        if message.role is MessageRole.USER and message.turn_id == turn.turn_id:
            text: str = json.loads(message.framework_item)["text"]
            return text
    raise RuntimeError(f"turn {turn.turn_id} has no user message")


def _answer(command: str, text: str) -> TurnCompleted:
    return TurnCompleted(new_messages=[_assistant_text(command, text)], usage=MODEL_CALL_USAGE)


def _complete_tool_turn(
    command: str,
    calls: list[ScriptedToolCall],
    deferred_results: dict[str, str],
    usage: AgentUsage,
) -> TurnCompleted:
    messages: list[FrameworkMessage] = []
    summaries: list[str] = []
    for call in calls:
        result = call.inline_result or deferred_results[call.tool_call_id]
        messages.append(
            FrameworkMessage(
                role=MessageRole.ASSISTANT,
                tool_call_id=call.tool_call_id,
                tool_name=call.tool_name,
                tool_arguments=call.arguments,
                framework_item=_framework_item("tool_call", call.tool_call_id, call.arguments),
            )
        )
        messages.append(
            FrameworkMessage(
                role=MessageRole.TOOL,
                tool_call_id=call.tool_call_id,
                tool_name=call.tool_name,
                tool_output=result,
                framework_item=_framework_item("tool_result", call.tool_call_id, result),
            )
        )
        summaries.append(f"{call.tool_name}: {result}")
    messages.append(_assistant_text(command, "; ".join(summaries)))
    return TurnCompleted(new_messages=messages, usage=usage)


def _assistant_text(command: str, text: str) -> FrameworkMessage:
    return FrameworkMessage(
        role=MessageRole.ASSISTANT,
        text=text,
        framework_item=json.dumps({"type": "assistant", "command": command, "text": text}),
    )


def _framework_item(item_type: str, tool_call_id: str, content: Any) -> str:
    return json.dumps({"type": item_type, "tool_call_id": tool_call_id, "content": content})
