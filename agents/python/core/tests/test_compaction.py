# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Context compaction: CompactContext, ContextSummary, and pairing, on a real Dex Server."""

from __future__ import annotations

import json
import os
import time
from collections.abc import AsyncIterator
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest
import pytest_asyncio
from dex import CodecRegistry

from dex_agents import (
    AGENT_STATE,
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentEvent,
    AgentEventType,
    AgentMessage,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentSessionStatus,
    AgentSnapshot,
    CompactContextInput,
    CompactionPolicy,
    ContextSummary,
    ContextSummaryRequest,
    MessageRole,
)
from dex_agents.stepwise import (
    CONTEXT_SUMMARY_INSTRUCTIONS,
    CONTEXT_SUMMARY_NOTE_HEADER,
    CONTEXT_SUMMARY_REQUEST,
    AgentDefinition,
    ModelInputItem,
    ModelOutputItem,
    ModelOutputWriter,
    ModelRequest,
    ModelResponse,
    ModelSettings,
    ModelToolCall,
    StepwiseAgentRunner,
    ToolResultItem,
    UserTextItem,
    build_context_summary_omission_note,
    encode_stepwise_message,
)
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    FileGate,
    InvocationLog,
    wait_for_agent_snapshot,
    wait_until,
)
from dex_agents.ui_stream import read_agent_event_stream_snapshot, stream_agent_event_frames
from tests.compaction_agent import (
    FAIL_SUMMARY_FILE_NAME,
    INVOCATION_LOG_NAME,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    SUMMARY_GATE_NAME,
    SUMMARY_PAD_ENVIRONMENT_VARIABLE,
    WINDOW_TOKENS_ENVIRONMENT_VARIABLE,
    build_compaction_agent_flow,
    read_model_requests,
)
from tests.conftest import CORE_DIR
from tests.scripted_agent import ScriptedRunner

WORKER_MODULE = "tests.compaction_worker"
# Each "pad" turn stores about 115 tokens, so the sixth model call crosses 85% of 600.
PAD_TURN_COMMANDS = [f"pad 400 t{turn}" for turn in range(1, 7)]
SUMMARIZED_THROUGH_SEQUENCE = 8
GUARD_SESSION_TURNS = 300
# The engine refuses a run budget below one maximum-sized value, so both shrink together.
GUARD_SESSION_SERVER_CONFIG = (
    "values:\n  maxValueBytes: 262144\nlimits:\n  maxFlowStateBytes: 1048576\n"
)
GUARD_SESSION_RUN_BUDGET_BYTES = 1024 * 1024
# Measured 5,433 bytes per turn (193 turns), queued messages included; the band catches drift.
GUARD_MIN_BYTES_PER_TURN = 4_700
GUARD_MAX_BYTES_PER_TURN = 6_200
# Measured about 2,885 bytes per turn in both cores, as the TypeScript guard checks.
GUARD_MIN_STORED_BYTES_PER_TURN = 2_600
GUARD_MAX_STORED_BYTES_PER_TURN = 3_200
LONG_TESTS_ENVIRONMENT_VARIABLE = "DEX_AGENTS_LONG_TESTS"
LONG_SESSION_TURNS = 2_000
# The engine refuses a run budget below one maximum-sized value, so both shrink together.
LONG_SESSION_SERVER_CONFIG = (
    "values:\n  maxValueBytes: 1048576\nlimits:\n  maxFlowStateBytes: 16777216\n"
)
LONG_SESSION_RUN_BUDGET_BYTES = 16 * 1024 * 1024


