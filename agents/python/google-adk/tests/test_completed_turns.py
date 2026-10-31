# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns with one model call per RunModel Step: history replay, snapshots, failures."""

from __future__ import annotations

import json

from google.adk.events import Event

from dex_agents import (
    FRAMEWORK_RUN_STATE,
    TURN_FAILURE_NOTE_FOOTER,
    TURN_FAILURE_NOTE_HEADER,
    AgentEventType,
    AgentSessionStatus,
    AgentUsage,
    MessageRole,
)
from tests.conftest import AdkAgentHarness
from tests.scripted_adk_agent import (
    MODEL_CALL_LOG_NAME,
    OUTPUT_TOKENS_PER_CALL,
    PROMPT_TOKENS_PER_CALL,
)


async def test_read_tool_turn_runs_model_tool_and_resume_steps_and_replays_next_turn(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call lookup {"query": "docs"}')
    first = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.tool_name) for message in first.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "lookup"),
        (MessageRole.TOOL, "lookup"),
        (MessageRole.ASSISTANT, None),
    ]
    assert json.loads(first.messages[1].tool_arguments or "") == {"query": "docs"}
    assert first.messages[1].tool_call_id == first.messages[2].tool_call_id
    assert first.messages[2].tool_output == "found docs"
    assert first.messages[-1].text == "lookup: found docs"
    assert first.usage.model_calls == 2
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
    assert harness.invocations.names() == [MODEL_CALL_LOG_NAME, "lookup", MODEL_CALL_LOG_NAME]
    events = [Event.model_validate_json(message.framework_item) for message in first.messages]
    # The stored user message takes the turn's ADK invocation ID when a later turn replays it.
    assert len({event.invocation_id for event in events[1:]}) == 1
    assert [event.author for event in events] == ["user", "assistant", "assistant", "assistant"]

    assert await harness.sessions.send_user_message(session_id, "recall")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == 'history: call lookup {"query": "docs"}'
    assert [message.turn_id for message in second.messages] == ["turn-1"] * 4 + ["turn-2"] * 2
    assert harness.invocations.count("lookup") == 1


async def test_snapshot_returns_the_conversation_after_a_completed_turn(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert await harness.sessions.send_user_message(session_id, "say hi")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert snapshot.state.last_turn_error is None
    assert [(message.sequence, message.role, message.text) for message in snapshot.messages] == [
        (1, MessageRole.USER, "say hi"),
        (2, MessageRole.ASSISTANT, "said: hi"),
    ]
    assert snapshot.usage == AgentUsage(
        model_calls=1,
        input_tokens=PROMPT_TOKENS_PER_CALL,
        output_tokens=OUTPUT_TOKENS_PER_CALL,
    )
    assert snapshot.pending_interrupts == []
    assert snapshot.tool_calls == []
    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    assert FRAMEWORK_RUN_STATE not in values

    text_deltas = [
        event
        for event in await harness.agent_events(session_id)
        if event.type == AgentEventType.TEXT_DELTA
    ]
    assert [json.loads(event.payload)["text"] for event in text_deltas] == ["said", ": hi"]
    assert len({event.step_execution_id for event in text_deltas}) == 1
    assert text_deltas[0].step_execution_id


async def test_failing_model_records_the_turn_error_and_the_session_continues(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "fail")
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "scripted model failure" in (failed.state.last_turn_error or "")
    assert [message.text for message in failed.messages] == ["fail"]

    assert await harness.sessions.send_user_message(session_id, "recall")
    recovered = await harness.wait_for_turns(session_id, 2)
    assert recovered.messages[-1].text == "history: fail"


async def test_failed_resumed_turn_after_a_write_stores_a_note_the_next_turn_replays(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    command = 'call publish {"message": "launch"} then fail'
    await harness.sessions.start_session(session_id, command)
    interrupt = await harness.wait_for_interrupt(session_id, "publish")
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    failed = await harness.wait_for_turns(session_id, 1)

    note = "\n".join(
        [
            TURN_FAILURE_NOTE_HEADER,
            '- publish {"message": "launch"} -> succeeded: published launch',
            TURN_FAILURE_NOTE_FOOTER,
        ]
    )
    assert failed.state is not None
    assert "scripted model failure" in (failed.state.last_turn_error or "")
    assert [(message.role, message.text) for message in failed.messages] == [
        (MessageRole.USER, command),
        (MessageRole.USER, note),
    ]
    assert Event.model_validate_json(failed.messages[1].framework_item).author == "user"
    assert harness.invocations.count("publish") == 1

    assert await harness.sessions.send_user_message(session_id, "recall")
    recovered = await harness.wait_for_turns(session_id, 2)
    assert recovered.messages[-1].text == f"history: {command} | {note}"
    assert harness.invocations.count("publish") == 1
