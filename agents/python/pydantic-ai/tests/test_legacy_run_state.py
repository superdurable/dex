# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Runner-level run state and unfinished turns: earlier releases, part types, replays."""

from __future__ import annotations

import json
from collections.abc import AsyncIterator
from pathlib import Path
from typing import cast

from pydantic_ai import (
    Agent,
    DeferredToolRequests,
    ModelMessage,
    ModelMessagesTypeAdapter,
    ModelRequest,
    ModelResponse,
    SystemPromptPart,
    TextPart,
    ToolCallPart,
    ToolReturnPart,
    UserPromptPart,
)
from pydantic_ai.agent import WrapperAgent
from pydantic_ai.messages import ToolSearchCallPart
from pydantic_ai.models.function import AgentInfo, FunctionModel

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TURN_STEERED_NOTE_HEADER,
    AgentEventWriter,
    AgentMessage,
    AgentToolCall,
    AgentTurnContext,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
    TurnCompleted,
)
from dex_agents_pydantic_ai import PydanticAIRunner
from tests.scripted_agent import SYSTEM_PROMPT, build_scripted_agent

COMMAND = 'call lookup {"query": "a"}'
TOOL_CALL_ID = "call-1-0-0"


class _DiscardedEvents:
    def write_text_delta(self, text: str) -> None:
        return None

    def write_reasoning_delta(self, text: str) -> None:
        return None


async def test_a_legacy_run_state_resumes_and_stores_the_turn_once(tmp_path: Path) -> None:
    runner = _runner(tmp_path)

    outcome = await runner.run_model_step(_legacy_turn())

    assert isinstance(outcome, TurnCompleted)
    assert [
        (message.role, message.tool_call_id, message.tool_output, message.text)
        for message in outcome.new_messages
    ] == [
        # The opening request holds the system prompt, so it stays, as before.
        (MessageRole.USER, None, None, None),
        (MessageRole.ASSISTANT, TOOL_CALL_ID, None, None),
        (MessageRole.TOOL, TOOL_CALL_ID, "found a", None),
        (MessageRole.ASSISTANT, None, None, "lookup: found a"),
    ]


async def test_a_legacy_run_state_ends_with_its_messages_and_the_recorded_result(
    tmp_path: Path,
) -> None:
    runner = _runner(tmp_path)
    turn = _legacy_turn()

    messages = runner.build_unfinished_turn_messages(turn)

    assert [(message.role, message.tool_call_id, message.tool_output) for message in messages] == [
        (MessageRole.USER, None, None),
        (MessageRole.ASSISTANT, TOOL_CALL_ID, None),
        (MessageRole.TOOL, TOOL_CALL_ID, "found a"),
    ]
    # A retried Step gets the same messages from the same committed state.
    assert runner.build_unfinished_turn_messages(turn) == messages


async def test_a_tool_call_an_earlier_worker_left_unanswered_replays_an_unknown_outcome() -> None:
    model_inputs: list[list[ModelMessage]] = []

    def respond(messages: list[ModelMessage], info: AgentInfo) -> ModelResponse:
        model_inputs.append(list(messages))
        return ModelResponse(parts=[TextPart("done")])

    async def stream_response(messages: list[ModelMessage], info: AgentInfo) -> AsyncIterator[str]:
        model_inputs.append(list(messages))
        yield "done"

    runner: PydanticAIRunner[None] = PydanticAIRunner(
        Agent(
            FunctionModel(respond, stream_function=stream_response),
            output_type=[str, DeferredToolRequests],
        )
    )
    # A newer Worker stored the call; a 0.6.11 Worker steered the turn with only a note.
    turn = _turn(
        "turn-2",
        [
            _message(1, "turn-1", MessageRole.USER, ModelRequest(parts=[UserPromptPart(COMMAND)])),
            _message(
                2,
                "turn-1",
                MessageRole.ASSISTANT,
                ModelResponse(parts=[ToolCallPart("lookup", '{"query": "a"}', TOOL_CALL_ID)]),
            ),
            _message(
                3,
                "turn-1",
                MessageRole.USER,
                ModelRequest(parts=[UserPromptPart(TURN_STEERED_NOTE_HEADER)]),
            ),
            _message(4, "turn-2", MessageRole.USER, ModelRequest(parts=[UserPromptPart("next")])),
        ],
        framework_run_state=None,
        tool_calls={},
    )

    outcome = await runner.run_model_step(turn)

    assert isinstance(outcome, TurnCompleted)
    assert [
        (part.tool_call_id, part.content, part.outcome, part.metadata)
        for message in model_inputs[0]
        if isinstance(message, ModelRequest)
        for part in message.parts
        if isinstance(part, ToolReturnPart)
    ] == [(TOOL_CALL_ID, OUTCOME_UNKNOWN_TOOL_RESULT, "failed", None)]