@dataclass
class CompactionHarness:
    stack: AgentTestStack
    flow: AgentSessionFlow
    sessions: AgentSessionClient
    state_dir: Path

    def new_session_id(self) -> str:
        return f"session-{uuid4().hex}"

    def model_requests(self, kind: str) -> list[dict[str, Any]]:
        return [entry for entry in read_model_requests(self.state_dir) if entry["kind"] == kind]

    async def executed_step_types(self, session_id: str) -> list[str]:
        history = await self.stack.read_step_history(session_id)
        return [entry.step_type for entry in history if entry.event_type == EXECUTE_COMPLETED_EVENT]

    async def agent_events(self, session_id: str) -> list[AgentEvent]:
        page = await self.stack.client.list_stream_messages(session_id, self.flow.agent_events, 500)
        return [message.value for message in reversed(page.messages)]

    async def event_payloads(self, session_id: str, event_type: str) -> list[dict[str, Any]]:
        return [
            json.loads(event.payload)
            for event in await self.agent_events(session_id)
            if event.type == event_type
        ]

    async def all_messages(self, session_id: str) -> list[AgentMessage]:
        messages: list[AgentMessage] = []
        before: int | None = None
        while True:
            page = await self.sessions.list_agent_messages(
                session_id, before_sequence=before, limit=200
            )
            messages[:0] = page.messages
            if page.next_before_sequence is None:
                return messages
            before = page.next_before_sequence

    async def wait_for_turns(self, session_id: str, turn_count: int) -> AgentSnapshot:
        return await wait_for_agent_snapshot(
            self.sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None
            and snapshot.state.turn_count >= turn_count
            and snapshot.state.active_turn_id is None
            else None,
            description=f"{turn_count} finished turn(s)",
        )

    async def run_turns(self, session_id: str, commands: list[str]) -> AgentSnapshot:
        await self.sessions.start_session(session_id, commands[0])
        for command in commands[1:]:
            assert await self.sessions.send_user_message(session_id, command)
        return await self.wait_for_turns(session_id, len(commands))


@pytest_asyncio.fixture
async def compaction_harness(tmp_path: Path) -> AsyncIterator[CompactionHarness]:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_compaction_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield CompactionHarness(stack, flow, stack.sessions(flow), state_dir)


async def test_crossing_the_threshold_compacts_once_before_the_model_call(
    compaction_harness: CompactionHarness,
) -> None:
    harness = compaction_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    snapshot = await harness.run_turns(session_id, PAD_TURN_COMMANDS)

    # One summary, of the first four turns, ran in the sixth turn before its model call.
    steps = await harness.executed_step_types(session_id)
    assert steps.count("CompactContext") == 1
    # The first RunModel of the turn makes no model call; it moves to CompactContext.
    assert steps[-4:] == ["AwaitUserMessage", "RunModel", "CompactContext", "RunModel"]
    summary = snapshot.context_summary
    assert summary is not None
    assert (summary.generation, summary.first_sequence, summary.last_sequence) == (
        1,
        1,
        SUMMARIZED_THROUGH_SEQUENCE,
    )
    assert summary.text.startswith("commands: pad 400 t1 || pad 400 t2 || pad 400 t3 || pad 400 t4")

    # The model input of that call starts with the summary and leaves the summarized messages out.
    [summary_request] = harness.model_requests("summary")
    assert summary_request["turn_id"] == "turn-6"
    sixth_turn_input = [
        entry["items"] for entry in harness.model_requests("model") if entry["turn_id"] == "turn-6"
    ]
    assert len(sixth_turn_input) == 1
    items = sixth_turn_input[0]
    assert items[0] == ["user", f"{CONTEXT_SUMMARY_NOTE_HEADER}\n{summary.text}"]
    shown_texts = [item[1] for item in items[1:]]
    assert shown_texts[0] == "pad 400 t5"
    assert shown_texts[-1] == "pad 400 t6"
    assert not any(tag in text for text in shown_texts for tag in ("t1", "t2", "t3", "t4"))

    # Every message stays stored and listable.
    messages = await harness.all_messages(session_id)
    assert [message.sequence for message in messages] == list(range(1, 13))
    assert [message.text for message in messages if message.role is MessageRole.USER] == (
        PAD_TURN_COMMANDS
    )
    assert snapshot.messages[-1].text == "t6:" + "x" * 400
    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert snapshot.usage.model_calls == 7

    assert await harness.event_payloads(session_id, AgentEventType.COMPACTION_STARTED) == [
        {"through_sequence": SUMMARIZED_THROUGH_SEQUENCE}
    ]
    assert await harness.event_payloads(session_id, AgentEventType.COMPACTION_FINISHED) == [
        {"generation": 1, "through_sequence": SUMMARIZED_THROUGH_SEQUENCE}
    ]
    compaction_events = [
        event
        for event in await harness.agent_events(session_id)
        if event.step_execution_id.startswith("CompactContext-")
    ]
    assert {event.turn_id for event in compaction_events} == {"turn-6"}
    assert [event.type for event in compaction_events] == [
        AgentEventType.COMPACTION_STARTED,
        AgentEventType.USAGE_RECORDED,
        AgentEventType.COMPACTION_FINISHED,
    ]


