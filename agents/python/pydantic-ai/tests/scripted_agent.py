# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A real Pydantic AI agent whose ``FunctionModel`` follows commands in the user prompt.

The scripted model reads the latest user prompt:

* ``say <text>`` answers ``said: <text>``.
* ``recall`` answers with every earlier user prompt and the system prompt it
  sees, proving history replay.
* ``report <title>`` answers with a structured ``Report`` through the output tool.
* ``call <tool> <json>`` asks for tool calls. Join calls of one response with
  `` && `` and later responses with `` then ``. A response item may also be
  ``report <title>``, and a ``then fail`` response raises. After the last
  response the model summarizes every tool result of the turn.
* ``stall <gate>`` calls ``lookup``, then streams text and waits at a ``FileGate``.
* ``fail`` raises on every model request.

Every model call records ``model:<turn number>:<model call of the turn>``, and
every tool records its name, in the invocation log. Tools take an optional
``gate`` argument that makes them wait at that ``FileGate``. ``book_room``
runs a booking sub-agent that reserves a room and then fails its own output
validation, so the tool body raises ``UnexpectedModelBehavior`` after a write.

The Flow serves the agent through a ``WrapperAgent``. The agent has an output
validator, so its ``output_type`` lists ``DeferredToolRequests``. It also has
an outermost capability that turns any exception around ``publish`` into a
result and records ``audit:publish``.
"""

from __future__ import annotations

import json
import os
from collections.abc import AsyncIterator
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path
from typing import Any

from pydantic import BaseModel
from pydantic_ai import (
    Agent,
    CallDeferred,
    DeferredToolRequests,
    ModelMessage,
    ModelRequest,
    ModelResponse,
    ModelRetry,
    RunContext,
    SystemPromptPart,
    TextPart,
    ToolCallPart,
    ToolDefinition,
    ToolReturnPart,
    UserPromptPart,
)
from pydantic_ai.agent import WrapperAgent
from pydantic_ai.capabilities import AbstractCapability, CapabilityOrdering, WrapToolExecuteHandler
from pydantic_ai.models.function import AgentInfo, DeltaToolCall, DeltaToolCalls, FunctionModel
from pydantic_ai.toolsets import ExternalToolset

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents.testing import FileGate, InvocationLog
from dex_agents_pydantic_ai import deferred_tool_idempotency_key, pydantic_ai_session_flow

SCRIPTED_AGENT_FLOW_TYPE = "ScriptedPydanticAIAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_STATE_DIR"
INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE = "SCRIPTED_AGENT_INTERRUPT_DEADLINE_SECONDS"
INVOCATION_LOG_NAME = "invocations.log"
SYSTEM_PROMPT = "You are the Dex test agent."
CONNECTION_LOST_AMOUNT = 999
TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "publish": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    "charge": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "charge_once": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "slow_write": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "start_export": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "archive": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "book_room": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
}


class Report(BaseModel):
    """A structured final answer."""

    title: str
    items: list[str]


class Booking(BaseModel):
    """The booking sub-agent's answer."""

    reference: str


@dataclass
class ExceptionSwallowingAudit(AbstractCapability[str]):
    """An outermost agent capability that returns any exception around ``publish`` as a result."""

    invocations: InvocationLog

    def get_ordering(self) -> CapabilityOrdering:
        return CapabilityOrdering(position="outermost")

    async def wrap_tool_execute(
        self,
        ctx: RunContext[str],
        *,
        call: ToolCallPart,
        tool_def: ToolDefinition,
        args: dict[str, Any],
        handler: WrapToolExecuteHandler,
    ) -> Any:
        if call.tool_name != "publish":
            return await handler(args)
        self.invocations.record("audit:publish")
        try:
            return await handler(args)
        except Exception as error:
            return f"audit swallowed {type(error).__name__}"


@dataclass(frozen=True)
class ScriptedResponse:
    text: str | None = None
    tool_calls: tuple[ToolCallPart, ...] = ()
    stall_gate: str | None = None


