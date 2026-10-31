# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A real ADK agent driven by a scripted model, so tests need no model key.

The scripted model reads commands from the latest user message:

* ``say <text>`` answers ``said: <text>``, streamed in two chunks.
* ``recall`` answers with every earlier user message, proving history replay.
* ``call <tool> <json>`` asks for tool calls; join parallel calls with `` && ``
  and later model steps with `` then ``. Once every step has its responses,
  the model answers ``<tool>: <result>`` for each, joined with ``; ``. A
  ``then fail`` step raises instead of calling tools.
* ``stall <gate>`` calls ``lookup``, then waits at a ``FileGate`` in the next
  model call before answering.
* ``fail`` raises on every attempt.

Every model call records ``model`` in the ``InvocationLog``, and every tool run
records its tool name. ``lookup`` and ``charge`` wait at the ``FileGate`` named
by a ``gate`` argument.
"""

from __future__ import annotations

import json
import os
from collections.abc import AsyncGenerator, Sequence
from pathlib import Path
from typing import Any

from google.adk.agents import LlmAgent, SequentialAgent
from google.adk.models.base_llm import BaseLlm
from google.adk.models.llm_request import LlmRequest
from google.adk.models.llm_response import LlmResponse
from google.adk.tools.base_tool import BaseTool
from google.adk.tools.exit_loop_tool import exit_loop
from google.adk.tools.function_tool import FunctionTool
from google.adk.tools.tool_context import ToolContext
from google.genai import types

from dex_agents import AgentSessionFlow, ToolEffect, ToolExecutionError, ToolPolicy
from dex_agents.testing import FileGate, InvocationLog
from dex_agents_google_adk import adk_agent_session_flow, idempotency_key_for_tool_call

SCRIPTED_ADK_AGENT_FLOW_TYPE = "ScriptedAdkAgent"
SCRIPTED_ADK_PIPELINE_FLOW_TYPE = "ScriptedAdkPipeline"
REVIEWER_ANSWER = "reviewed the draft"
FORBIDDEN_MESSAGE = "forbidden"
GUARDRAIL_ERROR = "blocked by the message guardrail"
STATE_DIR_ENVIRONMENT_VARIABLE = "SCRIPTED_ADK_AGENT_STATE_DIR"
INVOCATION_LOG_NAME = "invocations.log"
SLOW_WRITE_GATE = "slow_write"
MODEL_CALL_LOG_NAME = "model"
PROMPT_TOKENS_PER_CALL = 10
OUTPUT_TOKENS_PER_CALL = 5
TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "charge": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "refund": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "slow_write": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
}


class ScriptedModel(BaseLlm):
    """Answer each request from the commands in its latest user message."""

    model: str = "scripted-model"

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        _invocations().record(MODEL_CALL_LOG_NAME)
        user_texts = _user_texts(llm_request.contents)
        if not user_texts:
            raise RuntimeError("the request has no user message")
        command_index, command = user_texts[-1]
        later_contents = llm_request.contents[command_index + 1 :]
        if command.startswith("say "):
            async for response in _text_responses(f"said: {command.removeprefix('say ')}", stream):
                yield response
        elif command == "recall":
            earlier = [text for _, text in user_texts[:-1]]
            yield _text_response("history: " + " | ".join(earlier))
        elif command.startswith("call "):
            yield _tool_step_response(command, later_contents)
        elif command.startswith("stall "):
            yield await _stall_response(command.removeprefix("stall "), later_contents)
        elif command == "fail":
            raise RuntimeError("scripted model failure")
        else:
            raise ValueError(f"unknown scripted command {command!r}")


class ScriptedReviewerModel(BaseLlm):
    """Answer every request with the same review, without tools."""

    model: str = "scripted-reviewer-model"

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        yield _text_response(REVIEWER_ANSWER)


async def lookup(query: str, gate: str | None = None) -> str:
    """Look up documents that match a query."""
    _invocations().record("lookup")
    await _wait_at_gate(gate)
    return f"found {query}"


def publish(message: str) -> str:
    """Publish a message to subscribers."""
    _invocations().record("publish")
    return f"published {message}"


async def charge(amount: int, tool_context: ToolContext, gate: str | None = None) -> dict[str, Any]:
    """Charge the customer an amount."""
    _invocations().record("charge")
    await _wait_at_gate(gate)
    if amount <= 0:
        raise ToolExecutionError("amount must be positive")
    return {"charged": amount, "idempotency_key": idempotency_key_for_tool_call(tool_context)}


def refund(order_id: str) -> str:
    """Refund an order."""
    _invocations().record("refund")
    return f"refunded {order_id}"


async def slow_write(message: str) -> str:
    """Write a message to a slow ledger."""
    _invocations().record("slow_write")
    gate = FileGate(_state_dir(), SLOW_WRITE_GATE)
    gate.mark_reached()
    await gate.wait_for_release()
    return f"wrote {message}"


def block_forbidden_messages(
    tool: BaseTool, args: dict[str, Any], tool_context: ToolContext
) -> dict[str, Any] | None:
    """An agent before_tool_callback that answers forbidden messages without the tool."""
    if args.get("message") == FORBIDDEN_MESSAGE:
        return {"error": GUARDRAIL_ERROR}
    return None


def build_scripted_adk_agent(name: str = "assistant") -> LlmAgent:
    return LlmAgent(
        name=name,
        model=ScriptedModel(),
        instruction="Follow the scripted commands.",
        before_tool_callback=block_forbidden_messages,
        tools=[
            lookup,
            publish,
            charge,
            FunctionTool(refund, require_confirmation=True),
            slow_write,
            exit_loop,
        ],
    )


def build_scripted_adk_flow() -> AgentSessionFlow:
    return adk_agent_session_flow(
        build_scripted_adk_agent(),
        flow_type=SCRIPTED_ADK_AGENT_FLOW_TYPE,
        tool_policies=TOOL_POLICIES,
    )


def build_scripted_adk_pipeline_flow() -> AgentSessionFlow:
    """A drafter that uses the scripted tools, then a reviewer, in one SequentialAgent."""
    reviewer = LlmAgent(name="reviewer", model=ScriptedReviewerModel())
    return adk_agent_session_flow(
        SequentialAgent(
            name="pipeline", sub_agents=[build_scripted_adk_agent("drafter"), reviewer]
        ),
        flow_type=SCRIPTED_ADK_PIPELINE_FLOW_TYPE,
        tool_policies=TOOL_POLICIES,
    )


def _tool_step_response(command: str, later_contents: Sequence[types.Content]) -> LlmResponse:
    steps = command.split(" then ")
    finished_steps = sum(
        1
        for content in later_contents
        if content.role == "model" and any(part.function_call for part in content.parts or [])
    )
    if finished_steps < len(steps) and steps[finished_steps] == "fail":
        raise RuntimeError("scripted model failure")
    if finished_steps < len(steps):
        return _function_call_response(_parse_tool_requests(steps[finished_steps]))
    summaries = [
        f"{response.name}: {_describe_tool_response(response.response or {})}"
        for response in _function_responses(later_contents)
    ]
    return _text_response("; ".join(summaries))


async def _stall_response(gate_name: str, later_contents: Sequence[types.Content]) -> LlmResponse:
    if not _function_responses(later_contents):
        return _function_call_response([("lookup", {"query": "stall"})])
    gate = FileGate(_state_dir(), gate_name)
    gate.mark_reached()
    await gate.wait_for_release()
    return _text_response(f"stalled at {gate_name}")


def _parse_tool_requests(step: str) -> list[tuple[str, dict[str, Any]]]:
    requests: list[tuple[str, dict[str, Any]]] = []
    for request in step.split(" && "):
        _, tool_name, arguments = request.split(" ", 2)
        requests.append((tool_name, json.loads(arguments)))
    return requests


def _function_call_response(requests: Sequence[tuple[str, dict[str, Any]]]) -> LlmResponse:
    return LlmResponse(
        content=types.Content(
            role="model",
            parts=[
                types.Part(function_call=types.FunctionCall(name=tool_name, args=arguments))
                for tool_name, arguments in requests
            ],
        ),
        usage_metadata=_usage_metadata(),
    )


async def _text_responses(text: str, stream: bool) -> AsyncGenerator[LlmResponse, None]:
    if stream:
        middle = len(text) // 2
        for chunk in (text[:middle], text[middle:]):
            yield LlmResponse(
                content=types.Content(role="model", parts=[types.Part(text=chunk)]), partial=True
            )
    yield _text_response(text)


def _text_response(text: str) -> LlmResponse:
    return LlmResponse(
        content=types.Content(role="model", parts=[types.Part(text=text)]),
        usage_metadata=_usage_metadata(),
    )


def _usage_metadata() -> types.GenerateContentResponseUsageMetadata:
    return types.GenerateContentResponseUsageMetadata(
        prompt_token_count=PROMPT_TOKENS_PER_CALL,
        candidates_token_count=OUTPUT_TOKENS_PER_CALL,
        total_token_count=PROMPT_TOKENS_PER_CALL + OUTPUT_TOKENS_PER_CALL,
    )


def _describe_tool_response(response: dict[str, Any]) -> str:
    for key in ("result", "error"):
        if isinstance(response.get(key), str):
            return str(response[key])
    return json.dumps(response, sort_keys=True)


def _user_texts(contents: Sequence[types.Content]) -> list[tuple[int, str]]:
    """Return every user text part with the index of its content, in order."""
    return [
        (index, part.text)
        for index, content in enumerate(contents)
        if content.role == "user"
        for part in content.parts or []
        if part.text
    ]


def _function_responses(contents: Sequence[types.Content]) -> list[types.FunctionResponse]:
    return [
        part.function_response
        for content in contents
        for part in content.parts or []
        if part.function_response is not None
    ]


async def _wait_at_gate(gate_name: str | None) -> None:
    if gate_name is None:
        return
    gate = FileGate(_state_dir(), gate_name)
    gate.mark_reached()
    await gate.wait_for_release()


def _state_dir() -> Path:
    return Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])


def _invocations() -> InvocationLog:
    return InvocationLog(_state_dir() / INVOCATION_LOG_NAME)