async def test_event_frames_and_snapshot_json_carry_compaction_as_plain_data(
    compaction_harness: CompactionHarness,
) -> None:
    harness = compaction_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.run_turns(session_id, PAD_TURN_COMMANDS)

    opening = await read_agent_event_stream_snapshot(harness.stack.client, harness.flow, session_id)
    summary_json = opening.to_json()["snapshot"]["context_summary"]
    assert summary_json["generation"] == 1
    assert summary_json["last_sequence"] == SUMMARIZED_THROUGH_SEQUENCE
    frames = await stream_agent_event_frames(
        harness.stack.client,
        harness.flow,
        session_id,
        resume_token="",
        poll_timeout=timedelta(seconds=1),
    )
    sixth_turn_types: list[str] = []
    try:
        async for frame in frames:
            if not frame.startswith("id:"):
                continue
            data = json.loads(frame.split("data: ", 1)[1])
            if data["turn_id"] == "turn-6":
                sixth_turn_types.append(data["type"])
            if data["turn_id"] == "turn-6" and data["type"] == AgentEventType.TURN_COMPLETED:
                break
    finally:
        await frames.aclose()
    # Compaction streams as ordinary events before the model call, and the turn completes once.
    assert sixth_turn_types == [
        AgentEventType.TURN_STARTED,
        AgentEventType.COMPACTION_STARTED,
        AgentEventType.USAGE_RECORDED,
        AgentEventType.COMPACTION_FINISHED,
        AgentEventType.TEXT_DELTA,
        AgentEventType.USAGE_RECORDED,
        AgentEventType.TURN_COMPLETED,
    ]


async def test_the_plan_note_follows_the_summary_and_is_never_summarized(
    compaction_harness: CompactionHarness,
) -> None:
    harness = compaction_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    snapshot = await harness.run_turns(
        session_id, ["plan Write the report", *PAD_TURN_COMMANDS[1:]]
    )

    assert snapshot.context_summary is not None
    assert snapshot.plan is not None
    plan_note_prefix = "[Dex] The current plan, revision 1, is active:"
    after_summary = [
        entry["items"]
        for entry in harness.model_requests("model")
        if entry["items"][0][1].startswith(CONTEXT_SUMMARY_NOTE_HEADER)
    ]
    assert after_summary
    for items in after_summary:
        plan_notes = [
            index
            for index, item in enumerate(items)
            if item[0] == "user" and item[1].startswith(plan_note_prefix)
        ]
        assert len(plan_notes) == 1
        assert plan_notes[0] > 0
    for entry in harness.model_requests("summary"):
        assert not any(str(item[1]).startswith(plan_note_prefix) for item in entry["items"])


