# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Pydantic AI agent whose tool source offers the fake ledger Connector's operations.

The ``FunctionModel`` reads ``calls <json>`` from the latest user prompt, a
JSON list of ``[tool_name, arguments]`` pairs, and calls them in one response
with IDs ``call-0``, ``call-1``, and so on. More lists joined with `` then ``
are later responses, with IDs ``call-<response>-<index>``; a third item in a
pair sets the ID. Once every response ran it answers one
``<tool_name>: <result>`` line per result of the turn. A ``hold:<gate>`` word before
``calls`` makes that answer wait at a ``FileGate``, so a test can read the
turn's records before the turn ends.

``hiding_note`` comes from a second source whose listing a test can empty
or break by writing ``withdrawn`` into the state directory, as a dynamic
source that withdraws a tool would.

The agent also has its own function tool, ``lookup``. The session policy for
``policy_note`` claims ``READ`` and asks for approval; only the approval
applies, because the effect class comes from the manifest.
"""

from __future__ import annotations

import json
import os
from collections.abc import AsyncIterator, Sequence
from pathlib import Path
from typing import Any

from pydantic_ai import (
    Agent,
    ModelMessage,
    ModelRequest,
    ModelResponse,
    RetryPromptPart,
    TextPart,
    ToolCallPart,
    ToolReturnPart,
    UserPromptPart,
)
from pydantic_ai.models.function import AgentInfo, DeltaToolCall, DeltaToolCalls, FunctionModel

from dex_agents import (
    AgentSessionFlow,
    AgentToolCall,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    ToolCallStepOptions,
    ToolEffect,
    ToolExecutionResult,
    ToolPolicy,
    ToolTaskPoll,
    ToolTaskPollOutcome,
)
from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents.testing import FileGate
from dex_agents_pydantic_ai import pydantic_ai_session_flow
from tests.fake_ledger import (
    LEDGER_CONNECTION,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    CreatePayoutOperation,
    FakeLedgerProvider,
    GetBalanceOperation,
    PostNoteOperation,
)

CONNECTOR_AGENT_FLOW_TYPE = "PydanticAIConnectorAgent"
WITHDRAWN_FILE_NAME = "withdrawn"
CONNECTOR_TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "policy_note": ToolPolicy(ToolEffect.READ, requires_approval=True),
}


def build_connector_source(state_dir: Path) -> ConnectorToolSource:
    provider = FakeLedgerProvider(state_dir)
    payouts = CreatePayoutOperation(provider)
    notes = PostNoteOperation(provider)
    return ConnectorToolSource(
        [
            connector_tool(GetBalanceOperation(provider), LEDGER_CONNECTION, name="get_balance"),
            connector_tool(
                payouts, LEDGER_CONNECTION, name="create_payout", requires_approval=False
            ),
            connector_tool(payouts, LEDGER_CONNECTION, name="approved_payout"),
            connector_tool(notes, LEDGER_CONNECTION, name="post_note"),
            connector_tool(notes, LEDGER_CONNECTION, name="policy_note"),
        ]
    )


def build_connector_agent(state_dir: Path) -> Agent[None, str]:
    async def respond(messages: list[ModelMessage], info: AgentInfo) -> ModelResponse:
        return await _scripted_response(state_dir, messages)

    async def stream_response(
        messages: list[ModelMessage], info: AgentInfo
    ) -> AsyncIterator[str | DeltaToolCalls]:
        response = await _scripted_response(state_dir, messages)
        calls = [part for part in response.parts if isinstance(part, ToolCallPart)]
        if calls:
            yield {
                index: DeltaToolCall(
                    call.tool_name, call.args_as_json_str(), tool_call_id=call.tool_call_id
                )
                for index, call in enumerate(calls)
            }
            return
        yield "".join(part.content for part in response.parts if isinstance(part, TextPart))

    agent: Agent[None, str] = Agent(
        FunctionModel(respond, stream_function=stream_response), name="connector"
    )

    @agent.tool_plain
    def lookup(query: str) -> str:
        """Look up a document."""
        return f"found {query}"

    return agent


def build_connector_agent_flow(state_dir: Path) -> AgentSessionFlow:
    return pydantic_ai_session_flow(
        build_connector_agent(state_dir),
        flow_type=CONNECTOR_AGENT_FLOW_TYPE,
        tool_policies=CONNECTOR_TOOL_POLICIES,
        tool_sources=[build_connector_source(state_dir), build_withdrawing_source(state_dir)],
    )


def build_connector_agent_flow_from_environment() -> AgentSessionFlow:
    return build_connector_agent_flow(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


async def _scripted_response(state_dir: Path, messages: list[ModelMessage]) -> ModelResponse:
    prompt_index, prompt = _latest_user_prompt(messages)
    hold = None
    if prompt.startswith("hold:"):
        hold_word, prompt = prompt.split(" ", 1)
        hold = hold_word.removeprefix("hold:")
    if not prompt.startswith("calls "):
        return ModelResponse(parts=[TextPart(f"said: {prompt}")])
    rounds = prompt.removeprefix("calls ").split(" then ")
    later = messages[prompt_index + 1 :]
    finished_rounds = sum(
        1
        for message in later
        if isinstance(message, ModelResponse)
        and any(isinstance(part, ToolCallPart) for part in message.parts)
    )
    if finished_rounds < len(rounds):
        requested: list[list[Any]] = json.loads(rounds[finished_rounds])
        return ModelResponse(
            parts=[
                ToolCallPart(
                    request[0],
                    json.dumps(request[1]),
                    scripted_tool_call_id(finished_rounds, index, request),
                )
                for index, request in enumerate(requested)
            ]
        )
    if hold is not None:
        gate = FileGate(state_dir, hold)
        gate.mark_reached()
        await gate.wait_for_release()
    return ModelResponse(parts=[TextPart("\n".join(_tool_results(later)))])


def scripted_tool_call_id(round_index: int, index: int, request: list[Any]) -> str:
    if len(request) > 2:
        return str(request[2])
    return f"call-{index}" if round_index == 0 else f"call-{round_index}-{index}"


class WithdrawingToolSource:
    """A source whose live listing a test empties or breaks, as a dynamic source might."""

    def __init__(self, inner: ConnectorToolSource, state_dir: Path) -> None:
        self._inner = inner
        self._withdrawn_path = state_dir / WITHDRAWN_FILE_NAME

    @property
    def source_name(self) -> str:
        return self._inner.source_name

    async def list_tools(self) -> Sequence[Any]:
        withdrawn = self._withdrawn_path.read_text() if self._withdrawn_path.exists() else ""
        if withdrawn == "raise":
            raise ConnectionError("the source is down")
        return [] if withdrawn == "empty" else await self._inner.list_tools()

    def cached_tools(self) -> Sequence[Any]:
        return self._inner.cached_tools()

    def deferred_tool_call(self, turn: AgentTurnContext, **call: Any) -> DeferredToolCall:
        return self._inner.deferred_tool_call(turn, **call)

    def tool_call_step_options(self, tool_call: AgentToolCall) -> ToolCallStepOptions | None:
        return self._inner.tool_call_step_options(tool_call)

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        return await self._inner.execute_deferred_tool_call(call)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        return await self._inner.poll_tool_task(poll)


def build_withdrawing_source(state_dir: Path) -> WithdrawingToolSource:
    notes = PostNoteOperation(FakeLedgerProvider(state_dir))
    inner = ConnectorToolSource(
        [connector_tool(notes, LEDGER_CONNECTION, name="hiding_note")], source_name="hiding"
    )
    return WithdrawingToolSource(inner, state_dir)


def _latest_user_prompt(messages: list[ModelMessage]) -> tuple[int, str]:
    prompts = [
        (index, part.content)
        for index, message in enumerate(messages)
        if isinstance(message, ModelRequest)
        for part in message.parts
        if isinstance(part, UserPromptPart) and isinstance(part.content, str)
    ]
    return prompts[-1]


def _tool_results(messages: list[ModelMessage]) -> list[str]:
    results: list[str] = []
    for message in messages:
        if not isinstance(message, ModelRequest):
            continue
        for part in message.parts:
            if isinstance(part, ToolReturnPart):
                results.append(f"{part.tool_name}: {part.model_response_str()}")
            elif isinstance(part, RetryPromptPart) and part.tool_name is not None:
                results.append(f"{part.tool_name}: {part.content}")
    return results