def build_scripted_agent(state_dir: Path) -> Agent[str, Any]:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    async def wait_at_gate(gate_name: str | None) -> None:
        if gate_name is not None:
            gate = FileGate(state_dir, gate_name)
            gate.mark_reached()
            await gate.wait_for_release()

    def respond(messages: list[ModelMessage], info: AgentInfo) -> ModelResponse:
        response = plan_scripted_response(messages, info)
        if response.tool_calls:
            return ModelResponse(parts=list(response.tool_calls))
        return ModelResponse(parts=[TextPart(response.text or "")])

    async def stream_response(
        messages: list[ModelMessage], info: AgentInfo
    ) -> AsyncIterator[str | DeltaToolCalls]:
        invocations.record(scripted_model_call_name(messages))
        response = plan_scripted_response(messages, info)
        if response.tool_calls:
            yield {
                index: DeltaToolCall(
                    call.tool_name, call.args_as_json_str(), tool_call_id=call.tool_call_id
                )
                for index, call in enumerate(response.tool_calls)
            }
            return
        if response.stall_gate is not None:
            yield "thinking after found stall. "
            await wait_at_gate(response.stall_gate)
        yield response.text or ""

    agent: Agent[str, Any] = Agent(
        FunctionModel(respond, stream_function=stream_response),
        deps_type=str,
        output_type=[str, Report, DeferredToolRequests],
        system_prompt=SYSTEM_PROMPT,
        instructions="Answer tersely.",
        name="scripted",
        toolsets=[ExternalToolset([ToolDefinition(name="ask_browser")])],
        capabilities=[ExceptionSwallowingAudit(invocations)],
    )

    @agent.output_validator
    def require_report_items(output: Any) -> Any:
        if isinstance(output, Report) and not output.items:
            raise ModelRetry("a report needs items")
        return output

    @agent.tool_plain
    async def lookup(query: str, gate: str | None = None) -> str:
        """Look up a document."""
        invocations.record("lookup")
        await wait_at_gate(gate)
        return f"found {query}"

    @agent.tool_plain
    def publish(message: str) -> str:
        """Publish a message."""
        invocations.record("publish")
        return f"published {message}"

    @agent.tool
    async def charge(ctx: RunContext[str], amount: int, gate: str | None = None) -> str:
        """Charge an amount."""
        invocations.record("charge")
        await wait_at_gate(gate)
        if amount <= 0:
            raise ModelRetry("amount must be positive")
        if amount == CONNECTION_LOST_AMOUNT:
            raise ConnectionError("charge lost its connection")
        return f"charged {amount} for {ctx.deps} with key {deferred_tool_idempotency_key(ctx)}"

    @agent.tool_plain(retries=0)
    def charge_once(amount: int) -> str:
        """Charge an amount, with no retries."""
        invocations.record("charge_once")
        if amount <= 0:
            raise ModelRetry("amount must be positive")
        return f"charged {amount} once"

    @agent.tool_plain
    async def slow_write(message: str, gate: str | None = None) -> str:
        """Write slowly."""
        invocations.record("slow_write")
        await wait_at_gate(gate)
        return f"wrote {message}"

    @agent.tool_plain
    def start_export(name: str) -> str:
        """Start an export, then defer its result."""
        invocations.record("start_export")
        raise CallDeferred(metadata={"export": name})

    booking_agent = build_booking_agent(invocations)

    @agent.tool_plain
    async def book_room(room: str) -> str:
        """Book a room through the booking sub-agent."""
        result = await booking_agent.run(f"book {room}")
        return result.output.reference

    @agent.tool_plain(requires_approval=True)
    def archive(item: str) -> str:
        """Archive an item."""
        invocations.record("archive")
        return f"archived {item}"

    @agent.tool_plain(metadata={"annotations": {"readOnlyHint": True}})
    def read_page(url: str) -> str:
        """Read a page; annotated like a read-only MCP tool."""
        invocations.record("read_page")
        return f"page {url}"

    @agent.tool_plain(metadata={"annotations": {"readOnlyHint": False, "idempotentHint": True}})
    def upsert_note(text: str) -> str:
        """Store a note; annotated like an idempotent MCP tool."""
        invocations.record("upsert_note")
        return f"noted {text}"

    @agent.tool_plain(defer_loading=True)
    def notify(text: str) -> str:
        """Notify the team; found only through tool search."""
        invocations.record("notify")
        return f"notified {text}"

    return agent


def build_booking_agent(invocations: InvocationLog) -> Agent[None, Booking]:
    def respond(messages: list[ModelMessage], info: AgentInfo) -> ModelResponse:
        if not any(isinstance(message, ModelResponse) for message in messages):
            return ModelResponse(parts=[ToolCallPart("reserve_room", {"room": "A"}, "reserve")])
        return ModelResponse(
            parts=[ToolCallPart(info.output_tools[0].name, {"reference": ""}, "booking")]
        )

    booking_agent = Agent(FunctionModel(respond), output_type=Booking)

    @booking_agent.tool_plain
    def reserve_room(room: str) -> str:
        """Reserve a room."""
        invocations.record("reserve_room")
        return f"reserved {room}"

    @booking_agent.output_validator
    def require_reference(output: Booking) -> Booking:
        if not output.reference:
            raise ModelRetry("a booking needs a reference")
        return output

    return booking_agent