async def test_a_smaller_window_on_a_long_session_summarizes_a_request_that_fits(
    compaction_harness: CompactionHarness,
) -> None:
    harness = compaction_harness
    roomy_worker = await harness.stack.start_worker({WINDOW_TOKENS_ENVIRONMENT_VARIABLE: "100000"})
    session_id = harness.new_session_id()
    commands = [f"pad 400 t{turn}" for turn in range(1, 9)]
    await harness.run_turns(session_id, commands)
    assert not harness.model_requests("summary")

    # A deploy shrinks the window: the range to summarize no longer fits in one request.
    roomy_worker.kill()
    await harness.stack.start_worker()
    assert await harness.sessions.send_user_message(session_id, "pad 400 t9")
    snapshot = await harness.wait_for_turns(session_id, 9)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == "t9:" + "x" * 400
    assert snapshot.context_summary is not None
    [summary_request] = harness.model_requests("summary")
    omission_notes = [
        item[1]
        for item in summary_request["items"]
        if item[0] == "user" and item[1].endswith("did not fit in this request and are left out.")
    ]
    assert len(omission_notes) == 1
    assert omission_notes[0].startswith("[Dex] ")
    assert "pad 400 t1 ||" not in snapshot.context_summary.text


class _TenTokensPerItemModel:
    """Counts 10 tokens per input item and records each request."""

    def __init__(self) -> None:
        self.requests: list[ModelRequest] = []

    async def call_model(self, request: ModelRequest, output: ModelOutputWriter) -> ModelResponse:
        self.requests.append(request)
        return ModelResponse(output_items=[], text="summary")

    def estimate_input_tokens(self, request: ModelRequest) -> int:
        return 10 * len(request.input_items)


class _UnusedEventWriter:
    step_execution_id = "CompactContext-1"


async def _no_heartbeat() -> None:
    return None


async def test_the_summary_cap_leaves_out_whole_model_responses() -> None:
    # The OpenAI Responses API stores a reasoning item and its function call as two messages.
    reasoning = ModelOutputItem(provider="openai", api="responses", item='{"id":"rs_1"}')
    function_call = ModelOutputItem(
        provider="openai",
        api="responses",
        item='{"id":"fc_1"}',
        tool_calls=(ModelToolCall(tool_call_id="c1", tool_name="lookup_order", arguments="{}"),),
    )
    answer = ModelOutputItem(
        provider="openai", api="responses", item='{"id":"msg_1"}', text="Order 1 shipped."
    )
    range_items: list[ModelInputItem] = [
        UserTextItem(text="Look up order 1"),
        reasoning,
        function_call,
        ToolResultItem(tool_call_id="c1", tool_name="lookup_order", output="shipped"),
        answer,
    ]
    messages = [
        AgentMessage(
            sequence=sequence,
            turn_id="turn-1",
            created_at="2026-11-01T12:00:00.000Z",
            **vars(encode_stepwise_message(item)),
        )
        for sequence, item in enumerate(range_items, start=1)
    ]
    model = _TenTokensPerItemModel()

    # 20 tokens for the note and the request text, then 10 per item: 70 in all.
    for budget, max_output_tokens in ((50, None), (60, None), (70, 10)):
        definition = AgentDefinition(
            name="agent",
            instructions="Help.",
            model=model,
            model_settings=ModelSettings(max_output_tokens=max_output_tokens),
        )
        await StepwiseAgentRunner(definition).summarize_context(
            ContextSummaryRequest(
                flow_id="session-1",
                turn_id="turn-1",
                attempt=1,
                previous_summary=None,
                messages=messages,
                through_sequence=5,
                events=_UnusedEventWriter(),  # type: ignore[arg-type]
                heartbeat=_no_heartbeat,
                input_token_budget=budget,
            )
        )
    at_50, at_60, at_70_less_10_output = model.requests
    # Leaving out the user message is not enough, so the whole response and its result go too.
    assert at_50.input_items == [
        UserTextItem(text="[Dex] 4 earlier messages did not fit in this request and are left out."),
        answer,
        UserTextItem(text=CONTEXT_SUMMARY_REQUEST),
    ]
    assert at_60.input_items == [
        UserTextItem(text="[Dex] 1 earlier message did not fit in this request and is left out."),
        *range_items[1:],
        UserTextItem(text=CONTEXT_SUMMARY_REQUEST),
    ]
    # The window also holds the summary, so max_output_tokens comes off the budget.
    assert at_70_less_10_output.input_items == at_60.input_items


