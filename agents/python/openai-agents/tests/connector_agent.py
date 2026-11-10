# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""An OpenAI Agents SDK agent whose tool source offers the fake ledger Connector's operations.

The model reads ``calls <json>`` from the latest user message, a JSON list of
``[tool_name, arguments]`` pairs, and calls them in one response with IDs
``call-0``, ``call-1``, and so on. More lists joined with `` then `` are later
responses, with IDs ``call-<response>-<index>``; a third item in a pair sets
the ID. ``hiding_note`` comes from a second source whose listing a test can
empty or break by writing ``withdrawn`` into the state directory. It calls
only tools the SDK offers to it; any other name makes it answer
``unbound <name>``. Once every response ran it answers one
``<tool_name>: <result>`` line per result of the turn. A ``hold:<gate>`` word
before ``calls`` makes that answer wait at a ``FileGate``, so a test can read
the turn's records before the turn ends.

The agent also has its own function tool, ``lookup``. The session policy for
``policy_note`` claims ``READ`` and asks for approval; only the approval
applies, because the effect class comes from the manifest.
"""

from __future__ import annotations

import json
import os
from collections.abc import AsyncIterator, Mapping, Sequence
from pathlib import Path
from typing import Any

from agents import (
    Agent,
    Handoff,
    Model,
    ModelResponse,
    ModelSettings,
    ModelTracing,
    function_tool,
)
from agents.agent_output import AgentOutputSchemaBase
from agents.items import TResponseInputItem, TResponseOutputItem, TResponseStreamEvent
from agents.testing import ModelStep, ScriptedModel, assistant_message, function_call
from agents.tool import Tool
from openai.types.responses.response_prompt_param import ResponsePromptParam

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
from dex_agents_openai_agents import openai_agents_session_flow
from tests.fake_ledger import (
    LEDGER_CONNECTION,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    CreatePayoutOperation,
    FakeLedgerProvider,
    GetBalanceOperation,
    PostNoteOperation,
)
from tests.scripted_model import model_call_usage

CONNECTOR_AGENT_FLOW_TYPE = "OpenAIAgentsConnectorAgent"
WITHDRAWN_FILE_NAME = "withdrawn"
CONNECTOR_TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "policy_note": ToolPolicy(ToolEffect.READ, requires_approval=True),
}


class ConnectorScriptedModel(Model):
    """Call the tools the user message names, if they are offered, then report their results."""

    def __init__(self, state_dir: Path) -> None:
        self._state_dir = state_dir

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
        step = await self._respond(input, tools)
        return await ScriptedModel([step]).get_response(
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
        step = await self._respond(input, tools)
        async for event in ScriptedModel([step]).stream_response(
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
        ):
            yield event

    async def _respond(self, input: str | list[TResponseInputItem], tools: list[Tool]) -> ModelStep:
        items: list[Mapping[str, Any]] = (
            [{"role": "user", "content": input}]
            if isinstance(input, str)
            else [_as_mapping(item) for item in input]
        )
        prompt_index = max(index for index, item in enumerate(items) if _is_user_message(item))
        prompt = _message_text(items[prompt_index])
        hold = None
        if prompt.startswith("hold:"):
            hold_word, prompt = prompt.split(" ", 1)
            hold = hold_word.removeprefix("hold:")
        later = items[prompt_index + 1 :]
        if not prompt.startswith("calls "):
            return _step([assistant_message(f"said: {prompt}", item_id="answer")])
        rounds = prompt.removeprefix("calls ").split(" then ")
        finished_rounds = sum(
            1
            for index, item in enumerate(later)
            if _is_call(item) and (index == 0 or not _is_call(later[index - 1]))
        )
        if finished_rounds < len(rounds):
            requested: list[list[Any]] = json.loads(rounds[finished_rounds])
            offered = {getattr(tool, "name", "") for tool in tools}
            unbound = [request[0] for request in requested if request[0] not in offered]
            if unbound:
                text = f"unbound {', '.join(unbound)}"
                return _step([assistant_message(text, item_id="answer")])
            return _step(
                [
                    function_call(
                        request[0],
                        json.dumps(request[1]),
                        call_id=scripted_tool_call_id(finished_rounds, index, request),
                    )
                    for index, request in enumerate(requested)
                ]
            )
        if hold is not None:
            gate = FileGate(self._state_dir, hold)
            gate.mark_reached()
            await gate.wait_for_release()
        names = [str(item["name"]) for item in later if _is_call(item)]
        outputs = [
            _output_text(item.get("output"))
            for item in later
            if item.get("type") == "function_call_output"
        ]
        results = [f"{name}: {output}" for name, output in zip(names, outputs, strict=False)]
        return _step([assistant_message("\n".join(results), item_id="answer")])


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


@function_tool
def lookup(query: str) -> str:
    """Look up a document."""
    return f"found {query}"


def build_connector_agent(state_dir: Path) -> Agent[Any]:
    return Agent(
        name="connector",
        instructions="You are a scripted test agent.",
        model=ConnectorScriptedModel(state_dir),
        tools=[lookup],
    )


def build_connector_agent_flow(state_dir: Path) -> AgentSessionFlow:
    return openai_agents_session_flow(
        build_connector_agent(state_dir),
        flow_type=CONNECTOR_AGENT_FLOW_TYPE,
        tool_policies=CONNECTOR_TOOL_POLICIES,
        tool_sources=[build_connector_source(state_dir), build_withdrawing_source(state_dir)],
    )


def build_connector_agent_flow_from_environment() -> AgentSessionFlow:
    return build_connector_agent_flow(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


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


def _step(output: list[TResponseOutputItem]) -> ModelStep:
    return ModelStep(output=output, usage=model_call_usage())


def _as_mapping(item: Any) -> Mapping[str, Any]:
    if isinstance(item, Mapping):
        return item
    dumped: Mapping[str, Any] = item.model_dump(exclude_unset=True)
    return dumped


def _is_user_message(item: Mapping[str, Any]) -> bool:
    return item.get("role") == "user" and item.get("type") in (None, "message")


def _is_call(item: Mapping[str, Any]) -> bool:
    return item.get("type") == "function_call"


def _message_text(item: Mapping[str, Any]) -> str:
    content = item.get("content")
    if isinstance(content, str):
        return content
    return "".join(str(part.get("text", "")) for part in content or [] if isinstance(part, Mapping))


def _output_text(output: Any) -> str:
    return output if isinstance(output, str) else json.dumps(output)