def build_scripted_agent_flow(
    state_dir: Path,
    interrupt_deadline: timedelta | None = None,
) -> AgentSessionFlow:
    return pydantic_ai_session_flow(
        WrapperAgent(build_scripted_agent(state_dir)),
        flow_type=SCRIPTED_AGENT_FLOW_TYPE,
        tool_policies=TOOL_POLICIES,
        interrupt_deadline=interrupt_deadline,
        deps_factory=lambda session_id: f"deps-{session_id}",
    )


def build_scripted_agent_flow_from_environment() -> AgentSessionFlow:
    deadline_seconds = os.environ.get(INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE)
    return build_scripted_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        None if deadline_seconds is None else timedelta(seconds=float(deadline_seconds)),
    )


def scripted_model_call_name(messages: list[ModelMessage]) -> str:
    prompts = _user_prompts(messages)
    return f"model:{len(prompts)}:{_model_calls_since(messages, prompts[-1][0]) + 1}"


def plan_scripted_response(messages: list[ModelMessage], info: AgentInfo) -> ScriptedResponse:
    prompts = _user_prompts(messages)
    prompt_index, prompt = prompts[-1]
    stage = _model_calls_since(messages, prompt_index)
    output_tool_name = info.output_tools[0].name
    if prompt.startswith("say "):
        return ScriptedResponse(text=f"said: {prompt.removeprefix('say ')}")
    if prompt == "recall":
        system_prompts = [
            part.content
            for message in messages
            if isinstance(message, ModelRequest)
            for part in message.parts
            if isinstance(part, SystemPromptPart)
        ]
        earlier = " | ".join(text for _, text in prompts[:-1])
        return ScriptedResponse(text=f"history: {earlier}; system: {' '.join(system_prompts)}")
    if prompt.startswith("report "):
        return ScriptedResponse(
            tool_calls=(_report_call(output_tool_name, prompt.removeprefix("report "), "report"),)
        )
    if prompt.startswith("call "):
        stages = prompt.split(" then ")
        if stage < len(stages) and stages[stage] == "fail":
            raise RuntimeError("scripted model failure")
        if stage < len(stages):
            return ScriptedResponse(
                tool_calls=_parse_tool_calls(stages[stage], len(prompts), stage, output_tool_name)
            )
        return ScriptedResponse(text=_summarize_tool_results(messages[prompt_index + 1 :]))
    if prompt.startswith("stall "):
        if stage == 0:
            lookup = ToolCallPart("lookup", json.dumps({"query": "stall"}), "stall-lookup")
            return ScriptedResponse(tool_calls=(lookup,))
        gate_name = prompt.removeprefix("stall ")
        return ScriptedResponse(text=f"stalled at {gate_name}", stall_gate=gate_name)
    if prompt == "fail":
        raise RuntimeError("scripted model failure")
    raise ValueError(f"unknown scripted command {prompt!r}")


def _user_prompts(messages: list[ModelMessage]) -> list[tuple[int, str]]:
    return [
        (index, part.content)
        for index, message in enumerate(messages)
        if isinstance(message, ModelRequest)
        for part in message.parts
        if isinstance(part, UserPromptPart) and isinstance(part.content, str)
    ]


def _model_calls_since(messages: list[ModelMessage], prompt_index: int) -> int:
    return sum(isinstance(message, ModelResponse) for message in messages[prompt_index + 1 :])


def _parse_tool_calls(
    stage_text: str,
    turn_number: int,
    stage: int,
    output_tool_name: str,
) -> tuple[ToolCallPart, ...]:
    calls: list[ToolCallPart] = []
    for index, request in enumerate(stage_text.split(" && ")):
        tool_call_id = f"call-{turn_number}-{stage}-{index}"
        if request.startswith("report "):
            calls.append(
                _report_call(output_tool_name, request.removeprefix("report "), tool_call_id)
            )
            continue
        _, tool_name, arguments = request.split(" ", 2)
        calls.append(ToolCallPart(tool_name, arguments, tool_call_id))
    return tuple(calls)


def _report_call(output_tool_name: str, title: str, tool_call_id: str) -> ToolCallPart:
    return ToolCallPart(
        output_tool_name, json.dumps({"title": title, "items": ["a", "b"]}), tool_call_id
    )


def _summarize_tool_results(messages: list[ModelMessage]) -> str:
    return "; ".join(
        f"{part.tool_name}: {part.content}"
        for message in messages
        if isinstance(message, ModelRequest)
        for part in message.parts
        if isinstance(part, ToolReturnPart)
    )
