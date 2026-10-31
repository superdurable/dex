# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A deterministic SDK ``Model`` that derives every response from its input.

A queue of scripted steps would not survive a Worker restart or a retried
``RunModel``, so each call reads the current turn's user message and the items
the turn already produced. Commands:

* ``say <text>`` answers ``said: <text>``.
* ``recall`` answers with earlier user messages and tool outputs.
* ``think <text>`` returns a reasoning summary, then ``thought: <text>``.
* Otherwise the command is rounds joined by `` then ``, one model response
  each. A round is ``call <tool> <json>`` or ``handoff <agent>`` requests joined
  by `` && ``, or ``fail``, which raises, or ``stall <gate>``, which streams
  ``thinking before <gate>`` and waits at a ``FileGate`` before answering.
  After the last request round the model answers ``<tool>: <output>`` for every
  tool call of the turn, joined by ``; ``.

Every response reports ``model_call_usage()``, and every call is recorded as
``model:<user message count>:<call number in the turn>``.
"""

from __future__ import annotations

import json
from collections.abc import AsyncIterator, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from agents import Handoff, Model, ModelResponse, ModelSettings, ModelTracing, Usage
from agents.agent_output import AgentOutputSchemaBase
from agents.items import TResponseInputItem, TResponseOutputItem, TResponseStreamEvent
from agents.testing import ModelStep, ScriptedModel, assistant_message, function_call
from agents.tool import Tool
from openai.types.responses import ResponseReasoningItem
from openai.types.responses.response_prompt_param import ResponsePromptParam
from openai.types.responses.response_reasoning_item import Summary
from openai.types.responses.response_usage import InputTokensDetails, OutputTokensDetails

from dex_agents.testing import FileGate, InvocationLog

ROUND_SEPARATOR = " then "
REQUEST_SEPARATOR = " && "
SCRIPTED_MODEL_FAILURE = "scripted model failure"
_TEXT_DELTA_EVENT_TYPE = "response.output_text.delta"


def model_call_usage() -> Usage:
    return Usage(
        requests=1,
        input_tokens=10,
        input_tokens_details=InputTokensDetails(cache_write_tokens=0, cached_tokens=2),
        output_tokens=5,
        output_tokens_details=OutputTokensDetails(reasoning_tokens=1),
        total_tokens=15,
    )


@dataclass(frozen=True)
class _ScriptedResponse:
    output: list[TResponseOutputItem]
    gate_name: str | None = None


class CommandScriptedModel(Model):
    def __init__(self, state_dir: Path, invocations: InvocationLog) -> None:
        self._state_dir = state_dir
        self._invocations = invocations

    async def get_response(
        self,
        system_instructions: str | None,
        input: str | list[TResponseInputItem],
        model_settings: ModelSettings,
        tools: list[Tool],
        output_schema: AgentOutputSchemaBase | None,
        handoffs: list[Handoff],
        tracing: ModelTracing,
        *,
        previous_response_id: str | None,
        conversation_id: str | None,
        prompt: ResponsePromptParam | None,
    ) -> ModelResponse:
        response = self._respond(input)
        if response.gate_name is not None:
            await self._wait_at_gate(response.gate_name)
        return await ScriptedModel([_model_step(response)]).get_response(
            system_instructions,
            input,
            model_settings,
            tools,
            output_schema,
            handoffs,
            tracing,
            previous_response_id=previous_response_id,
            conversation_id=conversation_id,
            prompt=prompt,
        )

    async def stream_response(
        self,
        system_instructions: str | None,
        input: str | list[TResponseInputItem],
        model_settings: ModelSettings,
        tools: list[Tool],
        output_schema: AgentOutputSchemaBase | None,
        handoffs: list[Handoff],
        tracing: ModelTracing,
        *,
        previous_response_id: str | None,
        conversation_id: str | None,
        prompt: ResponsePromptParam | None,
    ) -> AsyncIterator[TResponseStreamEvent]:
        response = self._respond(input)
        events = ScriptedModel([_model_step(response)]).stream_response(
            system_instructions,
            input,
            model_settings,
            tools,
            output_schema,
            handoffs,
            tracing,
            previous_response_id=previous_response_id,
            conversation_id=conversation_id,
            prompt=prompt,
        )
        gate_name = response.gate_name
        async for event in events:
            yield event
            if gate_name is not None and getattr(event, "type", None) == _TEXT_DELTA_EVENT_TYPE:
                await self._wait_at_gate(gate_name)
                gate_name = None

    def _respond(self, input: str | list[TResponseInputItem]) -> _ScriptedResponse:
        items: list[Mapping[str, Any]] = (
            [{"role": "user", "content": input}]
            if isinstance(input, str)
            else [_as_mapping(item) for item in input]
        )
        user_indexes = [index for index, item in enumerate(items) if _is_user_message(item)]
        if not user_indexes:
            raise ValueError("the scripted model needs a user message")
        command = _message_text(items[user_indexes[-1]])
        turn = _Turn(
            number=len(user_indexes),
            earlier_items=items[: user_indexes[-1]],
            items=items[user_indexes[-1] + 1 :],
        )
        self._invocations.record(f"model:{turn.number}:{turn.batch_count + 1}")
        return _respond_to_command(command, turn)

    async def _wait_at_gate(self, gate_name: str) -> None:
        gate = FileGate(self._state_dir, gate_name)
        gate.mark_reached()
        await gate.wait_for_release()


class _Turn:
    def __init__(
        self,
        number: int,
        earlier_items: Sequence[Mapping[str, Any]],
        items: Sequence[Mapping[str, Any]],
    ) -> None:
        self.number = number
        self.earlier_items = earlier_items
        self.calls = [item for item in items if item.get("type") == "function_call"]
        self.outputs = {
            str(item["call_id"]): _output_text(item.get("output"))
            for item in items
            if item.get("type") == "function_call_output"
        }
        self.batch_count = sum(
            1
            for index, item in enumerate(items)
            if item.get("type") == "function_call"
            and (index == 0 or items[index - 1].get("type") != "function_call")
        )

    def message(self, text: str, index: int) -> TResponseOutputItem:
        return assistant_message(text, item_id=f"message-{self.number}-{index}")

    def call_id(self, round_index: int, request_index: int) -> str:
        return f"call-{self.number}-{round_index}-{request_index}"

    def summary(self) -> str:
        return "; ".join(
            f"{call['name']}: {self.outputs[str(call['call_id'])]}"
            for call in self.calls
            if not str(call["name"]).startswith("transfer_to_")
        )


def _respond_to_command(command: str, turn: _Turn) -> _ScriptedResponse:
    if command.startswith("say "):
        return _ScriptedResponse([turn.message(f"said: {command.removeprefix('say ')}", 0)])
    if command == "recall":
        texts = [_message_text(item) for item in turn.earlier_items if _is_user_message(item)]
        outputs = [
            _output_text(item.get("output"))
            for item in turn.earlier_items
            if item.get("type") == "function_call_output"
        ]
        text = f"history: {' | '.join(texts)}; tools: {' | '.join(outputs)}"
        return _ScriptedResponse([turn.message(text, 0)])
    if command.startswith("think "):
        topic = command.removeprefix("think ")
        reasoning = ResponseReasoningItem(
            id=f"reasoning-{turn.number}",
            summary=[Summary(text=f"pondering {topic}", type="summary_text")],
            type="reasoning",
        )
        return _ScriptedResponse([reasoning, turn.message(f"thought: {topic}", 0)])
    return _respond_to_rounds(command.split(ROUND_SEPARATOR), turn)


def _respond_to_rounds(rounds: Sequence[str], turn: _Turn) -> _ScriptedResponse:
    completed_requests = 0
    for round_index, round_text in enumerate(rounds):
        if round_text == "fail":
            if len(turn.calls) == completed_requests:
                raise RuntimeError(SCRIPTED_MODEL_FAILURE)
            continue
        if round_text.startswith("stall "):
            if len(turn.calls) == completed_requests:
                return _stall(round_text.removeprefix("stall "), turn, round_index)
            continue
        requests = round_text.split(REQUEST_SEPARATOR)
        if len(turn.calls) == completed_requests:
            return _ScriptedResponse(
                [
                    _request_output(request, turn.call_id(round_index, request_index))
                    for request_index, request in enumerate(requests)
                ]
            )
        completed_requests += len(requests)
    return _ScriptedResponse([turn.message(turn.summary(), len(rounds))])


def _stall(gate_name: str, turn: _Turn, round_index: int) -> _ScriptedResponse:
    summary = turn.summary()
    answer = f"stalled at {gate_name}: {summary}" if summary else f"stalled at {gate_name}"
    return _ScriptedResponse(
        [
            turn.message(f"thinking before {gate_name}", round_index),
            turn.message(answer, round_index + 1),
        ],
        gate_name=gate_name,
    )


def _request_output(request: str, call_id: str) -> TResponseOutputItem:
    verb, _, rest = request.partition(" ")
    if verb == "handoff":
        return function_call(f"transfer_to_{rest}", {}, call_id=call_id)
    if verb == "call":
        tool_name, _, arguments = rest.partition(" ")
        return function_call(tool_name, arguments, call_id=call_id)
    raise ValueError(f"unknown scripted request {request!r}")


def _model_step(response: _ScriptedResponse) -> ModelStep:
    return ModelStep(output=response.output, usage=model_call_usage())


def _as_mapping(item: Any) -> Mapping[str, Any]:
    if isinstance(item, Mapping):
        return item
    dumped: Mapping[str, Any] = item.model_dump(exclude_unset=True)
    return dumped


def _is_user_message(item: Mapping[str, Any]) -> bool:
    return item.get("role") == "user" and item.get("type") in (None, "message")


def _message_text(item: Mapping[str, Any]) -> str:
    content = item.get("content")
    if isinstance(content, str):
        return content
    return "".join(str(part.get("text", "")) for part in content or [] if isinstance(part, Mapping))


def _output_text(output: Any) -> str:
    return output if isinstance(output, str) else json.dumps(output)