async def test_a_turn_with_60_tool_calls_compacts_mid_turn_without_splitting_a_call(
    compaction_harness: CompactionHarness,
) -> None:
    harness = compaction_harness
    await harness.stack.start_worker({WINDOW_TOKENS_ENVIRONMENT_VARIABLE: "2000"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "chain 60")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == "chain done: 60 lookups"
    invocations = InvocationLog(harness.state_dir / INVOCATION_LOG_NAME)
    for step in range(60):
        assert invocations.count(f"lookup_order:{step}") == 1
    finished = await harness.event_payloads(session_id, AgentEventType.COMPACTION_FINISHED)
    assert len(finished) >= 2
    assert snapshot.context_summary is not None
    assert snapshot.context_summary.generation == len(finished)

    # The stored transcript holds exactly one result after every tool call.
    messages = await harness.all_messages(session_id)
    assert [message.sequence for message in messages] == list(range(1, len(messages) + 1))
    call_sequences = {
        message.tool_call_id: message.sequence
        for message in messages
        if message.role is MessageRole.ASSISTANT and message.tool_call_id is not None
    }
    result_sequences: dict[str | None, list[int]] = {}
    for message in messages:
        if message.role is MessageRole.TOOL:
            result_sequences.setdefault(message.tool_call_id, []).append(message.sequence)
    assert len(call_sequences) == 60
    # turn_completed counts every message the turn appended, summarized ones included.
    assert await harness.event_payloads(session_id, AgentEventType.TURN_COMPLETED) == [
        {"message_count": len(messages) - 1}
    ]
    assert set(result_sequences) == set(call_sequences)
    for tool_call_id, sequence in call_sequences.items():
        [result_sequence] = result_sequences[tool_call_id]
        assert result_sequence > sequence

    # Each summary ends at a boundary: no call before it has its result after it.
    for payload in finished:
        through = payload["through_sequence"]
        assert messages[through].role is not MessageRole.TOOL
        for tool_call_id, sequence in call_sequences.items():
            if sequence <= through:
                assert result_sequences[tool_call_id][0] <= through

    # Every model call saw each shown call answered, never with a made-up result.
    for entry in harness.model_requests("model"):
        open_calls: set[str] = set()
        for item in entry["items"]:
            if item[0] == "output":
                open_calls.update(item[2])
            elif item[0] == "result":
                assert item[2] != OUTCOME_UNKNOWN_TOOL_RESULT
                open_calls.remove(item[1])
        assert not open_calls
    steps = await harness.executed_step_types(session_id)
    assert steps.count("CompactContext") == len(finished)


async def test_a_steer_during_mid_turn_compaction_counts_the_summarized_messages(
    compaction_harness: CompactionHarness,
) -> None:
    harness = compaction_harness
    await harness.stack.start_worker({WINDOW_TOKENS_ENVIRONMENT_VARIABLE: "2000"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "chain 60 gate-summary")
    gate = FileGate(harness.state_dir, SUMMARY_GATE_NAME)
    await gate.wait_until_reached()
    assert await harness.sessions.steer_user_message(session_id, text="pad 10 steered")
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == "steered:" + "x" * 10
    [finished, *_] = await harness.event_payloads(session_id, AgentEventType.COMPACTION_FINISHED)
    # The summary covers the turn's user message and some of its tool calls.
    assert finished["through_sequence"] > 2
    # The turn-control check comes first, so no model call follows the summary in the turn.
    requests = [
        (entry["kind"], entry["turn_id"]) for entry in read_model_requests(harness.state_dir)
    ]
    first_summary = requests.index(("summary", "turn-1"))
    assert ("model", "turn-1") not in requests[first_summary:]
    messages = await harness.all_messages(session_id)
    first_turn_messages = [message for message in messages if message.turn_id == "turn-1"]
    # turn_steered counts every message the turn appended, summarized ones included.
    assert await harness.event_payloads(session_id, AgentEventType.TURN_STEERED) == [
        {"steered_message_count": 1, "message_count": len(first_turn_messages) - 1}
    ]


async def test_worker_killed_during_compact_context_reruns_only_the_summary_call(
    compaction_harness: CompactionHarness,
) -> None:
    harness = compaction_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    commands = ["pad 400 gate-summary", *PAD_TURN_COMMANDS[1:]]
    await harness.sessions.start_session(session_id, commands[0])
    for command in commands[1:]:
        assert await harness.sessions.send_user_message(session_id, command)
    gate = FileGate(harness.state_dir, SUMMARY_GATE_NAME)
    await gate.wait_until_reached()
    compacting = await harness.sessions.get_agent_snapshot(session_id)
    assert compacting.state is not None
    assert compacting.state.status is AgentSessionStatus.COMPACTING_CONTEXT
    assert compacting.context_summary is None
    # CompactContext holds no lock, so a message sent meanwhile queues for the next turn.
    assert await harness.sessions.send_user_message(session_id, "pad 10 t7")

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, len(commands) + 1)

    assert gate.reached_count() == 2
    assert len(harness.model_requests("summary")) == 2
    sixth_turn_calls = [
        entry for entry in harness.model_requests("model") if entry["turn_id"] == "turn-6"
    ]
    assert len(sixth_turn_calls) == 1
    assert snapshot.context_summary is not None
    assert snapshot.context_summary.generation == 1
    assert [message.text for message in snapshot.messages[-4:]] == [
        "pad 400 t6",
        "t6:" + "x" * 400,
        "pad 10 t7",
        "t7:" + "x" * 10,
    ]
    assert len(await harness.event_payloads(session_id, AgentEventType.COMPACTION_FINISHED)) == 1
    history = await harness.stack.read_step_history(session_id)
    assert [
        entry.step_type
        for entry in history
        if entry.event_type == EXECUTE_COMPLETED_EVENT and entry.step_type == "CompactContext"
    ] == ["CompactContext"]
    # The usage counts seven model calls and the one committed summary call.
    assert snapshot.usage.model_calls == 8


async def test_a_failing_summary_fails_the_turn_and_the_next_message_compacts_again(
    compaction_harness: CompactionHarness,
) -> None:
    harness = compaction_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    fail_summary = harness.state_dir / FAIL_SUMMARY_FILE_NAME
    fail_summary.touch()
    failed = await harness.run_turns(session_id, PAD_TURN_COMMANDS)

    assert failed.state is not None
    assert "scripted summary failure" in (failed.state.last_turn_error or "")
    assert failed.context_summary is None
    assert len(harness.model_requests("summary")) == 3
    assert not [entry for entry in harness.model_requests("model") if entry["turn_id"] == "turn-6"]
    steps = await harness.executed_step_types(session_id)
    assert steps[-3:] == ["AwaitUserMessage", "RunModel", "RecordTurnFailure"]
    assert "CompactContext" not in steps
    assert failed.messages[-1].text == PAD_TURN_COMMANDS[-1]

    fail_summary.unlink()
    assert await harness.sessions.send_user_message(session_id, "pad 400 t7")
    snapshot = await harness.wait_for_turns(session_id, 7)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.context_summary is not None
    assert snapshot.context_summary.generation == 1
    assert snapshot.messages[-1].text == "t7:" + "x" * 400
    assert (await harness.executed_step_types(session_id))[-4:] == [
        "AwaitUserMessage",
        "RunModel",
        "CompactContext",
        "RunModel",
    ]


async def test_compaction_needs_a_runner_with_the_capability(tmp_path: Path) -> None:
    with pytest.raises(TypeError, match="ContextCompactionRunner"):
        AgentSessionFlow(
            ScriptedRunner(tmp_path),
            flow_type="NoCompaction",
            options=AgentSessionOptions(compaction=CompactionPolicy(context_window_tokens=1000)),
        )
    for invalid in (
        {"context_window_tokens": 0},
        {"context_window_tokens": 1000, "retained_ratio": 0.9},
        {"context_window_tokens": 1000, "trigger_ratio": 1.5},
    ):
        with pytest.raises(ValueError):
            CompactionPolicy(**invalid)


async def test_compaction_values_have_the_shared_wire_format() -> None:
    codecs = CodecRegistry()
    summary = ContextSummary(
        generation=2,
        first_sequence=1,
        last_sequence=41,
        text="the user wants a refund",
        created_at="2026-11-01T12:00:00.000Z",
    )
    # The TypeScript core's wire-compatibility test encodes the same values to the same JSON.
    assert codecs.resolve(ContextSummary).encode(summary).data == (
        '{"created_at":"2026-11-01T12:00:00.000Z","first_sequence":1,"generation":2,'
        '"last_sequence":41,"text":"the user wants a refund"}'
    )
    assert (
        codecs.resolve(CompactContextInput | None)
        .encode(CompactContextInput(through_sequence=41))
        .data
        == '{"through_sequence":41}'
    )
    assert codecs.resolve(CompactContextInput | None).encode(None).data == "null"
    summary_codec = codecs.resolve(ContextSummary)
    assert summary_codec.decode(summary_codec.encode(summary)) == summary
    # Model-visible text, the same literals as in the TypeScript core's test.
    assert CONTEXT_SUMMARY_NOTE_HEADER == "[Dex] Summary of the earlier conversation:"
    assert build_context_summary_omission_note(3) == (
        "[Dex] 3 earlier messages did not fit in this request and are left out."
    )
    assert build_context_summary_omission_note(1) == (
        "[Dex] 1 earlier message did not fit in this request and is left out."
    )
    assert CONTEXT_SUMMARY_REQUEST == (
        "[Dex] Summarize the conversation above. Reply with the summary only."
    )
    assert CONTEXT_SUMMARY_INSTRUCTIONS == (
        "You summarize the earlier part of an agent conversation. The agent continues from "
        "your summary and the messages after it, so keep every fact, decision, user request "
        "and preference, open task, and tool result that it still needs, including what an "
        "earlier summary says. Write plain text."
    )


@dataclass
class PaddedSession:
    """What one storage session left behind: its last state, messages, and status."""

    snapshot: AgentSnapshot
    messages: list[AgentMessage]
    flow_status: str
    elapsed_seconds: float


async def _run_padded_session(
    tmp_path: Path, turns: int, server_config: str, timeout: timedelta
) -> PaddedSession:
    """Queue ``turns`` turns of a 100-character question and a 600-character answer, then wait."""
    config_path = tmp_path / "server-zd.yaml"
    config_path.write_text(server_config)
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_compaction_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={
            STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir),
            WINDOW_TOKENS_ENVIRONMENT_VARIABLE: "8000",
            # A real summary of about 1,000 tokens, so superseded summaries count as they would.
            SUMMARY_PAD_ENVIRONMENT_VARIABLE: "4000",
        },
        server_zd_config=config_path,
    ) as stack:
        await stack.start_worker()
        harness = CompactionHarness(stack, flow, stack.sessions(flow), state_dir)
        session_id = harness.new_session_id()
        started = time.monotonic()
        filler = "q" * 80
        await harness.sessions.start_session(session_id, f"pad 600 turn-0001-{filler}")
        for turn in range(2, turns + 1):
            assert await harness.sessions.send_user_message(
                session_id, f"pad 600 turn-{turn:04d}-{filler}"
            )

        async def read_end() -> str | None:
            # AgentState is small; the snapshot RPC would load every message on each poll.
            flow_status = (await stack.client.describe_flow(session_id)).status.value
            if flow_status != "running":
                return flow_status
            state = (await stack.client.get_attributes(session_id, AGENT_STATE)).get(AGENT_STATE)
            if state is None or state.active_turn_id is not None or state.turn_count < turns:
                return None
            return flow_status

        flow_status = await wait_until(
            f"{turns} finished turns or a closed session",
            read_end,
            timeout=timeout,
            interval=timedelta(seconds=1),
        )
        elapsed = time.monotonic() - started
        return PaddedSession(
            snapshot=await harness.sessions.get_agent_snapshot(session_id),
            messages=await harness.all_messages(session_id),
            flow_status=flow_status,
            elapsed_seconds=elapsed,
        )