async def test_unfinished_turn_results_keep_each_part_type_and_outcome(tmp_path: Path) -> None:
    runner = _runner(tmp_path)
    search_content = {"discovered_tools": [{"name": "notify"}]}
    response = ModelResponse(
        parts=[
            ToolSearchCallPart(
                tool_name="search_tools", args={"queries": ["notify"]}, tool_call_id="search"
            ),
            ToolCallPart("publish", '{"message": "b"}', "publish"),
            ToolCallPart("lookup", '{"query": "a"}', "unrecorded"),
        ]
    )
    turn = _turn(
        "turn-1",
        [
            _message(1, "turn-1", MessageRole.USER, ModelRequest(parts=[UserPromptPart(COMMAND)])),
            _message(2, "turn-1", MessageRole.ASSISTANT, response),
        ],
        framework_run_state=json.dumps(
            {
                "format": "dex_agents_pydantic_ai.pending_tool_calls.v2",
                "tool_call_ids": ["search", "publish"],
                "structured_result_tool_call_ids": ["search"],
            }
        ),
        tool_calls={
            "search": _record("search", "search_tools", ToolCallStatus.SUCCEEDED, search_content),
            "publish": _record("publish", "publish", ToolCallStatus.REJECTED, None),
        },
    )

    messages = runner.build_unfinished_turn_messages(turn)

    assert [message.role for message in messages] == [MessageRole.TOOL]
    (request,) = ModelMessagesTypeAdapter.validate_json(messages[0].framework_item)
    assert isinstance(request, ModelRequest)
    assert [
        (type(part).__name__, part.tool_call_id, part.content, part.outcome, part.timestamp)
        for part in request.parts
        if isinstance(part, ToolReturnPart)
    ] == [
        ("ToolSearchReturnPart", "search", search_content, "success", response.timestamp),
        ("ToolReturnPart", "publish", "not now", "denied", response.timestamp),
        ("ToolReturnPart", "unrecorded", OUTCOME_UNKNOWN_TOOL_RESULT, "failed", response.timestamp),
    ]


def _record(
    tool_call_id: str, tool_name: str, status: ToolCallStatus, output: object
) -> AgentToolCall:
    return AgentToolCall(
        tool_call_id=tool_call_id,
        turn_id="turn-1",
        tool_name=tool_name,
        arguments="{}",
        effect=ToolEffect.READ,
        requires_approval=status is ToolCallStatus.REJECTED,
        status=status,
        output=None if output is None else json.dumps(output),
        rejection_message="not now" if status is ToolCallStatus.REJECTED else None,
    )


def _runner(state_dir: Path) -> PydanticAIRunner[str]:
    return PydanticAIRunner(
        WrapperAgent(build_scripted_agent(state_dir)),
        deps_factory=lambda session_id: f"deps-{session_id}",
    )


def _legacy_turn() -> AgentTurnContext:
    """Return a turn as a 0.6.11 Worker left it: only the user message is stored."""
    turn_messages: list[ModelMessage] = [
        ModelRequest(parts=[SystemPromptPart(SYSTEM_PROMPT), UserPromptPart(COMMAND)]),
        ModelResponse(parts=[ToolCallPart("lookup", '{"query": "a"}', TOOL_CALL_ID)]),
    ]
    legacy_run_state = json.dumps(
        {
            "format": "dex_agents_pydantic_ai.pending_tool_calls.v1",
            "turn_messages": ModelMessagesTypeAdapter.dump_python(turn_messages, mode="json"),
            "tool_call_ids": [TOOL_CALL_ID],
            "structured_result_tool_call_ids": [],
        }
    )
    record = AgentToolCall(
        tool_call_id=TOOL_CALL_ID,
        turn_id="turn-1",
        tool_name="lookup",
        arguments='{"query": "a"}',
        effect=ToolEffect.READ,
        requires_approval=False,
        status=ToolCallStatus.SUCCEEDED,
        output="found a",
    )
    return _turn(
        "turn-1",
        [_message(1, "turn-1", MessageRole.USER, ModelRequest(parts=[UserPromptPart(COMMAND)]))],
        framework_run_state=legacy_run_state,
        tool_calls={TOOL_CALL_ID: record},
    )


def _message(
    sequence: int, turn_id: str, role: MessageRole, model_message: ModelMessage
) -> AgentMessage:
    return AgentMessage(
        sequence=sequence,
        turn_id=turn_id,
        role=role,
        text=next(
            (
                part.content
                for part in model_message.parts
                if isinstance(part, UserPromptPart) and isinstance(part.content, str)
            ),
            None,
        ),
        framework_item=ModelMessagesTypeAdapter.dump_json([model_message]).decode(),
        created_at="2026-11-14T00:00:00Z",
    )


def _turn(
    turn_id: str,
    messages: list[AgentMessage],
    *,
    framework_run_state: str | None,
    tool_calls: dict[str, AgentToolCall],
) -> AgentTurnContext:
    async def heartbeat() -> None:
        return None

    return AgentTurnContext(
        flow_id="session-1",
        turn_id=turn_id,
        attempt=1,
        messages=tuple(messages),
        framework_run_state=framework_run_state,
        tool_calls=tool_calls,
        tool_policies={},
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=cast(AgentEventWriter, _DiscardedEvents()),
        heartbeat=heartbeat,
    )
