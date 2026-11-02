# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted framework runner that stands in for a real agent framework.

Each ``run_model_step`` is one scripted model call. The model reads commands
from the user message:

* ``say <text>`` answers ``said: <text>``.
* ``recall`` answers with every earlier user message, proving history replay.
* ``stall <gate>`` writes a text delta, waits at a ``FileGate``, then answers.
* ``fail`` raises on every attempt.
* ``call <tool> <json>`` asks for one tool call; join several with
  `` && call ``. The second model call summarizes the results.
* ``call-then-fail <tool> <json>`` does the same, then fails in the second call.
* ``call-then-stall <gate> <tool> <json>`` waits at a gate in the second call.
* ``chain <tool> <json> && <tool> <json> then <tool> <json> [then fail]`` asks
  for two tool batches in the first two model calls, then answers or fails.
* ``chain-reusing-ids ...`` does the same, but repeats the first batch's tool
  call IDs in the second batch.
* ``call-incremental <tool> <json>`` asks for the call and stores its item at
  once through ``ToolCallsRequested.new_messages``, which this runner may not do.

The released 0.1.0 Worker imports this module too, so it uses only the 0.1.0
API unless ``SCRIPTED_AGENT_BUILT_IN_TOOLS`` enables built-in tools.

Every tool accepts ``"gate"``, which waits at that ``FileGate`` first, and
``"fail_attempts"``, which raises on attempts up to that number.
"""

from __future__ import annotations

import asyncio
import json
import os
from collections.abc import Sequence
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
    ToolCallsRequested,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
    describe_tool_call_outcome,
)
from dex_agents.testing import FileGate, InvocationLog

SCRIPTED_AGENT_FLOW_TYPE = "ScriptedAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_STATE_DIR"
INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_INTERRUPT_DEADLINE_SECONDS"
BUILT_IN_TOOLS_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_BUILT_IN_TOOLS"
INVOCATION_LOG_NAME = "invocations.log"
IDEMPOTENCY_KEY_LOG_NAME = "idempotency-keys.log"
MODEL_CALL_USAGE = AgentUsage(model_calls=1, input_tokens=10, output_tokens=5)
TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "publish": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    "charge": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "slow_write": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
}


@dataclass(frozen=True)
class ScriptedToolCall:
    tool_call_id: str
    tool_name: str
    arguments: str


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

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        if turn.framework_run_state is not None:
            return await self._continue_after_tool_calls(turn, turn.framework_run_state)
        self._invocations.record(f"model:{turn.turn_id}:1")
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
        if command.startswith("stall "):
            await self._wait_at_gate(turn, command.removeprefix("stall "))
            return _answer(command, f"stalled at {command.removeprefix('stall ')}")
        if command == "fail":
            raise RuntimeError("scripted failure")
        if command.startswith(("call ", "call-then-fail ", "call-then-stall ")):
            return self._request_tool_calls(turn, command)
        if command.startswith("call-incremental "):
            requested = self._request_tool_calls(turn, command.replace("-incremental", "", 1))
            assert isinstance(requested, ToolCallsRequested)
            return ToolCallsRequested(
                framework_run_state=requested.framework_run_state,
                tool_calls=requested.tool_calls,
                usage=requested.usage,
                new_messages=[_assistant_text(command, "calling")],
            )
        if command.startswith(("chain ", "chain-reusing-ids ")):
            return self._request_chain_batch(turn, command, [], batch_index=0, model_calls=1)
        raise ValueError(f"unknown scripted command {command!r}")

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        record = call.tool_call
        self._invocations.record(record.tool_name)
        self._idempotency_keys.record(
            f"{record.tool_name} {call.idempotency_key} attempt {call.attempt}"
        )
        arguments = json.loads(record.arguments)
        if "gate" in arguments:
            gate = FileGate(self._state_dir, str(arguments["gate"]))
            gate.mark_reached()
            await gate.wait_for_release()
        if call.attempt <= int(arguments.get("fail_attempts", 0)):
            raise RuntimeError(f"{record.tool_name} attempt {call.attempt} lost its connection")
        if record.tool_name == "lookup":
            return ToolExecutionResult(output=f"found {arguments['query']}")
        if record.tool_name == "publish":
            await asyncio.to_thread(
                call.events.write_text_delta, f"publishing {arguments['message']}"
            )
            return ToolExecutionResult(output=f"published {arguments['message']}")
        if record.tool_name == "charge":
            if int(arguments["amount"]) <= 0:
                raise ToolExecutionError("amount must be positive")
            return ToolExecutionResult(
                output=f"charged {arguments['amount']} with key {call.idempotency_key}"
            )
        if record.tool_name == "slow_write":
            return ToolExecutionResult(output=f"wrote {arguments['message']}")
        raise ToolExecutionError(f"unknown tool {record.tool_name}")

    def _request_tool_calls(self, turn: AgentTurnContext, command: str) -> AgentTurnOutcome:
        requests = command.split(" ", 1)[1]
        if command.startswith("call-then-stall "):
            requests = requests.split(" ", 1)[1]
        calls: list[ScriptedToolCall] = []
        for index, request in enumerate(requests.split(" && call ")):
            tool_name, arguments = request.removeprefix("call ").split(" ", 1)
            calls.append(ScriptedToolCall(f"{turn.turn_id}-call-{index}", tool_name, arguments))
        return ToolCallsRequested(
            framework_run_state=json.dumps(
                {"command": command, "model_calls": 1, "calls": [asdict(call) for call in calls]}
            ),
            tool_calls=[
                DeferredToolCall(
                    tool_call_id=call.tool_call_id,
                    tool_name=call.tool_name,
                    arguments=call.arguments,
                    policy=turn.resolve_tool_policy(call.tool_name),
                )
                for call in calls
            ],
            usage=MODEL_CALL_USAGE,
        )

    async def _continue_after_tool_calls(
        self,
        turn: AgentTurnContext,
        framework_run_state: str,
    ) -> AgentTurnOutcome:
        state = json.loads(framework_run_state)
        model_call = int(state["model_calls"]) + 1
        self._invocations.record(f"model:{turn.turn_id}:{model_call}")
        command = str(state["command"])
        if command.startswith("call-then-fail "):
            raise RuntimeError("scripted failure after the tool calls ran")
        requested_batches = int(state.get("batches", 1))
        if command.startswith(("chain ", "chain-reusing-ids ")) and requested_batches < len(
            _chain_batches(command)
        ):
            earlier = [ScriptedToolCall(**call) for call in state["calls"]]
            return self._request_chain_batch(
                turn, command, earlier, batch_index=requested_batches, model_calls=model_call
            )
        if command.startswith("chain ") and command.endswith(" then fail"):
            raise RuntimeError("scripted failure after two tool batches")
        if command.startswith("call-then-stall "):
            await self._wait_at_gate(turn, command.split(" ", 2)[1])
        calls = [ScriptedToolCall(**call) for call in state["calls"]]
        results: dict[str, str] = {}
        for call in calls:
            record = turn.recorded_tool_call(call.tool_call_id)
            if record is None:
                raise RuntimeError(f"no record for tool call {call.tool_call_id}")
            results[call.tool_call_id] = describe_tool_call_outcome(record)
        return _complete_tool_turn(command, calls, results)

    def _request_chain_batch(
        self,
        turn: AgentTurnContext,
        command: str,
        earlier: list[ScriptedToolCall],
        batch_index: int,
        model_calls: int,
    ) -> AgentTurnOutcome:
        batches = _chain_batches(command)
        label = "a" if command.startswith("chain-reusing-ids ") else "ab"[batch_index]
        requested = [
            ScriptedToolCall(f"{turn.turn_id}-{label}-{index}", *request.split(" ", 1))
            for index, request in enumerate(batches[batch_index].split(" && "))
        ]
        calls = [*earlier, *requested]
        return ToolCallsRequested(
            framework_run_state=json.dumps(
                {
                    "command": command,
                    "model_calls": model_calls,
                    "batches": batch_index + 1,
                    "calls": [asdict(call) for call in calls],
                }
            ),
            tool_calls=[
                DeferredToolCall(
                    tool_call_id=call.tool_call_id,
                    tool_name=call.tool_name,
                    arguments=call.arguments,
                    policy=turn.resolve_tool_policy(call.tool_name),
                )
                for call in requested
            ],
            usage=MODEL_CALL_USAGE,
        )

    async def _wait_at_gate(self, turn: AgentTurnContext, gate_name: str) -> None:
        turn.events.write_text_delta(f"thinking before {gate_name}")
        gate = FileGate(self._state_dir, gate_name)
        gate.mark_reached()
        await gate.wait_for_release()


def build_scripted_agent_flow(
    state_dir: Path,
    interrupt_deadline: timedelta | None = None,
    built_in_tool_names: Sequence[str] = (),
) -> AgentSessionFlow:
    options: dict[str, Any] = {}
    if built_in_tool_names:
        # Imported here, so the released 0.1.0 Worker can load this module.
        from dex_agents import BuiltInTool

        options["built_in_tools"] = frozenset(BuiltInTool(name) for name in built_in_tool_names)
    return AgentSessionFlow(
        ScriptedRunner(state_dir),
        flow_type=SCRIPTED_AGENT_FLOW_TYPE,
        options=AgentSessionOptions(
            tool_policies=TOOL_POLICIES, interrupt_deadline=interrupt_deadline, **options
        ),
    )


def build_scripted_agent_flow_from_environment() -> AgentSessionFlow:
    deadline_seconds = os.environ.get(INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE)
    built_in_tool_names = os.environ.get(BUILT_IN_TOOLS_ENVIRONMENT_VARIABLE, "")
    return build_scripted_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        None if deadline_seconds is None else timedelta(seconds=float(deadline_seconds)),
        [name for name in built_in_tool_names.split(",") if name],
    )


def _chain_batches(command: str) -> list[str]:
    batches = command.split(" ", 1)[1].removesuffix(" then fail")
    return batches.split(" then ")


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
    results: dict[str, str],
) -> TurnCompleted:
    messages: list[FrameworkMessage] = []
    summaries: list[str] = []
    for call in calls:
        result = results[call.tool_call_id]
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
    return TurnCompleted(new_messages=messages, usage=MODEL_CALL_USAGE)


def _assistant_text(command: str, text: str) -> FrameworkMessage:
    return FrameworkMessage(
        role=MessageRole.ASSISTANT,
        text=text,
        framework_item=json.dumps({"type": "assistant", "command": command, "text": text}),
    )


def _framework_item(item_type: str, tool_call_id: str, content: Any) -> str:
    return json.dumps({"type": item_type, "tool_call_id": tool_call_id, "content": content})