async def test_the_run_budget_is_crossed_where_the_guide_says_a_turn_costs_about_5_kb(
    tmp_path: Path,
) -> None:
    """Guards the deployment guide's bound: about 5.2 KB of run budget per turn, compaction on."""
    session = await _run_padded_session(
        tmp_path, GUARD_SESSION_TURNS, GUARD_SESSION_SERVER_CONFIG, timedelta(minutes=5)
    )

    completed_turns = sum(
        1 for message in session.messages if message.role is MessageRole.ASSISTANT
    )
    budget_bytes_per_turn = GUARD_SESSION_RUN_BUDGET_BYTES / completed_turns
    message_codec = CodecRegistry().resolve(AgentMessage)
    stored_bytes_per_turn = (
        sum(len(str(message_codec.encode(message).data)) for message in session.messages)
        / completed_turns
    )
    print(
        "A7-5 guard:",
        json.dumps(
            {
                "completed_turns": completed_turns,
                "budget_bytes_per_turn": round(budget_bytes_per_turn),
                "stored_bytes_per_turn": round(stored_bytes_per_turn),
                "flow_status": session.flow_status,
                "elapsed_seconds": round(session.elapsed_seconds, 1),
            }
        ),
    )
    # The session stopped at the budget, before it ran every queued turn.
    assert completed_turns < GUARD_SESSION_TURNS
    assert session.snapshot.context_summary is not None
    assert GUARD_MIN_BYTES_PER_TURN <= budget_bytes_per_turn <= GUARD_MAX_BYTES_PER_TURN
    assert (
        GUARD_MIN_STORED_BYTES_PER_TURN <= stored_bytes_per_turn <= GUARD_MAX_STORED_BYTES_PER_TURN
    )


@pytest.mark.skipif(
    os.environ.get(LONG_TESTS_ENVIRONMENT_VARIABLE) != "1",
    reason=f"runs for minutes; set {LONG_TESTS_ENVIRONMENT_VARIABLE}=1, as the nightly job does",
)
async def test_a_2000_turn_session_with_compaction_reports_its_storage_against_the_run_budget(
    tmp_path: Path,
) -> None:
    """Calibrates the deployment guide: 100-character questions and 600-character answers."""
    session = await _run_padded_session(
        tmp_path, LONG_SESSION_TURNS, LONG_SESSION_SERVER_CONFIG, timedelta(minutes=25)
    )

    # Every turn completed inside the 16 MiB run budget, so no Step crossed it.
    snapshot = session.snapshot
    assert session.flow_status == "running"
    assert snapshot.state is not None
    assert snapshot.state.turn_count == LONG_SESSION_TURNS
    assert snapshot.state.last_turn_error is None
    assert len(session.messages) == 2 * LONG_SESSION_TURNS
    summary = snapshot.context_summary
    assert summary is not None
    assert summary.generation >= 100
    codecs = CodecRegistry()
    message_codec = codecs.resolve(AgentMessage)
    message_bytes = sum(
        len(str(message_codec.encode(message).data)) for message in session.messages
    )
    summary_bytes = len(str(codecs.resolve(ContextSummary).encode(summary).data))
    print(
        "A7-5 storage:",
        json.dumps(
            {
                "turns": LONG_SESSION_TURNS,
                "run_budget_bytes": LONG_SESSION_RUN_BUDGET_BYTES,
                "stored_message_bytes": message_bytes,
                "stored_message_bytes_per_turn": message_bytes // LONG_SESSION_TURNS,
                "summary_generations": summary.generation,
                "summary_bytes": summary_bytes,
                "model_calls": snapshot.usage.model_calls,
                "elapsed_seconds": round(session.elapsed_seconds, 1),
            }
        ),
    )
    assert message_bytes < LONG_SESSION_RUN_BUDGET_BYTES / 2
